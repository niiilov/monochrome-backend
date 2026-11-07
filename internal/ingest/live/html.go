package live

import (
	"bytes"
	"fmt"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"cyberdigest/internal/domain"
)

func parseHTML(src Source, body []byte) ([]domain.Article, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("разбор HTML: %w", err)
	}
	itemSel := strings.TrimSpace(src.Item)
	if itemSel == "" {
		itemSel = "article"
	}
	titleSel := strings.TrimSpace(src.Title)
	if titleSel == "" {
		titleSel = "h1, h2"
	}
	linkSel := strings.TrimSpace(src.Link)
	if linkSel == "" {
		linkSel = "a"
	}
	dateSel := strings.TrimSpace(src.Date)
	if dateSel == "" {
		dateSel = "time"
	}
	textSel := strings.TrimSpace(src.Text)
	if textSel == "" {
		textSel = "p"
	}
	var out []domain.Article
	doc.Find(itemSel).Each(func(_ int, item *goquery.Selection) {
		title := strings.TrimSpace(item.Find(titleSel).First().Text())
		link := attrOrText(item.Find(linkSel).First(), "href")
		link = resolveLink(src.URL, link)
		published := attrOrText(item.Find(dateSel).First(), "datetime")
		text := strings.TrimSpace(item.Find(textSel).Text())
		article, ok := buildArticle(src, link, title, text, link, published)
		if !ok {
			return
		}
		out = append(out, article)
	})
	return out, nil
}

func attrOrText(s *goquery.Selection, attr string) string {
	if s.Length() == 0 {
		return ""
	}
	if attr != "" {
		if v, ok := s.Attr(attr); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return strings.TrimSpace(s.Text())
}

func resolveLink(baseRaw, href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	ref, err := url.Parse(href)
	if err != nil {
		return href
	}
	if ref.IsAbs() {
		return href
	}
	base, err := url.Parse(baseRaw)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return href
	}
	return base.ResolveReference(ref).String()
}
