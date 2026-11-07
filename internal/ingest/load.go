package ingest

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"cyberdigest/internal/config"
	"cyberdigest/internal/domain"
)

const (
	reasonRead  = "Не удалось прочитать запись"
	reasonEmpty = "Пустой файл или запись"
	reasonDup   = "Повторный ID в наборе, использована первая запись"
)

// Loader reads one dataset path (a file or a directory).
type Loader interface {
	Load(ctx context.Context, path string) ([]domain.Article, []domain.JournalEntry, error)
}

type fileLoader struct {
	fields fieldReader
}

type loadState struct {
	seen map[string]bool
	seq  int
}

func newState() *loadState {
	return &loadState{seen: map[string]bool{}}
}

// NewLoader returns a file and directory loader with the given field map.
// An empty field map or a nil marker list falls back to config.Default.
func NewLoader(cfg config.Ingest) Loader {
	return &fileLoader{fields: newFieldReader(cfg)}
}

// LoadDataset reads every path and returns articles plus journal rows for bad records.
// Duplicate IDs are resolved across the whole call: the first record is kept.
// A missing path is an error. Malformed content is not.
func LoadDataset(ctx context.Context, paths []string, cfg config.Ingest) ([]domain.Article, []domain.JournalEntry, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(paths) == 0 {
		return nil, nil, errors.New("не указан путь к набору")
	}
	l := &fileLoader{fields: newFieldReader(cfg)}
	st := newState()
	var articles []domain.Article
	var journal []domain.JournalEntry
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		arts, rows, err := l.loadPath(ctx, path, st)
		if err != nil {
			return nil, nil, err
		}
		articles = append(articles, arts...)
		journal = append(journal, rows...)
	}
	return articles, journal, nil
}

// Load reads one path. Duplicate detection starts over on each call.
func (l *fileLoader) Load(ctx context.Context, path string) ([]domain.Article, []domain.JournalEntry, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return l.loadPath(ctx, path, newState())
}

func (l *fileLoader) loadPath(ctx context.Context, path string, st *loadState) ([]domain.Article, []domain.JournalEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, fmt.Errorf("чтение набора %s: %w", path, err)
	}
	if !info.IsDir() {
		return l.readFile(path, st)
	}
	var names []string
	err = filepath.WalkDir(path, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name := d.Name()
		if d.IsDir() {
			if p != path && strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") {
			return nil
		}
		if strings.EqualFold(name, "README.md") || strings.EqualFold(name, "README.txt") {
			return nil
		}
		names = append(names, p)
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("чтение каталога %s: %w", path, err)
	}
	sort.Strings(names)
	var articles []domain.Article
	var journal []domain.JournalEntry
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		arts, rows, err := l.readFile(name, st)
		if err != nil {
			return nil, nil, err
		}
		articles = append(articles, arts...)
		journal = append(journal, rows...)
	}
	return articles, journal, nil
}

func (l *fileLoader) readFile(path string, st *loadState) ([]domain.Article, []domain.JournalEntry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("чтение набора %s: %w", path, err)
	}
	text := decodeText(raw)
	if strings.TrimSpace(text) == "" {
		return nil, []domain.JournalEntry{st.empty(whereFile(path), "файл пуст")}, nil
	}
	arts, rows := l.parseText(path, text, st)
	return arts, rows, nil
}

func whereFile(path string) string {
	return filepath.ToSlash(path)
}

func (st *loadState) nextUnknown() string {
	for {
		st.seq++
		id := fmt.Sprintf("unknown-%d", st.seq)
		if st.seen[id] {
			continue
		}
		st.seen[id] = true
		return id
	}
}

func (st *loadState) empty(where, detail string) domain.JournalEntry {
	return st.row(st.nextUnknown(), domain.ReasonEmptyInput, reasonEmpty, detail+" ("+where+")", "", "")
}

func (st *loadState) readRow(where, detail string) domain.JournalEntry {
	return st.row(st.nextUnknown(), domain.ReasonReadError, reasonRead, reasonRead+": "+detail+" ("+where+")", "", "")
}

func (st *loadState) row(id string, code domain.ReasonCode, reason, errText, source, rawURL string) domain.JournalEntry {
	return domain.JournalEntry{
		ID:         id,
		Status:     domain.StatusExcluded,
		ReasonCode: code,
		Reason:     reason,
		Error:      errText,
		Source:     source,
		SourceKind: SourceKindOf(source, rawURL),
	}
}
