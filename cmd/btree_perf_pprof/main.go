// BTree Set pprof 性能分析工具
package main

import (
	"context"
	"fmt"
	"os"
	"runtime/pprof"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/btree"
)

func main() {
	ctx := context.Background()
	tree, err := btree.OpenBTree("", &model.BTreeConfig{})
	if err != nil {
		panic(err)
	}
	defer tree.Close()

	initCount := 200
	count := 50000

	randBytes := make([]byte, initCount)
	for i := range randBytes {
		randBytes[i] = byte(i % 256)
	}
	fmt.Fprintf(os.Stderr, "初始化 %d 条...\n", initCount)
	for i := 0; i < initCount; i++ {
		key := fmt.Sprintf("%ckey-%d", randBytes[i], i)
		tree.Set(ctx, []byte(key), []byte(fmt.Sprintf("val-%d", i)))
	}
	fmt.Fprintf(os.Stderr, "初始化完成\n")

	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("warmup-%d", i%100)
		tree.Set(ctx, []byte(key), []byte("w"))
	}

	f, err := os.Create("cpu.prof")
	if err != nil {
		panic(err)
	}
	pprof.StartCPUProfile(f)

	fmt.Fprintf(os.Stderr, "开始 CPU profiling: %d 次 Set...\n", count)
	start := time.Now()
	for j := 0; j < count; j++ {
		key := fmt.Sprintf("%ckey-p-%d", byte(j%256), j)
		tree.Set(ctx, []byte(key), []byte(fmt.Sprintf("val-%d", j)))
	}
	duration := time.Since(start)

	pprof.StopCPUProfile()
	f.Close()

	fmt.Fprintf(os.Stderr, "完成: %d ops in %v, %.0f ops/s, %.2f μs/op\n",
		count, duration, float64(count)/duration.Seconds(), float64(duration.Nanoseconds())/float64(count)/1000)
	fmt.Fprintf(os.Stderr, "CPU profile -> cpu.prof\n")
}
