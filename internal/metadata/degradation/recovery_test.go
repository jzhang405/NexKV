// Package degradation 提供分区恢复管理器实现
package degradation

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/partition"
	"github.com/jzhang405/NexKV/internal/metadata/quorum"
)

// ==================== RecoveryManager Tests ====================

func createRecoveryTestSetup(t *testing.T, quorumSize int) (*partition.Detector, *Manager, *mockQuorumWriter) {
	detector := createTestDetector(quorumSize)
	quorumWriter := newMockQuorumWriter()
	localWriter := newMockLocalWriter()

	manager := NewManager(&ManagerConfig{
		Detector:     detector,
		QuorumWriter: quorumWriter,
		LocalWriter:  localWriter,
		AutoRecover:  false,
	})

	return detector, manager, quorumWriter
}

func TestNewRecoveryManager_DefaultConfig(t *testing.T) {
	detector, manager, _ := createRecoveryTestSetup(t, 2)

	recovery := NewRecoveryManager(detector, manager, nil, nil)
	if recovery == nil {
		t.Fatal("expected recovery manager to be created")
	}

	// 验证默认配置
	if recovery.config.BatchSize != 10 {
		t.Errorf("expected default batch size 10, got %d", recovery.config.BatchSize)
	}
	if recovery.config.RetryCount != 3 {
		t.Errorf("expected default retry count 3, got %d", recovery.config.RetryCount)
	}
}

func TestNewRecoveryManager_CustomConfig(t *testing.T) {
	detector, manager, _ := createRecoveryTestSetup(t, 2)

	config := &RecoveryConfig{
		BatchSize:      5,
		RetryCount:     5,
		RetryDelay:     200 * time.Millisecond,
		AutoCompaction: false,
	}

	recovery := NewRecoveryManager(detector, manager, nil, config)
	if recovery.config.BatchSize != 5 {
		t.Errorf("expected batch size 5, got %d", recovery.config.BatchSize)
	}
	if recovery.config.RetryCount != 5 {
		t.Errorf("expected retry count 5, got %d", recovery.config.RetryCount)
	}
}

func TestRecoveryManager_CheckAndRecover_StillPartitioned(t *testing.T) {
	detector, manager, quorumWriter := createRecoveryTestSetup(t, 3)

	recovery := NewRecoveryManager(detector, manager, quorumWriter, nil)

	// 模拟分区
	detector.SimulateFailure("node-2")
	detector.SimulateFailure("node-3")

	// 降级写入
	ctx := context.Background()
	_ = manager.Write(ctx, "ns1", "key1", []byte("value1"))

	// 尝试恢复（仍在分区中）
	result, err := recovery.CheckAndRecover(ctx)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if result.Success {
		t.Error("expected success to be false when still partitioned")
	}
	if result.TotalEntries != 0 {
		t.Errorf("expected 0 entries, got %d", result.TotalEntries)
	}
}

func TestRecoveryManager_CheckAndRecover_Success(t *testing.T) {
	detector, manager, quorumWriter := createRecoveryTestSetup(t, 3)

	recovery := NewRecoveryManager(detector, manager, quorumWriter, nil)

	ctx := context.Background()

	// 模拟分区
	detector.SimulateFailure("node-2")
	detector.SimulateFailure("node-3")

	// 降级写入
	_ = manager.Write(ctx, "ns1", "key1", []byte("value1"))
	_ = manager.Write(ctx, "ns1", "key2", []byte("value2"))

	// 验证降级日志有条目
	if manager.GetDemotionLog().UnsyncedCount() != 2 {
		t.Errorf("expected 2 unsynced, got %d", manager.GetDemotionLog().UnsyncedCount())
	}

	// 恢复分区
	detector.SimulateRecovery("node-2")
	detector.SimulateRecovery("node-3")

	// 执行恢复
	result, err := recovery.CheckAndRecover(ctx)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if !result.Success {
		t.Error("expected success to be true")
	}
	if result.TotalEntries != 2 {
		t.Errorf("expected 2 total entries, got %d", result.TotalEntries)
	}
	if result.SyncedCount != 2 {
		t.Errorf("expected 2 synced, got %d", result.SyncedCount)
	}
	if result.FailedCount != 0 {
		t.Errorf("expected 0 failed, got %d", result.FailedCount)
	}

	// 验证降级日志已清空
	if manager.GetDemotionLog().UnsyncedCount() != 0 {
		t.Errorf("expected 0 unsynced after recovery, got %d", manager.GetDemotionLog().UnsyncedCount())
	}

	// 验证 Quorum 写入
	quorumWrites := quorumWriter.getWrites()
	if len(quorumWrites) != 2 {
		t.Errorf("expected 2 quorum writes, got %d", len(quorumWrites))
	}
}

