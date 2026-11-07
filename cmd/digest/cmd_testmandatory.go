package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"cyberdigest/internal/mandatory"
)

func init() {
	Register("test-mandatory", "прогнать обязательные сценарии и записать отчёт", runTestMandatory)
}

func runTestMandatory(ctx context.Context, args []string) int {
	opt, code, err := parseTestMandatoryArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "test-mandatory: %s\n", err)
		return code
	}
	if opt.help {
		fmt.Println("Использование: digest test-mandatory [--live] [--out out] [--config config.yaml]")
		fmt.Println("Прогоняет сценарии T1-T8, инъекции и трудные входы.")
		fmt.Println("Без --live используется mock-модель. С --live вызывается модель из окружения.")
		fmt.Println("Пишет tests_report.json и tests_report.md. Секреты в отчёт не попадают.")
		return 0
	}
	loadDotEnv(".env")
	rep, err := mandatory.Execute(ctx, mandatory.Options{
		OutDir:     opt.outDir,
		ConfigPath: opt.configPath,
		Live:       opt.live,
		Version:    version,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "test-mandatory: %s\n", err)
		return 2
	}
	for _, line := range rep.Lines() {
		fmt.Println(line)
	}
	fmt.Printf("отчёт: %s\n", opt.outDir)
	if rep.OK() {
		return 0
	}
	return 1
}

type testMandatoryOpts struct {
	help       bool
	live       bool
	outDir     string
	configPath string
}

func parseTestMandatoryArgs(args []string) (testMandatoryOpts, int, error) {
	opt := testMandatoryOpts{outDir: "out", configPath: "config.yaml"}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			opt.help = true
			return opt, 0, nil
		case arg == "--live":
			opt.live = true
		case arg == "--out" || arg == "--config":
			value, err := takeValue(args, &i, arg)
			if err != nil {
				return opt, 2, err
			}
			if arg == "--out" {
				opt.outDir = value
			} else {
				opt.configPath = value
			}
		case strings.HasPrefix(arg, "--out="):
			opt.outDir = strings.TrimPrefix(arg, "--out=")
		case strings.HasPrefix(arg, "--config="):
			opt.configPath = strings.TrimPrefix(arg, "--config=")
		default:
			return opt, 2, fmt.Errorf("неизвестный аргумент %s", arg)
		}
	}
	if strings.TrimSpace(opt.outDir) == "" {
		return opt, 2, fmt.Errorf("пустой каталог --out")
	}
	if strings.TrimSpace(opt.configPath) == "" {
		return opt, 2, fmt.Errorf("пустой путь --config")
	}
	return opt, 0, nil
}
