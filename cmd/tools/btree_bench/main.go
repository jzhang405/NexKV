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

	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/btree"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/checkpoint"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/chunk"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/lob"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/mvcc"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/persist"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/wal"
)

var (
	mmapMB      = flag.Int("mmap", 512, "mmap size in MB")
	ops         = flag.Int("n", 1_000_000, "operations per benchmark")
	threads     = flag.Int("t", 0, "goroutines (0=auto: GOMAXPROCS)")
	warmup      = flag.Int("warmup", 100_000, "warmup ops")
	enableEpoch = flag.Bool("epoch", false, "enable epoch-based page reclamation")
	cpuProfile  = flag.String("cpuprofile", "", "write cpu profile to file")
	only        = flag.String("only", "", "run only tests matching this prefix")
	batchSizes  = flag.String("batch", "64,256,1024", "comma-separated batch sizes for batch benchmarks")
	noPreGen    = flag.Bool("no-pregenerate", false, "use fmt.Sprintf per call (old behavior, GC-heavy)")
	runLOBBench = flag.Bool("lob", false, "run LOB large-object benchmarks")
	lobSize     = flag.Int("lob-size", 4096, "LOB value size in bytes (default 4KB)")

	// Persistence flags
	persistMode  = flag.String("persist", "", "persistence mode: wal (empty=memory-only)")
	walSync      = flag.String("wal-sync", "group-commit", "WAL sync strategy: every-write, group-commit, every-second")
	walDir       = flag.String("wal-dir", "", "WAL/chunk directory (default: os.TempDir)")
	ckptInterval = flag.Int("ckpt-interval", 10000, "checkpoint save interval (ops per save)")

	// keyPool and valPool are pre-generated in main to eliminate fmt.Sprintf GC pressure.
	keyPool [][]byte
	valPool [][]byte
	lobVal []byte // single pre-allocated LOB value (reused across operations)
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

	// Batch benchmarks
	for _, bs := range parseBatchSizes(*batchSizes) {
		batchN := n
		if batchN < bs {
			batchN = bs
		}
		for _, label := range []string{"batch-set", "batch-get"} {
			if *only != "" && !strings.HasPrefix(label, *only) {
				continue
			}
			runBatch(fmt.Sprintf("%s-%d", label, bs), batchN, 1, label == "batch-get", bs, mmapSize)
		}
		for _, conc := range []int{4, 8, 16} {
			for _, label := range []string{"par-batch-set", "par-batch-get"} {
				if *only != "" && !strings.HasPrefix(label, *only) {
					continue
				}
				runBatch(fmt.Sprintf("%s-%d-%d", label, bs, min(t, conc)), batchN, min(t, conc), label == "par-batch-get", bs, mmapSize)
			}
		}
	}

	// === LOB (non-transactional, large value via EncodeValue/DecodeValue) ===
	if *runLOBBench {
		lobScenes := []struct {
			label  string
			n, thr int
			getOnly bool
		}{
			{fmt.Sprintf("lob-put-%dk", *lobSize/1024), n, 1, false},
			{fmt.Sprintf("lob-get-%dk", *lobSize/1024), n, 1, true},
			{fmt.Sprintf("par-lob-put-%dk-8", *lobSize/1024), n, min(t, 8), false},
			{fmt.Sprintf("par-lob-get-%dk-8", *lobSize/1024), n, min(t, 8), true},
		}
		for _, sc := range lobScenes {
			if *only != "" && !strings.HasPrefix(sc.label, *only) {
				continue
			}
			runLOB(sc.label, sc.n, sc.thr, sc.getOnly, mmapSize)
		}
	}

	// Persist WAL benchmarks
	if *persistMode == "wal" {
		syncMode := parseWalSyncMode(*walSync)
		modeLabel := fmt.Sprintf("wal-%s", syncMode)
		walDir := *walDir
		if walDir == "" {
			walDir = os.TempDir() + "/nexkv-bench-wal"
		}
		fmt.Printf("\n--- Mode: persist (%s) ---\n", modeLabel)

		scenes := []struct {
			label   string
			n, thr  int
			getOnly bool
		}{
			{fmt.Sprintf("seq-put-persist-%s", syncMode), n, 1, false},
			{fmt.Sprintf("par-put-persist-%s-8", syncMode), n, min(t, 8), false},
		}
		for _, sc := range scenes {
			if *only != "" && !strings.HasPrefix(sc.label, *only) {
				continue
			}
			runPersistWAL(sc.label, sc.n, sc.thr, sc.getOnly, mmapSize, syncMode, walDir)
		}
	}

	// Persist Checkpoint benchmarks
	if *persistMode == "checkpoint" {
		ckptDir := *walDir
		if ckptDir == "" {
			ckptDir = os.TempDir() + "/nexkv-bench-ckpt"
		}
		fmt.Printf("\n--- Mode: persist (checkpoint, interval=%d) ---\n", *ckptInterval)
		scenes := []struct {
			label   string
			n, thr  int
			getOnly bool
		}{
			{fmt.Sprintf("seq-put-ckpt-%d", *ckptInterval), n, 1, false},
			{fmt.Sprintf("par-put-ckpt-%d-8", *ckptInterval), n, min(t, 8), false},
		}
		for _, sc := range scenes {
			if *only != "" && !strings.HasPrefix(sc.label, *only) {
				continue
			}
			runPersistCheckpoint(sc.label, sc.n, sc.thr, sc.getOnly, mmapSize, ckptDir, int64(*ckptInterval))
		}
	}
}

