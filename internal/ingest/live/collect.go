package live

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"

	"cyberdigest/internal/config"
	"cyberdigest/internal/domain"
	"cyberdigest/internal/ingest"
	"cyberdigest/internal/normalize"
)

const (
	maxBody    = 8 << 20
	foldBudget = 8000
	cacheTTL   = time.Hour
)

var errRobots = errors.New("robots.txt запрещает сбор")

// Collector reads sources.yaml and turns each site into dataset articles.
// HTTP, clock, and sleep can be replaced in tests. A nil HTTP client uses TimeoutSec and UserAgent from Config.
type Collector struct {
	Config   config.Live
	HTTP     *http.Client
	Now      func() time.Time
	Sleep    func(context.Context, time.Duration)
	CacheTTL time.Duration
	Dir      string
}

// Collect loads every source. A missing file or a dead site becomes a journal row and does not fail the call.
func Collect(ctx context.Context, cfg config.Live) Result {
	return Collector{Config: cfg}.Collect(ctx)
}

// Result is the articles that entered the dataset and the rows for sources that did not.
type Result struct {
	Articles []domain.Article
	Journal  []domain.JournalEntry
}

// Collect fetches each source. Context cancellation stops the loop and keeps what was already read.
func (c Collector) Collect(ctx context.Context) Result {
	if ctx == nil {
		ctx = context.Background()
	}
	c = c.prepare()
	path := strings.TrimSpace(c.Config.SourcesFile)
	if path == "" {
		return Result{Journal: []domain.JournalEntry{downEntry(Source{ID: "sources"}, "не задан файл источников")}}
	}
	sources, err := LoadSources(path)
	if err != nil {
		return Result{Journal: []domain.JournalEntry{downEntry(Source{ID: "sources", Name: path}, clip(err.Error(), 240))}}
	}
	c.Dir = filepath.Dir(path)
	var got Result
	seen := map[string]bool{}
	for _, src := range sources {
		if err := ctx.Err(); err != nil {
			break
		}
		arts, row, ok := c.one(ctx, src)
		if !ok {
			if row.ID != "" && !seen[row.ID] {
				seen[row.ID] = true
				got.Journal = append(got.Journal, row)
			}
			continue
		}
		for _, article := range arts {
			if article.ID == "" || seen[article.ID] {
				continue
			}
			seen[article.ID] = true
			got.Articles = append(got.Articles, article)
		}
	}
	return got
}

// Merge appends live articles and unavailable-source rows to a dataset load.
// An ID that is already present stays as it was. The first record wins.
func Merge(articles []domain.Article, rows []domain.JournalEntry, got Result) ([]domain.Article, []domain.JournalEntry) {
	seen := map[string]bool{}
	for _, article := range articles {
		if article.ID != "" {
			seen[article.ID] = true
		}
	}
	for _, row := range rows {
		if row.ID != "" {
			seen[row.ID] = true
		}
	}
	for _, article := range got.Articles {
		if article.ID == "" || seen[article.ID] {
			continue
		}
		seen[article.ID] = true
		articles = append(articles, article)
	}
	for _, row := range got.Journal {
		if row.ID == "" || seen[row.ID] {
			continue
		}
		seen[row.ID] = true
		rows = append(rows, row)
	}
	return articles, rows
}

func (c Collector) prepare() Collector {
	if strings.TrimSpace(c.Config.UserAgent) == "" {
		c.Config.UserAgent = "cyberdigest/0.1"
	}
	if c.Config.TimeoutSec <= 0 {
		c.Config.TimeoutSec = 20
	}
	if c.Config.MaxRetries < 0 {
		c.Config.MaxRetries = 0
	}
	if c.HTTP == nil {
		c.HTTP = &http.Client{Timeout: time.Duration(c.Config.TimeoutSec) * time.Second}
	}
	return c
}

func (c Collector) one(ctx context.Context, src Source) ([]domain.Article, domain.JournalEntry, bool) {
	if strings.TrimSpace(src.URL) == "" {
		return nil, downEntry(src, "пустой адрес источника"), false
	}
	body, err := c.get(ctx, src.URL)
	if err != nil {
		return nil, downEntry(src, clip(err.Error(), 240)), false
	}
	var arts []domain.Article
	switch src.Kind {
	case "rss":
		arts, err = parseRSS(src, body)
	case "html":
		arts, err = parseHTML(src, body)
	case "api":
		arts, err = parseAPI(src, body)
	default:
		err = fmt.Errorf("неизвестный тип источника %q", src.Kind)
	}
	if err != nil {
		return nil, downEntry(src, clip(err.Error(), 240)), false
	}
	return arts, domain.JournalEntry{}, true
}

