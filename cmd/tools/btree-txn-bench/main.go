// Command btree-txn-bench measures NexKV MVCC transaction throughput.
//
// Usage: go run ./cmd/tools/btree-txn-bench [flags]
package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"runtime/pprof"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/btree"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/mvcc"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/persist"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/wal"
)

var (
	nFlag      = flag.Int("n", 100_000, "operations per benchmark")
	warmupFlag = flag.Int("warmup", 10_000, "warmup ops")
	mmapMB     = flag.Int("mmap", 512, "mmap size in MB")
	only       = flag.String("only", "", "run only tests matching this prefix")
	noPreGen   = flag.Bool("no-pregenerate", false, "use fmt.Sprintf per call (GC-heavy)")

	// Transaction flags
	txnBatch    = flag.Int("txn-batch", 0, "ops per txn (0=run all batch sizes)")
	runWrite    = flag.Bool("write", true, "run pure-write txn benchmarks")
	runRead     = flag.Bool("read", true, "run read-only txn benchmarks")
	runLOB      = flag.Bool("lob", false, "run LOB large-object benchmarks")
	lobSize     = flag.Int("lob-size", 4096, "LOB value size in bytes (default 4KB)")
	runMixed    = flag.Bool("mixed", true, "run mixed r/w txn benchmarks")
	runRollback = flag.Bool("rollback", false, "run rollback benchmarks")

	// Persist flags
	persistMode = flag.String("persist", "", "persist mode: wal (empty=memory-only)")
	walSyncStr  = flag.String("wal-sync", "group-commit", "WAL sync: every-write, group-commit, every-second")
	walDirFlag  = flag.String("wal-dir", "", "WAL dir (default: os.TempDir)")

	// Profile flags
	cpuProfile = flag.String("cpuprofile", "", "write cpu profile to file")
	memProfile = flag.String("memprofile", "", "write memory profile to file")

	keyPool [][]byte
	valPool [][]byte
)

