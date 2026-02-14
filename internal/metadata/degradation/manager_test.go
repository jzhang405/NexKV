// Package degradation 提供分区降级管理器实现
package degradation

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/jzhang405/NexKV/internal/metadata/partition"
	"github.com/jzhang405/NexKV/internal/metadata/quorum"
)

// ==================== Mock 实现 ====================

// mockQuorumWriter 模拟 Quorum 写入器
type mockQuorumWriter struct {
	mu       sync.Mutex
	writes   map[string][]byte // 记录写入
	fail     bool              // 是否失败
	failures int               // 失败次数
}

func newMockQuorumWriter() *mockQuorumWriter {
	return &mockQuorumWriter{
		writes: make(map[string][]byte),
	}
}

func (m *mockQuorumWriter) PutWithQuorum(ctx context.Context, ns, key string, value any, opts *quorum.PutOptions) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.fail {
		m.failures++
		return errors.New("quorum write failed")
	}

	m.writes[ns+":"+key] = value.([]byte)
	return nil
}

func (m *mockQuorumWriter) getWrites() map[string][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make(map[string][]byte)
	for k, v := range m.writes {
		result[k] = v
	}
	return result
}

// mockLocalWriter 模拟本地写入器
type mockLocalWriter struct {
	mu     sync.Mutex
	writes map[string][]byte
	fail   bool
}

func newMockLocalWriter() *mockLocalWriter {
	return &mockLocalWriter{
		writes: make(map[string][]byte),
	}
}

func (m *mockLocalWriter) Put(ctx context.Context, ns, key string, value any) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.fail {
		return errors.New("local write failed")
	}

	m.writes[ns+":"+key] = value.([]byte)
	return nil
}

func (m *mockLocalWriter) getWrites() map[string][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make(map[string][]byte)
	for k, v := range m.writes {
		result[k] = v
	}
	return result
}

// mockNotifier 模拟通知器
type mockNotifier struct {
	mu      sync.Mutex
	writes  []string
	enabled bool
}

func newMockNotifier() *mockNotifier {
	return &mockNotifier{
		writes:  make([]string, 0),
		enabled: true,
	}
}

func (m *mockNotifier) OnWrite(namespace, key string) {
	if !m.enabled {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writes = append(m.writes, namespace+":"+key)
}

func (m *mockNotifier) getWrites() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.writes...)
}

// ==================== 辅助函数 ====================

func createTestDetector(quorumSize int) *partition.Detector {
	config := &partition.DetectorConfig{
		LocalNodeID:      "node-1",
		AllNodes:         []string{"node-1", "node-2", "node-3"},
		QuorumSize:       quorumSize,
		RequiredFailures: 1,
	}
	return partition.NewDetector(config)
}

// ==================== NewManager Tests ====================

func TestNewManager_DefaultConfig(t *testing.T) {
	detector := createTestDetector(2)
	quorumWriter := newMockQuorumWriter()
	localWriter := newMockLocalWriter()

	config := &ManagerConfig{
		Detector:     detector,
		QuorumWriter: quorumWriter,
		LocalWriter:  localWriter,
	}
	manager := NewManager(config)

	if manager == nil {
		t.Fatal("expected manager to be created")
	}
	if manager.IsDegraded() {
		t.Error("expected not degraded initially")
	}
	if manager.GetConsistencyLevel() != ConsistencyLevelStrong {
		t.Error("expected strong consistency initially")
	}
	if manager.GetDemotionLog() == nil {
		t.Error("expected demotion log to be created")
	}
}

func TestNewManager_WithCustomDemotionLog(t *testing.T) {
	detector := createTestDetector(2)
	quorumWriter := newMockQuorumWriter()
	localWriter := newMockLocalWriter()
	log := NewDemotionLog()

	config := &ManagerConfig{
		Detector:     detector,
		QuorumWriter: quorumWriter,
		LocalWriter:  localWriter,
		DemotionLog:  log,
	}
	manager := NewManager(config)

	if manager.GetDemotionLog() != log {
		t.Error("expected to use provided demotion log")
	}
}