func (c Collector) get(ctx context.Context, rawURL string) ([]byte, error) {
	if body, ok := c.cacheGet(rawURL); ok {
		return body, nil
	}
	if !isHTTP(rawURL) {
		body, err := readLocal(rawURL)
		if err != nil {
			return nil, err
		}
		body = asUTF8(body)
		c.cachePut(rawURL, body)
		return body, nil
	}
	if c.Config.RespectRobots {
		allowed, err := c.robotsOK(ctx, rawURL)
		if err != nil {
			return nil, err
		}
		if !allowed {
			return nil, errRobots
		}
	}
	attempts := c.Config.MaxRetries + 1
	var last error
	for i := 0; i < attempts; i++ {
		if err := ctx.Err(); err != nil {
			if last != nil {
				return nil, last
			}
			return nil, err
		}
		body, status, err := c.do(ctx, rawURL)
		if err == nil && status >= 200 && status < 300 {
			body = asUTF8(body)
			c.cachePut(rawURL, body)
			return body, nil
		}
		if err == nil {
			err = fmt.Errorf("ответ HTTP %d", status)
		}
		last = err
		if !retryable(status, err) || i == attempts-1 {
			return nil, last
		}
		if err := c.sleep(ctx, retryDelay(i)); err != nil {
			return nil, last
		}
	}
	if last == nil {
		last = errors.New("источник не ответил")
	}
	return nil, last
}

func (c Collector) robotsOK(ctx context.Context, rawURL string) (bool, error) {
	origin := originOf(rawURL)
	if origin == "" {
		return true, nil
	}
	key := "robots|" + origin
	body, ok := c.cacheGet(key)
	status := 200
	var err error
	if !ok {
		body, status, err = c.do(ctx, origin+"/robots.txt")
		if err != nil {
			return true, nil
		}
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			return false, nil
		}
		if status < 200 || status >= 300 {
			return true, nil
		}
		c.cachePut(key, body)
	}
	return robotsAllow(body, c.Config.UserAgent, requestPath(rawURL)), nil
}

func (c Collector) do(ctx context.Context, rawURL string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", c.Config.UserAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if len(raw) > maxBody {
		return nil, resp.StatusCode, errors.New("ответ слишком большой")
	}
	return raw, resp.StatusCode, nil
}

func (c Collector) sleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		c.Sleep(ctx, d)
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c Collector) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c Collector) ttl() time.Duration {
	if c.CacheTTL > 0 {
		return c.CacheTTL
	}
	return cacheTTL
}

func retryable(status int, err error) bool {
	if status == http.StatusTooManyRequests || status == http.StatusRequestTimeout {
		return true
	}
	if status >= 500 {
		return true
	}
	if status >= 400 {
		return false
	}
	return err != nil
}

func retryDelay(attempt int) time.Duration {
	d := 200 * time.Millisecond
	for i := 0; i < attempt; i++ {
		d *= 2
		if d > 2*time.Second {
			return 2 * time.Second
		}
	}
	return d
}

func isHTTP(raw string) bool {
	low := strings.ToLower(strings.TrimSpace(raw))
	return strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://")
}

func readLocal(raw string) ([]byte, error) {
	path := raw
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), "file:") {
		u, err := url.Parse(raw)
		if err != nil {
			return nil, err
		}
		path = u.Path
		if u.Host != "" && u.Host != "localhost" {
			path = "//" + u.Host + path
		}
		path, err = url.PathUnescape(path)
		if err != nil {
			return nil, err
		}
		if runtime.GOOS == "windows" && strings.HasPrefix(path, "/") && len(path) >= 3 && path[2] == ':' {
			path = path[1:]
		}
		path = filepath.FromSlash(path)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("источник не найден")
		}
		return nil, err
	}
	return body, nil
}

func asUTF8(raw []byte) []byte {
	if bytesHasBOM(raw) {
		raw = raw[3:]
	}
	if len(raw) == 0 || utf8.Valid(raw) {
		return raw
	}
	out, err := charmap.Windows1251.NewDecoder().Bytes(raw)
	if err != nil {
		return raw
	}
	return out
}

func bytesHasBOM(raw []byte) bool {
	return len(raw) >= 3 && raw[0] == 0xEF && raw[1] == 0xBB && raw[2] == 0xBF
}

func downEntry(src Source, detail string) domain.JournalEntry {
	name := strings.TrimSpace(src.Name)
	if name == "" {
		name = src.ID
	}
	return domain.JournalEntry{
		ID:         downID(src),
		Status:     domain.StatusExcluded,
		ReasonCode: domain.ReasonSourceDown,
		Reason:     "Источник недоступен",
		Error:      detail,
		Source:     name,
		SourceKind: ingest.SourceKindOf(name, src.URL),
	}
}

func downID(src Source) string {
	id := strings.TrimSpace(src.ID)
	if id != "" {
		return "live-" + slug(id)
	}
	return "live-src-" + shortHash(src.URL+"\n"+src.Name)
}

func articleID(sourceID, seed string) string {
	return "live-" + slug(sourceID) + "-" + shortHash(seed)
}

func shortHash(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:6])
}

func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "src"
	}
	return out
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n])
}

func buildArticle(src Source, seed, title, text, link, published string) (domain.Article, bool) {
	title = strings.TrimSpace(normalize.Clean(title))
	text = FoldParts(normalize.Clean(text), foldBudget)
	if title == "" && text == "" {
		return domain.Article{}, false
	}
	link = strings.TrimSpace(link)
	if link == "" {
		link = src.URL
	}
	if seed == "" {
		seed = link
	}
	if seed == "" {
		seed = title
	}
	name := src.Name
	if name == "" {
		name = src.ID
	}
	return domain.Article{
		ID:           articleID(src.ID, seed),
		Title:        title,
		Text:         text,
		RawText:      text,
		Source:       name,
		URL:          link,
		PublishedRaw: strings.TrimSpace(published),
		SourceKind:   ingest.SourceKindOf(name, link),
	}, true
}
