package ingest

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"mono/internal/config"
	"mono/internal/textutil"
)

func TestSyntheticIDs(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "synthetic", "articles.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var recs []map[string]any
	if err := json.Unmarshal(raw, &recs); err != nil {
		t.Fatal(err)
	}
	if len(recs) < 30 {
		t.Fatalf("синтетика: %d записей, нужно не меньше 30", len(recs))
	}
	if textutil.HasLongDash(string(raw)) {
		t.Fatal("в синтетике есть длинное тире")
	}
	arts, journal, err := LoadDataset(context.Background(), []string{path}, config.Default().Ingest)
	if err != nil {
		t.Fatal(err)
	}
	if len(journal) != 0 {
		t.Fatalf("у синтетики не должно быть отбраковки: %+v", journal)
	}
	got := map[string]domainOnce{}
	for _, a := range arts {
		if a.ID == "" {
			t.Fatal("пустой id")
		}
		got[a.ID] = domainOnce{}
		if a.RawText != a.Text {
			t.Fatalf("%s: загрузчик не должен чистить текст", a.ID)
		}
	}
	if len(arts) != len(recs) {
		t.Fatalf("статей %d, записей %d", len(arts), len(recs))
	}
	for _, rec := range recs {
		id, _ := rec["id"].(string)
		if _, ok := got[id]; !ok {
			t.Errorf("нет id %s", id)
		}
	}
}

type domainOnce struct{}

func TestHardInputsDoNotCrash(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "hard")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default().Ingest
	ctx := context.Background()
	saw := 0
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		saw++
		path := filepath.Join(dir, entry.Name())
		if _, _, err := LoadDataset(ctx, []string{path}, cfg); err != nil {
			t.Fatalf("%s: %v", entry.Name(), err)
		}
	}
	if saw < 5 {
		t.Fatalf("мало трудных файлов: %d", saw)
	}
	arts, journal, err := LoadDataset(ctx, []string{dir}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(arts)+len(journal) == 0 {
		t.Fatal("каталог трудных входов не дал ни одной записи")
	}
}

func TestEncodingsAndBOM(t *testing.T) {
	cfg := config.Default().Ingest
	ctx := context.Background()
	hard := filepath.Join("..", "..", "testdata", "hard")
	cases := []struct {
		file string
		id   string
		want string
	}{
		{"utf8-bom.json", "bom-1", "Проверка BOM"},
		{"cp1251.json", "cp1251-1", "Проверка кодировки"},
		{"koi8r.json", "koi8-1", "Проверка КОИ"},
		{"utf16.json", "utf16-1", "Проверка UTF-16"},
	}
	for _, tc := range cases {
		arts, journal, err := LoadDataset(ctx, []string{filepath.Join(hard, tc.file)}, cfg)
		if err != nil {
			t.Fatal(err)
		}
		if len(journal) != 0 || len(arts) != 1 {
			t.Fatalf("%s: arts=%d journal=%+v", tc.file, len(arts), journal)
		}
		if arts[0].ID != tc.id || arts[0].Title != tc.want {
			t.Fatalf("%s: %+v", tc.file, arts[0])
		}
	}
}

func TestNullByteRecord(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "hard", "nulls.json")
	arts, journal, err := LoadDataset(context.Background(), []string{path}, config.Default().Ingest)
	if err != nil {
		t.Fatal(err)
	}
	if len(journal) != 0 || len(arts) != 1 {
		t.Fatalf("arts=%d journal=%+v", len(arts), journal)
	}
	if !strings.Contains(arts[0].RawText, "\x00") || !strings.Contains(arts[0].Text, "\x00") {
		t.Fatal("нулевой байт должен сохраниться до нормализации")
	}
}

func TestUTF16WithoutBOM(t *testing.T) {
	s := "Привет, журнал"
	encoded := utf16.Encode([]rune(s))
	raw := make([]byte, len(encoded)*2)
	for i, unit := range encoded {
		raw[2*i] = byte(unit)
		raw[2*i+1] = byte(unit >> 8)
	}
	if got := decodeText(raw); got != s {
		t.Fatalf("got %q", got)
	}
}

