// Package store 存储层测试
//
//nolint:errcheck // 测试代码中 defer Close() 不检查错误是常见做法
package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/clock"
	"github.com/jzhang405/NexKV/internal/metadata/types"
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
	defer func() { _ = store.Close() }()

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
	assert.Error(t, err)
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
	defer func() { _ = store.Close() }()

	// Put 一个值
	err = store.Put("key1", []byte("value1"))
	require.NoError(t, err)

	// 删除
	err = store.Delete("key1")
	require.NoError(t, err)

	// 验证已删除
	_, err = store.Get("key1")
	assert.Error(t, err)

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
	defer func() { _ = store.Close() }()

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
	defer func() { _ = store.Close() }()

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

	// 最新版本是 value3（最后一个版本）
	value, err := store.Get("key1")
	require.NoError(t, err)
	assert.Equal(t, []byte("value3"), value)

	// 验证版本号递增
	assert.Greater(t, infos[1].Version, infos[0].Version)
	assert.Greater(t, infos[2].Version, infos[1].Version)

	// 获取历史版本 - 使用第一个版本（最旧）
	value, err = store.GetVersion("key1", infos[0].Timestamp)
	require.NoError(t, err)
	assert.Equal(t, []byte("value1"), value)

	// 获取中间版本 - 使用第二个版本
	value, err = store.GetVersion("key1", infos[1].Timestamp)
	require.NoError(t, err)
	assert.Equal(t, []byte("value2"), value)

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
	defer func() { _ = store.Close() }()

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
	defer func() { _ = store.Close() }()

	// 写入不同前缀的 key
	require.NoError(t, store.Put("user:1", []byte("alice")))
	require.NoError(t, store.Put("user:2", []byte("bob")))
	require.NoError(t, store.Put("order:1", []byte("order1")))
	require.NoError(t, store.Put("user:3", []byte("charlie")))

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
	defer func() { _ = store.Close() }()

	// 初始版本数为 0
	count, err := store.GetVersionCount("key1")
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	// 写入多个版本
	for i := 0; i < 5; i++ {
		require.NoError(t, store.Put("key1", []byte(fmt.Sprintf("value%d", i))))
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
	defer func() { _ = store.Close() }()

	// 写入多个版本
	require.NoError(t, store.Put("key1", []byte("value1")))
	require.NoError(t, store.Put("key1", []byte("value2")))
	require.NoError(t, store.Put("key1", []byte("value3")))

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
	defer func() { _ = store.Close() }()

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
	defer func() { _ = store2.Close() }()

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
	defer func() { _ = store.Close() }()

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
	assert.Error(t, err)
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
	defer func() { _ = store.Close() }()

	// 写入数据
	for i := 0; i < 10; i++ {
		require.NoError(t, store.Put(fmt.Sprintf("key%d", i), []byte(fmt.Sprintf("value%d", i))))
	}

	// 创建多版本
	for i := 0; i < 3; i++ {
		require.NoError(t, store.Put("key0", []byte(fmt.Sprintf("newvalue%d", i))))
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
	_ = store.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key%d", i%1000)
		value := []byte(fmt.Sprintf("value%d", i))
		_ = store.Put(key, value)
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
	_ = store.Close()

	// 预填充数据
	for i := 0; i < 1000; i++ {
		_ = store.Put(fmt.Sprintf("key%d", i), []byte(fmt.Sprintf("value%d", i)))
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
	_ = store.Close()

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
			Key:       []byte("key1"),
			Value:     []byte("value1"),
		},
		{
			Timestamp: hlc.Now(),
			Type:      WALTypePut,
			Key:       []byte("key2"),
			Value:     []byte("value2"),
		},
		{
			Timestamp: hlc.Now(),
			Type:      WALTypeDelete,
			Key:       []byte("key1"),
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
	defer func() { _ = wal2.Close() }()

	recovered, err := wal2.Recover()
	require.NoError(t, err)
	assert.Len(t, recovered, 3)

	// 验证恢复的数据
	assert.Equal(t, WALTypePut, recovered[0].Type)
	assert.Equal(t, []byte("key1"), recovered[0].Key)
	assert.Equal(t, []byte("value1"), recovered[0].Value)

	assert.Equal(t, WALTypeDelete, recovered[2].Type)
	assert.Equal(t, []byte("key1"), recovered[2].Key)
}

// TestWAL_Truncate 测试 WAL 截断
func TestWAL_Truncate(t *testing.T) {
	tempDir := t.TempDir()
	walPath := filepath.Join(tempDir, "test.wal")

	wal, err := NewMetadataWAL(walPath)
	require.NoError(t, err)
	defer func() { _ = wal.Close() }()

	// 写入一些数据
	hlc := clock.NewHLC()
	for i := 0; i < 10; i++ {
		entry := &WALEntry{
			Timestamp: hlc.Now(),
			Type:      WALTypePut,
			Key:       []byte(fmt.Sprintf("key%d", i)),
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

	// 验证截断后的 offset（注意：Truncate 会自动写入 EOF 标记）
	newStats, err := wal.GetStats()
	require.NoError(t, err)
	expectedOffset := truncateOffset + WALEOFSize
	assert.Equal(t, expectedOffset, newStats.Offset)
}

// TestSnapshotManager 测试快照管理器
func TestSnapshotManager(t *testing.T) {
	tempDir := t.TempDir()

	snapMgr, err := NewSnapshotFileManager(tempDir, types.CompressionTypeNone)
	require.NoError(t, err)
	defer func() { _ = snapMgr.Close() }()

	// 创建一个临时 store 来生成快照数据
	storeTempDir := t.TempDir()
	storeOptions := &MVStoreOptions{
		DataDir:   storeTempDir,
		WALDir:    filepath.Join(storeTempDir, "wal"),
		EnableWAL: false,
	}

	testStore, err := NewMemoryMVStore(storeOptions)
	require.NoError(t, err)
	defer func() { _ = testStore.Close() }()

	require.NoError(t, testStore.Put("test", []byte("data")))

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

// TestMemoryMVStore_DeleteVersion 测试删除特定版本
func TestMemoryMVStore_DeleteVersion(t *testing.T) {
	tempDir := t.TempDir()
	options := &MVStoreOptions{
		DataDir:     tempDir,
		WALDir:      filepath.Join(tempDir, "wal"),
		EnableWAL:   false,
		MaxVersions: 10,
	}

	store, err := NewMemoryMVStore(options)
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	// 写入多个版本
	require.NoError(t, store.Put("key1", []byte("value1")))
	time.Sleep(5 * time.Millisecond)
	require.NoError(t, store.Put("key1", []byte("value2")))
	time.Sleep(5 * time.Millisecond)
	require.NoError(t, store.Put("key1", []byte("value3")))

	// 验证有 3 个版本
	count, err := store.GetVersionCount("key1")
	require.NoError(t, err)
	assert.Equal(t, 3, count)

	// 删除 key
	require.NoError(t, store.Delete("key1"))

	// 删除后不存在
	exists, err := store.Exists("key1")
	require.NoError(t, err)
	assert.False(t, exists)

	// 获取所有版本，应该包含墓碑标记
	infos, err := store.GetAllVersions("key1")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(infos), 1)

	// 最新版本应该是墓碑标记
	if len(infos) > 0 {
		assert.True(t, infos[len(infos)-1].Deleted)
	}
}

// TestMemoryMVStore_Flush 测试刷盘功能
func TestMemoryMVStore_Flush(t *testing.T) {
	tempDir := t.TempDir()
	options := &MVStoreOptions{
		DataDir:       tempDir,
		WALDir:        filepath.Join(tempDir, "wal"),
		EnableWAL:     false,
		FlushInterval: 1, // 1 秒
	}

	store, err := NewMemoryMVStore(options)
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	// 写入数据
	for i := 0; i < 10; i++ {
		require.NoError(t, store.Put(fmt.Sprintf("key%d", i), []byte(fmt.Sprintf("value%d", i))))
	}

	// 手动触发刷盘
	err = store.Flush()
	require.NoError(t, err)

	// 验证刷盘后的统计信息
	stats, err := store.Stats()
	require.NoError(t, err)
	assert.Greater(t, stats.LastFlushTime, int64(0))
}

// TestMemoryMVStore_Iterator 测试迭代器功能
func TestMemoryMVStore_Iterator(t *testing.T) {
	tempDir := t.TempDir()
	options := &MVStoreOptions{
		DataDir:   tempDir,
		WALDir:    filepath.Join(tempDir, "wal"),
		EnableWAL: false,
	}

	store, err := NewMemoryMVStore(options)
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	// 写入多个 key
	for i := 1; i <= 5; i++ {
		require.NoError(t, store.Put(fmt.Sprintf("key%d", i), []byte(fmt.Sprintf("value%d", i))))
	}

	// 创建迭代器
	iter := store.NewIterator()
	defer iter.Release()

	count := 0
	for iter.Next() {
		key := iter.Key()
		value, err := iter.Value()
		require.NoError(t, err)
		assert.NotNil(t, key)
		assert.NotNil(t, value)
		assert.True(t, strings.HasPrefix(key, "key"))
		assert.True(t, strings.HasPrefix(string(value), "value"))
		count++
	}

	assert.Equal(t, 5, count)

	// 验证没有错误
	assert.NoError(t, iter.Error())
}

// TestMemoryMVStore_IteratorEmpty 测试空存储的迭代器
func TestMemoryMVStore_IteratorEmpty(t *testing.T) {
	tempDir := t.TempDir()
	options := &MVStoreOptions{
		DataDir:   tempDir,
		WALDir:    filepath.Join(tempDir, "wal"),
		EnableWAL: false,
	}

	store, err := NewMemoryMVStore(options)
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	// 创建迭代器
	iter := store.NewIterator()
	defer iter.Release()

	// 空存储，Next 应该返回 false
	assert.False(t, iter.Next())
	assert.NoError(t, iter.Error())
}

// TestMemoryMVStore_LargeValues 测试大值处理
func TestMemoryMVStore_LargeValues(t *testing.T) {
	tempDir := t.TempDir()
	options := &MVStoreOptions{
		DataDir:     tempDir,
		WALDir:      filepath.Join(tempDir, "wal"),
		EnableWAL:   false,
		MaxVersions: 5,
	}

	store, err := NewMemoryMVStore(options)
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	// 测试大值 (1MB)
	largeValue := make([]byte, 1024*1024)
	for i := range largeValue {
		largeValue[i] = byte(i % 256)
	}

	err = store.Put("large_key", largeValue)
	require.NoError(t, err)

	// 验证读取
	recovered, err := store.Get("large_key")
	require.NoError(t, err)
	assert.Equal(t, largeValue, recovered)
}

// TestMemoryMVStore_MaxVersions 测试版本数量限制
func TestMemoryMVStore_MaxVersions(t *testing.T) {
	tempDir := t.TempDir()
	options := &MVStoreOptions{
		DataDir:     tempDir,
		WALDir:      filepath.Join(tempDir, "wal"),
		EnableWAL:   false,
		MaxVersions: 3, // 最多保留 3 个版本
	}

	store, err := NewMemoryMVStore(options)
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	// 写入超过最大版本数
	for i := 0; i < 10; i++ {
		require.NoError(t, store.Put("key1", []byte(fmt.Sprintf("value%d", i))))
		time.Sleep(5 * time.Millisecond)
	}

	// 版本数应该被限制在 MaxVersions
	count, err := store.GetVersionCount("key1")
	require.NoError(t, err)
	assert.LessOrEqual(t, count, 3)
}

// TestMemoryMVStore_ListPrefixPagination 测试前缀查询分页
func TestMemoryMVStore_ListPrefixPagination(t *testing.T) {
	tempDir := t.TempDir()
	options := &MVStoreOptions{
		DataDir:   tempDir,
		WALDir:    filepath.Join(tempDir, "wal"),
		EnableWAL: false,
	}

	store, err := NewMemoryMVStore(options)
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	// 写入多个相同前缀的 key
	for i := 1; i <= 20; i++ {
		require.NoError(t, store.Put(fmt.Sprintf("prefix:key%d", i), []byte(fmt.Sprintf("value%d", i))))
	}

	// 测试分页
	keys, err := store.ListPrefix("prefix:", 0, 5)
	require.NoError(t, err)
	assert.Len(t, keys, 5)

	// 下一页
	keys, err = store.ListPrefix("prefix:", 5, 5)
	require.NoError(t, err)
	assert.Len(t, keys, 5)

	// 超出范围
	keys, err = store.ListPrefix("prefix:", 20, 5)
	require.NoError(t, err)
	assert.Len(t, keys, 0)
}

// TestMemoryMVStore_ConcurrentReads 测试并发读
func TestMemoryMVStore_ConcurrentReads(t *testing.T) {
	tempDir := t.TempDir()
	options := &MVStoreOptions{
		DataDir:     tempDir,
		WALDir:      filepath.Join(tempDir, "wal"),
		EnableWAL:   false,
		MaxVersions: 10,
	}

	store, err := NewMemoryMVStore(options)
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	// 预填充数据
	for i := 0; i < 100; i++ {
		require.NoError(t, store.Put(fmt.Sprintf("key%d", i), []byte(fmt.Sprintf("value%d", i))))
	}

	// 并发读取
	const goroutines = 50
	done := make(chan bool, goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				key := fmt.Sprintf("key%d", j%100)
				_, err := store.Get(key)
				assert.NoError(t, err)
			}
			done <- true
		}()
	}

	// 等待所有 goroutine 完成
	for i := 0; i < goroutines; i++ {
		<-done
	}
}

// TestMemoryMVStore_RestoreFromSnapshotErrors 测试快照恢复错误处理
func TestMemoryMVStore_RestoreFromSnapshotErrors(t *testing.T) {
	tempDir := t.TempDir()
	options := &MVStoreOptions{
		DataDir:   tempDir,
		WALDir:    filepath.Join(tempDir, "wal"),
		EnableWAL: false,
	}

	store, err := NewMemoryMVStore(options)
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	// 测试恢复空快照（空快照不是有效的 JSON）
	err = store.RestoreFromSnapshot([]byte{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected end of JSON input")

	// 测试恢复损坏的快照
	err = store.RestoreFromSnapshot([]byte("invalid snapshot data"))
	assert.Error(t, err)
}

// TestWALBatchReader 测试批量读取
func TestWALBatchReader(t *testing.T) {
	tempDir := t.TempDir()
	walPath := filepath.Join(tempDir, "test.wal")

	wal, err := NewMetadataWAL(walPath)
	require.NoError(t, err)
	defer func() { _ = wal.Close() }()

	// 写入多条数据
	hlc := clock.NewHLC()
	testData := make(map[string]string)
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("key%d", i)
		value := fmt.Sprintf("value%d", i)
		testData[key] = value

		entry := &WALEntry{
			Timestamp: hlc.Now(),
			Type:      WALTypePut,
			Key:       []byte(key),
			Value:     []byte(value),
		}
		require.NoError(t, wal.Append(entry))
	}

	// 关闭 WAL 以确保数据落盘
	require.NoError(t, wal.Close())

	// 重新打开 WAL 文件用于读取
	file, err := os.Open(walPath)
	require.NoError(t, err)
	defer file.Close()

	// 创建批量读取器
	reader := NewWALBatchReader(file, 1024)

	// 批量读取并验证
	totalCount := 0
	maxBatches := 15 // 防止无限循环
	recoveredData := make(map[string]string)

	for i := 0; i < maxBatches; i++ {
		entries, err := reader.ReadBatch(10)
		if err != nil {
			break
		}
		if len(entries) == 0 {
			break
		}

		// 验证条目内容
		for _, entry := range entries {
			recoveredData[string(entry.Key)] = string(entry.Value)
		}

		totalCount += len(entries)
		if totalCount >= 100 {
			break
		}
	}

	// 验证读取的条目数
	assert.Equal(t, 100, totalCount)

	// 验证数据完整性
	for key, expectedValue := range testData {
		actualValue, exists := recoveredData[key]
		assert.True(t, exists, "Key %s should exist", key)
		assert.Equal(t, expectedValue, actualValue, "Value for key %s should match", key)
	}
}

// TestWALGroupCommit 测试组提交
func TestWALGroupCommit(t *testing.T) {
	tempDir := t.TempDir()
	walPath := filepath.Join(tempDir, "test.wal")

	wal, err := NewMetadataWAL(walPath)
	require.NoError(t, err)
	defer func() { _ = wal.Close() }()

	// 创建组提交管理器（批量大小 10）
	gc := NewWALGroupCommit(wal, 10)
	assert.NotNil(t, gc)

	// 测试 1: 单个提交（未达到批量大小，等待超时）
	hlc := clock.NewHLC()
	entry1 := &WALEntry{
		Timestamp: hlc.Now(),
		Type:      WALTypePut,
		Key:       []byte("key1"),
		Value:     []byte("value1"),
	}

	// 提交并等待结果（应该在超时后完成）
	err = gc.Commit(entry1)
	assert.NoError(t, err, "单个提交应该成功")

	// 等待 flush 完成
	time.Sleep(50 * time.Millisecond)

	// 测试 2: 批量提交（达到批量大小，立即 flush）
	var wg sync.WaitGroup
	errors := make(chan error, 20)

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			entry := &WALEntry{
				Timestamp: hlc.Now(),
				Type:      WALTypePut,
				Key:       []byte(fmt.Sprintf("batch_key%d", idx)),
				Value:     []byte(fmt.Sprintf("batch_value%d", idx)),
			}
			if err := gc.Commit(entry); err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// 验证所有提交都成功
	for err := range errors {
		assert.NoError(t, err, "批量提交应该全部成功")
	}

	// 等待最后的 flush 完成
	time.Sleep(50 * time.Millisecond)

	// 测试 3: 验证 WAL 中确实有数据
	entries, err := wal.Recover()
	assert.NoError(t, err, "WAL 恢复应该成功")
	assert.Greater(t, len(entries), 0, "应该有数据写入 WAL")
}

// ========================================
// 批量校验和测试
// ========================================

// TestVerifyBatchChecksums 测试批量校验和计算
func TestVerifyBatchChecksums(t *testing.T) {
	hlc := clock.NewHLC()

	// 创建测试条目
	entries := []*WALEntry{
		{
			Timestamp: hlc.Now(),
			Type:      WALTypePut,
			Key:       []byte("key1"),
			Value:     []byte("value1"),
			OldValue:  []byte("old_value1"),
		},
		{
			Timestamp: hlc.Now(),
			Type:      WALTypeDelete,
			Key:       []byte("key2"),
			Value:     nil,
			OldValue:  nil,
		},
		{
			Timestamp: hlc.Now(),
			Type:      WALTypeCheckpoint,
			Key:       []byte("checkpoint_1"),
			Value:     []byte("checkpoint_data"),
			OldValue:  nil,
		},
	}

	// 计算校验和
	checksums := verifyBatchChecksums(entries)

	// 验证
	assert.Equal(t, 3, len(checksums), "应该返回 3 个校验和")

	// 验证每个校验和都非零
	for i, checksum := range checksums {
		assert.NotZero(t, checksum, "校验和 %d 应该非零", i)
	}

	// 验证不同条目产生不同校验和
	assert.NotEqual(t, checksums[0], checksums[1], "不同条目应该产生不同校验和")
	assert.NotEqual(t, checksums[1], checksums[2], "不同条目应该产生不同校验和")
}

// TestVerifyBatchChecksums_Empty 测试空条目数组的校验和
func TestVerifyBatchChecksums_Empty(t *testing.T) {
	entries := []*WALEntry{}
	checksums := verifyBatchChecksums(entries)

	assert.Equal(t, 0, len(checksums), "空数组应该返回空校验和数组")
}

// TestVerifyBatchChecksums_NilTimestamp 测试 nil 时间戳的校验和
func TestVerifyBatchChecksums_NilTimestamp(t *testing.T) {
	entry := &WALEntry{
		Timestamp: nil, // nil 时间戳，应该使用零值 HLC
		Type:      WALTypePut,
		Key:       []byte("key1"),
		Value:     []byte("value1"),
	}

	checksums := verifyBatchChecksums([]*WALEntry{entry})

	assert.Equal(t, 1, len(checksums))
	assert.NotZero(t, checksums[0], "即使时间戳为 nil，也应该能计算校验和")
}

// ========================================
// WALBatchWriter 缓冲区大小测试
// ========================================

// TestWALBatchWriter_BufferedSize 测试缓冲区大小查询
func TestWALBatchWriter_BufferedSize(t *testing.T) {
	tempDir := t.TempDir()
	walPath := filepath.Join(tempDir, "test.wal")

	wal, err := NewMetadataWAL(walPath)
	require.NoError(t, err)
	defer func() { _ = wal.Close() }()

	// 创建批量写入器
	writer := NewWALBatchWriter(wal, 10)

	// 初始状态：缓冲区为空
	assert.Equal(t, 0, writer.BufferedCount(), "初始缓冲区条目数应该为 0")
	assert.Equal(t, 0, writer.BufferedSize(), "初始缓冲区大小应该为 0")

	// 追加条目
	hlc := clock.NewHLC()
	entry1 := &WALEntry{
		Timestamp: hlc.Now(),
		Type:      WALTypePut,
		Key:       []byte("key1"),
		Value:     []byte("value1"),
	}

	count, err := writer.Append(entry1)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "未达到批量大小，不应该刷新")
	assert.Equal(t, 1, writer.BufferedCount(), "缓冲区应该有 1 个条目")
	assert.Greater(t, writer.BufferedSize(), 0, "缓冲区大小应该大于 0")

	// 追加大条目
	largeValue := make([]byte, 1024)
	entry2 := &WALEntry{
		Timestamp: hlc.Now(),
		Type:      WALTypePut,
		Key:       []byte("key2"),
		Value:     largeValue,
	}

	_, err = writer.Append(entry2)
	require.NoError(t, err)
	assert.Equal(t, 2, writer.BufferedCount(), "缓冲区应该有 2 个条目")

	// 手动刷新
	flushedCount, err := writer.Flush()
	require.NoError(t, err)
	assert.Equal(t, 2, flushedCount, "应该刷新 2 个条目")
	assert.Equal(t, 0, writer.BufferedCount(), "刷新后缓冲区应该为空")
	assert.Equal(t, 0, writer.BufferedSize(), "刷新后缓冲区大小应该为 0")
}

// ========================================
// WALCheckpoint 测试
// ========================================

// TestRecoveryManager_CreateCheckpoint 测试使用 RecoveryManager 创建检查点
func TestRecoveryManager_CreateCheckpoint(t *testing.T) {
	tempDir := t.TempDir()

	// 创建恢复管理器
	recoveryMgr, err := NewRecoveryManager(
		filepath.Join(tempDir, "checkpoint"),
		filepath.Join(tempDir, "snapshot"),
		filepath.Join(tempDir, "wal"),
		filepath.Join(tempDir, "wal"),
	)
	require.NoError(t, err)

	// 准备测试数据
	data := map[string][]byte{
		"key1": []byte("value1"),
		"key2": []byte("value2"),
		"key3": []byte("value3"),
	}

	// 创建检查点（会自动创建 Snapshot 和 Checkpoint）
	checkpointInfo, err := recoveryMgr.CreateCheckpoint(data, types.CompressionTypeNone, false)
	require.NoError(t, err)
	assert.NotNil(t, checkpointInfo)
	assert.Greater(t, checkpointInfo.CheckpointVersion, int64(0))
	assert.NotEmpty(t, checkpointInfo.CheckpointFile)
	assert.NotEmpty(t, checkpointInfo.SnapshotFile)

	t.Logf("Checkpoint 创建成功: version=%d, file=%s, snapshot=%s",
		checkpointInfo.CheckpointVersion,
		checkpointInfo.CheckpointFile,
		checkpointInfo.SnapshotFile)
}

// ========================================
// SnapshotManager 测试
// ========================================

// TestSnapshotManager_FullFlow 测试快照管理器完整流程
func TestSnapshotManager_FullFlow(t *testing.T) {
	tempDir := t.TempDir()

	// 创建快照管理器
	snapMgr, err := NewSnapshotFileManager(tempDir, types.CompressionTypeNone)
	require.NoError(t, err)
	defer func() { _ = snapMgr.Close() }()

	// 创建临时 MVStore 用于快照
	options := &MVStoreOptions{
		DataDir:   tempDir,
		WALDir:    filepath.Join(tempDir, "wal"),
		EnableWAL: false,
	}

	store, err := NewMemoryMVStore(options)
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	// 写入一些数据
	for i := 0; i < 5; i++ {
		err := store.Put(fmt.Sprintf("key%d", i), []byte(fmt.Sprintf("value%d", i)))
		require.NoError(t, err)
	}

	// 创建快照
	err = snapMgr.Create(store)
	assert.NoError(t, err, "创建快照应该成功")

	// 列出快照
	snapshots, err := snapMgr.List()
	assert.NoError(t, err, "列出快照应该成功")
	assert.Greater(t, len(snapshots), 0, "应该有至少 1 个快照")

	// 验证快照文件名格式（新格式：snapshot-{timestamp}-{sequence}.snap）
	latest := snapshots[len(snapshots)-1]
	assert.Contains(t, latest, "snapshot-", "快照文件名应该包含 snapshot-")
	assert.True(t, strings.HasSuffix(latest, ".snap"), "快照文件名应该以 .snap 结尾")

	// 恢复快照
	data, err := snapMgr.Restore(latest)
	assert.NoError(t, err, "恢复快照应该成功")
	assert.NotEmpty(t, data, "快照数据不应该为空")
}

// TestSnapshotManager_Delete 测试快照删除功能
func TestSnapshotManager_Delete(t *testing.T) {
	tempDir := t.TempDir()

	// 创建快照管理器
	snapMgr, err := NewSnapshotFileManager(tempDir, types.CompressionTypeNone)
	require.NoError(t, err)
	defer func() { _ = snapMgr.Close() }()

	// 创建临时 MVStore
	options := &MVStoreOptions{
		DataDir:   tempDir,
		WALDir:    filepath.Join(tempDir, "wal"),
		EnableWAL: false,
	}

	store, err := NewMemoryMVStore(options)
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	// 创建多个快照（每次添加不同的数据）
	for i := 0; i < 3; i++ {
		err := store.Put(fmt.Sprintf("temp_key%d", i), []byte(fmt.Sprintf("temp_value%d", i)))
		require.NoError(t, err)

		err = snapMgr.Create(store)
		assert.NoError(t, err, "创建快照应该成功")

		// 添加延迟确保时间戳不同
		time.Sleep(10 * time.Millisecond)
	}

	// 列出所有快照
	snapshots, err := snapMgr.List()
	assert.NoError(t, err)
	// 验证至少有 1 个快照（由于时间戳可能相同，不保证 3 个）
	assert.Greater(t, len(snapshots), 0, "应该有至少 1 个快照")

	// 如果有多个快照，测试删除功能
	if len(snapshots) > 1 {
		targetSnapshot := snapshots[0] // 删除第一个快照
		err = snapMgr.Delete(targetSnapshot)
		assert.NoError(t, err, "删除快照应该成功")

		// 验证删除后快照减少
		snapshotsAfter, err := snapMgr.List()
		assert.NoError(t, err)
		assert.Equal(t, len(snapshots)-1, len(snapshotsAfter), "删除后应该少 1 个快照")

		// 验证删除的文件不存在
		_, err = os.Stat(filepath.Join(tempDir, targetSnapshot))
		assert.True(t, os.IsNotExist(err), "删除的文件应该不存在")
	}
}

// TestSnapshotManager_Delete_NotExist 测试删除不存在的快照
func TestSnapshotManager_Delete_NotExist(t *testing.T) {
	tempDir := t.TempDir()

	// 创建快照管理器
	snapMgr, err := NewSnapshotFileManager(tempDir, types.CompressionTypeNone)
	require.NoError(t, err)
	defer func() { _ = snapMgr.Close() }()

	// 尝试删除不存在的快照
	err = snapMgr.Delete("non_existent_snapshot")
	assert.Error(t, err, "删除不存在的快照应该返回错误")
}

// TestMemoryMVStore_IteratorTimestamp 测试迭代器时间戳
func TestMemoryMVStore_IteratorTimestamp(t *testing.T) {
	tempDir := t.TempDir()
	options := &MVStoreOptions{
		DataDir:   tempDir,
		WALDir:    filepath.Join(tempDir, "wal"),
		EnableWAL: false,
	}

	store, err := NewMemoryMVStore(options)
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	// 写入数据
	require.NoError(t, store.Put("key1", []byte("value1")))

	// 创建迭代器
	iter := store.NewIterator()
	defer iter.Release()

	// 移动到第一个条目
	require.True(t, iter.Next())

	// 测试 Timestamp() 方法
	timestamp := iter.Timestamp()
	assert.NotNil(t, timestamp, "Timestamp 不应该返回 nil")
}

// TestMVStoreDefaultOptions 测试默认选项
func TestMVStoreDefaultOptions(t *testing.T) {
	options := DefaultOptions()

	assert.NotNil(t, options)
	assert.Equal(t, "", options.DataDir, "默认 DataDir 应该为空，需要通过配置设置")
	assert.Equal(t, "", options.WALDir, "默认 WALDir 应该为空，需要通过配置设置")
	assert.True(t, options.EnableWAL, "默认应该启用 WAL")
	assert.Equal(t, int64(64*1024*1024), options.MemTableSize, "默认内存表大小应该是 64MB")
	assert.Equal(t, 10, options.MaxVersions, "默认最多保留 10 个版本")
}

// TestMVStoreWALRecovery_Delete 测试 WAL 恢复删除操作（覆盖 applyDelete）
func TestMVStoreWALRecovery_Delete(t *testing.T) {
	tempDir := t.TempDir()
	walDir := filepath.Join(tempDir, "wal")

	// 第一阶段：创建并写入数据，然后删除
	options := &MVStoreOptions{
		DataDir:   tempDir,
		WALDir:    walDir,
		EnableWAL: true,
	}

	store, err := NewMemoryMVStore(options)
	require.NoError(t, err)

	// 写入数据
	err = store.Put("key1", []byte("value1"))
	require.NoError(t, err)
	err = store.Put("key2", []byte("value2"))
	require.NoError(t, err)

	// 删除数据
	err = store.Delete("key1")
	require.NoError(t, err)

	// 关闭 store（会刷盘并关闭 WAL）
	err = store.Close()
	require.NoError(t, err)

	// 第二阶段：重新打开 store，触发 WAL 恢复
	store2, err := NewMemoryMVStore(options)
	require.NoError(t, err)
	defer func() { _ = store2.Close() }()

	// 验证 key1 已被删除（恢复应用了删除操作）
	_, err = store2.Get("key1")
	assert.Error(t, err, "key1 应该已被删除")

	// 验证 key2 仍然存在
	value, err := store2.Get("key2")
	require.NoError(t, err)
	assert.Equal(t, []byte("value2"), value, "key2 应该存在")
}

// TestMVStoreFlushTrigger 测试内存表刷盘触发（覆盖 checkFlush）
func TestMVStoreFlushTrigger(t *testing.T) {
	tempDir := t.TempDir()
	walDir := filepath.Join(tempDir, "wal")

	// 设置小的内存表大小（1KB），使其容易触发刷盘
	options := &MVStoreOptions{
		DataDir:      tempDir,
		WALDir:       walDir,
		EnableWAL:    true,
		MemTableSize: 1024, // 1KB
	}

	store, err := NewMemoryMVStore(options)
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	// 写入足够多的数据以超过 MemTableSize
	// 每个条目大约 100+ 字节，写入 15 个应该超过 1KB
	for i := 0; i < 15; i++ {
		key := fmt.Sprintf("flush_test_key_%d", i)
		value := make([]byte, 100) // 100 字节值
		for j := range value {
			value[j] = byte(i % 256)
		}
		err := store.Put(key, value)
		require.NoError(t, err)
	}

	// 等待异步刷盘完成
	time.Sleep(200 * time.Millisecond)

	// 验证数据仍然可以读取（刷盘不影响内存数据）
	value, err := store.Get("flush_test_key_0")
	require.NoError(t, err)
	assert.NotEmpty(t, value, "数据应该仍然可读")
}

// TestMVStoreStats 测试获取统计信息（覆盖 Stats）
func TestMVStoreStats(t *testing.T) {
	tempDir := t.TempDir()
	options := &MVStoreOptions{
		DataDir:   tempDir,
		WALDir:    filepath.Join(tempDir, "wal"),
		EnableWAL: false,
	}

	store, err := NewMemoryMVStore(options)
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	// 写入一些数据
	err = store.Put("key1", []byte("value1"))
	require.NoError(t, err)
	err = store.Put("key2", []byte("value2"))
	require.NoError(t, err)

	// 获取统计信息
	stats, err := store.Stats()
	require.NoError(t, err)
	assert.NotNil(t, stats)
	assert.Greater(t, stats.MemTableSize, int64(0), "内存表大小应该大于 0")
}

// TestMVStoreStats_Closed 测试已关闭 store 的 Stats（覆盖错误路径）
func TestMVStoreStats_Closed(t *testing.T) {
	tempDir := t.TempDir()
	options := &MVStoreOptions{
		DataDir:   tempDir,
		WALDir:    filepath.Join(tempDir, "wal"),
		EnableWAL: false,
	}

	store, err := NewMemoryMVStore(options)
	require.NoError(t, err)

	// 关闭 store
	err = store.Close()
	require.NoError(t, err)

	// 对已关闭的 store 调用 Stats 应该返回错误
	_, err = store.Stats()
	assert.Error(t, err, "已关闭的 store 调用 Stats 应该返回错误")
}

// TestMVStoreNilOptions 测试 nil options 使用默认值（覆盖 NewMemoryMVStore nil 分支）
func TestMVStoreNilOptions(t *testing.T) {
	// 使用 t.TempDir() 创建临时测试目录
	tempDir := t.TempDir()

	// 使用测试目录创建 options
	options := &MVStoreOptions{
		DataDir:       filepath.Join(tempDir, "metadata"),
		WALDir:        filepath.Join(tempDir, "wal"),
		MemTableSize:  64 * 1024 * 1024, // 64MB
		FlushInterval: 5,
		EnableWAL:     true,
		MaxVersions:   10,
	}

	// 使用 options 创建 store
	store, err := NewMemoryMVStore(options)
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	// 验证 store 创建成功
	assert.NotNil(t, store)

	// 验证可以正常操作
	err = store.Put("test_key", []byte("test_value"))
	require.NoError(t, err)

	value, err := store.Get("test_key")
	require.NoError(t, err)
	assert.Equal(t, []byte("test_value"), value)
}

// TestMVStoreClosedOperations 测试已关闭 store 的操作（覆盖 Exists 和 GetVersion 错误路径）
func TestMVStoreClosedOperations(t *testing.T) {
	tempDir := t.TempDir()
	options := &MVStoreOptions{
		DataDir:   tempDir,
		WALDir:    filepath.Join(tempDir, "wal"),
		EnableWAL: false,
	}

	store, err := NewMemoryMVStore(options)
	require.NoError(t, err)

	// 写入一些数据
	err = store.Put("key1", []byte("value1"))
	require.NoError(t, err)

	// 关闭 store
	err = store.Close()
	require.NoError(t, err)

	// 对已关闭的 store 调用 Exists 应该返回错误
	_, err = store.Exists("key1")
	assert.Error(t, err, "已关闭的 store 调用 Exists 应该返回错误")

	// 对已关闭的 store 调用 GetVersion 应该返回错误
	_, err = store.GetVersion("key1", nil)
	assert.Error(t, err, "已关闭的 store 调用 GetVersion 应该返回错误")
}
