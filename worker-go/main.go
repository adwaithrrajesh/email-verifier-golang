package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
}

func main() {
	redisURL := getenv("REDIS_URL", "redis://localhost:6379/0")
	rdb := redis.NewClient(redisOptionsFromURL(redisURL))
	ctx := context.Background()

	limiter := NewLimiter(rdb, 10, 10) // ~10 tokens/sec/domain
	mxCache := NewMXCache(10 * time.Minute)
	cfg := SMTPConfig{Timeout: 5 * time.Second, UseStartTLS: false, RcptsPerSession: 40}

	streamTasks := "verify.tasks"
	streamResults := "verify.results"
	group := "workers"
	consumer := hostname()

	_ = rdb.XGroupCreateMkStream(ctx, streamTasks, group, "0").Err()

	for {
		resp, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    group,
			Consumer: consumer,
			Streams:  []string{streamTasks, ">"},
			Count:    1,
			Block:    5 * time.Second,
		}).Result()
		if err == redis.Nil { continue }
		if err != nil { fmt.Println("XReadGroup:", err); time.Sleep(time.Second); continue }

		for _, str := range resp {
			for _, msg := range str.Messages {
				task := Task{
					JobID:  asString(msg.Values["job_id"]),
					Domain: asString(msg.Values["domain"]),
					Sender: asString(msg.Values["sender"]),
				}
				// emails may be encoded as JSON string
				em := msg.Values["emails"]
				switch v := em.(type) {
				case string:
					_ = json.Unmarshal([]byte(v), &task.Emails)
				case []any:
					for _, it := range v { task.Emails = append(task.Emails, fmt.Sprint(it)) }
				}

				if ok, wait := limiter.Allow(ctx, task.Domain); !ok { time.Sleep(wait) }

				mxs, ok := mxCache.Get(task.Domain)
				if !ok {
					if res, err := ResolveMX(task.Domain); err == nil && len(res) > 0 {
						mxs = res; mxCache.Set(task.Domain, res)
					}
				}
				if len(mxs) == 0 {
					for _, e := range task.Emails {
						emit(rdb, streamResults, Result{task.JobID, e, "invalid", "no MX", "", task.Domain})
					}
					_ = rdb.XAck(ctx, streamTasks, group, msg.ID).Err()
					continue
				}

				mx := mxs[0]
				for i := 0; i < len(task.Emails); i += cfg.RcptsPerSession {
					end := i + cfg.RcptsPerSession; if end > len(task.Emails) { end = len(task.Emails) }
					chunk := task.Emails[i:end]
					resMap := smtpSession(mx, task.Sender, chunk, cfg)
					for _, r := range resMap {
						emit(rdb, streamResults, Result{task.JobID, r.Email, r.Status, r.Message, r.MX, r.Domain})
					}
				}
				_ = rdb.XAck(ctx, streamTasks, group, msg.ID).Err()
			}
		}
	}
}

func emit(rdb *redis.Client, stream string, r Result) {
	_, _ = rdb.XAdd(context.Background(), &redis.XAddArgs{
		Stream: stream,
		Values: map[string]any{
			"job_id":  r.JobID,
			"email":   r.Email,
			"status":  r.Status,
			"message": r.Message,
			"mx":      r.MX,
			"domain":  r.Domain,
		},
	}).Result()
}

func getenv(k, d string) string { if v := os.Getenv(k); v != "" { return v }; return d }
func hostname() string          { h, _ := os.Hostname(); return h }

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
