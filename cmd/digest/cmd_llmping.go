package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"cyberdigest/internal/config"
	"cyberdigest/internal/llm"
)

func init() {
	Register("llm-ping", "проверить доступ к модели одним запросом", runLLMPing)
}

func runLLMPing(ctx context.Context, args []string) int {
	configPath := "config.yaml"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-h", "--help":
			fmt.Println("Использование: digest llm-ping [-config config.yaml]")
			return 0
		case "-config", "--config":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "llm-ping: укажите путь после -config")
				return 2
			}
			i++
			configPath = args[i]
		default:
			fmt.Fprintf(os.Stderr, "llm-ping: неизвестный аргумент %s\n", args[i])
			return 2
		}
	}
	loadDotEnv(".env")
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "llm-ping: %s\n", err)
		return 2
	}
	if gaps := settingsGaps(cfg); len(gaps) > 0 {
		fmt.Fprintln(os.Stderr, "не хватает настроек модели:")
		for _, gap := range gaps {
			fmt.Fprintf(os.Stderr, "- %s\n", gap)
		}
		return 2
	}
	client, _, err := llm.New(cfg.LLM)
	if err != nil {
		fmt.Fprintf(os.Stderr, "llm-ping: %s\n", redactKey(err.Error(), cfg.LLM.APIKey))
		return 2
	}
	start := time.Now()
	resp, err := client.Complete(ctx, llm.Request{
		Task:        "llm_ping",
		System:      "Ты отвечаешь одним словом.",
		User:        "Ответь одним словом: пинг",
		Temperature: cfg.LLM.Temperature,
		MaxTokens:   256,
	})
	elapsed := time.Since(start)
	if err != nil {
		fmt.Fprintf(os.Stderr, "запрос к модели не выполнен: %s\n", redactKey(err.Error(), cfg.LLM.APIKey))
		return 1
	}
	model := resp.Model
	if model == "" {
		model = cfg.LLM.Model
	}
	fmt.Printf("модель: %s\n", model)
	fmt.Printf("входные токены: %d\n", resp.Usage.InputTokens)
	fmt.Printf("выходные токены: %d\n", resp.Usage.OutputTokens)
	fmt.Printf("время: %s\n", elapsed.Round(time.Millisecond))
	return 0
}

func settingsGaps(cfg config.Config) []string {
	var gaps []string
	if strings.TrimSpace(cfg.LLM.APIKey) == "" {
		gaps = append(gaps, "LLM_API_KEY")
	}
	if strings.TrimSpace(cfg.LLM.BaseURL) == "" {
		gaps = append(gaps, "LLM_BASE_URL или llm.base_url")
	}
	if strings.TrimSpace(cfg.LLM.Model) == "" {
		gaps = append(gaps, "LLM_MODEL или llm.model")
	}
	return gaps
}

func redactKey(msg, key string) string {
	if strings.TrimSpace(key) == "" {
		return msg
	}
	return strings.ReplaceAll(msg, key, "[redacted]")
}

// loadDotEnv sets variables from path when they are not already in the environment.
// Values are never printed.
func loadDotEnv(path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		_ = os.Setenv(key, val)
	}
}
