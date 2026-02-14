// Package degradation 提供分区降级管理器实现
//
// DEGR-3: 降级管理器
//
// 核心功能：
//   - 监控分区状态，自动切换一致性级别
//   - 正常状态：使用 Quorum 写入（强一致）
//   - 分区状态：降级到本地写入 + Gossip 同步（最终一致）
//   - 记录降级日志，分区恢复后同步
//
// 降级策略：
//  1. 检测到 Quorum 不可达时，进入降级状态
//  2. 写入本地存储，记录降级日志
//  3. 触发 Gossip 事件传播
//  4. 分区恢复后，同步降级日志到 Quorum
package degradation

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/kvstore"
	"github.com/jzhang405/NexKV/internal/metadata/partition"
	"github.com/jzhang405/NexKV/internal/metadata/quorum"
)

// ==================== 状态定义 ====================

// ConsistencyLevel 一致性级别
type ConsistencyLevel int

const (
	// ConsistencyLevelStrong 强一致性（Quorum）
	ConsistencyLevelStrong ConsistencyLevel = iota
	// ConsistencyLevelEventual 最终一致性（Gossip）
	ConsistencyLevelEventual
)

// String 返回一致性级别的字符串表示
func (l ConsistencyLevel) String() string {
	switch l {
	case ConsistencyLevelStrong:
		return "strong"
	case ConsistencyLevelEventual:
		return "eventual"
	default:
		return "unknown"
	}
}

// DegradationStatus 降级状态
type DegradationStatus struct {
	// IsDegraded 是否处于降级状态
	IsDegraded bool

	// ConsistencyLevel 当前一致性级别
	ConsistencyLevel ConsistencyLevel

	// DegradedSince 降级开始时间
	DegradedSince time.Time

	// DegradedDuration 降级持续时间
	DegradedDuration time.Duration

	// PendingLogEntries 待同步的降级日志数量
	PendingLogEntries int

	// PartitionStatus 分区状态
	PartitionStatus partition.PartitionStatus
}

// ==================== 降级日志（简化版，DEGR-4 将实现 WAL）====================

// DemotionEntry 降级日志条目
type DemotionEntry struct {
	// ID 条目 ID
	ID string

	// Namespace 命名空间
	Namespace string

	// Key 键
	Key string

	// Value 值
	Value []byte

	// Timestamp 时间戳
	Timestamp time.Time

	// Synced 是否已同步到 Quorum
	Synced bool
}

// DemotionLog 降级日志（内存版本）
//
// 注意：这是一个简化版本，DEGR-4 将实现 WAL 持久化版本
type DemotionLog struct {
	mu      sync.RWMutex
	entries []*DemotionEntry
	idSeq   int
}

// NewDemotionLog 创建降级日志
func NewDemotionLog() *DemotionLog {
	return &DemotionLog{
		entries: make([]*DemotionEntry, 0),
	}
}

// Append 添加降级日志条目
func (l *DemotionLog) Append(namespace, key string, value []byte) *DemotionEntry {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.idSeq++
	entry := &DemotionEntry{
		ID:        generateEntryID(l.idSeq),
		Namespace: namespace,
		Key:       key,
		Value:     value,
		Timestamp: time.Now(),
		Synced:    false,
	}
	l.entries = append(l.entries, entry)

	return entry
}

// GetUnsynced 获取未同步的条目
func (l *DemotionLog) GetUnsynced() []*DemotionEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	var unsynced []*DemotionEntry
	for _, entry := range l.entries {
		if !entry.Synced {
			unsynced = append(unsynced, entry)
		}
	}
	return unsynced
}

// MarkSynced 标记条目为已同步
func (l *DemotionLog) MarkSynced(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	for _, entry := range l.entries {
		if entry.ID == id {
			entry.Synced = true
			break
		}
	}
}

// ClearSynced 清除已同步的条目
func (l *DemotionLog) ClearSynced() {
	l.mu.Lock()
	defer l.mu.Unlock()

	var unsynced []*DemotionEntry
	for _, entry := range l.entries {
		if !entry.Synced {
			unsynced = append(unsynced, entry)
		}
	}
	l.entries = unsynced
}

// Count 获取日志条目数量
func (l *DemotionLog) Count() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.entries)
}

// UnsyncedCount 获取未同步条目数量
func (l *DemotionLog) UnsyncedCount() int {
	l.mu.RLock()
	defer l.mu.RUnlock()

	count := 0
	for _, entry := range l.entries {
		if !entry.Synced {
			count++
		}
	}
	return count
}

