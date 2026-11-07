package live

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"cyberdigest/internal/config"
	"cyberdigest/internal/ingest"
	"cyberdigest/internal/normalize"
)

func TestFoldKeepsHeadAndTail(t *testing.T) {
	var b strings.Builder
	b.WriteString("HEADTOKEN начало длинной статьи про утечку. Второе предложение первого блока.")
	for i := 0; i < 8; i++ {
		b.WriteString("\n\nБлок заполняет страницу. ")
		b.WriteString(strings.Repeat("слово ", 40))
	}
	b.WriteString("\n\nПоследний блок закрывает материал. TAILTOKEN 07.11.2026 1000.")
	text := b.String()
	parts := normalize.Chunks(text, 400)
	if len(parts) < 3 {
		t.Fatalf("чанков %d", len(parts))
	}
	folded := FoldParts(text, 800)
	if utf8.RuneCountInString(folded) > 800 {
		t.Fatalf("длина %d", utf8.RuneCountInString(folded))
	}
	if !strings.Contains(folded, "HEADTOKEN") || !strings.Contains(folded, "TAILTOKEN") {
		t.Fatalf("сводка потеряла край: %s", folded)
	}
	if !strings.Contains(folded, "07.11.2026") || !strings.Contains(folded, "1000") {
		t.Fatalf("сводка потеряла дату или число: %s", folded)
	}
	if FoldParts("короткий текст", 100) != "короткий текст" {
		t.Fatal("короткий текст не должен меняться")
	}
}

func TestLargeFixtureFolds(t *testing.T) {
	path := filepath.Join("..", "..", "..", "testdata", "hard", "large.json")
	arts, journal, err := ingest.LoadDataset(context.Background(), []string{path}, config.Default().Ingest)
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != 1 || len(journal) != 0 {
		t.Fatalf("arts %d journal %+v", len(arts), journal)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 100000 {
		t.Fatalf("файл %d байт", len(raw))
	}
	parts := normalize.Chunks(arts[0].Text, 4000)
	if len(parts) < 2 {
		t.Fatalf("чанков %d", len(parts))
	}
	folded := FoldParts(arts[0].Text, 4000)
	if folded == "" || utf8.RuneCountInString(folded) > 4000 {
		t.Fatalf("сводка %d", utf8.RuneCountInString(folded))
	}
	if utf8.RuneCountInString(folded) >= utf8.RuneCountInString(arts[0].Text) {
		t.Fatal("сводка не короче исходника")
	}
}
