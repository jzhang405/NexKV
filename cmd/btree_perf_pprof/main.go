// BTree Set pprof 性能分析工具
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime/pprof"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/btree"
)

var (
	flagThreads int
	flagCount   int
	flagInit    int
)

func init() {
	flag.IntVar(&flagThreads, "threads", 8, "并发线程数")
	flag.IntVar(&flagCount, "count", 50000, "每线程操作数")
	flag.IntVar(&flagInit, "init", 200, "初始数据量")
}

func main() {
	flag.Parse()

	ctx := context.Background()
	tree, err := btree.OpenBTree("", &model.BTreeConfig{})
	if err != nil {
		panic(err)
	}
	defer tree.Close()

	numThreads := flagThreads
	initCount := flagInit
	count := flagCount

	// 预生成 keys/values
	type threadData struct {
		keys   [][]byte
		values [][]byte
	}
	threadDataArr := make([]threadData, numThreads)
	for t := 0; t < numThreads; t++ {
		td := threadData{}
		td.keys = make([][]byte, count)
		td.values = make([][]byte, count)
		for j := 0; j < count; j++ {
			td.keys[j] = []byte(fmt.Sprintf("t%d-key-%d", t, j%initCount))
			td.values[j] = []byte(fmt.Sprintf("val-%d", j))
		}
		threadDataArr[t] = td
	}

	// 初始化
	initKeys := make([][]byte, initCount)
	initValues := make([][]byte, initCount)
	for i := 0; i < initCount; i++ {
		initKeys[i] = []byte(fmt.Sprintf("init-key-%d", i))
		initValues[i] = []byte(fmt.Sprintf("init-val-%d", i))
	}
	fmt.Fprintf(os.Stderr, "初始化 %d 条...\n", initCount)
	for i := 0; i < initCount; i++ {
		tree.Set(ctx, initKeys[i], initValues[i])
	}
	fmt.Fprintf(os.Stderr, "初始化完成\n")

	// warmup
	for i := 0; i < 1000; i++ {
		tree.Set(ctx, []byte(fmt.Sprintf("warmup-%d", i%100)), []byte("w"))
	}

	f, err := os.Create("cpu.prof")
	if err != nil {
		panic(err)
	}
	pprof.StartCPUProfile(f)

	totalOps := numThreads * count
	fmt.Fprintf(os.Stderr, "开始 CPU profiling: %d 线程 × %d 次 Set = %d ops...\n", numThreads, count, totalOps)

	var successCount atomic.Int64
	var wg sync.WaitGroup
	start := time.Now()

	for t := 0; t < numThreads; t++ {
		wg.Add(1)
		go func(threadID int, td threadData) {
			defer wg.Done()
			for j := 0; j < count; j++ {
				if err := tree.Set(ctx, td.keys[j], td.values[j]); err == nil {
					successCount.Add(1)
				}
			}
		}(t, threadDataArr[t])
	}

	wg.Wait()
	duration := time.Since(start)

	pprof.StopCPUProfile()
	f.Close()

	success := successCount.Load()
	fmt.Fprintf(os.Stderr, "完成: %d/%d ops in %v, %.0f ops/s, %.2f μs/op\n",
		success, totalOps, duration, float64(success)/duration.Seconds(), float64(duration.Nanoseconds())/float64(success)/1000)
	fmt.Fprintf(os.Stderr, "CPU profile -> cpu.prof\n")
}
