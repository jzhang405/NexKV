// Package fault 提供故障检测器实现
package fault

import (
	"math"
	"testing"
	"time"
)

// ==================== PhiAccrualDetector Tests ====================

func TestNewPhiAccrualDetector_DefaultConfig(t *testing.T) {
	detector := NewPhiAccrualDetector(nil)

	if detector.threshold != DefaultThreshold {
		t.Errorf("expected threshold %f, got %f", DefaultThreshold, detector.threshold)
	}
	if detector.minStdDev != DefaultMinStdDev {
		t.Errorf("expected minStdDev %v, got %v", DefaultMinStdDev, detector.minStdDev)
	}
	if detector.minSamples != DefaultMinSamples {
		t.Errorf("expected minSamples %d, got %d", DefaultMinSamples, detector.minSamples)
	}
	if detector.maxSampleSize != DefaultMaxSampleSize {
		t.Errorf("expected maxSampleSize %d, got %d", DefaultMaxSampleSize, detector.maxSampleSize)
	}
}

func TestNewPhiAccrualDetector_CustomConfig(t *testing.T) {
	config := &PhiAccrualConfig{
		LocalNodeID:   "node-1",
		Threshold:     10.0,
		MinStdDev:     200 * time.Millisecond,
		MinSamples:    5,
		MaxSampleSize: 500,
	}
	detector := NewPhiAccrualDetector(config)

	if detector.threshold != 10.0 {
		t.Errorf("expected threshold 10.0, got %f", detector.threshold)
	}
	if detector.minStdDev != 200*time.Millisecond {
		t.Errorf("expected minStdDev 200ms, got %v", detector.minStdDev)
	}
	if detector.minSamples != 5 {
		t.Errorf("expected minSamples 5, got %d", detector.minSamples)
	}
	if detector.maxSampleSize != 500 {
		t.Errorf("expected maxSampleSize 500, got %d", detector.maxSampleSize)
	}
}

// ==================== RecordHeartbeat Tests ====================

func TestPhiAccrualDetector_RecordHeartbeat_FirstHeartbeat(t *testing.T) {
	detector := NewPhiAccrualDetector(nil)

	detector.RecordHeartbeat("node-2")

	stats := detector.GetStats("node-2")
	if stats == nil {
		t.Fatal("expected stats to be created")
	}
	if stats.LastHeartbeat.IsZero() {
		t.Error("expected LastHeartbeat to be set")
	}
	if len(stats.Intervals) != 0 {
		t.Errorf("expected 0 intervals on first heartbeat, got %d", len(stats.Intervals))
	}
}

func TestPhiAccrualDetector_RecordHeartbeat_MultipleHeartbeats(t *testing.T) {
	detector := NewPhiAccrualDetector(nil)

	// 记录多次心跳
	for i := 0; i < 15; i++ {
		detector.RecordHeartbeat("node-2")
		time.Sleep(10 * time.Millisecond)
	}

	stats := detector.GetStats("node-2")
	if stats == nil {
		t.Fatal("expected stats to be created")
	}
	// 由于时间不稳定，只检查有足够的间隔记录
	if len(stats.Intervals) < 10 {
		t.Errorf("expected at least 10 intervals, got %d", len(stats.Intervals))
	}
	if stats.Mean == 0 {
		t.Error("expected Mean to be calculated")
	}
	if stats.Variance == 0 {
		t.Error("expected Variance to be calculated")
	}
}

func TestPhiAccrualDetector_RecordHeartbeat_MaxSampleSize(t *testing.T) {
	config := &PhiAccrualConfig{
		MaxSampleSize: 5,
	}
	detector := NewPhiAccrualDetector(config)

	// 调试：验证 maxSampleSize 设置正确
	t.Logf("detector.maxSampleSize = %d", detector.maxSampleSize)

	// 记录超过 maxSampleSize 次心跳（使用较长间隔确保每次产生间隔）
	for i := 0; i < 10; i++ {
		detector.RecordHeartbeat("node-2")
		time.Sleep(20 * time.Millisecond)
	}

	stats := detector.GetStats("node-2")
	if stats == nil {
		t.Fatal("expected stats to be created")
	}

	t.Logf("intervals count = %d, maxSampleSize = %d", len(stats.Intervals), config.MaxSampleSize)

	// 验证间隔数不超过 maxSampleSize
	if len(stats.Intervals) > config.MaxSampleSize {
		t.Errorf("expected at most %d intervals (maxSampleSize), got %d", config.MaxSampleSize, len(stats.Intervals))
	}
}

