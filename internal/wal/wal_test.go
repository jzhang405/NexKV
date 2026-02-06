// Package store WAL 单元测试
//
// 测试覆盖率目标：>80%
// 测试 WAL 的核心功能和边界情况
//
//nolint:errcheck // 测试代码中 defer Close() 不检查错误是常见做法
package store

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jzhang405/NexKV/internal/clock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ========================================
// 辅助函数
// ========================================

// createTestWAL 创建测试用的 WAL 实例
func createTestWAL(t *testing.T) (*MetadataWAL, string) {
	t.Helper()

	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "test.wal")

	wal, err := NewMetadataWAL(walPath)
	require.NoError(t, err)
	require.NotNil(t, wal)

	return wal, walPath
}

// createTestEntry 创建测试用的 WAL 条目
func createTestEntry(t *testing.T, key, value string) *WALEntry {
	t.Helper()

	// 创建 HLC 时间戳（自动初始化为当前时间）
	ts := clock.NewHLC()

	return &WALEntry{
		Timestamp: ts,
		Type:      WALTypePut,
		Key:       []byte(key),
		Value:     []byte(value),
		Checksum:  0, // 将在 Append 时自动计算
	}
}

// ========================================
// 基础功能测试
// ========================================

// TestNewMetadataWAL 测试创建 WAL
func TestNewMetadataWAL(t *testing.T) {
	t.Run("创建新WAL文件", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		assert.NotNil(t, wal)
		assert.False(t, wal.closed)
	})

	t.Run("创建WAL时自动创建目录", func(t *testing.T) {
		tmpDir := t.TempDir()
		walPath := filepath.Join(tmpDir, "subdir", "test.wal")

		wal, err := NewMetadataWAL(walPath)
		require.NoError(t, err)
		defer wal.Close()

		// 验证目录已创建
		_, err = os.Stat(filepath.Dir(walPath))
		assert.NoError(t, err)
	})

	t.Run("打开已存在的WAL文件", func(t *testing.T) {
		wal, walPath := createTestWAL(t)

		// 写入一些数据
		entry := createTestEntry(t, "key1", "value1")
		err := wal.Append(entry)
		require.NoError(t, err)
		require.NoError(t, wal.Close())

		// 重新打开
		wal2, err := NewMetadataWAL(walPath)
		require.NoError(t, err)
		defer wal2.Close()

		stats, err := wal2.GetStats()
		require.NoError(t, err)
		assert.Greater(t, stats.Size, int64(0))
	})
}

// TestMetadataWAL_Append 测试追加日志条目
func TestMetadataWAL_Append(t *testing.T) {
	t.Run("追加单个条目", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		entry := createTestEntry(t, "key1", "value1")
		err := wal.Append(entry)
		assert.NoError(t, err)

		stats, err := wal.GetStats()
		require.NoError(t, err)
		assert.Equal(t, uint64(1), stats.Entries)
		assert.Greater(t, stats.Size, int64(0))
	})

	t.Run("追加多个条目", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		// 追加 100 个条目
		for i := 0; i < 100; i++ {
			entry := createTestEntry(t, "key", "value")
			err := wal.Append(entry)
			assert.NoError(t, err)
		}

		stats, err := wal.GetStats()
		require.NoError(t, err)
		assert.Equal(t, uint64(100), stats.Entries)
	})

	t.Run("追加nil条目返回错误", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		err := wal.Append(nil)
		assert.Error(t, err)
	})

	t.Run("追加后关闭的WAL返回错误", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		require.NoError(t, wal.Close())

		entry := createTestEntry(t, "key1", "value1")
		err := wal.Append(entry)
		assert.Error(t, err)
	})

	t.Run("追加不同类型的条目", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		entryTypes := []WALType{
			WALTypePut,
			WALTypeDelete,
			WALTypeCheckpoint,
		}

		for _, entryType := range entryTypes {
			entry := createTestEntry(t, "key", "value")
			entry.Type = entryType
			err := wal.Append(entry)
			assert.NoError(t, err)
		}

		stats, err := wal.GetStats()
		require.NoError(t, err)
		assert.Equal(t, uint64(3), stats.Entries)
	})
}

// TestMetadataWAL_Recover 测试恢复日志
func TestMetadataWAL_Recover(t *testing.T) {
	t.Run("恢复空WAL", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		entries, err := wal.Recover()
		assert.NoError(t, err)
		assert.Empty(t, entries)
	})

	t.Run("恢复单个条目", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		// 写入条目
		originalEntry := createTestEntry(t, "key1", "value1")
		require.NoError(t, wal.Append(originalEntry))

		// 恢复
		entries, err := wal.Recover()
		require.NoError(t, err)
		assert.Len(t, entries, 1)

		// 验证内容
		recoveredEntry := entries[0]
		assert.Equal(t, originalEntry.Type, recoveredEntry.Type)
		assert.Equal(t, originalEntry.Key, recoveredEntry.Key)
		assert.Equal(t, originalEntry.Value, recoveredEntry.Value)
	})

	t.Run("恢复多个条目", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		// 写入多个条目
		expectedCount := 50
		for i := 0; i < expectedCount; i++ {
			entry := createTestEntry(t, "key", "value")
			require.NoError(t, wal.Append(entry))
		}

		// 恢复
		entries, err := wal.Recover()
		require.NoError(t, err)
		assert.Len(t, entries, expectedCount)
	})

	t.Run("恢复后能继续追加", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		// 写入初始条目
		entry1 := createTestEntry(t, "key1", "value1")
		require.NoError(t, wal.Append(entry1))

		// 恢复
		entries, err := wal.Recover()
		require.NoError(t, err)
		assert.Len(t, entries, 1)

		// 继续追加
		entry2 := createTestEntry(t, "key2", "value2")
		require.NoError(t, wal.Append(entry2))

		stats, err := wal.GetStats()
		require.NoError(t, err)
		assert.Equal(t, uint64(2), stats.Entries)
	})

	t.Run("恢复带有旧值的条目", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		// 创建带有旧值的条目
		entry := createTestEntry(t, "key1", "newValue")
		entry.OldValue = []byte("oldValue")
		require.NoError(t, wal.Append(entry))

		// 恢复
		entries, err := wal.Recover()
		require.NoError(t, err)
		assert.Len(t, entries, 1)

		recoveredEntry := entries[0]
		assert.Equal(t, []byte("oldValue"), recoveredEntry.OldValue)
		assert.Equal(t, []byte("newValue"), recoveredEntry.Value)
	})
}

