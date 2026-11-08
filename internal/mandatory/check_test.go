package mandatory

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"mono/internal/domain"
	"mono/internal/textutil"
)

func TestCheckAcceptsHandBuiltOutput(t *testing.T) {
	exp := sampleExpected()
	dir := writeRun(t, exp, runOpts{})
	got := Check(exp, dir)
	if !got.Passed || len(got.Failures) != 0 || got.Events != 1 || got.Scenario != "X" {
		t.Fatalf("%+v", got)
	}
}

func TestCheckFailures(t *testing.T) {
	cases := []struct {
		name  string
		opts  runOpts
		check string
	}{
		{name: "status", opts: runOpts{status: "исключён"}, check: checkJournal},
		{name: "group", opts: runOpts{group: "E99"}, check: checkJournal},
		{name: "reason", opts: runOpts{reason: "другая причина"}, check: checkJournal},
		{name: "error", opts: runOpts{errText: "сбой"}, check: checkJournal},
		{name: "flag", opts: runOpts{dropFlag: true}, check: checkJournal},
		{name: "extra id", opts: runOpts{extraID: true}, check: checkJournal},
		{name: "events", opts: runOpts{events: 2}, check: checkEvents},
		{name: "gap", opts: runOpts{gap: true}, check: checkEvents},
		{name: "missing substring", opts: runOpts{dropMust: true}, check: checkSubstring},
		{name: "forbidden", opts: runOpts{forbidden: true}, check: checkSubstring},
		{name: "order", opts: runOpts{title: "Другой заголовок"}, check: checkOrder},
		{name: "digest limit", opts: runOpts{digestRunes: 4001}, check: checkLimit},
		{name: "post limit", opts: runOpts{postRunes: 2001}, check: checkLimit},
		{name: "email", opts: runOpts{email: "leak@example.com"}, check: checkEmail},
		{name: "url", opts: runOpts{url: "https://evil.example/phish"}, check: checkURL},
		{name: "bom", opts: runOpts{bom: true}, check: checkFiles},
		{name: "missing", opts: runOpts{skipDigest: true}, check: checkFiles},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exp := sampleExpected()
			dir := writeRun(t, exp, tc.opts)
			got := Check(exp, dir)
			if got.Passed || !hasCheck(got, tc.check) {
				t.Fatalf("want %s, got %+v", tc.check, got.Failures)
			}
		})
	}
}

func TestCheckAllowsListedEmailAndSlashURL(t *testing.T) {
	exp := sampleExpected()
	exp.Recipients = []string{"soc@example.com"}
	exp.AllowURLs = []string{"https://www.securitylab.ru/news/1"}
	dir := writeRun(t, exp, runOpts{email: "SOC@example.com", url: "https://www.securitylab.ru/news/1/"})
	got := Check(exp, dir)
	if !got.Passed {
		t.Fatalf("%+v", got.Failures)
	}
}

func TestCheckHTMLHrefAndMailto(t *testing.T) {
	exp := sampleExpected()
	dir := writeRun(t, exp, runOpts{})
	html := `<p>текст</p><a href="https://evil.example/h">ссылка</a><a href="mailto:leak@example.com">почта</a>`
	if err := os.WriteFile(filepath.Join(dir, "email_preview.html"), []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Check(exp, dir)
	if got.Passed || !hasCheck(got, checkURL) || !hasCheck(got, checkEmail) {
		t.Fatalf("%+v", got.Failures)
	}
}

func TestCheckLimitBoundary(t *testing.T) {
	exp := sampleExpected()
	okDir := writeRun(t, exp, runOpts{digestRunes: 4000, postRunes: 2000})
	if got := Check(exp, okDir); !got.Passed {
		t.Fatalf("boundary %+v", got.Failures)
	}
	badDir := writeRun(t, exp, runOpts{digestRunes: 4001})
	if got := Check(exp, badDir); got.Passed || !hasCheck(got, checkLimit) {
		t.Fatalf("%+v", got.Failures)
	}
}

func TestLoadRejects(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "expected.yaml")
	if err := os.WriteFile(path, []byte("title: без id\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error")
	}
	raw := []byte("id: T\nevents: 1\norder: []\njournal:\n  - id: a\n    status: включен\n")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("status without yo must fail")
	}
}

