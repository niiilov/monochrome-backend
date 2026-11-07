package guard

import (
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"cyberdigest/internal/textutil"
)

const (
	// KindIgnorePrevious is "ignore previous instructions" and the Russian pair.
	KindIgnorePrevious = "ignore_previous"
	// KindForgetPrevious is "forget/disregard previous" and "забудь предыдущие".
	KindForgetPrevious = "forget_previous"
	// KindSystemPrompt asks to reveal or replace the system prompt.
	KindSystemPrompt = "system_prompt"
	// KindRoleOverride is "you are now" / "ты теперь".
	KindRoleOverride = "role_override"
	// KindSendTo is "отправь на" / "send to".
	KindSendTo = "send_to"
	// KindForward is "перешли" / "forward to".
	KindForward = "forward"
	// KindPutFirst is "включи эту новость первой" and the English pair.
	KindPutFirst = "put_first"
	// KindEmail is an address found in untrusted text.
	KindEmail = "email"
	// KindHTML is a script, iframe, event handler, or javascript URL.
	KindHTML = "html"
	// KindMarkdown is a markdown image or a javascript markdown link.
	KindMarkdown = "markdown"
	// KindBase64 is a long base64 blob.
	KindBase64 = "base64"

	// ViolURL is a URL that is not in the source registry.
	ViolURL = "url"
	// ViolEmail is an address that is not on the allow list.
	ViolEmail = "email"
	// ViolPhrase is a service phrase that should not appear in a digest.
	ViolPhrase = "service_phrase"
)

// Finding is one suspicious span in untrusted input. Excerpt is shortened.
type Finding struct {
	Kind    string
	Excerpt string
	At      int
}

// Violation is one problem in model output.
type Violation struct {
	Kind    string
	Excerpt string
}

type pattern struct {
	kind string
	re   *regexp.Regexp
}

var patterns = []pattern{
	{KindIgnorePrevious, regexp.MustCompile(`(?i)\bignore[\s+\-_]+(?:all[\s+\-_]+|any[\s+\-_]+)?(?:previous|above|prior)\b`)},
	{KindIgnorePrevious, regexp.MustCompile(`(?i)игнорир\p{L}*[\s+\-_]+(?:все[\s+\-_]+)?предыдущ\p{L}*`)},
	{KindForgetPrevious, regexp.MustCompile(`(?i)\b(?:forget|disregard)[\s+\-_]+(?:all[\s+\-_]+)?(?:previous|above|prior)\b`)},
	{KindForgetPrevious, regexp.MustCompile(`(?i)забудь(?:те)?[\s+\-_]+(?:все[\s+\-_]+)?(?:предыдущ\p{L}*|инструкц\p{L}*|правил\p{L}*)`)},
	{KindSystemPrompt, regexp.MustCompile(`(?i)\bsystem[\s+\-_]+prompts?\b`)},
	{KindSystemPrompt, regexp.MustCompile(`(?i)системн\p{L}*[\s+\-_]+промпт\p{L}*`)},
	{KindRoleOverride, regexp.MustCompile(`(?i)\byou[\s+\-_]+are[\s+\-_]+now\b`)},
	{KindRoleOverride, regexp.MustCompile(`(?i)ты[\s+\-_]+теперь`)},
	{KindSendTo, regexp.MustCompile(`(?i)отправ(?:ь|ьте)[\s+\-_]+на(?:[^\p{L}]|$)`)},
	{KindSendTo, regexp.MustCompile(`(?i)\bsend[\s+\-_]+(?:this[\s+\-_]+|it[\s+\-_]+)?to\b`)},
	{KindForward, regexp.MustCompile(`(?i)перешл(?:и|ите)(?:[^\p{L}]|$)`)},
	{KindForward, regexp.MustCompile(`(?i)\bforward[\s+\-_]+(?:this[\s+\-_]+|it[\s+\-_]+)?to\b`)},
	{KindPutFirst, regexp.MustCompile(`(?i)включи(?:те)?[\s+\-_]+эту[\s+\-_]+новость[\s+\-_]+первой`)},
	{KindPutFirst, regexp.MustCompile(`(?i)\b(?:put|place|include)[\s+\-_]+this[\s+\-_]+(?:news|article|story|item)[\s+\-_]+first\b`)},
	{KindHTML, regexp.MustCompile(`(?i)<\s*/?\s*(?:script|iframe|img|object|embed|svg|link|meta)\b`)},
	{KindHTML, regexp.MustCompile(`(?i)\bon[a-z]+\s*=`)},
	{KindHTML, regexp.MustCompile(`(?i)\bjavascript\s*:`)},
	{KindMarkdown, regexp.MustCompile(`!\[[^\]]{0,200}\]\([^)\s]{1,500}\)`)},
	{KindMarkdown, regexp.MustCompile(`(?i)\[[^\]]{0,200}\]\(\s*javascript:`)},
	{KindBase64, regexp.MustCompile(`[A-Za-z0-9+/]{100,}={0,2}`)},
}