// TestMetadataWAL_Truncate 测试截断日志
func TestMetadataWAL_Truncate(t *testing.T) {
	t.Run("截断WAL", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		// 写入多个条目
		for i := 0; i < 10; i++ {
			entry := createTestEntry(t, "key", "value")
			require.NoError(t, wal.Append(entry))
		}

		// 获取当前大小
		statsBefore, err := wal.GetStats()
		require.NoError(t, err)

		// 截断到一半
		truncateOffset := statsBefore.Size / 2
		err = wal.Truncate(truncateOffset)
		assert.NoError(t, err)

		// 验证大小（Truncate 会写入 EOF 标记）
		statsAfter, err := wal.GetStats()
		require.NoError(t, err)
		assert.Equal(t, truncateOffset+WALEOFSize, statsAfter.Size, "截断后大小应等于截断点加 EOF 标记大小")
	})

	t.Run("截断到0", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		// 写入条目
		entry := createTestEntry(t, "key", "value")
		require.NoError(t, wal.Append(entry))

		// 截断到 0
		err := wal.Truncate(0)
		assert.NoError(t, err)

		// 验证文件为空
		entries, err := wal.Recover()
		require.NoError(t, err)
		assert.Empty(t, entries)
	})

	t.Run("截断已关闭的WAL返回错误", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		require.NoError(t, wal.Close())

		err := wal.Truncate(0)
		assert.Error(t, err)
	})
}

// TestMetadataWAL_Sync 测试同步到磁盘
func TestMetadataWAL_Sync(t *testing.T) {
	t.Run("同步成功", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		entry := createTestEntry(t, "key", "value")
		require.NoError(t, wal.Append(entry))

		err := wal.Sync()
		assert.NoError(t, err)
	})

	t.Run("同步已关闭的WAL返回错误", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		require.NoError(t, wal.Close())

		err := wal.Sync()
		assert.Error(t, err)
	})
}

// TestMetadataWAL_Close 测试关闭WAL
func TestMetadataWAL_Close(t *testing.T) {
	t.Run("关闭成功", func(t *testing.T) {
		wal, _ := createTestWAL(t)

		err := wal.Close()
		assert.NoError(t, err)
		assert.True(t, wal.closed)
	})

	t.Run("重复关闭不报错", func(t *testing.T) {
		wal, _ := createTestWAL(t)

		err1 := wal.Close()
		err2 := wal.Close()
		assert.NoError(t, err1)
		assert.NoError(t, err2)
	})

	t.Run("关闭后不能追加", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		require.NoError(t, wal.Close())

		entry := createTestEntry(t, "key", "value")
		err := wal.Append(entry)
		assert.Error(t, err)
	})
}

// TestMetadataWAL_GetStats 测试获取统计信息
func TestMetadataWAL_GetStats(t *testing.T) {
	t.Run("空WAL的统计信息", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		stats, err := wal.GetStats()
		require.NoError(t, err)
		assert.Equal(t, uint64(0), stats.Entries)
		assert.Equal(t, int64(0), stats.Size)
	})

	t.Run("有数据WAL的统计信息", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		// 写入条目
		for i := 0; i < 10; i++ {
			entry := createTestEntry(t, "key", "value")
			require.NoError(t, wal.Append(entry))
		}

		stats, err := wal.GetStats()
		require.NoError(t, err)
		assert.Equal(t, uint64(10), stats.Entries)
		assert.Greater(t, stats.Size, int64(0))
	})

	t.Run("已关闭WAL的统计信息", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		require.NoError(t, wal.Close())

		_, err := wal.GetStats()
		assert.Error(t, err)
	})
}

// ========================================
// RecoverOptimized 测试
// ========================================

// TestMetadataWAL_RecoverOptimized 测试优化的恢复方法
func TestMetadataWAL_RecoverOptimized(t *testing.T) {
	t.Run("优化恢复空WAL", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		entries, err := wal.RecoverOptimized()
		assert.NoError(t, err)
		assert.Empty(t, entries)
	})

	t.Run("优化恢复多个条目", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		// 写入多个条目
		expectedCount := 100
		for i := 0; i < expectedCount; i++ {
			entry := createTestEntry(t, "key", "value")
			require.NoError(t, wal.Append(entry))
		}

		// 优化恢复
		entries, err := wal.RecoverOptimized()
		require.NoError(t, err)
		assert.Len(t, entries, expectedCount)
	})

	t.Run("优化恢复后能继续写入", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		// 写入初始条目
		entry1 := createTestEntry(t, "key1", "value1")
		require.NoError(t, wal.Append(entry1))

		// 优化恢复
		entries, err := wal.RecoverOptimized()
		require.NoError(t, err)
		assert.Len(t, entries, 1)

		// 继续追加
		entry2 := createTestEntry(t, "key2", "value2")
		require.NoError(t, wal.Append(entry2))

		stats, err := wal.GetStats()
		require.NoError(t, err)
		assert.Equal(t, uint64(2), stats.Entries)
	})
}

// ========================================
// RecoverBatch 测试
// ========================================

// TestMetadataWAL_RecoverBatch 测试批量恢复
func TestMetadataWAL_RecoverBatch(t *testing.T) {
	t.Run("批量恢复指定数量", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		// 写入 100 个条目
		for i := 0; i < 100; i++ {
			entry := createTestEntry(t, "key", "value")
			require.NoError(t, wal.Append(entry))
		}

		// 批量恢复 50 个
		entries, nextOffset, err := wal.RecoverBatch(50)
		require.NoError(t, err)
		assert.Len(t, entries, 50)
		assert.Greater(t, nextOffset, int64(0))
	})

	t.Run("批量恢复全部", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		// 写入 10 个条目
		count := 10
		for i := 0; i < count; i++ {
			entry := createTestEntry(t, "key", "value")
			require.NoError(t, wal.Append(entry))
		}

		// 批量恢复所有（请求更多）
		entries, _, err := wal.RecoverBatch(1000)
		require.NoError(t, err)
		assert.Len(t, entries, count)
	})
}

