package llm

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type inflightClient struct {
	cur atomic.Int32
	max atomic.Int32
}

func (c *inflightClient) Complete(ctx context.Context, r Request) (Response, error) {
	n := c.cur.Add(1)
	for {
		old := c.max.Load()
		if n <= old || c.max.CompareAndSwap(old, n) {
			break
		}
	}
	time.Sleep(40 * time.Millisecond)
	c.cur.Add(-1)
	return Response{Text: "ok", Model: "m"}, nil
}

func TestLimiterCapsInflight(t *testing.T) {
	inner := &inflightClient{}
	c := WithLimiter(inner, 2)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Complete(context.Background(), Request{User: "u"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if inner.max.Load() > 2 {
		t.Fatalf("max inflight %d", inner.max.Load())
	}
	if inner.max.Load() < 1 {
		t.Fatal("no calls")
	}
}
