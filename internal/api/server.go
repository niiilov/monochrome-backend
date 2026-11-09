package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"mono/internal/domain"
	"mono/internal/mailer"
	"mono/internal/runner"
	"mono/internal/storage"
	"mono/internal/timeutil"
)

const (
	statusRunning = "running"
	statusDone    = "done"
	statusFailed  = "failed"

	maxUpload = 8 << 20
)

// Server is the HTTP API. The runner is injected so tests and cmd/monod can swap it.
type Server struct {
	runner        runner.Runner
	mail          *mailer.Mailer
	runsDir       string
	log           *slog.Logger
	store         storage.Repository
	secureCookies bool
	authLimit     authLimiter
	codeAttempts  codeLimiter
	datasetRoots  []string
	publicURL     string

	mu     sync.RWMutex
	fileMu sync.Mutex
	runs   map[string]*runRec
	wg     sync.WaitGroup

	ctx    context.Context
	cancel context.CancelFunc
}

type runRec struct {
	UserID     string
	ID         string
	Status     string
	Stage      string
	Done       int
	Total      int
	Stages     []stageProg
	Error      string
	StartedAt  time.Time
	FinishedAt time.Time
	Params     domain.RunParams
	ParentID   string
}

type stageProg struct {
	Name  string `json:"name"`
	Done  int    `json:"done"`
	Total int    `json:"total"`
}

type runView struct {
	RunID       string      `json:"run_id"`
	Status      string      `json:"status"`
	Stage       string      `json:"stage,omitempty"`
	Done        int         `json:"done"`
	Total       int         `json:"total"`
	Stages      []stageProg `json:"stages"`
	Error       string      `json:"error,omitempty"`
	StartedAt   string      `json:"started_at,omitempty"`
	FinishedAt  string      `json:"finished_at,omitempty"`
	Dataset     string      `json:"dataset,omitempty"`
	ControlTime string      `json:"control_time,omitempty"`
	ParentRunID string      `json:"parent_run_id,omitempty"`
	ExtraPaths  []string    `json:"extra_paths,omitempty"`
}

type storedRequest struct {
	DatasetPath string              `json:"dataset_path"`
	ControlTime time.Time           `json:"control_time"`
	ExtraPaths  []string            `json:"extra_paths,omitempty"`
	Keywords    []string            `json:"keywords,omitempty"`
	Live        bool                `json:"live,omitempty"`
	Sources     []domain.LiveSource `json:"sources,omitempty"`
	ParentRunID string              `json:"parent_run_id,omitempty"`
}

// Options configures HTTP sessions and trusted server-side dataset directories.
type Options struct {
	SecureCookies bool
	DatasetRoots  []string
	PublicURL     string
}

// New requires persistent storage; authentication cannot be disabled.
func New(r runner.Runner, m *mailer.Mailer, runsDir string, log *slog.Logger, db storage.Repository, opts Options) (*Server, error) {
	if db == nil {
		return nil, errors.New("требуется PostgreSQL")
	}
	publicURL, err := validatePublicURL(opts.PublicURL)
	if err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		runner:        r,
		mail:          m,
		runsDir:       runsDir,
		log:           log,
		store:         db,
		secureCookies: opts.SecureCookies,
		datasetRoots:  append([]string(nil), opts.DatasetRoots...),
		publicURL:     publicURL,
		runs:          map[string]*runRec{},
		ctx:           ctx,
		cancel:        cancel,
	}
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		cancel()
		return nil, fmt.Errorf("каталог прогонов: %w", err)
	}
	startup, done := context.WithTimeout(ctx, 10*time.Second)
	defer done()
	if err := db.RecoverRuns(startup); err != nil {
		cancel()
		return nil, err
	}
	return s, nil
}