var servicePhrases = []string{
	"system prompt",
	"системный промпт",
	"ignore previous instructions",
	"игнорируй предыдущие",
	"как языковая модель",
	"as a language model",
	"llm_api_key",
	"список получателей",
}

// WrapUntrusted marks title and text as data inside <article id="...">.
// Every "<" in the body is escaped, so a closing tag inside the article
// cannot end the block early.
func WrapUntrusted(id, title, text string) string {
	var b strings.Builder
	b.Grow(len(id) + len(title) + len(text) + 32)
	b.WriteString("<article id=\"")
	b.WriteString(escapeAttr(id))
	b.WriteString("\">\n")
	if strings.TrimSpace(title) != "" {
		b.WriteString(escapeText(title))
		b.WriteByte('\n')
	}
	b.WriteString(escapeText(text))
	b.WriteString("\n</article>")
	return b.String()
}

// Scan looks for injection patterns and does not change the text.
// The result is ordered by byte offset. An empty result is nil.
func Scan(text string) []Finding {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var out []Finding
	targets := []string{text}
	if folded := foldSeparators(text); folded != text {
		targets = append(targets, folded)
	}
	seen := map[string]bool{}
	for _, target := range targets {
		for _, p := range patterns {
			idxs := p.re.FindAllStringIndex(target, -1)
			for _, pair := range idxs {
				excerpt := clip(target[pair[0]:pair[1]])
				key := p.kind + "\x00" + excerpt
				if seen[key] {
					continue
				}
				seen[key] = true
				at := pair[0]
				if target != text {
					at = strings.Index(text, excerpt)
					if at < 0 {
						at = pair[0]
					}
				}
				out = append(out, Finding{Kind: p.kind, Excerpt: excerpt, At: at})
			}
		}
	}
	seenMail := map[string]bool{}
	for _, addr := range textutil.ExtractEmails(text) {
		key := strings.ToLower(addr)
		if seenMail[key] {
			continue
		}
		seenMail[key] = true
		at := strings.Index(text, addr)
		if at < 0 {
			at = 0
		}
		out = append(out, Finding{Kind: KindEmail, Excerpt: addr, At: at})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].At == out[j].At {
			return out[i].Kind < out[j].Kind
		}
		return out[i].At < out[j].At
	})
	return out
}

// CheckOutput flags URLs outside allowedURLs, addresses outside allowedEmails,
// and service phrases. A nil map allows nothing. Comparison of URLs also
// accepts a single trailing slash difference. Mail comparison is case-insensitive.
func CheckOutput(text string, allowedURLs, allowedEmails map[string]bool) []Violation {
	var out []Violation
	seenURL := map[string]bool{}
	for _, raw := range textutil.ExtractURLs(text) {
		if seenURL[raw] {
			continue
		}
		seenURL[raw] = true
		if !urlAllowed(raw, allowedURLs) {
			out = append(out, Violation{Kind: ViolURL, Excerpt: raw})
		}
	}
	seenMail := map[string]bool{}
	for _, addr := range textutil.ExtractEmails(text) {
		key := strings.ToLower(addr)
		if seenMail[key] {
			continue
		}
		seenMail[key] = true
		if !emailAllowed(addr, allowedEmails) {
			out = append(out, Violation{Kind: ViolEmail, Excerpt: addr})
		}
	}
	low := strings.ToLower(text)
	for _, phrase := range servicePhrases {
		if strings.Contains(low, strings.ToLower(phrase)) {
			out = append(out, Violation{Kind: ViolPhrase, Excerpt: phrase})
		}
	}
	return out
}

func escapeAttr(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

func escapeText(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

func urlAllowed(raw string, allow map[string]bool) bool {
	if len(allow) == 0 {
		return false
	}
	if allow[raw] {
		return true
	}
	trim := strings.TrimRight(raw, "/")
	return allow[trim] || allow[trim+"/"]
}

func emailAllowed(addr string, allow map[string]bool) bool {
	if len(allow) == 0 || !allowValue(allow, addr) {
		return false
	}
	return true
}

func allowValue(allow map[string]bool, addr string) bool {
	if allow[addr] {
		return true
	}
	if allow[strings.ToLower(addr)] {
		return true
	}
	for key, ok := range allow {
		if ok && strings.EqualFold(key, addr) {
			return true
		}
	}
	return false
}

func foldSeparators(s string) string {
	if !strings.ContainsAny(s, "+%") {
		return s
	}
	return strings.NewReplacer("+", " ", "%20", " ", "%2B", " ").Replace(s)
}

func clip(s string) string {
	const limit = 160
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	return string([]rune(s)[:limit])
}
