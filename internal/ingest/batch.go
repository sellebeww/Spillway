// Copyright (c) 2026 Vassel Goleyu. MIT License.

package ingest

import "time"

// runBatcher collects items from in and calls flush with batches of at most size items,
// or earlier when interval has passed since the last flush and something is waiting.
// When in is closed the remaining items are flushed and runBatcher returns, so closing the
// channel drains without losing anything. flush is only ever called from this goroutine.
func runBatcher[T any](in <-chan T, size int, interval time.Duration, flush func([]T)) {
	batch := make([]T, 0, size)
	tick := time.NewTicker(interval)
	defer tick.Stop()
	emit := func() {
		if len(batch) == 0 {
			return
		}
		flush(batch)
		batch = make([]T, 0, size)
	}
	for {
		select {
		case it, ok := <-in:
			if !ok {
				emit()
				return
			}
			batch = append(batch, it)
			if len(batch) >= size {
				emit()
			}
		case <-tick.C:
			emit()
		}
	}
}
