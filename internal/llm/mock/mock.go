// Package mock is a deterministic llm.Client for tests.
//
// Register the most specific rule last: the last matching rule wins.
// An empty task matches every task. An empty substring matches every prompt.
// Contains is a case-sensitive substring of system + newline + user.
//
// Each rule has a queue. When and Reply repeat the last answer forever.
// Queue repeats the last of several answers. Once stops after the queue
// and further matches return an error.
//
//	m := mock.New()
//	m.When("classify_topic", "ransomware", `{"on_topic":true}`)
//	m.Queue("extract_events", "article", `{"events":[]}`, `{"events":[{"id":"e1"}]}`)
//	m.Fail("score_event", "", errors.New("временный сбой"))
//	resp, err := m.Complete(ctx, llm.Request{Task: "classify_topic", User: "ransomware news"})
//	_ = resp.Text
//	_ = err
//	_ = m.Calls()
//	_ = m.CallsFor("classify_topic")
//	_ = m.Requests()
package mock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"mono/internal/llm"
)

// Client is safe for concurrent Complete calls.
type Client struct {
	mu     sync.Mutex
	rules  []*rule
	calls  int
	byTask map[string]int
	seen   []llm.Request
}

type reply struct {
	resp llm.Response
	err  error
}

type rule struct {
	task     string
	contains string
	queue    []reply
	sticky   bool
}

// New returns an empty script. Complete fails until a rule is added.
func New() *Client {
	return &Client{byTask: map[string]int{}}
}

// When registers one text answer and repeats it for every later match.
func (c *Client) When(task, contains, text string) *Client {
	return c.add(task, contains, true, textReplies(text))
}

// Queue registers answers in order. After the last one, that last answer repeats.
func (c *Client) Queue(task, contains string, texts ...string) *Client {
	return c.add(task, contains, true, textReplies(texts...))
}

// Once registers answers in order and does not repeat them.
func (c *Client) Once(task, contains string, texts ...string) *Client {
	return c.add(task, contains, false, textReplies(texts...))
}

// Reply registers one full response, including usage and model, and repeats it.
func (c *Client) Reply(task, contains string, resp llm.Response) *Client {
	return c.add(task, contains, true, []reply{{resp: resp}})
}

// Fail registers an error that repeats for every match.
func (c *Client) Fail(task, contains string, err error) *Client {
	if err == nil {
		err = errors.New("llm: mock: пустая ошибка")
	}
	return c.add(task, contains, true, []reply{{err: err}})
}

// Calls is the number of Complete calls, including those that returned an error.
func (c *Client) Calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// CallsFor counts Complete calls whose Task equals task.
func (c *Client) CallsFor(task string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.byTask[task]
}

// Requests returns a copy of the requests in call order.
func (c *Client) Requests() []llm.Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]llm.Request, len(c.seen))
	copy(out, c.seen)
	return out
}

// Complete picks a rule and returns the next queued answer.
func (c *Client) Complete(ctx context.Context, r llm.Request) (llm.Response, error) {
	if err := ctx.Err(); err != nil {
		return llm.Response{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.byTask[r.Task]++
	c.seen = append(c.seen, cloneRequest(r))
	rule := c.match(r)
	if rule == nil {
		return llm.Response{}, fmt.Errorf("llm: mock: нет правила для задачи %q", r.Task)
	}
	return rule.pop()
}

func (c *Client) add(task, contains string, sticky bool, replies []reply) *Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rules = append(c.rules, &rule{
		task:     task,
		contains: contains,
		queue:    replies,
		sticky:   sticky,
	})
	return c
}

func (c *Client) match(r llm.Request) *rule {
	best := -1
	bestScore := -1
	haystack := r.System + "\n" + r.User
	for i, rule := range c.rules {
		if rule.task != "" && rule.task != r.Task {
			continue
		}
		if rule.contains != "" && !strings.Contains(haystack, rule.contains) {
			continue
		}
		score := 0
		if rule.task != "" {
			score += 2
		}
		if rule.contains != "" {
			score += 1
		}
		if score >= bestScore {
			bestScore = score
			best = i
		}
	}
	if best < 0 {
		return nil
	}
	return c.rules[best]
}

func (r *rule) pop() (llm.Response, error) {
	if len(r.queue) == 0 {
		return llm.Response{}, errors.New("llm: mock: нет заготовленного ответа")
	}
	item := r.queue[0]
	if len(r.queue) > 1 || !r.sticky {
		r.queue = r.queue[1:]
	}
	return item.resp, item.err
}

func textReplies(texts ...string) []reply {
	out := make([]reply, 0, len(texts))
	for _, text := range texts {
		out = append(out, reply{resp: llm.Response{Text: text, Model: "mock"}})
	}
	return out
}

func cloneRequest(r llm.Request) llm.Request {
	if r.Schema != nil {
		raw := make(json.RawMessage, len(r.Schema))
		copy(raw, r.Schema)
		r.Schema = raw
	}
	return r
}
