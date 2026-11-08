package mandatory

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"mono/internal/domain"
	"mono/internal/textutil"
)

const (
	checkFiles     = "files"
	checkEvents    = "events"
	checkOrder     = "order"
	checkSubstring = "substring"
	checkJournal   = "journal"
	checkLimit     = "limit"
	checkEmail     = "email"
	checkURL       = "url"
)

var (
	countRE = regexp.MustCompile(`(?m)^Событий в выпуске: (\d+)\b`)
	itemRE  = regexp.MustCompile(`(?m)^## (\d+)\. (.*)$`)
	hrefRE  = regexp.MustCompile(`(?i)href\s*=\s*("([^"]*)"|'([^']*)')`)
	tagRE   = regexp.MustCompile(`<[^>]*>`)
)

// Failure is one missed expectation. Check is a stable name, Message is Russian.
type Failure struct {
	Check   string `json:"check"`
	Message string `json:"message"`
}

// Result is the scenario report. Events is the number of "## N." blocks found.
type Result struct {
	Scenario string    `json:"scenario"`
	Passed   bool      `json:"passed"`
	Events   int       `json:"events"`
	Failures []Failure `json:"failures,omitempty"`
}

type journalRow struct {
	status string
	group  string
	reason string
	err    string
	code   string
	flags  []string
	seen   bool
}

// Check compares outDir with expected.
// A missing file or a broken journal is a failed check, not a Go error.
func Check(expected Expected, outDir string) Result {
	res := Result{Scenario: expected.ID, Passed: true}
	fail := func(check, msg string) {
		res.Passed = false
		res.Failures = append(res.Failures, Failure{Check: check, Message: msg})
	}

	digest := readOutput(outDir, "mono.md", true, fail)
	post := readOutput(outDir, "post.txt", true, fail)
	email := readOutput(outDir, "email.txt", true, fail)
	html, htmlOK := readOptional(outDir, "email_preview.html", fail)
	journalCSV := readOutput(outDir, "journal.csv", true, fail)
	journalJSON := readOutput(outDir, "journal.json", true, fail)

	if digest.ok {
		res.Events = checkDigest(expected, digest.body, fail)
	}
	checkSubstrings(outDir, expected, digest, post, email, html, htmlOK, fail)
	if post.ok {
		enforceLimit("post.txt", post.body, expected.Limits.Post, fail)
	}
	if digest.ok {
		enforceLimit("mono.md", digest.body, expected.Limits.Digest, fail)
	}
	checkContacts(expected, []namedBody{
		{"mono.md", digest},
		{"post.txt", post},
		{"email.txt", email},
		{"email_preview.html", fileBody{ok: htmlOK, body: html}},
	}, fail)
	if journalCSV.ok && journalJSON.ok {
		compareJournal(expected, journalCSV.body, journalJSON.body, fail)
	}
	return res
}

type fileBody struct {
	ok   bool
	body string
}

type namedBody struct {
	name string
	file fileBody
}

func readOutput(dir, name string, required bool, fail func(string, string)) fileBody {
	body, ok := readOptional(dir, name, fail)
	if !ok && required {
		return fileBody{}
	}
	return fileBody{ok: ok, body: body}
}

func readOptional(dir, name string, fail func(string, string)) (string, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		if os.IsNotExist(err) {
			if name != "email_preview.html" {
				fail(checkFiles, "нет файла "+name)
			}
			return "", false
		}
		fail(checkFiles, "не прочитан "+name+": "+err.Error())
		return "", false
	}
	if bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
		fail(checkFiles, name+" начинается с BOM")
		raw = raw[3:]
	}
	return string(raw), true
}

