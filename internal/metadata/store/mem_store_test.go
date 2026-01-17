// Package store 存储层测试
package store

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/clock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMemoryMVStore_PutGet 测试基本的 Put 和 Get 操作
func TestMemoryMVStore_PutGet(t *testing.T) {
	tempDir := t.TempDir()
	options := &MVStoreOptions{
		DataDir:     tempDir,
		WALDir:      filepath.Join(tempDir, "wal"),
		EnableWAL:   false, // 测试时不启用 WAL，加快速度
		MaxVersions: 5,
	}

	store, err := NewMemoryMVStore(options)
	require.NoError(t, err)
	defer store.Close()

	// 测试 Put
	err = store.Put("key1", []byte("value1"))
	require.NoError(t, err)

	err = store.Put("key2", []byte("value2"))
	require.NoError(t, err)

	// 测试 Get
	value, err := store.Get("key1")
	require.NoError(t, err)
	assert.Equal(t, []byte("value1"), value)

	value, err = store.Get("key2")
	require.NoError(t, err)
	assert.Equal(t, []byte("value2"), value)

	// 测试不存在的 key
	_, err = store.Get("key3")
	assert.ErrorIs(t, err, ErrNotFound)
}

// TestMemoryMVStore_Delete 测试删除操作
func TestMemoryMVStore_Delete(t *testing.T) {
	tempDir := t.TempDir()
	options := &MVStoreOptions{
		DataDir:   tempDir,
		WALDir:    filepath.Join(tempDir, "wal"),
		EnableWAL: false,
	}

	store, err := NewMemoryMVStore(options)
	require.NoError(t, err)
	defer store.Close()

	// Put 一个值
	err = store.Put("key1", []byte("value1"))
	require.NoError(t, err)

	// 删除
	err = store.Delete("key1")
	require.NoError(t, err)

	// 验证已删除
	_, err = store.Get("key1")
	assert.ErrorIs(t, err, ErrNotFound)

	// 删除不存在的 key 应该成功
	err = store.Delete("key2")
	assert.NoError(t, err)
}

// TestMemoryMVStore_Exists 测试 Exists 操作
func TestMemoryMVStore_Exists(t *testing.T) {
	tempDir := t.TempDir()
	options := &MVStoreOptions{
		DataDir:   tempDir,
		WALDir:    filepath.Join(tempDir, "wal"),
		EnableWAL: false,
	}

	store, err := NewMemoryMVStore(options)
	require.NoError(t, err)
	defer store.Close()

	// 不存在的 key
	exists, err := store.Exists("key1")
	require.NoError(t, err)
	assert.False(t, exists)

	// Put 后应该存在
	err = store.Put("key1", []byte("value1"))
	require.NoError(t, err)

	exists, err = store.Exists("key1")
	require.NoError(t, err)
	assert.True(t, exists)

	// 删除后不存在
	err = store.Delete("key1")
	require.NoError(t, err)

	exists, err = store.Exists("key1")
	require.NoError(t, err)
	assert.False(t, exists)
}

// TestMemoryMVStore_GetVersion 测试版本查询
func TestMemoryMVStore_GetVersion(t *testing.T) {
	tempDir := t.TempDir()
	options := &MVStoreOptions{
		DataDir:     tempDir,
		WALDir:      filepath.Join(tempDir, "wal"),
		EnableWAL:   false,
		MaxVersions: 10,
	}

	store, err := NewMemoryMVStore(options)
	require.NoError(t, err)
	defer store.Close()

	// 写入多个版本
	require.NoError(t, store.Put("key1", []byte("value1")))
	time.Sleep(10 * time.Millisecond)

	require.NoError(t, store.Put("key1", []byte("value2")))
	time.Sleep(10 * time.Millisecond)

	require.NoError(t, store.Put("key1", []byte("value3")))

	// 获取所有版本信息，使用实际存储的时间戳
	infos, err := store.GetAllVersions("key1")
	require.NoError(t, err)
	require.Len(t, infos, 3)

	// 最新版本是 value3
	value, err := store.Get("key1")
	require.NoError(t, err)
	assert.Equal(t, []byte("value3"), value)

	// 获取历史版本 - 使用实际的时间戳
	// infos[0] 是 value1, infos[1] 是 value2, infos[2] 是 value3
	value, err = store.GetVersion("key1", infos[1].Timestamp)
	require.NoError(t, err)
	assert.Equal(t, []byte("value2"), value)

	value, err = store.GetVersion("key1", infos[0].Timestamp)
	require.NoError(t, err)
	assert.Equal(t, []byte("value1"), value)

	// 测试 nil 时间戳应该返回最新版本
	value, err = store.GetVersion("key1", nil)
	require.NoError(t, err)
	assert.Equal(t, []byte("value3"), value)
}

