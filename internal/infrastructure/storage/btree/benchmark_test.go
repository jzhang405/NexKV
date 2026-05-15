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

// newLargeBenchmarkBTree creates a BTree with large storage for benchmarks.
// Returns a new tree instance; caller must Close when done.
func newLargeBenchmarkBTree(b *testing.B) (*BTree, *OffheapBTreeStorage) {
	b.Helper()
	storage, err := NewOffheapBTreeStorage(512 * 1024 * 1024) // 512MB
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

// preloadDataSet preloads a BTree with n unique keys, measuring the time.
func preloadDataSet(b *testing.B, tree *BTree, n int) {
	b.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		key := []byte("key-" + strconv.Itoa(i))
		value := []byte("value-" + strconv.Itoa(i))
		if err := tree.Set(ctx, key, value); err != nil {
			b.Fatalf("preload Set failed at %d: %v", i, err)
		}
	}
}

// BenchmarkBTreePreload measures the time to populate a tree of various sizes.
func BenchmarkBTreePreload(b *testing.B) {
	sizes := []int{1000, 5000}
	for _, n := range sizes {
		b.Run(strconv.Itoa(n)+"_keys", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				tree, _ := newLargeBenchmarkBTree(b)
				ctx := context.Background()
				b.StartTimer()
				for j := 0; j < n; j++ {
					key := []byte("key-" + strconv.Itoa(j))
					if err := tree.Set(ctx, key, []byte("val")); err != nil {
						b.Fatalf("Set failed: %v", err)
					}
				}
			}
		})
	}
}

// BenchmarkBTreeGetLargeDataset measures read performance on pre-populated tree (5K keys).
func BenchmarkBTreeGetLargeDataset(b *testing.B) {
	tree, _ := newLargeBenchmarkBTree(b)
	preloadDataSet(b, tree, 5000)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte("key-" + strconv.Itoa(i%5000))
		if _, err := tree.Get(ctx, key); err != nil {
			b.Fatalf("Get failed: %v", err)
		}
	}
}

// BenchmarkBTreeGetParallelLargeDataset measures parallel read on pre-populated tree.
func BenchmarkBTreeGetParallelLargeDataset(b *testing.B) {
	tree, _ := newLargeBenchmarkBTree(b)
	preloadDataSet(b, tree, 5000)
	ctx := context.Background()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := []byte("key-" + strconv.Itoa(i%5000))
			if _, err := tree.Get(ctx, key); err != nil {
				b.Fatalf("Get failed: %v", err)
			}
			i++
		}
	})
}

// BenchmarkBTreeMixedReadWriteLarge measures mixed read/write on pre-populated tree (80R:20W).
func BenchmarkBTreeMixedReadWriteLarge(b *testing.B) {
	tree, _ := newLargeBenchmarkBTree(b)
	preloadDataSet(b, tree, 3000)
	ctx := context.Background()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%5 == 0 {
				// 20% writes — overwrite existing keys
				key := []byte("key-" + strconv.Itoa(i%3000))
				_ = tree.Set(ctx, key, []byte("v"))
			} else {
				// 80% reads
				key := []byte("key-" + strconv.Itoa(i%3000))
				_, _ = tree.Get(ctx, key)
			}
			i++
		}
	})
}

// BenchmarkBTreeSetWithLatency measures preload + read with latency histograms enabled.
// Note: COW page leak prevents sustained pure-write benchmarks;
// uses preload + read pattern to exercise latency metrics.
func BenchmarkBTreeSetWithLatency(b *testing.B) {
	storage, err := NewOffheapBTreeStorage(512 * 1024 * 1024)
	if err != nil {
		b.Fatalf("failed to create storage: %v", err)
	}
	tree, err := NewBTree(storage, WithLatencyMetrics(), WithMetrics(NewBTreeMetrics()))
	if err != nil {
		b.Fatalf("failed to create btree: %v", err)
	}
	b.Cleanup(func() { tree.Close() })

	ctx := context.Background()
	for i := 0; i < 1000; i++ {
		key := []byte("key-" + strconv.Itoa(i))
		if err := tree.Set(ctx, key, []byte("val")); err != nil {
			b.Fatalf("preload Set failed: %v", err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte("key-" + strconv.Itoa(i%1000))
		if _, err := tree.Get(ctx, key); err != nil {
			b.Fatalf("Get failed: %v", err)
		}
	}

	b.StopTimer()
	ml := tree.metricsWithLatency()
	if ml != nil {
		readSnap := ml.ReadLat.Snapshot()
		b.ReportMetric(float64(readSnap.Count), "lat_samples")
		b.ReportMetric(float64(readSnap.AvgUs), "read_avg_us")
		b.ReportMetric(float64(readSnap.P99Us), "read_p99_us")
	}
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
