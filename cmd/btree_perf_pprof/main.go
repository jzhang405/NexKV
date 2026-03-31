// BTree Set retry 诊断工具
// 目标：1 线程下搞清楚 Set 失败的错误路径
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime/pprof"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/btree"
	errpkg "github.com/jzhang405/NexKV/pkg/errors"
)

var (
	flagThreads int
	flagCount   int
	flagInit    int
)

func init() {
	flag.IntVar(&flagThreads, "threads", 1, "并发线程数")
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
			td.keys[j] = []byte(fmt.Sprintf("init-%05d", (t*count+j)%initCount))
			td.values[j] = []byte(fmt.Sprintf("v%05d", j%initCount))
		}
		threadDataArr[t] = td
	}

	// 初始化
	initKeys := make([][]byte, initCount)
	initValues := make([][]byte, initCount)
	for i := 0; i < initCount; i++ {
		initKeys[i] = []byte(fmt.Sprintf("init-%05d", i))
		initValues[i] = []byte(fmt.Sprintf("v%05d", i))
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
	fmt.Fprintf(os.Stderr, "开始: %d 线程 × %d 次 Set = %d ops...\n", numThreads, count, totalOps)

	// 错误分类统计
	type errStats struct {
		success     atomic.Int64
		errRetry    atomic.Int64 // ErrRetry (CAS fail / lock fail)
		errCircRef  atomic.Int64 // ErrCircularReference
		errMaxRetry atomic.Int64 // ErrMaxRetriesExceeded
		errOther    atomic.Int64 // 所有其他错误
	}
	var stats errStats

	// 详细的 "other" 错误收集（只收集前 20 个不同的错误消息）
	var otherMu sync.Mutex
	otherErrors := make(map[string]int64)

	var wg sync.WaitGroup
	start := time.Now()

	for t := 0; t < numThreads; t++ {
		wg.Add(1)
		go func(threadID int, td threadData) {
			defer wg.Done()
			for j := 0; j < count; j++ {
				err := tree.Set(ctx, td.keys[j], td.values[j])
				switch {
				case err == nil:
					stats.success.Add(1)
				case errors.Is(err, errpkg.ErrBTreeRetry):
					stats.errRetry.Add(1)
				case errors.Is(err, errpkg.ErrBTreeCircularReference):
					stats.errCircRef.Add(1)
				case errors.Is(err, errpkg.ErrBTreeMaxRetriesExceeded):
					stats.errMaxRetry.Add(1)
				default:
					stats.errOther.Add(1)
					msg := err.Error()
					// 截取前 100 字符作为 key
					if len(msg) > 100 {
						msg = msg[:100]
					}
					otherMu.Lock()
					otherErrors[msg]++
					otherMu.Unlock()
				}
			}
		}(t, threadDataArr[t])
	}

	wg.Wait()
	duration := time.Since(start)

	pprof.StopCPUProfile()
	f.Close()

	success := stats.success.Load()
	errRetry := stats.errRetry.Load()
	errCircRef := stats.errCircRef.Load()
	errMaxRetry := stats.errMaxRetry.Load()
	errOther := stats.errOther.Load()

	fmt.Fprintf(os.Stderr, "\n========== 结果 ==========\n")
	fmt.Fprintf(os.Stderr, "耗时: %v, %.0f ops/s\n", duration, float64(success)/duration.Seconds())
	fmt.Fprintf(os.Stderr, "总 ops: %d\n\n", totalOps)
	fmt.Fprintf(os.Stderr, "--- 错误分类 ---\n")
	fmt.Fprintf(os.Stderr, "Success:            %6d (%.1f%%)\n", success, float64(success)/float64(totalOps)*100)
	fmt.Fprintf(os.Stderr, "ErrRetry:           %6d (%.1f%%)\n", errRetry, float64(errRetry)/float64(totalOps)*100)
	fmt.Fprintf(os.Stderr, "ErrCircRef:         %6d (%.1f%%)\n", errCircRef, float64(errCircRef)/float64(totalOps)*100)
	fmt.Fprintf(os.Stderr, "ErrMaxRetries:      %6d (%.1f%%)\n", errMaxRetry, float64(errMaxRetry)/float64(totalOps)*100)
	fmt.Fprintf(os.Stderr, "ErrOther:           %6d (%.1f%%)\n", errOther, float64(errOther)/float64(totalOps)*100)

	if len(otherErrors) > 0 {
		fmt.Fprintf(os.Stderr, "\n--- Other 错误明细 ---\n")
		for msg, cnt := range otherErrors {
			fmt.Fprintf(os.Stderr, "  [%6d] %s\n", cnt, msg)
		}
	}

	fmt.Fprintf(os.Stderr, "\nCPU profile -> cpu.prof\n")
}