func TestNewManager_WithNotifier(t *testing.T) {
	detector := createTestDetector(2)
	quorumWriter := newMockQuorumWriter()
	localWriter := newMockLocalWriter()
	notifier := newMockNotifier()

	config := &ManagerConfig{
		Detector:     detector,
		QuorumWriter: quorumWriter,
		LocalWriter:  localWriter,
		Notifier:     notifier,
	}
	manager := NewManager(config)

	if manager.notifier != notifier {
		t.Error("expected to use provided notifier")
	}
}

func TestNewManager_NilDetector(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic with nil detector")
		}
	}()

	NewManager(&ManagerConfig{
		QuorumWriter: newMockQuorumWriter(),
		LocalWriter:  newMockLocalWriter(),
	})
}

func TestNewManager_NilQuorumWriter(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic with nil quorumWriter")
		}
	}()

	NewManager(&ManagerConfig{
		Detector:    createTestDetector(2),
		LocalWriter: newMockLocalWriter(),
	})
}

func TestNewManager_NilLocalWriter(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic with nil localWriter")
		}
	}()

	NewManager(&ManagerConfig{
		Detector:     createTestDetector(2),
		QuorumWriter: newMockQuorumWriter(),
	})
}

// ==================== Write Tests ====================

func TestManager_Write_QuorumSuccess(t *testing.T) {
	detector := createTestDetector(2)
	quorumWriter := newMockQuorumWriter()
	localWriter := newMockLocalWriter()
	notifier := newMockNotifier()

	manager := NewManager(&ManagerConfig{
		Detector:     detector,
		QuorumWriter: quorumWriter,
		LocalWriter:  localWriter,
		Notifier:     notifier,
	})

	ctx := context.Background()
	err := manager.Write(ctx, "ns1", "key1", []byte("value1"))
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	// 验证写入到 Quorum
	writes := quorumWriter.getWrites()
	if len(writes) != 1 {
		t.Errorf("expected 1 quorum write, got %d", len(writes))
	}
	if string(writes["ns1:key1"]) != "value1" {
		t.Errorf("expected value1, got %s", writes["ns1:key1"])
	}

	// 验证通知被触发
	notifWrites := notifier.getWrites()
	if len(notifWrites) != 1 {
		t.Errorf("expected 1 notification, got %d", len(notifWrites))
	}

	// 验证本地没有写入
	localWrites := localWriter.getWrites()
	if len(localWrites) != 0 {
		t.Errorf("expected 0 local writes, got %d", len(localWrites))
	}
}

func TestManager_Write_DegradedMode(t *testing.T) {
	detector := createTestDetector(3) // Quorum=3，无法满足
	quorumWriter := newMockQuorumWriter()
	localWriter := newMockLocalWriter()
	notifier := newMockNotifier()

	manager := NewManager(&ManagerConfig{
		Detector:     detector,
		QuorumWriter: quorumWriter,
		LocalWriter:  localWriter,
		Notifier:     notifier,
		AutoRecover:  false, // 禁用自动恢复
	})

	// 模拟分区（Quorum 不可达）
	detector.SimulateFailure("node-2")
	detector.SimulateFailure("node-3")

	ctx := context.Background()
	err := manager.Write(ctx, "ns1", "key1", []byte("value1"))
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	// 验证进入降级状态
	if !manager.IsDegraded() {
		t.Error("expected to be degraded")
	}
	if manager.GetConsistencyLevel() != ConsistencyLevelEventual {
		t.Error("expected eventual consistency")
	}

	// 验证写入到本地
	localWrites := localWriter.getWrites()
	if len(localWrites) != 1 {
		t.Errorf("expected 1 local write, got %d", len(localWrites))
	}

	// 验证降级日志
	if manager.GetDemotionLog().Count() != 1 {
		t.Errorf("expected 1 demotion log entry, got %d", manager.GetDemotionLog().Count())
	}

	// 验证通知被触发
	notifWrites := notifier.getWrites()
	if len(notifWrites) != 1 {
		t.Errorf("expected 1 notification, got %d", len(notifWrites))
	}
}

