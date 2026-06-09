package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/infrastructure/storage/btree"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/lob"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/mvcc"
)

func main() {
	mmapMB := flag.Int("mmap", 2048, "mmap size in MB")
	ops := flag.Int("n", 10000, "operations")
	threads := flag.Int("t", 0, "goroutines")
	lobSize := flag.Int("size", 131072, "LOB size in bytes")
	flag.Parse()

	if *threads <= 0 {
		*threads = runtime.GOMAXPROCS(0)
	}

	// 强制 128KB 走 Tier 1 overflow page (mmap, 无 fsync)
	mvcc.LOBSizeThreshold = 256 * 1024
	mvcc.LOBFileThreshold = 256 * 1024 * 1024

	mmapSize := *mmapMB * 1024 * 1024
	n := *ops

	storage, err := btree.NewOffheapBTreeStorage(mmapSize)
	if err != nil {
		panic(err)
	}
	tree, err := btree.NewBTree(storage)
	if err != nil {
		panic(err)
	}
	defer tree.Close()
	ctx := context.Background()

	lobMgr := lob.NewDefaultLOBManager(storage.GetPageManager())
	val := make([]byte, *lobSize)
	for i := range val {
		val[i] = byte(i % 256)
	}

	fmt.Println("=== 128KB 走 Tier 1 (overflow page, 纯 mmap, 无 fsync) ===")
	fmt.Printf("ops=%d  goroutines=%d  mmap=%dMB  LOBSizeThreshold=%d\n\n",
		n, *threads, *mmapMB, mvcc.LOBSizeThreshold)

	// PUT — 单线程
	t0 := time.Now()
	for i := 0; i < n; i++ {
		encoded, _ := mvcc.EncodeValue(val, uint64(i+1), 0, 0, nil, lobMgr, nil)
		tree.Set(ctx, keyOf(i), encoded)
	}
	elapsed := time.Since(t0)
	fmt.Printf("%-30s t=1  op=write  lob=%dB  ops=%d  time=%.3fs  qps=%10.0f\n",
		"lob-overflow-put-128k", *lobSize, n, elapsed.Seconds(), float64(n)/elapsed.Seconds())

	// GET — 单线程
	t0 = time.Now()
	for i := 0; i < n; i++ {
		raw, _ := tree.Get(ctx, keyOf(i))
		mvcc.DecodeValue(raw, lobMgr, nil)
	}
	elapsed = time.Since(t0)
	fmt.Printf("%-30s t=1  op=read   lob=%dB  ops=%d  time=%.3fs  qps=%10.0f\n",
		"lob-overflow-get-128k", *lobSize, n, elapsed.Seconds(), float64(n)/elapsed.Seconds())

	// PUT — 多线程
	var wg sync.WaitGroup
	per := n / *threads
	t0 = time.Now()
	for t := 0; t < *threads; t++ {
		wg.Add(1)
		go func(start int) {
			defer wg.Done()
			for i := start; i < start+per; i++ {
				encoded, _ := mvcc.EncodeValue(val, uint64(i+n+1), 0, 0, nil, lobMgr, nil)
				tree.Set(ctx, keyOf(i+n), encoded)
			}
		}(t * per)
	}
	wg.Wait()
	elapsed = time.Since(t0)
	fmt.Printf("%-30s t=%-2d op=write  lob=%dB  ops=%d  time=%.3fs  qps=%10.0f\n",
		"par-lob-overflow-put-128k", *threads, *lobSize, n, elapsed.Seconds(), float64(n)/elapsed.Seconds())

	// GET — 多线程
	t0 = time.Now()
	for t := 0; t < *threads; t++ {
		wg.Add(1)
		go func(start int) {
			defer wg.Done()
			for i := start; i < start+per; i++ {
				raw, _ := tree.Get(ctx, keyOf(i))
				mvcc.DecodeValue(raw, lobMgr, nil)
			}
		}(t * per)
	}
	wg.Wait()
	elapsed = time.Since(t0)
	fmt.Printf("%-30s t=%-2d op=read   lob=%dB  ops=%d  time=%.3fs  qps=%10.0f\n",
		"par-lob-overflow-get-128k", *threads, *lobSize, n, elapsed.Seconds(), float64(n)/elapsed.Seconds())

	_ = os.RemoveAll("data")
}

var keyPool [][]byte

func init() {
	keyPool = make([][]byte, 20000)
	for i := range keyPool {
		keyPool[i] = []byte(fmt.Sprintf("k-%010d", i))
	}
}

func keyOf(i int) []byte {
	if i < len(keyPool) {
		return keyPool[i]
	}
	return []byte(fmt.Sprintf("k-%010d", i))
}
