package llm

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseProxyWebshareAndURL(t *testing.T) {
	web, err := parseProxy("203.0.113.10:8080:alice:s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if web.Scheme != "http" || web.Hostname() != "203.0.113.10" || web.Port() != "8080" {
		t.Fatalf("webshare: %s", web.String())
	}
	pass, ok := web.User.Password()
	if !ok || web.User.Username() != "alice" || pass != "s3cret" {
		t.Fatalf("auth: %s", web.User.String())
	}
	raw, err := parseProxy("https://bob:p%40ss@example.test:3128")
	if err != nil {
		t.Fatal(err)
	}
	if raw.Scheme != "https" || raw.Port() != "3128" || raw.User.Username() != "bob" {
		t.Fatalf("url: %s", raw.String())
	}
	if _, err := parseProxy("socks5://127.0.0.1:1080"); err == nil {
		t.Fatal("socks5 должен быть отвергнут")
	}
}

func TestProxyFileAndEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxies.txt")
	body := "# comment\n\n198.51.100.2:9000:u:p\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLM_PROXY", "http://one:a@203.0.113.5:8000")
	t.Setenv("LLM_PROXY_FILE", path)
	list, err := proxiesFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("count %d", len(list))
	}
	if list[0].Hostname() != "203.0.113.5" || list[1].Hostname() != "198.51.100.2" {
		t.Fatalf("%s %s", list[0].Host, list[1].Host)
	}
}

func TestProxyRingSkipsLoopbackAndRotates(t *testing.T) {
	first, _ := url.Parse("http://203.0.113.1:8000")
	second, _ := url.Parse("http://203.0.113.2:8000")
	ring := &proxyRing{list: []*url.URL{first, second}}
	local, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:1/v1", nil)
	got, err := ring.forRequest(local)
	if err != nil || got != nil {
		t.Fatalf("loopback %+v %v", got, err)
	}
	remote, _ := http.NewRequest(http.MethodGet, "https://example.test/v1", nil)
	a, _ := ring.forRequest(remote)
	b, _ := ring.forRequest(remote)
	if a.Host != "203.0.113.1:8000" || b.Host != "203.0.113.2:8000" {
		t.Fatalf("%s %s", a.Host, b.Host)
	}
}

func TestMissingProxyFile(t *testing.T) {
	t.Setenv("LLM_PROXY", "")
	t.Setenv("LLM_PROXY_FILE", filepath.Join(t.TempDir(), "missing.txt"))
	_, err := proxiesFromEnv()
	if err == nil || !strings.Contains(err.Error(), "LLM_PROXY_FILE") {
		t.Fatal(err)
	}
}