func TestManager_Write_QuorumFailure(t *testing.T) {
	detector := createTestDetector(2)
	quorumWriter := newMockQuorumWriter()
	quorumWriter.fail = true
	localWriter := newMockLocalWriter()

	manager := NewManager(&ManagerConfig{
		Detector:     detector,
		QuorumWriter: quorumWriter,
		LocalWriter:  localWriter,
	})

	ctx := context.Background()
	err := manager.Write(ctx, "ns1", "key1", []byte("value1"))
	if err == nil {
		t.Error("expected error on quorum failure")
	}
}

// ==================== Recover Tests ====================

func TestManager_Recover_AfterPartition(t *testing.T) {
	detector := createTestDetector(3) // Quorum=3
	quorumWriter := newMockQuorumWriter()
	localWriter := newMockLocalWriter()

	manager := NewManager(&ManagerConfig{
		Detector:     detector,
		QuorumWriter: quorumWriter,
		LocalWriter:  localWriter,
		AutoRecover:  false,
	})

	// 模拟分区
	detector.SimulateFailure("node-2")
	detector.SimulateFailure("node-3")

	ctx := context.Background()

	// 降级写入
	err := manager.Write(ctx, "ns1", "key1", []byte("value1"))
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	// 验证降级状态
	if !manager.IsDegraded() {
		t.Error("expected to be degraded")
	}

	// 恢复分区
	detector.SimulateRecovery("node-2")
	detector.SimulateRecovery("node-3")

	// 手动恢复
	err = manager.Recover(ctx)
	if err != nil {
		t.Errorf("expected no error on recover, got %v", err)
	}

	// 验证恢复状态
	if manager.IsDegraded() {
		t.Error("expected not to be degraded after recovery")
	}

	// 验证日志已同步
	if manager.GetDemotionLog().UnsyncedCount() != 0 {
		t.Errorf("expected 0 unsynced entries, got %d", manager.GetDemotionLog().UnsyncedCount())
	}

	// 验证 Quorum 写入
	quorumWrites := quorumWriter.getWrites()
	if len(quorumWrites) != 1 {
		t.Errorf("expected 1 quorum write, got %d", len(quorumWrites))
	}
}

func TestManager_Recover_StillPartitioned(t *testing.T) {
	detector := createTestDetector(3)
	quorumWriter := newMockQuorumWriter()
	localWriter := newMockLocalWriter()

	manager := NewManager(&ManagerConfig{
		Detector:     detector,
		QuorumWriter: quorumWriter,
		LocalWriter:  localWriter,
		AutoRecover:  false,
	})

	// 模拟分区
	detector.SimulateFailure("node-2")
	detector.SimulateFailure("node-3")

	ctx := context.Background()
	_ = manager.Write(ctx, "ns1", "key1", []byte("value1"))

	// 尝试恢复（仍在分区中）
	err := manager.Recover(ctx)
	if err != nil {
		t.Errorf("expected no error when still partitioned, got %v", err)
	}

	// 仍应是降级状态
	if !manager.IsDegraded() {
		t.Error("expected to still be degraded")
	}
}

// ==================== AutoRecover Tests ====================

func TestManager_AutoRecover_OnQuorumWrite(t *testing.T) {
	detector := createTestDetector(3)
	quorumWriter := newMockQuorumWriter()
	localWriter := newMockLocalWriter()

	manager := NewManager(&ManagerConfig{
		Detector:     detector,
		QuorumWriter: quorumWriter,
		LocalWriter:  localWriter,
		AutoRecover:  true, // 启用自动恢复
	})

	ctx := context.Background()

	// 模拟分区并写入
	detector.SimulateFailure("node-2")
	detector.SimulateFailure("node-3")
	_ = manager.Write(ctx, "ns1", "key1", []byte("value1"))

	if !manager.IsDegraded() {
		t.Error("expected to be degraded")
	}

	// 恢复分区
	detector.SimulateRecovery("node-2")
	detector.SimulateRecovery("node-3")

	// 下一次写入应该触发自动恢复
	_ = manager.Write(ctx, "ns1", "key2", []byte("value2"))

	// 验证不再降级
	if manager.IsDegraded() {
		t.Error("expected not to be degraded after auto recovery")
	}

	// 验证两个值都写入了 Quorum
	quorumWrites := quorumWriter.getWrites()
	if len(quorumWrites) != 2 {
		t.Errorf("expected 2 quorum writes, got %d", len(quorumWrites))
	}
}

