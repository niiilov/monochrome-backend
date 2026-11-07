package verify

import (
	"strings"

	"cyberdigest/internal/domain"
)

const (
	// SevError blocks publication until a retry or a degraded retelling.
	SevError = "error"
	// SevWarn is recorded and does not by itself start a retry.
	SevWarn = "warn"

	RuleNumbers     = "numbers"
	RuleDates       = "dates"
	RuleURL         = "url"
	RuleID          = "id"
	RuleQuote       = "quote"
	RuleName        = "name"
	RuleHedge       = "hedge"
	RuleLimit       = "limit"
	RuleFormat      = "format"
	RuleStyle       = "style"
	RuleConsistency = "consistency"
	RuleEmpty       = "empty"
	RuleInjection   = "injection"
)

// Finding is one deterministic check result.
// Where is "digest", "post", "email", "format", or "event:<id>/<part>".
type Finding struct {
	Rule     string
	Severity string
	Where    string
	Message  string
}

// EventID returns the event id encoded in Where, or an empty string.
func (f Finding) EventID() string {
	const prefix = "event:"
	if !strings.HasPrefix(f.Where, prefix) {
		return ""
	}
	rest := f.Where[len(prefix):]
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i]
	}
	return rest
}

// Report converts the finding to the run_report.json shape.
func (f Finding) Report() domain.VerifyFinding {
	return domain.VerifyFinding{
		Rule:     f.Rule,
		Severity: f.Severity,
		Where:    f.Where,
		Message:  f.Message,
	}
}

func finding(rule, severity, where, message string) Finding {
	return Finding{Rule: rule, Severity: severity, Where: where, Message: message}
}

func eventWhere(id, part string) string {
	if part == "" {
		return "event:" + id
	}
	return "event:" + id + "/" + part
}

func hasError(findings []Finding) bool {
	for _, f := range findings {
		if f.Severity == SevError {
			return true
		}
	}
	return false
}
