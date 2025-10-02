package main

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/textproto"
	"strings"
	"sync"
	"time"
)

type SMTPConfig struct {
	Timeout         time.Duration
	UseStartTLS     bool
	RcptsPerSession int
	MaxRetries      int
	RetryDelay      time.Duration
	ConnectTimeout  time.Duration
	ReadTimeout     time.Duration
}

type SMTPResult struct {
	Email, Status, Message, MX, Domain string
	Confidence                          float64
	Attempts                           int
}

// Connection pool for SMTP connections
type SMTPPool struct {
	mu    sync.RWMutex
	conns map[string]*pooledConn
}

type pooledConn struct {
	conn     net.Conn
	tp       *textproto.Conn
	bw       *bufio.Writer
	lastUsed time.Time
	mx       string
}

var smtpPool = &SMTPPool{conns: make(map[string]*pooledConn)}

// Get or create a pooled connection
func (p *SMTPPool) getConn(mx string, cfg SMTPConfig) (*pooledConn, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Check for existing connection
	if pc, exists := p.conns[mx]; exists {
		if time.Since(pc.lastUsed) < 5*time.Minute {
			pc.lastUsed = time.Now()
			return pc, nil
		}
		// Connection too old, close it
		pc.conn.Close()
		delete(p.conns, mx)
	}

	// Create new connection
	d := net.Dialer{Timeout: cfg.ConnectTimeout}
	conn, err := d.Dial("tcp", net.JoinHostPort(mx, "25"))
	if err != nil {
		return nil, fmt.Errorf("connect failed: %w", err)
	}

	// Set read/write timeouts
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(30 * time.Second)
	}

	bw := bufio.NewWriter(conn)
	tp := textproto.NewConn(conn)

	pc := &pooledConn{
		conn:     conn,
		tp:       tp,
		bw:       bw,
		lastUsed: time.Now(),
		mx:       mx,
	}

	p.conns[mx] = pc
	return pc, nil
}

func (p *SMTPPool) releaseConn(mx string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if pc, exists := p.conns[mx]; exists {
		pc.lastUsed = time.Now()
	}
}

func smtpSession(mx, sender string, recipients []string, cfg SMTPConfig) map[string]SMTPResult {
	out := make(map[string]SMTPResult)
	
	for _, email := range recipients {
		out[email] = smtpVerifyWithRetry(mx, sender, email, cfg)
	}
	
	return out
}

func smtpVerifyWithRetry(mx, sender, email string, cfg SMTPConfig) SMTPResult {
	var result SMTPResult
	
	for attempt := 1; attempt <= cfg.MaxRetries; attempt++ {
		result = smtpVerifySingle(mx, sender, email, cfg)
		result.Attempts = attempt
		
		// Success or permanent failure - don't retry
		if result.Status == "valid" || result.Status == "invalid" {
			return result
		}
		
		// Temporary failure - retry with backoff
		if attempt < cfg.MaxRetries {
			backoffDelay := cfg.RetryDelay * time.Duration(attempt)
			log.Printf("SMTP retry %d/%d for %s after %v: %s", attempt, cfg.MaxRetries, email, backoffDelay, result.Message)
			time.Sleep(backoffDelay)
		}
	}
	
	// All retries exhausted
	if result.Status == "unknown" {
		result.Message = fmt.Sprintf("max retries exceeded: %s", result.Message)
		result.Confidence = 0.1
	}
	
	return result
}