// TestMemoryMVStore_List 测试 List 操作
func TestMemoryMVStore_List(t *testing.T) {
	tempDir := t.TempDir()
	options := &MVStoreOptions{
		DataDir:   tempDir,
		WALDir:    filepath.Join(tempDir, "wal"),
		EnableWAL: false,
	}

	store, err := NewMemoryMVStore(options)
	require.NoError(t, err)
	defer store.Close()

	// 写入多个 key
	for i := 1; i <= 10; i++ {
		err = store.Put(fmt.Sprintf("key%d", i), []byte(fmt.Sprintf("value%d", i)))
		require.NoError(t, err)
	}

	// 测试无分页
	keys, err := store.List(0, 0)
	require.NoError(t, err)
	assert.Len(t, keys, 10)

	// 测试分页
	keys, err = store.List(0, 5)
	require.NoError(t, err)
	assert.Len(t, keys, 5)

	keys, err = store.List(5, 5)
	require.NoError(t, err)
	assert.Len(t, keys, 5)

	// 超出范围
	keys, err = store.List(10, 5)
	require.NoError(t, err)
	assert.Len(t, keys, 0)
}

// TestMemoryMVStore_ListPrefix 测试前缀查询
func TestMemoryMVStore_ListPrefix(t *testing.T) {
	tempDir := t.TempDir()
	options := &MVStoreOptions{
		DataDir:   tempDir,
		WALDir:    filepath.Join(tempDir, "wal"),
		EnableWAL: false,
	}

	store, err := NewMemoryMVStore(options)
	require.NoError(t, err)
	defer store.Close()

	// 写入不同前缀的 key
	store.Put("user:1", []byte("alice"))
	store.Put("user:2", []byte("bob"))
	store.Put("order:1", []byte("order1"))
	store.Put("user:3", []byte("charlie"))

	// 查询 user: 前缀
	keys, err := store.ListPrefix("user:", 0, 0)
	require.NoError(t, err)
	assert.Len(t, keys, 3)
	assert.Contains(t, keys, "user:1")
	assert.Contains(t, keys, "user:2")
	assert.Contains(t, keys, "user:3")

	// 查询 order: 前缀
	keys, err = store.ListPrefix("order:", 0, 0)
	require.NoError(t, err)
	assert.Len(t, keys, 1)
	assert.Equal(t, "order:1", keys[0])
}

// TestMemoryMVStore_GetVersionCount 测试版本计数
func TestMemoryMVStore_GetVersionCount(t *testing.T) {
	tempDir := t.TempDir()
	options := &MVStoreOptions{
		DataDir:     tempDir,
		WALDir:      filepath.Join(tempDir, "wal"),
		EnableWAL:   false,
		MaxVersions: 10,
	}

	store, err := NewMemoryMVStore(options)
	require.NoError(t, err)
	defer store.Close()

	// 初始版本数为 0
	count, err := store.GetVersionCount("key1")
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	// 写入多个版本
	for i := 0; i < 5; i++ {
		store.Put("key1", []byte(fmt.Sprintf("value%d", i)))
		time.Sleep(5 * time.Millisecond)
	}

	count, err = store.GetVersionCount("key1")
	require.NoError(t, err)
	assert.Equal(t, 5, count)
}

