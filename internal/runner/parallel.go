package runner

import (
	"context"
	"sync"
)

// runParallel runs fn(i) for i in [0, n). At most limit calls run at once.
// A failure of one index does not cancel the others. The returned error is
// the first one fn reported. Context cancellation is returned as well.
func runParallel(ctx context.Context, n, limit int, fn func(context.Context, int) error) error {
	if n <= 0 {
		return nil
	}
	if limit <= 0 {
		limit = 1
	}
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var first error
	keep := func(err error) {
		if err == nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if first == nil {
			first = err
		}
	}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case <-ctx.Done():
				keep(ctx.Err())
				return
			case sem <- struct{}{}:
			}
			defer func() { <-sem }()
			if err := ctx.Err(); err != nil {
				keep(err)
				return
			}
			keep(fn(ctx, i))
		}(i)
	}
	wg.Wait()
	return first
}