// ==================== Status Tests ====================

func TestManager_GetStatus(t *testing.T) {
	detector := createTestDetector(2)
	quorumWriter := newMockQuorumWriter()
	localWriter := newMockLocalWriter()

	manager := NewManager(&ManagerConfig{
		Detector:     detector,
		QuorumWriter: quorumWriter,
		LocalWriter:  localWriter,
	})

	// 正常状态
	status := manager.GetStatus()
	if status.IsDegraded {
		t.Error("expected not degraded")
	}
	if status.ConsistencyLevel != ConsistencyLevelStrong {
		t.Error("expected strong consistency")
	}

	// 模拟分区
	detector.SimulateFailure("node-2")
	detector.SimulateFailure("node-3")

	ctx := context.Background()
	_ = manager.Write(ctx, "ns1", "key1", []byte("value1"))

	// 降级状态
	status = manager.GetStatus()
	if !status.IsDegraded {
		t.Error("expected degraded")
	}
	if status.ConsistencyLevel != ConsistencyLevelEventual {
		t.Error("expected eventual consistency")
	}
	if status.PendingLogEntries != 1 {
		t.Errorf("expected 1 pending log entry, got %d", status.PendingLogEntries)
	}
}

// ==================== ResetDegraded Tests ====================

func TestManager_ResetDegraded(t *testing.T) {
	detector := createTestDetector(3)
	quorumWriter := newMockQuorumWriter()
	localWriter := newMockLocalWriter()

	manager := NewManager(&ManagerConfig{
		Detector:     detector,
		QuorumWriter: quorumWriter,
		LocalWriter:  localWriter,
		AutoRecover:  false,
	})

	// 模拟分区并写入
	detector.SimulateFailure("node-2")
	detector.SimulateFailure("node-3")
	ctx := context.Background()
	_ = manager.Write(ctx, "ns1", "key1", []byte("value1"))

	if !manager.IsDegraded() {
		t.Error("expected to be degraded")
	}

	// 重置
	manager.ResetDegraded()

	if manager.IsDegraded() {
		t.Error("expected not to be degraded after reset")
	}
}

// ==================== DemotionLog Tests ====================

func TestDemotionLog_AppendAndGet(t *testing.T) {
	log := NewDemotionLog()

	entry := log.Append("ns1", "key1", []byte("value1"))
	if entry == nil {
		t.Fatal("expected entry to be created")
	}
	if entry.Namespace != "ns1" {
		t.Errorf("expected namespace ns1, got %s", entry.Namespace)
	}
	if entry.Key != "key1" {
		t.Errorf("expected key key1, got %s", entry.Key)
	}
	if string(entry.Value) != "value1" {
		t.Errorf("expected value value1, got %s", entry.Value)
	}
	if entry.Synced {
		t.Error("expected Synced to be false")
	}
	if entry.ID == "" {
		t.Error("expected ID to be set")
	}
}

func TestDemotionLog_GetUnsynced(t *testing.T) {
	log := NewDemotionLog()

	log.Append("ns1", "key1", []byte("value1"))
	log.Append("ns1", "key2", []byte("value2"))
	log.Append("ns1", "key3", []byte("value3"))

	unsynced := log.GetUnsynced()
	if len(unsynced) != 3 {
		t.Errorf("expected 3 unsynced entries, got %d", len(unsynced))
	}

	// 标记一个为已同步
	log.MarkSynced(unsynced[0].ID)

	unsynced = log.GetUnsynced()
	if len(unsynced) != 2 {
		t.Errorf("expected 2 unsynced entries, got %d", len(unsynced))
	}
}