// ==================== Phi Tests ====================

func TestPhiAccrualDetector_Phii_NoHeartbeat(t *testing.T) {
	detector := NewPhiAccrualDetector(nil)

	phi := detector.Phi("unknown-node")
	if phi != 0 {
		t.Errorf("expected phi 0 for unknown node, got %f", phi)
	}
}

func TestPhiAccrualDetector_Phi_InsufficientSamples(t *testing.T) {
	config := &PhiAccrualConfig{
		MinSamples: 10,
	}
	detector := NewPhiAccrualDetector(config)

	// 记录少于 minSamples 次心跳
	for i := 0; i < 5; i++ {
		detector.RecordHeartbeat("node-2")
		time.Sleep(10 * time.Millisecond)
	}

	phi := detector.Phi("node-2")
	if phi != 0 {
		t.Errorf("expected phi 0 for insufficient samples, got %f", phi)
	}
}

func TestPhiAccrualDetector_Phi_Normal(t *testing.T) {
	detector := NewPhiAccrualDetector(nil)

	// 记录足够的心跳（超过 minSamples）
	for i := 0; i < 15; i++ {
		detector.RecordHeartbeat("node-2")
		time.Sleep(50 * time.Millisecond)
	}

	// 立即检查，Phi 值应该很小
	phi := detector.Phi("node-2")
	if phi > 1.0 {
		t.Errorf("expected phi < 1.0 for recent heartbeat, got %f", phi)
	}
}

func TestPhiAccrualDetector_Phi_HighAfterDelay(t *testing.T) {
	detector := NewPhiAccrualDetector(nil)

	// 记录足够的心跳
	for i := 0; i < 15; i++ {
		detector.RecordHeartbeat("node-2")
		time.Sleep(20 * time.Millisecond)
	}

	// 等待较长时间后检查
	time.Sleep(500 * time.Millisecond)

	phi := detector.Phi("node-2")
	if phi < 1.0 {
		t.Errorf("expected phi > 1.0 after delay, got %f", phi)
	}
}

// ==================== IsNodeFailed Tests ====================

func TestPhiAccrualDetector_IsNodeFailed_NoHeartbeat(t *testing.T) {
	detector := NewPhiAccrualDetector(nil)

	// 无心跳记录的节点不应被判定为故障
	if detector.IsNodeFailed("unknown-node") {
		t.Error("expected unknown node not to be failed")
	}
}

func TestPhiAccrualDetector_IsNodeFailed_InsufficientSamples(t *testing.T) {
	config := &PhiAccrualConfig{
		MinSamples: 10,
	}
	detector := NewPhiAccrualDetector(config)

	// 记录少于 minSamples 次心跳
	for i := 0; i < 5; i++ {
		detector.RecordHeartbeat("node-2")
		time.Sleep(10 * time.Millisecond)
	}

	// 样本不足不应被判定为故障
	if detector.IsNodeFailed("node-2") {
		t.Error("expected node not to be failed with insufficient samples")
	}
}

func TestPhiAccrualDetector_IsNodeFailed_Normal(t *testing.T) {
	detector := NewPhiAccrualDetector(nil)

	// 记录足够的心跳
	for i := 0; i < 15; i++ {
		detector.RecordHeartbeat("node-2")
		time.Sleep(20 * time.Millisecond)
	}

	// 立即检查，节点应该正常
	if detector.IsNodeFailed("node-2") {
		t.Error("expected node not to be failed with recent heartbeat")
	}
}

