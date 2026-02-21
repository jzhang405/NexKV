// Package degradation 提供分区恢复管理器实现
//
// DEGR-5: 分区恢复与同步
//
// 核心功能：
//   - 监控分区状态变化
//   - 分区恢复后自动同步降级日志
//   - 使用 HLC 时间戳 + LWW 语义处理冲突
//   - 支持批量同步和增量同步
//
// 恢复策略：
//  1. 检测到分区恢复（Quorum 可达）
//  2. 获取所有未同步的降级日志条目
//  3. 按 HLC 时间戳排序
//  4. 批量同步到 Quorum
//  5. 标记已同步的条目
//  6. 执行 Compaction 清理
package degradation

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/partition"
	"github.com/jzhang405/NexKV/internal/metadata/quorum"
)

// ==================== 恢复管理器 ====================

// RecoveryManager 恢复管理器
type RecoveryManager struct {
	mu sync.RWMutex

	// 分区检测器
	detector *partition.Detector

	// 降级管理器
	manager *Manager

	// Quorum 写入器
	quorumWriter QuorumWriter

	// 配置
	config *RecoveryConfig

	// 状态
	lastRecoveryTime time.Time
	recoveryCount    int
	totalSynced      int
	totalFailed      int
}

// RecoveryConfig 恢复配置
type RecoveryConfig struct {
	// BatchSize 批量同步大小（默认 10）
	BatchSize int

	// RetryCount 失败重试次数（默认 3）
	RetryCount int

	// RetryDelay 重试延迟（默认 100ms）
	RetryDelay time.Duration

	// AutoCompaction 是否自动执行 Compaction（默认 true）
	AutoCompaction bool
}

// DefaultRecoveryConfig 默认恢复配置
func DefaultRecoveryConfig() *RecoveryConfig {
	return &RecoveryConfig{
		BatchSize:      10,
		RetryCount:     3,
		RetryDelay:     100 * time.Millisecond,
		AutoCompaction: true,
	}
}

// NewRecoveryManager 创建恢复管理器
func NewRecoveryManager(
	detector *partition.Detector,
	manager *Manager,
	quorumWriter QuorumWriter,
	config *RecoveryConfig,
) *RecoveryManager {
	if config == nil {
		config = DefaultRecoveryConfig()
	}

	return &RecoveryManager{
		detector:     detector,
		manager:      manager,
		quorumWriter: quorumWriter,
		config:       config,
	}
}

// RecoveryResult 恢复结果
type RecoveryResult struct {
	// Success 是否成功
	Success bool

	// TotalEntries 总条目数
	TotalEntries int

	// SyncedCount 同步成功数
	SyncedCount int

	// FailedCount 同步失败数
	FailedCount int

	// Duration 恢复耗时
	Duration time.Duration

	// FailedEntries 失败的条目 ID
	FailedEntries []string
}

// CheckAndRecover 检查并恢复
//
// 检查分区状态，如果恢复则同步降级日志。
func (r *RecoveryManager) CheckAndRecover(ctx context.Context) (*RecoveryResult, error) {
	status := r.detector.CheckPartition()

	if !status.CanReachQuorum {
		// 仍在分区中
		return &RecoveryResult{
			Success:      false,
			TotalEntries: 0,
			SyncedCount:  0,
			FailedCount:  0,
		}, nil
	}

	// 分区已恢复，执行同步
	return r.SyncDemotionLog(ctx)
}

// SyncDemotionLog 同步降级日志
//
// 将所有未同步的降级日志条目同步到 Quorum。
func (r *RecoveryManager) SyncDemotionLog(ctx context.Context) (*RecoveryResult, error) {
	start := time.Now()

	unsynced := r.manager.GetDemotionLog().GetUnsynced()
	if len(unsynced) == 0 {
		// 没有待同步的日志
		return &RecoveryResult{
			Success:       true,
			TotalEntries:  0,
			SyncedCount:   0,
			FailedCount:   0,
			Duration:      time.Since(start),
			FailedEntries: []string{},
		}, nil
	}

	// 按时间戳排序（确保顺序同步）
	sortedEntries := r.sortByTimestamp(unsynced)

	logging.WithFields(map[string]any{
		"count":  len(sortedEntries),
		"method": "SyncDemotionLog",
	}).Info("开始同步降级日志")

	result := &RecoveryResult{
		TotalEntries:  len(sortedEntries),
		FailedEntries: []string{},
	}

	// 批量同步
	batchSize := r.config.BatchSize
	for i := 0; i < len(sortedEntries); i += batchSize {
		end := i + batchSize
		if end > len(sortedEntries) {
			end = len(sortedEntries)
		}

		batch := sortedEntries[i:end]
		synced, failed := r.syncBatch(ctx, batch)

		result.SyncedCount += synced
		result.FailedCount += failed
	}

	result.Duration = time.Since(start)
	result.Success = result.FailedCount == 0

	// 更新统计
	r.mu.Lock()
	r.lastRecoveryTime = time.Now()
	r.recoveryCount++
	r.totalSynced += result.SyncedCount
	r.totalFailed += result.FailedCount
	r.mu.Unlock()

	logging.WithFields(map[string]any{
		"total":    result.TotalEntries,
		"synced":   result.SyncedCount,
		"failed":   result.FailedCount,
		"success":  result.Success,
		"duration": result.Duration.String(),
	}).Info("降级日志同步完成")

	// 自动 Compaction
	if r.config.AutoCompaction && result.Success {
		r.manager.GetDemotionLog().ClearSynced()
	}

	return result, nil
}

