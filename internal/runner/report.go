package runner

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"cyberdigest/internal/config"
	"cyberdigest/internal/domain"
	"cyberdigest/internal/journal"
)

type texts struct {
	digest string
	post   string
	email  string
	html   string
}

// writeRun saves digest, post, email, journal and run_report.json.
// rows is the dataset order. Artifact hashes are filled before the report is written.
func writeRun(outDir string, rows []domain.JournalEntry, report *domain.RunReport, rendered texts) error {
	start := time.Now()
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("каталог результата: %w", err)
	}
	j := journal.New()
	for _, row := range rows {
		j.Add(row)
	}
	var csvBuf bytes.Buffer
	if err := j.WriteCSV(&csvBuf); err != nil {
		return err
	}
	var jsonBuf bytes.Buffer
	if err := j.WriteJSON(&jsonBuf); err != nil {
		return err
	}
	files := []struct {
		name string
		body []byte
	}{
		{"digest.md", []byte(rendered.digest)},
		{"post.txt", []byte(rendered.post)},
		{"email_preview.html", []byte(rendered.html)},
		{"email.txt", []byte(rendered.email)},
		{"journal.csv", csvBuf.Bytes()},
		{"journal.json", jsonBuf.Bytes()},
	}
	if report.ArtifactHashes == nil {
		report.ArtifactHashes = map[string]string{}
	}
	for _, file := range files {
		path := filepath.Join(outDir, file.name)
		if err := os.WriteFile(path, file.body, 0o644); err != nil {
			return fmt.Errorf("запись %s: %w", file.name, err)
		}
		report.ArtifactHashes[file.name] = sha256Hex(file.body)
	}
	if report.StageDurationsMS == nil {
		report.StageDurationsMS = map[string]int64{}
	}
	report.StageDurationsMS["write"] = time.Since(start).Milliseconds()
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("отчёт: %w", err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(filepath.Join(outDir, "run_report.json"), raw, 0o644); err != nil {
		return fmt.Errorf("запись run_report.json: %w", err)
	}
	return nil
}

func hashConfig(cfg config.Config, raw []byte) string {
	if len(bytes.TrimSpace(raw)) > 0 {
		return sha256Hex(raw)
	}
	safe := cfg
	safe.LLM.APIKey = ""
	safe.Mail.Password = ""
	encoded, err := json.Marshal(safe)
	if err != nil {
		return ""
	}
	return sha256Hex(encoded)
}

func hashDataset(paths []string) (string, error) {
	sum := sha256.New()
	for _, path := range paths {
		if err := hashPath(sum, path); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

func hashPath(sum hash.Hash, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return hashFile(sum, path)
	}
	var names []string
	err = filepath.WalkDir(path, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		names = append(names, name)
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		if err := hashFile(sum, name); err != nil {
			return err
		}
	}
	return nil
}

func hashFile(sum hash.Hash, path string) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sum.Write([]byte(filepath.ToSlash(path)))
	sum.Write([]byte{0})
	sum.Write(body)
	sum.Write([]byte{0})
	return nil
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:4])
}
