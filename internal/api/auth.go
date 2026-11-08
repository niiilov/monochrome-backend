package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
	"mono/internal/storage"
)

const sessionCookie = "mono_session"
const sessionTTL = 7 * 24 * time.Hour

type userKey struct{}

type authBody struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

func currentUser(r *http.Request) storage.User {
	user, _ := r.Context().Value(userKey{}).(storage.User)
	return user
}

func randomToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func tokenHash(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

func requestToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); auth != "" {
		kind, token, ok := strings.Cut(auth, " ")
		if ok && strings.EqualFold(kind, "Bearer") && len(token) == 64 {
			return token
		}
		return ""
	}
	if cookie, err := r.Cookie(sessionCookie); err == nil && len(cookie.Value) == 64 {
		return cookie.Value
	}
	return ""
}

func (s *Server) authenticated(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := requestToken(r)
		if token == "" {
			writeErr(w, http.StatusUnauthorized, "необходим вход в аккаунт")
			return
		}
		user, err := s.store.SessionUser(r.Context(), tokenHash(token))
		if errors.Is(err, storage.ErrNotFound) {
			writeErr(w, http.StatusUnauthorized, "сессия недействительна или истекла")
			return
		}
		if err != nil {
			s.databaseError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		next(w, r.WithContext(context.WithValue(r.Context(), userKey{}, user)))
	}
}

// SameSite cookies plus an Origin check protect mutations including login/logout.
func sameOrigin(next http.Handler, publicURL string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			origin := r.Header.Get("Origin")
			if origin != "" {
				u, err := url.Parse(origin)
				if err != nil || (u.Host != r.Host && origin != publicURL) || (u.Scheme != "http" && u.Scheme != "https") {
					writeErr(w, http.StatusForbidden, "запрос с другого сайта запрещён")
					return
				}
			}
			if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				writeErr(w, http.StatusForbidden, "запрос с другого сайта запрещён")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func readAuth(w http.ResponseWriter, r *http.Request) (authBody, bool) {
	var body authBody
	if !decodeSmallJSON(w, r, &body) {
		return body, false
	}
	body.Email = strings.ToLower(strings.TrimSpace(body.Email))
	body.Name = strings.TrimSpace(body.Name)
	return body, true
}

func decodeSmallJSON(w http.ResponseWriter, r *http.Request, body any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(body); err != nil {
		writeErr(w, 400, "некорректный JSON или слишком большой запрос")
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeErr(w, 400, "ожидается один JSON-объект")
		return false
	}
	return true
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	if !s.authLimit.allow(r.RemoteAddr) {
		writeErr(w, 429, "слишком много попыток, повторите через 15 минут")
		return
	}
	body, ok := readAuth(w, r)
	if !ok {
		return
	}
	address, err := mail.ParseAddress(body.Email)
	if err != nil || address.Address != body.Email || len(body.Email) > 254 || !strings.Contains(body.Email, "@") {
		writeErr(w, 400, "укажите корректный email")
		return
	}
	if !utf8.ValidString(body.Password) || utf8.RuneCountInString(body.Password) < 8 || len(body.Password) > 72 {
		writeErr(w, 400, "пароль должен содержать минимум 8 символов и не более 72 байт")
		return
	}
	if utf8.RuneCountInString(body.Name) > 100 {
		writeErr(w, 400, "имя должно быть не длиннее 100 символов")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		s.databaseError(w, err)
		return
	}
	id, err := randomToken()
	if err != nil {
		s.databaseError(w, err)
		return
	}
	code, err := randomCode()
	if err != nil {
		s.databaseError(w, err)
		return
	}
	if s.mail == nil {
		writeErr(w, 503, "отправка подтверждения не настроена")
		return
	}
	expires := time.Now().Add(verificationTTL)
	user, err := s.store.Register(r.Context(), storage.User{ID: id, Email: body.Email, Name: body.Name}, string(hash), verificationHash(id, code), expires)
	if errors.Is(err, storage.ErrEmailTaken) {
		writeErr(w, 409, "этот email уже зарегистрирован")
		return
	}
	if err != nil {
		s.databaseError(w, err)
		return
	}
	if err = s.sendVerification(r.Context(), user.Email, code); err != nil {
		writeErr(w, http.StatusBadGateway, "аккаунт создан, но письмо не отправлено; запросите подтверждение повторно")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"user": user, "verification_required": true, "message": "код подтверждения отправлен на почту"})
}

