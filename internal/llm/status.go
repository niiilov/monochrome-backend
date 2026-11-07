package llm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// StatusError is an HTTP failure from the model server.
// Body is already redacted and shortened. The API key is not included.
type StatusError struct {
	Status        int
	Body          string
	RetryAfter    time.Duration
	HasRetryAfter bool
}

func (e *StatusError) Error() string {
	if e == nil {
		return "llm: пустая ошибка HTTP"
	}
	if e.Body == "" {
		return fmt.Sprintf("llm: HTTP %d", e.Status)
	}
	return fmt.Sprintf("llm: HTTP %d: %s", e.Status, e.Body)
}

// retryable reports transport failures the retry wrapper may repeat.
// A canceled caller context is not retryable. A client timeout is.
func retryable(err error) bool {
	if err == nil {
		return false
	}
	var se *StatusError
	if errors.As(err, &se) {
		return se.Status == http.StatusTooManyRequests || se.Status == http.StatusRequestTimeout || se.Status >= 500
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne)
}
