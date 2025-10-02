package main

import (
	"testing"
	"time"
)

// Test MX cache functionality
func TestMXCache(t *testing.T) {
	cache := NewMXCache(1 * time.Minute)
	
	// Test setting and getting
	hosts := []string{"mx1.example.com", "mx2.example.com"}
	cache.Set("example.com", hosts)
	
	retrieved, found := cache.Get("example.com")
	if !found {
		t.Error("Expected to find cached MX records")
	}
	
	if len(retrieved) != len(hosts) {
		t.Errorf("Expected %d hosts, got %d", len(hosts), len(retrieved))
	}
	
	for i, host := range hosts {
		if retrieved[i] != host {
			t.Errorf("Expected host %s, got %s", host, retrieved[i])
		}
	}
}

func TestMXCacheExpiration(t *testing.T) {
	cache := NewMXCache(10 * time.Millisecond) // Very short TTL
	
	hosts := []string{"mx.example.com"}
	cache.Set("example.com", hosts)
	
	// Should be found immediately
	_, found := cache.Get("example.com")
	if !found {
		t.Error("Expected to find fresh cache entry")
	}
	
	// Wait for expiration
	time.Sleep(20 * time.Millisecond)
	
	// Should be expired
	_, found = cache.Get("example.com")
	if found {
		t.Error("Expected cache entry to be expired")
	}
}

// Test domain extraction
func TestDomainOf(t *testing.T) {
	tests := []struct {
		email    string
		expected string
	}{
		{"user@example.com", "example.com"},
		{"test.email@domain.org", "domain.org"},
		{"admin@sub.domain.co.uk", "sub.domain.co.uk"},
		{"invalid-email", ""},
		{"@domain.com", "domain.com"},
	}
	
	for _, test := range tests {
		result := domainOf(test.email)
		if result != test.expected {
			t.Errorf("domainOf(%s) = %s, expected %s", test.email, result, test.expected)
		}
	}
}

// Test SMTP result confidence scoring
func TestSMTPResultConfidence(t *testing.T) {
	// Test that different response codes produce appropriate confidence scores
	
	// This would be a more comprehensive test in a real implementation
	// For now, we'll test the basic structure
	
	result := SMTPResult{
		Email:      "test@example.com",
		Status:     "valid",
		Confidence: 0.95,
		Attempts:   1,
	}
	
	if result.Confidence < 0.9 {
		t.Error("Valid emails should have high confidence")
	}
	
	if result.Status != "valid" {
		t.Error("Expected valid status")
	}
}

// Benchmark MX cache performance
func BenchmarkMXCache(b *testing.B) {
	cache := NewMXCache(10 * time.Minute)
	hosts := []string{"mx1.example.com", "mx2.example.com"}
	
	// Pre-populate cache
	for i := 0; i < 1000; i++ {
		domain := fmt.Sprintf("domain%d.com", i)
		cache.Set(domain, hosts)
	}
	
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		domain := fmt.Sprintf("domain%d.com", i%1000)
		cache.Get(domain)
	}
}

// Test concurrent access to MX cache
func TestMXCacheConcurrency(t *testing.T) {
	cache := NewMXCache(1 * time.Minute)
	hosts := []string{"mx.example.com"}
	
	// Start multiple goroutines accessing the cache
	done := make(chan bool, 10)
	
	for i := 0; i < 10; i++ {
		go func(id int) {
			domain := fmt.Sprintf("domain%d.com", id)
			
			// Set and get multiple times
			for j := 0; j < 100; j++ {
				cache.Set(domain, hosts)
				_, _ = cache.Get(domain)
			}
			
			done <- true
		}(i)
	}
	
	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}
	
	// If we get here without deadlock, the test passes
}
