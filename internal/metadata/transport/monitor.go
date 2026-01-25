// Package transport 维度化监控
//
// 实现按消息类型/节点/错误类型的统计与监控
package transport

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// MetricType 监控指标类型
type MetricType string

const (
	// MetricTypeMessageCount 消息数量
	MetricTypeMessageCount MetricType = "message_count"
	// MetricTypeMessageLatency 消息延迟
	MetricTypeMessageLatency MetricType = "message_latency"
	// MetricTypeMessageSize 消息大小
	MetricTypeMessageSize MetricType = "message_size"
	// MetricTypeErrorCount 错误数量
	MetricTypeErrorCount MetricType = "error_count"
)

// DimensionalMonitor 维度化监控器
//
// 提供多维度统计：
//   - 按消息类型统计
//   - 按节点统计
//   - 按错误类型统计
//   - 按协议类型统计
//
// Goroutine 生命周期管理：
//   - 方法1：使用 NewDimensionalMonitorWithContext 创建，传入 context.Context
//   - 方法2：调用 Stop() 方法显式停止
//   - 推荐：两种方式结合使用，确保资源正确释放
type DimensionalMonitor struct {
	// 按消息类型统计
	byMessageType   map[types.MessageType]*MessageTypeStats
	byMessageTypeMu sync.RWMutex

	// 按节点统计
	byNode   map[string]*NodeStats
	byNodeMu sync.RWMutex

	// 按错误类型统计
	byErrorType   map[string]*ErrorTypeStats
	byErrorTypeMu sync.RWMutex

	// 按协议类型统计
	byProtocol   map[types.ProtocolType]*ProtocolStats
	byProtocolMu sync.RWMutex

	// 全局统计
	globalStats   *GlobalStats
	globalStatsMu sync.RWMutex

	// 启动时间
	startTime time.Time

	// 停止通道（用于优雅关闭 goroutine）
	stopCh   chan struct{}
	stopOnce sync.Once

	// context 控制（可选，用于外部控制生命周期）
	ctx    context.Context
	cancel context.CancelFunc
}

// MessageTypeStats 消息类型统计
type MessageTypeStats struct {
	Count           atomic.Uint64 // 消息总数
	SuccessCount    atomic.Uint64 // 成功数量
	FailureCount    atomic.Uint64 // 失败数量
	TotalLatency    atomic.Uint64 // 总延迟（纳秒）
	TotalSize       atomic.Uint64 // 总大小（字节）
	LastMessageTime atomic.Int64  // 最后一条消息时间
}

// NodeStats 节点统计
type NodeStats struct {
	NodeID          string            // 节点 ID
	Address         string            // 节点地址
	MessageCount    atomic.Uint64     // 消息数量
	SuccessCount    atomic.Uint64     // 成功数量
	FailureCount    atomic.Uint64     // 失败数量
	TotalLatency    atomic.Uint64     // 总延迟
	LastContactTime atomic.Int64      // 最后联系时间
	ErrorCounts     map[string]uint64 // 错误计数（按错误类型）
	ErrorCountsMu   sync.RWMutex      // 错误计数锁
}

// ErrorTypeStats 错误类型统计
type ErrorTypeStats struct {
	ErrorType       string              // 错误类型
	Count           atomic.Uint64       // 错误数量
	LastOccurrence  atomic.Int64        // 最后发生时间
	AffectedNodes   map[string]struct{} // 受影响的节点
	AffectedNodesMu sync.RWMutex        // 受影响节点锁
}

// ProtocolStats 协议统计
type ProtocolStats struct {
	ProtocolType types.ProtocolType // 协议类型
	MessageCount atomic.Uint64      // 消息数量
	SuccessCount atomic.Uint64      // 成功数量
	FailureCount atomic.Uint64      // 失败数量
	TotalLatency atomic.Uint64      // 总延迟
	IsActive     atomic.Bool        // 是否活跃
}

