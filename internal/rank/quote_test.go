package rank

import (
	"strings"
	"testing"

	"mono/internal/domain"
)

func TestFindQuoteCaseAndTitleGlue(t *testing.T) {
	title := "В утечке «ПолисМед» называют уже 640 000 записей"
	text := "Со ссылкой на канал, который первым выложил образец, сообщается о 640 000 записях."
	arts := []domain.Article{{ID: "pub-007", Title: title, Text: text}}
	got, ok := findQuote(arts, "со ссылкой на канал, который первым выложил образец")
	if !ok || got != "Со ссылкой на канал, который первым выложил образец" {
		t.Fatalf("case: %q %v", got, ok)
	}
	glued, ok := findQuote(arts, title+" "+text)
	if !ok || !strings.Contains(text, glued) {
		t.Fatalf("glue: %q %v", glued, ok)
	}
}