func parseBatchSizes(s string) []int {
	parts := strings.Split(s, ",")
	sizes := make([]int, 0, len(parts))
	for _, p := range parts {
		var v int
		if _, err := fmt.Sscanf(strings.TrimSpace(p), "%d", &v); err == nil && v > 0 {
			sizes = append(sizes, v)
		}
	}
	if len(sizes) == 0 {
		sizes = []int{64}
	}
	return sizes
}

// runLOB benchmarks non-transactional LOB Get/Set with EncodeValue/DecodeValue.
func runLOB(label string, n, threads int, getOnly bool, mmapSize int) {
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

	// Preload for read tests
	val := lobValOf()
	lobMgr := lob.NewDefaultLOBManager(storage.GetPageManager())
	var lobFileMgr mvcc.LOBFileManager
	if *lobSize > 65536 {
		lobDir, _ := os.MkdirTemp("", "nexkv-lob-bench-*")
		defer os.RemoveAll(lobDir)
		lobFileMgr, _ = lob.NewDefaultLOBFileManager(lobDir)
	}
	if getOnly {
		for i := 0; i < n; i++ {
			encoded, _ := mvcc.EncodeValue(val, uint64(i+1), 0, 0, nil, lobMgr, lobFileMgr)
			_ = tree.Set(ctx, keyOf(i), encoded)
		}
	}

	// Warmup
	totalOps := atomic.Int64{}
	lobLoop(*warmup, threads, tree, &totalOps, getOnly, n, lobMgr, lobFileMgr)
	totalOps.Store(0)

	// Measure
	t0 := time.Now()
	lobLoop(n, threads, tree, &totalOps, getOnly, n, lobMgr, lobFileMgr)
	elapsed := time.Since(t0)

	qps := float64(totalOps.Load()) / elapsed.Seconds()
	rw := "write"
	if getOnly {
		rw = "read"
	}
	fmt.Printf("%-28s t=%-2d op=%-8s lob=%dB ops=%d time=%.3fs qps=%10.0f\n",
		label, threads, rw, len(val), totalOps.Load(), elapsed.Seconds(), qps)

	_ = tree.Close()
}

