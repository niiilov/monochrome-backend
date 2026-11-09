package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"

	"mono/internal/config"
	"mono/internal/mailer"
	"mono/internal/storage"
)

func (s *Server) smtpSettings(ctx context.Context, userID string) (storage.SMTPSettings, bool, error) {
	settings, err := s.store.GetSMTPSettings(ctx, userID)
	if !errors.Is(err, storage.ErrNotFound) {
		return settings, false, err
	}
	if s.mail == nil {
		return storage.SMTPSettings{}, true, nil
	}
	cfg := s.mail.SMTPConfig()
	if cfg.Port == 0 {
		cfg.Port = 587
	}
	from := cfg.From
	if address, err := mailer.NormalizeAddress(from); err == nil {
		from = address
	}
	return storage.SMTPSettings{Host: cfg.Host, Port: cfg.Port, Username: cfg.User, Password: cfg.Password, From: from}, true, nil
}

func writeSMTPSettings(w http.ResponseWriter, settings storage.SMTPSettings, isDefault bool) {
	writeJSON(w, http.StatusOK, map[string]any{
		"settings": settings, "is_default": isDefault, "has_password": settings.Password != "",
	})
}

func (s *Server) getSMTPSettings(w http.ResponseWriter, r *http.Request) {
	settings, isDefault, err := s.smtpSettings(r.Context(), currentUser(r).ID)
	if err != nil {
		s.databaseError(w, err)
		return
	}
	writeSMTPSettings(w, settings, isDefault)
}

func (s *Server) saveSMTPSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Username string `json:"username"`
		Password string `json:"password"`
		From     string `json:"from"`
	}
	if !decodeSmallJSON(w, r, &body) {
		return
	}
	body.Host = strings.ToLower(strings.TrimSpace(body.Host))
	body.Username = strings.TrimSpace(body.Username)
	from, err := mailer.NormalizeAddress(body.From)
	if !validSMTPHost(body.Host) || body.Port < 1 || body.Port > 65535 || err != nil || len(body.Username) > 254 || strings.ContainsAny(body.Username, "\r\n\x00") || len(body.Password) > 1024 || strings.ContainsRune(body.Password, '\x00') {
		writeErr(w, http.StatusBadRequest, "проверьте SMTP-сервер, порт, логин и адрес отправителя")
		return
	}
	previous, _, err := s.smtpSettings(r.Context(), currentUser(r).ID)
	if err != nil {
		s.databaseError(w, err)
		return
	}
	// Never forward a saved secret to a different SMTP host, port or account.
	if body.Username == "" {
		body.Password = ""
	} else if body.Password == "" {
		if body.Host != strings.ToLower(previous.Host) || body.Port != previous.Port || body.Username != previous.Username || previous.Password == "" {
			writeErr(w, http.StatusBadRequest, "введите пароль для нового SMTP-подключения")
			return
		}
		body.Password = previous.Password
	}
	settings := storage.SMTPSettings{Host: body.Host, Port: body.Port, Username: body.Username, Password: body.Password, From: from}
	if err = s.store.SaveSMTPSettings(r.Context(), currentUser(r).ID, settings); err != nil {
		s.databaseError(w, err)
		return
	}
	writeSMTPSettings(w, settings, false)
}

func (s *Server) deleteSMTPSettings(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteSMTPSettings(r.Context(), currentUser(r).ID); err != nil {
		s.databaseError(w, err)
		return
	}
	s.getSMTPSettings(w, r)
}

func (s *Server) digestMailer(ctx context.Context, userID string) (*mailer.Mailer, error) {
	settings, err := s.store.GetSMTPSettings(ctx, userID)
	if errors.Is(err, storage.ErrNotFound) {
		return s.mail, nil
	}
	if err != nil {
		return nil, err
	}
	return s.mail.WithSMTP(config.Mail{Host: settings.Host, Port: settings.Port, User: settings.Username, Password: settings.Password, From: settings.From}), nil
}

func validSMTPHost(host string) bool {
	if net.ParseIP(host) != nil {
		return true
	}
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, ch := range label {
			if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '-' {
				return false
			}
		}
	}
	return true
}