// ========================================
// ValidateWALFile 测试
// ========================================

// TestMetadataWAL_ValidateWALFile 测试验证WAL文件
func TestMetadataWAL_ValidateWALFile(t *testing.T) {
	t.Run("验证空WAL文件", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		valid, corrupted, err := wal.ValidateWALFile()
		require.NoError(t, err)
		assert.Equal(t, 0, valid)
		assert.Equal(t, 0, corrupted)
	})

	t.Run("验证有效WAL文件", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		// 写入有效条目
		for i := 0; i < 10; i++ {
			entry := createTestEntry(t, "key", "value")
			require.NoError(t, wal.Append(entry))
		}

		valid, corrupted, err := wal.ValidateWALFile()
		require.NoError(t, err)
		assert.Equal(t, 10, valid)
		assert.Equal(t, 0, corrupted)
	})
}

// ========================================
// Checkpoint 测试
// ========================================

// TestMetadataWAL_Checkpoint 测试检查点功能
func TestMetadataWAL_Checkpoint(t *testing.T) {
	t.Run("创建检查点", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		// 写入一些条目
		for i := 0; i < 5; i++ {
			entry := createTestEntry(t, "key", "value")
			require.NoError(t, wal.Append(entry))
		}

		// 创建检查点
		offset, err := wal.CreateCheckpointOptimized()
		require.NoError(t, err)
		assert.Greater(t, offset, int64(0))
	})

	t.Run("恢复到检查点", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		// 写入初始条目
		for i := 0; i < 3; i++ {
			entry := createTestEntry(t, "key", "value")
			require.NoError(t, wal.Append(entry))
		}

		// 创建检查点
		_, err := wal.CreateCheckpointOptimized()
		require.NoError(t, err)

		// 写入更多条目
		for i := 0; i < 2; i++ {
			entry := createTestEntry(t, "key", "value")
			require.NoError(t, wal.Append(entry))
		}

		// 恢复到检查点
		entries, _, err := wal.RecoverToCheckpoint()
		require.NoError(t, err)

		// 应该只返回检查点之后的 2 个条目
		assert.Len(t, entries, 2)
	})

	t.Run("没有检查点时恢复全部", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		// 写入条目但不创建检查点
		count := 5
		for i := 0; i < count; i++ {
			entry := createTestEntry(t, "key", "value")
			require.NoError(t, wal.Append(entry))
		}

		// 恢复（没有检查点）
		entries, _, err := wal.RecoverToCheckpoint()
		require.NoError(t, err)
		assert.Len(t, entries, count)
	})
}

// ========================================
// 并发测试
// ========================================

// TestMetadataWAL_ConcurrentAppend 测试并发追加
func TestMetadataWAL_ConcurrentAppend(t *testing.T) {
	wal, _ := createTestWAL(t)
	defer wal.Close()

	// 并发追加
	concurrency := 10
	entriesPerGoroutine := 10

	errCh := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		go func() {
			for j := 0; j < entriesPerGoroutine; j++ {
				entry := createTestEntry(t, "key", "value")
				if err := wal.Append(entry); err != nil {
					errCh <- err
					return
				}
			}
			errCh <- nil
		}()
	}

	// 收集错误
	for i := 0; i < concurrency; i++ {
		assert.NoError(t, <-errCh)
	}

	// 验证总数
	stats, err := wal.GetStats()
	require.NoError(t, err)
	assert.Equal(t, uint64(concurrency*entriesPerGoroutine), stats.Entries)
}

// ========================================
// 边界情况测试
// ========================================

// TestMetadataWAL_EdgeCases 测试边界情况
func TestMetadataWAL_EdgeCases(t *testing.T) {
	t.Run("空键", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		entry := createTestEntry(t, "", "value")
		err := wal.Append(entry)
		assert.NoError(t, err)
	})

	t.Run("空值", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		entry := createTestEntry(t, "key", "")
		err := wal.Append(entry)
		assert.NoError(t, err)
	})

	t.Run("大值", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		largeValue := make([]byte, 1024*1024) // 1MB
		entry := createTestEntry(t, "key", "value")
		entry.Value = largeValue

		err := wal.Append(entry)
		assert.NoError(t, err)
	})

	t.Run("长键", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		longKey := string(make([]byte, 10000))
		entry := createTestEntry(t, longKey, "value")

		err := wal.Append(entry)
		assert.NoError(t, err)
	})
}

// ========================================
// WAL Rotation 测试
// ========================================

