package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mono/internal/domain"
	"mono/internal/mailer"
	"mono/internal/timeutil"
)

type inputError struct {
	msg string
}

func (e *inputError) Error() string { return e.msg }

type createBody struct {
	Dataset     string `json:"dataset"`
	DatasetPath string `json:"dataset_path"`
	ControlTime string `json:"control_time"`
}

type articleBody struct {
	Path        string `json:"path"`
	ID          string `json:"id"`
	Title       string `json:"title"`
	Text        string `json:"text"`
	Source      string `json:"source"`
	URL         string `json:"url"`
	PublishedAt string `json:"published_at"`
}

// sendBody takes recipients only from this object.
// Text fields of a publication are not part of the request and cannot change the list.
type sendBody struct {
	Recipients []string `json:"recipients"`
	DryRun     *bool    `json:"dry_run"`
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	id, dir, err := s.reserve()
	if err != nil {
		if s.ctx.Err() != nil {
			writeErr(w, http.StatusServiceUnavailable, "сервер останавливается")
			return
		}
		writeErr(w, http.StatusInternalServerError, "не удалось создать каталог прогона")
		return
	}
	dataset, control, err := readCreate(r, dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		writeInput(w, err)
		return
	}
	if err := checkDataset(dataset); err != nil {
		_ = os.RemoveAll(dir)
		writeInput(w, err)
		return
	}
	when, err := parseControl(control)
	if err != nil {
		_ = os.RemoveAll(dir)
		writeInput(w, err)
		return
	}
	if err := s.launch(id, dir, domain.RunParams{DatasetPath: dataset, ControlTime: when}, ""); err != nil {
		_ = os.RemoveAll(dir)
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"run_id": id, "status": statusRunning})
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	view, _, ok := s.readyID(w, r, false)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) digest(w http.ResponseWriter, r *http.Request) {
	dir, ok := s.readyDir(w, r)
	if !ok {
		return
	}
	if wantsJSON(r) && !strings.Contains(r.Header.Get("Accept"), "text/markdown") {
		s.serveFile(w, dir, "mono.json", "application/json; charset=utf-8")
		return
	}
	s.serveFile(w, dir, "mono.md", "text/markdown; charset=utf-8")
}

func (s *Server) post(w http.ResponseWriter, r *http.Request) {
	dir, ok := s.readyDir(w, r)
	if !ok {
		return
	}
	s.serveFile(w, dir, "post.txt", "text/plain; charset=utf-8")
}

func (s *Server) email(w http.ResponseWriter, r *http.Request) {
	dir, ok := s.readyDir(w, r)
	if !ok {
		return
	}
	accept := r.Header.Get("Accept")
	switch {
	case strings.Contains(accept, "text/html") && !strings.Contains(accept, "application/json"):
		s.serveFile(w, dir, "email_preview.html", "text/html; charset=utf-8")
	case strings.Contains(accept, "text/plain") && !strings.Contains(accept, "application/json"):
		s.serveFile(w, dir, "email.txt", "text/plain; charset=utf-8")
	default:
		text, html, err := readLetter(dir)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "не удалось прочитать письмо")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"subject": emailSubject(text),
			"text":    text,
			"html":    html,
		})
	}
}

func (s *Server) journal(w http.ResponseWriter, r *http.Request) {
	dir, ok := s.readyDir(w, r)
	if !ok {
		return
	}
	if r.URL.Query().Get("format") == "json" || (wantsJSON(r) && r.URL.Query().Get("format") != "csv") {
		s.serveFile(w, dir, "journal.json", "application/json; charset=utf-8")
		return
	}
	s.serveFile(w, dir, "journal.csv", "text/csv; charset=utf-8")
}

func (s *Server) report(w http.ResponseWriter, r *http.Request) {
	dir, ok := s.readyDir(w, r)
	if !ok {
		return
	}
	s.serveFile(w, dir, "run_report.json", "application/json; charset=utf-8")
}

func (s *Server) send(w http.ResponseWriter, r *http.Request) {
	dir, ok := s.readyDir(w, r)
	if !ok {
		return
	}
	if s.mail == nil {
		writeErr(w, http.StatusInternalServerError, "отправка не настроена")
		return
	}
	body, err := readSend(r)
	if err != nil {
		writeInput(w, err)
		return
	}
	text, html, err := readLetter(dir)
	if err != nil {
		writeErr(w, http.StatusConflict, "письмо ещё не собрано")
		return
	}
	// Recipients are the request list only. Letter text is not scanned for addresses.
	res, err := s.mail.Send(r.Context(), mailer.Request{
		Recipients: body.Recipients,
		DryRun:     body.DryRun,
		Message: mailer.Message{
			Subject: emailSubject(text),
			Text:    text,
			HTML:    html,
		},
	})
	if res.Entry.Time != "" {
		if logErr := s.appendLog(filepath.Join(dir, "send_log.jsonl"), res.Entry); logErr != nil && err == nil {
			err = logErr
		}
	}
	if err != nil {
		writeMail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res.Entry)
}