func main() {
	flag.Parse()

	// CPU profiling: start in bench() AFTER preload+warmup to exclude setup noise.
	// See bench() for actual StartCPUProfile call.

	mmapSize := *mmapMB * 1024 * 1024
	n := *nFlag
	poolSize := max(n, *warmupFlag) + 1
	if !*noPreGen {
		keyPool = make([][]byte, poolSize)
		valPool = make([][]byte, poolSize)
		for i := 0; i < poolSize; i++ {
			keyPool[i] = []byte(fmt.Sprintf("key-%010d", i))
			valPool[i] = []byte(fmt.Sprintf("value-%010d", i))
		}
	}

	modeLabel := "mem"
	if *persistMode == "wal" {
		modeLabel = "wal-" + *walSyncStr
	}
	fmt.Printf("=== NexKV Transaction KV Benchmark ===\n")
	fmt.Printf("ops=%d  mode=%s  mmap=%dMB  pageSize=4KB\n\n", n, modeLabel, *mmapMB)

	batches := []int{1, 10, 100, 1000, 10000}
	if *txnBatch > 0 {
		batches = []int{*txnBatch}
	}

	ctx := context.Background()

	// === Pure write ===
	if *runWrite {
		for _, batch := range batches {
			label := fmt.Sprintf("txn-put-%d", batch)
			if *only != "" && !strings.HasPrefix(label, *only) {
				continue
			}
			bench(label, n, batch, "write", 0, mmapSize, ctx)
		}
	}

	// === Read-only (preload first) ===
	if *runRead {
		// Preload once for all read benchmarks
		{
			storage, _ := btree.NewOffheapBTreeStorage(mmapSize)
			tree, _ := btree.NewBTree(storage, btree.WithTSGenerator(mvcc.NewLocalTS()))
			for i := 0; i < n; i++ {
				_ = tree.Set(ctx, keyOf(i), valOf(i))
			}
			_ = tree.Close()
		}
		for _, batch := range []int{1, 10, 100} {
			label := fmt.Sprintf("txn-get-%d", batch)
			if *only != "" && !strings.HasPrefix(label, *only) {
				continue
			}
			bench(label, n, batch, "read", 0, mmapSize, ctx)
		}
	}
	for _, batch := range []int{10, 100} {
		label := fmt.Sprintf("txn-get-batch-%d", batch)
		if *only != "" && !strings.HasPrefix(label, *only) {
			continue
		}
		bench(label, n, batch, "readbatch", 0, mmapSize, ctx)
	}

	// === Mixed ===
	if *runMixed {
		for _, batch := range batches {
			for _, ratio := range []float64{0.2, 0.5, 0.8} {
				label := fmt.Sprintf("txn-rw-%.0f-%d", ratio*100, batch)
				if *only != "" && !strings.HasPrefix(label, *only) {
					continue
				}
				if batch > 100 && ratio < 0.5 {
					continue
				} // skip heavy write+large batch combos
				bench(label, n, batch, "mixed", ratio, mmapSize, ctx)
			}
		}
	}

	// === Rollback ===
	if *runRollback {
		for _, batch := range []int{1, 100} {
			label := fmt.Sprintf("txn-rollback-%d", batch)
			if *only != "" && !strings.HasPrefix(label, *only) {
				continue
			}
			bench(label, n, batch, "rollback", 0, mmapSize, ctx)
		}
	}

	// === LOB ===
	if *runLOB {
		for _, batch := range []int{1, 10} {
			nLOB := n
			if *lobSize >= 65536 {
				nLOB = n / 10 // fewer ops for large LOBs
			}
			label := fmt.Sprintf("txn-put-lob-%dk-%d", *lobSize/1024, batch)
			if *only != "" && !strings.HasPrefix(label, *only) {
				continue
			}
			benchLOB(label, nLOB, batch, "write", mmapSize, ctx)
		}
		for _, batch := range []int{1, 10} {
			nLOB := n
			if *lobSize >= 65536 {
				nLOB = n / 10
			}
			label := fmt.Sprintf("txn-get-lob-%dk-%d", *lobSize/1024, batch)
			if *only != "" && !strings.HasPrefix(label, *only) {
				continue
			}
			benchLOB(label, nLOB, batch, "read", mmapSize, ctx)
		}
	}

	// Memory profiling
	if *memProfile != "" {
		f, err := os.Create(*memProfile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "create mem profile: %v\n", err)
			return
		}
		defer f.Close()
		if err := pprof.WriteHeapProfile(f); err != nil {
			fmt.Fprintf(os.Stderr, "write mem profile: %v\n", err)
		}
	}
}

func bench(label string, n, batch int, mode string, readRatio float64, mmapSize int, ctx context.Context) {
	storage, err := btree.NewOffheapBTreeStorage(mmapSize)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create storage: %v\n", err)
		return
	}

	opts := []btree.BTreeOption{btree.WithTSGenerator(mvcc.NewLocalTS())}
	tree, err := btree.NewBTree(storage, opts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create btree: %v\n", err)
		return
	}

	// WAL persistence wrapper
	var kv *persist.PersistWAL
	if *persistMode == "wal" {
		dir := *walDirFlag
		if dir == "" {
			dir = os.TempDir() + "/nexkv-bench-txn-wal"
		}
		w, err := wal.NewDiskWAL(&wal.WALConfig{Dir: dir, SegmentSize: 64 * 1024 * 1024})
		if err != nil {
			fmt.Fprintf(os.Stderr, "create wal: %v\n", err)
			return
		}
		kv = persist.NewPersistWAL(tree, w, parseWalSyncMode(*walSyncStr))
		defer kv.Close()
		defer tree.Close()
	} else {
		defer tree.Close()
	}

	// Preload for read/mixed
	if mode == "read" || mode == "mixed" {
		for i := 0; i < n; i++ {
			_ = setHelper(tree, kv, ctx, keyOf(i), valOf(i))
		}
	}

	// Warmup
	runTxnLoop(tree, kv, *warmupFlag, batch, mode, readRatio, nil, ctx)

	// Start profiling AFTER preload + warmup (only measure benchmark path)
	var cpuFile *os.File
	if *cpuProfile != "" {
		cpuFile, _ = os.Create(*cpuProfile)
		if cpuFile != nil {
			pprof.StartCPUProfile(cpuFile)
		}
	}

	// Measure
	var totalOps atomic.Int64
	t0 := time.Now()
	runTxnLoop(tree, kv, n, batch, mode, readRatio, &totalOps, ctx)
	elapsed := time.Since(t0)

	// Stop profiling BEFORE cleanup (avoids capturing tree.Close in profile)
	if cpuFile != nil {
		pprof.StopCPUProfile()
		cpuFile.Close()
	}
	if *memProfile != "" {
		memFile, _ := os.Create(*memProfile)
		if memFile != nil {
			pprof.WriteHeapProfile(memFile)
			memFile.Close()
		}
	}

	qps := float64(totalOps.Load()) / elapsed.Seconds()
	fmt.Printf("%-24s batch=%-6d ops=%d time=%.3fs qps=%10.0f\n",
		label, batch, totalOps.Load(), elapsed.Seconds(), qps)
}

