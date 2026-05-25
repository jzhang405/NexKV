// Command btree_bench measures NexKV BTree KV read/write throughput.
//
// Usage: go run ./cmd/tools/btree_bench [flags]
package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/infrastructure/storage/btree"
)

var (
	mmapMB      = flag.Int("mmap", 512, "mmap size in MB")
	ops         = flag.Int("n", 1_000_000, "operations per benchmark")
	threads     = flag.Int("t", 0, "goroutines (0=auto: GOMAXPROCS)")
	warmup      = flag.Int("warmup", 100_000, "warmup ops")
	enableEpoch = flag.Bool("epoch", false, "enable epoch-based page reclamation")
	cpuProfile  = flag.String("cpuprofile", "", "write cpu profile to file")
	only        = flag.String("only", "", "run only tests matching this prefix")
	noPreGen    = flag.Bool("no-pregenerate", false, "use fmt.Sprintf per call (old behavior, GC-heavy)")

	// keyPool and valPool are pre-generated in main to eliminate fmt.Sprintf GC pressure.
	keyPool [][]byte
	valPool [][]byte
)

func main() {
	flag.Parse()

	if *cpuProfile != "" {
		f, err := os.Create(*cpuProfile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "create cpu profile: %v\n", err)
			os.Exit(1)
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	mmapSize := *mmapMB * 1024 * 1024
	t := *threads
	if t <= 0 {
		t = runtime.GOMAXPROCS(0)
	}
	n := *ops

	// Pre-generate key/value pools to eliminate fmt.Sprintf GC pressure.
	// Each fmt.Sprintf call allocates a new []byte; with 100K+ ops this creates
	// massive GC pressure (~10MB/benchmark) that obscures real BTree performance.
	// Pre-generating once is closer to production: keys/values arrive pre-serialized
	// from the RPC layer, not manufactured per-operation.
	poolSize := max(n, *warmup) + 1
	if !*noPreGen {
		keyPool = make([][]byte, poolSize)
		valPool = make([][]byte, poolSize)
		for i := 0; i < poolSize; i++ {
			keyPool[i] = []byte(fmt.Sprintf("key-%010d", i))
			valPool[i] = []byte(fmt.Sprintf("value-%010d", i))
		}
	}

	epochLabel := "off"
	if *enableEpoch {
		epochLabel = "on"
	}
	poolLabel := "pre-gen"
	if *noPreGen {
		poolLabel = "fmt-per-call"
	}
	fmt.Printf("=== NexKV BTree KV Benchmark ===\n")
	fmt.Printf("ops=%d  goroutines=%d  mmap=%dMB  pageSize=4KB  epoch=%s  keygen=%s\n\n", n, t, *mmapMB, epochLabel, poolLabel)

	tests := []struct {
		label   string
		n, thr  int
		getOnly bool
		rr      float64
	}{
		{"seq-put", n, 1, false, 0},
		{"seq-get", n, 1, true, 0},
		{"seq-put-get", n, 1, false, 0.5},
		{"par-put-4", n, min(t, 4), false, 0},
		{"par-put-8", n, min(t, 8), false, 0},
		{"par-put-16", n, min(t, 16), false, 0},
		{"par-put-32", n, min(t, 32), false, 0},
		{"par-get-4", n, min(t, 4), true, 0},
		{"par-get-8", n, min(t, 8), true, 0},
		{"par-get-16", n, min(t, 16), true, 0},
		{"mixed-8-r80", n, min(t, 8), false, 0.8},
		{"mixed-16-r80", n, min(t, 16), false, 0.8},
	}
	for _, tc := range tests {
		if *only != "" && !strings.HasPrefix(tc.label, *only) {
			continue
		}
		if tc.rr > 0 {
			run(tc.label, tc.n, tc.thr, tc.getOnly, mmapSize, tc.rr)
		} else {
			run(tc.label, tc.n, tc.thr, tc.getOnly, mmapSize)
		}
	}
}

func run(label string, n, threads int, getOnly bool, mmapSize int, readRatios ...float64) {
	readRatio := 0.0
	if len(readRatios) > 0 {
		readRatio = readRatios[0]
	}

	storage, err := btree.NewOffheapBTreeStorage(mmapSize)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create storage: %v\n", err)
		return
	}
	opts := []btree.BTreeOption{}
	if *enableEpoch {
		opts = append(opts, btree.WithEpoch())
	}
	tree, err := btree.NewBTree(storage, opts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create btree: %v\n", err)
		return
	}
	ctx := context.Background()

	// preload for read/mixed tests
	if getOnly || readRatio > 0 {
		for i := 0; i < n; i++ {
			_ = tree.Set(ctx, keyOf(i), valOf(i))
		}
	}

	// warmup
	var totalOps atomic.Int64
	if readRatio > 0 {
		mixedLoop(*warmup, threads, tree, &totalOps, readRatio, n)
	} else {
		loop(*warmup, threads, tree, &totalOps, getOnly, n)
	}
	totalOps.Store(0)

	// measure
	t0 := time.Now()
	if readRatio > 0 {
		mixedLoop(n, threads, tree, &totalOps, readRatio, n)
	} else {
		loop(n, threads, tree, &totalOps, getOnly, n)
	}
	elapsed := time.Since(t0)

	qps := float64(totalOps.Load()) / elapsed.Seconds()
	rw := "write"
	if getOnly {
		rw = "read"
	} else if readRatio > 0 {
		rw = fmt.Sprintf("rw(%.0f%%)", readRatio*100)
	}
	fmt.Printf("%-20s t=%-2d op=%-8s ops=%d time=%.3fs qps=%10.0f\n",
		label, threads, rw, totalOps.Load(), elapsed.Seconds(), qps)

	_ = tree.Close()
}