func (s *Server) articles(w http.ResponseWriter, r *http.Request) {
	view, params, ok := s.readyID(w, r, true)
	if !ok {
		return
	}
	if strings.TrimSpace(params.DatasetPath) == "" {
		writeErr(w, http.StatusConflict, "у исходного прогона нет набора")
		return
	}
	id, dir, err := s.reserve()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "не удалось создать каталог прогона")
		return
	}
	extra, err := readArticle(r, dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		writeInput(w, err)
		return
	}
	extras := append([]string{}, params.ExtraPaths...)
	extras = append(extras, extra)
	if err := s.launch(id, dir, domain.RunParams{
		DatasetPath: params.DatasetPath,
		ControlTime: params.ControlTime,
		ExtraPaths:  extras,
	}, view.RunID); err != nil {
		_ = os.RemoveAll(dir)
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{
		"run_id":        id,
		"status":        statusRunning,
		"parent_run_id": view.RunID,
	})
}

func (s *Server) readyID(w http.ResponseWriter, r *http.Request, done bool) (runView, domain.RunParams, bool) {
	id := r.PathValue("id")
	if !idOK(id) {
		writeErr(w, http.StatusNotFound, "прогон не найден")
		return runView{}, domain.RunParams{}, false
	}
	view, params, ok := s.lookup(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "прогон не найден")
		return runView{}, domain.RunParams{}, false
	}
	if done && view.Status != statusDone {
		msg := "прогон ещё выполняется"
		if view.Status == statusFailed {
			msg = view.Error
			if msg == "" {
				msg = "прогон завершился с ошибкой"
			}
		}
		writeErr(w, http.StatusConflict, msg)
		return runView{}, domain.RunParams{}, false
	}
	return view, params, true
}

func (s *Server) readyDir(w http.ResponseWriter, r *http.Request) (string, bool) {
	_, params, ok := s.readyID(w, r, true)
	if !ok {
		return "", false
	}
	return params.OutDir, true
}

func (s *Server) serveFile(w http.ResponseWriter, dir, name, ctype string) {
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "нет файла "+name)
		return
	}
	serveBytes(w, http.StatusOK, ctype, raw)
}

func (s *Server) appendLog(path string, entry mailer.Entry) error {
	raw, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(raw)
	return err
}

func readCreate(r *http.Request, dir string) (string, string, error) {
	ct := r.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(ct, "multipart/form-data"):
		if err := r.ParseMultipartForm(maxUpload); err != nil {
			return "", "", &inputError{msg: "файл слишком большой или форма сломана"}
		}
		control := r.FormValue("control_time")
		file, hdr, err := r.FormFile("file")
		if err == nil {
			defer file.Close()
			path, saveErr := saveReader(dir, hdr.Filename, file)
			return path, control, saveErr
		}
		if !errors.Is(err, http.ErrMissingFile) {
			return "", "", &inputError{msg: "не удалось прочитать файл"}
		}
		return firstText(r.FormValue("dataset"), r.FormValue("dataset_path")), control, nil
	case strings.Contains(ct, "application/json"):
		var body createBody
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			return "", "", &inputError{msg: "некорректный JSON"}
		}
		return firstText(body.Dataset, body.DatasetPath), body.ControlTime, nil
	default:
		if err := r.ParseForm(); err != nil {
			return "", "", &inputError{msg: "некорректная форма"}
		}
		return firstText(r.FormValue("dataset"), r.FormValue("dataset_path")), r.FormValue("control_time"), nil
	}
}

func readArticle(r *http.Request, dir string) (string, error) {
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(maxUpload); err != nil {
			return "", &inputError{msg: "файл слишком большой или форма сломана"}
		}
		file, hdr, err := r.FormFile("file")
		if err == nil {
			defer file.Close()
			return saveReader(dir, hdr.Filename, file)
		}
		if !errors.Is(err, http.ErrMissingFile) {
			return "", &inputError{msg: "не удалось прочитать файл"}
		}
		if text := strings.TrimSpace(r.FormValue("text")); text != "" {
			return writeArticle(dir, articleBody{
				Title:       r.FormValue("title"),
				Text:        text,
				Source:      r.FormValue("source"),
				URL:         r.FormValue("url"),
				PublishedAt: r.FormValue("published_at"),
			})
		}
		if path := strings.TrimSpace(r.FormValue("path")); path != "" {
			return existingPath(path)
		}
		return "", &inputError{msg: "приложите файл или текст публикации"}
	}
	var body articleBody
	if err := json.NewDecoder(io.LimitReader(r.Body, maxUpload)).Decode(&body); err != nil {
		return "", &inputError{msg: "некорректный JSON"}
	}
	if strings.TrimSpace(body.Text) != "" || strings.TrimSpace(body.Title) != "" {
		return writeArticle(dir, body)
	}
	if strings.TrimSpace(body.Path) != "" {
		return existingPath(body.Path)
	}
	return "", &inputError{msg: "приложите файл или текст публикации"}
}