func TestPhiAccrualDetector_IsNodeFailed_AfterTimeout(t *testing.T) {
	config := &PhiAccrualConfig{
		Threshold:  3.0, // 使用较低阈值便于测试
		MinSamples: 5,
		MinStdDev:  10 * time.Millisecond,
	}
	detector := NewPhiAccrualDetector(config)

	// 记录足够的心跳（快速发送）
	for i := 0; i < 10; i++ {
		detector.RecordHeartbeat("node-2")
		time.Sleep(10 * time.Millisecond)
	}

	// 立即检查，节点应该正常
	if detector.IsNodeFailed("node-2") {
		t.Error("expected node not to be failed immediately")
	}

	// 等待足够长时间
	time.Sleep(200 * time.Millisecond)

	// 现在节点应该被判定为故障
	if !detector.IsNodeFailed("node-2") {
		t.Error("expected node to be failed after timeout")
	}
}

// ==================== Reset Tests ====================

func TestPhiAccrualDetector_Reset(t *testing.T) {
	detector := NewPhiAccrualDetector(nil)

	// 记录心跳
	for i := 0; i < 15; i++ {
		detector.RecordHeartbeat("node-2")
		time.Sleep(10 * time.Millisecond)
	}

	// 验证有记录
	stats := detector.GetStats("node-2")
	if stats == nil {
		t.Fatal("expected stats to exist")
	}

	// 重置
	detector.Reset("node-2")

	// 验证已清除
	stats = detector.GetStats("node-2")
	if stats != nil {
		t.Error("expected stats to be nil after reset")
	}
}

func TestPhiAccrualDetector_ResetAll(t *testing.T) {
	detector := NewPhiAccrualDetector(nil)

	// 记录多个节点的心跳
	for _, nodeID := range []string{"node-2", "node-3", "node-4"} {
		for i := 0; i < 5; i++ {
			detector.RecordHeartbeat(nodeID)
			time.Sleep(10 * time.Millisecond)
		}
	}

	// 验证有记录
	ids := detector.GetAllNodeIDs()
	if len(ids) != 3 {
		t.Errorf("expected 3 nodes, got %d", len(ids))
	}

	// 重置所有
	detector.ResetAll()

	// 验证已清除
	ids = detector.GetAllNodeIDs()
	if len(ids) != 0 {
		t.Errorf("expected 0 nodes after reset all, got %d", len(ids))
	}
}

// ==================== SetThreshold Tests ====================

func TestPhiAccrualDetector_SetThreshold(t *testing.T) {
	detector := NewPhiAccrualDetector(nil)

	if detector.GetThreshold() != DefaultThreshold {
		t.Errorf("expected default threshold %f, got %f", DefaultThreshold, detector.GetThreshold())
	}

	detector.SetThreshold(5.0)
	if detector.GetThreshold() != 5.0 {
		t.Errorf("expected threshold 5.0, got %f", detector.GetThreshold())
	}

	// 无效值不应更新
	detector.SetThreshold(-1.0)
	if detector.GetThreshold() != 5.0 {
		t.Errorf("expected threshold to remain 5.0, got %f", detector.GetThreshold())
	}
}

// ==================== SetMinSamples Tests ====================

func TestPhiAccrualDetector_SetMinSamples(t *testing.T) {
	detector := NewPhiAccrualDetector(nil)

	if detector.GetMinSamples() != DefaultMinSamples {
		t.Errorf("expected default minSamples %d, got %d", DefaultMinSamples, detector.GetMinSamples())
	}

	detector.SetMinSamples(20)
	if detector.GetMinSamples() != 20 {
		t.Errorf("expected minSamples 20, got %d", detector.GetMinSamples())
	}

	// 无效值不应更新
	detector.SetMinSamples(-1)
	if detector.GetMinSamples() != 20 {
		t.Errorf("expected minSamples to remain 20, got %d", detector.GetMinSamples())
	}
}

// ==================== erf Tests ====================