func TestDuplicatesMissingIDAndEmpty(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	dup := write("dup.json", `[
		{"id":"dup-1","title":"Первая","text":"Первая запись достаточна для проверки дубля.","source":"ТАСС","url":"https://tass.ru/1","published_at":"2026-11-07T10:00:00+03:00"},
		{"id":"dup-1","title":"Вторая","text":"Вторая запись не должна заменить первую.","source":"ТАСС","url":"https://tass.ru/2","published_at":"2026-11-07T10:05:00+03:00"}
	]`)
	missing := write("noid.json", `[{"title":"Без номера","text":"Текст есть, идентификатора нет.","source":"Хакер","url":"https://xakep.ru/n","published_at":"2026-11-07T10:00:00+03:00"}]`)
	empty := write("empty.json", "")
	broken := write("broken.json", `{"id":`)
	blank := write("blank.json", "   \n")
	arts, journal, err := LoadDataset(context.Background(), []string{dup, missing, empty, broken, blank}, config.Default().Ingest)
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != 2 {
		t.Fatalf("articles: %+v", arts)
	}
	if arts[0].ID != "dup-1" || arts[0].Title != "Первая" {
		t.Fatalf("first dup: %+v", arts[0])
	}
	if arts[1].ID != "unknown-1" || arts[1].ReadError == "" {
		t.Fatalf("missing id: %+v", arts[1])
	}
	var codes []string
	for _, row := range journal {
		codes = append(codes, string(row.ReasonCode))
		if row.Status != "исключён" || row.Error == "" || row.ID == "" {
			t.Fatalf("journal: %+v", row)
		}
	}
	joined := strings.Join(codes, ",")
	for _, want := range []string{"duplicate_id", "read_error", "empty_input"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("нет %s в %s", want, joined)
		}
	}
}

