package extract

import (
	"context"
	"log/slog"
	"strings"
	"unicode/utf8"
)

const stageExtract = "extract"

// Noter receives trace lines for the journal. *journal.Journal implements it.
type Noter interface {
	Note(id, stage, text string)
}

type noterKey struct{}

// WithNoter attaches a trace sink. Mentions and cluster.Build write dropped quotes
// and pair decisions here. A nil noter leaves the context unchanged.
func WithNoter(ctx context.Context, n Noter) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if n == nil {
		return ctx
	}
	return context.WithValue(ctx, noterKey{}, n)
}

// Note writes one trace line when ctx carries a Noter from WithNoter.
// The line is also logged without the article body.
func Note(ctx context.Context, stage, articleID, text string) {
	text = strings.TrimSpace(text)
	articleID = strings.TrimSpace(articleID)
	stage = strings.TrimSpace(stage)
	if text == "" || articleID == "" {
		return
	}
	if stage == "" {
		stage = stageExtract
	}
	slog.Info("pipeline", "stage", stage, "article_id", articleID, "note", clip(text, 240))
	if ctx == nil {
		return
	}
	n, _ := ctx.Value(noterKey{}).(Noter)
	if n == nil {
		return
	}
	n.Note(articleID, stage, text)
}

func clip(s string, n int) string {
	if n <= 0 || utf8.RuneCountInString(s) <= n {
		return s
	}
	rs := []rune(s)
	return string(rs[:n])
}