func loop(n, threads int, tree *btree.BTree, ops *atomic.Int64, getOnly bool, maxKey int) {
	ctx := context.Background()
	if threads == 1 {
		if getOnly {
			for i := 0; i < n; i++ {
				_, _ = tree.Get(ctx, keyOf(i%maxKey))
				ops.Add(1)
			}
		} else {
			for i := 0; i < n; i++ {
				_ = tree.Set(ctx, keyOf(i), valOf(i))
				ops.Add(1)
			}
		}
		return
	}

	var wg sync.WaitGroup
	per := n / threads
	for t := 0; t < threads; t++ {
		start := t * per
		end := start + per
		if t == threads-1 {
			end = n
		}
		wg.Add(1)
		go func(start, end int) {
			defer wg.Done()
			if getOnly {
				for i := start; i < end; i++ {
					_, _ = tree.Get(ctx, keyOf(i%maxKey))
					ops.Add(1)
				}
			} else {
				for i := start; i < end; i++ {
					_ = tree.Set(ctx, keyOf(i), valOf(i))
					ops.Add(1)
				}
			}
		}(start, end)
	}
	wg.Wait()
}

func mixedLoop(n, threads int, tree *btree.BTree, ops *atomic.Int64, readRatio float64, maxKey int) {
	ctx := context.Background()
	var wg sync.WaitGroup
	per := n / threads
	for t := 0; t < threads; t++ {
		start := t * per
		end := start + per
		if t == threads-1 {
			end = n
		}
		wg.Add(1)
		go func(start, end int) {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(uint64(start), uint64(time.Now().UnixNano())))
			for i := start; i < end; i++ {
				if rng.Float64() < readRatio {
					_, _ = tree.Get(ctx, keyOf(i%maxKey))
				} else {
					_ = tree.Set(ctx, keyOf(i), valOf(i))
				}
				ops.Add(1)
			}
		}(start, end)
	}
	wg.Wait()
}

func keyOf(i int) []byte {
	if !*noPreGen && i < len(keyPool) {
		return keyPool[i]
	}
	return []byte(fmt.Sprintf("key-%010d", i))
}

func valOf(i int) []byte {
	if !*noPreGen && i < len(valPool) {
		return valPool[i]
	}
	return []byte(fmt.Sprintf("value-%010d", i))
}
