package llm

import (
	"context"
	"errors"
	"time"
)

const (
	defaultMaxRetries = 3
	defaultBaseDelay  = 200 * time.Millisecond
	defaultMaxDelay   = 5 * time.Second
	maxRetryAfter     = 15 * time.Second
)

// RetryOptions controls WithRetry.
// A zero MaxRetries, BaseDelay, or MaxDelay is replaced by the default
// (3 repeats, 200ms, 5s). Wrap nothing if retries must be disabled.
type RetryOptions struct {
	MaxRetries int
	BaseDelay  time.Duration
	MaxDelay   time.Duration
}

func (o RetryOptions) norm() RetryOptions {
	if o.MaxRetries <= 0 {
		o.MaxRetries = defaultMaxRetries
	}
	if o.BaseDelay <= 0 {
		o.BaseDelay = defaultBaseDelay
	}
	if o.MaxDelay <= 0 {
		o.MaxDelay = defaultMaxDelay
	}
	if o.MaxDelay < o.BaseDelay {
		o.MaxDelay = o.BaseDelay
	}
	return o
}

type retryClient struct {
	next Client
	opt  RetryOptions
}

// WithRetry repeats 429, 408, 5xx, timeouts, and temporary network errors.
// The wait grows exponentially. Retry-After, when present, replaces the wait
// and is capped at 15 seconds.
func WithRetry(next Client, opt RetryOptions) Client {
	return &retryClient{next: next, opt: opt.norm()}
}

func (c *retryClient) Complete(ctx context.Context, r Request) (Response, error) {
	attempts := c.opt.MaxRetries + 1
	var last error
	for i := 0; i < attempts; i++ {
		if err := ctx.Err(); err != nil {
			if last != nil {
				return Response{}, last
			}
			return Response{}, err
		}
		resp, err := c.next.Complete(ctx, r)
		if err == nil {
			return resp, nil
		}
		last = err
		if ctx.Err() != nil || !retryable(err) || i == attempts-1 {
			return Response{}, err
		}
		if err := sleepCtx(ctx, c.delay(i, err)); err != nil {
			return Response{}, last
		}
	}
	if last == nil {
		return Response{}, ctx.Err()
	}
	return Response{}, last
}

func (c *retryClient) delay(attempt int, err error) time.Duration {
	var se *StatusError
	if errors.As(err, &se) && se.HasRetryAfter {
		d := se.RetryAfter
		if d > maxRetryAfter {
			return maxRetryAfter
		}
		return d
	}
	d := c.opt.BaseDelay
	for i := 0; i < attempt; i++ {
		if d >= c.opt.MaxDelay/2 {
			return c.opt.MaxDelay
		}
		d *= 2
	}
	if d > c.opt.MaxDelay {
		return c.opt.MaxDelay
	}
	return d
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
