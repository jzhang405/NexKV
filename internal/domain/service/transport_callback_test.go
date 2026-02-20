// Package service 定义领域服务接口
// BroadcastTracker 使用示例测试
package service

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// ========================================
// 示例 1: 日志记录场景
// ========================================

// LogCallback 日志记录回调（示例）
type LogCallback struct {
	logger *slog.Logger
}

func (cb *LogCallback) OnSuccess(peer model.PeerID, resp model.Message, stats BroadcastStats) {
	cb.logger.Info("✅ 收到成功响应",
		"peer", peer,
		"progress", fmt.Sprintf("%d/%d", stats.Success+stats.Failed, stats.Total))
}

func (cb *LogCallback) OnFailure(peer model.PeerID, err error, stats BroadcastStats) {
	cb.logger.Error("❌ 收到失败响应",
		"peer", peer,
		"error", err,
		"progress", fmt.Sprintf("%d/%d", stats.Success+stats.Failed, stats.Total))
}

func (cb *LogCallback) OnMajorityReached(stats BroadcastStats) {
	cb.logger.Info("🎯 达到多数派！",
		"success_rate", fmt.Sprintf("%.2f%%", stats.SuccessRate*100),
		"elapsed", stats.ElapsedTime)
}

func (cb *LogCallback) OnFullDone(stats BroadcastStats) {
	cb.logger.Info("✅ 全部完成！",
		"success", stats.Success,
		"failed", stats.Failed,
		"total_time", stats.ElapsedTime)
}

// ExampleBroadcastTracker_logCallback 日志记录场景示例
// 场景：使用回调记录广播过程的进度信息
func ExampleLogCallback() {
	// 创建 Tracker
	targets := []model.PeerID{"node-1", "node-2", "node-3"}
	tracker := NewBroadcastTracker("task-001", targets)

	// 设置回调
	tracker.SetCallback(&LogCallback{logger: slog.Default()})

	// 模拟广播过程
	_ = model.NewMessage("msg-1", model.MessageType(1), "node-1", "local", []byte("resp-1"))
	// tracker.RecordSuccess("node-1", resp1)
	// ... 其他操作
}

// ========================================
// 示例 2: 指标上报场景
// ========================================

// MetricsCallback 指标上报回调（示例）
type MetricsCallback struct {
	successCount     int64
	failureCount     int64
	majorityLatency  time.Duration
	totalLatency     time.Duration
}

func (cb *MetricsCallback) OnSuccess(peer model.PeerID, resp model.Message, stats BroadcastStats) {
	cb.successCount++
	// cb.metricsClient.Gauge("broadcast.progress", stats.SuccessRate)
}

func (cb *MetricsCallback) OnFailure(peer model.PeerID, err error, stats BroadcastStats) {
	cb.failureCount++
	// cb.metricsClient.Counter("broadcast.failures", 1)
}

func (cb *MetricsCallback) OnMajorityReached(stats BroadcastStats) {
	cb.majorityLatency = stats.ElapsedTime
	// cb.metricsClient.Counter("broadcast.majority_reached", 1)
	// cb.metricsClient.Histogram("broadcast.majority_latency", stats.ElapsedTime)
}

func (cb *MetricsCallback) OnFullDone(stats BroadcastStats) {
	cb.totalLatency = stats.ElapsedTime
	// cb.metricsClient.Histogram("broadcast.total_latency", stats.ElapsedTime)
}

// ExampleMetricsCallback 指标上报场景示例
// 场景：使用回调上报 Prometheus 监控指标
func ExampleMetricsCallback() {
	targets := []model.PeerID{"node-1", "node-2", "node-3"}
	tracker := NewBroadcastTracker("task-002", targets)

	metrics := &MetricsCallback{}
	tracker.SetCallback(metrics)

	// 模拟广播过程
	// ...
}

// ========================================
// 示例 3: 只关心 OnFullDone（部分实现）
// ========================================

// SimpleCallback 只关心全部完成的场景（使用 NoOpCallback）
type SimpleCallback struct {
	NoOpCallback // 嵌入所有空实现
}

// 只重写关心的方法
func (cb *SimpleCallback) OnFullDone(stats BroadcastStats) {
	fmt.Printf("广播完成！成功: %d, 失败: %d, 耗时: %v\n",
		stats.Success, stats.Failed, stats.ElapsedTime)
}

// ExampleSimpleCallback 部分实现示例
// 场景：只关心全部完成事件，使用 NoOpCallback 简化实现
func ExampleSimpleCallback() {
	targets := []model.PeerID{"node-1", "node-2", "node-3"}
	tracker := NewBroadcastTracker("task-003", targets)

	// 只设置 OnFullDone 回调
	tracker.SetCallback(&SimpleCallback{})

	// 模拟广播过程
	// ...
}

// ========================================
// 示例 4: 动态启用/禁用回调
// ========================================

// ExampleBroadcastTracker_enableDisable 动态启用/禁用回调示例
// 场景：在测试环境临时禁用回调
func ExampleBroadcastTracker() {
	targets := []model.PeerID{"node-1", "node-2", "node-3"}
	tracker := NewBroadcastTracker("task-004", targets)

	// 设置回调（默认启用）
	tracker.SetCallback(&LogCallback{logger: slog.Default()})

	// 临时禁用回调（例如在测试环境）
	tracker.EnableCallbacks(false)

	// ... 广播过程

	// 重新启用回调
	tracker.EnableCallbacks(true)
}