func readSend(r *http.Request) (sendBody, error) {
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") || ct == "" {
		var body sendBody
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			return sendBody{}, &inputError{msg: "некорректный JSON"}
		}
		return body, nil
	}
	if err := r.ParseForm(); err != nil {
		return sendBody{}, &inputError{msg: "некорректная форма"}
	}
	body := sendBody{Recipients: splitField(r.FormValue("recipients"))}
	if r.Form.Has("dry_run") {
		dry := parseBool(r.FormValue("dry_run"))
		body.DryRun = &dry
	}
	return body, nil
}

func writeArticle(dir string, body articleBody) (string, error) {
	if strings.TrimSpace(body.Title) == "" {
		body.Title = "Дополнительная публикация"
	}
	if strings.TrimSpace(body.Text) == "" {
		return "", &inputError{msg: "пустой текст публикации"}
	}
	if body.ID == "" {
		body.ID = "extra-" + filepath.Base(dir)
	}
	raw, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return "", err
	}
	raw = append(raw, '\n')
	if err := os.MkdirAll(filepath.Join(dir, "input"), 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "input", "extra.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func existingPath(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", &inputError{msg: "файл публикации не найден"}
	}
	if info.Size() == 0 {
		return "", &inputError{msg: "пустой файл"}
	}
	return path, nil
}

func saveReader(dir, name string, r io.Reader) (string, error) {
	if err := os.MkdirAll(filepath.Join(dir, "input"), 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "input", cleanName(name))
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	n, err := io.Copy(f, io.LimitReader(r, maxUpload+1))
	if err != nil {
		return "", err
	}
	if n == 0 {
		return "", &inputError{msg: "пустой файл"}
	}
	if n > maxUpload {
		return "", &inputError{msg: "файл больше 8 МБ"}
	}
	return path, nil
}

func checkDataset(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return &inputError{msg: "укажите путь к набору или загрузите файл"}
	}
	info, err := os.Stat(path)
	if err != nil {
		return &inputError{msg: "набор не найден"}
	}
	if !info.IsDir() && info.Size() == 0 {
		return &inputError{msg: "пустой файл"}
	}
	return nil
}

func parseControl(raw string) (time.Time, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return time.Time{}, &inputError{msg: "укажите контрольное время"}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	parsed, note := timeutil.ParseDate(s, timeutil.PolicyAssumeMSK)
	if parsed == nil {
		if note == "" {
			note = "дату разобрать нельзя"
		}
		return time.Time{}, &inputError{msg: "контрольное время: " + note}
	}
	return *parsed, nil
}

func readLetter(dir string) (string, string, error) {
	text, err := os.ReadFile(filepath.Join(dir, "email.txt"))
	if err != nil {
		return "", "", err
	}
	html, err := os.ReadFile(filepath.Join(dir, "email_preview.html"))
	if err != nil {
		return "", "", err
	}
	return string(text), string(html), nil
}

func emailSubject(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	line = strings.TrimSpace(line)
	if rest, ok := strings.CutPrefix(line, "Тема:"); ok {
		rest = strings.TrimSpace(rest)
		if rest != "" {
			return rest
		}
	}
	return "Дайджест по кибербезопасности"
}

func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

func writeInput(w http.ResponseWriter, err error) {
	var input *inputError
	if errors.As(err, &input) {
		writeErr(w, http.StatusBadRequest, input.msg)
		return
	}
	writeErr(w, http.StatusInternalServerError, "не удалось принять запрос")
}

func writeMail(w http.ResponseWriter, err error) {
	var rejected *mailer.RejectError
	if errors.As(err, &rejected) && rejected.Status != 0 {
		writeErr(w, rejected.Status, rejected.Message)
		return
	}
	writeErr(w, http.StatusBadGateway, "не удалось отправить письмо")
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		http.Error(w, "ошибка ответа", http.StatusInternalServerError)
		return
	}
	serveBytes(w, code, "application/json; charset=utf-8", []byte(buf.String()))
}

func serveBytes(w http.ResponseWriter, code int, ctype string, raw []byte) {
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_, _ = w.Write(raw)
}

func writeJSONFile(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(path, raw, 0o644)
}

func firstText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func splitField(raw string) []string {
	return strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\t' || r == ' '
	})
}

func parseBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on", "да":
		return true
	default:
		return false
	}
}

func cleanName(name string) string {
	base := filepath.Base(strings.TrimSpace(name))
	base = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, base)
	base = strings.Trim(base, ".")
	if base == "" || base == "." || base == ".." {
		return "upload.bin"
	}
	return base
}