// generateEntryID 生成条目 ID
func generateEntryID(seq int) string {
	return fmt.Sprintf("%s-%04d", time.Now().Format("20060102-150405"), seq)
}

// padSeq 已移除，使用 fmt.Sprintf 替代

// ==================== 降级管理器 ====================

// WriteNotifier 写入通知器接口
// 用于在写入后触发 Gossip 事件
type WriteNotifier interface {
	// OnWrite 写入事件通知
	OnWrite(namespace, key string)
}

// QuorumWriter Quorum 写入器接口
type QuorumWriter interface {
	// PutWithQuorum 使用 Quorum 机制写入
	PutWithQuorum(ctx context.Context, ns, key string, value any, opts *quorum.PutOptions) error
}

// LocalWriter 本地写入器接口
type LocalWriter interface {
	// Put 写入本地存储
	Put(ctx context.Context, ns, key string, value any) error
}

// Manager 降级管理器
type Manager struct {
	mu sync.RWMutex

	// 分区检测器
	detector *partition.Detector

	// 写入组件
	quorumWriter QuorumWriter
	localWriter  LocalWriter
	notifier     WriteNotifier

	// 降级日志
	demotionLog *DemotionLog

	// 降级状态
	isDegraded    bool
	degradedSince time.Time

	// 配置
	autoRecover bool // 是否自动恢复（默认 true）
}

// ManagerConfig 降级管理器配置
type ManagerConfig struct {
	// Detector 分区检测器
	Detector *partition.Detector

	// QuorumWriter Quorum 写入器
	QuorumWriter QuorumWriter

	// LocalWriter 本地写入器
	LocalWriter LocalWriter

	// Notifier 写入通知器（可选）
	Notifier WriteNotifier

	// DemotionLog 降级日志（可选，不提供则创建内存版本）
	DemotionLog *DemotionLog

	// AutoRecover 是否自动恢复（默认 true）
	AutoRecover bool
}

// NewManager 创建降级管理器
func NewManager(config *ManagerConfig) *Manager {
	if config == nil {
		panic("config cannot be nil")
	}

	if config.Detector == nil {
		panic("detector cannot be nil")
	}

	if config.QuorumWriter == nil {
		panic("quorumWriter cannot be nil")
	}

	if config.LocalWriter == nil {
		panic("localWriter cannot be nil")
	}

	demotionLog := config.DemotionLog
	if demotionLog == nil {
		demotionLog = NewDemotionLog()
	}

	// 默认启用自动恢复
	// 注意：当前实现始终启用自动恢复，后续可通过指针类型支持显式禁用
	autoRecover := true

	return &Manager{
		detector:      config.Detector,
		quorumWriter:  config.QuorumWriter,
		localWriter:   config.LocalWriter,
		notifier:      config.Notifier,
		demotionLog:   demotionLog,
		autoRecover:   autoRecover,
		isDegraded:    false,
		degradedSince: time.Time{},
	}
}

// Write 写入（自动降级）
//
// 根据分区状态自动选择一致性级别：
//   - Quorum 可达：使用强一致性写入
//   - Quorum 不可达：降级到本地写入 + 降级日志
func (m *Manager) Write(ctx context.Context, ns, key string, value []byte) error {
	status := m.detector.CheckPartition()

	if status.CanReachQuorum {
		// 正常：使用 Quorum
		return m.writeWithQuorum(ctx, ns, key, value)
	}

	// 降级：使用本地写入
	return m.writeWithDegradation(ctx, ns, key, value)
}

// writeWithQuorum 使用 Quorum 写入
func (m *Manager) writeWithQuorum(ctx context.Context, ns, key string, value []byte) error {
	// 如果之前是降级状态，尝试恢复
	if m.isDegraded && m.autoRecover {
		if err := m.tryRecover(ctx); err != nil {
			logging.WithFields(map[string]interface{}{
				"namespace": ns,
				"key":       key,
				"error":     err.Error(),
			}).Warn("自动恢复失败，继续使用 Quorum 写入")
		}
	}

	err := m.quorumWriter.PutWithQuorum(ctx, ns, key, value, nil)
	if err != nil {
		logging.WithFields(map[string]interface{}{
			"namespace": ns,
			"key":       key,
			"error":     err.Error(),
		}).Error("Quorum 写入失败")
		return err
	}

	// 通知写入事件
	if m.notifier != nil {
		m.notifier.OnWrite(ns, key)
	}

	return nil
}

