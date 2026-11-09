package live

import (
	"bytes"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// extractPage reads the article title, body, and publication time from an HTML page.
// Navigation, scripts, and the site chrome are dropped before the text is taken.
func extractPage(src Source, body []byte) (string, string, string) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return "", "", ""
	}
	doc.Find("script, style, noscript, nav, footer, aside, form, iframe").Remove()
	title := strings.TrimSpace(doc.Find("h1").First().Text())
	if title == "" {
		title = metaContent(doc, "meta[property='og:title'], meta[name='og:title']")
	}
	published := attrOrText(doc.Find("time").First(), "datetime")
	if published == "" {
		published = metaContent(doc, "meta[property='article:published_time']")
	}
	sel := strings.TrimSpace(src.Article)
	if sel == "" {
		sel = "article, [itemprop=articleBody], main"
	}
	root := doc.Find(sel).First()
	if root.Length() == 0 {
		root = doc.Find("body").First()
	}
	var paras []string
	root.Find("p").Each(func(_ int, s *goquery.Selection) {
		text := strings.TrimSpace(s.Text())
		if text != "" {
			paras = append(paras, text)
		}
	})
	text := strings.Join(paras, "\n\n")
	if strings.TrimSpace(text) == "" {
		text = strings.TrimSpace(root.Text())
	}
	return title, text, published
}

func metaContent(doc *goquery.Document, sel string) string {
	value, ok := doc.Find(sel).First().Attr("content")
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func sameSite(sourceURL, pageURL string) bool {
	base, err := url.Parse(strings.TrimSpace(sourceURL))
	if err != nil || base.Host == "" {
		return false
	}
	page, err := url.Parse(strings.TrimSpace(pageURL))
	if err != nil || page.Host == "" {
		return false
	}
	if page.Scheme != "http" && page.Scheme != "https" {
		return false
	}
	return sameHost(base.Host, page.Host)
}

func sameHost(a, b string) bool {
	return hostKey(a) == hostKey(b) && hostKey(a) != ""
}

func hostKey(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	host = strings.TrimPrefix(host, "www.")
	return host
}
