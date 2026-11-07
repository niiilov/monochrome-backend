package live

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Source is one site in sources.yaml.
// Kind is rss, html, or api. HTML and API fields select items inside the page.
type Source struct {
	ID         string `yaml:"id"`
	Name       string `yaml:"name"`
	Kind       string `yaml:"kind"`
	URL        string `yaml:"url"`
	Item       string `yaml:"item"`
	Title      string `yaml:"title"`
	Link       string `yaml:"link"`
	Date       string `yaml:"date"`
	Text       string `yaml:"text"`
	Items      string `yaml:"items"`
	FieldID    string `yaml:"field_id"`
	FieldTitle string `yaml:"field_title"`
	FieldText  string `yaml:"field_text"`
	FieldURL   string `yaml:"field_url"`
	FieldDate  string `yaml:"field_date"`
}

type sourceFile struct {
	Sources []Source `yaml:"sources"`
}

// LoadSources reads the YAML list. A missing file is an error for the caller to record.
func LoadSources(path string) ([]Source, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("файл источников: %w", err)
	}
	var file sourceFile
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("разбор источников: %w", err)
	}
	dir := filepath.Dir(path)
	out := make([]Source, 0, len(file.Sources))
	for _, src := range file.Sources {
		src.ID = strings.TrimSpace(src.ID)
		src.Name = strings.TrimSpace(src.Name)
		src.Kind = strings.ToLower(strings.TrimSpace(src.Kind))
		src.URL = resolveLoc(dir, src.URL)
		if src.Name == "" {
			src.Name = src.ID
		}
		out = append(out, src)
	}
	return out, nil
}

// resolveLoc keeps http(s) URLs and turns a relative path into a path next to the YAML file.
func resolveLoc(dir, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	low := strings.ToLower(raw)
	if strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://") || strings.HasPrefix(low, "file:") {
		return raw
	}
	if filepath.IsAbs(raw) {
		return raw
	}
	return filepath.Join(dir, filepath.FromSlash(raw))
}
