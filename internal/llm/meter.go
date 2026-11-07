package llm

import (
	"context"
	"sync"

	"cyberdigest/internal/config"
	"cyberdigest/internal/domain"
)

// Meter sums calls, tokens, and cost per task.
// Snapshot is safe to call while completions are in flight.
// Cost is nil and CostUnavailable is true when either price is missing.
type Meter struct {
	mu       sync.Mutex
	priceIn  *float64
	priceOut *float64
	stats    domain.LLMStats
}

// NewMeter copies prices from cfg so later edits of the config do not race.
func NewMeter(cfg config.LLM) *Meter {
	provider := cfg.Provider
	if provider == "" {
		provider = "openai"
	}
	m := &Meter{
		stats: domain.LLMStats{
			Provider:        provider,
			Model:           cfg.Model,
			Temperature:     cfg.Temperature,
			ByTask:          map[string]domain.LLMTaskStats{},
			CostUnavailable: true,
		},
	}
	if cfg.PricePer1MInput != nil {
		v := *cfg.PricePer1MInput
		m.priceIn = &v
	}
	if cfg.PricePer1MOutput != nil {
		v := *cfg.PricePer1MOutput
		m.priceOut = &v
	}
	m.recomputeLocked()
	return m
}

// Add records one successful call. An empty task name is stored as "unspecified".
func (m *Meter) Add(task, model string, usage Usage) {
	if m == nil {
		return
	}
	if task == "" {
		task = "unspecified"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	slot := m.stats.ByTask[task]
	slot.Calls++
	slot.InputTokens += usage.InputTokens
	slot.OutputTokens += usage.OutputTokens
	m.stats.ByTask[task] = slot
	m.stats.CallsTotal++
	m.stats.InputTokens += usage.InputTokens
	m.stats.OutputTokens += usage.OutputTokens
	if m.stats.Model == "" && model != "" {
		m.stats.Model = model
	}
	m.recomputeLocked()
}

// Snapshot returns a deep copy of the counters.
func (m *Meter) Snapshot() domain.LLMStats {
	if m == nil {
		return domain.LLMStats{ByTask: map[string]domain.LLMTaskStats{}, CostUnavailable: true}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.stats
	out.ByTask = make(map[string]domain.LLMTaskStats, len(m.stats.ByTask))
	for k, v := range m.stats.ByTask {
		out.ByTask[k] = v
	}
	if m.stats.Cost != nil {
		cost := *m.stats.Cost
		out.Cost = &cost
	}
	return out
}

func (m *Meter) recomputeLocked() {
	if m.priceIn == nil || m.priceOut == nil {
		m.stats.Cost = nil
		m.stats.CostUnavailable = true
		return
	}
	cost := float64(m.stats.InputTokens)/1_000_000*(*m.priceIn) + float64(m.stats.OutputTokens)/1_000_000*(*m.priceOut)
	m.stats.Cost = &cost
	m.stats.CostUnavailable = false
}

type metered struct {
	next  Client
	meter *Meter
}

// WithMeter records each successful Complete. Failures are not counted,
// because the server did not return token usage.
func WithMeter(next Client, m *Meter) Client {
	if m == nil {
		return next
	}
	return &metered{next: next, meter: m}
}

func (w *metered) Complete(ctx context.Context, r Request) (Response, error) {
	resp, err := w.next.Complete(ctx, r)
	if err != nil {
		return resp, err
	}
	w.meter.Add(r.Task, resp.Model, resp.Usage)
	return resp, nil
}