func smtpVerifySingle(mx, sender, email string, cfg SMTPConfig) SMTPResult {
	result := SMTPResult{
		Email:      email,
		MX:         mx,
		Domain:     domainOf(email),
		Status:     "unknown",
		Confidence: 0.5,
		Attempts:   1,
	}

	pc, err := smtpPool.getConn(mx, cfg)
	if err != nil {
		result.Message = err.Error()
		result.Confidence = 0.1
		return result
	}
	defer smtpPool.releaseConn(mx)

	// Set timeouts for this session
	deadline := time.Now().Add(cfg.ReadTimeout)
	pc.conn.SetDeadline(deadline)

	// Server banner
	_, _, err = pc.tp.ReadResponse(220)
	if err != nil {
		result.Message = fmt.Sprintf("banner read failed: %v", err)
		return result
	}

	// EHLO
	fmt.Fprintf(pc.bw, "EHLO mail-verifier.local\r\n")
	pc.bw.Flush()
	code, msg, err := pc.tp.ReadResponse(250)
	if err != nil {
		result.Message = fmt.Sprintf("EHLO failed: %v", err)
		return result
	}
	
	extensions := strings.ToLower(msg)
	supportsStartTLS := strings.Contains(extensions, "starttls")

	// STARTTLS if supported and enabled
	if cfg.UseStartTLS && supportsStartTLS {
		fmt.Fprintf(pc.bw, "STARTTLS\r\n")
		pc.bw.Flush()
		_, _, err := pc.tp.ReadResponse(220)
		if err != nil {
			result.Message = fmt.Sprintf("STARTTLS failed: %v", err)
			return result
		}

		// Upgrade to TLS
		tlsConn := tls.Client(pc.conn, &tls.Config{
			ServerName:         mx,
			InsecureSkipVerify: true, // For testing - should be configurable
		})
		
		if err := tlsConn.Handshake(); err != nil {
			result.Message = fmt.Sprintf("TLS handshake failed: %v", err)
			return result
		}

		// Update connection objects
		pc.conn = tlsConn
		pc.tp = textproto.NewConn(tlsConn)
		pc.bw = bufio.NewWriter(tlsConn)

		// EHLO again after TLS
		fmt.Fprintf(pc.bw, "EHLO mail-verifier.local\r\n")
		pc.bw.Flush()
		_, _, err = pc.tp.ReadResponse(250)
		if err != nil {
			result.Message = fmt.Sprintf("EHLO after TLS failed: %v", err)
			return result
		}
	}

	// MAIL FROM
	fmt.Fprintf(pc.bw, "MAIL FROM:<%s>\r\n", sender)
	pc.bw.Flush()
	code, msg, err = pc.tp.ReadResponse(250)
	if err != nil {
		result.Message = fmt.Sprintf("MAIL FROM failed: %d %s", code, msg)
		if code >= 500 && code < 600 {
			result.Status = "invalid"
			result.Confidence = 0.9
		}
		return result
	}

	// RCPT TO - the main verification
	fmt.Fprintf(pc.bw, "RCPT TO:<%s>\r\n", email)
	pc.bw.Flush()
	code, msg, err = pc.tp.ReadResponse(250)
	
	// Parse response and determine status
	result.Message = fmt.Sprintf("%d %s", code, msg)
	
	if err == nil && code >= 200 && code < 300 {
		// Success
		result.Status = "valid"
		result.Confidence = 0.95
	} else if code >= 400 && code < 500 {
		// Temporary failure - greylisting, rate limiting, etc.
		result.Status = "unknown"
		result.Confidence = 0.3
		if strings.Contains(strings.ToLower(msg), "greylist") {
			result.Message = fmt.Sprintf("greylisted: %s", msg)
		}
	} else if code >= 500 && code < 600 {
		// Permanent failure
		result.Status = "invalid"
		result.Confidence = 0.9
		
		// Check for specific error types
		msgLower := strings.ToLower(msg)
		if strings.Contains(msgLower, "user unknown") || 
		   strings.Contains(msgLower, "no such user") ||
		   strings.Contains(msgLower, "recipient unknown") {
			result.Confidence = 0.95
		}
	} else {
		// Network error or unexpected response
		result.Status = "unknown"
		result.Confidence = 0.1
		if err != nil {
			result.Message = fmt.Sprintf("network error: %v", err)
		}
	}

	// QUIT (best practice)
	fmt.Fprintf(pc.bw, "QUIT\r\n")
	pc.bw.Flush()
	pc.tp.ReadResponse(221) // Ignore errors on QUIT

	return result
}

func domainOf(email string) string {
	parts := strings.Split(email, "@")
	if len(parts) == 2 {
		return parts[1]
	}
	return ""
}
