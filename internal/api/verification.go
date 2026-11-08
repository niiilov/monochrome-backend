package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"sync"
	"time"

	"mono/internal/storage"
)

func validatePublicURL(raw string) (string, error) {
	if raw == "" {
		raw = "http://localhost:8080"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("APP_PUBLIC_URL должен содержать адрес сайта, например https://example.com")
	}
	return strings.TrimRight(raw, "/"), nil
}

const (
	verificationTTL = 15 * time.Minute
	maxCodeAttempts = 5
)

func randomCode() (string, error) {
	const span = 1000000
	limit := ^uint64(0) / span * span
	var raw [8]byte
	var value uint64
	for {
		if _, err := rand.Read(raw[:]); err != nil {
			return "", err
		}
		n := binary.BigEndian.Uint64(raw[:])
		if n < limit {
			value = n % span
			break
		}
	}
	var digits [6]byte
	for i := 5; i >= 0; i-- {
		digits[i] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[:]), nil
}

func verificationHash(userID, code string) string {
	sum := sha256.Sum256([]byte(userID + ":" + code))
	return hex.EncodeToString(sum[:])
}

func (s *Server) sendVerification(ctx context.Context, email, code string) error {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	return s.mail.SendVerification(ctx, email, code)
}

func validAccountEmail(email string) bool {
	address, err := mail.ParseAddress(email)
	return err == nil && address.Address == email && len(email) <= 254 && strings.Contains(email, "@")
}

func validCode(code string) bool {
	if len(code) != 6 {
		return false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (s *Server) verifyEmail(w http.ResponseWriter, r *http.Request) {
	if !s.authLimit.allow(r.RemoteAddr) {
		writeErr(w, 429, "слишком много попыток")
		return
	}
	var body struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if !decodeSmallJSON(w, r, &body) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(body.Email))
	code := strings.TrimSpace(body.Code)
	if !validAccountEmail(email) || !validCode(code) {
		writeErr(w, 400, "укажите почту и код из письма")
		return
	}
	user, _, err := s.store.UserByEmail(r.Context(), email)
	if errors.Is(err, storage.ErrNotFound) || (err == nil && user.EmailVerified) {
		writeErr(w, 400, "код уже использован или истёк; запросите новое письмо")
		return
	}
	if err != nil {
		s.databaseError(w, err)
		return
	}
	if s.codeAttempts.tooMany(user.ID) {
		writeErr(w, 400, "код уже использован или истёк; запросите новое письмо")
		return
	}
	verified, err := s.store.VerifyEmail(r.Context(), verificationHash(user.ID, code))
	if errors.Is(err, storage.ErrNotFound) {
		if s.codeAttempts.fail(user.ID) {
			s.invalidateCode(r.Context(), user.ID)
		}
		writeErr(w, 400, "код уже использован или истёк; запросите новое письмо")
		return
	}
	if err != nil {
		s.databaseError(w, err)
		return
	}
	s.codeAttempts.reset(user.ID)
	writeJSON(w, 200, map[string]any{"user": verified, "message": "почта подтверждена, теперь можно войти"})
}

func (s *Server) invalidateCode(ctx context.Context, userID string) {
	dead, err := randomToken()
	if err != nil {
		return
	}
	_ = s.store.ReplaceVerification(ctx, userID, tokenHash("dead:"+dead), time.Now().Add(verificationTTL))
}

func (s *Server) resendVerification(w http.ResponseWriter, r *http.Request) {
	if !s.authLimit.allow(r.RemoteAddr) {
		writeErr(w, 429, "слишком много попыток")
		return
	}
	body, ok := readAuth(w, r)
	if !ok {
		return
	}
	user, _, err := s.store.UserByEmail(r.Context(), body.Email)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		s.databaseError(w, err)
		return
	}
	if err == nil && !user.EmailVerified {
		if s.mail == nil {
			writeErr(w, 503, "отправка подтверждения не настроена")
			return
		}
		code, err := randomCode()
		if err != nil {
			s.databaseError(w, err)
			return
		}
		if err = s.store.ReplaceVerification(r.Context(), user.ID, verificationHash(user.ID, code), time.Now().Add(verificationTTL)); err != nil && !errors.Is(err, storage.ErrNotFound) {
			s.databaseError(w, err)
			return
		}
		if err == nil {
			if err = s.sendVerification(r.Context(), user.Email, code); err != nil {
				writeErr(w, 502, "не удалось отправить письмо подтверждения")
				return
			}
			s.codeAttempts.reset(user.ID)
		}
	}
	writeJSON(w, 202, map[string]string{"message": "если адрес ожидает подтверждения, письмо отправлено"})
}

type codeLimiter struct {
	mu sync.Mutex
	n  map[string]int
}

func (l *codeLimiter) fail(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.n == nil {
		l.n = map[string]int{}
	}
	l.n[id]++
	return l.n[id] >= maxCodeAttempts
}

func (l *codeLimiter) tooMany(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.n[id] >= maxCodeAttempts
}

func (l *codeLimiter) reset(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.n, id)
}
