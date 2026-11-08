package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"mono/internal/api"
	"mono/internal/config"
	"mono/internal/mailer"
	"mono/internal/runner"
	"mono/internal/runner/fake"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("monod", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fakeMode := fs.Bool("fake", false, "подготовленный прогон без модели")
	mockMode := fs.Bool("mock", false, "реальный раннер с локальной моделью, без сети")
	configPath := fs.String("config", "config.yaml", "путь к config.yaml")
	runsDir := fs.String("runs", "runs", "каталог состояния прогонов")
	fixture := fs.String("fixture", "testdata/fake_run", "каталог подготовленного прогона для --fake")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *fakeMode && *mockMode {
		return errors.New("флаги --fake и --mock вместе не используются")
	}
	loadDotEnv(".env")
	raw, err := os.ReadFile(*configPath)
	if err != nil {
		return fmt.Errorf("конфигурация %s: %w", *configPath, err)
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	var pipeline runner.Runner
	if *fakeMode {
		if _, err := os.Stat(filepath.Join(*fixture, "mono.md")); err != nil {
			return fmt.Errorf("подготовленный прогон %s: %w", *fixture, err)
		}
		replay := fake.New(*fixture)
		replay.Delay = 120 * time.Millisecond
		pipeline = replay
	} else {
		pipeline, err = buildRunner(cfg, raw, *mockMode)
		if err != nil {
			return fmt.Errorf("%w. Для прогона без сети добавьте --mock, для подготовленного выпуска --fake", err)
		}
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	service := api.New(pipeline, mailer.New(cfg.Mail, log), *runsDir, log)
	httpServer := &http.Server{
		Handler:           service.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	ln, err := net.Listen("tcp", cfg.Server.Addr)
	if err != nil {
		return fmt.Errorf("адрес %s: %w", cfg.Server.Addr, err)
	}
	log.Info("сервер запущен", "addr", ln.Addr().String(), "fake", *fakeMode, "mock", *mockMode)
	errCh := make(chan error, 1)
	go func() {
		if err := httpServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	select {
	case <-ctx.Done():
	case err := <-errCh:
		return err
	}
	shut, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = service.Shutdown(shut)
	return httpServer.Shutdown(shut)
}
