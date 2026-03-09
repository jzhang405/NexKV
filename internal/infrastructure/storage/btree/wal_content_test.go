// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWAL_ContentDetails tests detailed WAL file content.
func TestWAL_ContentDetails(t *testing.T) {
	dir := t.TempDir()
	fmt.Printf("=== WAL 文件内容详细解析 ===\n")
	fmt.Printf("目录: %s\n\n", dir)

	// Open BTree and write some data
	btree, err := OpenBTree(dir, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Write specific test data
	testData := []struct{ key, value string }{
		{"name", "Alice"},
		{"age", "30"},
		{"city", "Beijing"},
	}

	fmt.Printf("写入测试数据:\n")
	for i, data := range testData {
		key := []byte(data.key)
		value := []byte(data.value)
		err := btree.InsertWithSplit(ctx, key, value)
		require.NoError(t, err)
		fmt.Printf("  [%d] key=%s, value=%s\n", i, data.key, data.value)
	}

	btree.Close()

	// Read WAL file
	walPath := filepath.Join(dir, "wal.log")
	fmt.Printf("\n解析 WAL 文件: %s\n", walPath)

	walData, err := os.ReadFile(walPath)
	require.NoError(t, err)

	fmt.Printf("\n文件信息:\n")
	fmt.Printf("  大小: %d bytes\n", len(walData))
	fmt.Printf("  十六进制: %s\n", hex.EncodeToString(walData))

	// Parse WAL entries
	fmt.Printf("\n解析 WAL 条目:\n")
	offset := 0
	entryNum := 1

	for offset < len(walData) {
		if offset+5 > len(walData) {
			break
		}

		// Read header
		entryType := WALEntryType(walData[offset])
		keyLen := int(walData[offset+1]) | int(walData[offset+2])<<8
		valueLen := int(walData[offset+3]) | int(walData[offset+4])<<8

		fmt.Printf("\n条目 #%d:\n", entryNum)
		fmt.Printf("  类型: ")
		switch entryType {
		case WALEntryTypeInsert:
			fmt.Printf("INSERT\n")
		case WALEntryTypeDelete:
			fmt.Printf("DELETE\n")
		case WALEntryTypeCheckpoint:
			fmt.Printf("CHECKPOINT\n")
		default:
			fmt.Printf("UNKNOWN(%d)\n", entryType)
		}

		// Read key and value
		dataStart := offset + 5
		key := make([]byte, keyLen)
		value := make([]byte, valueLen)

		if dataStart+keyLen+valueLen > len(walData) {
			fmt.Printf("  ❌ 数据不完整\n")
			break
		}

		copy(key, walData[dataStart:dataStart+keyLen])
		copy(value, walData[dataStart+keyLen:dataStart+keyLen+valueLen])

		fmt.Printf("  Key:   len=%d, value=%s\n", keyLen, string(key))
		fmt.Printf("  Value: len=%d, value=%s\n", valueLen, string(value))

		// Read checksum
		checksumOffset := dataStart + keyLen + valueLen
		if checksumOffset+4 > len(walData) {
			fmt.Printf("  ❌ Checksum 不完整\n")
			break
		}

		checksum := uint32(walData[checksumOffset]) |
			uint32(walData[checksumOffset+1])<<8 |
			uint32(walData[checksumOffset+2])<<16 |
			uint32(walData[checksumOffset+3])<<24

		fmt.Printf("  Checksum: 0x%08x\n", checksum)

		// Move to next entry
		offset = checksumOffset + 4
		entryNum++
	}

	fmt.Printf("\n总共 %d 个 WAL 条目\n", entryNum-1)
}

// TestWAL_ManualInspection creates WAL files for manual inspection.
func TestWAL_ManualInspection(t *testing.T) {
	dir := t.TempDir()

	fmt.Printf("=== WAL 文件手动检查 ===\n")
	fmt.Printf("创建测试数据库: %s\n\n", dir)

	btree, err := OpenBTree(dir, nil)
	require.NoError(t, err)
	defer btree.Close()

	ctx := context.Background()

	// Write 10 entries
	fmt.Printf("写入 10 条测试数据...\n")
	for i := 0; i < 10; i++ {
		key := []byte{byte(i)}
		value := []byte{byte(i + 100)}
		err := btree.InsertWithSplit(ctx, key, value)
		require.NoError(t, err)
	}

	fmt.Printf("完成！\n\n")

	// Show file info
	walPath := filepath.Join(dir, "wal.log")
	dbPath := filepath.Join(dir, "database.db")

	walInfo, _ := os.Stat(walPath)
	dbInfo, _ := os.Stat(dbPath)

	fmt.Printf("文件列表:\n")
	fmt.Printf("  %-20s 大小: %6d bytes  路径: %s\n", "wal.log", walInfo.Size(), walPath)
	fmt.Printf("  %-20s 大小: %6d bytes  路径: %s\n", "database.db", dbInfo.Size(), dbPath)

	fmt.Printf("\n提示: 可以使用以下命令查看 WAL 文件内容:\n")
	fmt.Printf("  xxd %s\n", walPath)
	fmt.Printf("  cat %s | hexdump -C\n", walPath)
}
