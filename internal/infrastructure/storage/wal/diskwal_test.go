// Package wal 的 DiskWAL 测试
package wal

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestWAL(t *testing.T) *DiskWAL {
	t.Helper()

	dir := t.TempDir()
	config := &WALConfig{
		Dir:         dir,
		SegmentSize: 64 * 1024 * 1024,
		SyncPolicy:  SyncPolicyEveryWrite,
	}

	wal, err := NewDiskWAL(config)
	require.NoError(t, err)
	require.NotNil(t, wal)

	return wal
}

func TestNewDiskWAL(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		dir := t.TempDir()
		config := &WALConfig{
			Dir:         dir,
			SegmentSize: 64 * 1024 * 1024,
		}

		wal, err := NewDiskWAL(config)
		require.NoError(t, err)
		assert.NotNil(t, wal)

		// 检查文件是否创建
		files, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(files), 1)

		// 检查文件名格式
		hasWALFile := false
		for _, file := range files {
			if filepath.Ext(file.Name()) == ".wal" {
				hasWALFile = true
				break
			}
		}
		assert.True(t, hasWALFile, "should have .wal file")
	})

	t.Run("invalid config - empty dir", func(t *testing.T) {
		config := &WALConfig{
			Dir:         "",
			SegmentSize: 64 * 1024 * 1024,
		}

		wal, err := NewDiskWAL(config)
		assert.Error(t, err)
		assert.Nil(t, wal)
		assert.ErrorIs(t, err, ErrInvalidWALConfig)
	})
}

func TestDiskWAL_Append(t *testing.T) {
	t.Run("append single entry", func(t *testing.T) {
		wal := setupTestWAL(t)
		defer wal.Close()

		entry := NewWALEntry(WALTypeInsert, 0, []byte("key1"), []byte("value1"), LSNInvalid)

		lsn, err := wal.Append(entry)
		require.NoError(t, err)
		assert.Equal(t, LSN(1), lsn)
		assert.Equal(t, LSN(1), entry.LSN)
	})

	t.Run("append multiple entries", func(t *testing.T) {
		wal := setupTestWAL(t)
		defer wal.Close()

		for i := 1; i <= 10; i++ {
			key := []byte{byte(i)}
			value := []byte{byte(i + 100)}
			entry := NewWALEntry(WALTypeInsert, uint64(i), key, value, LSN(i-1))

			lsn, err := wal.Append(entry)
			require.NoError(t, err)
			assert.Equal(t, LSN(i), lsn)
		}

		stats := wal.GetStats()
		assert.Equal(t, int64(10), stats.TotalEntries)
		assert.Equal(t, LSN(10), stats.CurrentLSN)
	})

	t.Run("append after closed", func(t *testing.T) {
		wal := setupTestWAL(t)

		// 关闭 WAL
		require.NoError(t, wal.Close())

		entry := NewWALEntry(WALTypeInsert, 0, []byte("key"), []byte("value"), LSNInvalid)

		lsn, err := wal.Append(entry)
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrWALClosed)
		assert.Equal(t, LSNInvalid, lsn)
	})
}

func TestDiskWAL_Sync(t *testing.T) {
	t.Run("sync success", func(t *testing.T) {
		wal := setupTestWAL(t)
		defer wal.Close()

		err := wal.Sync()
		require.NoError(t, err)

		stats := wal.GetStats()
		assert.Greater(t, stats.SyncCount, int64(0))
	})

	t.Run("sync after closed", func(t *testing.T) {
		wal := setupTestWAL(t)
		require.NoError(t, wal.Close())

		err := wal.Sync()
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrWALClosed)
	})
}

func TestDiskWAL_AppendAsync(t *testing.T) {
	t.Run("append async success", func(t *testing.T) {
		wal := setupTestWAL(t)
		defer wal.Close()

		entry := NewWALEntry(WALTypeInsert, 0, []byte("key"), []byte("value"), LSNInvalid)

		task := wal.AppendAsync(context.Background(), entry)
		require.NotNil(t, task)

		lsn, err := task.Wait(context.Background())
		require.NoError(t, err)
		assert.Equal(t, LSN(1), lsn)
	})

	t.Run("append async multiple", func(t *testing.T) {
		wal := setupTestWAL(t)
		defer wal.Close()

		for i := 1; i <= 5; i++ {
			key := []byte{byte(i)}
			value := []byte{byte(i + 100)}
			entry := NewWALEntry(WALTypeInsert, uint64(i), key, value, LSN(i-1))

			task := wal.AppendAsync(context.Background(), entry)
			lsn, err := task.Wait(context.Background())
			require.NoError(t, err)
			assert.Equal(t, LSN(i), lsn)
		}

		stats := wal.GetStats()
		assert.Equal(t, int64(5), stats.TotalEntries)
	})
}

func TestDiskWAL_TruncateAsync(t *testing.T) {
	t.Run("truncate async", func(t *testing.T) {
		wal := setupTestWAL(t)
		defer wal.Close()

		// 先写入一些日志
		for i := 1; i <= 5; i++ {
			entry := NewWALEntry(WALTypeInsert, uint64(i), []byte{byte(i)}, []byte{byte(i + 100)}, LSN(i-1))
			_, err := wal.Append(entry)
			require.NoError(t, err)
		}

		// 截断到 LSN 3
		task := wal.TruncateAsync(context.Background(), LSN(3))
		_, err := task.Wait(context.Background())
		// TODO: 实现 Truncate 后应该成功
		_ = err
	})
}

