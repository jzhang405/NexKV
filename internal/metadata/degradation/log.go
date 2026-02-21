// Package degradation 提供降级日志的 WAL 持久化实现
//
// DEGR-4: 降级日志系统（含 WAL）
//
// 核心功能：
//   - WAL 持久化：降级日志条目持久化到磁盘
//   - 崩溃恢复：重启后恢复未同步的降级日志
//   - Compaction：清理已同步的日志条目
//   - 批量写入：支持批量追加优化性能
//
// 存储格式：
//   - Key: demotion:{entryID}
//   - Value: JSON 序列化的 DemotionEntry
//   - 使用 MetadataKV 的 WAL 机制确保持久化
package degradation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/clock"
	"github.com/jzhang405/NexKV/internal/config/logging"
)

// ==================== WAL 降级日志条目 ====================

// WALDemotionEntry WAL 持久化的降级日志条目
type WALDemotionEntry struct {
	// ID 条目 ID（唯一标识）
	ID string `json:"id"`

	// Namespace 命名空间
	Namespace string `json:"namespace"`

	// Key 键
	Key string `json:"key"`

	// Value 值（Base64 编码）
	Value []byte `json:"value"`

	// Timestamp 时间戳（HLC）
	Timestamp string `json:"timestamp"`

	// HLC HLC 时间戳（用于排序和冲突解决）
	HLC *clock.HLC `json:"hlc"`

	// Synced 是否已同步到 Quorum
	Synced bool `json:"synced"`

	// SyncedAt 同步时间（如果已同步）
	SyncedAt string `json:"synced_at,omitempty"`

	// CheckpointID 检查点 ID（用于 Compaction）
	CheckpointID string `json:"checkpoint_id,omitempty"`
}

// ==================== WAL 存储接口 ====================

// WALStore WAL 存储接口
//
// 抽象底层存储实现，支持使用 MetadataKV 或直接的 WAL
type WALStore interface {
	// Put 持久化写入
	Put(ctx context.Context, key string, value []byte) error

	// Get 读取
	Get(ctx context.Context, key string) ([]byte, error)

	// Delete 删除
	Delete(ctx context.Context, key string) error

	// Scan 前缀扫描
	Scan(ctx context.Context, prefix string) (map[string][]byte, error)
}

// ==================== WAL 降级日志 ====================

// WALDemotionLog WAL 持久化的降级日志
type WALDemotionLog struct {
	mu sync.RWMutex

	// 存储后端
	store WALStore

	// 内存缓存（加速读取）
	entries map[string]*WALDemotionEntry

	// 序列号生成器
	idSeq int

	// 检查点 ID（用于 Compaction）
	checkpointID string

	// 上次 Compaction 时间
	lastCompaction time.Time

	// Compaction 间隔（默认 1 分钟）
	compactionInterval time.Duration

	// 待同步条目数
	unsyncedCount int
}

// WALDemotionLogConfig WAL 降级日志配置
type WALDemotionLogConfig struct {
	// Store WAL 存储后端
	Store WALStore

	// CompactionInterval Compaction 间隔（默认 1 分钟）
	CompactionInterval time.Duration
}

// NewWALDemotionLog 创建 WAL 降级日志
func NewWALDemotionLog(config *WALDemotionLogConfig) (*WALDemotionLog, error) {
	if config == nil || config.Store == nil {
		return nil, fmt.Errorf("store cannot be nil")
	}

	compactionInterval := config.CompactionInterval
	if compactionInterval <= 0 {
		compactionInterval = time.Minute
	}

	log := &WALDemotionLog{
		store:              config.Store,
		entries:            make(map[string]*WALDemotionEntry),
		compactionInterval: compactionInterval,
	}

	// 从存储中恢复
	if err := log.recover(); err != nil {
		return nil, fmt.Errorf("recovery failed: %w", err)
	}

	return log, nil
}

// recover 从存储中恢复降级日志
func (l *WALDemotionLog) recover() error {
	ctx := context.Background()

	// 扫描所有降级日志条目
	data, err := l.store.Scan(ctx, "demotion:")
	if err != nil {
		return fmt.Errorf("scan failed: %w", err)
	}

	for key, value := range data {
		var entry WALDemotionEntry
		if err := json.Unmarshal(value, &entry); err != nil {
			logging.WithFields(map[string]any{
				"key":   key,
				"error": err.Error(),
			}).Warn("反序列化降级日志条目失败")
			continue
		}

		l.entries[entry.ID] = &entry
		if !entry.Synced {
			l.unsyncedCount++
		}

		// 更新序列号
		seq := extractSeqFromID(entry.ID)
		if seq > l.idSeq {
			l.idSeq = seq
		}
	}

	logging.WithFields(map[string]any{
		"total":    len(l.entries),
		"unsynced": l.unsyncedCount,
	}).Info("降级日志恢复完成")

	return nil
}

