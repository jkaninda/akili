package notification

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
	"os"
	"strconv"
	"strings"

	"github.com/jkaninda/akili/internal/domain"
)

// SMTPConfig holds SMTP connection parameters.
// The SMTP password is resolved per-channel from CredentialRef (environment variable).
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	From     string
	TLS      bool
}

// EmailSender sends notifications via SMTP.
type EmailSender struct {
	config SMTPConfig
	logger *slog.Logger
}

// NewEmailSender creates an SMTP-based email sender.
func NewEmailSender(cfg SMTPConfig, logger *slog.Logger) *EmailSender {
	if cfg.Port == 0 {
		cfg.Port = 587
	}
	return &EmailSender{config: cfg, logger: logger}
}

func (s *EmailSender) Type() string { return "email" }

func (s *EmailSender) Send(_ context.Context, ch *domain.NotificationChannel, msg *Message) error {
	to := ch.Config["to"]
	if to == "" {
		return fmt.Errorf("email channel %q missing 'to' in config", ch.Name)
	}

	// Resolve SMTP password from CredentialRef (treated as env var name).
	password := ""
	if ch.CredentialRef != "" {
		password = os.Getenv(ch.CredentialRef)
	}

	recipients := strings.Split(to, ",")
	for i := range recipients {
		recipients[i] = strings.TrimSpace(recipients[i])
	}

	subject := msg.Subject
	if subject == "" {
		subject = "[Akili] Notification"
	}

	body := buildEmailBody(s.config.From, recipients, subject, msg.Body)

	addr := net.JoinHostPort(s.config.Host, strconv.Itoa(s.config.Port))

	var auth smtp.Auth
	if s.config.Username != "" && password != "" {
		auth = smtp.PlainAuth("", s.config.Username, password, s.config.Host)
	}

	if s.config.TLS {
		return s.sendTLS(addr, auth, s.config.From, recipients, body)
	}
	return smtp.SendMail(addr, auth, s.config.From, recipients, body)
}

func (s *EmailSender) sendTLS(addr string, auth smtp.Auth, from string, to []string, body []byte) error {
	tlsConfig := &tls.Config{
		ServerName: s.config.Host,
		MinVersion: tls.VersionTLS12,
	}

	conn, err := tls.Dial("tcp", addr, tlsConfig)
	if err != nil {
		return fmt.Errorf("tls dial %s: %w", addr, err)
	}

	client, err := smtp.NewClient(conn, s.config.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp client: %w", err)
	}
	defer client.Close()

	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	if err := client.Mail(from); err != nil {
		return fmt.Errorf("smtp MAIL FROM: %w", err)
	}
	for _, addr := range to {
		if err := client.Rcpt(addr); err != nil {
			return fmt.Errorf("smtp RCPT TO %s: %w", addr, err)
		}
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("smtp write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close data: %w", err)
	}

	return client.Quit()
}

func buildEmailBody(from string, to []string, subject, text string) []byte {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + strings.Join(to, ", ") + "\r\n")
	b.WriteString("Subject: " + subject + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"utf-8\"\r\n")
	b.WriteString("\r\n")
	b.WriteString(text)
	return []byte(b.String())
}