// TestWALRotationManager 测试WAL轮转管理器
func TestWALRotationManager(t *testing.T) {
	t.Run("创建轮转管理器", func(t *testing.T) {
		tmpDir := t.TempDir()
		walPath := filepath.Join(tmpDir, "wal", "metadata.wal")

		manager, err := NewWALRotationManager(walPath, 1024*1024) // 1MB
		require.NoError(t, err)
		defer manager.Close()

		assert.NotNil(t, manager)
	})

	t.Run("追加条目", func(t *testing.T) {
		tmpDir := t.TempDir()
		walPath := filepath.Join(tmpDir, "wal", "metadata.wal")

		manager, err := NewWALRotationManager(walPath, 1024*1024)
		require.NoError(t, err)
		defer manager.Close()

		entry := createTestEntry(t, "key", "value")
		err = manager.Append(entry)
		assert.NoError(t, err)
	})

	t.Run("恢复所有WAL", func(t *testing.T) {
		tmpDir := t.TempDir()
		walPath := filepath.Join(tmpDir, "wal", "metadata.wal")

		manager, err := NewWALRotationManager(walPath, 1024*1024)
		require.NoError(t, err)

		// 写入条目
		for i := 0; i < 10; i++ {
			entry := createTestEntry(t, "key", "value")
			require.NoError(t, manager.Append(entry))
		}

		// 恢复
		entries, err := manager.Recover()
		require.NoError(t, err)
		assert.Len(t, entries, 10)

		require.NoError(t, manager.Close())
	})

	t.Run("获取统计信息", func(t *testing.T) {
		tmpDir := t.TempDir()
		walPath := filepath.Join(tmpDir, "wal", "metadata.wal")

		manager, err := NewWALRotationManager(walPath, 1024*1024)
		require.NoError(t, err)
		defer manager.Close()

		stats, err := manager.GetRotationStats()
		require.NoError(t, err)
		assert.NotNil(t, stats)
		assert.Equal(t, 0, stats.CurrentFileIndex)
	})

	t.Run("Sync 刷盘", func(t *testing.T) {
		tmpDir := t.TempDir()
		walPath := filepath.Join(tmpDir, "wal", "metadata.wal")

		manager, err := NewWALRotationManager(walPath, 1024*1024)
		require.NoError(t, err)
		defer manager.Close()

		// 写入一些数据
		entry := createTestEntry(t, "key", "value")
		require.NoError(t, manager.Append(entry))

		// 测试 Sync
		err = manager.Sync()
		assert.NoError(t, err, "Sync 应该成功")
	})

	t.Run("Sync 已关闭的管理器返回错误", func(t *testing.T) {
		tmpDir := t.TempDir()
		walPath := filepath.Join(tmpDir, "wal", "metadata.wal")

		manager, err := NewWALRotationManager(walPath, 1024*1024)
		require.NoError(t, err)

		// 关闭管理器
		require.NoError(t, manager.Close())

		// Sync 应该返回错误
		err = manager.Sync()
		assert.Error(t, err, "Sync 已关闭的管理器应该返回错误")
	})

	t.Run("Truncate 截断", func(t *testing.T) {
		tmpDir := t.TempDir()
		walPath := filepath.Join(tmpDir, "wal", "metadata.wal")

		manager, err := NewWALRotationManager(walPath, 1024*1024)
		require.NoError(t, err)
		defer manager.Close()

		// 写入一些数据
		for i := 0; i < 5; i++ {
			entry := createTestEntry(t, "key", "value")
			require.NoError(t, manager.Append(entry))
		}

		// 获取当前大小
		stats, err := manager.GetRotationStats()
		require.NoError(t, err)
		truncateOffset := stats.TotalSize / 2

		// 测试 Truncate
		err = manager.Truncate(truncateOffset)
		assert.NoError(t, err, "Truncate 应该成功")
	})

	t.Run("Truncate 已关闭的管理器返回错误", func(t *testing.T) {
		tmpDir := t.TempDir()
		walPath := filepath.Join(tmpDir, "wal", "metadata.wal")

		manager, err := NewWALRotationManager(walPath, 1024*1024)
		require.NoError(t, err)

		// 关闭管理器
		require.NoError(t, manager.Close())

		// Truncate 应该返回错误
		err = manager.Truncate(0)
		assert.Error(t, err, "Truncate 已关闭的管理器应该返回错误")
	})

	t.Run("Append 已关闭的管理器返回错误", func(t *testing.T) {
		tmpDir := t.TempDir()
		walPath := filepath.Join(tmpDir, "wal", "metadata.wal")

		manager, err := NewWALRotationManager(walPath, 1024*1024)
		require.NoError(t, err)

		// 关闭管理器
		require.NoError(t, manager.Close())

		// Append 应该返回错误
		entry := createTestEntry(t, "key", "value")
		err = manager.Append(entry)
		assert.Error(t, err, "Append 已关闭的管理器应该返回错误")
	})

	t.Run("Recover 已关闭的管理器返回错误", func(t *testing.T) {
		tmpDir := t.TempDir()
		walPath := filepath.Join(tmpDir, "wal", "metadata.wal")

		manager, err := NewWALRotationManager(walPath, 1024*1024)
		require.NoError(t, err)

		// 关闭管理器
		require.NoError(t, manager.Close())

		// Recover 应该返回错误
		_, err = manager.Recover()
		assert.Error(t, err, "Recover 已关闭的管理器应该返回错误")
	})

	t.Run("GetRotationStats 已关闭的管理器返回错误", func(t *testing.T) {
		tmpDir := t.TempDir()
		walPath := filepath.Join(tmpDir, "wal", "metadata.wal")

		manager, err := NewWALRotationManager(walPath, 1024*1024)
		require.NoError(t, err)

		// 关闭管理器
		require.NoError(t, manager.Close())

		// GetRotationStats 应该返回错误
		_, err = manager.GetRotationStats()
		assert.Error(t, err, "GetRotationStats 已关闭的管理器应该返回错误")
	})
}

// ========================================
// WAL Batch Writer 测试
// ========================================

