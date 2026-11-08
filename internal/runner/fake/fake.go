// Package fake is a runner.Runner that replays testdata/fake_run.
// cmd/monod uses it with --fake. Without that flag the server calls runner.New.
package fake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mono/internal/domain"
	"mono/internal/runner"
)

// Stages are the pipeline steps the real runner reports, plus the final write.
var Stages = []string{
	"load", "normalize", "period", "guard", "topic", "extract",
	"cluster", "rank", "select", "summarize", "verify", "write",
}

// artifactNames are the prepared files copied into OutDir.
var artifactNames = []string{
	"mono.md",
	"mono.json",
	"post.txt",
	"email.txt",
	"email_preview.html",
	"journal.csv",
	"journal.json",
	"run_report.json",
}

// Runner replays a prepared run and reports stage progress.
type Runner struct {
	Dir   string
	Delay time.Duration
}

// New returns a runner that reads dir (typically testdata/fake_run).
func New(dir string) *Runner {
	return &Runner{Dir: dir}
}

// Run copies the prepared files into p.OutDir and returns the parsed state.
// ExtraPaths are kept on the state for a later real rerun; the text stays the fixture.
func (r *Runner) Run(ctx context.Context, p domain.RunParams, progress runner.ProgressFunc) (*domain.RunState, domain.RunReport, error) {
	if err := r.walk(ctx, progress); err != nil {
		return nil, domain.RunReport{}, err
	}
	if strings.TrimSpace(p.OutDir) == "" {
		return nil, domain.RunReport{}, errors.New("не задан каталог результата")
	}
	if strings.TrimSpace(r.Dir) == "" {
		return nil, domain.RunReport{}, errors.New("не задан каталог подготовленного прогона")
	}
	if err := os.MkdirAll(p.OutDir, 0o755); err != nil {
		return nil, domain.RunReport{}, fmt.Errorf("каталог результата: %w", err)
	}
	for _, name := range artifactNames {
		if err := copyFile(filepath.Join(r.Dir, name), filepath.Join(p.OutDir, name)); err != nil {
			return nil, domain.RunReport{}, err
		}
	}
	state, report, err := readState(p.OutDir)
	if err != nil {
		return nil, domain.RunReport{}, err
	}
	state.Params = p
	report.ArtifactHashes = hashArtifacts(p.OutDir)
	if err := writeJSON(filepath.Join(p.OutDir, "run_report.json"), report); err != nil {
		return nil, domain.RunReport{}, err
	}
	return state, report, nil
}

func (r *Runner) walk(ctx context.Context, progress runner.ProgressFunc) error {
	for _, stage := range Stages {
		if err := ctx.Err(); err != nil {
			return err
		}
		if progress != nil {
			progress(stage, 0, 1)
		}
		if r.Delay > 0 {
			timer := time.NewTimer(r.Delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		if progress != nil {
			progress(stage, 1, 1)
		}
	}
	return nil
}

func readState(dir string) (*domain.RunState, domain.RunReport, error) {
	var doc digestFile
	if err := readJSON(filepath.Join(dir, "mono.json"), &doc); err != nil {
		return nil, domain.RunReport{}, err
	}
	if strings.TrimSpace(doc.Text) == "" {
		raw, err := os.ReadFile(filepath.Join(dir, "mono.md"))
		if err != nil {
			return nil, domain.RunReport{}, fmt.Errorf("чтение mono.md: %w", err)
		}
		doc.Text = string(raw)
	}
	var rows []domain.JournalEntry
	if err := readJSON(filepath.Join(dir, "journal.json"), &rows); err != nil {
		return nil, domain.RunReport{}, err
	}
	var report domain.RunReport
	if err := readJSON(filepath.Join(dir, "run_report.json"), &report); err != nil {
		return nil, domain.RunReport{}, err
	}
	journal := make(map[string]*domain.JournalEntry, len(rows))
	for i := range rows {
		journal[rows[i].ID] = &rows[i]
	}
	state := &domain.RunState{
		Journal: journal,
		Digest: domain.DigestModel{
			Period:      doc.Period,
			ControlTime: doc.ControlTime,
			Items:       doc.Events,
			Shortfall:   doc.Shortfall,
			Empty:       doc.Empty,
		},
	}
	return state, report, nil
}

type digestFile struct {
	Period      domain.Period       `json:"period"`
	ControlTime time.Time           `json:"control_time"`
	Shortfall   string              `json:"shortfall"`
	Empty       bool                `json:"empty"`
	Text        string              `json:"text"`
	Events      []domain.DigestItem `json:"events"`
}

func copyFile(src, dst string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("чтение %s: %w", filepath.Base(src), err)
	}
	if err := os.WriteFile(dst, raw, 0o644); err != nil {
		return fmt.Errorf("запись %s: %w", filepath.Base(dst), err)
	}
	return nil
}

func hashArtifacts(dir string) map[string]string {
	out := make(map[string]string, len(artifactNames))
	for _, name := range artifactNames {
		if name == "run_report.json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		sum := sha256.Sum256(raw)
		out[name] = hex.EncodeToString(sum[:])
	}
	return out
}

func readJSON(path string, dest any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("чтение %s: %w", filepath.Base(path), err)
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return fmt.Errorf("разбор %s: %w", filepath.Base(path), err)
	}
	return nil
}

func writeJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("запись %s: %w", filepath.Base(path), err)
	}
	return nil
}
