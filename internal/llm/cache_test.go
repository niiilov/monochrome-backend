package llm

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

type countingClient struct {
	n atomic.Int32
}

func (c *countingClient) Complete(ctx context.Context, r Request) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	c.n.Add(1)
	return Response{Text: "cached-answer", Model: "m", Usage: Usage{InputTokens: 1, OutputTokens: 1}}, nil
}

func TestCacheHit(t *testing.T) {
	dir := t.TempDir()
	inner := &countingClient{}
	c := WithCache(inner, dir, "m")
	req := Request{Task: "classify_topic", System: "sys", User: "user", Temperature: 0}
	if _, err := c.Complete(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Complete(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if inner.n.Load() != 1 {
		t.Fatalf("upstream calls %d", inner.n.Load())
	}
	other := req
	other.User = "other"
	if _, err := c.Complete(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	if inner.n.Load() != 2 {
		t.Fatalf("upstream calls %d", inner.n.Load())
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(matches) != 2 {
		t.Fatalf("cache files %d", len(matches))
	}
}

func TestCacheDisabled(t *testing.T) {
	inner := &countingClient{}
	c := WithCache(inner, "", "m")
	for i := 0; i < 2; i++ {
		if _, err := c.Complete(context.Background(), Request{User: "u"}); err != nil {
			t.Fatal(err)
		}
	}
	if inner.n.Load() != 2 {
		t.Fatalf("calls %d", inner.n.Load())
	}
}

func TestCacheParallel(t *testing.T) {
	dir := t.TempDir()
	inner := &countingClient{}
	c := WithCache(inner, dir, "m")
	req := Request{Task: "t", User: "same"}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.Complete(context.Background(), req)
		}()
	}
	wg.Wait()
	if _, err := c.Complete(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if inner.n.Load() != 1 {
		t.Fatalf("calls %d, want 1", inner.n.Load())
	}
	before := inner.n.Load()
	if _, err := c.Complete(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if inner.n.Load() != before {
		t.Fatal("warm cache missed")
	}
}