func TestDiskWAL_Close(t *testing.T) {
	t.Run("close success", func(t *testing.T) {
		wal := setupTestWAL(t)

		err := wal.Close()
		require.NoError(t, err)

		// 再次关闭应该返回错误
		err = wal.Close()
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrWALClosed)
	})

	t.Run("close idempotent", func(t *testing.T) {
		wal := setupTestWAL(t)

		firstErr := wal.Close()
		secondErr := wal.Close()
		thirdErr := wal.Close()

		assert.NoError(t, firstErr)
		assert.Error(t, secondErr)
		assert.Error(t, thirdErr)
		assert.ErrorIs(t, secondErr, ErrWALClosed)
		assert.ErrorIs(t, thirdErr, ErrWALClosed)
	})
}

func TestDiskWAL_GetStats(t *testing.T) {
	t.Run("get initial stats", func(t *testing.T) {
		wal := setupTestWAL(t)
		defer wal.Close()

		stats := wal.GetStats()
		assert.Equal(t, LSN(0), stats.CurrentLSN)
		assert.Equal(t, int64(0), stats.TotalEntries)
		assert.Equal(t, int64(0), stats.TotalBytes)
	})

	t.Run("get stats after writes", func(t *testing.T) {
		wal := setupTestWAL(t)
		defer wal.Close()

		for i := 1; i <= 5; i++ {
			entry := NewWALEntry(WALTypeInsert, uint64(i), []byte{byte(i)}, []byte{byte(i + 100)}, LSN(i-1))
			_, err := wal.Append(entry)
			require.NoError(t, err)
		}

		stats := wal.GetStats()
		assert.Equal(t, LSN(5), stats.CurrentLSN)
		assert.Equal(t, int64(5), stats.TotalEntries)
		assert.Greater(t, stats.TotalBytes, int64(0))
		assert.Greater(t, stats.SyncCount, int64(0))
	})
}

func TestDiskWAL_Recover(t *testing.T) {
	t.Run("recover from empty directory", func(t *testing.T) {
		wal := setupTestWAL(t)
		defer wal.Close()

		entries, err := wal.Recover()
		require.NoError(t, err)
		assert.NotNil(t, entries)
		// 空目录应该返回空列表
		assert.Equal(t, 0, len(entries))
	})

	t.Run("recover after writes", func(t *testing.T) {
		dir := t.TempDir()
		config := &WALConfig{
			Dir:         dir,
			SegmentSize: 64 * 1024 * 1024,
		}

		wal, err := NewDiskWAL(config)
		require.NoError(t, err)

		// 写入一些日志
		for i := 1; i <= 5; i++ {
			entry := NewWALEntry(WALTypeInsert, uint64(i), []byte{byte(i)}, []byte{byte(i + 100)}, LSN(i-1))
			_, err := wal.Append(entry)
			require.NoError(t, err)
		}

		require.NoError(t, wal.Sync())
		require.NoError(t, wal.Close())

		// 重新打开 WAL
		wal2, err := NewDiskWAL(config)
		require.NoError(t, err)
		defer wal2.Close()

		// 恢复
		entries, err := wal2.Recover()
		require.NoError(t, err)
		// TODO: 实现完整的恢复逻辑后，应该能恢复所有日志
		_ = entries
	})
}

func TestCompletedWALTask(t *testing.T) {
	t.Run("completed task executes immediately", func(t *testing.T) {
		called := false
		fn := func() (LSN, error) {
			called = true
			return LSN(42), nil
		}

		task := NewCompletedWALTask(fn)
		lsn, err := task.Wait(context.Background())
		require.NoError(t, err)
		assert.Equal(t, LSN(42), lsn)
		assert.True(t, called)
	})
}

func TestCompletedTruncateTask(t *testing.T) {
	t.Run("completed task executes immediately", func(t *testing.T) {
		called := false
		fn := func() (struct{}, error) {
			called = true
			return struct{}{}, nil
		}

		task := NewCompletedTruncateTask(fn)
		_, err := task.Wait(context.Background())
		require.NoError(t, err)
		assert.True(t, called)
	})
}

// 基准测试
func BenchmarkDiskWAL_Append(b *testing.B) {
	dir := b.TempDir()
	config := &WALConfig{
		Dir:         dir,
		SegmentSize: 64 * 1024 * 1024,
		SyncPolicy:  SyncPolicyEveryWrite,
	}

	wal, err := NewDiskWAL(config)
	if err != nil {
		b.Fatal(err)
	}
	defer wal.Close()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		entry := NewWALEntry(
			WALTypeInsert,
			uint64(i),
			[]byte("benchmark-key"),
			[]byte("benchmark-value"),
			LSNInvalid,
		)
		if _, err := wal.Append(entry); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWALEntry_Marshal(b *testing.B) {
	entry := &WALEntry{
		LSN:       LSN(1),
		TxID:      100,
		Timestamp: 1234567890,
		Type:      WALTypeInsert,
		Key:       []byte("test-key"),
		Value:     []byte("test-value"),
		PrevLSN:   LSNInvalid,
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if _, err := MarshalWALEntry(entry); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWALEntry_Unmarshal(b *testing.B) {
	entry := &WALEntry{
		LSN:       LSN(1),
		TxID:      100,
		Timestamp: 1234567890,
		Type:      WALTypeInsert,
		Key:       []byte("test-key"),
		Value:     []byte("test-value"),
		PrevLSN:   LSNInvalid,
	}

	data, err := MarshalWALEntry(entry)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		unentry := &WALEntry{}
		if err := UnmarshalWALEntry(unentry, data); err != nil {
			b.Fatal(err)
		}
	}
}
