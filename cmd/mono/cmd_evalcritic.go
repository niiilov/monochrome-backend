package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mono/internal/config"
	"mono/internal/critic"
	"mono/internal/llm"
	"mono/internal/llm/mock"
)

func init() {
	Register("eval-critic", "замерить эффект ИИ-критика и записать отчёт", runEvalCritic)
}

func runEvalCritic(ctx context.Context, args []string) int {
	opts, code, err := parseEvalCriticArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval-critic: %s\n", err)
		return code
	}
	if opts.help {
		fmt.Println("Использование: mono eval-critic [--out docs] [--config config.yaml] [--mock|--live]")
		fmt.Println("Сравнивает прогон с критиком и без него на учебном пересказе и на синтетике с подсаженными ошибками.")
		fmt.Println("Пишет docs/critic_eval.json и docs/critic_eval.md (каталог задаётся --out).")
		fmt.Println("По умолчанию используется --mock без сети.")
		return 0
	}
	loadDotEnv(".env")
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval-critic: %s\n", err)
		return 2
	}
	client, meter, mockMode, err := openEvalCriticClient(cfg, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval-critic: %s\n", err)
		return 2
	}
	rep, err := critic.RunEval(ctx, client, meter, mockMode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval-critic: %s\n", err)
		return 1
	}
	jsonPath := filepath.Join(opts.outDir, "critic_eval.json")
	mdPath := filepath.Join(opts.outDir, "critic_eval.md")
	if err := critic.WriteReport(rep, jsonPath, mdPath); err != nil {
		fmt.Fprintf(os.Stderr, "eval-critic: запись отчёта: %s\n", err)
		return 1
	}
	fmt.Println(rep.Summary)
	fmt.Printf("отчёт: %s\n", jsonPath)
	fmt.Printf("отчёт: %s\n", mdPath)
	return 0
}

type evalCriticOpts struct {
	help       bool
	mock       bool
	live       bool
	outDir     string
	configPath string
}

func parseEvalCriticArgs(args []string) (evalCriticOpts, int, error) {
	opts := evalCriticOpts{configPath: "config.yaml", outDir: "docs", mock: true}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			opts.help = true
			return opts, 0, nil
		case arg == "--mock":
			opts.mock = true
			opts.live = false
		case arg == "--live":
			opts.live = true
			opts.mock = false
		case arg == "--out" || arg == "--out-dir":
			value, err := takeValue(args, &i, arg)
			if err != nil {
				return opts, 2, err
			}
			opts.outDir = value
		case strings.HasPrefix(arg, "--out="):
			opts.outDir = strings.TrimPrefix(arg, "--out=")
			if opts.outDir == "" {
				return opts, 2, fmt.Errorf("пустой каталог --out")
			}
		case arg == "--config" || arg == "-config":
			value, err := takeValue(args, &i, arg)
			if err != nil {
				return opts, 2, err
			}
			opts.configPath = value
		default:
			return opts, 2, fmt.Errorf("неизвестный аргумент %s", arg)
		}
	}
	if strings.TrimSpace(opts.outDir) == "" {
		return opts, 2, fmt.Errorf("укажите каталог --out")
	}
	return opts, 0, nil
}

func openEvalCriticClient(cfg config.Config, opts evalCriticOpts) (llm.Client, *llm.Meter, bool, error) {
	if opts.mock || !opts.live {
		meter := llm.NewMeter(cfg.LLM)
		client := llm.WithMeter(critic.ScriptEvalMock(mock.New()), meter)
		return client, meter, true, nil
	}
	llmCfg := cfg.LLM
	if model := strings.TrimSpace(cfg.Critic.Model); model != "" {
		llmCfg.Model = model
	}
	client, meter, err := llm.New(llmCfg)
	if err != nil {
		return nil, nil, false, err
	}
	return client, meter, false, nil
}
