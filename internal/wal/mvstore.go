// Package store 提供 MVStore 存储抽象接口
// MVStore 是一个支持多版本并发控制（MVCC）的键值存储
package store

import (
	"sync"

	"github.com/jzhang405/NexKV/internal/clock"
)

// MVStore 多版本键值存储接口
//
// 核心特性:
//   - MVCC 支持：同一 key 可有多个版本，使用 HLC 时间戳区分
//   - 原子操作：Put/Delete/GetVersion 操作是原子的
//   - 持久化：通过 WAL 保证数据持久化
//   - 崩溃恢复：支持从 WAL 恢复数据
//
// 使用场景:
//   - 元数据存储：分片元数据、节点元数据
//   - 版本管理：支持历史版本查询
//   - 并发访问：多 goroutine 安全访问
type MVStore interface {
	// Put 写入键值对
	// 自动生成新的版本号（HLC 时间戳）
	// 如果 key 已存在，创建新版本，旧版本仍然可访问
	Put(key string, value []byte) error

	// Get 获取 key 的最新版本值
	// 如果 key 不存在，返回 ErrNotFound
	Get(key string) ([]byte, error)

	// GetVersion 获取 key 在指定 HLC 时间戳的版本值
	// 返回小于等于指定 hlcTimestamp 的最大版本
	// 如果不存在符合条件的版本，返回 ErrNotFound
	GetVersion(key string, hlcTimestamp *clock.HLC) ([]byte, error)

	// Delete 删除 key
	// 实际上是写入一个墓碑标记，最新版本为删除状态
	Delete(key string) error

	// Exists 检查 key 是否存在（未被删除）
	Exists(key string) (bool, error)

	// List 列出所有 key（分页支持）
	// offset: 起始位置
	// limit: 返回数量（0 表示不限制）
	List(offset, limit int) ([]string, error)

	// ListPrefix 列出指定前缀的所有 key
	ListPrefix(prefix string, offset, limit int) ([]string, error)

	// GetVersionCount 获取 key 的版本数量
	GetVersionCount(key string) (int, error)

	// GetAllVersions 获取 key 的所有版本信息
	GetAllVersions(key string) ([]*VersionInfo, error)

	// Flush 刷盘操作
	// 将内存数据持久化到磁盘
	Flush() error

	// CreateSnapshot 创建快照
	// 返回当前时刻的完整数据快照
	CreateSnapshot() ([]byte, error)

	// RestoreFromSnapshot 从快照恢复数据
	RestoreFromSnapshot(snapshot []byte) error

	// Close 关闭存储
	// 释放资源，刷盘
	Close() error
}

// VersionInfo 版本信息
type VersionInfo struct {
	Timestamp *clock.HLC // 版本时间戳（HLC）
	Version   uint64     // 版本号
	Deleted   bool       // 是否为墓碑标记（删除标记）
	Size      int        // 值的大小
}

// WALEntry WAL 日志条目
//
// WAL (Write-Ahead Log) 用于崩溃恢复：
//   - 所有写操作先写 WAL，再更新内存表
//   - 崩溃后通过重放 WAL 恢复数据
//   - 定期 checkpoint 清理已应用的日志
type WALEntry struct {
	// Timestamp 操作时间戳（HLC）
	// 用于版本控制和恢复时确定操作顺序
	Timestamp *clock.HLC

	// Type 操作类型
	Type WALType

	// Key 键
	Key []byte

	// Value 值（Type = WALTypePut 时有效）
	Value []byte

	// OldValue 旧值（用于 MVCC 冲突检测，可选）
	OldValue []byte

	// Checksum 校验和（保证数据完整性）
	Checksum uint32
}

// WALType WAL 操作类型
type WALType uint16

const (
	// WALTypePut 写入操作
	WALTypePut WALType = iota

	// WALTypeDelete 删除操作
	WALTypeDelete

	// WALTypeCheckpoint 检查点操作
	// 标识此点之前的日志可以删除
	WALTypeCheckpoint
)

// WAL WAL 日志接口
//
// 核心功能:
//   - 追加写入：Append 操作是原子追加的
//   - 持久化保证：刷盘后才能返回
//   - 崩溃恢复：Recover 重放未应用的日志
//   - 精简：Truncate 删除已应用的日志
type WAL interface {
	// Append 追加日志条目
	// 必须持久化到磁盘后才能返回
	Append(entry *WALEntry) error

	// appendNoSync 追加日志条目（内部方法，不触发 Sync）
	// 用于批量写入优化，调用者需自行在批量写入完成后调用 Sync()
	appendNoSync(entry *WALEntry) error

	// Recover 从 WAL 恢复数据
	// 重放所有日志条目，返回操作序列
	Recover() ([]*WALEntry, error)

	// Truncate 截断 WAL
	// 删除指定 offset 之前的日志
	Truncate(offset int64) error

	// Sync 强制刷盘
	Sync() error

	// Close 关闭 WAL
	Close() error
}

