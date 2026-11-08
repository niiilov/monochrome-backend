package api

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"mono/internal/mailer"
	"mono/internal/storage"
)

const (
	maxStoredRecipients = 200
	maxSendRecipients   = 100
)

func normalizeRecipientStatus(raw string, allowEmpty bool) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		if allowEmpty {
			return "send", true
		}
		return "", false
	case "send", "отправлять":
		return "send", true
	case "skip", "не отправлять":
		return "skip", true
	default:
		return "", false
	}
}

func (s *Server) listRecipients(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListRecipients(r.Context(), currentUser(r).ID)
	if err != nil {
		s.databaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recipients": list})
}

func (s *Server) createRecipients(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email  string   `json:"email"`
		Emails []string `json:"emails"`
		Status string   `json:"status"`
	}
	if !decodeSmallJSON(w, r, &body) {
		return
	}
	status, ok := normalizeRecipientStatus(body.Status, true)
	if !ok {
		writeErr(w, http.StatusBadRequest, "статус: send (отправлять) или skip (не отправлять)")
		return
	}
	raw := append([]string{}, body.Emails...)
	if strings.TrimSpace(body.Email) != "" {
		raw = append([]string{body.Email}, raw...)
	}
	if len(raw) == 0 {
		writeErr(w, http.StatusBadRequest, "укажите email")
		return
	}
	if len(raw) > 50 {
		writeErr(w, http.StatusBadRequest, "слишком много адресов за один раз")
		return
	}
	addresses := make([]string, 0, len(raw))
	seen := map[string]bool{}
	for _, item := range raw {
		addr, err := mailer.NormalizeAddress(item)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "укажите корректный email")
			return
		}
		if seen[addr] {
			continue
		}
		seen[addr] = true
		addresses = append(addresses, addr)
	}
	existing, err := s.store.ListRecipients(r.Context(), currentUser(r).ID)
	if err != nil {
		s.databaseError(w, err)
		return
	}
	have := map[string]bool{}
	for _, item := range existing {
		have[item.Email] = true
	}
	for _, addr := range addresses {
		if have[addr] {
			writeErr(w, http.StatusConflict, "этот адрес уже в списке")
			return
		}
	}
	if len(existing)+len(addresses) > maxStoredRecipients {
		writeErr(w, http.StatusBadRequest, "в списке может быть не больше 200 адресов")
		return
	}
	created := make([]storage.Recipient, 0, len(addresses))
	for _, addr := range addresses {
		id, err := randomToken()
		if err != nil {
			s.databaseError(w, err)
			return
		}
		item, err := s.store.CreateRecipient(r.Context(), currentUser(r).ID, storage.Recipient{ID: id, Email: addr, Status: status})
		if errors.Is(err, storage.ErrEmailTaken) {
			writeErr(w, http.StatusConflict, "этот адрес уже в списке")
			return
		}
		if err != nil {
			s.databaseError(w, err)
			return
		}
		created = append(created, item)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"recipients": created})
}

func (s *Server) updateRecipient(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Status string `json:"status"`
	}
	if !decodeSmallJSON(w, r, &body) {
		return
	}
	status, ok := normalizeRecipientStatus(body.Status, false)
	if !ok {
		writeErr(w, http.StatusBadRequest, "статус: send (отправлять) или skip (не отправлять)")
		return
	}
	item, err := s.store.UpdateRecipient(r.Context(), currentUser(r).ID, r.PathValue("id"), status)
	if errors.Is(err, storage.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "получатель не найден")
		return
	}
	if err != nil {
		s.databaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recipient": item})
}

func (s *Server) deleteRecipient(w http.ResponseWriter, r *http.Request) {
	err := s.store.DeleteRecipient(r.Context(), currentUser(r).ID, r.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "получатель не найден")
		return
	}
	if err != nil {
		s.databaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) sendSaved(w http.ResponseWriter, r *http.Request, dir string, body sendBody, text, html string) {
	if s.mail == nil {
		writeErr(w, http.StatusInternalServerError, "отправка не настроена")
		return
	}
	list, err := s.store.ListRecipients(r.Context(), currentUser(r).ID)
	if err != nil {
		s.databaseError(w, err)
		return
	}
	addresses, err := pickRecipients(list, body)
	if err != nil {
		writeInput(w, err)
		return
	}
	dry := false
	if body.DryRun != nil {
		dry = *body.DryRun
	}
	results := make([]map[string]any, 0, len(addresses))
	sent := 0
	for _, email := range addresses {
		res, sendErr := s.mail.Send(r.Context(), mailer.Request{
			Recipients:   []string{email},
			DryRun:       &dry,
			Unrestricted: true,
			Message: mailer.Message{
				Subject: emailSubject(text),
				Text:    text,
				HTML:    html,
			},
		})
		if res.Entry.Time != "" {
			if logErr := s.appendLog(filepath.Join(dir, "send_log.jsonl"), res.Entry); logErr != nil && sendErr == nil {
				sendErr = logErr
			}
		}
		var rejected *mailer.RejectError
		if errors.As(sendErr, &rejected) && rejected.Status != 0 && rejected.Status < 500 {
			writeMail(w, sendErr)
			return
		}
		item := map[string]any{"email": email, "result": res.Entry.Result}
		if sendErr != nil {
			item["result"] = "error"
			item["error"] = sendErr.Error()
		} else {
			sent++
		}
		results = append(results, item)
	}
	code := http.StatusOK
	if sent == 0 {
		code = http.StatusBadGateway
	}
	writeJSON(w, code, map[string]any{"mode": body.Mode, "dry_run": dry, "results": results})
}

func pickRecipients(list []storage.Recipient, body sendBody) ([]string, error) {
	switch body.Mode {
	case "all":
		out := make([]string, 0, len(list))
		for _, item := range list {
			if item.Status == "send" {
				out = append(out, item.Email)
			}
		}
		if len(out) == 0 {
			return nil, &inputError{msg: "нет адресов со статусом «отправлять»"}
		}
		if len(out) > maxSendRecipients {
			return nil, &inputError{msg: "слишком много адресов со статусом «отправлять»"}
		}
		return out, nil
	case "selected":
		if len(body.RecipientIDs) == 0 {
			return nil, &inputError{msg: "выберите хотя бы один адрес"}
		}
		if len(body.RecipientIDs) > maxSendRecipients {
			return nil, &inputError{msg: "слишком много адресов"}
		}
		byID := make(map[string]storage.Recipient, len(list))
		for _, item := range list {
			byID[item.ID] = item
		}
		out := make([]string, 0, len(body.RecipientIDs))
		seen := map[string]bool{}
		for _, id := range body.RecipientIDs {
			item, ok := byID[id]
			if !ok {
				return nil, &inputError{msg: "получатель не найден"}
			}
			if item.Status != "send" {
				return nil, &inputError{msg: "адрес отмечен как «не отправлять»: " + item.Email}
			}
			if seen[item.Email] {
				continue
			}
			seen[item.Email] = true
			out = append(out, item.Email)
		}
		return out, nil
	default:
		return nil, &inputError{msg: "режим рассылки: all или selected"}
	}
}