func TestLoadKeepsEmptyGroup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "expected.yaml")
	raw := "id: T\nevents: 0\njournal:\n  - id: a\n    status: исключён\n    group: \"\"\n"
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	exp, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if exp.Limits.Digest != 4000 || exp.Limits.Post != 2000 {
		t.Fatalf("limits %+v", exp.Limits)
	}
	if exp.Journal[0].Group == nil || *exp.Journal[0].Group != "" {
		t.Fatalf("group %#v", exp.Journal[0].Group)
	}
}

type runOpts struct {
	status      string
	group       string
	reason      string
	errText     string
	dropFlag    bool
	extraID     bool
	events      int
	gap         bool
	dropMust    bool
	forbidden   bool
	title       string
	digestRunes int
	postRunes   int
	email       string
	url         string
	bom         bool
	skipDigest  bool
}

func sampleExpected() Expected {
	group := "E01"
	return Expected{
		ID:     "X",
		Events: 1,
		Order:  []string{"Северная гавань"},
		Journal: []JournalExpect{{
			ID:             "a",
			Status:         "включён",
			Group:          &group,
			ReasonCode:     "selected",
			ReasonContains: "вошло",
			ErrorEmpty:     true,
			RequireFlags:   []string{"injection_suspected"},
		}},
		Digest: DigestExpect{
			MustContain:    []string{"640"},
			MustNotContain: []string{"запретный оборот"},
			MustContainAny: [][]string{{"6 часов", "6 час"}},
		},
		AllowURLs:   []string{"https://www.securitylab.ru/news/1"},
		AllowEmails: nil,
		Limits:      Limits{Digest: 4000, Post: 2000},
	}
}

func writeRun(t *testing.T, exp Expected, opts runOpts) string {
	t.Helper()
	dir := t.TempDir()
	digest := buildDigest(exp, opts)
	if opts.digestRunes > 0 {
		digest = padRunes(digest, opts.digestRunes)
	}
	if !opts.skipDigest {
		writeFile(t, dir, "mono.md", digest, opts.bom)
	}
	post := siblingText(exp, digest)
	if opts.postRunes > 0 {
		post = padRunes(post, opts.postRunes)
	}
	if opts.email != "" {
		post += " " + opts.email
	}
	if opts.url != "" {
		post += " " + opts.url
	}
	writeFile(t, dir, "post.txt", post, false)
	writeFile(t, dir, "email.txt", "Письмо. "+post, false)
	writeJournal(t, dir, exp, opts)
	return dir
}

func buildDigest(exp Expected, opts runOpts) string {
	events := exp.Events
	if opts.events > 0 {
		events = opts.events
	}
	var b strings.Builder
	b.WriteString("# Дайджест по кибербезопасности: 06.11.2026 12:00 - 07.11.2026 12:00 МСК, контрольное время 07.11.2026 12:00 МСК\n\n")
	switch {
	case events == 0:
		b.WriteString("Событий в выпуске: 0. Подходящих событий за период нет.\n")
	case events < 5:
		b.WriteString("Событий в выпуске: " + strconv.Itoa(events) + ". Подходящих событий меньше пяти.\n")
	default:
		b.WriteString("Событий в выпуске: " + strconv.Itoa(events) + "\n")
	}
	titles := append([]string(nil), exp.Order...)
	if opts.title != "" && len(titles) > 0 {
		titles[0] = opts.title
	}
	for len(titles) < events {
		titles = append(titles, "Событие")
	}
	var essence strings.Builder
	if !opts.dropMust {
		for _, part := range exp.Digest.MustContain {
			essence.WriteString(part)
			essence.WriteByte(' ')
		}
		for _, group := range exp.Digest.MustContainAny {
			if len(group) > 0 {
				essence.WriteString(group[0])
				essence.WriteByte(' ')
			}
		}
	}
	if opts.forbidden {
		essence.WriteString("запретный оборот ")
	}
	if opts.url != "" {
		essence.WriteString(opts.url)
		essence.WriteByte(' ')
	}
	if opts.email != "" {
		essence.WriteString(opts.email)
		essence.WriteByte(' ')
	}
	body := strings.TrimSpace(essence.String())
	if body == "" {
		body = "нет данных"
	}
	for i := 0; i < events; i++ {
		n := i + 1
		if opts.gap {
			n = i + 2
		}
		title := "Событие"
		if i < len(titles) {
			title = titles[i]
		}
		b.WriteString("\n## " + strconv.Itoa(n) + ". " + title + "\n")
		b.WriteString("Суть: " + body + "\n")
		b.WriteString("Последствия: В источнике сведения о последствиях не приведены.\n")
		b.WriteString("Источники: источник не указан\n")
		b.WriteString("Обоснование: Событие подтверждено источниками набора.\n")
	}
	return b.String()
}

