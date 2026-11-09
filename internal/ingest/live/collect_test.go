package live

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mono/internal/config"
	"mono/internal/domain"
	"mono/internal/textutil"
)

func TestFixturesOffline(t *testing.T) {
	path := filepath.Join("..", "..", "..", "testdata", "live", "sources.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if textutil.HasLongDash(string(raw)) {
		t.Fatal("в sources.yaml фикстур есть длинное тире")
	}
	cfg := config.Default().Live
	cfg.Enabled = true
	cfg.SourcesFile = path
	cfg.RespectRobots = true
	got := Collect(context.Background(), cfg)
	if len(got.Articles) != 5 {
		t.Fatalf("статей %d, журнал %+v", len(got.Articles), got.Journal)
	}
	var down bool
	for _, row := range got.Journal {
		if row.ReasonCode != domain.ReasonSourceDown || row.Status != domain.StatusExcluded {
			t.Fatalf("строка: %+v", row)
		}
		if row.Reason != "Источник недоступен" || row.Error == "" {
			t.Fatalf("причина: %+v", row)
		}
		if row.ID == "live-down" {
			down = true
		}
	}
	if !down {
		t.Fatalf("нет source_unavailable: %+v", got.Journal)
	}
	kinds := map[string]bool{}
	for _, article := range got.Articles {
		if !strings.HasPrefix(article.ID, "live-") {
			t.Fatalf("id %s", article.ID)
		}
		if article.Title == "" || article.URL == "" || article.PublishedRaw == "" || article.Source == "" {
			t.Fatalf("поля: %+v", article)
		}
		if textutil.CountChars(article.Text) < 200 {
			t.Fatalf("%s: короткий текст %d", article.ID, textutil.CountChars(article.Text))
		}
		if textutil.HasLongDash(article.Text + article.Title) {
			t.Fatalf("длинное тире в %s", article.ID)
		}
		kinds[article.Source] = true
	}
	for _, name := range []string{"SecurityLab", "Хакер", "ComNews", "CNews", "Positive Technologies"} {
		if !kinds[name] {
			t.Fatalf("нет источника %s в %#v", name, kinds)
		}
	}
}

func TestRobotsRetryCacheAndAgent(t *testing.T) {
	rss := []byte(`<?xml version="1.0"?><rss version="2.0"><channel><item><title>Лента</title><link>https://example.com/a</link><description>Короткий пункт ленты для проверки повтора.</description></item></channel></rss>`)
	var hits atomic.Int32
	var agent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			_, _ = w.Write([]byte("User-agent: *\nDisallow: /private\nAllow: /ok\n"))
			return
		}
		if r.URL.Path == "/private/feed.xml" {
			hits.Add(1)
			http.NotFound(w, r)
			return
		}
		agent = r.Header.Get("User-Agent")
		n := hits.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(rss)
	}))
	defer srv.Close()

	dir := t.TempDir()
	sources := filepath.Join(dir, "sources.yaml")
	body := "sources:\n" +
		"  - id: hidden\n    name: Hidden\n    kind: rss\n    url: " + srv.URL + "/private/feed.xml\n" +
		"  - id: open\n    name: Open\n    kind: rss\n    url: " + srv.URL + "/ok/feed.xml\n"
	if err := os.WriteFile(sources, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default().Live
	cfg.SourcesFile = sources
	cfg.UserAgent = "mono/0.1"
	cfg.MaxRetries = 2
	cfg.RespectRobots = true
	cfg.CacheDir = filepath.Join(dir, "cache")
	c := Collector{Config: cfg, Sleep: func(context.Context, time.Duration) {}, CacheTTL: time.Hour}
	first := c.Collect(context.Background())
	if len(first.Articles) != 1 || first.Articles[0].Source != "Open" {
		t.Fatalf("articles %+v journal %+v", first.Articles, first.Journal)
	}
	if agent != "mono/0.1" {
		t.Fatalf("user-agent %q", agent)
	}
	var denied bool
	for _, row := range first.Journal {
		if row.ID == "live-hidden" && row.ReasonCode == domain.ReasonSourceDown && strings.Contains(row.Error, "robots.txt") {
			denied = true
		}
	}
	if !denied {
		t.Fatalf("запрет robots не записан: %+v", first.Journal)
	}
	if hits.Load() != 3 {
		t.Fatalf("запросов ленты %d, нужно 3", hits.Load())
	}
	second := c.Collect(context.Background())
	if len(second.Articles) != 1 {
		t.Fatalf("кэш не отдал статью: %+v", second.Articles)
	}
	if hits.Load() != 3 {
		t.Fatalf("кэш сходил в сеть еще раз: %d", hits.Load())
	}
}

