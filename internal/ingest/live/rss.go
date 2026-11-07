package live

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"

	"cyberdigest/internal/domain"
)

func parseRSS(src Source, body []byte) ([]domain.Article, error) {
	feed, err := gofeed.NewParser().Parse(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("разбор RSS: %w", err)
	}
	var out []domain.Article
	for _, item := range feed.Items {
		if item == nil {
			continue
		}
		text := strings.TrimSpace(item.Content)
		if text == "" {
			text = item.Description
		}
		link := strings.TrimSpace(item.Link)
		seed := link
		if seed == "" {
			seed = strings.TrimSpace(item.GUID)
		}
		published := ""
		if item.PublishedParsed != nil {
			published = item.PublishedParsed.Format(time.RFC3339)
		} else {
			published = strings.TrimSpace(item.Published)
		}
		article, ok := buildArticle(src, seed, item.Title, text, link, published)
		if !ok {
			continue
		}
		out = append(out, article)
	}
	return out, nil
}
