package verify

import (
	"bytes"
	"strings"
	"testing"

	"cyberdigest/internal/domain"
	"cyberdigest/internal/journal"
	"cyberdigest/internal/render"
)

func TestCheckFormatAcceptsDigestAndJournal(t *testing.T) {
	w := sample()
	digest := render.Digest(w.model)
	csvText := sampleJournal()
	if err := CheckFormat(digest, csvText); err != nil {
		t.Fatal(err)
	}
	empty := sample()
	empty.model.Items = nil
	empty.model.Empty = true
	empty.model.Shortfall = "все материалы вне темы"
	if err := CheckFormat(render.Digest(empty.model), csvText); err != nil {
		t.Fatal(err)
	}
}

func TestCheckFormatRejectsDigestAndJournal(t *testing.T) {
	w := sample()
	goodDigest := render.Digest(w.model)
	goodJournal := sampleJournal()
	if err := CheckFormat("просто текст", goodJournal); err == nil {
		t.Fatal("битый дайджест прошёл")
	}
	broken := strings.Replace(goodDigest, "Суть: ", "", 1)
	if err := CheckFormat(broken, goodJournal); err == nil {
		t.Fatal("пункт без поля прошёл")
	}
	if err := CheckFormat(goodDigest, "\uFEFF"+goodJournal); err == nil || !strings.Contains(err.Error(), "BOM") {
		t.Fatalf("BOM: %v", err)
	}
	if err := CheckFormat(goodDigest, "id,status,group,reason,error\na1,принят,E01,причина,\n"); err == nil {
		t.Fatal("чужой статус прошёл")
	}
	merged := "id,status,group,reason,error\nb1,объединён,,без группы,\n"
	if err := CheckFormat(goodDigest, merged); err == nil || !strings.Contains(err.Error(), "без группы") {
		t.Fatalf("объединён без группы: %v", err)
	}
	dup := "id,status,group,reason,error\na1,включён,E01,a,\na1,исключён,,b,\n"
	if err := CheckFormat(goodDigest, dup); err == nil {
		t.Fatal("повтор id прошёл")
	}
}

func sampleJournal() string {
	j := journal.New()
	j.Add(domain.JournalEntry{
		ID:     "a1",
		Status: domain.StatusIncluded,
		Group:  "E01",
		Reason: "Событие вошло в выпуск",
	})
	j.Add(domain.JournalEntry{
		ID:     "a2",
		Status: domain.StatusMerged,
		Group:  "E01",
		Reason: "Сообщение о том же событии",
	})
	var buf bytes.Buffer
	if err := j.WriteCSV(&buf); err != nil {
		panic(err)
	}
	return buf.String()
}