// lobLoop runs LOB Get/Set operations with EncodeValue/DecodeValue wrapping.
func lobLoop(n, threads int, tree *btree.BTree, ops *atomic.Int64, getOnly bool, maxKey int, lobMgr mvcc.LOBManager, lobFileMgr mvcc.LOBFileManager) {
	ctx := context.Background()
	val := lobValOf()

	if threads == 1 {
		if getOnly {
			for i := 0; i < n; i++ {
				raw, err := tree.Get(ctx, keyOf(i%maxKey))
				if err == nil {
					_, _ = mvcc.DecodeValue(raw, lobMgr, lobFileMgr)
				}
				ops.Add(1)
			}
		} else {
			for i := 0; i < n; i++ {
				encoded, _ := mvcc.EncodeValue(val, uint64(i+1), 0, 0, nil, lobMgr, lobFileMgr)
				_ = tree.Set(ctx, keyOf(i), encoded)
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
					raw, err := tree.Get(ctx, keyOf(i%maxKey))
					if err == nil {
						_, _ = mvcc.DecodeValue(raw, lobMgr, lobFileMgr)
					}
					ops.Add(1)
				}
			} else {
				for i := start; i < end; i++ {
					encoded, _ := mvcc.EncodeValue(val, uint64(i+1), 0, 0, nil, lobMgr, lobFileMgr)
					_ = tree.Set(ctx, keyOf(i), encoded)
					ops.Add(1)
				}
			}
		}(start, end)
	}
	wg.Wait()
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

func lobValOf() []byte {
	if lobVal == nil {
		lobVal = make([]byte, *lobSize)
		for i := range lobVal {
			lobVal[i] = byte(i % 256)
		}
	}
	return lobVal
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

// --- Batch benchmarks ---

func runBatch(label string, n, threads int, getOnly bool, batchSize, mmapSize int) {
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

	// Preload for batch-get tests.
	if getOnly {
		for i := 0; i < n; i++ {
			_ = tree.Set(ctx, keyOf(i), valOf(i))
		}
	}

	// Warmup
	var totalOps atomic.Int64
	batchLoop(*warmup, threads, tree, &totalOps, getOnly, batchSize, n)
	totalOps.Store(0)

	// Measure
	t0 := time.Now()
	batchLoop(n, threads, tree, &totalOps, getOnly, batchSize, n)
	elapsed := time.Since(t0)

	qps := float64(totalOps.Load()) / elapsed.Seconds()
	rw := "write"
	if getOnly {
		rw = "read"
	}
	fmt.Printf("%-28s t=%-2d op=%-8s batch=%-5d ops=%d time=%.3fs qps=%10.0f\n",
		label, threads, rw, batchSize, totalOps.Load(), elapsed.Seconds(), qps)

	_ = tree.Close()
}

func batchLoop(n, threads int, tree *btree.BTree, ops *atomic.Int64, getOnly bool, batchSize, maxKey int) {
	ctx := context.Background()
	numBatches := (n + batchSize - 1) / batchSize

	execBatch := func(b int) {
		start := b * batchSize
		end := min(start+batchSize, n)
		if getOnly {
			keys := make([][]byte, end-start)
			for i := range keys {
				keys[i] = keyOf((start + i) % maxKey)
			}
			_, _ = tree.GetBatch(ctx, keys)
		} else {
			pairs := make([]service.KVPair, end-start)
			for i := range pairs {
				pairs[i] = service.KVPair{Key: keyOf(start + i), Value: valOf(start + i)}
			}
			_ = tree.SetBatch(ctx, pairs)
		}
		ops.Add(int64(end - start))
	}

	if threads <= 1 {
		for b := 0; b < numBatches; b++ {
			execBatch(b)
		}
		return
	}

	var wg sync.WaitGroup
	per := numBatches / threads
	for t := 0; t < threads; t++ {
		bStart := t * per
		bEnd := bStart + per
		if t == threads-1 {
			bEnd = numBatches
		}
		wg.Add(1)
		go func(bStart, bEnd int) {
			defer wg.Done()
			for b := bStart; b < bEnd; b++ {
				execBatch(b)
			}
		}(bStart, bEnd)
	}
	wg.Wait()
}

// --- Persist WAL benchmark ---

func parseWalSyncMode(s string) persist.WalSyncMode {
	switch s {
	case "every-write":
		return persist.WalSyncEveryWrite
	case "every-second":
		return persist.WalSyncEverySecond
	default:
		return persist.WalSyncGroupCommit
	}
}

func runPersistWAL(label string, n, threads int, getOnly bool, mmapSize int, syncMode persist.WalSyncMode, walDir string) {
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

	w, err := wal.NewDiskWAL(&wal.WALConfig{
		Dir:         walDir,
		SegmentSize: 64 * 1024 * 1024,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "create wal: %v\n", err)
		return
	}

	kv := persist.NewPersistWAL(tree, w, syncMode)
	ctx := context.Background()

	// Preload for read tests.
	if getOnly {
		for i := 0; i < n; i++ {
			_ = kv.Set(ctx, keyOf(i), valOf(i))
		}
	}

	// Warmup.
	var totalOps atomic.Int64
	for i := 0; i < *warmup; i++ {
		if getOnly {
			_, _ = kv.Get(ctx, keyOf(i%n))
		} else {
			_ = kv.Set(ctx, keyOf(i), valOf(i))
		}
		totalOps.Add(1)
	}
	totalOps.Store(0)

	// Measure.
	t0 := time.Now()
	if threads <= 1 {
		if getOnly {
			for i := 0; i < n; i++ {
				_, _ = kv.Get(ctx, keyOf(i%n))
				totalOps.Add(1)
			}
		} else {
			for i := 0; i < n; i++ {
				_ = kv.Set(ctx, keyOf(i), valOf(i))
				totalOps.Add(1)
			}
		}
	} else {
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
						_, _ = kv.Get(ctx, keyOf(i%n))
						totalOps.Add(1)
					}
				} else {
					for i := start; i < end; i++ {
						_ = kv.Set(ctx, keyOf(i), valOf(i))
						totalOps.Add(1)
					}
				}
			}(start, end)
		}
		wg.Wait()
	}
	elapsed := time.Since(t0)

	qps := float64(totalOps.Load()) / elapsed.Seconds()
	rw := "write"
	if getOnly {
		rw = "read"
	}
	modeLabel := "persist"
	fmt.Printf("%-28s mode=%-7s t=%-2d op=%-6s ops=%d time=%.3fs qps=%10.0f\n",
		label, modeLabel, threads, rw, totalOps.Load(), elapsed.Seconds(), qps)

	_ = kv.Close()
	_ = tree.Close()
}