func checkDigest(expected Expected, digest string, fail func(string, string)) int {
	headings := itemRE.FindAllStringSubmatch(digest, -1)
	if len(headings) != expected.Events {
		fail(checkEvents, fmt.Sprintf("блоков «## N.»: %d, ожидалось %d", len(headings), expected.Events))
	}
	for i, heading := range headings {
		n, err := strconv.Atoi(heading[1])
		if err != nil || n != i+1 {
			fail(checkEvents, fmt.Sprintf("блок ## %s. нарушает сквозную нумерацию с 1", heading[1]))
		}
	}
	count := countRE.FindStringSubmatch(digest)
	if count == nil {
		fail(checkEvents, "нет строки «Событий в выпуске»")
	} else if n, err := strconv.Atoi(count[1]); err != nil || n != expected.Events {
		got := count[1]
		fail(checkEvents, fmt.Sprintf("в строке «Событий в выпуске» указано %s, ожидалось %d", got, expected.Events))
	}
	if len(expected.Order) > 0 && len(headings) == len(expected.Order) {
		for i, part := range expected.Order {
			title := strings.TrimSpace(headings[i][2])
			if !strings.Contains(title, part) {
				fail(checkOrder, fmt.Sprintf("заголовок %d %q не содержит %q", i+1, title, part))
			}
		}
	}
	return len(headings)
}

func checkSubstrings(outDir string, expected Expected, digest, post, email fileBody, html string, htmlOK bool, fail func(string, string)) {
	if digest.ok {
		requireAll(digest.body, "mono.md", expected.Digest.MustContain, fail)
		forbidAll(digest.body, "mono.md", expected.Digest.MustNotContain, fail)
		requireAny(digest.body, "mono.md", expected.Digest.MustContainAny, fail)
	}
	for _, item := range []namedBody{{"post.txt", post}, {"email.txt", email}} {
		if !item.file.ok {
			continue
		}
		forbidAll(item.file.body, item.name, expected.Digest.MustNotContain, fail)
	}
	if htmlOK {
		forbidAll(html, "email_preview.html", expected.Digest.MustNotContain, fail)
	}
	for name, rule := range expected.Texts {
		body, ok := readNamed(digest, post, email, html, htmlOK, name)
		if !ok {
			if isStandardOutput(name) {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(outDir, filepath.Base(name)))
			if err != nil {
				fail(checkFiles, "нет файла "+name)
				continue
			}
			body = string(raw)
		}
		requireAll(body, name, rule.MustContain, fail)
		forbidAll(body, name, rule.MustNotContain, fail)
	}
}

func isStandardOutput(name string) bool {
	switch name {
	case "mono.md", "post.txt", "email.txt", "email_preview.html":
		return true
	default:
		return false
	}
}

func readNamed(digest, post, email fileBody, html string, htmlOK bool, name string) (string, bool) {
	switch name {
	case "mono.md":
		return digest.body, digest.ok
	case "post.txt":
		return post.body, post.ok
	case "email.txt":
		return email.body, email.ok
	case "email_preview.html":
		return html, htmlOK
	default:
		return "", false
	}
}

func requireAll(body, name string, parts []string, fail func(string, string)) {
	for _, part := range parts {
		if part == "" {
			continue
		}
		if !strings.Contains(body, part) {
			fail(checkSubstring, fmt.Sprintf("в %s нет подстроки %q", name, part))
		}
	}
}

func forbidAll(body, name string, parts []string, fail func(string, string)) {
	for _, part := range parts {
		if part == "" {
			continue
		}
		if strings.Contains(body, part) {
			fail(checkSubstring, fmt.Sprintf("в %s есть запрещённая подстрока %q", name, part))
		}
	}
}

func requireAny(body, name string, groups [][]string, fail func(string, string)) {
	for _, group := range groups {
		ok := false
		for _, part := range group {
			if part != "" && strings.Contains(body, part) {
				ok = true
				break
			}
		}
		if !ok {
			fail(checkSubstring, fmt.Sprintf("в %s нет ни одного из вариантов %q", name, group))
		}
	}
}

func enforceLimit(name, body string, limit int, fail func(string, string)) {
	n := textutil.CountChars(body)
	if n > limit {
		fail(checkLimit, fmt.Sprintf("%s: %d знаков без URL, лимит %d", name, n, limit))
	}
}

