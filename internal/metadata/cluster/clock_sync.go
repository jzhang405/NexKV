// Package cluster 提供时钟同步处理器
//
// 本包实现：
//   - 毫秒级精度的时钟同步（方案 A）
//   - 基于 Transport 层的时钟同步消息
//   - 支持 HLC (Hybrid Logical Clock)
//   - 自动检测和补偿时间漂移
//
// 时钟同步精度：
//   - 毫秒级精度（性能优先）
//   - 适用于最终一致性的元数据同步
//   - 适用于 Gossip 协议的时间戳排序
package cluster

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/clock"
	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/transport"
)

// ClockSyncHandler 时钟同步处理器
//
// 核心功能：
//   - 处理时钟同步请求/响应
//   - 计算时间漂移（毫秒级精度）
//   - 自动补偿本地 HLC
//
// 设计原则：
//   - 非阻塞：异步处理时钟同步
//   - 轻量级：最小化网络和 CPU 开销
//   - 容错性：单个节点时钟异常不影响整体
type ClockSyncHandler struct {
	// 本地 HLC
	hlc *clock.HLC

	// 传输层
	transport transport.Transport

	// 节点 ID
	localNodeID string

	// 统计信息
	stats *ClockSyncStats

	// 生命周期
	started atomic.Bool
	stopped atomic.Bool
}

// ClockSyncStats 时钟同步统计信息
type ClockSyncStats struct {
	// 同步次数
	SyncCount atomic.Int64

	// 成功次数
	SyncSuccess atomic.Int64

	// 失败次数
	SyncFailed atomic.Int64

	// 最大时间漂移（毫秒）
	MaxDrift atomic.Int64

	// 平均时间漂移（毫秒）
	AvgDrift atomic.Int64

	// 最后同步时间
	LastSyncTime atomic.Value // time.Time
}

// ClockSyncConfig 时钟同步配置
type ClockSyncConfig struct {
	// SyncInterval 同步间隔（默认 10 秒）
	SyncInterval time.Duration

	// SyncTimeout 同步超时（默认 5 秒）
	SyncTimeout time.Duration

	// MaxAcceptableDrift 最大可接受漂移（毫秒，默认 1000ms）
	MaxAcceptableDrift int64

	// EnableAutoCompensation 是否启用自动补偿
	EnableAutoCompensation bool
}

// DefaultClockSyncConfig 返回默认配置
func DefaultClockSyncConfig() *ClockSyncConfig {
	return &ClockSyncConfig{
		SyncInterval:           10 * time.Second,
		SyncTimeout:            5 * time.Second,
		MaxAcceptableDrift:     1000, // 1 秒
		EnableAutoCompensation: true,
	}
}

// NewClockSyncHandler 创建时钟同步处理器
func NewClockSyncHandler(
	hlc *clock.HLC,
	transport transport.Transport,
	localNodeID string,
) (*ClockSyncHandler, error) {
	if hlc == nil {
		return nil, fmt.Errorf("hlc 不能为空")
	}

	if transport == nil {
		return nil, fmt.Errorf("transport 不能为空")
	}

	if localNodeID == "" {
		return nil, fmt.Errorf("localNodeID 不能为空")
	}

	handler := &ClockSyncHandler{
		hlc:         hlc,
		transport:   transport,
		localNodeID: localNodeID,
		stats:       &ClockSyncStats{},
	}

	// 初始化最后同步时间
	handler.stats.LastSyncTime.Store(time.Time{})

	return handler, nil
}

// Start 启动时钟同步处理器
func (h *ClockSyncHandler) Start(config *ClockSyncConfig) error {
	if !h.started.CompareAndSwap(false, true) {
		return fmt.Errorf("时钟同步处理器已经启动")
	}

	if config == nil {
		config = DefaultClockSyncConfig()
	}

	logging.WithFields(map[string]any{
		"interval":  config.SyncInterval,
		"timeout":   config.SyncTimeout,
		"max_drift": config.MaxAcceptableDrift,
		"auto_comp": config.EnableAutoCompensation,
	}).Info("启动时钟同步处理器（毫秒级精度）")

	return nil
}

// Stop 停止时钟同步处理器
func (h *ClockSyncHandler) Stop() error {
	if !h.stopped.CompareAndSwap(false, true) {
		return nil // 已经停止
	}

	logging.Info("停止时钟同步处理器")
	return nil
}

