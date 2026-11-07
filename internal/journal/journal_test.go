package journal

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"cyberdigest/internal/domain"
)

func TestWriteCSVColumnsOrderAndStatuses(t *testing.T) {
	j := New()
	j.Set("b", domain.StatusExcluded, "", domain.ReasonOutOfPeriod, "Публикация вне периода 06.11.2026 12:00 - 07.11.2026 12:00 МСК", "")
	j.Set("a", domain.StatusIncluded, "E01", domain.ReasonSelected, `Событие вошло в выпуск: утечка, "840 000" записей`, "")
	j.Set("c", domain.StatusMerged, "E01", domain.ReasonMerged, "Сообщение о том же событии (E01), ссылка сохранена в источниках", "")
	j.Note("a", "period", "Дата без пояса, принята как МСК")

	var buf bytes.Buffer
	if err := j.WriteCSV(&buf); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	if bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatal("csv has a BOM")
	}
	if !bytes.HasPrefix(raw, []byte("id,status,group,reason,error\n")) {
		t.Fatalf("header %q", string(raw[:40]))
	}
	r := csv.NewReader(&buf)
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("rows %d", len(rows))
	}
	if strings.Join(rows[0], ",") != "id,status,group,reason,error" {
		t.Fatalf("columns %v", rows[0])
	}
	if rows[1][0] != "b" || rows[1][1] != "исключён" || rows[1][2] != "" {
		t.Fatalf("row b %#v", rows[1])
	}
	if rows[2][0] != "a" || rows[2][1] != "включён" || rows[2][2] != "E01" {
		t.Fatalf("row a %#v", rows[2])
	}
	if rows[2][3] != `Событие вошло в выпуск: утечка, "840 000" записей` {
		t.Fatalf("quoted reason %q", rows[2][3])
	}
	if rows[3][1] != "объединён" || rows[3][2] != "E01" {
		t.Fatalf("row c %#v", rows[3])
	}
	for _, row := range rows {
		if len(row) != 5 {
			t.Fatalf("width %d", len(row))
		}
	}

	got, ok := j.Get("a")
	if !ok || len(got.Trace) != 1 || got.Trace[0].Stage != "period" {
		t.Fatalf("trace %#v", got.Trace)
	}
	got.Trace[0].Note = "changed"
	again, _ := j.Get("a")
	if again.Trace[0].Note == "changed" {
		t.Fatal("Get returned a live slice")
	}
}

func TestWriteJSONExtendedFields(t *testing.T) {
	j := New()
	j.Add(domain.JournalEntry{
		ID: "a", Status: domain.StatusExcluded, ReasonCode: domain.ReasonReadError,
		Reason: "Не удалось прочитать запись", Error: "битый json",
		Source: "Хакер", SourceKind: domain.SourceMedia, PublishedAtMSK: "",
	})
	var buf bytes.Buffer
	if err := j.WriteJSON(&buf); err != nil {
		t.Fatal(err)
	}
	var rows []domain.JournalEntry
	if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ReasonCode != domain.ReasonReadError || rows[0].Error != "битый json" {
		t.Fatalf("%#v", rows)
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"reason_code"`)) || !bytes.Contains(buf.Bytes(), []byte(`"source_kind"`)) {
		t.Fatalf("json %s", buf.String())
	}
}

func TestValidateMissingIDAndBadStatus(t *testing.T) {
	j := New()
	j.Set("a", domain.StatusIncluded, "", domain.ReasonSelected, "Событие вошло в выпуск", "")
	j.Add(domain.JournalEntry{ID: "bad", Status: "включен"})
	j.Add(domain.JournalEntry{ID: "m", Status: domain.StatusMerged})

	err := j.Validate([]string{"a", "gone"})
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	for _, part := range []string{"нет записи для id gone", `статус "включен" недопустим`, "лишняя запись id bad", "статус «объединён» без группы у id m"} {
		if !strings.Contains(msg, part) {
			t.Fatalf("missing %q in %s", part, msg)
		}
	}

	okJournal := New()
	okJournal.Set("a", domain.StatusIncluded, "", domain.ReasonSelected, Text(domain.ReasonSelected, Data{Rationale: "масштаб"}), "")
	okJournal.Set("b", domain.StatusMerged, "E02", domain.ReasonMerged, Text(domain.ReasonMerged, Data{EventID: "E02"}), "")
	okJournal.Set("c", domain.StatusExcluded, "", domain.ReasonDateUnconfirmed, Text(domain.ReasonDateUnconfirmed, Data{}), "")
	if err := okJournal.Validate([]string{"c", "a", "b"}); err != nil {
		t.Fatal(err)
	}
	if err := okJournal.Validate([]string{"a"}); err == nil {
		t.Fatal("extra rows must fail")
	}
}

func TestNoteBeforeSet(t *testing.T) {
	j := New()
	j.Note("a", "period", "Дата без пояса, принята как МСК")
	j.Set("a", domain.StatusExcluded, "", domain.ReasonOutOfPeriod, "Публикация вне периода", "")
	got, ok := j.Get("a")
	if !ok || len(got.Trace) != 1 || got.Trace[0].Note == "" {
		t.Fatalf("%#v", got.Trace)
	}
}

func TestJournalConcurrent(t *testing.T) {
	j := New()
	var wg sync.WaitGroup
	ids := make([]string, 40)
	for i := range ids {
		ids[i] = fmt.Sprintf("id-%02d", i)
	}
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			j.Note(id, "period", "заметка")
			j.Set(id, domain.StatusExcluded, "", domain.ReasonOutOfPeriod, "вне периода", "")
			if _, ok := j.Get(id); !ok {
				t.Errorf("нет записи %s", id)
			}
		}(id)
	}
	wg.Wait()
	if err := j.Validate(ids); err != nil {
		t.Fatal(err)
	}
	if len(j.Entries()) != len(ids) {
		t.Fatalf("entries %d", len(j.Entries()))
	}
}