func checkContacts(expected Expected, files []namedBody, fail func(string, string)) {
	allowURL := map[string]bool{}
	for _, raw := range expected.AllowURLs {
		allowURL[strings.TrimSpace(raw)] = true
	}
	allowMail := map[string]bool{}
	for _, raw := range append(append([]string{}, expected.AllowEmails...), expected.Recipients...) {
		mail := strings.ToLower(strings.TrimSpace(raw))
		if mail != "" {
			allowMail[mail] = true
		}
	}
	for _, item := range files {
		if !item.file.ok {
			continue
		}
		_, urls, mails := inspectOutput(item.name, item.file.body)
		seenURL := map[string]bool{}
		for _, raw := range urls {
			if seenURL[raw] {
				continue
			}
			seenURL[raw] = true
			if !urlAllowed(raw, allowURL) {
				fail(checkURL, fmt.Sprintf("в %s URL %s вне реестра", item.name, raw))
			}
		}
		seenMail := map[string]bool{}
		for _, raw := range mails {
			key := strings.ToLower(raw)
			if seenMail[key] {
				continue
			}
			seenMail[key] = true
			if !allowMail[key] {
				fail(checkEmail, fmt.Sprintf("в %s адрес %s вне списка получателей", item.name, raw))
			}
		}
	}
}

func inspectOutput(name, body string) (plain string, urls, mails []string) {
	if !strings.HasSuffix(strings.ToLower(name), ".html") {
		return body, textutil.ExtractURLs(body), textutil.ExtractEmails(body)
	}
	plain = tagRE.ReplaceAllString(body, " ")
	urls = append(urls, textutil.ExtractURLs(plain)...)
	mails = append(mails, textutil.ExtractEmails(plain)...)
	for _, match := range hrefRE.FindAllStringSubmatch(body, -1) {
		href := match[2]
		if href == "" {
			href = match[3]
		}
		href = strings.TrimSpace(href)
		low := strings.ToLower(href)
		switch {
		case strings.HasPrefix(low, "mailto:"):
			addr := strings.TrimSpace(href[len("mailto:"):])
			if i := strings.Index(addr, "?"); i >= 0 {
				addr = addr[:i]
			}
			mails = append(mails, addr)
		case strings.HasPrefix(low, "http://"), strings.HasPrefix(low, "https://"):
			urls = append(urls, href)
		}
	}
	return plain, urls, mails
}

func urlAllowed(raw string, allow map[string]bool) bool {
	if len(allow) == 0 {
		return false
	}
	if allow[raw] {
		return true
	}
	trim := strings.TrimRight(raw, "/")
	return allow[trim] || allow[trim+"/"]
}