// Append 添加降级日志条目（持久化）
func (l *WALDemotionLog) Append(namespace, key string, value []byte) (*WALDemotionEntry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.idSeq++
	now := time.Now()
	hlc := clock.NewHLC().Now()

	entry := &WALDemotionEntry{
		ID:        generateWALEntryID(l.idSeq, now),
		Namespace: namespace,
		Key:       key,
		Value:     value,
		Timestamp: now.Format(time.RFC3339Nano),
		HLC:       hlc,
		Synced:    false,
	}

	// 序列化
	data, err := json.Marshal(entry)
	if err != nil {
		return nil, fmt.Errorf("marshal failed: %w", err)
	}

	// 持久化
	ctx := context.Background()
	storeKey := "demotion:" + entry.ID
	if err := l.store.Put(ctx, storeKey, data); err != nil {
		return nil, fmt.Errorf("persist failed: %w", err)
	}

	// 更新内存缓存
	l.entries[entry.ID] = entry
	l.unsyncedCount++

	logging.WithFields(map[string]any{
		"id":        entry.ID,
		"namespace": namespace,
		"key":       key,
		"unsynced":  l.unsyncedCount,
	}).Debug("降级日志条目已持久化")

	return entry, nil
}

// GetUnsynced 获取未同步的条目
func (l *WALDemotionLog) GetUnsynced() []*WALDemotionEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	var unsynced []*WALDemotionEntry
	for _, entry := range l.entries {
		if !entry.Synced {
			unsynced = append(unsynced, entry)
		}
	}
	return unsynced
}

// MarkSynced 标记条目为已同步（持久化）
func (l *WALDemotionLog) MarkSynced(id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry, exists := l.entries[id]
	if !exists {
		return fmt.Errorf("entry not found: %s", id)
	}

	if entry.Synced {
		return nil // 已经是同步状态
	}

	// 更新条目
	entry.Synced = true
	entry.SyncedAt = time.Now().Format(time.RFC3339Nano)

	// 序列化
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal failed: %w", err)
	}

	// 持久化
	ctx := context.Background()
	storeKey := "demotion:" + entry.ID
	if err := l.store.Put(ctx, storeKey, data); err != nil {
		return fmt.Errorf("persist failed: %w", err)
	}

	l.unsyncedCount--

	logging.WithFields(map[string]any{
		"id":       id,
		"unsynced": l.unsyncedCount,
	}).Debug("降级日志条目已标记为同步")

	return nil
}

// MarkSyncedBatch 批量标记为已同步（P1-4: 优化为单次加锁批量处理）
func (l *WALDemotionLog) MarkSyncedBatch(ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	ctx := context.Background()
	var errors []string

	for _, id := range ids {
		entry, exists := l.entries[id]
		if !exists {
			continue
		}

		if entry.Synced {
			continue // 已经是同步状态
		}

		// 更新内存状态
		entry.Synced = true
		entry.SyncedAt = time.Now().Format(time.RFC3339Nano)
		l.unsyncedCount--

		// 序列化并更新持久化存储
		data, err := json.Marshal(entry)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: marshal failed: %s", id, err.Error()))
			continue
		}

		storeKey := "demotion:" + id
		if err := l.store.Put(ctx, storeKey, data); err != nil {
			errors = append(errors, fmt.Sprintf("%s: %s", id, err.Error()))
			logging.WithFields(map[string]any{
				"id":    id,
				"error": err.Error(),
			}).Warn("批量标记同步失败")
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("部分条目同步失败: %s", strings.Join(errors, ", "))
	}

	logging.WithFields(map[string]any{
		"batch_size": len(ids),
		"unsynced":   l.unsyncedCount,
	}).Debug("降级日志批量标记已同步")

	return nil
}

