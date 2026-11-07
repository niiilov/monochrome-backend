package llm

import "context"

type limiter struct {
	next Client
	sem  chan struct{}
}

// WithLimiter bounds the number of completions running at once.
// n <= 0 is treated as 1. The caller context releases the wait.
func WithLimiter(next Client, n int) Client {
	if n <= 0 {
		n = 1
	}
	return &limiter{next: next, sem: make(chan struct{}, n)}
}

func (l *limiter) Complete(ctx context.Context, r Request) (Response, error) {
	select {
	case l.sem <- struct{}{}:
		defer func() { <-l.sem }()
	case <-ctx.Done():
		return Response{}, ctx.Err()
	}
	return l.next.Complete(ctx, r)
}