// TestWALBatchWriter 测试批量写入器
func TestWALBatchWriter(t *testing.T) {
	t.Run("创建批量写入器", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		writer := NewWALBatchWriter(wal, 10)
		assert.NotNil(t, writer)
		assert.Equal(t, 0, writer.BufferedCount())
	})

	t.Run("批量追加", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		writer := NewWALBatchWriter(wal, 10)

		// 追加 5 个条目
		for i := 0; i < 5; i++ {
			entry := createTestEntry(t, "key", "value")
			count, err := writer.Append(entry)
			assert.NoError(t, err)
			assert.Equal(t, 0, count) // 未达到批量大小，不刷新
		}

		assert.Equal(t, 5, writer.BufferedCount())
	})

	t.Run("达到批量大小自动刷新", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		batchSize := 5
		writer := NewWALBatchWriter(wal, batchSize)

		// 追加批量大小数量的条目
		for i := 0; i < batchSize; i++ {
			entry := createTestEntry(t, "key", "value")
			count, err := writer.Append(entry)
			assert.NoError(t, err)

			if i == batchSize-1 {
				// 最后一个应该触发刷新
				assert.Equal(t, batchSize, count)
			}
		}

		assert.Equal(t, 0, writer.BufferedCount())
	})

	t.Run("手动刷新", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		writer := NewWALBatchWriter(wal, 100)

		// 追加少量条目
		for i := 0; i < 3; i++ {
			entry := createTestEntry(t, "key", "value")
			_, err := writer.Append(entry)
			assert.NoError(t, err)
		}

		assert.Equal(t, 3, writer.BufferedCount())

		// 手动刷新
		count, err := writer.Flush()
		assert.NoError(t, err)
		assert.Equal(t, 3, count)
		assert.Equal(t, 0, writer.BufferedCount())
	})

	t.Run("批量追加数组", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		writer := NewWALBatchWriter(wal, 100)

		entries := []*WALEntry{}
		for i := 0; i < 10; i++ {
			entries = append(entries, createTestEntry(t, "key", "value"))
		}

		count, err := writer.AppendBatch(entries)
		assert.NoError(t, err)
		assert.Equal(t, 0, count) // 未达到批量大小
		assert.Equal(t, 10, writer.BufferedCount())
	})

	t.Run("关闭时自动刷新", func(t *testing.T) {
		wal, _ := createTestWAL(t)

		writer := NewWALBatchWriter(wal, 100)

		// 追加少量条目
		for i := 0; i < 3; i++ {
			entry := createTestEntry(t, "key", "value")
			_, err := writer.Append(entry)
			assert.NoError(t, err)
		}

		assert.Equal(t, 3, writer.BufferedCount())

		// 关闭应该自动刷新
		err := writer.Close()
		assert.NoError(t, err)

		// 验证WAL中有数据
		stats, err := wal.GetStats()
		require.NoError(t, err)
		assert.Equal(t, uint64(3), stats.Entries)

		defer wal.Close()
	})

	t.Run("追加nil条目", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		writer := NewWALBatchWriter(wal, 10)

		_, err := writer.Append(nil)
		assert.Error(t, err)
	})

	t.Run("关闭后追加", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		writer := NewWALBatchWriter(wal, 10)
		require.NoError(t, writer.Close())

		entry := createTestEntry(t, "key", "value")
		_, err := writer.Append(entry)
		assert.Error(t, err)
	})
}

// ========================================
// 文件系统测试
// ========================================

// TestWAL_FileSystem 测试文件系统相关
func TestWAL_FileSystem(t *testing.T) {
	t.Run("WAL文件持久化", func(t *testing.T) {
		tmpDir := t.TempDir()
		walPath := filepath.Join(tmpDir, "test.wal")

		// 创建WAL并写入数据
		wal1, err := NewMetadataWAL(walPath)
		require.NoError(t, err)

		entry := createTestEntry(t, "key1", "value1")
		require.NoError(t, wal1.Append(entry))
		require.NoError(t, wal1.Sync())
		require.NoError(t, wal1.Close())

		// 重新打开并恢复
		wal2, err := NewMetadataWAL(walPath)
		require.NoError(t, err)
		defer wal2.Close()

		entries, err := wal2.Recover()
		require.NoError(t, err)
		assert.Len(t, entries, 1)
		assert.Equal(t, []byte("key1"), entries[0].Key)
	})

	t.Run("目录不存在时自动创建", func(t *testing.T) {
		tmpDir := t.TempDir()
		walPath := filepath.Join(tmpDir, "subdir1", "subdir2", "test.wal")

		// 父目录不存在
		_, err := os.Stat(filepath.Dir(walPath))
		assert.True(t, os.IsNotExist(err))

		// 创建WAL应该自动创建目录
		wal, err := NewMetadataWAL(walPath)
		require.NoError(t, err)
		defer wal.Close()

		// 验证目录已创建
		_, err = os.Stat(filepath.Dir(walPath))
		assert.NoError(t, err)
	})
}

// ========================================
// 压力测试
// ========================================

// TestWAL_StressTest 压力测试
func TestWAL_StressTest(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过压力测试")
	}

	t.Run("大量写入", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		count := 10000
		for i := 0; i < count; i++ {
			entry := createTestEntry(t, "key", "value")
			require.NoError(t, wal.Append(entry))
		}

		stats, err := wal.GetStats()
		require.NoError(t, err)
		assert.Equal(t, uint64(count), stats.Entries)
	})

	t.Run("写入后恢复", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		count := 1000
		for i := 0; i < count; i++ {
			entry := createTestEntry(t, "key", "value")
			require.NoError(t, wal.Append(entry))
		}

		// 恢复
		entries, err := wal.Recover()
		require.NoError(t, err)
		assert.Len(t, entries, count)
	})
}

// ========================================
// 错误处理测试
// ========================================

// TestWAL_ErrorHandling 测试错误处理
func TestWAL_ErrorHandling(t *testing.T) {
	t.Run("无效路径", func(t *testing.T) {
		// 使用无效的路径（在某些系统上可能无法创建）
		walPath := "/dev/null/invalid/test.wal"

		_, err := NewMetadataWAL(walPath)
		assert.Error(t, err)
	})

	t.Run("截断负偏移量", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		entry := createTestEntry(t, "key", "value")
		require.NoError(t, wal.Append(entry))

		// 截断到负偏移量（无效，但测试行为）
		err := wal.Truncate(-1)
		assert.Error(t, err)
	})
}

// ========================================
// 接口测试
// ========================================

// TestWALInterface 测试WAL接口实现
func TestWALInterface(t *testing.T) {
	t.Run("WAL接口实现", func(t *testing.T) {
		wal, _ := createTestWAL(t)
		defer wal.Close()

		// 验证实现了 WAL 接口
		var _ WAL = wal
		_ = wal.Append
		_ = wal.Recover
		_ = wal.Truncate
		_ = wal.Sync
		_ = wal.Close
	})
}

// ========================================
// WAL Checkpoint 测试
// ========================================

