// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWAL_FileLocation tests where WAL files are stored.
func TestWAL_FileLocation(t *testing.T) {
	dir := t.TempDir()

	// Show temp directory location
	fmt.Printf("=== WAL 文件位置测试 ===\n")
	fmt.Printf("临时目录: %s\n", dir)

	// Open BTree with persistence
	btree, err := OpenBTree(dir, nil)
	require.NoError(t, err)
	defer btree.Close()

	// Show file paths
	dbPath := filepath.Join(dir, "database.db")
	walPath := filepath.Join(dir, "wal.log")

	fmt.Printf("\n预期文件路径:\n")
	fmt.Printf("  数据库文件: %s\n", dbPath)
	fmt.Printf("  WAL 文件:   %s\n", walPath)

	// Insert some data to generate WAL entries
	ctx := context.Background()
	fmt.Printf("\n写入测试数据...\n")
	for i := 0; i < 5; i++ {
		key := []byte{byte(i)}
		value := []byte{byte(i + 100)}
		err := btree.InsertWithSplit(ctx, key, value)
		require.NoError(t, err)
		fmt.Printf("  插入: key=%d, value=%d\n", i, i+100)
	}

	// Check if files exist
	fmt.Printf("\n检查文件...\n")

	dbInfo, err := os.Stat(dbPath)
	if err == nil {
		fmt.Printf("  ✅ 数据库文件存在: 大小=%d bytes\n", dbInfo.Size())
	} else {
		fmt.Printf("  ❌ 数据库文件不存在: %v\n", err)
	}

	walInfo, err := os.Stat(walPath)
	if err == nil {
		fmt.Printf("  ✅ WAL 文件存在: 大小=%d bytes\n", walInfo.Size())
	} else {
		fmt.Printf("  ❌ WAL 文件不存在: %v\n", err)
	}

	// Read and display WAL file content
	fmt.Printf("\n=== WAL 文件内容 ===\n")
	walData, err := os.ReadFile(walPath)
	if err == nil {
		fmt.Printf("WAL 文件大小: %d bytes\n", len(walData))
		fmt.Printf("WAL 条目数 (估算): %d 条\n", len(walData)/20) // 粗略估算
		fmt.Printf("前 100 bytes (hex): %x\n", walData[:min(100, len(walData))])
		fmt.Printf("前 100 bytes (ascii): %s\n", string(walData[:min(100, len(walData))]))
	}

	// List all files in directory
	fmt.Printf("\n=== 目录中的所有文件 ===\n")
	files, err := os.ReadDir(dir)
	if err == nil {
		for _, file := range files {
			filePath := filepath.Join(dir, file.Name())
			info, _ := os.Stat(filePath)
			fmt.Printf("  %-20s 大小: %6d bytes\n", file.Name(), info.Size())
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
