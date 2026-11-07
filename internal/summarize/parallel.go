package summarize

import (
	"context"
	"sync"
)

func parallel(ctx context.Context, n int, fn func(context.Context, int) error) error {
	if n == 0 {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sem := make(chan struct{}, maxParallel)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var first error
	setErr := func(err error) {
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
				setErr(ctx.Err())
				return
			case sem <- struct{}{}:
			}
			defer func() { <-sem }()
			if err := fn(ctx, i); err != nil {
				setErr(err)
				cancel()
			}
		}(i)
	}
	wg.Wait()
	return first
}
