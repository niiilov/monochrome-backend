package main

import (
	"strings"

	"cyberdigest/internal/config"
	"cyberdigest/internal/llm"
	"cyberdigest/internal/runner"
)

const serverVersion = "0.1.0"

// buildRunner wires runner.New from the loaded configuration.
// mock uses the offline client and does not open the network.
func buildRunner(cfg config.Config, raw []byte, mock bool) (runner.Runner, error) {
	client, meter, err := openClient(cfg, mock)
	if err != nil {
		return nil, err
	}
	return runner.New(runner.Deps{
		Config:    cfg,
		ConfigRaw: raw,
		Client:    client,
		Meter:     meter,
		Version:   serverVersion,
	}), nil
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
	return llm.New(cfg.LLM)
}
