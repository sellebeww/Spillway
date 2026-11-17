package ingest

import (
	"slices"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"
)

type flushLog struct {
	mu      sync.Mutex
	batches [][]int
	at      []time.Time
}

func (f *flushLog) flush(b []int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.batches = append(f.batches, slices.Clone(b))
	f.at = append(f.at, time.Now())
}

func (f *flushLog) snapshot() [][]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.batches)
}

func TestBatcherFlushesBySize(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	in := make(chan int)
	var f flushLog
	done := make(chan struct{})
	go func() { defer close(done); runBatcher(in, 4, time.Hour, f.flush) }() // time never triggers
	for i := 0; i < 9; i++ {
		in <- i
	}
	waitFor(t, func() bool { return len(f.snapshot()) == 2 }) // two full batches, one item still pending
	if got := f.snapshot(); !slices.Equal(got[0], []int{0, 1, 2, 3}) || !slices.Equal(got[1], []int{4, 5, 6, 7}) {
		t.Fatalf("batches: %v", got)
	}
	close(in)
	<-done
	if got := f.snapshot(); len(got) != 3 || !slices.Equal(got[2], []int{8}) {
		t.Fatalf("closing must flush the remainder: %v", got)
	}
}

func TestBatcherFlushesByTime(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	in := make(chan int)
	var f flushLog
	done := make(chan struct{})
	go func() { defer close(done); runBatcher(in, 1000, 20*time.Millisecond, f.flush) }()
	start := time.Now()
	in <- 1
	in <- 2
	waitFor(t, func() bool { return len(f.snapshot()) == 1 })
	f.mu.Lock()
	elapsed := f.at[0].Sub(start)
	f.mu.Unlock()
	if elapsed > time.Second || !slices.Equal(f.snapshot()[0], []int{1, 2}) {
		t.Fatalf("a partial batch must flush after the interval: %v %v", elapsed, f.snapshot())
	}
	time.Sleep(60 * time.Millisecond)
	if len(f.snapshot()) != 1 {
		t.Fatal("an empty batch must not be flushed")
	}
	close(in)
	<-done
}

func TestBatcherPreservesOrderAndLosesNothing(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	in := make(chan int, 64)
	var f flushLog
	done := make(chan struct{})
	go func() { defer close(done); runBatcher(in, 7, 5*time.Millisecond, f.flush) }()
	for i := 0; i < 1000; i++ {
		in <- i
	}
	close(in)
	<-done
	var all []int
	for _, b := range f.snapshot() {
		if len(b) > 7 {
			t.Fatalf("batch of %d exceeds the size limit", len(b))
		}
		all = append(all, b...)
	}
	if len(all) != 1000 || !slices.IsSorted(all) {
		t.Fatalf("got %d items, sorted=%v", len(all), slices.IsSorted(all))
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
