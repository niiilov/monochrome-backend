package mandatory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"mono/internal/extract"
	"mono/internal/topic"
)

func TestMandatoryScenarios(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "mandatory")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, entry.Name())
		}
	}
	if len(dirs) != 8 {
		t.Fatalf("scenarios %d", len(dirs))
	}
	want := []string{"T1", "T2", "T3", "T4", "T5", "T6", "T7", "T8"}
	for i, dir := range dirs {
		t.Run(dir, func(t *testing.T) {
			scenarioDir := filepath.Join(root, dir)
			for _, name := range []string{"input.json", "expected.yaml", "description.md", "control_time.txt"} {
				if _, err := os.Stat(filepath.Join(scenarioDir, name)); err != nil {
					t.Fatal(err)
				}
			}
			exp, err := Load(filepath.Join(scenarioDir, "expected.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			if exp.ID != want[i] {
				t.Fatalf("id %s", exp.ID)
			}
			clock, err := os.ReadFile(filepath.Join(scenarioDir, "control_time.txt"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(string(clock)) != "2026-11-07T12:00:00+03:00" || strings.TrimSpace(string(clock)) != strings.TrimSpace(exp.ControlTime) {
				t.Fatalf("control time %q yaml %q", clock, exp.ControlTime)
			}
			assertCleanTree(t, scenarioDir)
			articles := loadArticles(t, filepath.Join(scenarioDir, "input.json"))
			byID := map[string]article{}
			for _, article := range articles {
				byID[article.ID] = article
			}
			for _, row := range exp.Journal {
				if isUnknownID(row.ID) {
					continue
				}
				article, ok := byID[row.ID]
				if !ok {
					t.Fatalf("journal id %s is not in input.json", row.ID)
				}
				switch row.ReasonCode {
				case "off_topic":
					if hits := topic.Scan(article.Title, article.Text); len(hits) != 0 {
						t.Fatalf("%s dictionary hit %+v", row.ID, hits)
					}
				case "insufficient_text":
					if extract.EnoughText(article.Text, 200) {
						t.Fatalf("%s text is long enough", row.ID)
					}
				case "selected", "merged", "stale_no_new_facts":
					if !extract.EnoughText(article.Text, 200) {
						t.Fatalf("%s text is too short: %d", row.ID, utf8.RuneCountInString(article.Text))
					}
					if len(topic.Scan(article.Title, article.Text)) == 0 {
						t.Fatalf("%s has no security terms", row.ID)
					}
				}
			}
			for _, name := range exp.Dataset {
				if _, err := os.Stat(filepath.Join(scenarioDir, name)); err != nil {
					t.Fatal(err)
				}
			}
			got := Check(exp, writeRun(t, exp, runOpts{}))
			if !got.Passed {
				t.Fatalf("%+v", got.Failures)
			}
		})
	}
}

func TestInjectionsExpect(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "injections")
	set, err := LoadInjections(filepath.Join(root, "expected.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		ID    string `json:"id"`
		Place string `json:"place"`
		Text  string `json:"text"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 20 || len(set.Cases) != len(cases) {
		t.Fatalf("cases %d expectations %d", len(cases), len(set.Cases))
	}
	if len(set.Recipients) != 0 || !set.EventsUnchanged || !set.OrderUnchanged || !set.RulesUnchanged {
		t.Fatalf("%+v", set)
	}
	places := map[string]int{}
	byID := map[string]int{}
	for i, item := range cases {
		places[item.Place]++
		byID[item.ID] = i
		if strings.Contains(item.Title+item.Text, "\u2014") || strings.Contains(item.Title+item.Text, "\u2013") {
			t.Fatalf("long dash in %s", item.ID)
		}
	}
	for _, place := range []string{"title", "text", "url", "csv"} {
		if places[place] == 0 {
			t.Fatalf("no %s", place)
		}
	}
	for _, item := range set.Cases {
		if _, ok := byID[item.ID]; !ok {
			t.Fatalf("unknown expectation %s", item.ID)
		}
	}
}

func TestHardExpect(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "hard")
	set, err := LoadHard(filepath.Join(root, "expectations.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, item := range set.Cases {
		kinds[item.Kind] = true
		if _, err := os.Stat(filepath.Join(root, item.File)); err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range []string{"empty", "encoding", "huge", "nulls"} {
		if !kinds[kind] {
			t.Fatalf("missing kind %s", kind)
		}
	}
	raw, err := os.ReadFile(filepath.Join(root, "expectations.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "\u2014") || strings.Contains(string(raw), "\u2013") {
		t.Fatal("long dash")
	}
}

func assertCleanTree(t *testing.T, dir string) {
	t.Helper()
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(raw)
		if strings.Contains(text, "\u2014") || strings.Contains(text, "\u2013") {
			t.Fatalf("long dash in %s", path)
		}
		low := strings.ToLower(text)
		for _, secret := range []string{"llm_api_key", "sk-live", "smtp_password", "begin private"} {
			if strings.Contains(low, secret) {
				t.Fatalf("secret marker %s in %s", secret, path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

type article struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

func loadArticles(t *testing.T, path string) []article {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var items []article
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatal("empty input")
	}
	return items
}

var unknownID = regexp.MustCompile(`^unknown-\d+$`)

func isUnknownID(id string) bool {
	return unknownID.MatchString(id)
}