func TestRecoveryManager_SyncDemotionLog_Empty(t *testing.T) {
	detector, manager, quorumWriter := createRecoveryTestSetup(t, 2)

	recovery := NewRecoveryManager(detector, manager, quorumWriter, nil)

	ctx := context.Background()
	result, err := recovery.SyncDemotionLog(ctx)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if !result.Success {
		t.Error("expected success to be true")
	}
	if result.TotalEntries != 0 {
		t.Errorf("expected 0 entries, got %d", result.TotalEntries)
	}
}

func TestRecoveryManager_SyncDemotionLog_WithFailures(t *testing.T) {
	detector, manager, quorumWriter := createRecoveryTestSetup(t, 3)
	quorumWriter.fail = true // 模拟写入失败

	config := &RecoveryConfig{
		BatchSize:      10,
		RetryCount:     1, // 只重试 1 次
		RetryDelay:     10 * time.Millisecond,
		AutoCompaction: false,
	}
	recovery := NewRecoveryManager(detector, manager, quorumWriter, config)

	ctx := context.Background()

	// 模拟分区并写入
	detector.SimulateFailure("node-2")
	detector.SimulateFailure("node-3")
	_ = manager.Write(ctx, "ns1", "key1", []byte("value1"))

	// 恢复分区
	detector.SimulateRecovery("node-2")
	detector.SimulateRecovery("node-3")

	// 执行恢复（应该失败）
	result, err := recovery.SyncDemotionLog(ctx)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if result.Success {
		t.Error("expected success to be false when writes fail")
	}
	if result.FailedCount != 1 {
		t.Errorf("expected 1 failed, got %d", result.FailedCount)
	}
}

func TestRecoveryManager_SyncDemotionLog_PartialFailure(t *testing.T) {
	detector, manager, quorumWriter := createRecoveryTestSetup(t, 3)

	// 创建一个会在第 N 次写入后成功的 mock
	failCount := &struct {
		mu    sync.Mutex
		count int
		fail  bool
	}{fail: true}

	quorumWriterWrapper := &mockQuorumWriterWithCondition{
		mockQuorumWriter: quorumWriter,
		shouldFail: func() bool {
			failCount.mu.Lock()
			defer failCount.mu.Unlock()
			failCount.count++
			// 第一个失败，第二个成功
			return failCount.count == 1
		},
	}

	config := &RecoveryConfig{
		BatchSize:      10,
		RetryCount:     1,
		RetryDelay:     10 * time.Millisecond,
		AutoCompaction: false,
	}
	recovery := NewRecoveryManager(detector, manager, quorumWriterWrapper, config)

	ctx := context.Background()

	// 模拟分区并写入
	detector.SimulateFailure("node-2")
	detector.SimulateFailure("node-3")
	_ = manager.Write(ctx, "ns1", "key1", []byte("value1"))
	_ = manager.Write(ctx, "ns1", "key2", []byte("value2"))

	// 恢复分区
	detector.SimulateRecovery("node-2")
	detector.SimulateRecovery("node-3")

	// 执行恢复
	result, err := recovery.SyncDemotionLog(ctx)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	// 第一个条目会失败（重试后也失败），第二个会成功
	if result.SyncedCount != 1 {
		t.Errorf("expected 1 synced, got %d", result.SyncedCount)
	}
}

func TestRecoveryManager_GetStats(t *testing.T) {
	detector, manager, quorumWriter := createRecoveryTestSetup(t, 3)

	recovery := NewRecoveryManager(detector, manager, quorumWriter, nil)

	ctx := context.Background()

	// 初始统计
	stats := recovery.GetStats()
	if stats["recovery_count"].(int) != 0 {
		t.Error("expected initial recovery count 0")
	}

	// 执行一次恢复
	detector.SimulateFailure("node-2")
	detector.SimulateFailure("node-3")
	_ = manager.Write(ctx, "ns1", "key1", []byte("value1"))
	detector.SimulateRecovery("node-2")
	detector.SimulateRecovery("node-3")
	_, _ = recovery.CheckAndRecover(ctx)

	// 验证统计更新
	stats = recovery.GetStats()
	if stats["recovery_count"].(int) != 1 {
		t.Errorf("expected recovery count 1, got %d", stats["recovery_count"])
	}
	if stats["total_synced"].(int) != 1 {
		t.Errorf("expected total synced 1, got %d", stats["total_synced"])
	}
}

