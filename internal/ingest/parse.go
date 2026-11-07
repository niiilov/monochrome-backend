package ingest

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode"

	"cyberdigest/internal/domain"
)

var wrapKeys = []string{
	"articles", "items", "data", "records", "news",
	"publications", "materials", "dataset",
}

func (l *fileLoader) parseText(path, text string, st *loadState) ([]domain.Article, []domain.JournalEntry) {
	where := whereFile(path)
	switch strings.ToLower(filepath.Ext(path)) {
	case ".csv":
		return l.parseCSV(where, text, st)
	case ".jsonl", ".ndjson":
		return l.parseJSONL(where, text, st)
	case ".txt", ".html", ".htm", ".md":
		return l.parsePlain(path, text, st)
	case ".json":
		if arts, rows, err := l.decodeJSONFile(where, text, st); err == nil {
			return arts, rows
		} else if arts, rows, ok := l.tryJSONL(where, text, st); ok {
			return arts, rows
		} else {
			return nil, []domain.JournalEntry{st.readRow(where, clip(err.Error(), 180))}
		}
	default:
		if arts, rows, err := l.decodeJSONFile(where, text, st); err == nil {
			return arts, rows
		}
		if looksLikeCSV(text) {
			return l.parseCSV(where, text, st)
		}
		if arts, rows, ok := l.tryJSONL(where, text, st); ok {
			return arts, rows
		}
		return l.parsePlain(path, text, st)
	}
}

func (l *fileLoader) decodeJSONFile(where, text string, st *loadState) ([]domain.Article, []domain.JournalEntry, error) {
	v, err := decodeJSON(text)
	if err != nil {
		return nil, nil, err
	}
	arts, rows := l.fromValue(v, st, where)
	return arts, rows, nil
}

func decodeJSON(text string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, errors.New("лишние данные после JSON")
		}
		return nil, err
	}
	return v, nil
}

func (l *fileLoader) fromValue(v any, st *loadState, where string) ([]domain.Article, []domain.JournalEntry) {
	switch t := v.(type) {
	case []any:
		return l.fromArray(t, st, where)
	case map[string]any:
		if !looksLikeArticle(t) {
			if inner, ok := unwrap(t); ok {
				return l.fromValue(inner, st, where)
			}
		}
		return single(l.fromObject(t, st, where))
	default:
		return nil, []domain.JournalEntry{st.readRow(where, "ожидался объект или массив")}
	}
}

func (l *fileLoader) fromArray(items []any, st *loadState, where string) ([]domain.Article, []domain.JournalEntry) {
	if len(items) == 0 {
		return nil, []domain.JournalEntry{st.empty(where, "пустой массив")}
	}
	var arts []domain.Article
	var rows []domain.JournalEntry
	for i, item := range items {
		at := fmt.Sprintf("%s[%d]", where, i)
		switch t := item.(type) {
		case map[string]any:
			a, j := l.fromObject(t, st, at)
			collect(&arts, &rows, a, j)
		default:
			rows = append(rows, st.readRow(at, "запись не является объектом"))
		}
	}
	return arts, rows
}

func (l *fileLoader) fromObject(raw map[string]any, st *loadState, where string) (*domain.Article, *domain.JournalEntry) {
	fields, present := recordFromMap(raw)
	return l.accept(fields, present, st, where)
}

func (l *fileLoader) accept(fields map[string]string, present map[string]bool, st *loadState, where string) (*domain.Article, *domain.JournalEntry) {
	m := l.fields.mapRecord(fields, present)
	if m.id != "" && st.seen[m.id] {
		j := st.row(m.id, domain.ReasonDuplicateID, reasonDup, reasonDup, m.source, m.url)
		return nil, &j
	}
	if m.contentEmpty() {
		id := m.id
		if id == "" {
			id = st.nextUnknown()
		}
		j := st.row(id, domain.ReasonEmptyInput, reasonEmpty, reasonEmpty+" ("+where+")", m.source, m.url)
		return nil, &j
	}
	id := m.id
	readErr := ""
	if id == "" {
		id = st.nextUnknown()
		readErr = "нет идентификатора"
	} else {
		st.seen[id] = true
	}
	article := domain.Article{
		ID:            id,
		Title:         m.title,
		Text:          m.text,
		RawText:       m.text,
		Source:        m.source,
		URL:           m.url,
		PublishedRaw:  m.published,
		MissingFields: copyMissing(m.missing),
		ReadError:     readErr,
		SourceKind:    SourceKindOf(m.source, m.url),
	}
	if readErr == "" {
		return &article, nil
	}
	j := st.row(id, domain.ReasonReadError, reasonRead, reasonRead+": "+readErr+" ("+where+")", m.source, m.url)
	return &article, &j
}

func (l *fileLoader) parseJSONL(where, text string, st *loadState) ([]domain.Article, []domain.JournalEntry) {
	if !jsonlHasValue(text) {
		return nil, []domain.JournalEntry{st.readRow(where, "не удалось разобрать JSONL")}
	}
	arts, rows, _ := l.tryJSONL(where, text, st)
	return arts, rows
}