func TestErf_BoundaryValues(t *testing.T) {
	// erf(0) = 0（使用宽松容差）
	erfZero := erf(0)
	if math.Abs(erfZero) > 1e-6 {
		t.Errorf("expected erf(0) ≈ 0, got %f", erfZero)
	}

	// erf(+inf) = 1
	// erf(-inf) = -1
	// 这里只测试有限值

	// erf(3) ≈ 0.9999779
	erfThree := erf(3)
	if erfThree < 0.999 || erfThree > 1.0 {
		t.Errorf("expected erf(3) ≈ 1.0, got %f", erfThree)
	}

	// erf(-3) ≈ -0.9999779
	erfNegThree := erf(-3)
	if erfNegThree < -1.0 || erfNegThree > -0.999 {
		t.Errorf("expected erf(-3) ≈ -1.0, got %f", erfNegThree)
	}

	// erf(1) ≈ 0.8427
	erfOne := erf(1)
	expectedErfOne := 0.8427
	if math.Abs(erfOne-expectedErfOne) > 0.01 {
		t.Errorf("expected erf(1) ≈ %f, got %f", expectedErfOne, erfOne)
	}
}

func TestErf_Symmetry(t *testing.T) {
	// erf(-x) = -erf(x)
	for _, x := range []float64{0.5, 1.0, 1.5, 2.0} {
		if erf(-x) != -erf(x) {
			t.Errorf("erf(-%f) = %f, expected %f", x, erf(-x), -erf(x))
		}
	}
}

// ==================== Concurrent Tests ====================

func TestPhiAccrualDetector_ConcurrentAccess(t *testing.T) {
	detector := NewPhiAccrualDetector(nil)

	done := make(chan bool)

	// 并发记录心跳
	go func() {
		for i := 0; i < 100; i++ {
			detector.RecordHeartbeat("node-2")
			time.Sleep(time.Millisecond)
		}
		done <- true
	}()

	// 并发读取 Phi
	go func() {
		for i := 0; i < 100; i++ {
			_ = detector.Phi("node-2")
			time.Sleep(time.Millisecond)
		}
		done <- true
	}()

	// 并发检查故障状态
	go func() {
		for i := 0; i < 100; i++ {
			_ = detector.IsNodeFailed("node-2")
			time.Sleep(time.Millisecond)
		}
		done <- true
	}()

	// 等待所有 goroutine 完成
	for i := 0; i < 3; i++ {
		<-done
	}
}

// ==================== Network Jitter Simulation Test ====================

func TestPhiAccrualDetector_NetworkJitter(t *testing.T) {
	// 测试网络抖动场景：不应将正常抖动误判为故障
	config := &PhiAccrualConfig{
		Threshold:  8.0, // 高阈值
		MinSamples: 20,  // 更多样本
		MinStdDev:  100 * time.Millisecond,
	}
	detector := NewPhiAccrualDetector(config)

	// 模拟网络抖动：心跳间隔在 10ms-100ms 之间波动
	for i := 0; i < 30; i++ {
		detector.RecordHeartbeat("node-2")
		// 模拟抖动
		delay := time.Duration(10+i%10) * time.Millisecond
		time.Sleep(delay)
	}

	// 短暂等待后检查
	time.Sleep(50 * time.Millisecond)

	// 不应误判为故障
	if detector.IsNodeFailed("node-2") {
		t.Error("expected node not to be failed due to normal network jitter")
	}
}

// ==================== Benchmark ====================

func BenchmarkPhiAccrualDetector_RecordHeartbeat(b *testing.B) {
	detector := NewPhiAccrualDetector(nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		detector.RecordHeartbeat("node-2")
	}
}

func BenchmarkPhiAccrualDetector_Phi(b *testing.B) {
	detector := NewPhiAccrualDetector(nil)

	// 预先记录心跳
	for i := 0; i < 15; i++ {
		detector.RecordHeartbeat("node-2")
		time.Sleep(time.Millisecond)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = detector.Phi("node-2")
	}
}

func BenchmarkPhiAccrualDetector_IsNodeFailed(b *testing.B) {
	detector := NewPhiAccrualDetector(nil)

	// 预先记录心跳
	for i := 0; i < 15; i++ {
		detector.RecordHeartbeat("node-2")
		time.Sleep(time.Millisecond)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = detector.IsNodeFailed("node-2")
	}
}