// benchLOB runs a LOB benchmark with large values.
func benchLOB(label string, n, batch int, mode string, mmapSize int, ctx context.Context) {
	storage, err := btree.NewOffheapBTreeStorage(mmapSize)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create storage: %v\n", err)
		return
	}

	opts := []btree.BTreeOption{btree.WithTSGenerator(mvcc.NewLocalTS())}
	tree, err := btree.NewBTree(storage, opts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create btree: %v\n", err)
		return
	}
	defer tree.Close()

	// Preload for reads
	if mode == "read" {
		for i := 0; i < n; i++ {
			tx, _ := tree.BeginTx(ctx)
			tx.Set(ctx, keyOf(i), lobValOf())
			tx.Commit(ctx)
		}
	}

	// Warmup
	w := *warmupFlag
	if mode == "write" {
		for i := 0; i < w; i++ {
			tx, _ := tree.BeginTx(ctx)
			tx.Set(ctx, keyOf(n+i), lobValOf())
			tx.Commit(ctx)
		}
	} else {
		for i := 0; i < w; i++ {
			tx, _ := tree.BeginTx(ctx)
			tx.Get(ctx, keyOf(i%n))
			tx.Rollback(ctx)
		}
	}

	elapsed, ops := runLOBWriteLoop(tree, n, batch, mode)
	qps := float64(ops) / elapsed.Seconds()
	fmt.Printf("%-32s %10s %10d %13.0f op/s\n", label, "ok", ops, qps)
}

func runLOBWriteLoop(tree *btree.BTree, n, batch int, mode string) (time.Duration, int) {
	ctx := context.Background()
	val := lobValOf()
	var totalOps atomic.Int64
	var start time.Time

	if mode == "read" {
		start = time.Now()
		for i := 0; i < n; i += batch {
			tx, _ := tree.BeginTx(ctx)
			end := i + batch
			if end > n {
				end = n
			}
			for j := i; j < end; j++ {
				tx.Get(ctx, keyOf(j))
			}
			tx.Commit(ctx)
			totalOps.Add(int64(end - i))
		}
		return time.Since(start), int(totalOps.Load())
	}

	// Write mode
	start = time.Now()
	for i := 0; i < n; i += batch {
		tx, _ := tree.BeginTx(ctx)
		end := i + batch
		if end > n {
			end = n
		}
		for j := i; j < end; j++ {
			tx.Set(ctx, keyOf(j), val)
		}
		tx.Commit(ctx)
		totalOps.Add(int64(end - i))
	}
	return time.Since(start), int(totalOps.Load())
}

