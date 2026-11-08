package mailer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"mono/internal/config"
)

const (
	// MaxRecipients is the cap used when the frozen config has no field for it.
	MaxRecipients = 10

	resultDryRun   = "dry-run"
	resultSent     = "sent"
	resultRejected = "rejected"
	resultError    = "error"
)

// emailRE is the address we accept after net/mail has parsed a display name away.
var emailRE = regexp.MustCompile(`^[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}$`)

// RejectError is a refusal the API maps to an HTTP status.
// Status is 4xx for a bad address and 502 when SMTP itself fails.
type RejectError struct {
	Status  int
	Message string
}

func (e *RejectError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// Message is the letter body. Recipients are never read from these fields.
type Message struct {
	Subject string
	Period  string
	Text    string
	HTML    string
}

// Request is one delivery attempt.
// A nil DryRun uses the config value, which defaults to true.
// An empty Recipients list uses the configured allowlist.
// Unrestricted skips that allowlist. The API sets it only for addresses saved by the user.
type Request struct {
	Recipients   []string
	DryRun       *bool
	Message      Message
	Unrestricted bool
}

// Entry is one line of the send journal. It never contains a password.
type Entry struct {
	Time       string   `json:"time"`
	Recipients []string `json:"recipients"`
	DryRun     bool     `json:"dry_run"`
	Result     string   `json:"result"`
	Error      string   `json:"error,omitempty"`
}

// Result is the outcome of Send, including the journal line.
type Result struct {
	Entry Entry
}

// Sender delivers one already built MIME message.
type Sender interface {
	Send(ctx context.Context, from string, to []string, raw []byte) error
}

// Mailer checks the allowlist and sends one templated letter.
type Mailer struct {
	cfg   config.Mail
	allow map[string]bool
	log   *slog.Logger
	send  Sender
	max   int
	now   func() time.Time
}

// New builds a mailer. SMTP credentials come only from cfg, which Load fills from the environment.
func New(cfg config.Mail, log *slog.Logger) *Mailer {
	if log == nil {
		log = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	allow := make(map[string]bool, len(cfg.Allowlist))
	clean := make([]string, 0, len(cfg.Allowlist))
	for _, raw := range cfg.Allowlist {
		addr, err := parseAddress(raw)
		if err != nil {
			log.Warn("allowlist", "error", err.Error())
			continue
		}
		if allow[addr] {
			continue
		}
		allow[addr] = true
		clean = append(clean, addr)
	}
	cfg.Allowlist = clean
	m := &Mailer{
		cfg:   cfg,
		allow: allow,
		log:   log,
		max:   MaxRecipients,
		now:   time.Now,
	}
	m.send = smtpClient{host: cfg.Host, port: cfg.Port, user: cfg.User, password: cfg.Password}
	return m
}

// SetSender replaces the SMTP transport. Tests use it to avoid the network.
func (m *Mailer) SetSender(s Sender) {
	if s != nil {
		m.send = s
	}
}

// Send validates recipients, writes a journal line and either records a dry-run or calls SMTP.
// Publication text in Message cannot add or replace a recipient.
func (m *Mailer) Send(ctx context.Context, req Request) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	dry := m.cfg.DryRun
	if req.DryRun != nil {
		dry = *req.DryRun
	}
	rawList := req.Recipients
	if len(rawList) == 0 && !req.Unrestricted {
		rawList = append([]string(nil), m.cfg.Allowlist...)
	}
	recipients, err := normalizeList(rawList)
	if err != nil {
		return m.reject(recipients, dry, err)
	}
	if len(recipients) == 0 {
		return m.reject(nil, dry, &RejectError{Status: http.StatusBadRequest, Message: "укажите хотя бы один адрес"})
	}
	if len(recipients) > m.max {
		return m.reject(recipients, dry, &RejectError{
			Status:  http.StatusBadRequest,
			Message: fmt.Sprintf("слишком много адресов: %d, лимит %d", len(recipients), m.max),
		})
	}
	if !req.Unrestricted {
		if outside := notAllowed(recipients, m.allow); len(outside) > 0 {
			return m.reject(recipients, dry, &RejectError{
				Status:  http.StatusForbidden,
				Message: "адрес вне списка разрешённых: " + strings.Join(outside, ", "),
			})
		}
	}
	from, err := m.senderAddress(dry)
	if err != nil {
		return m.reject(recipients, dry, err)
	}
	subject := strings.TrimSpace(req.Message.Subject)
	if subject == "" {
		subject = "Дайджест по кибербезопасности"
	}
	msg := req.Message
	msg.Subject = subject
	raw, err := buildMIME(from, recipients, msg, m.now())
	if err != nil {
		return m.reject(recipients, dry, &RejectError{Status: http.StatusInternalServerError, Message: "не удалось собрать письмо"})
	}
	if dry {
		entry := m.entry(recipients, true, resultDryRun, "")
		m.record(entry)
		return Result{Entry: entry}, nil
	}
	if strings.TrimSpace(m.cfg.Host) == "" {
		return m.reject(recipients, false, &RejectError{Status: http.StatusBadRequest, Message: "не задан SMTP_HOST"})
	}
	if err := m.send.Send(ctx, from, recipients, raw); err != nil {
		wrapped := &RejectError{Status: http.StatusBadGateway, Message: "не удалось отправить письмо: " + m.safe(err)}
		entry := m.entry(recipients, false, resultError, wrapped.Error())
		m.record(entry)
		return Result{Entry: entry}, wrapped
	}
	entry := m.entry(recipients, false, resultSent, "")
	m.record(entry)
	return Result{Entry: entry}, nil
}

func (m *Mailer) reject(recipients []string, dry bool, err error) (Result, error) {
	entry := m.entry(recipients, dry, resultRejected, err.Error())
	m.record(entry)
	return Result{Entry: entry}, err
}

func (m *Mailer) entry(recipients []string, dry bool, result, reason string) Entry {
	if recipients == nil {
		recipients = []string{}
	}
	return Entry{
		Time:       m.now().UTC().Format(time.RFC3339),
		Recipients: append([]string(nil), recipients...),
		DryRun:     dry,
		Result:     result,
		Error:      reason,
	}
}

func (m *Mailer) record(entry Entry) {
	m.log.Info("отправка",
		"time", entry.Time,
		"recipients", entry.Recipients,
		"dry_run", entry.DryRun,
		"result", entry.Result,
		"error", entry.Error,
	)
}

func (m *Mailer) senderAddress(dry bool) (string, error) {
	raw := strings.TrimSpace(m.cfg.From)
	if raw == "" {
		if dry {
			return "mono@localhost", nil
		}
		return "", &RejectError{Status: http.StatusBadRequest, Message: "не задан адрес отправителя SMTP_FROM"}
	}
	addr, err := parseAddress(raw)
	if err != nil {
		return "", &RejectError{Status: http.StatusBadRequest, Message: "адрес отправителя: " + err.Error()}
	}
	return addr, nil
}

func (m *Mailer) safe(err error) string {
	msg := err.Error()
	if secret := m.cfg.Password; secret != "" {
		msg = strings.ReplaceAll(msg, secret, "[скрыто]")
	}
	return msg
}

func normalizeList(raw []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, item := range splitRaw(raw) {
		addr, err := parseAddress(item)
		if err != nil {
			return out, &RejectError{Status: http.StatusBadRequest, Message: err.Error()}
		}
		if seen[addr] {
			continue
		}
		seen[addr] = true
		out = append(out, addr)
	}
	return out, nil
}

func splitRaw(list []string) []string {
	var out []string
	for _, item := range list {
		parts := strings.FieldsFunc(item, func(r rune) bool {
			return r == ',' || r == ';' || r == '\n' || r == '\r'
		})
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

// NormalizeAddress checks one mailbox and returns it in lower case.
func NormalizeAddress(raw string) (string, error) {
	return parseAddress(raw)
}

func parseAddress(raw string) (string, error) {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return "", errors.New("пустой адрес")
	}
	if strings.ContainsAny(clean, "\r\n") {
		return "", errors.New("некорректный адрес: " + clean)
	}
	parsed, err := mail.ParseAddress(clean)
	if err != nil {
		return "", errors.New("некорректный адрес: " + clean)
	}
	addr := strings.ToLower(strings.TrimSpace(parsed.Address))
	if !emailRE.MatchString(addr) {
		return "", errors.New("некорректный адрес: " + clean)
	}
	return addr, nil
}

func notAllowed(recipients []string, allow map[string]bool) []string {
	var out []string
	for _, addr := range recipients {
		if !allow[addr] {
			out = append(out, addr)
		}
	}
	return out
}