// TestWALCheckpoint 测试WAL快照功能
func TestWALCheckpoint(t *testing.T) {
	t.Run("LoadSnapshot无快照", func(t *testing.T) {
		tmpDir := t.TempDir()
		walPath := filepath.Join(tmpDir, "test.wal")

		wal, err := NewMetadataWAL(walPath)
		require.NoError(t, err)
		defer wal.Close()

		// 测试 RecoveryManager 加载空快照
		recoveryMgr, err := NewRecoveryManager(tmpDir, tmpDir, tmpDir)
		require.NoError(t, err)
		_ = recoveryMgr

		// 无快照时应该返回空数据
		data, err := recoveryMgr.Recover()
		require.NoError(t, err)
		assert.Empty(t, data)
	})

	t.Run("Checkpoint基本操作", func(t *testing.T) {
		tmpDir := t.TempDir()

		// 创建恢复管理器
		recoveryMgr, err := NewRecoveryManager(
			filepath.Join(tmpDir, "checkpoint"),
			filepath.Join(tmpDir, "snapshot"),
			filepath.Join(tmpDir, "wal"),
		)
		require.NoError(t, err)
		assert.NotNil(t, recoveryMgr)
	})
}

// TestWALRotation 测试WAL轮转相关功能
func TestWALRotation(t *testing.T) {
	t.Run("ListAllWALFiles列出所有文件", func(t *testing.T) {
		tmpDir := t.TempDir()
		walPath := filepath.Join(tmpDir, "metadata.wal")

		// 创建一些WAL文件
		walDir := filepath.Dir(walPath)
		_, err := os.Create(filepath.Join(walDir, "metadata-0001.wal"))
		require.NoError(t, err)
		_, err = os.Create(filepath.Join(walDir, "metadata-0002.wal"))
		require.NoError(t, err)

		manager, err := NewWALRotationManager(walPath, 1024*1024)
		require.NoError(t, err)
		defer manager.Close()

		files, err := manager.ListAllWALFiles()
		require.NoError(t, err)
		// 应该包含当前WAL文件
		assert.GreaterOrEqual(t, len(files), 1)
	})

	t.Run("checkRotation检查轮转", func(t *testing.T) {
		tmpDir := t.TempDir()
		walPath := filepath.Join(tmpDir, "metadata.wal")

		// 创建一个小的最大大小来触发轮转
		manager, err := NewWALRotationManager(walPath, 100) // 100 bytes
		require.NoError(t, err)
		defer manager.Close()

		// 添加足够多的条目来触发轮转
		entry := createTestEntry(t, "key1", "value1")
		for i := 0; i < 5; i++ {
			require.NoError(t, manager.Append(entry))
		}

		// 验证轮转发生了
		stats, err := manager.GetRotationStats()
		require.NoError(t, err)
		assert.Greater(t, stats.TotalFiles, 0)
	})
}

// TestWALBatchChecksums 测试批量校验和
func TestWALBatchChecksums(t *testing.T) {
	t.Run("verifyBatchChecksums空批次", func(t *testing.T) {
		// 空批次应该返回空的校验和数组
		checksums := verifyBatchChecksums([]*WALEntry{})
		assert.Empty(t, checksums)
	})

	t.Run("verifyBatchChecksums有效条目", func(t *testing.T) {
		entry := createTestEntry(t, "key1", "value1")

		// 应该返回校验和数组
		checksums := verifyBatchChecksums([]*WALEntry{entry})
		assert.Len(t, checksums, 1)
		assert.NotZero(t, checksums[0])
	})
}

// TestWALRecoveryEdgeCases 测试WAL恢复边界情况
func TestWALRecoveryEdgeCases(t *testing.T) {
	t.Run("RecoverFromOffset从中间恢复", func(t *testing.T) {
		tmpDir := t.TempDir()
		walPath := filepath.Join(tmpDir, "test.wal")

		wal, err := NewMetadataWAL(walPath)
		require.NoError(t, err)

		// 写入多个条目
		for i := 0; i < 10; i++ {
			entry := createTestEntry(t, fmt.Sprintf("key%d", i), fmt.Sprintf("value%d", i))
			require.NoError(t, wal.Append(entry))
		}

		// 获取当前偏移量
		stats, err := wal.GetStats()
		require.NoError(t, err)
		midOffset := stats.Offset / 2

		// 从中间偏移量恢复
		entries, err := wal.RecoverFromOffset(midOffset)
		require.NoError(t, err)
		assert.Less(t, len(entries), 10) // 应该少于全部10个
	})

	t.Run("RecoverBatch批量恢复", func(t *testing.T) {
		tmpDir := t.TempDir()
		walPath := filepath.Join(tmpDir, "test.wal")

		wal, err := NewMetadataWAL(walPath)
		require.NoError(t, err)
		defer wal.Close()

		// 写入多个条目
		count := 20
		for i := 0; i < count; i++ {
			entry := createTestEntry(t, fmt.Sprintf("key%d", i), fmt.Sprintf("value%d", i))
			require.NoError(t, wal.Append(entry))
		}

		// 批量恢复 - maxCount=10 应该返回全部20条
		entries, _, err := wal.RecoverBatch(100) // 使用足够大的maxCount
		require.NoError(t, err)
		assert.Len(t, entries, count)
	})
}

// TestWALBatchOperations 测试WAL批量操作
func TestWALBatchOperations(t *testing.T) {
	t.Run("BatchWriter批量写入", func(t *testing.T) {
		tmpDir := t.TempDir()
		walPath := filepath.Join(tmpDir, "test.wal")

		wal, err := NewMetadataWAL(walPath)
		require.NoError(t, err)
		defer wal.Close()

		writer := NewWALBatchWriter(wal, 3)
		defer writer.Close()

		// 批量写入
		entries := []*WALEntry{
			createTestEntry(t, "key1", "value1"),
			createTestEntry(t, "key2", "value2"),
			createTestEntry(t, "key3", "value3"),
		}

		count, err := writer.AppendBatch(entries)
		require.NoError(t, err)
		assert.Equal(t, 3, count)

		// 验证数据已写入
		recovered, err := wal.Recover()
		require.NoError(t, err)
		assert.Len(t, recovered, 3)
	})

	t.Run("BatchReader批量读取", func(t *testing.T) {
		tmpDir := t.TempDir()
		walPath := filepath.Join(tmpDir, "test.wal")

		wal, err := NewMetadataWAL(walPath)
		require.NoError(t, err)

		// 写入多个条目
		for i := 0; i < 15; i++ {
			entry := createTestEntry(t, fmt.Sprintf("key%d", i), fmt.Sprintf("value%d", i))
			require.NoError(t, wal.Append(entry))
		}

		// 关闭WAL以确保持久化
		require.NoError(t, wal.Close())

		// 重新打开WAL文件
		file, err := os.Open(walPath)
		require.NoError(t, err)
		defer file.Close()

		reader := NewWALBatchReader(file, 4096)

		// 读取第一批
		batch1, err := reader.ReadBatch(5)
		require.NoError(t, err)
		assert.Len(t, batch1, 5)

		// 读取第二批
		batch2, err := reader.ReadBatch(5)
		require.NoError(t, err)
		assert.Len(t, batch2, 5)

		// 读取第三批
		batch3, err := reader.ReadBatch(5)
		require.NoError(t, err)
		assert.Len(t, batch3, 5)
	})
}