// writeWithDegradation 使用降级模式写入
func (m *Manager) writeWithDegradation(ctx context.Context, ns, key string, value []byte) error {
	// 进入降级状态
	m.mu.Lock()
	if !m.isDegraded {
		m.isDegraded = true
		m.degradedSince = time.Now()
		logging.WithFields(map[string]interface{}{
			"namespace": ns,
			"key":       key,
		}).Warn("进入降级模式")
	}
	m.mu.Unlock()

	// 写入本地存储
	if err := m.localWriter.Put(ctx, ns, key, value); err != nil {
		logging.WithFields(map[string]interface{}{
			"namespace": ns,
			"key":       key,
			"error":     err.Error(),
		}).Error("本地写入失败")
		return err
	}

	// 记录降级日志
	m.demotionLog.Append(ns, key, value)

	// 通知写入事件（触发 Gossip）
	if m.notifier != nil {
		m.notifier.OnWrite(ns, key)
	}

	logging.WithFields(map[string]interface{}{
		"namespace": ns,
		"key":       key,
		"pending":   m.demotionLog.UnsyncedCount(),
	}).Info("降级写入成功")

	return nil
}

// tryRecover 尝试恢复（同步降级日志）
func (m *Manager) tryRecover(ctx context.Context) error {
	unsynced := m.demotionLog.GetUnsynced()
	if len(unsynced) == 0 {
		// 没有待同步的日志，重置降级状态
		m.ResetDegraded()
		return nil
	}

	logging.WithFields(map[string]interface{}{
		"count": len(unsynced),
	}).Info("开始同步降级日志")

	syncedCount := 0
	for _, entry := range unsynced {
		if err := m.quorumWriter.PutWithQuorum(ctx, entry.Namespace, entry.Key, entry.Value, nil); err != nil {
			logging.WithFields(map[string]interface{}{
				"id":        entry.ID,
				"namespace": entry.Namespace,
				"key":       entry.Key,
				"error":     err.Error(),
			}).Error("同步降级日志失败")
			continue
		}
		m.demotionLog.MarkSynced(entry.ID)
		syncedCount++
	}

	logging.WithFields(map[string]interface{}{
		"total":  len(unsynced),
		"synced": syncedCount,
		"failed": len(unsynced) - syncedCount,
	}).Info("降级日志同步完成")

	// 清除已同步的条目
	m.demotionLog.ClearSynced()

	// 如果全部同步成功，重置降级状态
	if syncedCount == len(unsynced) {
		m.ResetDegraded()
	}

	return nil
}

// Recover 手动触发恢复
func (m *Manager) Recover(ctx context.Context) error {
	status := m.detector.CheckPartition()
	if !status.CanReachQuorum {
		return nil // 仍在分区中
	}

	return m.tryRecover(ctx)
}

// ResetDegraded 重置降级状态
func (m *Manager) ResetDegraded() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.isDegraded {
		duration := time.Since(m.degradedSince)
		logging.WithFields(map[string]interface{}{
			"duration": duration.String(),
		}).Info("退出降级模式")
	}

	m.isDegraded = false
	m.degradedSince = time.Time{}
}

// GetStatus 获取降级状态
func (m *Manager) GetStatus() DegradationStatus {
	m.mu.RLock()
	isDegraded := m.isDegraded
	degradedSince := m.degradedSince
	m.mu.RUnlock()

	var duration time.Duration
	if isDegraded {
		duration = time.Since(degradedSince)
	}

	status := m.detector.CheckPartition()

	var consistencyLevel ConsistencyLevel
	if isDegraded {
		consistencyLevel = ConsistencyLevelEventual
	} else {
		consistencyLevel = ConsistencyLevelStrong
	}

	return DegradationStatus{
		IsDegraded:        isDegraded,
		ConsistencyLevel:  consistencyLevel,
		DegradedSince:     degradedSince,
		DegradedDuration:  duration,
		PendingLogEntries: m.demotionLog.UnsyncedCount(),
		PartitionStatus:   status,
	}
}

// IsDegraded 是否处于降级状态
func (m *Manager) IsDegraded() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.isDegraded
}

// GetConsistencyLevel 获取当前一致性级别
func (m *Manager) GetConsistencyLevel() ConsistencyLevel {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.isDegraded {
		return ConsistencyLevelEventual
	}
	return ConsistencyLevelStrong
}

// GetDemotionLog 获取降级日志
func (m *Manager) GetDemotionLog() *DemotionLog {
	return m.demotionLog
}

// GetDetector 获取分区检测器
func (m *Manager) GetDetector() *partition.Detector {
	return m.detector
}

// ==================== 确保接口实现 ====================

var (
	_ LocalWriter = (*kvstore.MetadataKV)(nil)
)
