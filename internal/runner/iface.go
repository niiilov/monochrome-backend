package runner

import (
	"context"

	"mono/internal/domain"
)

// ProgressFunc reports how far the current stage has moved.
type ProgressFunc func(stage string, done, total int)

// Runner executes one mono run.
type Runner interface {
	Run(ctx context.Context, p domain.RunParams, progress ProgressFunc) (*domain.RunState, domain.RunReport, error)
}