// syncBatch 同步一批条目
func (r *RecoveryManager) syncBatch(ctx context.Context, entries []*DemotionEntry) (synced, failed int) {
	for _, entry := range entries {
		if r.syncEntry(ctx, entry) {
			synced++
		} else {
			failed++
		}
	}
	return
}

// syncEntry 同步单个条目（带重试）
func (r *RecoveryManager) syncEntry(ctx context.Context, entry *DemotionEntry) bool {
	for i := 0; i < r.config.RetryCount; i++ {
		err := r.quorumWriter.PutWithQuorum(ctx, entry.Namespace, entry.Key, entry.Value, nil)
		if err == nil {
			// 同步成功，标记为已同步
			r.manager.GetDemotionLog().MarkSynced(entry.ID)
			return true
		}

		logging.WithFields(map[string]any{
			"id":        entry.ID,
			"namespace": entry.Namespace,
			"key":       entry.Key,
			"attempt":   i + 1,
			"max":       r.config.RetryCount,
			"error":     err.Error(),
		}).Warn("同步降级日志失败，准备重试")

		// 等待重试
		if i < r.config.RetryCount-1 {
			time.Sleep(r.config.RetryDelay)
		}
	}

	return false
}

// sortByTimestamp 按时间戳排序
func (r *RecoveryManager) sortByTimestamp(entries []*DemotionEntry) []*DemotionEntry {
	// 复制以避免修改原始切片
	sorted := make([]*DemotionEntry, len(entries))
	copy(sorted, entries)

	// 按时间戳排序
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Timestamp.Before(sorted[j].Timestamp)
	})

	return sorted
}

// GetStats 获取恢复统计
func (r *RecoveryManager) GetStats() map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return map[string]any{
		"last_recovery_time": r.lastRecoveryTime.Format(time.RFC3339),
		"recovery_count":     r.recoveryCount,
		"total_synced":       r.totalSynced,
		"total_failed":       r.totalFailed,
		"pending_entries":    r.manager.GetDemotionLog().UnsyncedCount(),
	}
}

// ==================== 后台恢复任务 ====================

// BackgroundRecovery 后台恢复任务
type BackgroundRecovery struct {
	manager  *RecoveryManager
	interval time.Duration
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	running  bool
	mu       sync.Mutex
}

// NewBackgroundRecovery 创建后台恢复任务
func NewBackgroundRecovery(manager *RecoveryManager, interval time.Duration) *BackgroundRecovery {
	if interval <= 0 {
		interval = 5 * time.Second
	}

	return &BackgroundRecovery{
		manager:  manager,
		interval: interval,
	}
}

// Start 启动后台恢复
func (b *BackgroundRecovery) Start(ctx context.Context) {
	b.mu.Lock()
	if b.running {
		b.mu.Unlock()
		return
	}
	b.running = true
	b.mu.Unlock()

	b.ctx, b.cancel = context.WithCancel(ctx)
	b.wg.Add(1)

	go b.run()
}

// Stop 停止后台恢复
func (b *BackgroundRecovery) Stop() {
	b.mu.Lock()
	if !b.running {
		b.mu.Unlock()
		return
	}
	b.mu.Unlock()

	if b.cancel != nil {
		b.cancel()
	}
	b.wg.Wait()

	b.mu.Lock()
	b.running = false
	b.mu.Unlock()
}

// run 运行恢复循环
func (b *BackgroundRecovery) run() {
	defer b.wg.Done()

	ticker := time.NewTicker(b.interval)
	defer ticker.Stop()

	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			_, _ = b.manager.CheckAndRecover(b.ctx)
		}
	}
}

// ==================== 确保接口实现 ====================

var (
	_ QuorumWriter = (*quorum.QuorumCoordinator)(nil)
)