// ClearSynced 清除已同步的条目（从存储中删除）
func (l *WALDemotionLog) ClearSynced() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	ctx := context.Background()
	var deleted int

	for id, entry := range l.entries {
		if entry.Synced {
			storeKey := "demotion:" + id
			if err := l.store.Delete(ctx, storeKey); err != nil {
				logging.WithFields(map[string]any{
					"id":    id,
					"error": err.Error(),
				}).Warn("删除已同步降级日志条目失败")
				continue
			}
			delete(l.entries, id)
			deleted++
		}
	}

	if deleted > 0 {
		logging.WithFields(map[string]any{
			"deleted": deleted,
			"remain":  len(l.entries),
		}).Info("已清理同步完成的降级日志条目")
	}

	return nil
}

// Compaction 执行 Compaction（清理已同步的旧日志）
func (l *WALDemotionLog) Compaction() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// 检查是否需要 Compaction
	if time.Since(l.lastCompaction) < l.compactionInterval {
		return nil
	}

	// 生成新的检查点 ID
	newCheckpointID := generateCheckpointID()
	l.checkpointID = newCheckpointID

	// 清理已同步的条目
	ctx := context.Background()
	var deleted int

	for id, entry := range l.entries {
		if entry.Synced {
			storeKey := "demotion:" + id
			if err := l.store.Delete(ctx, storeKey); err != nil {
				continue
			}
			delete(l.entries, id)
			deleted++
		}
	}

	l.lastCompaction = time.Now()

	logging.WithFields(map[string]any{
		"checkpoint":  newCheckpointID,
		"deleted":     deleted,
		"remain":      len(l.entries),
		"unsynced":    l.unsyncedCount,
		"lastCompact": l.lastCompaction.Format(time.RFC3339),
	}).Info("降级日志 Compaction 完成")

	return nil
}

// Count 获取日志条目数量
func (l *WALDemotionLog) Count() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.entries)
}

// UnsyncedCount 获取未同步条目数量
func (l *WALDemotionLog) UnsyncedCount() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.unsyncedCount
}

// GetEntry 获取指定条目
func (l *WALDemotionLog) GetEntry(id string) (*WALDemotionEntry, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	entry, exists := l.entries[id]
	return entry, exists
}

// GetAllEntries 获取所有条目
func (l *WALDemotionLog) GetAllEntries() []*WALDemotionEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	entries := make([]*WALDemotionEntry, 0, len(l.entries))
	for _, entry := range l.entries {
		entries = append(entries, entry)
	}
	return entries
}

// Reset 重置所有日志（用于测试）
func (l *WALDemotionLog) Reset() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	ctx := context.Background()
	for id := range l.entries {
		storeKey := "demotion:" + id
		_ = l.store.Delete(ctx, storeKey)
	}

	l.entries = make(map[string]*WALDemotionEntry)
	l.idSeq = 0
	l.unsyncedCount = 0

	return nil
}

// ==================== 辅助函数 ====================

// generateWALEntryID 生成 WAL 条目 ID
func generateWALEntryID(seq int, t time.Time) string {
	return fmt.Sprintf("dem-%s-%04d", t.Format("20060102-150405"), seq)
}

// generateCheckpointID 生成检查点 ID
func generateCheckpointID() string {
	return fmt.Sprintf("cp-%s", time.Now().Format("20060102-150405"))
}

// extractSeqFromID 从 ID 中提取序列号
func extractSeqFromID(id string) int {
	// ID 格式: dem-20060102-150405-0001
	lastDash := strings.LastIndex(id, "-")
	if lastDash == -1 || lastDash+1 >= len(id) {
		return 0
	}

	var seq int
	_, _ = fmt.Sscanf(id[lastDash+1:], "%d", &seq)
	return seq
}

// ==================== 内存存储实现（用于测试）====================

// MemoryWALStore 内存 WAL 存储（用于测试）
type MemoryWALStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

// NewMemoryWALStore 创建内存 WAL 存储
func NewMemoryWALStore() *MemoryWALStore {
	return &MemoryWALStore{
		data: make(map[string][]byte),
	}
}

// Put 写入
func (s *MemoryWALStore) Put(ctx context.Context, key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = value
	return nil
}

// Get 读取
func (s *MemoryWALStore) Get(ctx context.Context, key string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data[key], nil
}

// Delete 删除
func (s *MemoryWALStore) Delete(ctx context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
	return nil
}

// Scan 前缀扫描
func (s *MemoryWALStore) Scan(ctx context.Context, prefix string) (map[string][]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string][]byte)
	for key, value := range s.data {
		if strings.HasPrefix(key, prefix) {
			result[key] = value
		}
	}
	return result, nil
}

// Size 获取存储大小
func (s *MemoryWALStore) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.data)
}
