package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"mono/internal/domain"
)

// historyItem is one row of the digest history for the frontend.
type historyItem struct {
	RunID       string `json:"run_id"`
	Status      string `json:"status"`
	StartedAt   string `json:"started_at,omitempty"`
	FinishedAt  string `json:"finished_at,omitempty"`
	ControlTime string `json:"control_time,omitempty"`
	Dataset     string `json:"dataset,omitempty"`
	ParentRunID string `json:"parent_run_id,omitempty"`
	Events      *int   `json:"events,omitempty"`
	PeriodStart string `json:"period_start,omitempty"`
	PeriodEnd   string `json:"period_end,omitempty"`
	ExportURL   string `json:"export_url,omitempty"`

	started time.Time
}

type historyResponse struct {
	Runs []historyItem `json:"runs"`
}

// history lists stored runs, newest first.
// GET /api/v1/runs?status=done returns only finished digests that can be downloaded.
func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	want := r.URL.Query().Get("status")
	switch want {
	case "", statusRunning, statusDone, statusFailed:
	default:
		writeErr(w, http.StatusBadRequest, "статус должен быть running, done или failed")
		return
	}
	items, err := s.listHistory(r.Context(), currentUser(r).ID, want)
	if err != nil {
		s.databaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, historyResponse{Runs: items})
}

type historySnap struct {
	view    runView
	dir     string
	started time.Time
}

func (s *Server) listHistory(ctx context.Context, userID, status string) ([]historyItem, error) {
	runs, err := s.store.ListRuns(ctx, userID, status)
	if err != nil {
		return nil, err
	}
	snaps := make([]historySnap, 0, len(runs))
	for _, run := range runs {
		var view runView
		if err := json.Unmarshal(run.Snapshot, &view); err != nil {
			return nil, err
		}
		snaps = append(snaps, historySnap{view: view, dir: filepath.Join(s.runsDir, run.ID), started: run.StartedAt})
	}

	items := make([]historyItem, 0, len(snaps))
	for _, snap := range snaps {
		view := snap.view
		item := historyItem{
			RunID:       view.RunID,
			Status:      view.Status,
			StartedAt:   view.StartedAt,
			FinishedAt:  view.FinishedAt,
			ControlTime: view.ControlTime,
			Dataset:     view.Dataset,
			ParentRunID: view.ParentRunID,
			started:     snap.started,
		}
		if events, start, end, ok := readHistoryReport(snap.dir); ok {
			item.Events = &events
			item.PeriodStart = start
			item.PeriodEnd = end
		}
		if view.Status == statusDone && archiveReady(snap.dir) {
			item.ExportURL = "/api/v1/runs/" + view.RunID + "/export"
		}
		items = append(items, item)
	}
	return items, nil
}

func readHistoryReport(dir string) (events int, start, end string, ok bool) {
	raw, err := os.ReadFile(filepath.Join(dir, "run_report.json"))
	if err != nil {
		return 0, "", "", false
	}
	var report domain.RunReport
	if json.Unmarshal(raw, &report) != nil {
		return 0, "", "", false
	}
	return report.Counts.Events, report.Period.Start, report.Period.End, true
}

func archiveReady(dir string) bool {
	groups := [][]string{
		{"digest.md", "mono.md"},
		{"post.md", "post.txt"},
		{"email.md", "email.txt"},
		{"journal.csv"},
	}
	for _, names := range groups {
		info, err := os.Stat(firstExisting(dir, names...))
		if err != nil || info.IsDir() {
			return false
		}
	}
	return true
}
