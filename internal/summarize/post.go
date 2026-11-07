package summarize

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"cyberdigest/internal/domain"
	"cyberdigest/internal/llm"
	"cyberdigest/internal/prompts"
	"cyberdigest/internal/textutil"
)

const postSchemaJSON = `{"type":"object","additionalProperties":false,"required":["items"],"properties":{"items":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["n","title","text"],"properties":{"n":{"type":"integer"},"title":{"type":"string"},"text":{"type":"string"}}}}}}`

// ShortItem is one numbered event in the short post.
// URLs are copied from the digest item so the model cannot drop or invent a link.
type ShortItem struct {
	N     int
	Title string
	Text  string
	URLs  []string
}

type postJSON struct {
	Items []struct {
		N     int    `json:"n"`
		Title string `json:"title"`
		Text  string `json:"text"`
	} `json:"items"`
}

type postView struct {
	Limit    int
	Items    []postEntry
	Previous []prevEntry
}

type postEntry struct {
	N            int
	Title        string
	Essence      string
	Consequences string
	Rationale    string
	URLs         []string
}

type prevEntry struct {
	N     int
	Title string
	Text  string
}

// ShortItems compresses a digest model into post lines.
// The model answer is not trusted for links: every URL comes from the item sources.
// When the draft is over limit, the model is called once more and then sentences are cut.
// An empty model returns no lines and does not call the model.
func ShortItems(ctx context.Context, c llm.Client, m domain.DigestModel, limit int) ([]ShortItem, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.Empty || len(m.Items) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = postCharLimit
	}
	if c == nil {
		return nil, errors.New("summarize: клиент LLM не задан")
	}
	shorts, err := askPost(ctx, c, m, limit, nil)
	if err != nil {
		return nil, err
	}
	if postWeight(shorts) > limit {
		shorts, err = askPost(ctx, c, m, limit, shorts)
		if err != nil {
			return nil, err
		}
	}
	shrinkShorts(shorts, limit)
	return shorts, nil
}

func askPost(ctx context.Context, c llm.Client, m domain.DigestModel, limit int, previous []ShortItem) ([]ShortItem, error) {
	view := postView{Limit: limit, Items: entriesOf(m)}
	for _, item := range previous {
		view.Previous = append(view.Previous, prevEntry{N: item.N, Title: item.Title, Text: item.Text})
	}
	system, user, _, err := prompts.Render("write_post", view)
	if err != nil {
		return nil, fmt.Errorf("summarize: промпт поста: %w", err)
	}
	var parsed postJSON
	_, err = llm.CompleteJSON(ctx, c, llm.Request{
		Task:        "write_post",
		System:      system,
		User:        user,
		Schema:      json.RawMessage(postSchemaJSON),
		Temperature: 0,
		MaxTokens:   1200,
	}, &parsed)
	if err != nil {
		return nil, fmt.Errorf("summarize: пост: %w", err)
	}
	return mapShorts(m, parsed), nil
}

func entriesOf(m domain.DigestModel) []postEntry {
	out := make([]postEntry, len(m.Items))
	for i, item := range m.Items {
		n := item.N
		if n <= 0 {
			n = i + 1
		}
		out[i] = postEntry{
			N:            n,
			Title:        item.Title,
			Essence:      item.Essence,
			Consequences: item.Consequences,
			Rationale:    item.Rationale,
			URLs:         itemURLs(item),
		}
	}
	return out
}

func mapShorts(m domain.DigestModel, parsed postJSON) []ShortItem {
	byN := map[int]int{}
	for i, item := range parsed.Items {
		byN[item.N] = i
	}
	out := make([]ShortItem, len(m.Items))
	for i, item := range m.Items {
		n := item.N
		if n <= 0 {
			n = i + 1
		}
		title := item.Title
		text := item.Essence
		if idx, ok := byN[n]; ok {
			if got := cleanProse(parsed.Items[idx].Title); got != "" {
				title = got
			}
			if got := cleanProse(textutil.StripURLs(parsed.Items[idx].Text)); got != "" {
				text = got
			}
		}
		out[i] = ShortItem{N: n, Title: title, Text: text, URLs: itemURLs(item)}
	}
	return out
}

func itemURLs(item domain.DigestItem) []string {
	var out []string
	seen := map[string]bool{}
	for _, source := range item.Sources {
		url := strings.TrimSpace(source.URL)
		if url == "" || seen[url] {
			continue
		}
		seen[url] = true
		out = append(out, url)
	}
	return out
}

func postWeight(shorts []ShortItem) int {
	n := 100
	for _, item := range shorts {
		n += 8 + runeCount(item.Title) + runeCount(item.Text)
	}
	return n
}

func shrinkShorts(shorts []ShortItem, limit int) {
	for step := 0; step < 400 && postWeight(shorts) > limit; step++ {
		overflow := postWeight(shorts) - limit
		if !trimShorts(shorts, overflow) {
			return
		}
	}
}

func trimShorts(shorts []ShortItem, overflow int) bool {
	best, bestScore := -1, 1
	for i, item := range shorts {
		n := len(SplitSentences(item.Text))
		if n > bestScore {
			best, bestScore = i, n
		}
	}
	if best >= 0 {
		parts := SplitSentences(shorts[best].Text)
		shorts[best].Text = JoinSentences(parts[:len(parts)-1])
		return true
	}
	best, bestLen := -1, 0
	for i, item := range shorts {
		n := runeCount(item.Text)
		if n > bestLen {
			best, bestLen = i, n
		}
	}
	if best >= 0 && bestLen > 60 {
		next := cutRunes(shorts[best].Text, bestLen-overflow, 60)
		if next == shorts[best].Text {
			return false
		}
		shorts[best].Text = next
		return true
	}
	return false
}

func cutRunes(s string, keep, floor int) string {
	rs := []rune(s)
	if keep < floor {
		keep = floor
	}
	if keep >= len(rs) {
		return s
	}
	if keep < 1 {
		keep = 1
	}
	cut := rs[:keep]
	for i := len(cut) - 1; i > len(cut)/2; i-- {
		if cut[i] == ' ' {
			cut = cut[:i]
			break
		}
	}
	return strings.TrimSpace(string(cut))
}
