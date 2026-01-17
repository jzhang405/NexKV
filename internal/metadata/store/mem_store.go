// Package store 内存表实现
// 使用 sync.Map + atomic 实现高性能 MVCC 存储
package store

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/clock"
	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
)

// MemoryMVStore 内存 MVStore 实现
//
// 核心特性:
//   - sync.Map: 并发安全，无需加锁
//   - atomic: 版本号原子递增
//   - MVCC: 多版本支持，使用 HLC 时间戳
//   - 内存优化：自动清理旧版本
type MemoryMVStore struct {
	mu        sync.RWMutex
	data      sync.Map // key -> *versionList
	options   *MVStoreOptions
	version   atomic.Uint64 // 全局版本号
	closed    atomic.Bool
	flushCh   chan struct{}
	doneCh    chan struct{}
	hlc       *clock.HLC
	wal       WAL
	snapMgr   SnapshotManager
	lastFlush atomic.Int64 // 最后刷盘时间戳
	memSize   atomic.Int64 // 当前内存大小
}

// versionList 版本列表（按时间戳降序）
type versionList struct {
	versions []*versionEntry
	mu       sync.RWMutex
}

// versionEntry 版本条目
type versionEntry struct {
	timestamp *clock.HLC
	version   uint64
	value     []byte
	deleted   bool
	size      int
}

// NewMemoryMVStore 创建内存 MVStore
func NewMemoryMVStore(options *MVStoreOptions) (*MemoryMVStore, error) {
	if options == nil {
		options = DefaultOptions()
	}

	// 创建数据目录
	if err := os.MkdirAll(options.DataDir, 0755); err != nil {
		return nil, fmt.Errorf("创建数据目录失败: %w", err)
	}

	// 创建 WAL 目录
	if err := os.MkdirAll(options.WALDir, 0755); err != nil {
		return nil, fmt.Errorf("创建 WAL 目录失败: %w", err)
	}

	store := &MemoryMVStore{
		options: options,
		flushCh: make(chan struct{}, 1),
		doneCh:  make(chan struct{}),
		hlc:     clock.NewHLC(),
	}

	// 初始化 WAL
	if options.EnableWAL {
		wal, err := NewMetadataWAL(filepath.Join(options.WALDir, "metadata.wal"))
		if err != nil {
			return nil, fmt.Errorf("初始化 WAL 失败: %w", err)
		}
		store.wal = wal
	}

	// 初始化快照管理器
	snapMgr, err := NewSnapshotManager(options.DataDir)
	if err != nil {
		return nil, fmt.Errorf("初始化快照管理器失败: %w", err)
	}
	store.snapMgr = snapMgr

	// 启动后台刷盘协程
	go store.flushLoop()

	// 尝试从 WAL 恢复
	if err := store.recoverFromWAL(); err != nil {
		logging.Warnf("从 WAL 恢复失败: %v", err)
	}

	return store, nil
}

// Put 写入键值对
func (m *MemoryMVStore) Put(key string, value []byte) error {
	if m.closed.Load() {
		return ErrClosed
	}

	if key == "" {
		return fmt.Errorf("key 不能为空")
	}

	timestamp := m.hlc.Now()
	version := m.version.Add(1)

	entry := &versionEntry{
		timestamp: timestamp,
		version:   version,
		value:     value,
		deleted:   false,
		size:      len(value),
	}

	// 写入 WAL
	if m.wal != nil {
		walEntry := &WALEntry{
			Timestamp: timestamp,
			Type:      WALTypePut,
			Key:       key,
			Value:     value,
		}
		if err := m.wal.Append(walEntry); err != nil {
			return fmt.Errorf("写入 WAL 失败: %w", err)
		}
	}

	// 更新内存表
	vlist, _ := m.data.LoadOrStore(key, &versionList{})
	list := vlist.(*versionList)

	list.mu.Lock()
	list.versions = append(list.versions, entry)
	// 限制版本数量
	if m.options.MaxVersions > 0 && len(list.versions) > m.options.MaxVersions {
		// 删除最旧的版本
		removed := list.versions[0]
		m.memSize.Add(int64(-removed.size))
		list.versions = list.versions[1:]
	}
	list.mu.Unlock()

	// 更新内存大小
	m.memSize.Add(int64(entry.size))

	// 检查是否需要刷盘
	m.checkFlush()

	return nil
}

// Get 获取最新值
func (m *MemoryMVStore) Get(key string) ([]byte, error) {
	if m.closed.Load() {
		return nil, ErrClosed
	}

	vlist, ok := m.data.Load(key)
	if !ok {
		return nil, ErrNotFound
	}

	list := vlist.(*versionList)
	list.mu.RLock()
	defer list.mu.RUnlock()

	if len(list.versions) == 0 {
		return nil, ErrNotFound
	}

	// 返回最新版本
	latest := list.versions[len(list.versions)-1]
	if latest.deleted {
		return nil, ErrNotFound
	}

	// 返回值的副本，避免外部修改
	result := make([]byte, len(latest.value))
	copy(result, latest.value)
	return result, nil
}

