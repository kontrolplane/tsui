package tsui

import (
	"errors"
	"sync"
)

// bulkWorkers bounds the concurrent requests of a bulk operation.
const bulkWorkers = 16

// forEach runs fn for every item with bounded parallelism. It returns the items fn succeeded for,
// in their original order, and the joined errors of the ones it failed for.
func forEach[T any](items []T, fn func(T) error) ([]T, error) {
	errs := make([]error, len(items))
	next := make(chan int)
	var wg sync.WaitGroup
	for range min(bulkWorkers, len(items)) {
		wg.Go(func() {
			for i := range next {
				errs[i] = fn(items[i])
			}
		})
	}
	for i := range items {
		next <- i
	}
	close(next)
	wg.Wait()

	var done []T
	for i, item := range items {
		if errs[i] == nil {
			done = append(done, item)
		}
	}
	return done, errors.Join(errs...)
}
