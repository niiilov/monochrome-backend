package llm

import (
	"errors"
	"strings"

	"cyberdigest/internal/config"
)

// New builds the production client from cfg.
// Required: API key, base URL, and model. They come from the environment
// (LLM_API_KEY, LLM_BASE_URL, LLM_MODEL) via config.Load.
func New(cfg config.LLM) (Client, *Meter, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, nil, errors.New("llm: не задан LLM_API_KEY")
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, nil, errors.New("llm: не задан базовый URL (LLM_BASE_URL)")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, nil, errors.New("llm: не задана модель (LLM_MODEL)")
	}
	var c Client = NewOpenAI(cfg)
	c = WithLimiter(c, cfg.MaxConcurrency)
	c = WithRetry(c, RetryOptions{})
	meter := NewMeter(cfg)
	c = WithMeter(c, meter)
	c = WithCache(c, cfg.CacheDir, cfg.Model)
	return c, meter, nil
}
