package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultsAndEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	raw := []byte("llm:\n  model: from-file\nmail:\n  dry_run: true\n")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLM_MODEL", "from-env")
	t.Setenv("LLM_API_KEY", "test-key")
	t.Setenv("SMTP_PORT", "2525")
	t.Setenv("ALLOWED_TEST_RECIPIENTS", " a@example.com, b@example.com ")
	t.Setenv("MONO_ADDR", "127.0.0.1:9090")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Model != "from-env" {
		t.Fatalf("model: got %q", cfg.LLM.Model)
	}
	if cfg.LLM.APIKey != "test-key" {
		t.Fatal("ключ из окружения не подхватился")
	}
	if cfg.LLM.Provider != "openai" || cfg.LLM.Seed != 7 || cfg.Limits.DigestChars != 4000 {
		t.Fatalf("значения по умолчанию затёрлись: %+v", cfg)
	}
	if cfg.Mail.Port != 2525 || cfg.Server.Addr != "127.0.0.1:9090" {
		t.Fatalf("smtp/addr: %+v %+v", cfg.Mail, cfg.Server)
	}
	if len(cfg.Mail.Allowlist) != 2 || cfg.Mail.Allowlist[0] != "a@example.com" {
		t.Fatalf("allowlist: %#v", cfg.Mail.Allowlist)
	}
	if cfg.Ingest.FieldMap["published_at"] != "published_at" {
		t.Fatalf("field_map: %#v", cfg.Ingest.FieldMap)
	}
}

func TestValidateRejectsBadPolicy(t *testing.T) {
	cfg := Default()
	cfg.Period.NaiveTZPolicy = "utc"
	if err := cfg.Validate(); err == nil {
		t.Fatal("ожидалась ошибка политики пояса")
	}
}

func TestRepoConfig(t *testing.T) {
	for _, key := range []string{
		"LLM_API_KEY", "LLM_BASE_URL", "LLM_MODEL",
		"SMTP_HOST", "SMTP_PORT", "SMTP_USER", "SMTP_PASSWORD", "SMTP_FROM",
		"ALLOWED_TEST_RECIPIENTS", "MONO_ADDR",
	} {
		t.Setenv(key, "")
	}
	cfg, err := Load(filepath.Join("..", "..", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Rank.MaxEvents != 5 || cfg.Limits.PostChars != 2000 || cfg.Verify.CriticEnabled {
		t.Fatalf("config.yaml разошёлся с разделом 8: %+v", cfg)
	}
	if cfg.Live.SourcesFile != "sources.yaml" || cfg.Mail.DryRun != true {
		t.Fatalf("live/mail: %+v %+v", cfg.Live, cfg.Mail)
	}
}
