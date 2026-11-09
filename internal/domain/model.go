// Package domain holds the shared data contracts of the digest pipeline.
// JSON names are snake_case so journal.json and run_report.json stay stable.
package domain

import "time"

// SourceKind is the kind of a publication source.
type SourceKind string

const (
	SourceMedia     SourceKind = "media"     // newsroom
	SourceAuthor    SourceKind = "author"    // platform author
	SourceVendor    SourceKind = "vendor"    // vendor
	SourceRegulator SourceKind = "regulator" // regulator
	SourceUnknown   SourceKind = "unknown"
)

// Article is one normalized publication from a dataset.
type Article struct {
	ID            string     `json:"id"`
	Title         string     `json:"title"`
	Text          string     `json:"text"`
	RawText       string     `json:"raw_text"`
	Source        string     `json:"source"`
	URL           string     `json:"url"`
	PublishedAt   *time.Time `json:"published_at"`
	PublishedRaw  string     `json:"published_raw"`
	SourceKind    SourceKind `json:"source_kind"`
	MissingFields []string   `json:"missing_fields"`
	ReadError     string     `json:"read_error"`
	Flags         []string   `json:"flags"`
}

// Status is the journal status of one dataset item.
type Status string

const (
	StatusIncluded Status = "включён"
	StatusMerged   Status = "объединён"
	StatusExcluded Status = "исключён"
)

// ReasonCode is a stable reason identifier. Reason text is Russian.
type ReasonCode string

const (
	ReasonSelected        ReasonCode = "selected"
	ReasonMerged          ReasonCode = "merged"
	ReasonOffTopic        ReasonCode = "off_topic"
	ReasonAdvertising     ReasonCode = "advertising"
	ReasonOutOfPeriod     ReasonCode = "out_of_period"
	ReasonDateUnconfirmed ReasonCode = "date_unconfirmed"
	ReasonStaleNoNewFacts ReasonCode = "stale_no_new_facts"
	ReasonInsufficient    ReasonCode = "insufficient_text"
	ReasonLowerPriority   ReasonCode = "lower_priority"
	ReasonReadError       ReasonCode = "read_error"
	ReasonEmptyInput      ReasonCode = "empty_input"
	ReasonDuplicateID     ReasonCode = "duplicate_id"
	ReasonSourceDown      ReasonCode = "source_unavailable"
)

// JournalEntry is one journal row for every dataset item.
type JournalEntry struct {
	ID             string       `json:"id"`
	Status         Status       `json:"status"`
	Group          string       `json:"group"`
	ReasonCode     ReasonCode   `json:"reason_code"`
	Reason         string       `json:"reason"`
	Error          string       `json:"error"`
	Source         string       `json:"source"`
	SourceKind     SourceKind   `json:"source_kind"`
	PublishedAtMSK string       `json:"published_at_msk"`
	Topic          *TopicResult `json:"topic"`
	Flags          []string     `json:"flags"`
	Trace          []StageNote  `json:"trace"`
}

// StageNote is one trace line attached to a journal entry.
type StageNote struct {
	Stage string `json:"stage"`
	Note  string `json:"note"`
}

// TopicResult is the topic and advertising classification of an article.
type TopicResult struct {
	IsOnTopic     bool     `json:"is_on_topic"`
	IsAdvertising bool     `json:"is_advertising"`
	Confidence    float64  `json:"confidence"`
	MatchedTerms  []string `json:"matched_terms"`
	Reason        string   `json:"reason"`
}

// Fact is one claim backed by a verbatim quote from an article.
type Fact struct {
	Text      string `json:"text"`
	Quote     string `json:"quote"`
	ArticleID string `json:"article_id"`
}

// Mention is one event mention inside a single article.
type Mention struct {
	ID                 string   `json:"id"`
	ArticleID          string   `json:"article_id"`
	TitleDraft         string   `json:"title_draft"`
	Facts              []Fact   `json:"facts"`
	Affected           string   `json:"affected"`
	Consequences       string   `json:"consequences"`
	Hedges             []string `json:"hedges"`
	Entities           []string `json:"entities"`
	IsCompanyStatement bool     `json:"is_company_statement"`
	IsReprint          bool     `json:"is_reprint"`
	Reporter           string   `json:"reporter"`
	ReferencesEarlier  bool     `json:"references_earlier"`
	NewFacts           []string `json:"new_facts"`
	SufficientText     bool     `json:"sufficient_text"`
}

