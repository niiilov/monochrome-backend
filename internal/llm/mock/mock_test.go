package mock

import (
	"context"
	"errors"
	"sync"
	"testing"

	"cyberdigest/internal/llm"
)

func TestQueueOrderAndStickyTail(t *testing.T) {
	m := New()
	m.Queue("extract_events", "article", "one", "two")
	ctx := context.Background()
	for i, want := range []string{"one", "two", "two"} {
		resp, err := m.Complete(ctx, llm.Request{Task: "extract_events", User: "article body"})
		if err != nil {
			t.Fatal(err)
		}
		if resp.Text != want {
			t.Fatalf("call %d: %q", i, resp.Text)
		}
	}
	if m.Calls() != 3 || m.CallsFor("extract_events") != 3 {
		t.Fatalf("calls %d task %d", m.Calls(), m.CallsFor("extract_events"))
	}
}

func TestMostSpecificRuleWins(t *testing.T) {
	m := New()
	m.When("", "", "default")
	m.When("classify_topic", "", "by-task")
	m.When("classify_topic", "ransomware", "specific")
	resp, err := m.Complete(context.Background(), llm.Request{Task: "classify_topic", User: "ransomware news"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "specific" {
		t.Fatalf("%q", resp.Text)
	}
	resp, err = m.Complete(context.Background(), llm.Request{Task: "classify_topic", User: "other"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "by-task" {
		t.Fatalf("%q", resp.Text)
	}
}

func TestOnceStops(t *testing.T) {
	m := New()
	m.Once("t", "", "only")
	if _, err := m.Complete(context.Background(), llm.Request{Task: "t"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Complete(context.Background(), llm.Request{Task: "t"}); err == nil {
		t.Fatal("expected empty queue")
	}
}

func TestFailAndRequests(t *testing.T) {
	m := New()
	m.Fail("score_event", "bad", errors.New("временный сбой"))
	_, err := m.Complete(context.Background(), llm.Request{Task: "score_event", System: "sys", User: "bad input"})
	if err == nil {
		t.Fatal("expected error")
	}
	reqs := m.Requests()
	if len(reqs) != 1 || reqs[0].Task != "score_event" || reqs[0].User != "bad input" {
		t.Fatalf("%+v", reqs)
	}
}

func TestParallelCalls(t *testing.T) {
	m := New()
	m.When("t", "", "ok")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := m.Complete(context.Background(), llm.Request{Task: "t", User: "u"})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if m.Calls() != 20 {
		t.Fatalf("calls %d", m.Calls())
	}
}