// GlobalStats 全局统计
type GlobalStats struct {
	TotalMessages atomic.Uint64 // 总消息数
	TotalSuccess  atomic.Uint64 // 总成功数
	TotalFailure  atomic.Uint64 // 总失败数
	TotalLatency  atomic.Uint64 // 总延迟
	TotalBytes    atomic.Uint64 // 总字节数
	StartTime     time.Time     // 启动时间
	Uptime        atomic.Int64  // 运行时长（纳秒）
}

// NewDimensionalMonitor 创建维度化监控器
//
// 注意：创建的监控器会启动一个后台 goroutine 更新运行时长。
// 使用后必须调用 Stop() 方法释放资源，避免 goroutine 泄漏。
//
// 推荐使用 defer 确保资源释放：
//
//	monitor := transport.NewDimensionalMonitor()
//	defer monitor.Stop()
func NewDimensionalMonitor() *DimensionalMonitor {
	return NewDimensionalMonitorWithContext(context.Background())
}

// NewDimensionalMonitorWithContext 创建维度化监控器（支持 context 控制）
//
// 当 ctx 被取消时，goroutine 会自动停止。同时仍需调用 Stop() 释放资源。
//
// 参数:
//   - ctx: 用于控制监控器生命周期的 context
//
// 推荐使用 defer 确保资源释放：
//
//	ctx, cancel := context.WithCancel(context.Background())
//	defer cancel()
//	monitor := transport.NewDimensionalMonitorWithContext(ctx)
//	defer monitor.Stop()
func NewDimensionalMonitorWithContext(ctx context.Context) *DimensionalMonitor {
	now := time.Now()
	// 创建可取消的 context
	monitorCtx, monitorCancel := context.WithCancel(ctx)

	m := &DimensionalMonitor{
		byMessageType: make(map[types.MessageType]*MessageTypeStats),
		byNode:        make(map[string]*NodeStats),
		byErrorType:   make(map[string]*ErrorTypeStats),
		byProtocol:    make(map[types.ProtocolType]*ProtocolStats),
		startTime:     now,
		globalStats: &GlobalStats{
			StartTime: now,
		},
		stopCh: make(chan struct{}),
		ctx:    monitorCtx,
		cancel: monitorCancel,
	}

	// 启动运行时更新协程
	go m.updateUptime()

	return m
}

// RecordMessage 记录消息
func (m *DimensionalMonitor) RecordMessage(
	msgType types.MessageType,
	protocol types.ProtocolType,
	nodeAddr string,
	size int,
	latency int64,
	success bool,
	err error,
) {
	now := time.Now().UnixNano()

	m.updateGlobalStats(size, latency, success)
	m.recordMessageTypeStats(msgType, size, latency, success, now)
	m.recordNodeStats(nodeAddr, size, latency, success, err, now)
	m.recordProtocolStats(protocol, size, latency, success)

	if err != nil {
		m.recordError(err, nodeAddr, now)
	}
}

// updateGlobalStats 更新全局统计
func (m *DimensionalMonitor) updateGlobalStats(size int, latency int64, success bool) {
	m.globalStatsMu.Lock()
	defer m.globalStatsMu.Unlock()

	m.globalStats.TotalMessages.Add(1)
	m.globalStats.TotalBytes.Add(uint64(size))

	if success {
		m.globalStats.TotalSuccess.Add(1)
	} else {
		m.globalStats.TotalFailure.Add(1)
	}

	if latency > 0 {
		m.globalStats.TotalLatency.Add(uint64(latency))
	}
}

