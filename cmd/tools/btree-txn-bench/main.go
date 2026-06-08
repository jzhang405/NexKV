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

	// CPU profiling
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

	// Measure
	var totalOps atomic.Int64
	t0 := time.Now()
	runTxnLoop(tree, kv, n, batch, mode, readRatio, &totalOps, ctx)
	elapsed := time.Since(t0)

	qps := float64(totalOps.Load()) / elapsed.Seconds()
	fmt.Printf("%-24s batch=%-6d ops=%d time=%.3fs qps=%10.0f\n",
		label, batch, totalOps.Load(), elapsed.Seconds(), qps)
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
