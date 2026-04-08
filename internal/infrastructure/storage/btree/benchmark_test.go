package btree

import (
	"context"
	"strconv"
	"sync"
	"testing"
)

// newBenchmarkBTree creates a BTree for benchmarking with larger storage.
func newBenchmarkBTree(b *testing.B) (*BTree, *OffheapBTreeStorage) {
	b.Helper()
	// Use larger storage for COW workloads (each update creates a new page)
	storage, err := NewOffheapBTreeStorage(512 * 1024 * 1024) // 512MB for benchmarks
	if err != nil {
		b.Fatalf("failed to create storage: %v", err)
	}
	tree, err := NewBTree(storage)
	if err != nil {
		b.Fatalf("failed to create btree: %v", err)
	}
	b.Cleanup(func() { tree.Close() })
	return tree, storage
}

// newBenchmarkBTreeWithMetrics creates a BTree with metrics collection enabled.
func newBenchmarkBTreeWithMetrics(b *testing.B) (*BTree, *BTreeMetrics) {
	b.Helper()
	// Use larger storage for COW workloads
	storage, err := NewOffheapBTreeStorage(512 * 1024 * 1024) // 512MB for benchmarks
	if err != nil {
		b.Fatalf("failed to create storage: %v", err)
	}
	metrics := &BTreeMetrics{}
	tree, err := NewBTreeWithMetrics(storage, metrics)
	if err != nil {
		b.Fatalf("failed to create btree: %v", err)
	}
	b.Cleanup(func() { tree.Close() })
	return tree, metrics
}

// BenchmarkBTreeSequentialSet measures sequential write performance.
// Note: Limited to 100 unique keys to avoid triggering split (Phase 5 doesn't support split yet).
func BenchmarkBTreeSequentialSet(b *testing.B) {
	tree, _ := newBenchmarkBTree(b)
	ctx := context.Background()

	const maxKeys = 100 // Limited to avoid triggering split
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte("key-" + strconv.Itoa(i%maxKeys))
		value := []byte("value-" + strconv.Itoa(i%maxKeys))
		if err := tree.Set(ctx, key, value); err != nil {
			b.Fatalf("Set failed: %v", err)
		}
	}
}

// BenchmarkBTreeSetParallel measures parallel write performance.
// Note: Limited to 100 unique keys to avoid triggering split.
func BenchmarkBTreeSetParallel(b *testing.B) {
	tree, _ := newBenchmarkBTree(b)
	ctx := context.Background()

	const maxKeys = 100 // Limited to avoid triggering split
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := []byte("key-" + strconv.Itoa(i%maxKeys))
			value := []byte("value-" + strconv.Itoa(i%maxKeys))
			if err := tree.Set(ctx, key, value); err != nil {
				b.Fatalf("Set failed: %v", err)
			}
			i++
		}
	})
}

// BenchmarkBTreeGetSequential measures sequential read performance.
func BenchmarkBTreeGetSequential(b *testing.B) {
	tree, _ := newBenchmarkBTree(b)
	ctx := context.Background()

	// Pre-populate with 100 keys
	const maxKeys = 100
	for i := 0; i < maxKeys; i++ {
		key := []byte("key-" + strconv.Itoa(i))
		value := []byte("value-" + strconv.Itoa(i))
		if err := tree.Set(ctx, key, value); err != nil {
			b.Fatalf("Setup Set failed: %v", err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte("key-" + strconv.Itoa(i%maxKeys))
		_, err := tree.Get(ctx, key)
		if err != nil {
			b.Fatalf("Get failed: %v", err)
		}
	}
}

// BenchmarkBTreeGetParallel measures parallel read performance.
func BenchmarkBTreeGetParallel(b *testing.B) {
	tree, _ := newBenchmarkBTree(b)
	ctx := context.Background()

	// Pre-populate with 100 keys
	const maxKeys = 100
	for i := 0; i < maxKeys; i++ {
		key := []byte("key-" + strconv.Itoa(i))
		value := []byte("value-" + strconv.Itoa(i))
		if err := tree.Set(ctx, key, value); err != nil {
			b.Fatalf("Setup Set failed: %v", err)
		}
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := []byte("key-" + strconv.Itoa(i%maxKeys))
			_, err := tree.Get(ctx, key)
			if err != nil {
				b.Fatalf("Get failed: %v", err)
			}
			i++
		}
	})
}

// BenchmarkBTreeMixedReadWrite measures mixed read/write workload.
// 80% reads, 20% writes.
func BenchmarkBTreeMixedReadWrite(b *testing.B) {
	tree, _ := newBenchmarkBTree(b)
	ctx := context.Background()

	// Pre-populate with 100 keys
	const maxKeys = 100
	for i := 0; i < maxKeys; i++ {
		key := []byte("key-" + strconv.Itoa(i))
		value := []byte("value-" + strconv.Itoa(i))
		if err := tree.Set(ctx, key, value); err != nil {
			b.Fatalf("Setup Set failed: %v", err)
		}
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := []byte("key-" + strconv.Itoa(i%maxKeys))
			// 80% reads, 20% writes
			if i%5 == 0 {
				value := []byte("value-" + strconv.Itoa(i%maxKeys))
				if err := tree.Set(ctx, key, value); err != nil {
					b.Fatalf("Set failed: %v", err)
				}
			} else {
				_, err := tree.Get(ctx, key)
				if err != nil {
					b.Fatalf("Get failed: %v", err)
				}
			}
			i++
		}
	})
}

