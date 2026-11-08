package mailer

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// smtpClient sends one message with STARTTLS, or implicit TLS on port 465.
type smtpClient struct {
	host     string
	port     int
	user     string
	password string
}

func (s smtpClient) Send(ctx context.Context, from string, to []string, raw []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.port == 0 {
		s.port = 587
	}
	addr := net.JoinHostPort(s.host, strconv.Itoa(s.port))
	dialer := &net.Dialer{Timeout: 20 * time.Second}
	var (
		conn net.Conn
		err  error
	)
	if s.port == 465 {
		tlsDial := &tls.Dialer{
			NetDialer: dialer,
			Config:    &tls.Config{ServerName: s.host, MinVersion: tls.VersionTLS12},
		}
		conn, err = tlsDial.DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("соединение: %w", err)
	}
	defer conn.Close()
	deadline := time.Now().Add(25 * time.Second)
	if requested, ok := ctx.Deadline(); ok && requested.Before(deadline) {
		deadline = requested
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	client, err := smtp.NewClient(conn, s.host)
	if err != nil {
		return fmt.Errorf("клиент smtp: %w", err)
	}
	defer client.Close()
	if s.port != 465 {
		if ok, _ := client.Extension("STARTTLS"); ok {
			tlsCfg := &tls.Config{ServerName: s.host, MinVersion: tls.VersionTLS12}
			if err := client.StartTLS(tlsCfg); err != nil {
				return fmt.Errorf("starttls: %w", err)
			}
		} else if s.user != "" && !localSMTP(s.host) {
			return errors.New("сервер не предлагает STARTTLS")
		}
	}
	if s.user != "" {
		auth := smtp.PlainAuth("", s.user, s.password, s.host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("авторизация: %w", err)
		}
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("отправитель: %w", err)
	}
	for _, rcpt := range to {
		if err := client.Rcpt(rcpt); err != nil {
			return fmt.Errorf("получатель %s: %w", rcpt, err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("данные: %w", err)
	}
	if _, err := w.Write(raw); err != nil {
		_ = w.Close()
		return fmt.Errorf("запись: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("завершение: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("quit: %w", err)
	}
	return nil
}

func localSMTP(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}
