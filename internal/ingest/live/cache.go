package live

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (c Collector) cacheGet(key string) ([]byte, bool) {
	dir := strings.TrimSpace(c.Config.CacheDir)
	if dir == "" {
		return nil, false
	}
	raw, err := os.ReadFile(filepath.Join(dir, cacheName(key)))
	if err != nil {
		return nil, false
	}
	i := bytes.IndexByte(raw, '\n')
	if i <= 0 {
		return nil, false
	}
	sec, err := strconv.ParseInt(string(raw[:i]), 10, 64)
	if err != nil {
		return nil, false
	}
	age := c.now().Sub(time.Unix(sec, 0))
	if age < 0 {
		age = 0
	}
	if age > c.ttl() {
		return nil, false
	}
	return append([]byte(nil), raw[i+1:]...), true
}

func (c Collector) cachePut(key string, body []byte) {
	dir := strings.TrimSpace(c.Config.CacheDir)
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	var buf bytes.Buffer
	buf.WriteString(strconv.FormatInt(c.now().Unix(), 10))
	buf.WriteByte('\n')
	buf.Write(body)
	_ = os.WriteFile(filepath.Join(dir, cacheName(key)), buf.Bytes(), 0o644)
}

func cacheName(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:]) + ".bin"
}