// HandleClockSyncRequest 处理时钟同步请求
//
// 流程：
//  1. 接收远程节点的时钟同步请求
//  2. 记录本地时间戳（毫秒级）
//  3. 计算时间漂移
//  4. 构造响应消息
func (h *ClockSyncHandler) HandleClockSyncRequest(
	ctx context.Context,
	msg *transport.ClockSyncMessage,
) (*transport.ClockSyncReplyMessage, error) {
	h.stats.SyncCount.Add(1)
	h.stats.LastSyncTime.Store(time.Now())

	// 获取本地 HLC 时间戳（毫秒级）
	localTimestamp := h.hlc.Now().PhysicalTime()

	// 计算时间漂移（毫秒）
	drift := localTimestamp - msg.Timestamp
	if drift < 0 {
		drift = -drift
	}

	// 更新统计信息
	h.updateDriftStats(drift)

	logging.WithFields(map[string]any{
		"remote_node_id": msg.NodeID,
		"remote_ts":      msg.Timestamp,
		"local_ts":       localTimestamp,
		"drift_ms":       drift,
	}).Debug("处理时钟同步请求")

	// 构造响应
	reply := &transport.ClockSyncReplyMessage{
		Timestamp: localTimestamp,
		NodeID:    h.localNodeID,
		Drift:     drift,
	}

	h.stats.SyncSuccess.Add(1)

	return reply, nil
}

// HandleClockSyncReply 处理时钟同步响应
//
// 流程：
//  1. 接收远程节点的时钟同步响应
//  2. 计算本地时间漂移
//  3. 如果启用自动补偿，调整本地 HLC
func (h *ClockSyncHandler) HandleClockSyncReply(
	reply *transport.ClockSyncReplyMessage,
	config *ClockSyncConfig,
) error {
	h.stats.SyncCount.Add(1)
	h.stats.LastSyncTime.Store(time.Now())

	// 获取本地 HLC 时间戳
	localTimestamp := h.hlc.Now().PhysicalTime()

	// 计算时间漂移（毫秒）
	drift := localTimestamp - reply.Timestamp
	if drift < 0 {
		drift = -drift
	}

	// 更新统计信息
	h.updateDriftStats(drift)

	logging.WithFields(map[string]any{
		"remote_node_id": reply.NodeID,
		"remote_ts":      reply.Timestamp,
		"local_ts":       localTimestamp,
		"drift_ms":       drift,
		"reply_drift_ms": reply.Drift,
	}).Debug("处理时钟同步响应")

	// 检查是否超过最大可接受漂移
	if config == nil {
		config = DefaultClockSyncConfig()
	}

	if drift > config.MaxAcceptableDrift {
		logging.WithFields(map[string]any{
			"drift_ms":       drift,
			"max_drift_ms":   config.MaxAcceptableDrift,
			"remote_node_id": reply.NodeID,
		}).Warn("时间漂移超过阈值")

		// TODO: 触发告警
	}

	// 自动补偿（如果启用）
	if config.EnableAutoCompensation && drift > 10 { // 超过 10ms 才补偿
		h.compensateClockDrift(drift)
	}

	h.stats.SyncSuccess.Add(1)

	return nil
}

// SendClockSyncRequest 发送时钟同步请求
//
// 向指定节点发送时钟同步请求，用于主动同步
func (h *ClockSyncHandler) SendClockSyncRequest(
	ctx context.Context,
	targetNodeID string,
	targetAddr string,
) error {
	// 获取本地 HLC 时间戳
	localTimestamp := h.hlc.Now().PhysicalTime()

	// 构造请求
	req := &transport.ClockSyncMessage{
		Timestamp: localTimestamp,
		NodeID:    h.localNodeID,
	}

	// 发送请求
	if err := h.transport.Send(ctx, targetAddr, req); err != nil {
		h.stats.SyncFailed.Add(1)
		return fmt.Errorf("发送时钟同步请求失败: %w", err)
	}

	logging.WithFields(map[string]any{
		"target_node_id": targetNodeID,
		"timestamp":      localTimestamp,
	}).Debug("发送时钟同步请求")

	h.stats.SyncCount.Add(1)
	h.stats.SyncSuccess.Add(1)
	h.stats.LastSyncTime.Store(time.Now())

	return nil
}

// updateDriftStats 更新漂移统计信息
func (h *ClockSyncHandler) updateDriftStats(drift int64) {
	// 更新最大漂移
	for {
		maxDrift := h.stats.MaxDrift.Load()
		if drift <= maxDrift {
			break
		}
		if h.stats.MaxDrift.CompareAndSwap(maxDrift, drift) {
			break
		}
	}

	// 更新平均漂移（简化版：移动平均）
	oldAvg := h.stats.AvgDrift.Load()
	newAvg := (oldAvg*9 + drift) / 10 // 简单的移动平均
	h.stats.AvgDrift.Store(newAvg)
}

// compensateClockDrift 补偿时钟漂移
//
// 方案 A：毫秒级精度补偿
//   - 直接调整 HLC 的逻辑时间部分
//   - 不修改物理时间
//   - 补偿范围：-1000ms 到 +1000ms
func (h *ClockSyncHandler) compensateClockDrift(drift int64) {
	// TODO: 实现 HLC 漂移补偿
	// 这需要扩展 clock.HLC 接口，添加 AdjustDrift 方法

	logging.WithField("drift_ms", drift).Debug("补偿时钟漂移（未实现）")
}

// GetStats 获取统计信息
func (h *ClockSyncHandler) GetStats() *ClockSyncStats {
	return h.stats
}

// GetClockDrift 获取当前时钟漂移估计
func (h *ClockSyncHandler) GetClockDrift() int64 {
	return h.stats.AvgDrift.Load()
}
