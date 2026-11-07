package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"cyberdigest/internal/config"
	"cyberdigest/internal/domain"
	"cyberdigest/internal/llm"
	"cyberdigest/internal/runner"
	"cyberdigest/internal/timeutil"
)

func init() {
	Register("run", "собрать дайджест и записать файлы выпуска", runRun)
}

func runRun(ctx context.Context, args []string) int {
	opts, code, err := parseRunArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "run: %s\n", err)
		return code
	}
	if opts.help {
		fmt.Println("Использование: digest run --dataset <path> --control-time <RFC3339 или ДД.ММ.ГГГГ ЧЧ:ММ> --out <dir> [--extra <file>] [--config config.yaml] [--mock] [--live] [--sources sources.yaml]")
		fmt.Println("Собирает дайджест и пишет digest.md, post.txt, email_preview.html, email.txt, journal.csv, journal.json, run_report.json.")
		fmt.Println("Флаг --extra можно повторять: файл добавляется к набору.")
		fmt.Println("Флаг --mock запускает прогон без сетевой модели.")
		fmt.Println("Флаг --live добавляет записи из sources.yaml. Без флага сбор выключен. --sources подменяет файл источников.")
		return 0
	}
	// Stage progress is printed by the callback. Info notes from the pipeline
	// stay in the journal, not on the console.
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))
	loadDotEnv(".env")
	raw, err := os.ReadFile(opts.configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "run: не удалось прочитать конфигурацию %s\n", opts.configPath)
		return 2
	}
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "run: %s\n", err)
		return 2
	}
	if opts.live {
		cfg.Live.Enabled = true
	}
	if strings.TrimSpace(opts.sources) != "" {
		cfg.Live.SourcesFile = opts.sources
	}
	control, err := parseControl(opts.control, cfg.Period.NaiveTZPolicy)
	if err != nil {
		fmt.Fprintf(os.Stderr, "run: контрольное время: %s\n", err)
		return 2
	}
	client, meter, newErr := openClient(cfg, opts.mock)
	state, report, runErr := runner.New(runner.Deps{
		Config:    cfg,
		ConfigRaw: raw,
		Client:    client,
		Meter:     meter,
		Version:   version,
	}).Run(ctx, domain.RunParams{
		DatasetPath: opts.dataset,
		ControlTime: control,
		OutDir:      opts.outDir,
		ExtraPaths:  opts.extra,
	}, func(stage string, done, total int) {
		fmt.Fprintf(os.Stderr, "%s: %d/%d\n", stageLabel(stage), done, total)
	})
	if runErr != nil {
		if newErr != nil {
			fmt.Fprintf(os.Stderr, "прогон остановлен: %s\n", newErr)
			fmt.Fprintln(os.Stderr, "Для прогона без модели добавьте флаг --mock.")
		} else {
			fmt.Fprintf(os.Stderr, "прогон остановлен: %s\n", runErr)
		}
		fmt.Fprintf(os.Stderr, "Журнал и отчёт записаны в %s\n", opts.outDir)
		printSummary(state, report, opts.outDir)
		return 1
	}
	printSummary(state, report, opts.outDir)
	return 0
}

type runOpts struct {
	help       bool
	mock       bool
	live       bool
	dataset    string
	control    string
	outDir     string
	configPath string
	sources    string
	extra      []string
}

