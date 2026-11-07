package domain

// LLMTaskStats is token usage of one named model task.
type LLMTaskStats struct {
	Calls        int `json:"calls"`
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// LLMStats is the model usage block of run_report.json.
// Cost is nil when the price is not configured.
type LLMStats struct {
	Provider        string                  `json:"provider"`
	Model           string                  `json:"model"`
	Temperature     float64                 `json:"temperature"`
	CallsTotal      int                     `json:"calls_total"`
	ByTask          map[string]LLMTaskStats `json:"by_task"`
	InputTokens     int                     `json:"input_tokens"`
	OutputTokens    int                     `json:"output_tokens"`
	Cost            *float64                `json:"cost"`
	CostUnavailable bool                    `json:"cost_unavailable"`
}

// ReportPeriod is the period block of run_report.json.
// Values are already formatted timestamps, not domain.Period.
type ReportPeriod struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// RunCounts are item and event counters of one run.
type RunCounts struct {
	Total    int `json:"total"`
	Included int `json:"included"`
	Merged   int `json:"merged"`
	Excluded int `json:"excluded"`
	Events   int `json:"events"`
}

// VerifyFinding is one deterministic or critic finding stored in the run report.
type VerifyFinding struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Where    string `json:"where"`
	Message  string `json:"message"`
}

// VerifyReport is the verification block of run_report.json.
type VerifyReport struct {
	Retries  int             `json:"retries"`
	Findings []VerifyFinding `json:"findings"`
	Degraded bool            `json:"degraded"`
}

// RunReport is the on-disk run_report.json document.
type RunReport struct {
	RunID            string            `json:"run_id"`
	StartedAt        string            `json:"started_at"`
	FinishedAt       string            `json:"finished_at"`
	DurationMS       int64             `json:"duration_ms"`
	ControlTime      string            `json:"control_time"`
	Period           ReportPeriod      `json:"period"`
	AppVersion       string            `json:"app_version"`
	GoVersion        string            `json:"go_version"`
	LLM              LLMStats          `json:"llm"`
	PromptsHash      string            `json:"prompts_hash"`
	ConfigHash       string            `json:"config_hash"`
	DatasetHash      string            `json:"dataset_hash"`
	Counts           RunCounts         `json:"counts"`
	StageDurationsMS map[string]int64  `json:"stage_durations_ms"`
	Verify           VerifyReport      `json:"verify"`
	ArtifactHashes   map[string]string `json:"artifact_hashes"`
}
