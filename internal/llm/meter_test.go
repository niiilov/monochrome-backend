package llm

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"

	"mono/internal/config"
	"mono/internal/domain"
)

type fixedClient struct {
	resp Response
	err  error
}

func (f fixedClient) Complete(ctx context.Context, r Request) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	return f.resp, f.err
}

func TestMeterParallel(t *testing.T) {
	m := NewMeter(config.LLM{Provider: "openai", Model: "m", Temperature: 0})
	c := WithMeter(fixedClient{resp: Response{
		Text:  "ok",
		Model: "m",
		Usage: Usage{InputTokens: 3, OutputTokens: 4},
	}}, m)
	const n = 40
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Complete(context.Background(), Request{Task: "classify_topic"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	snap := m.Snapshot()
	if snap.CallsTotal != n || snap.InputTokens != n*3 || snap.OutputTokens != n*4 {
		t.Fatalf("%+v", snap)
	}
	task := snap.ByTask["classify_topic"]
	if task.Calls != n || task.InputTokens != n*3 || task.OutputTokens != n*4 {
		t.Fatalf("task %+v", task)
	}
	if snap.Cost != nil || !snap.CostUnavailable {
		t.Fatalf("cost %+v unavailable %v", snap.Cost, snap.CostUnavailable)
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"cost":null`) {
		t.Fatalf("json %s", raw)
	}
}

func TestMeterCost(t *testing.T) {
	in := 1.5
	out := 2.5
	m := NewMeter(config.LLM{
		Provider:         "openai",
		Model:            "m",
		PricePer1MInput:  &in,
		PricePer1MOutput: &out,
	})
	m.Add("score_event", "m", Usage{InputTokens: 1000, OutputTokens: 2000})
	snap := m.Snapshot()
	if snap.CostUnavailable || snap.Cost == nil {
		t.Fatalf("%+v", snap)
	}
	const want = 0.0065
	if math.Abs(*snap.Cost-want) > 1e-9 {
		t.Fatalf("cost %v", *snap.Cost)
	}
	snap.ByTask["score_event"] = domain.LLMTaskStats{}
	if m.Snapshot().ByTask["score_event"].Calls != 1 {
		t.Fatal("snapshot shares the task map")
	}
}

func TestMeterIgnoresFailure(t *testing.T) {
	m := NewMeter(config.LLM{Model: "m"})
	c := WithMeter(fixedClient{err: errors.New("boom")}, m)
	_, _ = c.Complete(context.Background(), Request{Task: "t"})
	if m.Snapshot().CallsTotal != 0 {
		t.Fatal("failure was counted")
	}
}
