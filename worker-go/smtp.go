package main

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"net/textproto"
	"strings"
	"time"
)

type SMTPConfig struct {
	Timeout         time.Duration
	UseStartTLS     bool
	RcptsPerSession int
}

type SMTPResult struct {
	Email, Status, Message, MX, Domain string
}

func smtpSession(mx, sender string, recipients []string, cfg SMTPConfig) map[string]SMTPResult {
	out := map[string]SMTPResult{}

	d := net.Dialer{Timeout: cfg.Timeout}
	conn, err := d.Dial("tcp", net.JoinHostPort(mx, "25"))
	if err != nil {
		for _, r := range recipients {
			out[r] = SMTPResult{Email: r, Status: "unknown", Message: "connect failed: " + err.Error(), MX: mx, Domain: domainOf(r)}
		}
		return out
	}
	defer conn.Close()

	// We’ll use textproto for reads and bufio.Writer for writes (no need for a bufio.Reader)
	bw := bufio.NewWriter(conn)
	tp := textproto.NewConn(conn)
	defer tp.Close()

	// Server banner
	_, _, _ = tp.ReadResponse(220)

	// EHLO
	fmt.Fprintf(bw, "EHLO example.com\r\n")
	bw.Flush()
	_, msg, _ := tp.ReadResponse(250)
	ext := strings.ToLower(msg)

	// Optional STARTTLS
	if cfg.UseStartTLS && strings.Contains(ext, "starttls") {
		fmt.Fprintf(bw, "STARTTLS\r\n")
		bw.Flush()
		_, _, _ = tp.ReadResponse(220)
		tlsConn := tls.Client(conn, &tls.Config{ServerName: mx, InsecureSkipVerify: true})
		if err := tlsConn.Handshake(); err == nil {
			conn = tlsConn
			tp = textproto.NewConn(conn)
			defer tp.Close()
			bw = bufio.NewWriter(conn)

			// EHLO again after TLS
			fmt.Fprintf(bw, "EHLO example.com\r\n")
			bw.Flush()
			_, _, _ = tp.ReadResponse(250)
		}
	}

	// MAIL FROM once
	fmt.Fprintf(bw, "MAIL FROM:<%s>\r\n", sender)
	bw.Flush()
	_, _, _ = tp.ReadResponse(250)

	// PIPELINE RCPTs (write all first)
	for _, rcpt := range recipients {
		fmt.Fprintf(bw, "RCPT TO:<%s>\r\n", rcpt)
	}
	bw.Flush()

	// Read one response per RCPT (in order)
	for _, rcpt := range recipients {
		code, message, err := tp.ReadResponse(250)
		res := SMTPResult{Email: rcpt, MX: mx, Domain: domainOf(rcpt)}
		if err != nil {
			switch {
			case code >= 400 && code < 500:
				res.Status = "unknown"
			case code == 0:
				res.Status = "unknown"
			default:
				res.Status = "invalid"
			}
			res.Message = fmt.Sprintf("%d %s", code, message)
		} else {
			res.Status = "valid"
			res.Message = message
		}
		out[rcpt] = res
	}

	// QUIT (optional)
	fmt.Fprintf(bw, "QUIT\r\n")
	bw.Flush()
	return out
}

func domainOf(email string) string {
	parts := strings.Split(email, "@")
	if len(parts) == 2 {
		return parts[1]
	}
	return ""
}
