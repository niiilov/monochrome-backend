package mandatory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Lines is the console summary. The last line is the acceptance count.
func (r Report) Lines() []string {
	out := make([]string, 0, len(r.Scenarios)+4)
	for _, sc := range r.Scenarios {
		out = append(out, fmt.Sprintf("%s %s: %s", sc.ID, sc.Title, sc.Result))
		for _, fail := range sc.Failures {
			out = append(out, "  - "+fail.Message)
		}
	}
	out = append(out, fmt.Sprintf("обязательные сценарии: %s", r.Summary))
	if r.Injections.Total == 0 {
		out = append(out, "инъекции: не запускались")
	} else {
		word := "не пройдены"
		if r.Injections.Passed {
			word = "пройдены"
		}
		out = append(out, fmt.Sprintf("инъекции: %d/%d %s", r.Injections.PassCount, r.Injections.Total, word))
		for _, item := range r.Injections.Cases {
			if item.Passed {
				continue
			}
			for _, fail := range item.Failures {
				out = append(out, "  - "+item.ID+": "+fail.Message)
			}
		}
	}
	if r.Hard.Passed {
		out = append(out, fmt.Sprintf("трудные входы: прогон не упал, журнал полон (%d строк)", r.Hard.Journal))
	} else {
		out = append(out, "трудные входы: не пройдены")
		for _, fail := range r.Hard.Failures {
			out = append(out, "  - "+fail.Message)
		}
	}
	return out
}

// OK reports whether the mock acceptance criteria passed.
func (r Report) OK() bool {
	return r.Total > 0 && r.Passed == r.Total && r.Injections.Passed && r.Hard.Passed
}

func writeReports(dir string, rep Report) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	body := redactSecrets(string(raw))
	if err := os.WriteFile(filepath.Join(dir, "tests_report.json"), []byte(body), 0o644); err != nil {
		return err
	}
	md := redactSecrets(markdownReport(rep))
	return os.WriteFile(filepath.Join(dir, "tests_report.md"), []byte(md), 0o644)
}

func markdownReport(rep Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Отчёт обязательных сценариев\n\n")
	fmt.Fprintf(&b, "- Режим: %s\n", rep.Mode)
	fmt.Fprintf(&b, "- Модель: %s\n", emptyDash(rep.Model))
	fmt.Fprintf(&b, "- Хэш промптов: %s\n", emptyDash(rep.PromptsHash))
	fmt.Fprintf(&b, "- Старт: %s\n", rep.StartedAt)
	fmt.Fprintf(&b, "- Финиш: %s\n", rep.FinishedAt)
	fmt.Fprintf(&b, "- Итог: %s\n\n", rep.Summary)
	b.WriteString("## Настройки\n\n")
	for _, key := range []string{"provider", "model", "temperature", "seed", "max_concurrency", "max_events", "verify_max_retries", "period_hours", "naive_tz_policy"} {
		if v, ok := rep.Settings[key]; ok {
			fmt.Fprintf(&b, "- %s: %v\n", key, v)
		}
	}
	b.WriteString("\n## Сценарии\n\n")
	for _, sc := range rep.Scenarios {
		fmt.Fprintf(&b, "### %s. %s\n\n", sc.ID, sc.Title)
		fmt.Fprintf(&b, "Результат: %s. Модель: %s. Хэш промптов: %s. Старт: %s.\n\n", sc.Result, emptyDash(sc.Model), emptyDash(sc.PromptsHash), sc.StartedAt)
		if sc.RunError != "" {
			fmt.Fprintf(&b, "Ошибка прогона: %s\n\n", sc.RunError)
		}
		if len(sc.Failures) > 0 {
			b.WriteString("Замечания:\n\n")
			for _, fail := range sc.Failures {
				fmt.Fprintf(&b, "- %s: %s\n", fail.Check, fail.Message)
			}
			b.WriteString("\n")
		}
		b.WriteString("Входные данные:\n\n")
		fmt.Fprintf(&b, "Контрольное время: %s\n\n", sc.Input.ControlTime)
		for name, body := range sc.Input.Files {
			fmt.Fprintf(&b, "Файл `%s`:\n\n```\n%s\n```\n\n", name, strings.TrimRight(body, "\n"))
		}
		b.WriteString("Дайджест:\n\n```\n")
		b.WriteString(strings.TrimRight(sc.Digest, "\n"))
		b.WriteString("\n```\n\nЖурнал:\n\n```\n")
		b.WriteString(strings.TrimRight(sc.Journal, "\n"))
		b.WriteString("\n```\n\n")
	}
	b.WriteString("## Инъекции\n\n")
	fmt.Fprintf(&b, "Нейтральный набор: `%s`. Событий: %d. Пройдено: %d/%d.\n\n", rep.Injections.Neutral, rep.Injections.Baseline, rep.Injections.PassCount, rep.Injections.Total)
	if len(rep.Injections.Order) > 0 {
		b.WriteString("Порядок:\n\n")
		for i, title := range rep.Injections.Order {
			fmt.Fprintf(&b, "%d. %s\n", i+1, title)
		}
		b.WriteString("\n")
	}
	for _, item := range rep.Injections.Cases {
		state := "пройдена"
		if !item.Passed {
			state = "не пройдена"
		}
		fmt.Fprintf(&b, "- %s: %s\n", item.ID, state)
		for _, fail := range item.Failures {
			fmt.Fprintf(&b, "  - %s\n", fail.Message)
		}
	}
	b.WriteString("\n## Трудные входы\n\n")
	if rep.Hard.Passed {
		fmt.Fprintf(&b, "Прогон не упал. Строк журнала: %d.\n", rep.Hard.Journal)
	} else {
		b.WriteString("Прогон не принят.\n\n")
		for _, fail := range rep.Hard.Failures {
			fmt.Fprintf(&b, "- %s\n", fail.Message)
		}
	}
	b.WriteString("\n")
	return b.String()
}

func emptyDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// redactSecrets removes values of known secret environment variables.
// An empty variable is ignored so the replacement cannot wipe the report.
func redactSecrets(s string) string {
	for _, key := range []string{"LLM_API_KEY", "SMTP_PASSWORD", "SMTP_USER", "SMTP_FROM"} {
		value := strings.TrimSpace(os.Getenv(key))
		if value == "" {
			continue
		}
		s = strings.ReplaceAll(s, value, "[redacted]")
	}
	return s
}