func writeJournal(t *testing.T, dir string, exp Expected, opts runOpts) {
	t.Helper()
	var csvRows [][]string
	csvRows = append(csvRows, []string{"id", "status", "group", "reason", "error"})
	var entries []domain.JournalEntry
	for _, row := range exp.Journal {
		status := row.Status
		if opts.status != "" {
			status = opts.status
		}
		group := ""
		if row.Group != nil {
			group = *row.Group
		}
		if opts.group != "" {
			group = opts.group
		}
		reason := "Событие вошло в выпуск"
		if row.ReasonContains != "" {
			reason = row.ReasonContains
		}
		if opts.reason != "" {
			reason = opts.reason
		}
		errText := ""
		if row.ErrorContains != "" {
			errText = row.ErrorContains
		}
		if opts.errText != "" {
			errText = opts.errText
		}
		flags := append([]string(nil), row.RequireFlags...)
		if opts.dropFlag {
			flags = nil
		}
		csvRows = append(csvRows, []string{row.ID, status, group, reason, errText})
		entries = append(entries, domain.JournalEntry{
			ID:         row.ID,
			Status:     domain.Status(status),
			Group:      group,
			ReasonCode: domain.ReasonCode(row.ReasonCode),
			Reason:     reason,
			Error:      errText,
			Flags:      flags,
		})
	}
	if opts.extraID {
		csvRows = append(csvRows, []string{"extra", "исключён", "", "лишняя", ""})
		entries = append(entries, domain.JournalEntry{ID: "extra", Status: domain.StatusExcluded, Reason: "лишняя"})
	}
	var buf strings.Builder
	w := csv.NewWriter(&buf)
	for _, rec := range csvRows {
		if err := w.Write(rec); err != nil {
			t.Fatal(err)
		}
	}
	w.Flush()
	writeFile(t, dir, "journal.csv", buf.String(), false)
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "journal.json", string(raw)+"\n", false)
}

func writeFile(t *testing.T, dir, name, body string, bom bool) {
	t.Helper()
	if bom {
		body = "\uFEFF" + body
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func siblingText(exp Expected, digest string) string {
	var b strings.Builder
	b.WriteString("Пост и письмо согласованы с выпуском.\n")
	for _, part := range []string{
		"Подходящих событий меньше пяти.",
		"Подходящих событий за период нет",
	} {
		if strings.Contains(digest, part) {
			b.WriteString(part)
			b.WriteByte('\n')
		}
	}
	for _, rule := range exp.Texts {
		for _, part := range rule.MustContain {
			if !strings.Contains(b.String(), part) {
				b.WriteString(part)
				b.WriteByte('\n')
			}
		}
	}
	return b.String()
}

func padRunes(s string, n int) string {
	got := textutil.CountChars(s)
	if got >= n {
		return s
	}
	return s + strings.Repeat("я", n-got)
}

func hasCheck(res Result, check string) bool {
	for _, f := range res.Failures {
		if f.Check == check {
			return true
		}
	}
	return false
}