// Handler is the HTTP API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /api/v1/healthz", s.health)
	mux.HandleFunc("POST /api/v1/auth/register", s.register)
	mux.HandleFunc("POST /api/v1/auth/login", s.login)
	mux.HandleFunc("POST /api/v1/auth/verify-email", s.verifyEmail)
	mux.HandleFunc("POST /api/v1/auth/resend-verification", s.resendVerification)
	mux.HandleFunc("POST /api/v1/auth/logout", s.authenticated(s.logout))
	mux.HandleFunc("GET /api/v1/auth/me", s.authenticated(s.me))
	mux.HandleFunc("PATCH /api/v1/auth/me", s.authenticated(s.updateProfile))
	mux.HandleFunc("POST /api/v1/auth/change-password", s.authenticated(s.changePassword))
	mux.HandleFunc("POST /api/v1/runs", s.authenticated(s.create))
	mux.HandleFunc("GET /api/v1/runs", s.authenticated(s.history))
	mux.HandleFunc("GET /api/v1/runs/{id}", s.authenticated(s.status))
	mux.HandleFunc("GET /api/v1/runs/{id}/digest", s.authenticated(s.digest))
	mux.HandleFunc("GET /api/v1/runs/{id}/post", s.authenticated(s.post))
	mux.HandleFunc("GET /api/v1/runs/{id}/email", s.authenticated(s.email))
	mux.HandleFunc("GET /api/v1/runs/{id}/journal", s.authenticated(s.journal))
	mux.HandleFunc("GET /api/v1/runs/{id}/report", s.authenticated(s.report))
	mux.HandleFunc("GET /api/v1/runs/{id}/export", s.authenticated(s.export))
	mux.HandleFunc("GET /api/v1/sources", s.authenticated(s.listSources))
	mux.HandleFunc("POST /api/v1/sources", s.authenticated(s.createSource))
	mux.HandleFunc("PATCH /api/v1/sources/{id}", s.authenticated(s.updateSource))
	mux.HandleFunc("DELETE /api/v1/sources/{id}", s.authenticated(s.deleteSource))
	mux.HandleFunc("GET /api/v1/recipients", s.authenticated(s.listRecipients))
	mux.HandleFunc("POST /api/v1/recipients", s.authenticated(s.createRecipients))
	mux.HandleFunc("PATCH /api/v1/recipients/{id}", s.authenticated(s.updateRecipient))
	mux.HandleFunc("DELETE /api/v1/recipients/{id}", s.authenticated(s.deleteRecipient))
	mux.HandleFunc("POST /api/v1/runs/{id}/send", s.authenticated(s.send))
	mux.HandleFunc("POST /api/v1/runs/{id}/articles", s.authenticated(s.articles))
	return sameOrigin(mux, s.publicURL)
}

// Shutdown cancels in-flight runs and waits for them.
func (s *Server) Shutdown(ctx context.Context) error {
	s.cancel()
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		s.databaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// reserve creates an empty run directory before the upload is saved.
// The runner starts only after launch, when the dataset path is final.
func (s *Server) reserve() (string, string, error) {
	if err := s.ctx.Err(); err != nil {
		return "", "", errors.New("сервер останавливается")
	}
	var last error
	for attempt := 0; attempt < 5; attempt++ {
		id := s.freshID()
		dir := filepath.Join(s.runsDir, id)
		err := os.Mkdir(dir, 0o755)
		if err == nil {
			return id, dir, nil
		}
		last = err
		if !os.IsExist(err) {
			return "", "", fmt.Errorf("каталог прогона: %w", err)
		}
	}
	if last == nil {
		last = errors.New("не удалось выделить прогон")
	}
	return "", "", last
}

func (s *Server) launch(id, dir string, params domain.RunParams, parent, userID string) error {
	if err := s.ctx.Err(); err != nil {
		return errors.New("сервер останавливается")
	}
	if err := os.MkdirAll(filepath.Join(dir, "input"), 0o755); err != nil {
		return fmt.Errorf("каталог прогона: %w", err)
	}
	params.OutDir = dir
	rec := &runRec{
		UserID:    userID,
		ID:        id,
		Status:    statusRunning,
		StartedAt: time.Now(),
		Params:    params,
		ParentID:  parent,
		Stages:    []stageProg{},
	}
	req := storedRequest{
		DatasetPath: params.DatasetPath,
		ControlTime: params.ControlTime,
		ExtraPaths:  append([]string(nil), params.ExtraPaths...),
		Keywords:    append([]string(nil), params.Keywords...),
		Live:        params.Live,
		Sources:     append([]domain.LiveSource(nil), params.Sources...),
		ParentRunID: parent,
	}
	if err := writeJSONFile(filepath.Join(dir, "request.json"), req); err != nil {
		return err
	}
	snapshot, err := json.Marshal(rec.view())
	if err != nil {
		return err
	}
	request, err := json.Marshal(req)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	if err = s.store.CreateRun(ctx, storage.Run{ID: id, UserID: userID, ParentID: parent, Status: rec.Status, StartedAt: rec.StartedAt, Snapshot: snapshot, Request: request}); err != nil {
		return err
	}
	s.mu.Lock()
	if _, exists := s.runs[id]; exists {
		s.mu.Unlock()
		return errors.New("прогон уже существует")
	}
	s.runs[id] = rec
	s.mu.Unlock()
	s.persist(rec.view())
	s.log.Info("запуск", "run_id", id, "dataset", filepath.Base(params.DatasetPath), "parent", parent)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.execute(id)
	}()
	return nil
}