// GetVersion 获取指定 HLC 时间戳的版本
func (m *MemoryMVStore) GetVersion(key string, hlcTimestamp *clock.HLC) ([]byte, error) {
	if m.closed.Load() {
		return nil, ErrClosed
	}

	if hlcTimestamp == nil {
		return m.Get(key)
	}

	vlist, ok := m.data.Load(key)
	if !ok {
		return nil, ErrNotFound
	}

	list := vlist.(*versionList)
	list.mu.RLock()
	defer list.mu.RUnlock()

	// 查找小于等于指定时间戳的最大版本
	for i := len(list.versions) - 1; i >= 0; i-- {
		v := list.versions[i]
		if v.timestamp.LessThan(hlcTimestamp) || v.timestamp.Equal(hlcTimestamp) {
			if v.deleted {
				return nil, ErrNotFound
			}
			result := make([]byte, len(v.value))
			copy(result, v.value)
			return result, nil
		}
	}

	return nil, ErrVersionNotFound
}

// Delete 删除 key
func (m *MemoryMVStore) Delete(key string) error {
	if m.closed.Load() {
		return ErrClosed
	}

	timestamp := m.hlc.Now()
	version := m.version.Add(1)

	// 写入 WAL
	if m.wal != nil {
		walEntry := &WALEntry{
			Timestamp: timestamp,
			Type:      WALTypeDelete,
			Key:       key,
		}
		if err := m.wal.Append(walEntry); err != nil {
			return fmt.Errorf("写入 WAL 失败: %w", err)
		}
	}

	// 写入墓碑标记
	entry := &versionEntry{
		timestamp: timestamp,
		version:   version,
		deleted:   true,
		size:      0,
	}

	vlist, _ := m.data.LoadOrStore(key, &versionList{})
	list := vlist.(*versionList)

	list.mu.Lock()
	list.versions = append(list.versions, entry)
	list.mu.Unlock()

	return nil
}