// --- Persist Checkpoint benchmark ---

func runPersistCheckpoint(label string, n, threads int, getOnly bool, mmapSize int, ckptDir string, ckptInterval int64) {
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

	cm, err := chunk.NewDiskChunkManager(ckptDir, 256*1024*1024)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create chunk mgr: %v\n", err)
		return
	}
	tree.SetChunkManager(cm, &chunk.PageSerializer{})

	kv := persist.NewPersistCheckpoint(
		tree,
		func() checkpoint.PageRef { return tree.RootPage() },
		tree.EnumeratePages,
		cm, persist.WithCkptInterval(ckptInterval),
	)
	ctx := context.Background()

	// Preload for read tests.
	if getOnly {
		for i := 0; i < n; i++ {
			_ = kv.Set(ctx, keyOf(i), valOf(i))
		}
	}

	// Warmup.
	var totalOps atomic.Int64
	for i := 0; i < *warmup; i++ {
		if getOnly {
			_, _ = kv.Get(ctx, keyOf(i%n))
		} else {
			_ = kv.Set(ctx, keyOf(i), valOf(i))
		}
		totalOps.Add(1)
	}
	totalOps.Store(0)

	// Measure.
	t0 := time.Now()
	if threads <= 1 {
		if getOnly {
			for i := 0; i < n; i++ {
				_, _ = kv.Get(ctx, keyOf(i%n))
				totalOps.Add(1)
			}
		} else {
			for i := 0; i < n; i++ {
				_ = kv.Set(ctx, keyOf(i), valOf(i))
				totalOps.Add(1)
			}
		}
	} else {
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
						_, _ = kv.Get(ctx, keyOf(i%n))
						totalOps.Add(1)
					}
				} else {
					for i := start; i < end; i++ {
						_ = kv.Set(ctx, keyOf(i), valOf(i))
						totalOps.Add(1)
					}
				}
			}(start, end)
		}
		wg.Wait()
	}
	elapsed := time.Since(t0)

	qps := float64(totalOps.Load()) / elapsed.Seconds()
	rw := "write"
	if getOnly {
		rw = "read"
	}
	modeLabel := "persist-ckpt"
	fmt.Printf("%-28s mode=%-11s t=%-2d op=%-6s ops=%d time=%.3fs qps=%10.0f\n",
		label, modeLabel, threads, rw, totalOps.Load(), elapsed.Seconds(), qps)

	_ = kv.Close()
	_ = tree.Close()
}
