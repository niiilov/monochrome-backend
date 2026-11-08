package fake

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mono/internal/domain"
	"mono/internal/textutil"
)

func TestRunCopiesFixtureAndReportsStages(t *testing.T) {
	dir := t.TempDir()
	var stages []string
	state, report, err := New(fixtureDir(t)).Run(context.Background(), domain.RunParams{
		DatasetPath: "testdata/synthetic/articles.json",
		ControlTime: time.Date(2026, 11, 7, 12, 0, 0, 0, time.FixedZone("MSK", 3*60*60)),
		OutDir:      dir,
		ExtraPaths:  []string{"extra.json"},
	}, func(stage string, done, total int) {
		if done == total {
			stages = append(stages, stage)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(stages, ",") != strings.Join(Stages, ",") {
		t.Fatalf("stages %v", stages)
	}
	if state == nil || state.Journal["fake-1"] == nil || state.Journal["fake-1"].Status != domain.StatusIncluded {
		t.Fatalf("journal %+v", state)
	}
	if len(state.Digest.Items) != 1 || state.Digest.Items[0].Title == "" {
		t.Fatalf("digest %+v", state.Digest)
	}
	if len(state.Digest.Items[0].Sources) == 0 || state.Digest.Items[0].Sources[0].URL == "" {
		t.Fatalf("sources %+v", state.Digest.Items[0].Sources)
	}
	if strings.Join(state.Params.ExtraPaths, ",") != "extra.json" {
		t.Fatalf("extras %+v", state.Params.ExtraPaths)
	}
	if report.Counts.Events != 1 || report.ArtifactHashes["mono.md"] == "" {
		t.Fatalf("report %+v", report)
	}
	for _, name := range artifactNames {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if textutil.HasLongDash(string(raw)) {
			t.Fatalf("%s contains a long dash", name)
		}
	}
	digest, err := os.ReadFile(filepath.Join(dir, "mono.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(digest), "Волга-Линк") || !strings.Contains(string(digest), "https://example.com/news/volga-link-2026") {
		t.Fatalf("digest %s", digest)
	}
}

func TestRunHonorsCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := New(fixtureDir(t)).Run(ctx, domain.RunParams{OutDir: t.TempDir()}, nil)
	if err == nil {
		t.Fatal("expected cancel")
	}
}

func TestRunRequiresOutputDir(t *testing.T) {
	_, _, err := New(fixtureDir(t)).Run(context.Background(), domain.RunParams{}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func fixtureDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join("..", "..", "..", "testdata", "fake_run")
	if _, err := os.Stat(filepath.Join(dir, "mono.md")); err != nil {
		t.Fatal(err)
	}
	return dir
}
