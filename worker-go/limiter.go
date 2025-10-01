package main

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

type Limiter struct {
	rdb   *redis.Client
	rate  int64 // tokens/sec (unused in this simple sketch)
	burst int64
}

func NewLimiter(rdb *redis.Client, rate, burst int64) *Limiter {
	return &Limiter{rdb: rdb, rate: rate, burst: burst}
}

// Simple per-domain token bucket approximation using a 1s window.
// For production, use a Lua script to do exact math atomically.
func (l *Limiter) Allow(ctx context.Context, domain string) (bool, time.Duration) {
	key := "limiter:" + domain
	pipe := l.rdb.TxPipeline()
	incr := pipe.IncrBy(ctx, key, -1) // consume one token
	pipe.Expire(ctx, key, time.Second)
	_, _ = pipe.Exec(ctx)

	if incr.Val() >= 0 {
		return true, 0
	}
	return false, time.Second
}
