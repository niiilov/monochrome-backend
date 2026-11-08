// Package config loads config.yaml and environment overrides.
// Secrets are read only from the environment and are never written back.
package config

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the full application configuration. Stages must not add fields later.
type Config struct {
	LLM     LLM     `yaml:"llm"`
	Ingest  Ingest  `yaml:"ingest"`
	Period  Period  `yaml:"period"`
	Cluster Cluster `yaml:"cluster"`
	Rank    Rank    `yaml:"rank"`
	Limits  Limits  `yaml:"limits"`
	Verify  Verify  `yaml:"verify"`
	Mail    Mail    `yaml:"mail"`
	Critic  Critic  `yaml:"critic"`
	Live    Live    `yaml:"live"`
	Server  Server  `yaml:"server"`
}

// LLM is the OpenAI-compatible model client settings.
type LLM struct {
	Provider         string   `yaml:"provider"`
	BaseURL          string   `yaml:"base_url"`
	Model            string   `yaml:"model"`
	APIKey           string   `yaml:"-"`
	Temperature      float64  `yaml:"temperature"`
	Seed             int      `yaml:"seed"`
	TimeoutSec       int      `yaml:"timeout_sec"`
	MaxConcurrency   int      `yaml:"max_concurrency"`
	MaxInputChars    int      `yaml:"max_input_chars"`
	PricePer1MInput  *float64 `yaml:"price_per_1m_input"`
	PricePer1MOutput *float64 `yaml:"price_per_1m_output"`
	CacheDir         string   `yaml:"cache_dir"`
}

// Ingest describes how a dataset file is mapped into articles.
type Ingest struct {
	FieldMap       map[string]string `yaml:"field_map"`
	MissingMarkers []string          `yaml:"missing_markers"`
	MinTextChars   int               `yaml:"min_text_chars"`
}

// Period is the publication window policy.
type Period struct {
	Hours         int    `yaml:"hours"`
	NaiveTZPolicy string `yaml:"naive_tz_policy"`
}

// Cluster is the mention-grouping policy.
type Cluster struct {
	SimilarityThreshold float64 `yaml:"similarity_threshold"`
	MaxPairsPerArticle  int     `yaml:"max_pairs_per_article"`
}

// Weights are the rank formula coefficients. They must sum to 1.
type Weights struct {
	Impact    float64 `yaml:"impact"`
	Scale     float64 `yaml:"scale"`
	Urgency   float64 `yaml:"urgency"`
	Relevance float64 `yaml:"relevance"`
}

// Rank is the event selection policy.
type Rank struct {
	MaxEvents int     `yaml:"max_events"`
	Weights   Weights `yaml:"weights"`
}

// Limits are character caps. URLs are not counted by textutil.CountChars.
type Limits struct {
	DigestChars int `yaml:"digest_chars"`
	PostChars   int `yaml:"post_chars"`
}

// Verify is the deterministic check and critic switch.
type Verify struct {
	MaxRetries    int  `yaml:"max_retries"`
	CriticEnabled bool `yaml:"critic_enabled"`
}

// Mail is SMTP delivery. Password comes only from SMTP_PASSWORD.
type Mail struct {
	DryRun    bool     `yaml:"dry_run"`
	Host      string   `yaml:"host"`
	Port      int      `yaml:"port"`
	User      string   `yaml:"user"`
	Password  string   `yaml:"-"`
	From      string   `yaml:"from"`
	Allowlist []string `yaml:"allowlist"`
}

// Critic is the optional second model used by the star feature.
// The on/off switch for the pipeline is Verify.CriticEnabled.
// Model empty means "use llm.model".
type Critic struct {
	Model string `yaml:"model"`
}

// Live is the optional live-collection client. Disabled by default.
type Live struct {
	Enabled       bool   `yaml:"enabled"`
	SourcesFile   string `yaml:"sources_file"`
	TimeoutSec    int    `yaml:"timeout_sec"`
	MaxRetries    int    `yaml:"max_retries"`
	UserAgent     string `yaml:"user_agent"`
	CacheDir      string `yaml:"cache_dir"`
	RespectRobots bool   `yaml:"respect_robots"`
}

// Server is the HTTP listen address. MONO_ADDR overrides Addr.
type Server struct {
	Addr string `yaml:"addr"`
}

// Default returns the configuration used when a file omits a field.
func Default() Config {
	return Config{
		LLM: LLM{
			Provider:       "openai",
			Temperature:    0,
			Seed:           7,
			TimeoutSec:     90,
			MaxConcurrency: 4,
			MaxInputChars:  24000,
		},
		Ingest: Ingest{
			FieldMap: map[string]string{
				"id":           "id",
				"title":        "title",
				"text":         "text",
				"source":       "source",
				"url":          "url",
				"published_at": "published_at",
			},
			MissingMarkers: []string{"", "null", "N/A", "не заполнено"},
			MinTextChars:   200,
		},
		Period: Period{Hours: 24, NaiveTZPolicy: "assume_msk"},
		Cluster: Cluster{
			SimilarityThreshold: 0.35,
			MaxPairsPerArticle:  5,
		},
		Rank: Rank{
			MaxEvents: 5,
			Weights:   Weights{Impact: 0.30, Scale: 0.25, Urgency: 0.25, Relevance: 0.20},
		},
		Limits: Limits{DigestChars: 4000, PostChars: 2000},
		Verify: Verify{MaxRetries: 2, CriticEnabled: false},
		Mail:   Mail{DryRun: true, Port: 587},
		Live: Live{
			Enabled:       false,
			SourcesFile:   "sources.yaml",
			TimeoutSec:    20,
			MaxRetries:    2,
			UserAgent:     "mono/0.1",
			RespectRobots: true,
		},
		Server: Server{Addr: ":8080"},
	}
}

