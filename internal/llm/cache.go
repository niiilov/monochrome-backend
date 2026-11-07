package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type cacheClient struct {
	next  Client
	dir   string
	model string
}

// WithCache stores successful responses on disk, keyed by a hash of the
// model and the request. The API key is not part of the hash and is not written.
// An empty dir disables the cache and returns next unchanged.
// A cache read or write failure does not fail the completion.
func WithCache(next Client, dir, model string) Client {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return next
	}
	return &cacheClient{next: next, dir: dir, model: model}
}

func (c *cacheClient) Complete(ctx context.Context, r Request) (Response, error) {
	path := filepath.Join(c.dir, requestHash(c.model, r)+".json")
	if resp, ok := readCache(path); ok {
		return resp, nil
	}
	resp, err := c.next.Complete(ctx, r)
	if err != nil {
		return resp, err
	}
	writeCache(path, resp)
	return resp, nil
}

func requestHash(model string, r Request) string {
	doc := struct {
		Model       string          `json:"model"`
		Task        string          `json:"task"`
		System      string          `json:"system"`
		User        string          `json:"user"`
		Schema      json.RawMessage `json:"schema,omitempty"`
		Temperature float64         `json:"temperature"`
		MaxTokens   int             `json:"max_tokens"`
	}{
		Model:       model,
		Task:        r.Task,
		System:      r.System,
		User:        r.User,
		Schema:      r.Schema,
		Temperature: r.Temperature,
		MaxTokens:   r.MaxTokens,
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		raw = []byte(model + "\n" + r.Task + "\n" + r.System + "\n" + r.User)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func readCache(path string) (Response, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Response{}, false
	}
	var resp Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		return Response{}, false
	}
	if strings.TrimSpace(resp.Text) == "" {
		return Response{}, false
	}
	return resp, true
}

func writeCache(path string, resp Response) {
	raw, err := json.Marshal(resp)
	if err != nil {
		return
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	f, err := os.CreateTemp(dir, "resp-*.tmp")
	if err != nil {
		return
	}
	tmp := f.Name()
	_, werr := f.Write(raw)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmp)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(path)
		if err := os.Rename(tmp, path); err != nil {
			_ = os.Remove(tmp)
		}
	}
}