func (s *Server) execute(id string) {
	s.mu.RLock()
	rec := s.runs[id]
	params := rec.Params
	ctx := s.ctx
	s.mu.RUnlock()

	state, report, err := s.runner.Run(ctx, params, func(stage string, done, total int) {
		s.update(id, func(run *runRec) {
			run.Stage = stage
			run.Done = done
			run.Total = total
			run.upsert(stage, done, total)
		})
	})
	if err == nil && state == nil {
		err = errors.New("раннер не вернул состояние")
	}
	if err == nil {
		err = finishFiles(params.OutDir, id, state, &report)
	}
	s.update(id, func(run *runRec) {
		run.FinishedAt = time.Now()
		if err != nil {
			run.Status = statusFailed
			run.Error = runError(err)
			return
		}
		run.Status = statusDone
		run.Error = ""
	})
	if err != nil {
		s.log.Info("прогон", "run_id", id, "status", statusFailed, "error", runError(err))
		return
	}
	s.log.Info("прогон", "run_id", id, "status", statusDone)
}

func (s *Server) update(id string, fn func(*runRec)) {
	s.mu.Lock()
	run := s.runs[id]
	if run == nil {
		s.mu.Unlock()
		return
	}
	fn(run)
	view := run.view()
	raw, err := json.Marshal(view)
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err = s.store.UpdateRun(ctx, storage.Run{ID: id, UserID: run.UserID, Status: run.Status, Snapshot: raw})
		cancel()
	}
	if err != nil {
		s.log.Error("сохранение прогона", "run_id", id, "err", err.Error())
	}
	// Keep writes ordered so an older progress event cannot overwrite completion.
	s.persist(view)
	if run.Status != statusRunning {
		delete(s.runs, id)
	}
	s.mu.Unlock()
}

func (s *Server) lookup(ctx context.Context, userID, id string) (runView, domain.RunParams, error) {
	run, err := s.store.GetRun(ctx, userID, id)
	if err != nil {
		return runView{}, domain.RunParams{}, err
	}
	var view runView
	var req storedRequest
	if err = json.Unmarshal(run.Snapshot, &view); err != nil {
		return view, domain.RunParams{}, err
	}
	if err = json.Unmarshal(run.Request, &req); err != nil {
		return view, domain.RunParams{}, err
	}
	return view, domain.RunParams{
		OutDir:      filepath.Join(s.runsDir, id),
		DatasetPath: req.DatasetPath,
		ControlTime: req.ControlTime,
		ExtraPaths:  req.ExtraPaths,
		Keywords:    append([]string(nil), req.Keywords...),
		Live:        req.Live,
		Sources:     append([]domain.LiveSource(nil), req.Sources...),
	}, nil
}

func (s *Server) freshID() string {
	for {
		var buf [4]byte
		_, _ = rand.Read(buf[:])
		id := time.Now().In(timeutil.MSK).Format("20060102T150405") + "-" + hex.EncodeToString(buf[:])
		s.mu.RLock()
		_, exists := s.runs[id]
		s.mu.RUnlock()
		if !exists && idOK(id) {
			return id
		}
	}
}

func (s *Server) persist(view runView) {
	raw, err := json.MarshalIndent(view, "", "  ")
	if err != nil {
		s.log.Error("статус", "run_id", view.RunID, "err", err.Error())
		return
	}
	raw = append(raw, '\n')
	path := filepath.Join(s.runsDir, view.RunID, "status.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		s.log.Error("статус", "run_id", view.RunID, "err", err.Error())
	}
}

func (run *runRec) upsert(stage string, done, total int) {
	if len(run.Stages) > 0 {
		last := &run.Stages[len(run.Stages)-1]
		if last.Name == stage {
			last.Done = done
			last.Total = total
			return
		}
	}
	run.Stages = append(run.Stages, stageProg{Name: stage, Done: done, Total: total})
}

func (run *runRec) view() runView {
	stages := append([]stageProg(nil), run.Stages...)
	if stages == nil {
		stages = []stageProg{}
	}
	extras := make([]string, 0, len(run.Params.ExtraPaths))
	for _, path := range run.Params.ExtraPaths {
		extras = append(extras, filepath.Base(path))
	}
	dataset := filepath.Base(run.Params.DatasetPath)
	if strings.TrimSpace(run.Params.DatasetPath) == "" && run.Params.Live {
		dataset = "сайты"
	}
	view := runView{
		RunID:       run.ID,
		Status:      run.Status,
		Stage:       run.Stage,
		Done:        run.Done,
		Total:       run.Total,
		Stages:      stages,
		Error:       run.Error,
		StartedAt:   formatStamp(run.StartedAt),
		FinishedAt:  formatStamp(run.FinishedAt),
		Dataset:     dataset,
		ParentRunID: run.ParentID,
		ExtraPaths:  extras,
	}
	if !run.Params.ControlTime.IsZero() {
		view.ControlTime = run.Params.ControlTime.Format(time.RFC3339)
	}
	return view
}

func formatStamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func runError(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "прогон отменён"
	case errors.Is(err, context.DeadlineExceeded):
		return "прогон прерван по таймауту"
	default:
		return err.Error()
	}
}

func idOK(id string) bool {
	if id == "" || len(id) > 80 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}