// The dummy hash makes unknown users go through the same expensive password check.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("invalid-login-placeholder"), bcrypt.DefaultCost)

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.authLimit.allow(r.RemoteAddr) {
		writeErr(w, 429, "слишком много попыток, повторите через 15 минут")
		return
	}
	body, ok := readAuth(w, r)
	if !ok {
		return
	}
	user, hash, err := s.store.UserByEmail(r.Context(), body.Email)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		s.databaseError(w, err)
		return
	}
	if errors.Is(err, storage.ErrNotFound) {
		hash = string(dummyHash)
	}
	passwordErr := bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.Password))
	if err != nil || passwordErr != nil {
		writeErr(w, 401, "неверный email или пароль")
		return
	}
	if !user.EmailVerified {
		writeErr(w, http.StatusForbidden, "сначала введите код из письма")
		return
	}
	token, err := randomToken()
	if err != nil {
		s.databaseError(w, err)
		return
	}
	expires := time.Now().Add(sessionTTL)
	if err = s.store.CreateSession(r.Context(), user.ID, tokenHash(token), expires); err != nil {
		s.databaseError(w, err)
		return
	}
	s.authResponse(w, http.StatusOK, user, token, expires)
}

func (s *Server) authResponse(w http.ResponseWriter, status int, user storage.User, token string, expires time.Time) {
	w.Header().Set("Cache-Control", "no-store")
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.secureCookies, SameSite: http.SameSiteLaxMode, MaxAge: int(sessionTTL.Seconds()), Expires: expires})
	writeJSON(w, status, map[string]any{"user": user, "token": token, "expires_at": expires.UTC()})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"user": currentUser(r)})
}

func (s *Server) updateProfile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     *string   `json:"name"`
		Keywords *[]string `json:"keywords"`
	}
	if !decodeSmallJSON(w, r, &body) {
		return
	}
	if body.Name != nil {
		name := strings.TrimSpace(*body.Name)
		if name == "" || utf8.RuneCountInString(name) > 100 {
			writeErr(w, 400, "имя должно содержать от 1 до 100 символов")
			return
		}
		body.Name = &name
	}
	if body.Keywords != nil {
		if len(*body.Keywords) > 12 {
			writeErr(w, 400, "можно сохранить до 12 ключевых слов")
			return
		}
		for i, keyword := range *body.Keywords {
			keyword = strings.TrimSpace(keyword)
			if keyword == "" || utf8.RuneCountInString(keyword) > 80 {
				writeErr(w, 400, "ключевое слово должно содержать от 1 до 80 символов")
				return
			}
			(*body.Keywords)[i] = keyword
		}
	}
	user, err := s.store.UpdateUser(r.Context(), currentUser(r).ID, body.Name, body.Keywords)
	if err != nil {
		s.databaseError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"user": user})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteSession(r.Context(), tokenHash(requestToken(r))); err != nil {
		s.databaseError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: s.secureCookies, SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0)})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) databaseError(w http.ResponseWriter, err error) {
	s.log.Error("ошибка хранилища", "err", err.Error())
	writeErr(w, http.StatusServiceUnavailable, "хранилище временно недоступно")
}

type authAttempt struct {
	count   int
	expires time.Time
}
type authLimiter struct {
	mu       sync.Mutex
	attempts map[string]authAttempt
}

func (l *authLimiter) allow(remote string) bool {
	ip, _, err := net.SplitHostPort(remote)
	if err != nil {
		ip = remote
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.attempts == nil {
		l.attempts = map[string]authAttempt{}
	}
	now := time.Now()
	for key, entry := range l.attempts {
		if !entry.expires.After(now) {
			delete(l.attempts, key)
		}
	}
	entry, exists := l.attempts[ip]
	if !exists {
		if len(l.attempts) >= 10000 {
			return false
		}
		entry.expires = now.Add(15 * time.Minute)
	}
	entry.count++
	l.attempts[ip] = entry
	return entry.count <= 30
}