// TestWALDataIntegrity 测试数据完整性
func TestWALDataIntegrity(t *testing.T) {
	t.Run("编解码后数据一致性", func(t *testing.T) {
		tmpDir := t.TempDir()
		walPath := filepath.Join(tmpDir, "test.wal")

		wal, err := NewMetadataWAL(walPath)
		require.NoError(t, err)
		defer wal.Close()

		// 创建包含所有类型的条目
		originalEntries := []*WALEntry{
			createTestEntry(t, "key1", "value1"),
			createTestEntry(t, "key2", "value2"),
			createTestEntry(t, "key3", ""),
		}

		// 写入条目
		for _, entry := range originalEntries {
			require.NoError(t, wal.Append(entry))
		}

		// 恢复条目
		recoveredEntries, err := wal.Recover()
		require.NoError(t, err)
		assert.Len(t, recoveredEntries, len(originalEntries))

		// 验证每个条目
		for i, orig := range originalEntries {
			recovered := recoveredEntries[i]
			assert.Equal(t, orig.Key, recovered.Key)
			// Value 可能为 nil，检查长度
			if len(orig.Value) == 0 {
				assert.Equal(t, 0, len(recovered.Value))
			} else {
				assert.Equal(t, orig.Value, recovered.Value)
			}
			assert.Equal(t, orig.Type, recovered.Type)
		}
	})
}

// TestWALConcurrentAccess 测试并发访问
func TestWALConcurrentAccess(t *testing.T) {
	t.Run("顺序写入-基本功能验证", func(t *testing.T) {
		// 先验证基本功能是否正常
		tmpDir := t.TempDir()
		walPath := filepath.Join(tmpDir, "test-sequential.wal")

		wal, err := NewMetadataWAL(walPath)
		require.NoError(t, err)
		defer wal.Close()

		// 顺序写入 5 个条目
		const count = 5
		for i := 0; i < count; i++ {
			entry := createTestEntry(t, fmt.Sprintf("key%d", i), fmt.Sprintf("value%d", i))
			require.NoError(t, wal.Append(entry))
		}

		// 关闭 WAL
		require.NoError(t, wal.Close())

		// 验证文件大小是否正确
		fileInfo, err := os.Stat(walPath)
		require.NoError(t, err)
		t.Logf("WAL 文件大小: %d bytes", fileInfo.Size())

		// 重新打开并恢复
		wal2, err := NewMetadataWAL(walPath)
		require.NoError(t, err)
		defer wal2.Close()

		entries, err := wal2.Recover()
		require.NoError(t, err)
		assert.Equal(t, count, len(entries), "应该恢复所有条目")
	})

	t.Run("并发写入-验证文件完整性", func(t *testing.T) {
		// 暂时跳过 Recover 验证，只测试 Append 是否成功
		tmpDir := t.TempDir()
		walPath := filepath.Join(tmpDir, "test-concurrent-skip.wal")

		wal, err := NewMetadataWAL(walPath)
		require.NoError(t, err)
		defer wal.Close()

		// 并发写入
		const concurrency = 5
		var wg sync.WaitGroup
		successCh := make(chan bool, concurrency)

		for i := 0; i < concurrency; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				entry := createTestEntry(t, fmt.Sprintf("key%d", idx), fmt.Sprintf("value%d", idx))
				if err := wal.Append(entry); err != nil {
					t.Errorf("goroutine %d Append 失败: %v", idx, err)
					successCh <- false
				} else {
					successCh <- true
				}
			}(i)
		}

		wg.Wait()
		close(successCh)

		// 统计成功次数
		success := 0
		for ok := range successCh {
			if ok {
				success++
			}
		}
		t.Logf("并发写入成功: %d/%d", success, concurrency)
		assert.Equal(t, concurrency, success, "所有并发写入都应该成功")

		// 重新打开并恢复（验证并发写入后的文件完整性）
		wal2, err := NewMetadataWAL(walPath)
		require.NoError(t, err)
		defer wal2.Close()

		entries, err := wal2.Recover()
		require.NoError(t, err)
		assert.Equal(t, concurrency, len(entries), "应该恢复所有并发写入的条目")
	})
}

