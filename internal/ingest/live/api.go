package live

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"mono/internal/domain"
)

func parseAPI(src Source, body []byte) ([]domain.Article, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("разбор JSON: %w", err)
	}
	found := pickItems(v, src.Items)
	arr, ok := found.([]any)
	if !ok {
		return nil, errors.New("в ответе API нет списка записей")
	}
	idKey := fieldKey(src.FieldID, "id")
	titleKey := fieldKey(src.FieldTitle, "title")
	textKey := fieldKey(src.FieldText, "text")
	urlKey := fieldKey(src.FieldURL, "url")
	dateKey := fieldKey(src.FieldDate, "published_at")
	var out []domain.Article
	for _, item := range arr {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		title := firstString(obj, titleKey, "headline", "name")
		text := firstString(obj, textKey, "description", "body", "content")
		link := firstString(obj, urlKey, "link", "href")
		published := firstString(obj, dateKey, "published", "date")
		seed := firstString(obj, idKey, "guid")
		if seed == "" {
			seed = link
		}
		article, ok := buildArticle(src, seed, title, text, link, published)
		if !ok {
			continue
		}
		out = append(out, article)
	}
	return out, nil
}

func pickItems(v any, path string) any {
	path = strings.TrimSpace(path)
	if path != "" {
		return dig(v, path)
	}
	if _, ok := v.([]any); ok {
		return v
	}
	for _, key := range []string{"items", "articles", "data", "records", "news"} {
		if found := dig(v, key); found != nil {
			if _, ok := found.([]any); ok {
				return found
			}
		}
	}
	return nil
}

func dig(v any, path string) any {
	cur := v
	for _, part := range strings.Split(path, ".") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = obj[part]
	}
	return cur
}

func fieldKey(name, fallback string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return fallback
	}
	return name
}

func firstString(obj map[string]any, keys ...string) string {
	for _, key := range keys {
		if key == "" {
			continue
		}
		v, ok := obj[key]
		if !ok || v == nil {
			continue
		}
		s := scalar(v)
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func scalar(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return ""
		}
		return string(b)
	}
}
