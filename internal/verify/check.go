package verify

import (
	"cyberdigest/internal/domain"
	"cyberdigest/internal/render"
)

// Check runs every deterministic rule.
// events carry hedges and Rank. arts and reg are the dataset.
// post and emailText are the rendered artifacts. The digest text is rendered
// from m, and the same string is what the limit and consistency rules see
// except the limit of post, which uses the post argument.
func Check(m domain.DigestModel, post, emailText string, reg Registry, arts map[string]domain.Article, events []domain.Event) []Finding {
	if reg.byID == nil {
		reg = NewRegistry(arts)
	}
	if arts == nil {
		arts = map[string]domain.Article{}
	}
	digest := render.Digest(m)
	var out []Finding
	out = append(out, checkItems(m, reg, arts, events)...)
	out = append(out, checkLimits(digest, post)...)
	out = append(out, checkModelFormat(m, digest)...)
	out = append(out, checkRank(m, events)...)
	out = append(out, checkStyle(m, post, emailText)...)
	out = append(out, checkConsistency(digest, post, emailText)...)
	out = append(out, checkEmpty(m, digest, post, emailText)...)
	out = append(out, checkInjection(m, digest, post, emailText, reg)...)
	return out
}