// recordMessageTypeStats 记录消息类型统计
func (m *DimensionalMonitor) recordMessageTypeStats(
	msgType types.MessageType,
	size int,
	latency int64,
	success bool,
	now int64,
) {
	m.byMessageTypeMu.Lock()
	stats, exists := m.byMessageType[msgType]
	if !exists {
		stats = &MessageTypeStats{}
		m.byMessageType[msgType] = stats
	}
	m.byMessageTypeMu.Unlock()

	stats.Count.Add(1)
	stats.TotalSize.Add(uint64(size))
	stats.LastMessageTime.Store(now)

	if success {
		stats.SuccessCount.Add(1)
	} else {
		stats.FailureCount.Add(1)
	}

	if latency > 0 {
		stats.TotalLatency.Add(uint64(latency))
	}
}

// recordNodeStats 记录节点统计
func (m *DimensionalMonitor) recordNodeStats(
	nodeAddr string,
	_ int, // size: 预留用于未来按消息大小统计
	latency int64,
	success bool,
	err error,
	now int64,
) {
	m.byNodeMu.Lock()
	stats, exists := m.byNode[nodeAddr]
	if !exists {
		stats = &NodeStats{
			NodeID:      nodeAddr,
			Address:     nodeAddr,
			ErrorCounts: make(map[string]uint64),
		}
		m.byNode[nodeAddr] = stats
	}
	m.byNodeMu.Unlock()

	stats.MessageCount.Add(1)
	stats.LastContactTime.Store(now)

	if success {
		stats.SuccessCount.Add(1)
	} else {
		stats.FailureCount.Add(1)
	}

	if latency > 0 {
		stats.TotalLatency.Add(uint64(latency))
	}

	if err != nil {
		errType := getErrorType(err)
		stats.ErrorCountsMu.Lock()
		stats.ErrorCounts[errType]++
		stats.ErrorCountsMu.Unlock()
	}
}

// recordProtocolStats 记录协议统计
func (m *DimensionalMonitor) recordProtocolStats(
	protocol types.ProtocolType,
	_ int, // size: 预留用于未来按消息大小统计
	latency int64,
	success bool,
) {
	m.byProtocolMu.Lock()
	stats, exists := m.byProtocol[protocol]
	if !exists {
		stats = &ProtocolStats{
			ProtocolType: protocol,
		}
		m.byProtocol[protocol] = stats
		stats.IsActive.Store(true)
	}
	m.byProtocolMu.Unlock()

	stats.MessageCount.Add(1)
	if success {
		stats.SuccessCount.Add(1)
	} else {
		stats.FailureCount.Add(1)
	}

	if latency > 0 {
		stats.TotalLatency.Add(uint64(latency))
	}
}

// recordError 记录错误
func (m *DimensionalMonitor) recordError(err error, nodeAddr string, now int64) {
	errType := getErrorType(err)

	m.byErrorTypeMu.Lock()
	stats, exists := m.byErrorType[errType]
	if !exists {
		stats = &ErrorTypeStats{
			ErrorType:     errType,
			AffectedNodes: make(map[string]struct{}),
		}
		m.byErrorType[errType] = stats
	}
	m.byErrorTypeMu.Unlock()

	stats.Count.Add(1)
	stats.LastOccurrence.Store(now)

	stats.AffectedNodesMu.Lock()
	stats.AffectedNodes[nodeAddr] = struct{}{}
	stats.AffectedNodesMu.Unlock()
}

// GetMessageTypeStats 获取消息类型统计
func (m *DimensionalMonitor) GetMessageTypeStats(
	msgType types.MessageType,
) (*MessageTypeStats, bool) {
	m.byMessageTypeMu.RLock()
	defer m.byMessageTypeMu.RUnlock()

	stats, exists := m.byMessageType[msgType]
	if !exists {
		return nil, false
	}

	return copyMessageTypeStats(stats), true
}

// GetAllMessageTypeStats 获取所有消息类型统计
func (m *DimensionalMonitor) GetAllMessageTypeStats() map[types.MessageType]*MessageTypeStats {
	m.byMessageTypeMu.RLock()
	defer m.byMessageTypeMu.RUnlock()

	return copyStatsMap(m.byMessageType, copyMessageTypeStats)
}

