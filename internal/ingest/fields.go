package ingest

import (
	"encoding/json"
	"strconv"
	"strings"

	"mono/internal/config"
)

// aliasList is used when the configured column is absent.
// body/content/text and published/date/pub_date cover the usual organizer spellings.
var aliasList = map[string][]string{
	"id":           {"id", "article_id", "uid", "guid"},
	"title":        {"title", "headline", "name", "заголовок"},
	"text":         {"text", "body", "content", "article", "full_text", "description", "текст"},
	"source":       {"source", "source_name", "publisher", "site", "источник"},
	"url":          {"url", "link", "href", "ссылка"},
	"published_at": {"published_at", "published", "date", "pub_date", "datetime", "published_date", "дата"},
}

type mapped struct {
	id, title, text, source, url, published string
	missing                                 []string
}

func (m mapped) contentEmpty() bool {
	return strings.TrimSpace(m.title) == "" &&
		strings.TrimSpace(m.text) == "" &&
		strings.TrimSpace(m.source) == "" &&
		strings.TrimSpace(m.url) == "" &&
		strings.TrimSpace(m.published) == ""
}

type fieldReader struct {
	cfg     config.Ingest
	markers []string
}

func newFieldReader(cfg config.Ingest) fieldReader {
	def := config.Default().Ingest
	if len(cfg.FieldMap) == 0 {
		cfg.FieldMap = def.FieldMap
	}
	cp := make(map[string]string, len(cfg.FieldMap))
	for k, v := range cfg.FieldMap {
		cp[strings.ToLower(strings.TrimSpace(k))] = v
	}
	cfg.FieldMap = cp
	markers := cfg.MissingMarkers
	if markers == nil {
		markers = append([]string(nil), def.MissingMarkers...)
	} else {
		markers = append([]string(nil), markers...)
	}
	return fieldReader{cfg: cfg, markers: markers}
}

func (f fieldReader) isMissing(value string) bool {
	trimmed := strings.TrimSpace(value)
	for _, marker := range f.markers {
		if marker == "" {
			if trimmed == "" {
				return true
			}
			continue
		}
		if strings.EqualFold(trimmed, strings.TrimSpace(marker)) {
			return true
		}
	}
	return false
}

func (f fieldReader) mapRecord(fields map[string]string, present map[string]bool) mapped {
	var out mapped
	take := func(canonical string) (string, bool) {
		val, marked, ok := f.lookup(fields, present, canonical)
		if marked {
			out.missing = append(out.missing, canonical)
		}
		return val, ok && !marked
	}
	if v, ok := take("id"); ok {
		out.id = strings.TrimSpace(v)
	}
	if v, ok := take("title"); ok {
		out.title = v
	}
	if v, ok := take("text"); ok {
		out.text = v
	}
	if v, ok := take("source"); ok {
		out.source = strings.TrimSpace(v)
	}
	if v, ok := take("url"); ok {
		out.url = strings.TrimSpace(v)
	}
	if v, ok := take("published_at"); ok {
		out.published = strings.TrimSpace(v)
	}
	return out
}

func (f fieldReader) lookup(fields map[string]string, present map[string]bool, canonical string) (string, bool, bool) {
	var cands []string
	if name := strings.TrimSpace(f.cfg.FieldMap[canonical]); name != "" {
		cands = append(cands, strings.ToLower(name))
	}
	cands = append(cands, aliasList[canonical]...)
	seen := map[string]bool{}
	for _, cand := range cands {
		cand = strings.ToLower(strings.TrimSpace(cand))
		if cand == "" || seen[cand] {
			continue
		}
		seen[cand] = true
		if !present[cand] {
			continue
		}
		val := fields[cand]
		if f.isMissing(val) {
			return "", true, true
		}
		return val, false, true
	}
	return "", false, false
}

func recordFromMap(raw map[string]any) (map[string]string, map[string]bool) {
	fields := make(map[string]string, len(raw))
	present := make(map[string]bool, len(raw))
	for key, val := range raw {
		lk := strings.ToLower(strings.TrimSpace(key))
		if lk == "" {
			continue
		}
		fields[lk] = scalarString(val)
		present[lk] = true
	}
	return fields, present
}

func recordFromRow(header []string, row []string) (map[string]string, map[string]bool) {
	fields := make(map[string]string, len(header))
	present := make(map[string]bool, len(header))
	for i, key := range header {
		lk := strings.ToLower(strings.TrimSpace(key))
		if lk == "" {
			continue
		}
		val := ""
		if i < len(row) {
			val = row[i]
		}
		fields[lk] = val
		present[lk] = true
	}
	return fields, present
}

func scalarString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return ""
		}
		return string(b)
	}
}
