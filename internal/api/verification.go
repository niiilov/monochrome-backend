package api

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"mono/internal/storage"
)

const verificationTTL = 24 * time.Hour

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

func (s *Server) sendVerification(ctx context.Context, email, token string) error {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	link := s.publicURL + "/api/v1/auth/verify-email?token=" + url.QueryEscape(token)
	return s.mail.SendVerification(ctx, email, link)
}

var verificationTemplate = template.Must(template.New("verification").Parse(`<!doctype html>
<html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Подтверждение почты | Monochrome Digital</title></head>
<body><main><h1>Подтверждение почты</h1>
{{if .Token}}<p>Нажмите кнопку, чтобы подтвердить адрес и завершить регистрацию.</p>
<form method="post" action="/api/v1/auth/verify-email"><input type="hidden" name="token" value="{{.Token}}"><button type="submit">Подтвердить почту</button></form>
{{else}}<p>Почта подтверждена. Теперь можно войти в аккаунт.</p><a href="/">Перейти на сайт</a>{{end}}
</main></body></html>`))

func verificationHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
}

// GET never consumes the token: mail scanners must not activate an account.
func (s *Server) verificationPage(w http.ResponseWriter, r *http.Request) {
	verificationHeaders(w)
	token := r.URL.Query().Get("token")
	if len(token) != 64 {
		writeErr(w, 400, "некорректная ссылка подтверждения")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = verificationTemplate.Execute(w, struct{ Token string }{token})
}

func (s *Server) verifyEmail(w http.ResponseWriter, r *http.Request) {
	verificationHeaders(w)
	if !s.authLimit.allow(r.RemoteAddr) {
		writeErr(w, 429, "слишком много попыток")
		return
	}
	var token string
	isForm := strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded")
	if isForm {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err := r.ParseForm(); err != nil {
			writeErr(w, 400, "некорректная форма")
			return
		}
		token = r.PostForm.Get("token")
	} else {
		var body struct {
			Token string `json:"token"`
		}
		if !decodeSmallJSON(w, r, &body) {
			return
		}
		token = body.Token
	}
	if len(token) != 64 {
		writeErr(w, 400, "некорректная ссылка подтверждения")
		return
	}
	user, err := s.store.VerifyEmail(r.Context(), tokenHash(token))
	if errors.Is(err, storage.ErrNotFound) {
		writeErr(w, 400, "ссылка уже использована или истекла; запросите новое письмо")
		return
	}
	if err != nil {
		s.databaseError(w, err)
		return
	}
	if isForm {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = verificationTemplate.Execute(w, struct{ Token string }{})
		return
	}
	writeJSON(w, 200, map[string]any{"user": user, "message": "почта подтверждена, теперь можно войти"})
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
		token, err := randomToken()
		if err != nil {
			s.databaseError(w, err)
			return
		}
		if err = s.store.ReplaceVerification(r.Context(), user.ID, tokenHash(token), time.Now().Add(verificationTTL)); err != nil && !errors.Is(err, storage.ErrNotFound) {
			s.databaseError(w, err)
			return
		}
		if err == nil {
			if err = s.sendVerification(r.Context(), user.Email, token); err != nil {
				writeErr(w, 502, "не удалось отправить письмо подтверждения")
				return
			}
		}
	}
	writeJSON(w, 202, map[string]string{"message": "если адрес ожидает подтверждения, письмо отправлено"})
}