// Conflict is a disagreement between sources about one field.
type Conflict struct {
	Field       string   `json:"field"`
	Description string   `json:"description"`
	ArticleIDs  []string `json:"article_ids"`
}

// Scores holds model scores. A nil criterion means unknown.
type Scores struct {
	Impact    *int              `json:"impact"`
	Scale     *int              `json:"scale"`
	Urgency   *int              `json:"urgency"`
	Relevance *int              `json:"relevance"`
	Evidence  map[string]string `json:"evidence"`
}

// Event is a group of mentions about one incident.
type Event struct {
	ID                 string     `json:"id"`
	Mentions           []Mention  `json:"mentions"`
	ArticleIDs         []string   `json:"article_ids"`
	PrimaryArticleID   string     `json:"primary_article_id"`
	ContextArticleIDs  []string   `json:"context_article_ids"`
	Conflicts          []Conflict `json:"conflicts"`
	Scores             Scores     `json:"scores"`
	Total              float64    `json:"total"`
	Rank               int        `json:"rank"`
	Selected           bool       `json:"selected"`
	Rationale          string     `json:"rationale"`
	IndependentSources int        `json:"independent_sources"`
}

// SourceRef is one source line inside a digest item.
type SourceRef struct {
	ArticleID string `json:"article_id"`
	Name      string `json:"name"`
	Date      string `json:"date"`
	URL       string `json:"url"`
	Role      string `json:"role"`
}

// DigestItem is one numbered event in the digest.
type DigestItem struct {
	N            int         `json:"n"`
	EventID      string      `json:"event_id"`
	Title        string      `json:"title"`
	Essence      string      `json:"essence"`
	Consequences string      `json:"consequences"`
	Sources      []SourceRef `json:"sources"`
	Rationale    string      `json:"rationale"`
}

// NotIncluded explains why an article stayed out of the digest.
type NotIncluded struct {
	ArticleID string `json:"article_id"`
	Reason    string `json:"reason"`
}

// Period is the half-open window (Start, End] in Moscow time.
type Period struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// Contains reports whether t is inside (Start, End].
// The method lives here because a Go method must be declared in the type's package.
func (p Period) Contains(t time.Time) bool {
	return t.After(p.Start) && !t.After(p.End)
}

// DigestModel is the structured digest before rendering.
type DigestModel struct {
	Period      Period        `json:"period"`
	ControlTime time.Time     `json:"control_time"`
	Items       []DigestItem  `json:"items"`
	Shortfall   string        `json:"shortfall"`
	NotIncluded []NotIncluded `json:"not_included"`
	Empty       bool          `json:"empty"`
}

// RunParams are the inputs of one pipeline run.
// Keywords are optional terms from the request. Events whose articles contain
// one of them are ordered ahead of the other on-topic events.
type RunParams struct {
	DatasetPath string    `json:"dataset_path"`
	ControlTime time.Time `json:"control_time"`
	OutDir      string    `json:"out_dir"`
	ExtraPaths  []string  `json:"extra_paths"`
	Keywords    []string  `json:"keywords,omitempty"`
	// Live collects the sites from sources.yaml for this run.
	// The global live.enabled switch stays off for everyone else.
	Live bool `json:"live,omitempty"`
}

// RunState is the in-memory result of a pipeline run.
type RunState struct {
	Params   RunParams                `json:"params"`
	Period   Period                   `json:"period"`
	Articles []Article                `json:"articles"`
	Context  []Article                `json:"context"`
	Journal  map[string]*JournalEntry `json:"journal"`
	Mentions []Mention                `json:"mentions"`
	Events   []Event                  `json:"events"`
	Digest   DigestModel              `json:"digest"`
}
