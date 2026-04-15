package mvcc

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLocalTS_StartsAtOne(t *testing.T) {
	ts := NewLocalTS()
	assert.Equal(t, uint64(1), ts.NextTS())
}

func TestLocalTS_Monotonic(t *testing.T) {
	ts := NewLocalTS()
	var prev uint64
	for i := 0; i < 1000; i++ {
		curr := ts.NextTS()
		assert.Greater(t, curr, prev, "timestamp must be strictly increasing")
		prev = curr
	}
}

func TestLocalTS_ConcurrentSafety(t *testing.T) {
	ts := NewLocalTS()
	const goroutines = 100
	const perGoroutine = 100

	var wg sync.WaitGroup
	results := make(chan uint64, goroutines*perGoroutine)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				results <- ts.NextTS()
			}
		}()
	}
	wg.Wait()
	close(results)

	seen := make(map[uint64]struct{})
	for v := range results {
		_, dup := seen[v]
		assert.False(t, dup, "duplicate timestamp: %d", v)
		seen[v] = struct{}{}
	}
	assert.Equal(t, goroutines*perGoroutine, len(seen), "all timestamps must be unique")
}