// TestMemoryMVStore_GetAllVersions 测试获取所有版本信息
func TestMemoryMVStore_GetAllVersions(t *testing.T) {
	tempDir := t.TempDir()
	options := &MVStoreOptions{
		DataDir:     tempDir,
		WALDir:      filepath.Join(tempDir, "wal"),
		EnableWAL:   false,
		MaxVersions: 5,
	}

	store, err := NewMemoryMVStore(options)
	require.NoError(t, err)
	defer store.Close()

	// 写入多个版本
	store.Put("key1", []byte("value1"))
	store.Put("key1", []byte("value2"))
	store.Put("key1", []byte("value3"))

	// 获取所有版本信息
	infos, err := store.GetAllVersions("key1")
	require.NoError(t, err)
	assert.Len(t, infos, 3)

	// 验证版本信息递增
	for i := 1; i < len(infos); i++ {
		assert.Greater(t, infos[i].Version, infos[i-1].Version)
		assert.False(t, infos[i].Deleted)
	}
}

// TestMemoryMVStore_Concurrent 测试并发访问
func TestMemoryMVStore_Concurrent(t *testing.T) {
	tempDir := t.TempDir()
	options := &MVStoreOptions{
		DataDir:     tempDir,
		WALDir:      filepath.Join(tempDir, "wal"),
		EnableWAL:   false,
		MaxVersions: 10,
	}

	store, err := NewMemoryMVStore(options)
	require.NoError(t, err)
	defer store.Close()

	// 并发写入
	const goroutines = 100
	const putsPerGoroutine = 10

	done := make(chan bool, goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			for j := 0; j < putsPerGoroutine; j++ {
				key := fmt.Sprintf("key%d", id)
				value := []byte(fmt.Sprintf("goroutine%d-value%d", id, j))
				err := store.Put(key, value)
				assert.NoError(t, err)
			}
			done <- true
		}(i)
	}

	// 等待所有 goroutine 完成
	for i := 0; i < goroutines; i++ {
		<-done
	}

	// 验证数据
	for i := 0; i < goroutines; i++ {
		key := fmt.Sprintf("key%d", i)
		_, err := store.Get(key)
		assert.NoError(t, err)
	}
}

// TestMemoryMVStore_WAL 测试 WAL 功能
func TestMemoryMVStore_WAL(t *testing.T) {
	tempDir := t.TempDir()
	walDir := filepath.Join(tempDir, "wal")
	options := &MVStoreOptions{
		DataDir:     tempDir,
		WALDir:      walDir,
		EnableWAL:   true,
		MaxVersions: 10,
	}

	// 第一阶段：写入数据并关闭
	store1, err := NewMemoryMVStore(options)
	require.NoError(t, err)

	err = store1.Put("key1", []byte("value1"))
	require.NoError(t, err)

	err = store1.Put("key2", []byte("value2"))
	require.NoError(t, err)

	err = store1.Close()
	require.NoError(t, err)

	// 第二阶段：从 WAL 恢复
	store2, err := NewMemoryMVStore(options)
	require.NoError(t, err)
	defer store2.Close()

	// 验证恢复的数据
	value, err := store2.Get("key1")
	require.NoError(t, err)
	assert.Equal(t, []byte("value1"), value)

	value, err = store2.Get("key2")
	require.NoError(t, err)
	assert.Equal(t, []byte("value2"), value)
}

