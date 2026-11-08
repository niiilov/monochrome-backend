package critic

import (
	"context"
	"fmt"
	"strings"

	"mono/internal/domain"
	"mono/internal/llm"
	"mono/internal/verify"
)

// GateCritic adapts Check to verify.Critic.
// Unsupported claims become error findings so Gate rewrites the event.
type GateCritic struct {
	Client llm.Client
}

// CheckItem implements verify.Critic.
func (g GateCritic) CheckItem(ctx context.Context, item domain.DigestItem, arts map[string]domain.Article) ([]verify.Finding, error) {
	if g.Client == nil {
		return nil, fmt.Errorf("critic: клиент LLM не задан")
	}
	rep, err := Check(ctx, g.Client, item, arts)
	if err != nil {
		return nil, err
	}
	var out []verify.Finding
	for _, claim := range rep.Claims {
		switch claim.Verdict {
		case VerdictUnsupported:
			msg := "критик: не подтверждено источником: " + claim.Text
			if reason := strings.TrimSpace(claim.Reason); reason != "" {
				msg += " (" + reason + ")"
			}
			out = append(out, verify.Finding{
				Rule:     verify.RuleCritic,
				Severity: verify.SevError,
				Where:    "event:" + item.EventID + "/essence",
				Message:  msg,
			})
		case VerdictUncertain:
			msg := "критик: неясно по источникам: " + claim.Text
			if reason := strings.TrimSpace(claim.Reason); reason != "" {
				msg += " (" + reason + ")"
			}
			out = append(out, verify.Finding{
				Rule:     verify.RuleCritic,
				Severity: verify.SevWarn,
				Where:    "event:" + item.EventID + "/essence",
				Message:  msg,
			})
		}
	}
	return out, nil
}