// SnapshotManager 快照管理接口
//
// 快照用于加速恢复：
//   - 定期创建全量快照
//   - 恢复时先加载快照，再重放增量 WAL
//   - 减少恢复时间
type SnapshotManager interface {
	// Create 创建快照（从 MVStore 获取数据并保存）
	Create(store MVStore) error

	// List 列出所有快照
	List() ([]string, error)

	// Restore 从快照恢复（返回快照数据）
	Restore(snapshotName string) ([]byte, error)

	// Delete 删除快照
	Delete(snapshotName string) error

	// Close 关闭快照管理器
	Close() error
}

// versionEntry 单个版本条目
type versionEntry struct {
	timestamp *clock.HLC
	version   uint64
	value     []byte
	deleted   bool
	size      int
}

// versionList 版本列表
type versionList struct {
	versions []*versionEntry
	mu       sync.RWMutex
}

// VersionedEntry 带版本的数据条目
type VersionedEntry struct {
	Key       string
	Value     []byte
	Timestamp *clock.HLC
	Version   uint64
	Deleted   bool
}

// MVStoreOptions MVStore 配置选项
type MVStoreOptions struct {
	// DataDir 数据目录
	DataDir string

	// WALDir WAL 目录
	WALDir string

	// MemTableSize 内存表大小限制（字节）
	// 超过后触发刷盘
	MemTableSize int64

	// FlushInterval 刷盘间隔
	// 定期刷盘保证数据持久化
	FlushInterval int64 // 秒

	// EnableWAL 是否启用 WAL
	EnableWAL bool

	// WALSyncSize WAL 同步大小
	// 每 N 条日志强制刷盘
	WALSsyncSize int

	// MaxVersions 最大版本数
	// 超过后触发旧版本清理
	MaxVersions int
}

// DefaultOptions 默认配置
//
// 注意：DataDir 和 WALDir 应该通过配置来设置
// 正式环境使用：{NEXKV_BASE_DIR}/{host_id}/metadata 和 {NEXKV_BASE_DIR}/{host_id}/wal
// 测试环境使用：临时目录（t.TempDir()）
func DefaultOptions() *MVStoreOptions {
	return &MVStoreOptions{
		DataDir:       "",               // 空值表示需要通过配置设置，降级使用 "./data/metadata"
		WALDir:        "",               // 空值表示需要通过配置设置，降级使用 "./data/wal"
		MemTableSize:  64 * 1024 * 1024, // 64MB
		FlushInterval: 5,                // 5 秒
		EnableWAL:     true,
		WALSsyncSize:  1000, // 每 1000 条日志刷盘
		MaxVersions:   10,   // 最多保留 10 个版本
	}
}

// KVStore 简单键值存储接口（不支持版本）
// 用于不需要 MVCC 的场景
type KVStore interface {
	Put(key string, value []byte) error
	Get(key string) ([]byte, error)
	Delete(key string) error
	Exists(key string) (bool, error)
	List(offset, limit int) ([]string, error)
	Close() error
}

// Iterator 迭代器接口
type Iterator interface {
	// Next 移动到下一个元素
	Next() bool

	// Key 当前 key
	Key() string

	// Value 当前 value
	Value() ([]byte, error)

	// Timestamp 当前时间戳
	Timestamp() *clock.HLC

	// Release 释放迭代器
	Release() error

	// Error 返回迭代过程中的错误
	Error() error
}

// Reader 只读接口
type Reader interface {
	Get(key string) ([]byte, error)
	GetVersion(key string, hlcTimestamp *clock.HLC) ([]byte, error)
	Exists(key string) (bool, error)
	List(offset, limit int) ([]string, error)
	NewIterator() Iterator
}

// Writer 只写接口
type Writer interface {
	Put(key string, value []byte) error
	Delete(key string) error
	Flush() error
}

// Batch 批量操作接口
type Batch interface {
	// Put 批量写入
	Put(key string, value []byte) error

	// Delete 批量删除
	Delete(key string) error

	// Write 提交批量操作
	Write() error

	// Clear 清空批量操作
	Clear()

	// Size 返回批量操作数量
	Size() int
}

// ReaderWriter 读写接口
type ReaderWriter interface {
	Reader
	Writer
}

// Stats 存储统计信息
type Stats struct {
	// KeyCount key 数量
	KeyCount int

	// VersionCount 版本数量
	VersionCount int

	// MemTableSize 内存表大小
	MemTableSize int64

	// WALSize WAL 大小
	WALSize int64

	// LastFlushTime 最后刷盘时间
	LastFlushTime int64
}

// StatProvider 统计信息接口
type StatProvider interface {
	// Stats 获取统计信息
	Stats() (*Stats, error)
}
