package llm

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// newLLMHTTPClient uses LLM_PROXY and LLM_PROXY_FILE when they are set.
// Localhost is never sent through a proxy, so tests keep talking to httptest.
// An empty configuration keeps a direct client with a larger idle pool.
func newLLMHTTPClient(timeout time.Duration) (*http.Client, []string, error) {
	proxies, err := proxiesFromEnv()
	if err != nil {
		return nil, nil, err
	}
	transport := tunedTransport()
	if len(proxies) == 0 {
		return &http.Client{Timeout: timeout, Transport: transport}, nil, nil
	}
	ring := &proxyRing{list: proxies}
	transport.Proxy = ring.forRequest
	return &http.Client{Timeout: timeout, Transport: transport}, proxySecrets(proxies), nil
}

// tunedTransport keeps idle TLS connections for a whole parallel batch.
// The default client keeps two idle connections per host, so the rest of a
// burst opens a new handshake for every call.
func tunedTransport() *http.Transport {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   32,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: time.Second,
			ForceAttemptHTTP2:     true,
		}
	}
	t := base.Clone()
	if t.MaxIdleConnsPerHost < 32 {
		t.MaxIdleConnsPerHost = 32
	}
	if t.MaxIdleConns < 100 {
		t.MaxIdleConns = 100
	}
	return t
}

func proxiesFromEnv() ([]*url.URL, error) {
	var lines []string
	if raw := strings.TrimSpace(os.Getenv("LLM_PROXY")); raw != "" {
		lines = append(lines, splitProxyList(raw)...)
	}
	path := strings.TrimSpace(os.Getenv("LLM_PROXY_FILE"))
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("llm: не удалось прочитать LLM_PROXY_FILE: %w", err)
		}
		lines = append(lines, splitProxyFile(string(raw))...)
	}
	out := make([]*url.URL, 0, len(lines))
	for i, line := range lines {
		parsed, err := parseProxy(line)
		if err != nil {
			return nil, fmt.Errorf("llm: прокси в строке %d: %w", i+1, err)
		}
		out = append(out, parsed)
	}
	return out, nil
}

func splitProxyList(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	})
	return trimProxyLines(parts)
}

func splitProxyFile(raw string) []string {
	raw = strings.TrimPrefix(raw, "\uFEFF")
	return trimProxyLines(strings.Split(raw, "\n"))
}

func trimProxyLines(parts []string) []string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || strings.HasPrefix(part, "#") {
			continue
		}
		out = append(out, part)
	}
	return out
}

// parseProxy accepts http(s) URLs and Webshare lines host:port:user:pass.
func parseProxy(line string) (*url.URL, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, fmt.Errorf("пустая строка")
	}
	if strings.Contains(line, "://") {
		u, err := url.Parse(line)
		if err != nil {
			return nil, fmt.Errorf("непонятный адрес")
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return nil, fmt.Errorf("нужна схема http или https")
		}
		if u.Hostname() == "" || u.Port() == "" {
			return nil, fmt.Errorf("нужны хост и порт")
		}
		return u, nil
	}
	parts := strings.SplitN(line, ":", 4)
	switch len(parts) {
	case 2:
		return proxyURL("http", parts[0], parts[1], "", "")
	case 4:
		return proxyURL("http", parts[0], parts[1], parts[2], parts[3])
	default:
		return nil, fmt.Errorf("ожидался адрес http://user:pass@host:port или host:port:user:pass")
	}
}

func proxyURL(scheme, host, port, user, pass string) (*url.URL, error) {
	host = strings.TrimSpace(host)
	port = strings.TrimSpace(port)
	if host == "" || port == "" {
		return nil, fmt.Errorf("нужны хост и порт")
	}
	if _, err := net.LookupPort("tcp", port); err != nil {
		return nil, fmt.Errorf("непонятный порт")
	}
	u := &url.URL{Scheme: scheme, Host: net.JoinHostPort(host, port)}
	user = strings.TrimSpace(user)
	if user != "" || pass != "" {
		u.User = url.UserPassword(user, pass)
	}
	return u, nil
}

type proxyRing struct {
	list []*url.URL
	n    atomic.Uint64
}

func (r *proxyRing) forRequest(req *http.Request) (*url.URL, error) {
	if r == nil || len(r.list) == 0 || req == nil || req.URL == nil || isLoopback(req.URL.Hostname()) {
		return nil, nil
	}
	i := r.n.Add(1) - 1
	return r.list[int(i%uint64(len(r.list)))], nil
}

func isLoopback(host string) bool {
	host = strings.Trim(strings.ToLower(host), "[]")
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func proxySecrets(list []*url.URL) []string {
	var secrets []string
	seen := map[string]bool{}
	add := func(s string) {
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		secrets = append(secrets, s)
	}
	for _, u := range list {
		if u.User == nil {
			continue
		}
		add(u.User.Username())
		pass, ok := u.User.Password()
		if ok {
			add(pass)
		}
		add(u.User.String())
	}
	return secrets
}