// Exists 检查 key 是否存在
func (m *MemoryMVStore) Exists(key string) (bool, error) {
	if m.closed.Load() {
		return false, ErrClosed
	}

	_, err := m.Get(key)
	if err != nil {
		if err == ErrNotFound {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// List 列出所有 key
func (m *MemoryMVStore) List(offset, limit int) ([]string, error) {
	if m.closed.Load() {
		return nil, ErrClosed
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var keys []string
	m.data.Range(func(key, value any) bool {
		vlist := value.(*versionList)
		vlist.mu.RLock()
		hasValue := len(vlist.versions) > 0 && !vlist.versions[len(vlist.versions)-1].deleted
		vlist.mu.RUnlock()

		if hasValue {
			keys = append(keys, key.(string))
		}
		return true
	})

	// 分页
	if offset >= len(keys) {
		return []string{}, nil
	}

	end := offset + limit
	if limit <= 0 || end > len(keys) {
		end = len(keys)
	}

	return keys[offset:end], nil
}

// ListPrefix 列出指定前缀的 key
func (m *MemoryMVStore) ListPrefix(prefix string, offset, limit int) ([]string, error) {
	if m.closed.Load() {
		return nil, ErrClosed
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var keys []string
	m.data.Range(func(key, value any) bool {
		keyStr := key.(string)
		if len(keyStr) >= len(prefix) && keyStr[:len(prefix)] == prefix {
			vlist := value.(*versionList)
			vlist.mu.RLock()
			hasValue := len(vlist.versions) > 0 && !vlist.versions[len(vlist.versions)-1].deleted
			vlist.mu.RUnlock()

			if hasValue {
				keys = append(keys, keyStr)
			}
		}
		return true
	})

	// 分页
	if offset >= len(keys) {
		return []string{}, nil
	}

	end := offset + limit
	if limit <= 0 || end > len(keys) {
		end = len(keys)
	}

	return keys[offset:end], nil
}

// GetVersionCount 获取版本数量
func (m *MemoryMVStore) GetVersionCount(key string) (int, error) {
	if m.closed.Load() {
		return 0, ErrClosed
	}

	vlist, ok := m.data.Load(key)
	if !ok {
		return 0, nil
	}

	list := vlist.(*versionList)
	list.mu.RLock()
	defer list.mu.RUnlock()

	return len(list.versions), nil
}

// GetAllVersions 获取所有版本信息
func (m *MemoryMVStore) GetAllVersions(key string) ([]*VersionInfo, error) {
	if m.closed.Load() {
		return nil, ErrClosed
	}

	vlist, ok := m.data.Load(key)
	if !ok {
		return []*VersionInfo{}, nil
	}

	list := vlist.(*versionList)
	list.mu.RLock()
	defer list.mu.RUnlock()

	infos := make([]*VersionInfo, 0, len(list.versions))
	for _, v := range list.versions {
		infos = append(infos, &VersionInfo{
			Timestamp: v.timestamp,
			Version:   v.version,
			Deleted:   v.deleted,
			Size:      v.size,
		})
	}

	return infos, nil
}

// Flush 刷盘
func (m *MemoryMVStore) Flush() error {
	if m.closed.Load() {
		return ErrClosed
	}

	logging.Info("开始刷盘...")

	// 通过快照管理器保存快照
	if m.snapMgr != nil {
		if err := m.snapMgr.Create(m); err != nil {
			return fmt.Errorf("创建快照失败: %w", err)
		}
	}

	// WAL checkpoint
	if m.wal != nil {
		if err := m.wal.Sync(); err != nil {
			return fmt.Errorf("WAL sync 失败: %w", err)
		}
	}

	m.lastFlush.Store(time.Now().Unix())
	logging.Info("刷盘完成")

	return nil
}

// CreateSnapshot 创建快照
func (m *MemoryMVStore) CreateSnapshot() ([]byte, error) {
	if m.closed.Load() {
		return nil, ErrClosed
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	snapshot := make(map[string][]*versionEntry)

	m.data.Range(func(key, value any) bool {
		list := value.(*versionList)
		list.mu.RLock()
		// 复制版本列表
		versions := make([]*versionEntry, len(list.versions))
		copy(versions, list.versions)
		snapshot[key.(string)] = versions
		list.mu.RUnlock()
		return true
	})

	return json.Marshal(snapshot)
}

// RestoreFromSnapshot 从快照恢复
func (m *MemoryMVStore) RestoreFromSnapshot(snapshot []byte) error {
	if m.closed.Load() {
		return ErrClosed
	}

	var data map[string][]*versionEntry
	if err := json.Unmarshal(snapshot, &data); err != nil {
		return fmt.Errorf("反序列化快照失败: %w", err)
	}

	// 清空当前数据
	m.data.Range(func(key, value any) bool {
		m.data.Delete(key)
		return true
	})

	// 恢复数据
	maxVersion := uint64(0)
	for key, versions := range data {
		vlist := &versionList{
			versions: versions,
		}
		m.data.Store(key, vlist)

		// 更新最大版本号
		for _, v := range versions {
			if v.version > maxVersion {
				maxVersion = v.version
			}
			m.memSize.Add(int64(v.size))
		}
	}

	m.version.Store(maxVersion)

	return nil
}

// Close 关闭存储
func (m *MemoryMVStore) Close() error {
	if !m.closed.CompareAndSwap(false, true) {
		return nil // 已经关闭
	}

	// 发送停止信号
	close(m.doneCh)

	// 最后刷盘
	if err := m.Flush(); err != nil {
		logging.Errorf("关闭前刷盘失败: %v", err)
	}

	// 关闭 WAL
	if m.wal != nil {
		if err := m.wal.Close(); err != nil {
			logging.Errorf("关闭 WAL 失败: %v", err)
		}
	}

	// 关闭快照管理器
	if m.snapMgr != nil {
		if err := m.snapMgr.Close(); err != nil {
			logging.Errorf("关闭快照管理器失败: %v", err)
		}
	}

	logging.Info("MemoryMVStore 已关闭")
	return nil
}

// flushLoop 后台刷盘协程
func (m *MemoryMVStore) flushLoop() {
	if m.options.FlushInterval > 0 {
		// 有定时刷盘间隔
		ticker := time.NewTicker(time.Duration(m.options.FlushInterval) * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if err := m.Flush(); err != nil {
					logging.Errorf("定时刷盘失败: %v", err)
				}
			case <-m.flushCh:
				if err := m.Flush(); err != nil {
					logging.Errorf("触发刷盘失败: %v", err)
				}
			case <-m.doneCh:
				return
			}
		}
	} else {
		// 无定时刷盘，仅响应手动触发
		for {
			select {
			case <-m.flushCh:
				if err := m.Flush(); err != nil {
					logging.Errorf("触发刷盘失败: %v", err)
				}
			case <-m.doneCh:
				return
			}
		}
	}
}

// checkFlush 检查是否需要刷盘
func (m *MemoryMVStore) checkFlush() {
	if m.options.MemTableSize > 0 && m.memSize.Load() >= m.options.MemTableSize {
		select {
		case m.flushCh <- struct{}{}:
		default:
		}
	}
}

// recoverFromWAL 从 WAL 恢复
func (m *MemoryMVStore) recoverFromWAL() error {
	if m.wal == nil {
		return nil
	}

	logging.Info("从 WAL 恢复数据...")

	entries, err := m.wal.Recover()
	if err != nil {
		return fmt.Errorf("读取 WAL 失败: %w", err)
	}

	if len(entries) == 0 {
		logging.Info("WAL 为空，无需恢复")
		return nil
	}

	recovered := 0
	for _, entry := range entries {
		switch entry.Type {
		case WALTypePut:
			if err := m.applyPut(entry); err != nil {
				logging.Errorf("应用 WAL Put 失败: %v", err)
				continue
			}
			recovered++
		case WALTypeDelete:
			if err := m.applyDelete(entry); err != nil {
				logging.Errorf("应用 WAL Delete 失败: %v", err)
				continue
			}
			recovered++
		case WALTypeCheckpoint:
			// 检查点，截断之前的 WAL
			logging.Info("遇到 WAL checkpoint")
		}
	}

	logging.Infof("从 WAL 恢复了 %d 条记录", recovered)

	return nil
}

// applyPut 应用 Put 操作（不写 WAL）
func (m *MemoryMVStore) applyPut(entry *WALEntry) error {
	version := m.version.Add(1)

	verEntry := &versionEntry{
		timestamp: entry.Timestamp,
		version:   version,
		value:     entry.Value,
		deleted:   false,
		size:      len(entry.Value),
	}

	vlist, _ := m.data.LoadOrStore(entry.Key, &versionList{})
	list := vlist.(*versionList)

	list.mu.Lock()
	list.versions = append(list.versions, verEntry)
	list.mu.Unlock()

	m.memSize.Add(int64(verEntry.size))

	return nil
}

// applyDelete 应用 Delete 操作（不写 WAL）
func (m *MemoryMVStore) applyDelete(entry *WALEntry) error {
	version := m.version.Add(1)

	verEntry := &versionEntry{
		timestamp: entry.Timestamp,
		version:   version,
		deleted:   true,
		size:      0,
	}

	vlist, _ := m.data.LoadOrStore(entry.Key, &versionList{})
	list := vlist.(*versionList)

	list.mu.Lock()
	list.versions = append(list.versions, verEntry)
	list.mu.Unlock()

	return nil
}

// Stats 获取统计信息（实现 StatProvider）
func (m *MemoryMVStore) Stats() (*Stats, error) {
	if m.closed.Load() {
		return nil, ErrClosed
	}

	stats := &Stats{
		MemTableSize:  m.memSize.Load(),
		LastFlushTime: m.lastFlush.Load(),
	}

	// 统计 key 和版本数量
	m.data.Range(func(key, value any) bool {
		vlist := value.(*versionList)
		vlist.mu.RLock()
		stats.VersionCount += len(vlist.versions)
		if len(vlist.versions) > 0 {
			stats.KeyCount++
		}
		vlist.mu.RUnlock()
		return true
	})

	// 统计 WAL 大小
	if m.wal != nil {
		if wal, ok := m.wal.(*MetadataWAL); ok {
			if info, err := wal.file.Stat(); err == nil {
				stats.WALSize = info.Size()
			}
		}
	}

	return stats, nil
}

// NewIterator 创建迭代器
func (m *MemoryMVStore) NewIterator() Iterator {
	return &memoryIterator{
		store: m,
		keys:  make([]string, 0),
		index: -1,
	}
}

// memoryIterator 内存迭代器实现
type memoryIterator struct {
	store *MemoryMVStore
	keys  []string
	index int
	mu    sync.RWMutex
}

func (it *memoryIterator) Next() bool {
	it.mu.Lock()
	defer it.mu.Unlock()
	it.index++
	return it.index < len(it.keys)
}

func (it *memoryIterator) Key() string {
	it.mu.RLock()
	defer it.mu.RUnlock()
	if it.index < 0 || it.index >= len(it.keys) {
		return ""
	}
	return it.keys[it.index]
}

func (it *memoryIterator) Value() ([]byte, error) {
	it.mu.RLock()
	defer it.mu.RUnlock()
	if it.index < 0 || it.index >= len(it.keys) {
		return nil, io.EOF
	}
	return it.store.Get(it.keys[it.index])
}

func (it *memoryIterator) Timestamp() *clock.HLC {
	it.mu.RLock()
	defer it.mu.RUnlock()
	if it.index < 0 || it.index >= len(it.keys) {
		return nil
	}

	vlist, ok := it.store.data.Load(it.keys[it.index])
	if !ok {
		return nil
	}

	list := vlist.(*versionList)
	list.mu.RLock()
	defer list.mu.RUnlock()

	if len(list.versions) == 0 {
		return nil
	}

	return list.versions[len(list.versions)-1].timestamp
}

func (it *memoryIterator) Release() error {
	it.mu.Lock()
	defer it.mu.Unlock()
	it.keys = nil
	it.index = -1
	return nil
}

func (it *memoryIterator) Error() error {
	return nil
}