func TestTimeoutDoesNotStopOtherSources(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte("nope"))
	}))
	defer srv.Close()
	dir := t.TempDir()
	good := filepath.Join(dir, "good.rss")
	rss := `<?xml version="1.0"?><rss version="2.0"><channel><item><title>Рядом</title><link>https://example.com/b</link><description>Соседний источник отвечает из файла.</description></item></channel></rss>`
	if err := os.WriteFile(good, []byte(rss), 0o644); err != nil {
		t.Fatal(err)
	}
	sources := filepath.Join(dir, "sources.yaml")
	text := "sources:\n" +
		"  - id: slow\n    name: Slow\n    kind: rss\n    url: " + srv.URL + "/slow.xml\n" +
		"  - id: file\n    name: File\n    kind: rss\n    url: good.rss\n"
	if err := os.WriteFile(sources, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default().Live
	cfg.SourcesFile = sources
	cfg.RespectRobots = false
	cfg.MaxRetries = 0
	cfg.TimeoutSec = 1
	c := Collector{
		Config: cfg,
		HTTP:   &http.Client{Timeout: 30 * time.Millisecond},
	}
	got := c.Collect(context.Background())
	if len(got.Articles) != 1 || got.Articles[0].Title != "Рядом" {
		t.Fatalf("articles %+v", got.Articles)
	}
	if len(got.Journal) != 1 || got.Journal[0].ReasonCode != domain.ReasonSourceDown {
		t.Fatalf("journal %+v", got.Journal)
	}
}

func TestMergeKeepsDatasetID(t *testing.T) {
	articles := []domain.Article{{ID: "live-open-aaaaaa", Title: "из набора"}}
	rows := []domain.JournalEntry{{ID: "broken-1", ReasonCode: domain.ReasonReadError}}
	got := Result{
		Articles: []domain.Article{
			{ID: "live-open-aaaaaa", Title: "дубль"},
			{ID: "live-open-bbbbbb", Title: "новая"},
		},
		Journal: []domain.JournalEntry{{
			ID: "live-down", Status: domain.StatusExcluded, ReasonCode: domain.ReasonSourceDown, Reason: "Источник недоступен",
		}},
	}
	articles, rows = Merge(articles, rows, got)
	if len(articles) != 2 || articles[0].Title != "из набора" || articles[1].ID != "live-open-bbbbbb" {
		t.Fatalf("articles %+v", articles)
	}
	if len(rows) != 2 || rows[1].ReasonCode != domain.ReasonSourceDown {
		t.Fatalf("rows %+v", rows)
	}
}

func TestRobotsParser(t *testing.T) {
	body := []byte("User-agent: *\nDisallow: /secret\nAllow: /secret/ok\n\nUser-agent: mono\nAllow: /\n")
	if !robotsAllow(body, "mono/0.1", "/secret/news") {
		t.Fatal("свой агент должен быть разрешен")
	}
	star := []byte("User-agent: *\nDisallow: /secret\nAllow: /secret/ok\n")
	if robotsAllow(star, "other/1", "/secret/news") {
		t.Fatal("/secret/news запрещен")
	}
	if !robotsAllow(star, "other/1", "/secret/ok") {
		t.Fatal("более длинный Allow должен победить")
	}
	if !robotsAllow(nil, "mono/0.1", "/any") {
		t.Fatal("пустой robots разрешает")
	}
}

func TestFetchReplacesShortListing(t *testing.T) {
	const page = `<!doctype html><html><body><nav><p>Меню сайта и прочие ссылки, которые не должны попасть в текст новости.</p></nav><article><h1>Утечка в банке</h1><time datetime="2026-11-09T08:00:00+03:00"></time><p>Банк подтвердил утечку записей клиентов и отключил затронутый сегмент. Служба безопасности проверяет журналы доступа и уже ограничила учетные записи подрядчика.</p><p>Платежные реквизиты, по заявлению банка, не затронуты. Число клиентов служба пока не назвала, расследование продолжается.</p></article></body></html>`
	var pages atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/news/1" {
			pages.Add(1)
			_, _ = w.Write([]byte(page))
			return
		}
		if r.URL.Path == "/elsewhere" {
			t.Errorf("запрос ушел на чужой путь %s", r.URL.Path)
		}
		rss := `<?xml version="1.0"?><rss version="2.0"><channel><item><title>Анонс</title><link>` + srv.URL + `/news/1</link><pubDate>Mon, 09 Nov 2026 08:00:00 +0300</pubDate><description>Короткий анонс.</description></item><item><title>Чужой</title><link>https://example.com/news</link><description>Тоже короткий анонс чужого сайта.</description></item></channel></rss>`
		_, _ = w.Write([]byte(rss))
	}))
	defer srv.Close()
	dir := t.TempDir()
	sources := filepath.Join(dir, "sources.yaml")
	body := "sources:\n" +
		"  - id: open\n    name: Open\n    kind: rss\n    url: " + srv.URL + "/feed.xml\n    fetch: true\n"
	if err := os.WriteFile(sources, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default().Live
	cfg.SourcesFile = sources
	cfg.RespectRobots = false
	cfg.MaxRetries = 2
	got := Collector{Config: cfg, Sleep: func(context.Context, time.Duration) {}}.Collect(context.Background())
	if len(got.Articles) != 2 {
		t.Fatalf("articles %+v journal %+v", got.Articles, got.Journal)
	}
	if pages.Load() != 1 {
		t.Fatalf("страниц %d", pages.Load())
	}
	var own domain.Article
	for _, article := range got.Articles {
		if strings.Contains(article.URL, "/news/1") {
			own = article
		}
		if strings.Contains(article.URL, "example.com") && strings.Contains(article.Text, "сегмент") {
			t.Fatalf("чужой адрес не должен открываться: %+v", article)
		}
	}
	if !strings.Contains(own.Text, "отключил затронутый сегмент") || strings.Contains(own.Text, "Меню сайта") {
		t.Fatalf("текст страницы: %s", own.Text)
	}
	if own.PublishedRaw == "" {
		t.Fatal("дата не сохранилась")
	}
}