// TestMemoryMVStore_Snapshot 测试快照功能
func TestMemoryMVStore_Snapshot(t *testing.T) {
	tempDir := t.TempDir()
	options := &MVStoreOptions{
		DataDir:   tempDir,
		WALDir:    filepath.Join(tempDir, "wal"),
		EnableWAL: false,
	}

	store, err := NewMemoryMVStore(options)
	require.NoError(t, err)
	defer store.Close()

	// 写入数据
	for i := 1; i <= 5; i++ {
		err = store.Put(fmt.Sprintf("key%d", i), []byte(fmt.Sprintf("value%d", i)))
		require.NoError(t, err)
	}

	// 创建快照
	snapshot, err := store.CreateSnapshot()
	require.NoError(t, err)
	assert.NotNil(t, snapshot)

	// 修改数据
	err = store.Put("key6", []byte("value6"))
	require.NoError(t, err)

	// 从快照恢复
	err = store.RestoreFromSnapshot(snapshot)
	require.NoError(t, err)

	// 验证恢复后的状态
	_, err = store.Get("key1")
	require.NoError(t, err)

	_, err = store.Get("key6")
	assert.ErrorIs(t, err, ErrNotFound)
}

// TestMemoryMVStore_Stats 测试统计信息
func TestMemoryMVStore_Stats(t *testing.T) {
	tempDir := t.TempDir()
	options := &MVStoreOptions{
		DataDir:     tempDir,
		WALDir:      filepath.Join(tempDir, "wal"),
		EnableWAL:   false,
		MaxVersions: 5,
	}

	store, err := NewMemoryMVStore(options)
	require.NoError(t, err)
	defer store.Close()

	// 写入数据
	for i := 0; i < 10; i++ {
		store.Put(fmt.Sprintf("key%d", i), []byte(fmt.Sprintf("value%d", i)))
	}

	// 创建多版本
	for i := 0; i < 3; i++ {
		store.Put("key0", []byte(fmt.Sprintf("newvalue%d", i)))
	}

	// 获取统计信息
	stats, err := store.Stats()
	require.NoError(t, err)

	assert.Equal(t, 10, stats.KeyCount)
	assert.Greater(t, stats.VersionCount, 10)
	assert.Greater(t, stats.MemTableSize, int64(0))
}

// BenchmarkMemoryMVStore_Put 性能基准测试 - Put 操作
func BenchmarkMemoryMVStore_Put(b *testing.B) {
	tempDir := b.TempDir()
	options := &MVStoreOptions{
		DataDir:   tempDir,
		WALDir:    filepath.Join(tempDir, "wal"),
		EnableWAL: false,
	}

	store, err := NewMemoryMVStore(options)
	require.NoError(b, err)
	defer store.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key%d", i%1000)
		value := []byte(fmt.Sprintf("value%d", i))
		store.Put(key, value)
	}
}

// BenchmarkMemoryMVStore_Get 性能基准测试 - Get 操作
func BenchmarkMemoryMVStore_Get(b *testing.B) {
	tempDir := b.TempDir()
	options := &MVStoreOptions{
		DataDir:   tempDir,
		WALDir:    filepath.Join(tempDir, "wal"),
		EnableWAL: false,
	}

	store, err := NewMemoryMVStore(options)
	require.NoError(b, err)
	defer store.Close()

	// 预填充数据
	for i := 0; i < 1000; i++ {
		store.Put(fmt.Sprintf("key%d", i), []byte(fmt.Sprintf("value%d", i)))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key%d", i%1000)
		_, _ = store.Get(key)
	}
}

// BenchmarkMemoryMVStore_Parallel 性能基准测试 - 并发读写
func BenchmarkMemoryMVStore_Parallel(b *testing.B) {
	tempDir := b.TempDir()
	options := &MVStoreOptions{
		DataDir:   tempDir,
		WALDir:    filepath.Join(tempDir, "wal"),
		EnableWAL: false,
	}

	store, err := NewMemoryMVStore(options)
	require.NoError(b, err)
	defer store.Close()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%2 == 0 {
				key := fmt.Sprintf("key%d", i%1000)
				value := []byte(fmt.Sprintf("value%d", i))
				_ = store.Put(key, value)
			} else {
				key := fmt.Sprintf("key%d", i%1000)
				_, _ = store.Get(key)
			}
			i++
		}
	})
}

