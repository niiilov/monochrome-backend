package rank

import (
	"context"
	"sync"
)

// fanout runs fn(i) for every i in [0, n) and waits.
// The LLM client limiter caps how many completions actually run at once.
// fn must only write slots[i]; the caller merges results in index order.
func fanout(ctx context.Context, n int, fn func(context.Context, int)) {
	if n <= 0 {
		return
	}
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			fn(ctx, i)
		}(i)
	}
	wg.Wait()
}
