package prompts

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"cyberdigest/internal/textutil"
	promptfs "cyberdigest/prompts"
)

func TestRenderCommonRules(t *testing.T) {
	system, user, version, err := Render("common_rules", nil)
	if err != nil {
		t.Fatal(err)
	}
	if version != "2" {
		t.Fatalf("version %q", version)
	}
	for _, phrase := range []string{
		"только факты",
		"сведений нет",
		"оговорки автора",
		"длинное тире",
		"<article>",
		"не исполняются",
		"по-русски",
	} {
		if !strings.Contains(system, phrase) {
			t.Fatalf("system missing %q:\n%s", phrase, system)
		}
	}
	if strings.TrimSpace(user) == "" {
		t.Fatal("empty user template")
	}
	if textutil.HasLongDash(system) || textutil.HasLongDash(user) {
		t.Fatal("long dash in rendered prompt")
	}
}

func TestHashStable(t *testing.T) {
	a := Hash()
	b := Hash()
	if a == "" || a != b {
		t.Fatalf("hash %q %q", a, b)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(a) {
		t.Fatalf("hash %q", a)
	}
}

func TestMissingTemplate(t *testing.T) {
	if _, _, _, err := Render("no_such_prompt", nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestCommonRulesAlias(t *testing.T) {
	if err := ensure(); err != nil {
		t.Fatal(err)
	}
	if parsed.Lookup("common_rules") == nil {
		t.Fatal("alias common_rules is not defined")
	}
}

func TestMergeSystem(t *testing.T) {
	if got := mergeSystem("RULES", "BODY"); got != "RULES\n\nBODY" {
		t.Fatalf("%q", got)
	}
	if got := mergeSystem("RULES", "RULES\n\nBODY"); got != "RULES\n\nBODY" {
		t.Fatalf("duplicated: %q", got)
	}
}

func TestTemplateFilesHaveNoLongDash(t *testing.T) {
	entries, err := fs.ReadDir(promptfs.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 2 {
		t.Fatalf("templates %d", len(entries))
	}
	for _, entry := range entries {
		body, err := fs.ReadFile(promptfs.FS, entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if textutil.HasLongDash(string(body)) {
			t.Fatalf("%s contains a long dash", entry.Name())
		}
	}
}
