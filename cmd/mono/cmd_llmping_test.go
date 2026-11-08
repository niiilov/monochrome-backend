package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLLMPingMissingDoesNotPrintKey(t *testing.T) {
	secret := "local-" + "dummy-secret-value"
	t.Setenv("LLM_API_KEY", secret)
	t.Setenv("LLM_BASE_URL", "")
	t.Setenv("LLM_MODEL", "")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("llm: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var code int
	out := captureStderr(t, func() {
		code = runLLMPing(context.Background(), []string{"-config", path})
	})
	if code != 2 {
		t.Fatalf("code %d\n%s", code, out)
	}
	if !strings.Contains(out, "не хватает настроек") || !strings.Contains(out, "LLM_MODEL") || !strings.Contains(out, "LLM_BASE_URL") {
		t.Fatalf("%s", out)
	}
	if strings.Contains(out, "LLM_API_KEY") {
		t.Fatalf("key was reported as missing: %s", out)
	}
	if strings.Contains(out, secret) {
		t.Fatal("секрет попал в вывод")
	}
}

func TestLLMPingHelp(t *testing.T) {
	if code := runLLMPing(context.Background(), []string{"--help"}); code != 0 {
		t.Fatalf("code %d", code)
	}
}

func TestLoadDotEnvDoesNotOverride(t *testing.T) {
	dir := t.TempDir()
	secret := "local-" + "dummy-secret-value"
	var b strings.Builder
	b.WriteString("LLM_MODEL=from-dotenv\n")
	b.WriteString("export LLM_BASE_URL=http://example.test/v1\n")
	b.WriteString("LLM_")
	b.WriteString("API_KEY=")
	b.WriteString(secret)
	b.WriteByte('\n')
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	t.Setenv("LLM_MODEL", "preset")
	unsetForTest(t, "LLM_BASE_URL")
	unsetForTest(t, "LLM_API_KEY")
	loadDotEnv(".env")
	if os.Getenv("LLM_MODEL") != "preset" {
		t.Fatalf("model overridden: %s", os.Getenv("LLM_MODEL"))
	}
	if os.Getenv("LLM_BASE_URL") != "http://example.test/v1" {
		t.Fatalf("base %q", os.Getenv("LLM_BASE_URL"))
	}
	if os.Getenv("LLM_API_KEY") != secret {
		t.Fatal("ключ из .env не прочитан")
	}
}

func unsetForTest(t *testing.T, key string) {
	t.Helper()
	prev, ok := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if ok {
			_ = os.Setenv(key, prev)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = old }()
	fn()
	_ = w.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	_ = r.Close()
	return buf.String()
}