func (l *fileLoader) tryJSONL(where, text string, st *loadState) ([]domain.Article, []domain.JournalEntry, bool) {
	if !jsonlHasValue(text) {
		return nil, nil, false
	}
	var arts []domain.Article
	var rows []domain.JournalEntry
	for i, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if line == "" {
			continue
		}
		at := fmt.Sprintf("%s:%d", where, i+1)
		v, err := decodeJSON(line)
		if err != nil {
			rows = append(rows, st.readRow(at, clip(err.Error(), 160)))
			continue
		}
		a, j := l.fromValue(v, st, at)
		arts = append(arts, a...)
		rows = append(rows, j...)
	}
	return arts, rows, true
}

func jsonlHasValue(text string) bool {
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if line == "" {
			continue
		}
		if _, err := decodeJSON(line); err == nil {
			return true
		}
	}
	return false
}

func (l *fileLoader) parseCSV(where, text string, st *loadState) ([]domain.Article, []domain.JournalEntry) {
	reader := csv.NewReader(strings.NewReader(text))
	reader.Comma = detectDelim(text)
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true
	header, err := reader.Read()
	if err != nil {
		return nil, []domain.JournalEntry{st.readRow(where, clip(err.Error(), 180))}
	}
	if len(header) > 0 {
		header[0] = strings.TrimPrefix(header[0], "\uFEFF")
	}
	var arts []domain.Article
	var rows []domain.JournalEntry
	n := 0
	for {
		rec, err := reader.Read()
		if err == io.EOF {
			break
		}
		n++
		at := fmt.Sprintf("%s:row %d", where, n)
		if err != nil {
			rows = append(rows, st.readRow(at, clip(err.Error(), 160)))
			continue
		}
		fields, present := recordFromRow(header, rec)
		a, j := l.accept(fields, present, st, at)
		collect(&arts, &rows, a, j)
	}
	if n == 0 {
		rows = append(rows, st.empty(where, "в csv нет записей"))
	}
	return arts, rows
}

func (l *fileLoader) parsePlain(path, text string, st *loadState) ([]domain.Article, []domain.JournalEntry) {
	title, body := splitPlain(text)
	id := sanitizeID(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	fields := map[string]string{
		"title": title,
		"text":  body,
	}
	present := map[string]bool{
		"title": title != "",
		"text":  true,
	}
	if id != "" {
		fields["id"] = id
		present["id"] = true
	}
	return single(l.accept(fields, present, st, whereFile(path)))
}

func splitPlain(text string) (string, string) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.TrimSpace(text)
	lines := strings.Split(text, "\n")
	title := ""
	restAt := 0
	for i, line := range lines {
		if strings.TrimSpace(line) != "" {
			title = strings.TrimSpace(line)
			restAt = i + 1
			break
		}
	}
	rest := ""
	if restAt < len(lines) {
		rest = strings.TrimSpace(strings.Join(lines[restAt:], "\n"))
	}
	if rest == "" {
		rest = title
	}
	runes := []rune(title)
	if len(runes) > 200 {
		title = string(runes[:200])
	}
	return title, rest
}

func sanitizeID(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-_")
}

func detectDelim(text string) rune {
	line := text
	if i := strings.IndexAny(text, "\r\n"); i >= 0 {
		line = text[:i]
	}
	commas, semis := 0, 0
	inQuote := false
	for _, r := range line {
		if r == '"' {
			inQuote = !inQuote
			continue
		}
		if inQuote {
			continue
		}
		switch r {
		case ',':
			commas++
		case ';':
			semis++
		}
	}
	if semis > commas {
		return ';'
	}
	return ','
}

func looksLikeCSV(text string) bool {
	line := text
	if i := strings.IndexAny(text, "\r\n"); i >= 0 {
		line = text[:i]
	}
	if !strings.ContainsAny(line, ",;") {
		return false
	}
	lower := strings.ToLower(line)
	for _, names := range aliasList {
		for _, name := range names {
			if strings.Contains(lower, name) {
				return true
			}
		}
	}
	return false
}

func looksLikeArticle(raw map[string]any) bool {
	_, present := recordFromMap(raw)
	for _, key := range []string{"id", "title", "text"} {
		for _, name := range aliasList[key] {
			if present[name] {
				return true
			}
		}
	}
	return false
}

func unwrap(raw map[string]any) (any, bool) {
	lower := make(map[string]any, len(raw))
	for key, val := range raw {
		lower[strings.ToLower(strings.TrimSpace(key))] = val
	}
	for _, key := range wrapKeys {
		val, ok := lower[key]
		if !ok {
			continue
		}
		switch val.(type) {
		case []any, map[string]any:
			return val, true
		}
	}
	var only any
	n := 0
	for _, val := range raw {
		if _, ok := val.([]any); ok {
			only = val
			n++
		}
	}
	if n == 1 {
		return only, true
	}
	return nil, false
}

func single(a *domain.Article, j *domain.JournalEntry) ([]domain.Article, []domain.JournalEntry) {
	var arts []domain.Article
	var rows []domain.JournalEntry
	collect(&arts, &rows, a, j)
	return arts, rows
}

func collect(arts *[]domain.Article, rows *[]domain.JournalEntry, a *domain.Article, j *domain.JournalEntry) {
	if a != nil {
		*arts = append(*arts, *a)
	}
	if j != nil {
		*rows = append(*rows, *j)
	}
}

func copyMissing(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	return append([]string(nil), in...)
}

func clip(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n]) + "..."
}
