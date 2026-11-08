package api

import (
	"archive/zip"
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"mono/internal/domain"
)

// exportNames are the formal archive. Optional files are added when the runner wrote them.
var exportRequired = []string{
	"mono.json",
	"mono.md",
	"post.txt",
	"email_preview.html",
	"journal.csv",
	"run_report.json",
}

var exportOptional = []string{"email.txt", "journal.json"}

type digestFile struct {
	Period      domain.Period       `json:"period"`
	ControlTime string              `json:"control_time,omitempty"`
	Shortfall   string              `json:"shortfall,omitempty"`
	Empty       bool                `json:"empty"`
	Text        string              `json:"text"`
	Events      []domain.DigestItem `json:"events"`
}

func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	dir, ok := s.readyDir(w, r)
	if !ok {
		return
	}
	raw, err := zipDir(dir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	id := r.PathValue("id")
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=\"mono-"+id+".zip\"")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func finishFiles(dir, id string, state *domain.RunState, report *domain.RunReport) error {
	if err := ensureDigestJSON(dir, state); err != nil {
		return err
	}
	report.RunID = id
	if report.LLM.ByTask == nil {
		report.LLM.ByTask = map[string]domain.LLMTaskStats{}
	}
	if report.Verify.Findings == nil {
		report.Verify.Findings = []domain.VerifyFinding{}
	}
	if report.StageDurationsMS == nil {
		report.StageDurationsMS = map[string]int64{}
	}
	if report.ArtifactHashes == nil {
		report.ArtifactHashes = map[string]string{}
	}
	return writeJSONFile(filepath.Join(dir, "run_report.json"), report)
}

func ensureDigestJSON(dir string, state *domain.RunState) error {
	path := filepath.Join(dir, "mono.json")
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		return nil
	}
	text, _ := os.ReadFile(filepath.Join(dir, "mono.md"))
	doc := digestFile{Text: string(text), Events: []domain.DigestItem{}}
	if state != nil {
		doc.Period = state.Digest.Period
		if !state.Digest.ControlTime.IsZero() {
			doc.ControlTime = state.Digest.ControlTime.Format(time.RFC3339)
		}
		doc.Shortfall = state.Digest.Shortfall
		doc.Empty = state.Digest.Empty || len(state.Digest.Items) == 0
		if state.Digest.Items != nil {
			doc.Events = state.Digest.Items
		}
	}
	return writeJSONFile(path, doc)
}

func zipDir(dir string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range exportRequired {
		if err := addZip(zw, dir, name); err != nil {
			_ = zw.Close()
			return nil, fmt.Errorf("в архиве нет файла %s", name)
		}
	}
	for _, name := range exportOptional {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			continue
		}
		if err := addZip(zw, dir, name); err != nil {
			_ = zw.Close()
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func addZip(zw *zip.Writer, dir, name string) error {
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return err
	}
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = w.Write(raw)
	return err
}
