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
	"sync"
	"time"

	"mono/internal/domain"
	"mono/internal/mailer"
	"mono/internal/runner"
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
	runner  runner.Runner
	mail    *mailer.Mailer
	runsDir string
	log     *slog.Logger

	mu     sync.RWMutex
	fileMu sync.Mutex
	runs   map[string]*runRec
	wg     sync.WaitGroup

	ctx    context.Context
	cancel context.CancelFunc
}

type runRec struct {
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
	DatasetPath string    `json:"dataset_path"`
	ControlTime time.Time `json:"control_time"`
	ExtraPaths  []string  `json:"extra_paths,omitempty"`
	ParentRunID string    `json:"parent_run_id,omitempty"`
}

// New stores runs under runsDir and serves until Shutdown.
func New(r runner.Runner, m *mailer.Mailer, runsDir string, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		runner:  r,
		mail:    m,
		runsDir: runsDir,
		log:     log,
		runs:    map[string]*runRec{},
		ctx:     ctx,
		cancel:  cancel,
	}
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		log.Error("каталог прогонов", "err", err.Error())
	}
	s.loadExisting()
	return s
}

// Handler is the HTTP API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /api/v1/healthz", s.health)
	mux.HandleFunc("POST /api/v1/runs", s.create)
	mux.HandleFunc("GET /api/v1/runs", s.history)
	mux.HandleFunc("GET /api/v1/runs/{id}", s.status)
	mux.HandleFunc("GET /api/v1/runs/{id}/digest", s.digest)
	mux.HandleFunc("GET /api/v1/runs/{id}/post", s.post)
	mux.HandleFunc("GET /api/v1/runs/{id}/email", s.email)
	mux.HandleFunc("GET /api/v1/runs/{id}/journal", s.journal)
	mux.HandleFunc("GET /api/v1/runs/{id}/report", s.report)
	mux.HandleFunc("GET /api/v1/runs/{id}/export", s.export)
	mux.HandleFunc("POST /api/v1/runs/{id}/send", s.send)
	mux.HandleFunc("POST /api/v1/runs/{id}/articles", s.articles)
	return mux
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

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
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

func (s *Server) launch(id, dir string, params domain.RunParams, parent string) error {
	if err := s.ctx.Err(); err != nil {
		return errors.New("сервер останавливается")
	}
	if err := os.MkdirAll(filepath.Join(dir, "input"), 0o755); err != nil {
		return fmt.Errorf("каталог прогона: %w", err)
	}
	params.OutDir = dir
	rec := &runRec{
		ID:        id,
		Status:    statusRunning,
		StartedAt: time.Now(),
		Params:    params,
		ParentID:  parent,
		Stages:    []stageProg{},
	}
	if err := writeJSONFile(filepath.Join(dir, "request.json"), storedRequest{
		DatasetPath: params.DatasetPath,
		ControlTime: params.ControlTime,
		ExtraPaths:  append([]string(nil), params.ExtraPaths...),
		ParentRunID: parent,
	}); err != nil {
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
	s.mu.Unlock()
	s.persist(view)
}

func (s *Server) lookup(id string) (runView, domain.RunParams, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	run := s.runs[id]
	if run == nil {
		return runView{}, domain.RunParams{}, false
	}
	return run.view(), run.Params, true
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

func (s *Server) loadExisting() {
	entries, err := os.ReadDir(s.runsDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !idOK(entry.Name()) {
			continue
		}
		dir := filepath.Join(s.runsDir, entry.Name())
		raw, err := os.ReadFile(filepath.Join(dir, "status.json"))
		if err != nil {
			continue
		}
		var view runView
		if json.Unmarshal(raw, &view) != nil || !idOK(view.RunID) {
			continue
		}
		rec := view.rec()
		rec.Params.OutDir = dir
		if req, err := readRequest(dir); err == nil {
			rec.Params.DatasetPath = req.DatasetPath
			rec.Params.ControlTime = req.ControlTime
			rec.Params.ExtraPaths = append([]string(nil), req.ExtraPaths...)
			rec.ParentID = req.ParentRunID
		}
		if rec.Status == statusRunning {
			rec.Status = statusFailed
			rec.Error = "сервер перезапущен до конца прогона"
			s.persist(rec.view())
		}
		s.runs[rec.ID] = rec
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
		Dataset:     filepath.Base(run.Params.DatasetPath),
		ParentRunID: run.ParentID,
		ExtraPaths:  extras,
	}
	if !run.Params.ControlTime.IsZero() {
		view.ControlTime = run.Params.ControlTime.Format(time.RFC3339)
	}
	return view
}

func (view runView) rec() *runRec {
	rec := &runRec{
		ID:         view.RunID,
		Status:     view.Status,
		Stage:      view.Stage,
		Done:       view.Done,
		Total:      view.Total,
		Stages:     append([]stageProg(nil), view.Stages...),
		Error:      view.Error,
		StartedAt:  parseStamp(view.StartedAt),
		FinishedAt: parseStamp(view.FinishedAt),
		ParentID:   view.ParentRunID,
	}
	if rec.Stages == nil {
		rec.Stages = []stageProg{}
	}
	return rec
}

func formatStamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func parseStamp(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return t
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

func readRequest(dir string) (storedRequest, error) {
	var req storedRequest
	raw, err := os.ReadFile(filepath.Join(dir, "request.json"))
	if err != nil {
		return req, err
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return storedRequest{}, err
	}
	return req, nil
}
