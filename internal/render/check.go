package render

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"mono/internal/textutil"
)

var (
	headerRE = regexp.MustCompile(`(?m)^# Дайджест по кибербезопасности: .+, контрольное время .+$`)
	countRE  = regexp.MustCompile(`(?m)^Событий в выпуске: (\d+)\b`)
	itemRE   = regexp.MustCompile(`(?m)^## (\d+)\. \S`)
)

// CheckFormat reports whether mono.md follows the stage format.
// It checks the header, the event count, consecutive ## N. blocks and the four fields.
func CheckFormat(digest string) error {
	if strings.TrimSpace(digest) == "" {
		return fmt.Errorf("пустой дайджест")
	}
	if textutil.HasLongDash(digest) {
		return fmt.Errorf("длинное тире")
	}
	if strings.Contains(digest, "Не вошли") {
		return fmt.Errorf("лишний блок")
	}
	if n := textutil.CountChars(digest); n > DigestLimit {
		return fmt.Errorf("лимит знаков: %d", n)
	}
	if !headerRE.MatchString(digest) {
		return fmt.Errorf("заголовок выпуска не совпадает с форматом")
	}
	countMatch := countRE.FindStringSubmatch(digest)
	if countMatch == nil {
		return fmt.Errorf("нет строки «Событий в выпуске»")
	}
	count, err := strconv.Atoi(countMatch[1])
	if err != nil {
		return fmt.Errorf("число событий: %w", err)
	}
	matches := itemRE.FindAllStringSubmatchIndex(digest, -1)
	if count == 0 {
		if len(matches) != 0 {
			return fmt.Errorf("при нуле событий не должно быть блоков")
		}
		if !strings.Contains(digest, emptyEvents) {
			return fmt.Errorf("нет сообщения о пустом выпуске")
		}
		return nil
	}
	if count > 5 {
		return fmt.Errorf("событий больше пяти: %d", count)
	}
	if len(matches) != count {
		return fmt.Errorf("блоков %d, в строке счёта %d", len(matches), count)
	}
	for i, loc := range matches {
		n, convErr := strconv.Atoi(digest[loc[2]:loc[3]])
		if convErr != nil {
			return convErr
		}
		if n != i+1 {
			return fmt.Errorf("номер %d, ожидался %d", n, i+1)
		}
		end := len(digest)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		if err := checkBlock(digest[loc[0]:end]); err != nil {
			return err
		}
	}
	return nil
}

func checkBlock(block string) error {
	labels := []string{"Суть: ", "Последствия: ", "Источники: ", "Обоснование: "}
	prev := -1
	for _, label := range labels {
		at := strings.Index(block, "\n"+label)
		if at < 0 || at <= prev {
			return fmt.Errorf("в блоке нет четырёх полей в нужном порядке")
		}
		rest := block[at+1+len(label):]
		line := rest
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			line = rest[:nl]
		}
		if strings.TrimSpace(line) == "" {
			return fmt.Errorf("пустое поле %s", strings.TrimSpace(label))
		}
		prev = at
	}
	return nil
}