// TestWALBatchWriterErrors 测试 WAL Batch Writer 错误处理
func TestWALBatchWriterErrors(t *testing.T) {
	t.Run("Append 已关闭的批量写入器", func(t *testing.T) {
		tmpDir := t.TempDir()
		walPath := filepath.Join(tmpDir, "wal", "metadata.wal")

		wal, err := NewMetadataWAL(walPath)
		require.NoError(t, err)
		defer wal.Close()

		writer := NewWALBatchWriter(wal, 10)

		// 关闭批量写入器
		require.NoError(t, writer.Close())

		// 尝试追加应该返回错误
		entry := &WALEntry{
			Type:  WALTypePut,
			Key:   []byte("key"),
			Value: []byte("value"),
		}

		_, err = writer.Append(entry)
		assert.Error(t, err, "Append 已关闭的批量写入器应该返回错误")
	})

	t.Run("Append nil 条目", func(t *testing.T) {
		tmpDir := t.TempDir()
		walPath := filepath.Join(tmpDir, "wal", "metadata.wal")

		wal, err := NewMetadataWAL(walPath)
		require.NoError(t, err)
		defer wal.Close()

		writer := NewWALBatchWriter(wal, 10)
		defer writer.Close()

		// 尝试追加 nil 条目应该返回错误
		_, err = writer.Append(nil)
		assert.Error(t, err, "Append nil 条目应该返回错误")
	})

	t.Run("AppendBatch 已关闭的批量写入器", func(t *testing.T) {
		tmpDir := t.TempDir()
		walPath := filepath.Join(tmpDir, "wal", "metadata.wal")

		wal, err := NewMetadataWAL(walPath)
		require.NoError(t, err)
		defer wal.Close()

		writer := NewWALBatchWriter(wal, 10)

		// 关闭批量写入器
		require.NoError(t, writer.Close())

		// 尝试批量追加应该返回错误
		entries := []*WALEntry{
			{Type: WALTypePut, Key: []byte("key"), Value: []byte("value")},
		}

		_, err = writer.AppendBatch(entries)
		assert.Error(t, err, "AppendBatch 已关闭的批量写入器应该返回错误")
	})

	t.Run("AppendBatch 空数组", func(t *testing.T) {
		tmpDir := t.TempDir()
		walPath := filepath.Join(tmpDir, "wal", "metadata.wal")

		wal, err := NewMetadataWAL(walPath)
		require.NoError(t, err)
		defer wal.Close()

		writer := NewWALBatchWriter(wal, 10)
		defer writer.Close()

		// 空数组应该返回 0, nil
		count, err := writer.AppendBatch([]*WALEntry{})
		assert.NoError(t, err)
		assert.Equal(t, 0, count)
	})

	t.Run("AppendBatch 包含 nil 条目", func(t *testing.T) {
		tmpDir := t.TempDir()
		walPath := filepath.Join(tmpDir, "wal", "metadata.wal")

		wal, err := NewMetadataWAL(walPath)
		require.NoError(t, err)
		defer wal.Close()

		writer := NewWALBatchWriter(wal, 10)
		defer writer.Close()

		// 包含 nil 条目的数组应该跳过 nil 条目
		entries := []*WALEntry{
			{Type: WALTypePut, Key: []byte("key1"), Value: []byte("value1")},
			nil,
			{Type: WALTypePut, Key: []byte("key2"), Value: []byte("value2")},
		}

		count, err := writer.AppendBatch(entries)
		assert.NoError(t, err, "AppendBatch 应该跳过 nil 条目并继续")
		// 由于批量大小是 10，条目只有 2 个有效条目（跳过 nil），不会触发刷新
		// 所以 count 应该是 0（除非手动 Flush）
		assert.Equal(t, 0, count, "未达到批量大小，不应该自动刷新")
	})

	t.Run("Flush 已关闭的批量写入器", func(t *testing.T) {
		tmpDir := t.TempDir()
		walPath := filepath.Join(tmpDir, "wal", "metadata.wal")

		wal, err := NewMetadataWAL(walPath)
		require.NoError(t, err)
		defer wal.Close()

		writer := NewWALBatchWriter(wal, 10)

		// 写入一些数据
		entry := &WALEntry{
			Type:  WALTypePut,
			Key:   []byte("key"),
			Value: []byte("value"),
		}
		_, err = writer.Append(entry)
		require.NoError(t, err)

		// 关闭批量写入器
		require.NoError(t, writer.Close())

		// 尝试刷新应该返回错误
		_, err = writer.Flush()
		assert.Error(t, err, "Flush 已关闭的批量写入器应该返回错误")
	})

	t.Run("Close 多次调用", func(t *testing.T) {
		tmpDir := t.TempDir()
		walPath := filepath.Join(tmpDir, "wal", "metadata.wal")

		wal, err := NewMetadataWAL(walPath)
		require.NoError(t, err)
		defer wal.Close()

		writer := NewWALBatchWriter(wal, 10)

		// 第一次关闭
		err = writer.Close()
		assert.NoError(t, err)

		// 第二次关闭应该成功（幂等）
		err = writer.Close()
		assert.NoError(t, err, "Close 应该是幂等的")
	})

	t.Run("Close 刷新剩余缓冲区", func(t *testing.T) {
		tmpDir := t.TempDir()
		walPath := filepath.Join(tmpDir, "wal", "metadata.wal")

		wal, err := NewMetadataWAL(walPath)
		require.NoError(t, err)
		defer wal.Close()

		writer := NewWALBatchWriter(wal, 100) // 大批量大小，避免自动刷新

		// 写入一些数据但不达到批量大小
		for i := 0; i < 5; i++ {
			entry := &WALEntry{
				Type:  WALTypePut,
				Key:   []byte(fmt.Sprintf("key%d", i)),
				Value: []byte(fmt.Sprintf("value%d", i)),
			}
			_, err = writer.Append(entry)
			assert.NoError(t, err)
		}

		// 验证缓冲区中有数据
		assert.Equal(t, 5, writer.BufferedCount(), "缓冲区应该有 5 个条目")

		// 关闭应该自动刷新剩余缓冲区
		err = writer.Close()
		assert.NoError(t, err)

		// 验证数据已写入
		entries, err := wal.Recover()
		assert.NoError(t, err)
		assert.Equal(t, 5, len(entries), "应该恢复所有 5 个条目")
	})
}

// TestWALBatchReaderErrors 测试 WAL Batch Reader 错误处理
func TestWALBatchReaderErrors(t *testing.T) {
	t.Run("ReadBatch 读取失败（无效魔术字）", func(t *testing.T) {
		// 创建一个无效的 WAL 文件
		data := []byte("BAD!") // 无效的魔术字
		reader := bytes.NewReader(data)

		batchReader := NewWALBatchReader(reader, 1024)

		_, err := batchReader.ReadBatch(10)
		assert.Error(t, err, "读取无效魔术字应该返回错误")
	})

	t.Run("ReadBatch 读取空数据", func(t *testing.T) {
		// 创建一个空的 WAL 文件
		data := []byte{}
		reader := bytes.NewReader(data)

		batchReader := NewWALBatchReader(reader, 1024)

		entries, err := batchReader.ReadBatch(10)
		assert.NoError(t, err)
		assert.Equal(t, 0, len(entries), "空数据应该返回空条目列表")
	})
}