func compareJournal(expected Expected, csvBody, jsonBody string, fail func(string, string)) {
	rows, order, err := parseCSV(csvBody)
	if err != nil {
		fail(checkJournal, err.Error())
		return
	}
	if err := mergeJSON(rows, jsonBody); err != nil {
		fail(checkJournal, err.Error())
		return
	}
	if len(order) != len(expected.Journal) {
		fail(checkJournal, fmt.Sprintf("строк журнала %d, ожидалось %d", len(order), len(expected.Journal)))
	}
	for i, want := range expected.Journal {
		if i < len(order) && order[i] != want.ID {
			fail(checkJournal, fmt.Sprintf("порядок журнала: на месте %d id %s, ожидался %s", i+1, order[i], want.ID))
		}
		got, ok := rows[want.ID]
		if !ok {
			fail(checkJournal, "нет записи для id "+want.ID)
			continue
		}
		if got.status != want.Status {
			fail(checkJournal, fmt.Sprintf("статус %s: получено %q, ожидалось %q", want.ID, got.status, want.Status))
		}
		if want.Group != nil && got.group != *want.Group {
			fail(checkJournal, fmt.Sprintf("группа %s: получено %q, ожидалось %q", want.ID, got.group, *want.Group))
		}
		if want.ReasonCode != "" && got.code != want.ReasonCode {
			fail(checkJournal, fmt.Sprintf("код причины %s: получено %q, ожидалось %q", want.ID, got.code, want.ReasonCode))
		}
		if want.ReasonContains != "" && !strings.Contains(got.reason, want.ReasonContains) {
			fail(checkJournal, fmt.Sprintf("причина %s не содержит %q", want.ID, want.ReasonContains))
		}
		if want.ErrorContains != "" && !strings.Contains(got.err, want.ErrorContains) {
			fail(checkJournal, fmt.Sprintf("колонка error у %s не содержит %q", want.ID, want.ErrorContains))
		}
		if want.ErrorEmpty && strings.TrimSpace(got.err) != "" {
			fail(checkJournal, fmt.Sprintf("колонка error у %s не пуста: %q", want.ID, got.err))
		}
		for _, flag := range want.RequireFlags {
			if !hasFlag(got.flags, flag) {
				fail(checkJournal, fmt.Sprintf("у %s нет флага %s", want.ID, flag))
			}
		}
	}
	wantIDs := map[string]bool{}
	for _, row := range expected.Journal {
		wantIDs[row.ID] = true
	}
	for _, id := range order {
		if !wantIDs[id] {
			fail(checkJournal, "лишняя запись id "+id)
		}
	}
}

func parseCSV(body string) (map[string]*journalRow, []string, error) {
	r := csv.NewReader(strings.NewReader(body))
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		return nil, nil, fmt.Errorf("journal.csv: %s", err.Error())
	}
	if len(records) == 0 {
		return nil, nil, fmt.Errorf("journal.csv пуст")
	}
	head := records[0]
	if len(head) != 5 || head[0] != "id" || head[1] != "status" || head[2] != "group" || head[3] != "reason" || head[4] != "error" {
		return nil, nil, fmt.Errorf("заголовок journal.csv должен быть id,status,group,reason,error")
	}
	rows := map[string]*journalRow{}
	var order []string
	for _, rec := range records[1:] {
		if len(rec) < 5 {
			return nil, nil, fmt.Errorf("в journal.csv не хватает столбцов")
		}
		id := strings.TrimSpace(rec[0])
		if id == "" {
			return nil, nil, fmt.Errorf("пустой id в journal.csv")
		}
		if _, ok := rows[id]; ok {
			return nil, nil, fmt.Errorf("повтор id %s в journal.csv", id)
		}
		rows[id] = &journalRow{status: rec[1], group: rec[2], reason: rec[3], err: rec[4]}
		order = append(order, id)
	}
	return rows, order, nil
}

func mergeJSON(rows map[string]*journalRow, body string) error {
	var entries []domain.JournalEntry
	dec := json.NewDecoder(strings.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&entries); err != nil {
		return fmt.Errorf("journal.json: %s", err.Error())
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		id := strings.TrimSpace(entry.ID)
		if id == "" {
			return fmt.Errorf("пустой id в journal.json")
		}
		if seen[id] {
			return fmt.Errorf("повтор id %s в journal.json", id)
		}
		seen[id] = true
		row, ok := rows[id]
		if !ok {
			return fmt.Errorf("id %s есть в journal.json и нет в journal.csv", id)
		}
		row.code = string(entry.ReasonCode)
		row.flags = append([]string(nil), entry.Flags...)
		row.seen = true
		if row.status != string(entry.Status) {
			return fmt.Errorf("статус %s в csv и json различается", id)
		}
	}
	for id, row := range rows {
		if !row.seen {
			return fmt.Errorf("id %s есть в journal.csv и нет в journal.json", id)
		}
	}
	return nil
}

func hasFlag(flags []string, want string) bool {
	for _, flag := range flags {
		if flag == want {
			return true
		}
	}
	return false
}
