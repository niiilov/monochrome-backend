package ingest

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mono/internal/config"
	"mono/internal/domain"
)

func TestHardExpectationsLoad(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "hard")
	list := expectationFiles(t, filepath.Join(dir, "expectations.yaml"))
	if len(list) < 10 {
		t.Fatalf("мало ожиданий: %d", len(list))
	}
	cfg := config.Default().Ingest
	for _, name := range list {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, _, err := LoadDataset(context.Background(), []string{path}, cfg); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestZeroFieldsAndHugeValues(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "hard")
	cfg := config.Default().Ingest
	arts, journal, err := LoadDataset(context.Background(), []string{filepath.Join(dir, "zero-fields.json")}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(journal) != 0 {
		t.Fatalf("журнал: %+v", journal)
	}
	byID := map[string]domain.Article{}
	for _, article := range arts {
		byID[article.ID] = article
	}
	zero, ok := byID["zero-1"]
	if !ok || strings.TrimSpace(zero.Title) != "" {
		t.Fatalf("zero-1: %+v", zero)
	}
	if !contains(zero.MissingFields, "title") || strings.TrimSpace(zero.Text) == "" {
		t.Fatalf("zero-1 fields: %+v", zero)
	}
	only, ok := byID["zero-2"]
	if !ok || strings.TrimSpace(only.Text) != "" || strings.TrimSpace(only.Title) == "" {
		t.Fatalf("zero-2: %+v", only)
	}
	if !contains(only.MissingFields, "text") {
		t.Fatalf("zero-2 missing: %+v", only.MissingFields)
	}

	arts, journal, err = LoadDataset(context.Background(), []string{filepath.Join(dir, "huge.json")}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(journal) != 0 || len(arts) != 1 || arts[0].ID != "huge-1" {
		t.Fatalf("arts %+v journal %+v", arts, journal)
	}
	if !strings.Contains(arts[0].Text, "999999999999999999999999999999") {
		t.Fatal("огромное число потеряно")
	}

	arts, journal, err = LoadDataset(context.Background(), []string{filepath.Join(dir, "empty.json")}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != 0 || len(journal) != 1 || journal[0].ReasonCode != domain.ReasonEmptyInput {
		t.Fatalf("пустой файл: arts %d journal %+v", len(arts), journal)
	}
}

func expectationFiles(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		idx := strings.Index(line, "file:")
		if idx < 0 {
			continue
		}
		name := strings.TrimSpace(line[idx+len("file:"):])
		if name != "" {
			out = append(out, name)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