func parseRunArgs(args []string) (runOpts, int, error) {
	opts := runOpts{configPath: "config.yaml"}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			opts.help = true
			return opts, 0, nil
		case arg == "--mock":
			opts.mock = true
		case arg == "--live":
			opts.live = true
		case arg == "--dataset" || arg == "--control-time" || arg == "--out" || arg == "--extra" || arg == "--config" || arg == "-config" || arg == "--sources":
			value, err := takeValue(args, &i, arg)
			if err != nil {
				return opts, 2, err
			}
			switch arg {
			case "--dataset":
				opts.dataset = value
			case "--control-time":
				opts.control = value
			case "--out":
				opts.outDir = value
			case "--extra":
				opts.extra = append(opts.extra, value)
			case "--sources":
				opts.sources = value
			default:
				opts.configPath = value
			}
		case strings.HasPrefix(arg, "--dataset="):
			opts.dataset = strings.TrimPrefix(arg, "--dataset=")
		case strings.HasPrefix(arg, "--control-time="):
			opts.control = strings.TrimPrefix(arg, "--control-time=")
		case strings.HasPrefix(arg, "--out="):
			opts.outDir = strings.TrimPrefix(arg, "--out=")
		case strings.HasPrefix(arg, "--extra="):
			opts.extra = append(opts.extra, strings.TrimPrefix(arg, "--extra="))
		case strings.HasPrefix(arg, "--config="):
			opts.configPath = strings.TrimPrefix(arg, "--config=")
		case strings.HasPrefix(arg, "--sources="):
			opts.sources = strings.TrimPrefix(arg, "--sources=")
		default:
			return opts, 2, fmt.Errorf("неизвестный аргумент %s", arg)
		}
	}
	if opts.help {
		return opts, 0, nil
	}
	if strings.TrimSpace(opts.dataset) == "" {
		return opts, 2, fmt.Errorf("укажите --dataset")
	}
	if strings.TrimSpace(opts.control) == "" {
		return opts, 2, fmt.Errorf("укажите --control-time")
	}
	if strings.TrimSpace(opts.outDir) == "" {
		return opts, 2, fmt.Errorf("укажите --out")
	}
	if strings.TrimSpace(opts.configPath) == "" {
		return opts, 2, fmt.Errorf("пустой путь --config")
	}
	return opts, 0, nil
}

func takeValue(args []string, i *int, name string) (string, error) {
	if *i+1 >= len(args) {
		return "", fmt.Errorf("укажите значение после %s", name)
	}
	*i++
	value := strings.TrimSpace(args[*i])
	if value == "" {
		return "", fmt.Errorf("пустое значение %s", name)
	}
	return value, nil
}

func parseControl(raw, policy string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, fmt.Errorf("пустая строка")
	}
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		return parsed, nil
	}
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return parsed, nil
	}
	if policy == "" {
		policy = timeutil.PolicyAssumeMSK
	}
	got, note := timeutil.ParseDate(raw, policy)
	if got == nil {
		if strings.TrimSpace(note) == "" {
			note = "не удалось разобрать"
		}
		return time.Time{}, fmt.Errorf("%s", note)
	}
	return *got, nil
}

func openClient(cfg config.Config, mock bool) (llm.Client, *llm.Meter, error) {
	if mock {
		cfg.LLM.Provider = "mock"
		if strings.TrimSpace(cfg.LLM.Model) == "" {
			cfg.LLM.Model = "mock"
		}
		meter := llm.NewMeter(cfg.LLM)
		return llm.WithMeter(runner.NewOfflineClient(), meter), meter, nil
	}
	client, meter, err := llm.New(cfg.LLM)
	if err != nil {
		return nil, llm.NewMeter(cfg.LLM), err
	}
	return client, meter, nil
}

func printSummary(state *domain.RunState, report domain.RunReport, outDir string) {
	fmt.Printf("включено: %d\n", report.Counts.Included)
	fmt.Printf("объединено: %d\n", report.Counts.Merged)
	fmt.Printf("исключено: %d\n", report.Counts.Excluded)
	fmt.Printf("события: %d\n", report.Counts.Events)
	fmt.Printf("входные токены: %d\n", report.LLM.InputTokens)
	fmt.Printf("выходные токены: %d\n", report.LLM.OutputTokens)
	if report.LLM.Cost != nil && !report.LLM.CostUnavailable {
		fmt.Printf("стоимость: %.6f\n", *report.LLM.Cost)
	}
	fmt.Printf("каталог: %s\n", outDir)
	if state != nil && state.Digest.Empty {
		fmt.Println("выпуск пустой")
	}
}

func stageLabel(stage string) string {
	switch stage {
	case "load":
		return "загрузка"
	case "normalize":
		return "нормализация"
	case "period":
		return "период"
	case "guard":
		return "проверка инъекций"
	case "topic":
		return "тема"
	case "extract":
		return "извлечение"
	case "cluster":
		return "кластеризация"
	case "rank":
		return "ранжирование"
	case "select":
		return "отбор"
	case "summarize":
		return "тексты"
	case "verify":
		return "проверка"
	case "write":
		return "запись"
	default:
		return stage
	}
}
