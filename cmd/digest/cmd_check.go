package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"cyberdigest/internal/verify"
)

func init() {
	Register("check", "проверить формат digest.md и journal.csv", runCheck)
}

func runCheck(ctx context.Context, args []string) int {
	outDir := "./out"
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			fmt.Println("Использование: digest check [--out ./out]")
			fmt.Println("Проверяет out/digest.md и out/journal.csv.")
			fmt.Println("Если в каталоге запуска есть tools/check_digest.py, запускает и его.")
			return 0
		case arg == "--out" || arg == "-out" || arg == "--out-dir":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "check: укажите каталог после --out")
				return 2
			}
			i++
			outDir = args[i]
		case strings.HasPrefix(arg, "--out="):
			outDir = strings.TrimPrefix(arg, "--out=")
			if outDir == "" {
				fmt.Fprintln(os.Stderr, "check: пустой каталог --out")
				return 2
			}
		default:
			fmt.Fprintf(os.Stderr, "check: неизвестный аргумент %s\n", arg)
			return 2
		}
	}
	digestPath := filepath.Join(outDir, "digest.md")
	journalPath := filepath.Join(outDir, "journal.csv")
	digest, err := os.ReadFile(digestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "check: нет файла %s\n", digestPath)
		return 1
	}
	journal, err := os.ReadFile(journalPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "check: нет файла %s\n", journalPath)
		return 1
	}
	if err := verify.CheckFormat(string(digest), string(journal)); err != nil {
		fmt.Fprintf(os.Stderr, "check: %s\n", err)
		return 1
	}
	scriptOut, ran, err := runOrganizerScript(ctx, outDir)
	if err != nil {
		if strings.TrimSpace(scriptOut) != "" {
			fmt.Fprintln(os.Stderr, strings.TrimSpace(scriptOut))
		}
		fmt.Fprintf(os.Stderr, "check: %s\n", err)
		return 1
	}
	fmt.Println("формат в порядке")
	if ran {
		fmt.Println("скрипт организаторов: ок")
		if strings.TrimSpace(scriptOut) != "" {
			fmt.Println(strings.TrimSpace(scriptOut))
		}
	}
	return 0
}

// runOrganizerScript runs tools/check_digest.py when the file exists.
// The working directory is the directory from which digest was started.
// ran is false when the file is absent. output is the script's stdout and stderr.
func runOrganizerScript(ctx context.Context, outDir string) (output string, ran bool, err error) {
	script := filepath.Join("tools", "check_digest.py")
	info, err := os.Stat(script)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	if info.IsDir() {
		return "", true, fmt.Errorf("tools/check_digest.py является каталогом")
	}
	python := findPython()
	if python == "" {
		return "", true, fmt.Errorf("не найден Python для tools/check_digest.py")
	}
	cmd := exec.CommandContext(ctx, python, script, outDir)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return buf.String(), true, fmt.Errorf("tools/check_digest.py завершился с ошибкой")
	}
	return buf.String(), true, nil
}

func findPython() string {
	for _, name := range []string{"python", "python3"} {
		path, err := exec.LookPath(name)
		if err == nil && path != "" {
			return path
		}
	}
	return ""
}