// GetNodeStats 获取节点统计
func (m *DimensionalMonitor) GetNodeStats(nodeAddr string) (*NodeStats, bool) {
	m.byNodeMu.RLock()
	defer m.byNodeMu.RUnlock()

	stats, exists := m.byNode[nodeAddr]
	if !exists {
		return nil, false
	}

	return copyNodeStats(stats), true
}

// GetAllNodeStats 获取所有节点统计
func (m *DimensionalMonitor) GetAllNodeStats() map[string]*NodeStats {
	m.byNodeMu.RLock()
	defer m.byNodeMu.RUnlock()

	return copyStatsMap(m.byNode, copyNodeStats)
}

// GetErrorTypeStats 获取错误类型统计
func (m *DimensionalMonitor) GetErrorTypeStats(errType string) (*ErrorTypeStats, bool) {
	m.byErrorTypeMu.RLock()
	defer m.byErrorTypeMu.RUnlock()

	stats, exists := m.byErrorType[errType]
	if !exists {
		return nil, false
	}

	return copyErrorTypeStats(stats), true
}

// GetAllErrorTypeStats 获取所有错误类型统计
func (m *DimensionalMonitor) GetAllErrorTypeStats() map[string]*ErrorTypeStats {
	m.byErrorTypeMu.RLock()
	defer m.byErrorTypeMu.RUnlock()

	return copyStatsMap(m.byErrorType, copyErrorTypeStats)
}

// GetProtocolStats 获取协议统计
func (m *DimensionalMonitor) GetProtocolStats(
	protocol types.ProtocolType,
) (*ProtocolStats, bool) {
	m.byProtocolMu.RLock()
	defer m.byProtocolMu.RUnlock()

	stats, exists := m.byProtocol[protocol]
	if !exists {
		return nil, false
	}

	return copyProtocolStats(stats), true
}

// GetAllProtocolStats 获取所有协议统计
func (m *DimensionalMonitor) GetAllProtocolStats() map[types.ProtocolType]*ProtocolStats {
	m.byProtocolMu.RLock()
	defer m.byProtocolMu.RUnlock()

	return copyStatsMap(m.byProtocol, copyProtocolStats)
}

// GetGlobalStats 获取全局统计
func (m *DimensionalMonitor) GetGlobalStats() *GlobalStats {
	m.globalStatsMu.RLock()
	defer m.globalStatsMu.RUnlock()

	return copyGlobalStats(m.globalStats)
}

// copyStatsMap 泛型统计 map 拷贝辅助函数
func copyStatsMap[K comparable, V any](m map[K]*V, copyFunc func(*V) *V) map[K]*V {
	if m == nil {
		return nil
	}
	result := make(map[K]*V, len(m))
	for k, v := range m {
		result[k] = copyFunc(v)
	}
	return result
}

// updateUptime 更新运行时长
func (m *DimensionalMonitor) updateUptime() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			uptime := time.Since(m.startTime)
			m.globalStats.Uptime.Store(uptime.Nanoseconds())
		case <-m.stopCh:
			return
		case <-m.ctx.Done():
			// context 被取消，停止 goroutine
			return
		}
	}
}

// Stop 停止监控器（优雅关闭 goroutine）
func (m *DimensionalMonitor) Stop() {
	m.stopOnce.Do(func() {
		// 关闭 stopCh 通知 updateUptime 停止
		close(m.stopCh)
		// 调用 cancel() 释放 context 相关资源
		if m.cancel != nil {
			m.cancel()
		}
	})
}

// getErrorType 获取错误类型
func getErrorType(err error) string {
	if err == nil {
		return "unknown"
	}

	// 协议层错误
	if types.IsProtocolError(err) {
		return "protocol_error"
	}

	// 业务层错误
	if types.IsBusinessError(err) {
		return "business_error"
	}

	return "unknown"
}