func TestDemotionLog_ClearSynced(t *testing.T) {
	log := NewDemotionLog()

	entry1 := log.Append("ns1", "key1", []byte("value1"))
	log.Append("ns1", "key2", []byte("value2"))

	log.MarkSynced(entry1.ID)
	log.ClearSynced()

	if log.Count() != 1 {
		t.Errorf("expected 1 entry after clear, got %d", log.Count())
	}
}

func TestDemotionLog_Count(t *testing.T) {
	log := NewDemotionLog()

	if log.Count() != 0 {
		t.Errorf("expected 0 entries, got %d", log.Count())
	}

	log.Append("ns1", "key1", []byte("value1"))
	if log.Count() != 1 {
		t.Errorf("expected 1 entry, got %d", log.Count())
	}

	log.Append("ns1", "key2", []byte("value2"))
	if log.Count() != 2 {
		t.Errorf("expected 2 entries, got %d", log.Count())
	}
}

func TestDemotionLog_UnsyncedCount(t *testing.T) {
	log := NewDemotionLog()

	entry1 := log.Append("ns1", "key1", []byte("value1"))
	log.Append("ns1", "key2", []byte("value2"))

	if log.UnsyncedCount() != 2 {
		t.Errorf("expected 2 unsynced, got %d", log.UnsyncedCount())
	}

	log.MarkSynced(entry1.ID)
	if log.UnsyncedCount() != 1 {
		t.Errorf("expected 1 unsynced, got %d", log.UnsyncedCount())
	}
}

// ==================== ConsistencyLevel Tests ====================

func TestConsistencyLevel_String(t *testing.T) {
	if ConsistencyLevelStrong.String() != "strong" {
		t.Errorf("expected 'strong', got '%s'", ConsistencyLevelStrong.String())
	}
	if ConsistencyLevelEventual.String() != "eventual" {
		t.Errorf("expected 'eventual', got '%s'", ConsistencyLevelEventual.String())
	}
	if ConsistencyLevel(99).String() != "unknown" {
		t.Errorf("expected 'unknown', got '%s'", ConsistencyLevel(99).String())
	}
}

// ==================== Concurrent Tests ====================

func TestManager_ConcurrentWrites(t *testing.T) {
	detector := createTestDetector(2)
	quorumWriter := newMockQuorumWriter()
	localWriter := newMockLocalWriter()

	manager := NewManager(&ManagerConfig{
		Detector:     detector,
		QuorumWriter: quorumWriter,
		LocalWriter:  localWriter,
	})

	ctx := context.Background()
	done := make(chan bool)

	// 并发写入
	for i := 0; i < 10; i++ {
		go func(i int) {
			key := string(rune('a' + i))
			_ = manager.Write(ctx, "ns1", key, []byte("value"))
			done <- true
		}(i)
	}

	// 等待所有写入完成
	for i := 0; i < 10; i++ {
		<-done
	}

	// 验证所有写入都成功
	writes := quorumWriter.getWrites()
	if len(writes) != 10 {
		t.Errorf("expected 10 writes, got %d", len(writes))
	}
}

// ==================== Benchmark ====================

func BenchmarkManager_Write_Quorum(b *testing.B) {
	detector := createTestDetector(2)
	quorumWriter := newMockQuorumWriter()
	localWriter := newMockLocalWriter()

	manager := NewManager(&ManagerConfig{
		Detector:     detector,
		QuorumWriter: quorumWriter,
		LocalWriter:  localWriter,
	})

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = manager.Write(ctx, "ns1", "key", []byte("value"))
	}
}

func BenchmarkManager_Write_Degraded(b *testing.B) {
	detector := createTestDetector(3)
	quorumWriter := newMockQuorumWriter()
	localWriter := newMockLocalWriter()

	manager := NewManager(&ManagerConfig{
		Detector:     detector,
		QuorumWriter: quorumWriter,
		LocalWriter:  localWriter,
		AutoRecover:  false,
	})

	detector.SimulateFailure("node-2")
	detector.SimulateFailure("node-3")

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = manager.Write(ctx, "ns1", "key", []byte("value"))
	}
}
