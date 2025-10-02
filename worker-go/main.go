package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

type Task struct {
	JobID  string   `redis:"job_id" json:"job_id"`
	Domain string   `redis:"domain" json:"domain"`
	Emails []string `redis:"emails" json:"emails"`
	Sender string   `redis:"sender" json:"sender"`
}

type Result struct {
	JobID, Email, Status, Message, MX, Domain string
	Confidence                                 float64
	Attempts                                  int
}

type Config struct {
	RedisURL         string
	RateLimit        int64
	RateBurst        int64
	MXCacheTTL       time.Duration
	SMTPTimeout      time.Duration
	SMTPUseStartTLS  bool
	SMTPMaxRetries   int
	SMTPRetryDelay   time.Duration
	SMTPConnTimeout  time.Duration
	SMTPReadTimeout  time.Duration
	RcptsPerSession  int
	WorkerConcurrency int
}

func loadConfig() Config {
	return Config{
		RedisURL:          getenv("REDIS_URL", "redis://localhost:6379/0"),
		RateLimit:         getenvInt("RATE_LIMIT_PER_DOMAIN", 10),
		RateBurst:         getenvInt("RATE_BURST_PER_DOMAIN", 10),
		MXCacheTTL:        getenvDuration("MX_CACHE_TTL", 10*time.Minute),
		SMTPTimeout:       getenvDuration("SMTP_TIMEOUT", 15*time.Second),
		SMTPUseStartTLS:   getenvBool("SMTP_USE_STARTTLS", true),
		SMTPMaxRetries:    int(getenvInt("SMTP_MAX_RETRIES", 3)),
		SMTPRetryDelay:    getenvDuration("SMTP_RETRY_DELAY", 2*time.Second),
		SMTPConnTimeout:   getenvDuration("SMTP_CONNECT_TIMEOUT", 10*time.Second),
		SMTPReadTimeout:   getenvDuration("SMTP_READ_TIMEOUT", 15*time.Second),
		RcptsPerSession:   int(getenvInt("RCPTS_PER_SESSION", 40)),
		WorkerConcurrency: int(getenvInt("WORKER_CONCURRENCY", 5)),
	}
}

func main() {
	config := loadConfig()
	
	log.Printf("Starting email verification worker with config: %+v", config)
	
	rdb := redis.NewClient(redisOptionsFromURL(config.RedisURL))
	ctx := context.Background()

	// Test Redis connection
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}

	limiter := NewLimiter(rdb, config.RateLimit, config.RateBurst)
	mxCache := NewMXCache(config.MXCacheTTL)
	cfg := SMTPConfig{
		Timeout:         config.SMTPTimeout,
		UseStartTLS:     config.SMTPUseStartTLS,
		RcptsPerSession: config.RcptsPerSession,
		MaxRetries:      config.SMTPMaxRetries,
		RetryDelay:      config.SMTPRetryDelay,
		ConnectTimeout:  config.SMTPConnTimeout,
		ReadTimeout:     config.SMTPReadTimeout,
	}

	streamTasks := "verify.tasks"
	streamResults := "verify.results"
	group := "workers"
	consumer := hostname()

	_ = rdb.XGroupCreateMkStream(ctx, streamTasks, group, "0").Err()

	// Create worker pool
	taskChan := make(chan redis.XMessage, config.WorkerConcurrency*2)
	var wg sync.WaitGroup

	// Start worker goroutines
	for i := 0; i < config.WorkerConcurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			log.Printf("Worker %d started", workerID)
			
			for msg := range taskChan {
				processTask(ctx, rdb, msg, limiter, mxCache, cfg, streamTasks, streamResults, group)
			}
			
			log.Printf("Worker %d stopped", workerID)
		}(i)
	}

	log.Printf("Started %d worker goroutines", config.WorkerConcurrency)

	// Main loop - fetch tasks and distribute to workers
	for {
		resp, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    group,
			Consumer: consumer,
			Streams:  []string{streamTasks, ">"},
			Count:    int64(config.WorkerConcurrency),
			Block:    5 * time.Second,
		}).Result()
		
		if err == redis.Nil { 
			continue 
		}
		if err != nil { 
			log.Printf("XReadGroup error: %v", err)
			time.Sleep(time.Second)
			continue 
		}

		for _, str := range resp {
			for _, msg := range str.Messages {
				select {
				case taskChan <- msg:
					// Task sent to worker
				case <-ctx.Done():
					log.Println("Context cancelled, shutting down")
					close(taskChan)
					wg.Wait()
					return
				}
			}
		}
	}
}