// ==================== BackgroundRecovery Tests ====================

func TestBackgroundRecovery_StartStop(t *testing.T) {
	detector, manager, quorumWriter := createRecoveryTestSetup(t, 3)

	recovery := NewRecoveryManager(detector, manager, quorumWriter, nil)
	bg := NewBackgroundRecovery(recovery, 100*time.Millisecond)

	ctx := context.Background()
	bg.Start(ctx)

	// 等待一会儿
	time.Sleep(150 * time.Millisecond)

	// 停止
	bg.Stop()

	// 验证停止后不再运行
	bg.mu.Lock()
	running := bg.running
	bg.mu.Unlock()

	if running {
		t.Error("expected not running after stop")
	}
}

func TestBackgroundRecovery_AutoRecovery(t *testing.T) {
	detector, manager, quorumWriter := createRecoveryTestSetup(t, 3)

	recovery := NewRecoveryManager(detector, manager, quorumWriter, nil)
	bg := NewBackgroundRecovery(recovery, 50*time.Millisecond)

	ctx := context.Background()

	// 模拟分区并写入
	detector.SimulateFailure("node-2")
	detector.SimulateFailure("node-3")
	_ = manager.Write(ctx, "ns1", "key1", []byte("value1"))

	// 启动后台恢复
	bg.Start(ctx)

	// 等待一会儿（仍在分区中，不会恢复）
	time.Sleep(100 * time.Millisecond)

	// 验证仍未同步
	if manager.GetDemotionLog().UnsyncedCount() != 1 {
		t.Errorf("expected 1 unsynced during partition, got %d", manager.GetDemotionLog().UnsyncedCount())
	}

	// 恢复分区
	detector.SimulateRecovery("node-2")
	detector.SimulateRecovery("node-3")

	// 等待后台恢复触发
	time.Sleep(150 * time.Millisecond)

	// 验证已同步
	if manager.GetDemotionLog().UnsyncedCount() != 0 {
		t.Errorf("expected 0 unsynced after recovery, got %d", manager.GetDemotionLog().UnsyncedCount())
	}

	// 停止
	bg.Stop()
}

// ==================== Mock 实现 ====================

// mockQuorumWriterWithCondition 带条件的 mock Quorum 写入器
type mockQuorumWriterWithCondition struct {
	*mockQuorumWriter
	shouldFail func() bool
}

func (m *mockQuorumWriterWithCondition) PutWithQuorum(ctx context.Context, ns, key string, value any, opts *quorum.PutOptions) error {
	if m.shouldFail() {
		m.fail = true
		defer func() { m.fail = false }()
	}
	return m.mockQuorumWriter.PutWithQuorum(ctx, ns, key, value, opts)
}

// ==================== Benchmark ====================

func BenchmarkRecoveryManager_SyncDemotionLog(b *testing.B) {
	detector := createTestDetector(3)
	quorumWriter := newMockQuorumWriter()
	localWriter := newMockLocalWriter()

	manager := NewManager(&ManagerConfig{
		Detector:     detector,
		QuorumWriter: quorumWriter,
		LocalWriter:  localWriter,
		AutoRecover:  false,
	})

	recovery := NewRecoveryManager(detector, manager, quorumWriter, nil)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// 添加降级日志条目
		_ = manager.GetDemotionLog().Append("ns1", "key", []byte("value"))
		// 同步
		_, _ = recovery.SyncDemotionLog(ctx)
		// 重置
		manager.GetDemotionLog().ClearSynced()
	}
}

func BenchmarkRecoveryManager_CheckAndRecover(b *testing.B) {
	detector := createTestDetector(3)
	quorumWriter := newMockQuorumWriter()
	localWriter := newMockLocalWriter()

	manager := NewManager(&ManagerConfig{
		Detector:     detector,
		QuorumWriter: quorumWriter,
		LocalWriter:  localWriter,
		AutoRecover:  false,
	})

	recovery := NewRecoveryManager(detector, manager, quorumWriter, nil)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = recovery.CheckAndRecover(ctx)
	}
}