// Load reads YAML from path, applies environment overrides, and validates the result.
func Load(path string) (Config, error) {
	cfg := Default()
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("чтение конфигурации: %w", err)
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("разбор конфигурации: %w", err)
	}
	if err := cfg.applyEnv(); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) applyEnv() error {
	set := func(key string, dst *string) {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			*dst = v
		}
	}
	set("LLM_API_KEY", &c.LLM.APIKey)
	set("LLM_BASE_URL", &c.LLM.BaseURL)
	set("LLM_MODEL", &c.LLM.Model)
	set("SMTP_HOST", &c.Mail.Host)
	set("SMTP_USER", &c.Mail.User)
	set("SMTP_PASSWORD", &c.Mail.Password)
	set("SMTP_FROM", &c.Mail.From)
	set("MONO_ADDR", &c.Server.Addr)
	if v := strings.TrimSpace(os.Getenv("SMTP_PORT")); v != "" {
		port, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("SMTP_PORT: %w", err)
		}
		c.Mail.Port = port
	}
	if v := strings.TrimSpace(os.Getenv("ALLOWED_TEST_RECIPIENTS")); v != "" {
		parts := strings.Split(v, ",")
		list := make([]string, 0, len(parts))
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part != "" {
				list = append(list, part)
			}
		}
		c.Mail.Allowlist = list
	}
	return nil
}

// Validate checks structural limits. A missing API key is not an error.
func (c Config) Validate() error {
	if strings.TrimSpace(c.LLM.Provider) == "" {
		return errors.New("llm.provider: укажите провайдера")
	}
	if c.LLM.Temperature < 0 || c.LLM.Temperature > 2 {
		return errors.New("llm.temperature: ожидается значение от 0 до 2")
	}
	if c.LLM.TimeoutSec <= 0 {
		return errors.New("llm.timeout_sec: ожидается положительное число")
	}
	if c.LLM.MaxConcurrency <= 0 {
		return errors.New("llm.max_concurrency: ожидается положительное число")
	}
	if c.LLM.MaxInputChars <= 0 {
		return errors.New("llm.max_input_chars: ожидается положительное число")
	}
	if c.LLM.PricePer1MInput != nil && *c.LLM.PricePer1MInput < 0 {
		return errors.New("llm.price_per_1m_input: цена не может быть отрицательной")
	}
	if c.LLM.PricePer1MOutput != nil && *c.LLM.PricePer1MOutput < 0 {
		return errors.New("llm.price_per_1m_output: цена не может быть отрицательной")
	}
	for _, key := range []string{"id", "title", "text", "source", "url", "published_at"} {
		if strings.TrimSpace(c.Ingest.FieldMap[key]) == "" {
			return fmt.Errorf("ingest.field_map: нет поля %s", key)
		}
	}
	if c.Ingest.MinTextChars < 0 {
		return errors.New("ingest.min_text_chars: не может быть отрицательным")
	}
	if c.Period.Hours <= 0 {
		return errors.New("period.hours: ожидается положительное число")
	}
	switch c.Period.NaiveTZPolicy {
	case "assume_msk", "reject":
	default:
		return errors.New("period.naive_tz_policy: допустимо assume_msk или reject")
	}
	if c.Cluster.SimilarityThreshold < 0 || c.Cluster.SimilarityThreshold > 1 {
		return errors.New("cluster.similarity_threshold: ожидается значение от 0 до 1")
	}
	if c.Cluster.MaxPairsPerArticle <= 0 {
		return errors.New("cluster.max_pairs_per_article: ожидается положительное число")
	}
	if c.Rank.MaxEvents <= 0 {
		return errors.New("rank.max_events: ожидается положительное число")
	}
	sum := c.Rank.Weights.Impact + c.Rank.Weights.Scale + c.Rank.Weights.Urgency + c.Rank.Weights.Relevance
	if math.Abs(sum-1) > 0.02 {
		return fmt.Errorf("rank.weights: сумма должна быть 1, сейчас %.3f", sum)
	}
	for _, w := range []float64{c.Rank.Weights.Impact, c.Rank.Weights.Scale, c.Rank.Weights.Urgency, c.Rank.Weights.Relevance} {
		if w < 0 {
			return errors.New("rank.weights: вес не может быть отрицательным")
		}
	}
	if c.Limits.DigestChars <= 0 || c.Limits.PostChars <= 0 {
		return errors.New("limits: лимиты знаков должны быть положительными")
	}
	if c.Verify.MaxRetries < 0 {
		return errors.New("verify.max_retries: не может быть отрицательным")
	}
	if c.Mail.Port != 0 && (c.Mail.Port < 1 || c.Mail.Port > 65535) {
		return errors.New("mail.port: ожидается порт от 1 до 65535")
	}
	if c.Live.TimeoutSec <= 0 {
		return errors.New("live.timeout_sec: ожидается положительное число")
	}
	if c.Live.MaxRetries < 0 {
		return errors.New("live.max_retries: не может быть отрицательным")
	}
	if strings.TrimSpace(c.Server.Addr) == "" {
		return errors.New("server.addr: укажите адрес")
	}
	return nil
}