func processTask(ctx context.Context, rdb *redis.Client, msg redis.XMessage, limiter *Limiter, mxCache *MXCache, cfg SMTPConfig, streamTasks, streamResults, group string) {
	task := Task{
		JobID:  asString(msg.Values["job_id"]),
		Domain: asString(msg.Values["domain"]),
		Sender: asString(msg.Values["sender"]),
	}
	
	// Parse emails from message
	em := msg.Values["emails"]
	switch v := em.(type) {
	case string:
		_ = json.Unmarshal([]byte(v), &task.Emails)
	case []any:
		for _, it := range v { 
			task.Emails = append(task.Emails, fmt.Sprint(it)) 
		}
	}

	// Rate limiting
	if ok, wait := limiter.Allow(ctx, task.Domain); !ok { 
		time.Sleep(wait) 
	}

	// MX lookup with caching
	mxs, ok := mxCache.Get(task.Domain)
	if !ok {
		if res, err := ResolveMX(task.Domain); err == nil && len(res) > 0 {
			mxs = res
			mxCache.Set(task.Domain, res)
		}
	}
	
	if len(mxs) == 0 {
		// No MX records found
		for _, e := range task.Emails {
			emit(rdb, streamResults, Result{
				JobID: task.JobID, Email: e, Status: "invalid", 
				Message: "no MX", MX: "", Domain: task.Domain,
				Confidence: 0.95, Attempts: 1,
			})
		}
		_ = rdb.XAck(ctx, streamTasks, group, msg.ID).Err()
		return
	}

	// Try multiple MX records in priority order
	for _, mx := range mxs {
		// Process emails in chunks
		for i := 0; i < len(task.Emails); i += cfg.RcptsPerSession {
			end := i + cfg.RcptsPerSession
			if end > len(task.Emails) { 
				end = len(task.Emails) 
			}
			chunk := task.Emails[i:end]
			
			resMap := smtpSession(mx, task.Sender, chunk, cfg)
			
			// Check if we got successful results
			hasSuccess := false
			for _, r := range resMap {
				emit(rdb, streamResults, Result{
					JobID: task.JobID, Email: r.Email, Status: r.Status, 
					Message: r.Message, MX: r.MX, Domain: r.Domain,
					Confidence: r.Confidence, Attempts: r.Attempts,
				})
				
				if r.Status == "valid" || r.Status == "invalid" {
					hasSuccess = true
				}
			}
			
			// If we got definitive results, don't try other MX records
			if hasSuccess {
				break
			}
		}
		
		// If first MX worked, don't try others
		break
	}
	
	_ = rdb.XAck(ctx, streamTasks, group, msg.ID).Err()
}

func emit(rdb *redis.Client, stream string, r Result) {
	_, _ = rdb.XAdd(context.Background(), &redis.XAddArgs{
		Stream: stream,
		Values: map[string]any{
			"job_id":     r.JobID,
			"email":      r.Email,
			"status":     r.Status,
			"message":    r.Message,
			"mx":         r.MX,
			"domain":     r.Domain,
			"confidence": r.Confidence,
			"attempts":   r.Attempts,
		},
	}).Result()
}

func getenv(k, d string) string { 
	if v := os.Getenv(k); v != "" { 
		return v 
	}
	return d 
}

func getenvInt(k string, d int64) int64 {
	if v := os.Getenv(k); v != "" {
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return i
		}
	}
	return d
}

func getenvBool(k string, d bool) bool {
	if v := os.Getenv(k); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return d
}

func getenvDuration(k string, d time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if dur, err := time.ParseDuration(v); err == nil {
			return dur
		}
	}
	return d
}

func hostname() string { 
	h, _ := os.Hostname()
	return h 
}

func asString(v any) string {
	switch t := v.(type) {
	case string: return t
	default: return fmt.Sprint(v)
	}
}

// tiny URL parser for redis:
func redisOptionsFromURL(url string) *redis.Options {
	opt, _ := redis.ParseURL(url); return opt
}