// TestWAL_Append_Recover 测试 WAL 追加和恢复
func TestWAL_Append_Recover(t *testing.T) {
	tempDir := t.TempDir()
	walPath := filepath.Join(tempDir, "test.wal")

	// 创建 WAL 并写入数据
	wal1, err := NewMetadataWAL(walPath)
	require.NoError(t, err)

	hlc := clock.NewHLC()

	entries := []*WALEntry{
		{
			Timestamp: hlc.Now(),
			Type:      WALTypePut,
			Key:       "key1",
			Value:     []byte("value1"),
		},
		{
			Timestamp: hlc.Now(),
			Type:      WALTypePut,
			Key:       "key2",
			Value:     []byte("value2"),
		},
		{
			Timestamp: hlc.Now(),
			Type:      WALTypeDelete,
			Key:       "key1",
		},
	}

	for _, entry := range entries {
		err = wal1.Append(entry)
		require.NoError(t, err)
	}

	err = wal1.Close()
	require.NoError(t, err)

	// 重新打开 WAL 并恢复数据
	wal2, err := NewMetadataWAL(walPath)
	require.NoError(t, err)
	defer wal2.Close()

	recovered, err := wal2.Recover()
	require.NoError(t, err)
	assert.Len(t, recovered, 3)

	// 验证恢复的数据
	assert.Equal(t, WALTypePut, recovered[0].Type)
	assert.Equal(t, "key1", recovered[0].Key)
	assert.Equal(t, []byte("value1"), recovered[0].Value)

	assert.Equal(t, WALTypeDelete, recovered[2].Type)
	assert.Equal(t, "key1", recovered[2].Key)
}

// TestWAL_Truncate 测试 WAL 截断
func TestWAL_Truncate(t *testing.T) {
	tempDir := t.TempDir()
	walPath := filepath.Join(tempDir, "test.wal")

	wal, err := NewMetadataWAL(walPath)
	require.NoError(t, err)
	defer wal.Close()

	// 写入一些数据
	hlc := clock.NewHLC()
	for i := 0; i < 10; i++ {
		entry := &WALEntry{
			Timestamp: hlc.Now(),
			Type:      WALTypePut,
			Key:       fmt.Sprintf("key%d", i),
			Value:     []byte(fmt.Sprintf("value%d", i)),
		}
		err = wal.Append(entry)
		require.NoError(t, err)
	}

	// 获取当前 offset
	stats, err := wal.GetStats()
	require.NoError(t, err)

	// 截断一半
	truncateOffset := stats.Offset / 2
	err = wal.Truncate(truncateOffset)
	require.NoError(t, err)

	// 验证截断后的 offset
	newStats, err := wal.GetStats()
	require.NoError(t, err)
	assert.Equal(t, truncateOffset, newStats.Offset)
}

// TestSnapshotManager 测试快照管理器
func TestSnapshotManager(t *testing.T) {
	tempDir := t.TempDir()

	snapMgr, err := NewSnapshotManager(tempDir)
	require.NoError(t, err)
	defer snapMgr.Close()

	// 创建一个临时 store 来生成快照数据
	storeTempDir := t.TempDir()
	storeOptions := &MVStoreOptions{
		DataDir:   storeTempDir,
		WALDir:    filepath.Join(storeTempDir, "wal"),
		EnableWAL: false,
	}

	testStore, err := NewMemoryMVStore(storeOptions)
	require.NoError(t, err)
	require.NoError(t, testStore.Put("test", []byte("data")))
	defer testStore.Close()

	// 创建快照
	err = snapMgr.Create(testStore)
	require.NoError(t, err)

	// 列出快照
	snapshots, err := snapMgr.List()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(snapshots), 1)

	// 恢复快照
	if len(snapshots) > 0 {
		recovered, err := snapMgr.Restore(snapshots[0])
		require.NoError(t, err)
		assert.NotNil(t, recovered)
	}
}
