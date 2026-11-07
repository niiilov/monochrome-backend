package journal

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"cyberdigest/internal/domain"
)

// Journal is a thread-safe accumulator. One ID keeps one row, in the order of the first Add or Set.
type Journal struct {
	mu      sync.Mutex
	order   []string
	byID    map[string]*domain.JournalEntry
	pending map[string][]domain.StageNote
}

// New returns an empty journal.
func New() *Journal {
	return &Journal{
		byID:    map[string]*domain.JournalEntry{},
		pending: map[string][]domain.StageNote{},
	}
}

// Add inserts or replaces the row for e.ID. The first position of that ID is kept.
// Notes recorded before the row existed are appended to Trace.
func (j *Journal) Add(e domain.JournalEntry) {
	e.ID = strings.TrimSpace(e.ID)
	if e.ID == "" {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	cp := cloneEntry(e)
	if notes := j.pending[cp.ID]; len(notes) > 0 {
		cp.Trace = append(cp.Trace, notes...)
		delete(j.pending, cp.ID)
	}
	if cur, ok := j.byID[cp.ID]; ok {
		*cur = cp
		return
	}
	j.byID[cp.ID] = &cp
	j.order = append(j.order, cp.ID)
}

// Set writes the decision fields of one row. Source, topic, flags and trace already stored are kept.
func (j *Journal) Set(id string, status domain.Status, group string, code domain.ReasonCode, reason, errText string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	e := j.ensure(id)
	e.Status = status
	e.Group = strings.TrimSpace(group)
	e.ReasonCode = code
	e.Reason = reason
	e.Error = errText
}

// Note appends a trace line. A note for an unknown ID is kept until Add or Set creates the row.
func (j *Journal) Note(id, stage, text string) {
	id = strings.TrimSpace(id)
	text = strings.TrimSpace(text)
	if id == "" || text == "" {
		return
	}
	note := domain.StageNote{Stage: strings.TrimSpace(stage), Note: text}
	j.mu.Lock()
	defer j.mu.Unlock()
	if e, ok := j.byID[id]; ok {
		e.Trace = append(e.Trace, note)
		return
	}
	j.pending[id] = append(j.pending[id], note)
}

// Get returns a copy of the row.
func (j *Journal) Get(id string) (domain.JournalEntry, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	e, ok := j.byID[strings.TrimSpace(id)]
	if !ok {
		return domain.JournalEntry{}, false
	}
	return cloneEntry(*e), true
}

// Entries returns copies in dataset order.
func (j *Journal) Entries() []domain.JournalEntry {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.snapshot()
}

// Validate checks that the journal has exactly one row for every article ID,
// that every status is one of the three allowed values, and that "объединён" has a group.
func (j *Journal) Validate(articleIDs []string) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	want := make(map[string]struct{}, len(articleIDs))
	var problems []string
	for _, id := range articleIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			problems = append(problems, "пустой id в списке набора")
			continue
		}
		want[id] = struct{}{}
	}
	for _, id := range j.order {
		e := j.byID[id]
		if !validStatus(e.Status) {
			problems = append(problems, fmt.Sprintf("статус %q недопустим для id %s", e.Status, id))
		}
		if e.Status == domain.StatusMerged && strings.TrimSpace(e.Group) == "" {
			problems = append(problems, fmt.Sprintf("статус «объединён» без группы у id %s", id))
		}
		if _, ok := want[id]; !ok {
			problems = append(problems, fmt.Sprintf("лишняя запись id %s", id))
		}
	}
	for _, id := range articleIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := j.byID[id]; !ok {
			problems = append(problems, fmt.Sprintf("нет записи для id %s", id))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("журнал: %s", strings.Join(problems, "; "))
}

// WriteCSV writes UTF-8 without a BOM. Columns are exactly id,status,group,reason,error.
// Row order is the order of the first Add or Set.
func (j *Journal) WriteCSV(w io.Writer) error {
	j.mu.Lock()
	rows := j.snapshot()
	j.mu.Unlock()

	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"id", "status", "group", "reason", "error"}); err != nil {
		return fmt.Errorf("журнал csv: %w", err)
	}
	for _, e := range rows {
		rec := []string{e.ID, string(e.Status), e.Group, e.Reason, e.Error}
		if err := cw.Write(rec); err != nil {
			return fmt.Errorf("журнал csv: %w", err)
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return fmt.Errorf("журнал csv: %w", err)
	}
	return nil
}

// WriteJSON writes the rows as a JSON array, including reason_code and the extended fields.
func (j *Journal) WriteJSON(w io.Writer) error {
	j.mu.Lock()
	rows := j.snapshot()
	j.mu.Unlock()

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rows); err != nil {
		return fmt.Errorf("журнал json: %w", err)
	}
	return nil
}

func (j *Journal) ensure(id string) *domain.JournalEntry {
	if e, ok := j.byID[id]; ok {
		return e
	}
	e := &domain.JournalEntry{ID: id}
	if notes := j.pending[id]; len(notes) > 0 {
		e.Trace = append([]domain.StageNote(nil), notes...)
		delete(j.pending, id)
	}
	j.byID[id] = e
	j.order = append(j.order, id)
	return e
}

func (j *Journal) snapshot() []domain.JournalEntry {
	out := make([]domain.JournalEntry, 0, len(j.order))
	for _, id := range j.order {
		out = append(out, cloneEntry(*j.byID[id]))
	}
	return out
}

func validStatus(status domain.Status) bool {
	switch status {
	case domain.StatusIncluded, domain.StatusMerged, domain.StatusExcluded:
		return true
	default:
		return false
	}
}

func cloneEntry(e domain.JournalEntry) domain.JournalEntry {
	out := e
	if e.Flags != nil {
		out.Flags = append([]string(nil), e.Flags...)
	}
	if e.Trace != nil {
		out.Trace = append([]domain.StageNote(nil), e.Trace...)
	}
	if e.Topic != nil {
		topic := *e.Topic
		if e.Topic.MatchedTerms != nil {
			topic.MatchedTerms = append([]string(nil), e.Topic.MatchedTerms...)
		}
		out.Topic = &topic
	}
	return out
}
