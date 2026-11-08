package prompts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"sync"
	"text/template"

	promptfs "mono/prompts"
)

const commonName = "common_rules"

var versionRE = regexp.MustCompile(`^\{\{/\*\s*version:\s*(\d+)\s*\*/\}\}$`)

var (
	loadOnce sync.Once
	rawFiles map[string]string
	parsed   *template.Template
	loadErr  error
)

// Render executes prompts/<name>.system.tmpl and prompts/<name>.user.tmpl.
// version is taken from the first-line comment {{/* version: N */}}.
// Every prompt except common_rules itself is prefixed with the common rules,
// unless the system text already contains that block.
func Render(name string, data any) (system, user, version string, err error) {
	if err := ensure(); err != nil {
		return "", "", "", err
	}
	system, err = exec(name+".system", data)
	if err != nil {
		return "", "", "", err
	}
	user, err = exec(name+".user", data)
	if err != nil {
		return "", "", "", err
	}
	version, err = fileVersion(name)
	if err != nil {
		return "", "", "", err
	}
	if name != commonName {
		rules, _, _, err := Render(commonName, nil)
		if err != nil {
			return "", "", "", err
		}
		system = mergeSystem(rules, system)
	}
	return system, strings.TrimSpace(user), version, nil
}

// Hash is the SHA-256 hex digest of every embedded template, names sorted.
// The API key and other configuration are not part of the digest.
func Hash() string {
	if err := ensure(); err != nil {
		return ""
	}
	names := make([]string, 0, len(rawFiles))
	for name := range rawFiles {
		names = append(names, name)
	}
	sort.Strings(names)
	sum := sha256.New()
	for _, name := range names {
		sum.Write([]byte(name))
		sum.Write([]byte{0})
		sum.Write([]byte(rawFiles[name]))
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil))
}

func ensure() error {
	loadOnce.Do(func() {
		rawFiles = map[string]string{}
		root := template.New("prompts")
		entries, err := fs.ReadDir(promptfs.FS, ".")
		if err != nil {
			loadErr = fmt.Errorf("prompts: чтение шаблонов: %w", err)
			return
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".tmpl") {
				continue
			}
			body, err := fs.ReadFile(promptfs.FS, entry.Name())
			if err != nil {
				loadErr = fmt.Errorf("prompts: чтение %s: %w", entry.Name(), err)
				return
			}
			rawFiles[entry.Name()] = string(body)
			name := strings.TrimSuffix(entry.Name(), ".tmpl")
			if _, err := root.New(name).Parse(string(body)); err != nil {
				loadErr = fmt.Errorf("prompts: разбор %s: %w", entry.Name(), err)
				return
			}
		}
		raw, ok := rawFiles[commonName+".system.tmpl"]
		if !ok {
			loadErr = errorsMissing(commonName + ".system.tmpl")
			return
		}
		if _, err := root.New(commonName).Parse(raw); err != nil {
			loadErr = fmt.Errorf("prompts: общий блок: %w", err)
			return
		}
		parsed = root
	})
	return loadErr
}

func exec(templateName string, data any) (string, error) {
	tpl := parsed.Lookup(templateName)
	if tpl == nil {
		return "", fmt.Errorf("prompts: нет шаблона %s", templateName)
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("prompts: выполнение %s: %w", templateName, err)
	}
	return strings.TrimSpace(buf.String()), nil
}

func fileVersion(name string) (string, error) {
	systemRaw, ok := rawFiles[name+".system.tmpl"]
	if !ok {
		return "", fmt.Errorf("prompts: нет файла %s.system.tmpl", name)
	}
	systemVersion := versionOf(systemRaw)
	if systemVersion == "" {
		return "", fmt.Errorf("prompts: в %s.system.tmpl нет комментария version", name)
	}
	if userRaw, ok := rawFiles[name+".user.tmpl"]; ok {
		userVersion := versionOf(userRaw)
		if userVersion != "" && userVersion != systemVersion {
			return "", fmt.Errorf("prompts: версии %s не совпадают: system %s, user %s", name, systemVersion, userVersion)
		}
	}
	return systemVersion, nil
}

func versionOf(raw string) string {
	line := raw
	if i := strings.IndexAny(raw, "\r\n"); i >= 0 {
		line = raw[:i]
	}
	line = strings.TrimSpace(line)
	m := versionRE.FindStringSubmatch(line)
	if m == nil {
		return ""
	}
	return m[1]
}

func mergeSystem(rules, body string) string {
	rules = strings.TrimSpace(rules)
	body = strings.TrimSpace(body)
	if rules == "" {
		return body
	}
	if body == "" || strings.Contains(body, rules) {
		if body == "" {
			return rules
		}
		return body
	}
	return rules + "\n\n" + body
}

func errorsMissing(name string) error {
	return fmt.Errorf("prompts: нет файла %s", name)
}