func runTxnLoop(tree *btree.BTree, kv *persist.PersistWAL, n, batch int,
	mode string, readRatio float64, ops *atomic.Int64, ctx context.Context) {

	if kv != nil {
		// WAL mode: use PersistWAL decorator (txn inside WAL Set)
		runPersistTxnLoop(tree, kv, n, batch, mode, readRatio, ops, ctx)
		return
	}

	// Memory mode
	rng := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 42))
	writeIdx := n

	for i := 0; i < n; i += batch {
		tx := beginTx(tree, ctx)
		count := 0

		// readbatch: one GetBatch call replaces entire per-key loop
		if mode == "readbatch" {
			batchTx, ok := tx.(interface {
				GetBatch(context.Context, [][]byte) ([][]byte, error)
			})
			if ok {
				keys := make([][]byte, batch)
				for k := 0; k < batch && i+k < n; k++ {
					keys[k] = keyOf((i + k) % n)
				}
				_, _ = batchTx.GetBatch(ctx, keys)
				count = len(keys)
			}
		} else {
			for j := 0; j < batch && i+j < n; j++ {
				switch mode {
				case "write":
					_ = tx.Set(ctx, keyOf(i+j), valOf(i+j))
				case "read":
					_, _ = tx.Get(ctx, keyOf((i+j)%n))
				case "mixed":
					if rng.Float64() < readRatio {
						_, _ = tx.Get(ctx, keyOf(rng.IntN(n)))
					} else {
						_ = tx.Set(ctx, keyOf(writeIdx), valOf(writeIdx))
						writeIdx++
					}
				case "rollback":
					_ = tx.Set(ctx, keyOf(i+j), valOf(i+j))
				}
				count++
			}
		}
		if mode == "rollback" {
			_ = tx.Rollback(ctx)
		} else {
			_ = tx.Commit(ctx)
		}
		if ops != nil {
			ops.Add(int64(count))
		}
	}
}

// WAL mode: transactions write through PersistWAL which appends WAL entries.
// WAL fsync happens in the background goroutine (group-commit/every-second).
func runPersistTxnLoop(tree *btree.BTree, kv *persist.PersistWAL, n, batch int,
	mode string, readRatio float64, ops *atomic.Int64, ctx context.Context) {

	rng := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 42))
	writeIdx := n

	for i := 0; i < n; i += batch {
		// BeginTx on the underlying tree, not PersistWAL
		tx := beginTx(tree, ctx)
		count := 0

		// readbatch: one GetBatch call replaces entire per-key loop
		if mode == "readbatch" {
			batchTx, ok := tx.(interface {
				GetBatch(context.Context, [][]byte) ([][]byte, error)
			})
			if ok {
				keys := make([][]byte, batch)
				for k := 0; k < batch && i+k < n; k++ {
					keys[k] = keyOf((i + k) % n)
				}
				_, _ = batchTx.GetBatch(ctx, keys)
				count = len(keys)
			}
		} else {
			for j := 0; j < batch && i+j < n; j++ {
				switch mode {
				case "write":
					_ = tx.Set(ctx, keyOf(i+j), valOf(i+j))
				case "read":
					_, _ = tx.Get(ctx, keyOf((i+j)%n))
				case "mixed":
					if rng.Float64() < readRatio {
						_, _ = tx.Get(ctx, keyOf(rng.IntN(n)))
					} else {
						_ = tx.Set(ctx, keyOf(writeIdx), valOf(writeIdx))
						writeIdx++
					}
				case "rollback":
					_ = tx.Set(ctx, keyOf(i+j), valOf(i+j))
				}
				count++
			}
		}
		if mode == "rollback" {
			_ = tx.Rollback(ctx)
		} else {
			_ = tx.Commit(ctx) // Commit writes to BTree; WAL append is independent
		}
		if ops != nil {
			ops.Add(int64(count))
		}
	}
}

func setHelper(tree *btree.BTree, kv *persist.PersistWAL, ctx context.Context, k, v []byte) error {
	if kv != nil {
		return kv.Set(ctx, k, v)
	}
	return tree.Set(ctx, k, v)
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

// lobValOf returns a large value for LOB benchmarks, reusing the same slice for all calls
// to avoid per-bench allocation overhead. Content is deterministic.
var lobValPool []byte

func lobValOf() []byte {
	if lobValPool == nil {
		lobValPool = make([]byte, *lobSize)
		for i := range lobValPool {
			lobValPool[i] = byte(i % 256)
		}
	}
	return lobValPool
}

func beginTx(tree *btree.BTree, ctx context.Context) service.Transaction {
	tx, _ := tree.BeginTx(ctx)
	return tx
}

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
