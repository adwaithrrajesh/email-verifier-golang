package main

import (
	"net"
	"strings"
	"sync"
	"time"
)

type MXCache struct {
	mu   sync.RWMutex
	data map[string]mxEntry
	ttl  time.Duration
}
type mxEntry struct {
	hosts []string
	exp   time.Time
}

func NewMXCache(ttl time.Duration) *MXCache {
	return &MXCache{data: map[string]mxEntry{}, ttl: ttl}
}
func (c *MXCache) Get(domain string) ([]string, bool) {
	c.mu.RLock(); e, ok := c.data[domain]; c.mu.RUnlock()
	return e.hosts, ok && time.Now().Before(e.exp)
}
func (c *MXCache) Set(domain string, hosts []string) {
	c.mu.Lock(); c.data[domain] = mxEntry{hosts: hosts, exp: time.Now().Add(c.ttl)}; c.mu.Unlock()
}
func ResolveMX(domain string) ([]string, error) {
	recs, err := net.LookupMX(domain)
	if err != nil || len(recs) == 0 { return nil, err }
	hosts := make([]string, len(recs))
	for i, r := range recs { hosts[i] = strings.TrimSuffix(r.Host, ".") }
	return hosts, nil
}
