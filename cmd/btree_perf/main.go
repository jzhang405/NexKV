package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jzhang405/NexKV/internal/infrastructure/storage/btree"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <output_dir>\n", os.Args[0])
		os.Exit(1)
	}

	outputDir := os.Args[1]

	// 创建 BTree
	tree, err := btree.OpenBTree(outputDir, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open BTree: %v\n", err)
		os.Exit(1)
	}
	defer tree.Close()

	ctx := context.Background()

	// 预先插入 1000 个键
	fmt.Fprintln(os.Stderr, "Initializing BTree with 1000 keys...")
	for i := 0; i < 1000; i++ {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		if err := tree.Set(ctx, key, value); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to set key-%d: %v\n", i, err)
			os.Exit(1)
		}
	}

	fmt.Fprintln(os.Stderr, "Starting benchmark (100K iterations)...")

	// 运行基准测试
	start := time.Now()
	for i := 0; i < 100000; i++ {
		key := []byte(fmt.Sprintf("key-%d", i%1000))
		value := []byte(fmt.Sprintf("value-%d", i))
		if err := tree.Set(ctx, key, value); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to set key-%d: %v\n", i, err)
			os.Exit(1)
		}

		if (i+1)%10000 == 0 {
			elapsed := time.Since(start)
			opsPerSec := float64(i+1) / elapsed.Seconds()
			fmt.Fprintf(os.Stderr, "\rProgress: %d/%d (%.1f ops/sec)", i+1, 100000, opsPerSec)
		}
	}

	elapsed := time.Since(start)
	opsPerSec := 100000.0 / elapsed.Seconds()
	fmt.Fprintf(os.Stderr, "\nCompleted: 100000 ops in %v (%.1f ops/sec)\n", elapsed, opsPerSec)
}
