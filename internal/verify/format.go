package verify

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"mono/internal/render"
)

// CheckFormat is the built-in stand-in for the organizers' check_mono.py.
// digestMD must follow section 6.1. journalCSV is UTF-8 without a BOM and
// uses the header id,status,group,reason,error.
func CheckFormat(digestMD, journalCSV string) error {
	var parts []string
	if err := render.CheckFormat(digestMD); err != nil {
		parts = append(parts, "дайджест: "+err.Error())
	}
	if err := checkJournalCSV(journalCSV); err != nil {
		parts = append(parts, "журнал: "+err.Error())
	}
	if len(parts) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(parts, "; "))
}

func checkJournalCSV(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("пустой файл")
	}
	if strings.HasPrefix(raw, "\uFEFF") || bytes.HasPrefix([]byte(raw), []byte{0xEF, 0xBB, 0xBF}) {
		return fmt.Errorf("файл содержит BOM")
	}
	if !utf8.ValidString(raw) {
		return fmt.Errorf("файл не в UTF-8")
	}
	reader := csv.NewReader(strings.NewReader(raw))
	reader.FieldsPerRecord = 5
	header, err := reader.Read()
	if err != nil {
		return fmt.Errorf("не читается заголовок: %w", err)
	}
	want := []string{"id", "status", "group", "reason", "error"}
	for i, column := range want {
		if strings.TrimSpace(header[i]) != column {
			return fmt.Errorf("заголовок должен быть id,status,group,reason,error")
		}
	}
	seen := map[string]bool{}
	line := 1
	for {
		line++
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("строка %d: %w", line, err)
		}
		id := strings.TrimSpace(row[0])
		status := strings.TrimSpace(row[1])
		group := strings.TrimSpace(row[2])
		if id == "" {
			return fmt.Errorf("строка %d: пустой id", line)
		}
		if seen[id] {
			return fmt.Errorf("строка %d: повтор id %s", line, id)
		}
		seen[id] = true
		switch status {
		case "включён", "объединён", "исключён":
		default:
			return fmt.Errorf("строка %d: статус %q недопустим", line, status)
		}
		if status == "объединён" && group == "" {
			return fmt.Errorf("строка %d: статус «объединён» без группы", line)
		}
	}
	return nil
}