func copyMessageTypeStats(stats *MessageTypeStats) *MessageTypeStats {
	copy := &MessageTypeStats{}
	copy.Count.Store(stats.Count.Load())
	copy.SuccessCount.Store(stats.SuccessCount.Load())
	copy.FailureCount.Store(stats.FailureCount.Load())
	copy.TotalLatency.Store(stats.TotalLatency.Load())
	copy.TotalSize.Store(stats.TotalSize.Load())
	copy.LastMessageTime.Store(stats.LastMessageTime.Load())
	return copy
}

func copyNodeStats(stats *NodeStats) *NodeStats {
	stats.ErrorCountsMu.RLock()
	defer stats.ErrorCountsMu.RUnlock()

	copy := &NodeStats{
		NodeID:      stats.NodeID,
		Address:     stats.Address,
		ErrorCounts: make(map[string]uint64),
	}
	copy.MessageCount.Store(stats.MessageCount.Load())
	copy.SuccessCount.Store(stats.SuccessCount.Load())
	copy.FailureCount.Store(stats.FailureCount.Load())
	copy.TotalLatency.Store(stats.TotalLatency.Load())
	copy.LastContactTime.Store(stats.LastContactTime.Load())

	// 拷贝 ErrorCounts（值类型，不需要深拷贝指针）
	for k, v := range stats.ErrorCounts {
		copy.ErrorCounts[k] = v
	}

	return copy
}

func copyErrorTypeStats(stats *ErrorTypeStats) *ErrorTypeStats {
	copy := &ErrorTypeStats{
		ErrorType:     stats.ErrorType,
		AffectedNodes: make(map[string]struct{}),
	}
	copy.Count.Store(stats.Count.Load())
	copy.LastOccurrence.Store(stats.LastOccurrence.Load())

	stats.AffectedNodesMu.RLock()
	defer stats.AffectedNodesMu.RUnlock()
	for k := range stats.AffectedNodes {
		copy.AffectedNodes[k] = struct{}{}
	}

	return copy
}

func copyProtocolStats(stats *ProtocolStats) *ProtocolStats {
	copy := &ProtocolStats{
		ProtocolType: stats.ProtocolType,
	}
	copy.MessageCount.Store(stats.MessageCount.Load())
	copy.SuccessCount.Store(stats.SuccessCount.Load())
	copy.FailureCount.Store(stats.FailureCount.Load())
	copy.TotalLatency.Store(stats.TotalLatency.Load())
	copy.IsActive.Store(stats.IsActive.Load())
	return copy
}

func copyGlobalStats(stats *GlobalStats) *GlobalStats {
	copy := &GlobalStats{
		StartTime: stats.StartTime,
	}
	copy.TotalMessages.Store(stats.TotalMessages.Load())
	copy.TotalSuccess.Store(stats.TotalSuccess.Load())
	copy.TotalFailure.Store(stats.TotalFailure.Load())
	copy.TotalLatency.Store(stats.TotalLatency.Load())
	copy.TotalBytes.Store(stats.TotalBytes.Load())
	copy.Uptime.Store(stats.Uptime.Load())
	return copy
}

// Reset 重置所有统计
func (m *DimensionalMonitor) Reset() {
	m.byMessageTypeMu.Lock()
	m.byMessageType = make(map[types.MessageType]*MessageTypeStats)
	m.byMessageTypeMu.Unlock()

	m.byNodeMu.Lock()
	m.byNode = make(map[string]*NodeStats)
	m.byNodeMu.Unlock()

	m.byErrorTypeMu.Lock()
	m.byErrorType = make(map[string]*ErrorTypeStats)
	m.byErrorTypeMu.Unlock()

	m.byProtocolMu.Lock()
	m.byProtocol = make(map[types.ProtocolType]*ProtocolStats)
	m.byProtocolMu.Unlock()

	m.globalStatsMu.Lock()
	m.globalStats = &GlobalStats{
		StartTime: time.Now(),
	}
	m.globalStatsMu.Unlock()

	m.startTime = time.Now()
}
