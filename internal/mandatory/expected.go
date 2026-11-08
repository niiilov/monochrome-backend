package mandatory

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"mono/internal/domain"
	"mono/internal/render"
)

// JournalExpect is one expected journal row.
// A nil Group means the group is not checked. An empty string requires an empty group.
type JournalExpect struct {
	ID             string   `yaml:"id"`
	Status         string   `yaml:"status"`
	Group          *string  `yaml:"group"`
	ReasonCode     string   `yaml:"reason_code"`
	ReasonContains string   `yaml:"reason_contains"`
	ErrorContains  string   `yaml:"error_contains"`
	ErrorEmpty     bool     `yaml:"error_empty"`
	RequireFlags   []string `yaml:"require_flags"`
}

// DigestExpect lists substrings of mono.md.
// Every MustContain item must appear. Every MustNotContain item must be absent.
// Each MustContainAny group needs at least one of its variants.
type DigestExpect struct {
	MustContain    []string   `yaml:"must_contain"`
	MustNotContain []string   `yaml:"must_not_contain"`
	MustContainAny [][]string `yaml:"must_contain_any"`
}

// TextExpect lists substrings of one more output file, for example post.txt.
type TextExpect struct {
	MustContain    []string `yaml:"must_contain"`
	MustNotContain []string `yaml:"must_not_contain"`
}

// Limits are rune caps after URLs are removed. Zero means the project default.
type Limits struct {
	Digest int `yaml:"digest"`
	Post   int `yaml:"post"`
}

// Expected is the contents of expected.yaml.
// Dataset is the file list for the future runner, relative to the scenario directory.
// Recipients and AllowEmails are the only addresses allowed in the output.
// AllowURLs is the source registry: a URL outside it fails the check.
type Expected struct {
	ID          string                `yaml:"id" json:"id"`
	Title       string                `yaml:"title" json:"title"`
	ControlTime string                `yaml:"control_time" json:"control_time"`
	Dataset     []string              `yaml:"dataset" json:"dataset"`
	Events      int                   `yaml:"events" json:"events"`
	Order       []string              `yaml:"order" json:"order,omitempty"`
	Recipients  []string              `yaml:"recipients" json:"recipients"`
	Journal     []JournalExpect       `yaml:"journal" json:"journal"`
	Digest      DigestExpect          `yaml:"digest" json:"digest"`
	Texts       map[string]TextExpect `yaml:"texts" json:"texts,omitempty"`
	Limits      Limits                `yaml:"limits" json:"limits"`
	AllowURLs   []string              `yaml:"allow_urls" json:"allow_urls"`
	AllowEmails []string              `yaml:"allow_emails" json:"allow_emails"`
}

// Load reads expected.yaml and fills default limits.
func Load(path string) (Expected, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Expected{}, fmt.Errorf("чтение %s: %w", path, err)
	}
	var exp Expected
	if err := yaml.Unmarshal(raw, &exp); err != nil {
		return Expected{}, fmt.Errorf("разбор %s: %w", path, err)
	}
	if err := exp.validate(); err != nil {
		return Expected{}, fmt.Errorf("разбор %s: %w", path, err)
	}
	exp.applyDefaults()
	return exp, nil
}

func (e *Expected) applyDefaults() {
	if e.Limits.Digest == 0 {
		e.Limits.Digest = render.DigestLimit
	}
	if e.Limits.Post == 0 {
		e.Limits.Post = render.PostLimit
	}
	if e.Recipients == nil {
		e.Recipients = []string{}
	}
	if e.AllowEmails == nil {
		e.AllowEmails = []string{}
	}
	if e.AllowURLs == nil {
		e.AllowURLs = []string{}
	}
}

func (e Expected) validate() error {
	if strings.TrimSpace(e.ID) == "" {
		return fmt.Errorf("нет id")
	}
	if e.Events < 0 {
		return fmt.Errorf("число событий отрицательное")
	}
	if e.Order != nil && len(e.Order) != e.Events {
		return fmt.Errorf("длина order %d не равна числу событий %d", len(e.Order), e.Events)
	}
	seen := map[string]bool{}
	for _, row := range e.Journal {
		id := strings.TrimSpace(row.ID)
		if id == "" {
			return fmt.Errorf("пустой id в журнале")
		}
		if seen[id] {
			return fmt.Errorf("повтор id %s", id)
		}
		seen[id] = true
		if !validStatus(row.Status) {
			return fmt.Errorf("статус %q недопустим для id %s", row.Status, id)
		}
		if row.ReasonCode != "" && !validReason(row.ReasonCode) {
			return fmt.Errorf("код причины %q недопустим для id %s", row.ReasonCode, id)
		}
	}
	return nil
}

func validStatus(status string) bool {
	switch domain.Status(status) {
	case domain.StatusIncluded, domain.StatusMerged, domain.StatusExcluded:
		return true
	default:
		return false
	}
}

func validReason(code string) bool {
	switch domain.ReasonCode(code) {
	case domain.ReasonSelected, domain.ReasonMerged, domain.ReasonOffTopic, domain.ReasonAdvertising,
		domain.ReasonOutOfPeriod, domain.ReasonDateUnconfirmed, domain.ReasonStaleNoNewFacts,
		domain.ReasonInsufficient, domain.ReasonLowerPriority, domain.ReasonReadError,
		domain.ReasonEmptyInput, domain.ReasonDuplicateID, domain.ReasonSourceDown:
		return true
	default:
		return false
	}
}