// BenchmarkBTreeMetricsCollection measures the overhead of metrics collection.
func BenchmarkBTreeMetricsCollection(b *testing.B) {
	b.Run("without-metrics", func(b *testing.B) {
		tree, _ := newBenchmarkBTree(b)
		ctx := context.Background()
		const maxKeys = 100

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			key := []byte("key-" + strconv.Itoa(i%maxKeys))
			value := []byte("value-" + strconv.Itoa(i%maxKeys))
			_ = tree.Set(ctx, key, value)
		}
	})

	b.Run("with-metrics", func(b *testing.B) {
		tree, metrics := newBenchmarkBTreeWithMetrics(b)
		ctx := context.Background()
		const maxKeys = 100

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			key := []byte("key-" + strconv.Itoa(i%maxKeys))
			value := []byte("value-" + strconv.Itoa(i%maxKeys))
			_ = tree.Set(ctx, key, value)
		}

		// Report metrics
		snapshot := metrics.Snapshot()
		b.ReportMetric(float64(snapshot.CASRetryCount)/float64(b.N), "cas_retries/op")
	})
}

// BenchmarkBTreeConcurrentContention measures performance under high CAS contention.
func BenchmarkBTreeConcurrentContention(b *testing.B) {
	tree, metrics := newBenchmarkBTreeWithMetrics(b)
	ctx := context.Background()

	const maxKeys = 10 // Small key set to maximize contention
	var wg sync.WaitGroup

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			key := []byte("key-" + strconv.Itoa(idx%maxKeys))
			value := []byte("value-" + strconv.Itoa(idx%maxKeys))
			_ = tree.Set(ctx, key, value)
		}(i)
	}
	wg.Wait()

	// Report contention metrics
	snapshot := metrics.Snapshot()
	b.ReportMetric(float64(snapshot.CASRetryCount)/float64(b.N), "cas_retries/op")
}

// newProfileBTree creates a BTree with large storage for profiling benchmarks.
func newProfileBTree(b *testing.B) *BTree {
	b.Helper()
	storage, err := NewOffheapBTreeStorage(4 * 1024 * 1024 * 1024)
	if err != nil {
		b.Fatalf("failed to create storage: %v", err)
	}
	tree, err := NewBTree(storage)
	if err != nil {
		b.Fatalf("failed to create btree: %v", err)
	}
	b.Cleanup(func() { tree.Close() })
	return tree
}

// BenchmarkProfileGetParallel measures parallel read performance with pre-populated data.
// Use with: go test -bench=BenchmarkProfileGetParallel -cpuprofile=cpu.prof -memprofile=mem.prof
func BenchmarkProfileGetParallel(b *testing.B) {
	tree := newProfileBTree(b)
	ctx := context.Background()

	// Pre-populate with 10000 keys
	const maxKeys = 10000
	for i := range maxKeys {
		key := []byte("key-" + strconv.Itoa(i))
		value := []byte("value-" + strconv.Itoa(i))
		if err := tree.Set(ctx, key, value); err != nil {
			b.Fatalf("Setup Set failed: %v", err)
		}
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := []byte("key-" + strconv.Itoa(i%maxKeys))
			_, err := tree.Get(ctx, key)
			if err != nil {
				b.Fatalf("Get failed: %v", err)
			}
			i++
		}
	})
}

// BenchmarkProfileSetSequential measures sequential write performance with metrics.
func BenchmarkProfileSetSequential(b *testing.B) {
	storage, err := NewOffheapBTreeStorage(4 * 1024 * 1024 * 1024)
	if err != nil {
		b.Fatalf("failed to create storage: %v", err)
	}
	metrics := &BTreeMetrics{}
	tree, err := NewBTreeWithMetrics(storage, metrics)
	if err != nil {
		b.Fatalf("failed to create btree: %v", err)
	}
	b.Cleanup(func() { tree.Close() })

	ctx := context.Background()
	const maxKeys = 10000

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte("key-" + strconv.Itoa(i%maxKeys))
		value := []byte("value-" + strconv.Itoa(i%maxKeys))
		if err := tree.Set(ctx, key, value); err != nil {
			b.Fatalf("Set failed: %v", err)
		}
	}

	snapshot := metrics.Snapshot()
	b.ReportMetric(float64(snapshot.CASRetryCount)/float64(b.N), "cas_retries/op")
	b.ReportMetric(float64(snapshot.SplitCount), "splits")
}