func TestAliasesMarkersCSVAndWrapper(t *testing.T) {
	dir := t.TempDir()
	alias := filepath.Join(dir, "alias.json")
	body := `{"article_id":"a-1","headline":"Заголовок","body":"Текст алиасов про утечку.","publisher":"ТАСС","link":"https://tass.ru/a","pub_date":"07.11.2026, 11:45","extra":"N/A"}`
	if err := os.WriteFile(alias, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	marked := filepath.Join(dir, "marked.json")
	markedBody := `[{"id":"m-1","title":"N/A","text":"null","source":"не заполнено","url":"","published_at":"2026-11-07T10:00:00+03:00"}]`
	if err := os.WriteFile(marked, []byte(markedBody), 0o644); err != nil {
		t.Fatal(err)
	}
	wrap := filepath.Join(dir, "wrap.json")
	if err := os.WriteFile(wrap, []byte(`{"articles":[{"id":"w-1","title":"В обёртке","text":"Текст.","source":"CNews","url":"https://safe.cnews.ru/1","published_at":"2026-11-07T09:00:00+03:00"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(dir, "custom.json")
	if err := os.WriteFile(custom, []byte(`[{"news_id":"c-1","headline":"Своё поле","full":"Текст своего поля.","publisher":"Хакер","link":"https://xakep.ru/c","when":"2026-11-07T08:00:00+03:00"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	semi := filepath.Join(dir, "semi.csv")
	csvBody := "id;title;text;source;url;published_at\r\n" +
		"s-1;\"Заголовок; точка\";Текст csv;ТАСС;https://tass.ru/s;2026-11-07T10:00:00+03:00\r\n"
	if err := os.WriteFile(semi, []byte(csvBody), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default().Ingest
	arts, journal, err := LoadDataset(context.Background(), []string{alias, marked, wrap, semi}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(journal) != 0 {
		t.Fatalf("journal: %+v", journal)
	}
	byID := map[string]string{}
	missing := map[string][]string{}
	for _, a := range arts {
		byID[a.ID] = a.Title
		missing[a.ID] = a.MissingFields
		if a.PublishedAt != nil {
			t.Fatalf("%s: дату разбирает этап S2, сейчас PublishedAt должен быть пустым", a.ID)
		}
	}
	if byID["a-1"] != "Заголовок" || byID["w-1"] != "В обёртке" || byID["s-1"] != "Заголовок; точка" {
		t.Fatalf("titles: %#v", byID)
	}
	if strings.Join(missing["m-1"], ",") != "title,text,source,url" {
		t.Fatalf("markers: %#v", missing["m-1"])
	}
	customCfg := config.Default().Ingest
	customCfg.FieldMap = map[string]string{
		"id": "news_id", "title": "headline", "text": "full",
		"source": "publisher", "url": "link", "published_at": "when",
	}
	arts, _, err = LoadDataset(context.Background(), []string{custom}, customCfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != 1 || arts[0].ID != "c-1" || arts[0].Text != "Текст своего поля." {
		t.Fatalf("custom map: %+v", arts)
	}
}

func TestJSONLDirectoryAndCommaCSV(t *testing.T) {
	dir := t.TempDir()
	jsonl := " {\"id\":\"j-1\",\"title\":\"Один\",\"text\":\"Первая строка.\",\"source\":\"Хакер\",\"url\":\"https://xakep.ru/1\",\"published_at\":\"2026-11-07T10:00:00+03:00\"}\n" +
		"{битый\n" +
		"\n" +
		"{\"id\":\"j-2\",\"title\":\"Два\",\"text\":\"Вторая строка.\",\"source\":\"Хакер\",\"url\":\"https://xakep.ru/2\",\"published_at\":\"2026-11-07T10:01:00+03:00\"}\n"
	if err := os.WriteFile(filepath.Join(dir, "rows.jsonl"), []byte(jsonl), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "pack")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	note := "Заголовок файла\nТекст файла-статьи про сбой VPN.\n"
	if err := os.WriteFile(filepath.Join(sub, "note.txt"), []byte(note), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "README.md"), []byte("это описание, не статья"), 0o644); err != nil {
		t.Fatal(err)
	}
	csvBody := "id,title,text,source,url,published_at\n" +
		"csv-1,\"Заголовок, с запятой\",\"Текст, внутри\",ТАСС,https://tass.ru/csv,2026-11-07T10:00:00+03:00\n"
	if err := os.WriteFile(filepath.Join(sub, "comma.csv"), []byte(csvBody), 0o644); err != nil {
		t.Fatal(err)
	}
	arts, journal, err := LoadDataset(context.Background(), []string{dir}, config.Default().Ingest)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, a := range arts {
		ids[a.ID] = true
	}
	for _, id := range []string{"j-1", "j-2", "note", "csv-1"} {
		if !ids[id] {
			t.Fatalf("нет %s в %#v", id, ids)
		}
	}
	if ids["README"] {
		t.Fatal("README не должен становиться статьёй")
	}
	if len(journal) != 1 || journal[0].ReasonCode != "read_error" {
		t.Fatalf("journal: %+v", journal)
	}
}

func TestDatasetWrapperWithDescription(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "news.json")
	body := `{
	  "dataset": "train",
	  "control_time": "2026-11-06T10:00:00+03:00",
	  "description": "Учебный набор: описание файла, не текст новости.",
	  "items": [
	    {"id":"TR-01","title":"Атака","text":"Склады остановлены.","source":"КиберВестник","url":"https://kibervestnik.example/1","published_at":"2026-11-05T14:20:00+03:00","missing":[],"error":null},
	    {"id":"TR-28","title":"Облако","text":null,"source":"КиберВестник","url":"https://kibervestnik.example/2","published_at":"2026-11-06T09:40:00+03:00","missing":["text"],"error":"Не удалось загрузить текст"}
	  ]
	}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	arts, journal, err := LoadDataset(context.Background(), []string{path}, config.Default().Ingest)
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != 2 {
		t.Fatalf("записей %d, журнал %+v", len(arts), journal)
	}
	if arts[0].ID != "TR-01" || arts[0].Text != "Склады остановлены." || arts[0].ReadError != "" {
		t.Fatalf("TR-01: %+v", arts[0])
	}
	if arts[1].ID != "TR-28" || arts[1].ReadError != "Не удалось загрузить текст" {
		t.Fatalf("TR-28: %+v", arts[1])
	}
	if len(journal) != 1 || journal[0].ID != "TR-28" || journal[0].ReasonCode != "read_error" {
		t.Fatalf("journal: %+v", journal)
	}
}

func TestMissingPath(t *testing.T) {
	_, _, err := LoadDataset(context.Background(), []string{filepath.Join(t.TempDir(), "nope.json")}, config.Default().Ingest)
	if err == nil {
		t.Fatal("ожидалась ошибка отсутствующего файла")
	}
}
