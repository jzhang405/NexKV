// Package cluster 故障检测器实现
//
// 基于心跳的故障检测机制
//
// 核心特性:
//   - 主动探测：定期向目标节点发送心跳探测
//   - 被动监听：监听节点的心跳消息
//   - Φ 累加器：使用类似 Phi Accrual Failure Detector 的算法
//   - 自适应阈值：根据网络状况动态调整故障判定阈值
package cluster

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/transport"
)

// FailureDetector 故障检测器
//
// 检测算法：
//  1. 维护每个节点的心跳间隔样本
//  2. 计算心跳间隔的均值和标准差
//  3. 使用 Φ 值计算节点故障概率
//  4. Φ 值超过阈值时判定节点故障
type FailureDetector struct {
	// 配置
	config *FailureDetectorConfig

	// 本地节点
	localNodeID string

	// 传输层
	transport transport.Transport

	// 节点心跳状态
	nodeStates   map[string]*NodeState
	nodeStatesMu sync.RWMutex

	// 故障回调
	onNodeFailed func(nodeID string)

	// 生命周期
	started atomic.Bool
	stopped atomic.Bool
	stopCh  chan struct{}
	stopWg  sync.WaitGroup

	// 统计信息
	stats *FailureDetectorStats
}

// FailureDetectorConfig 故障检测配置
type FailureDetectorConfig struct {
	// Interval 心跳探测间隔（默认 5 秒）
	Interval time.Duration

	// Timeout 心跳超时时间（默认 15 秒）
	Timeout time.Duration

	// PhiThreshold Φ 阈值（默认 8.0）
	// Φ 值越大，故障判定越严格
	PhiThreshold float64

	// MinSamples 最小样本数（默认 10）
	// 收集足够样本后才进行 Φ 计算
	MinSamples int
}

// DefaultFailureDetectorConfig 返回默认配置
func DefaultFailureDetectorConfig() *FailureDetectorConfig {
	return &FailureDetectorConfig{
		Interval:     5 * time.Second,
		Timeout:      15 * time.Second,
		PhiThreshold: 8.0,
		MinSamples:   10,
	}
}

// NodeState 节点状态
type NodeState struct {
	// NodeID 节点ID
	NodeID string

	// LastHeartbeat 最后心跳时间
	LastHeartbeat time.Time

	// HeartbeatIntervals 心跳间隔样本（毫秒）
	HeartbeatIntervals []float64

	// Mean 心跳间隔均值
	Mean float64

	// Variance 心跳间隔方差
	Variance float64

	// StdDev 标准差
	StdDev float64

	// IsFailed 是否已判定为故障
	IsFailed atomic.Bool

	// FailCount 故障计数
	FailCount atomic.Int64
}

// FailureDetectorStats 故障检测统计信息
type FailureDetectorStats struct {
	// 探测总数
	PingsTotal atomic.Int64

	// 探测成功数
	PingsSuccess atomic.Int64

	// 探测失败数
	PingsFailed atomic.Int64

	// 检测到的故障数
	FailuresDetected atomic.Int64

	// 最后一次探测时间
	LastPingTime atomic.Value // time.Time
}

// NewFailureDetector 创建故障检测器
func NewFailureDetector(
	localNodeID string,
	transport transport.Transport,
	config *FailureDetectorConfig,
) (*FailureDetector, error) {
	if config == nil {
		config = DefaultFailureDetectorConfig()
	}

	if transport == nil {
		return nil, fmt.Errorf("transport 不能为空")
	}

	if localNodeID == "" {
		return nil, fmt.Errorf("localNodeID 不能为空")
	}

	detector := &FailureDetector{
		config:      config,
		localNodeID: localNodeID,
		transport:   transport,
		nodeStates:  make(map[string]*NodeState),
		stopCh:      make(chan struct{}),
		stats:       &FailureDetectorStats{},
	}

	// 初始化统计信息
	detector.stats.LastPingTime.Store(time.Time{})

	return detector, nil
}

// Start 启动故障检测器
func (fd *FailureDetector) Start() error {
	if !fd.started.CompareAndSwap(false, true) {
		return fmt.Errorf("故障检测器已经启动")
	}

	logging.WithFields(map[string]any{
		"node_id":        fd.localNodeID,
		"interval":       fd.config.Interval,
		"timeout":        fd.config.Timeout,
		"phi_threshold":  fd.config.PhiThreshold,
	}).Info("启动故障检测器")

	// 启动探测循环
	fd.stopWg.Add(1)
	go fd.detectionLoop()

	fd.started.Store(true)

	logging.Info("故障检测器启动成功")
	return nil
}

// Stop 停止故障检测器
func (fd *FailureDetector) Stop() error {
	if !fd.stopped.CompareAndSwap(false, true) {
		return nil // 已经停止
	}

	logging.Info("停止故障检测器...")

	// 关闭停止信号
	close(fd.stopCh)

	// 等待所有协程退出
	fd.stopWg.Wait()

	// 打印统计信息
	logging.WithFields(map[string]any{
		"pings_total":       fd.stats.PingsTotal.Load(),
		"pings_success":     fd.stats.PingsSuccess.Load(),
		"pings_failed":      fd.stats.PingsFailed.Load(),
		"failures_detected": fd.stats.FailuresDetected.Load(),
	}).Info("故障检测器统计")

	logging.Info("故障检测器已停止")
	return nil
}

// ========================================
// 核心检测逻辑
// ========================================

// detectionLoop 探测循环
func (fd *FailureDetector) detectionLoop() {
	defer fd.stopWg.Done()

	ticker := time.NewTicker(fd.config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			fd.detectNodes()

		case <-fd.stopCh:
			return
		}
	}
}

// detectNodes 检测所有节点
func (fd *FailureDetector) detectNodes() {
	fd.nodeStatesMu.RLock()
	nodes := make([]*NodeState, 0, len(fd.nodeStates))
	for _, state := range fd.nodeStates {
		nodes = append(nodes, state)
	}
	fd.nodeStatesMu.RUnlock()

	for _, state := range nodes {
		fd.pingNode(state.NodeID)
	}
}

// pingNode 探测节点
func (fd *FailureDetector) pingNode(nodeID string) {
	fd.stats.PingsTotal.Add(1)
	fd.stats.LastPingTime.Store(time.Now())

	logging.WithFields(map[string]any{
		"node_id": nodeID,
	}).Debug("探测节点")

	// TODO: 实际发送心跳探测消息
	// 这里应该通过 transport 发送心跳请求
	// 暂时使用超时判断

	fd.nodeStatesMu.Lock()
	state, exists := fd.nodeStates[nodeID]
	if !exists {
		state = &NodeState{
			NodeID:             nodeID,
			HeartbeatIntervals: make([]float64, 0, fd.config.MinSamples),
		}
		fd.nodeStates[nodeID] = state
	}
	fd.nodeStatesMu.Unlock()

	// 检查心跳超时
	now := time.Now()
	if !state.LastHeartbeat.IsZero() {
		elapsed := now.Sub(state.LastHeartbeat)

		if elapsed > fd.config.Timeout {
			fd.stats.PingsFailed.Add(1)

			// 计算 Φ 值
			phi := fd.computePhi(state, elapsed)

			logging.WithFields(map[string]any{
				"node_id":   nodeID,
				"elapsed":   elapsed,
				"phi":       phi,
				"threshold": fd.config.PhiThreshold,
			}).Warn("节点心跳超时")

			// Φ 值超过阈值，判定为故障
			if phi > fd.config.PhiThreshold {
				fd.markNodeFailed(state, nodeID)
			}
		} else {
			fd.stats.PingsSuccess.Add(1)
			state.IsFailed.Store(false)
		}
	}
}

// computePhi 计算 Φ 值
//
// Φ 值表示当前延迟偏离正常分布的程度
// Φ = (current_time - last_heartbeat - mean) / std_dev
func (fd *FailureDetector) computePhi(state *NodeState, elapsed time.Duration) float64 {
	var phi float64

	// 样本不足，使用简单的超时判断
	if len(state.HeartbeatIntervals) < fd.config.MinSamples {
		if elapsed > fd.config.Timeout {
			return 100.0 // 高 Φ 值表示可能故障
		}
		return 0.0
	}

	// 计算标准差
	if state.StdDev == 0 {
		return 0.0
	}

	// 计算 Φ 值
	elapsedMs := float64(elapsed.Milliseconds())
	deviation := elapsedMs - state.Mean
	phi = deviation / state.StdDev

	// Φ 值不能为负
	if phi < 0 {
		phi = 0
	}

	return phi
}

// markNodeFailed 标记节点故障
func (fd *FailureDetector) markNodeFailed(state *NodeState, nodeID string) {
	if state.IsFailed.CompareAndSwap(false, true) {
		state.FailCount.Add(1)
		fd.stats.FailuresDetected.Add(1)

		logging.WithFields(map[string]any{
			"node_id":    nodeID,
			"fail_count": state.FailCount.Load(),
		}).Warn("检测到节点故障")

		// 触发故障回调
		if fd.onNodeFailed != nil {
			go fd.onNodeFailed(nodeID)
		}
	}
}

// ========================================
// 心跳处理
// ========================================

// RecordHeartbeat 记录心跳
func (fd *FailureDetector) RecordHeartbeat(nodeID string) {
	now := time.Now()

	fd.nodeStatesMu.Lock()
	defer fd.nodeStatesMu.Unlock()

	state, exists := fd.nodeStates[nodeID]
	if !exists {
		state = &NodeState{
			NodeID:             nodeID,
			HeartbeatIntervals: make([]float64, 0, fd.config.MinSamples),
		}
		fd.nodeStates[nodeID] = state
	}

	// 计算心跳间隔
	if !state.LastHeartbeat.IsZero() {
		interval := now.Sub(state.LastHeartbeat).Milliseconds()
		state.HeartbeatIntervals = append(state.HeartbeatIntervals, float64(interval))

		// 限制样本数量
		maxSamples := fd.config.MinSamples * 2
		if len(state.HeartbeatIntervals) > maxSamples {
			state.HeartbeatIntervals = state.HeartbeatIntervals[len(state.HeartbeatIntervals)-maxSamples:]
		}

		// 更新统计信息
		fd.updateStatistics(state)
	}

	state.LastHeartbeat = now

	// 如果节点之前被标记为故障，现在恢复
	if state.IsFailed.Load() {
		state.IsFailed.Store(false)
		logging.WithField("node_id", nodeID).Info("节点从故障中恢复")
	}
}

// updateStatistics 更新统计信息
func (fd *FailureDetector) updateStatistics(state *NodeState) {
	intervals := state.HeartbeatIntervals
	if len(intervals) == 0 {
		return
	}

	// 计算均值
	sum := 0.0
	for _, interval := range intervals {
		sum += interval
	}
	state.Mean = sum / float64(len(intervals))

	// 计算方差
	if len(intervals) > 1 {
		sumSqDiff := 0.0
		for _, interval := range intervals {
			diff := interval - state.Mean
			sumSqDiff += diff * diff
		}
		state.Variance = sumSqDiff / float64(len(intervals)-1)
		state.StdDev = math.Sqrt(state.Variance)
	}
}

// ========================================
// 节点管理
// ========================================

// AddNode 添加节点
func (fd *FailureDetector) AddNode(nodeID string) {
	fd.nodeStatesMu.Lock()
	defer fd.nodeStatesMu.Unlock()

	if _, exists := fd.nodeStates[nodeID]; !exists {
		fd.nodeStates[nodeID] = &NodeState{
			NodeID:             nodeID,
			HeartbeatIntervals: make([]float64, 0, fd.config.MinSamples),
			LastHeartbeat:      time.Now(),
		}

		logging.WithField("node_id", nodeID).Info("添加节点到故障检测器")
	}
}

// RemoveNode 移除节点
func (fd *FailureDetector) RemoveNode(nodeID string) {
	fd.nodeStatesMu.Lock()
	defer fd.nodeStatesMu.Unlock()

	delete(fd.nodeStates, nodeID)

	logging.WithField("node_id", nodeID).Info("从故障检测器移除节点")
}

// GetNodeState 获取节点状态
func (fd *FailureDetector) GetNodeState(nodeID string) (*NodeState, error) {
	fd.nodeStatesMu.RLock()
	defer fd.nodeStatesMu.RUnlock()

	state, exists := fd.nodeStates[nodeID]
	if !exists {
		return nil, fmt.Errorf("节点不存在: %s", nodeID)
	}

	return state, nil
}

// ========================================
// 查询接口
// ========================================

// IsNodeAlive 检查节点是否存活
func (fd *FailureDetector) IsNodeAlive(nodeID string) bool {
	fd.nodeStatesMu.RLock()
	defer fd.nodeStatesMu.RUnlock()

	state, exists := fd.nodeStates[nodeID]
	if !exists {
		return false
	}

	return !state.IsFailed.Load() && time.Since(state.LastHeartbeat) < fd.config.Timeout
}

// GetStats 获取统计信息
func (fd *FailureDetector) GetStats() *FailureDetectorStats {
	return fd.stats
}

// SetFailureCallback 设置故障回调
func (fd *FailureDetector) SetFailureCallback(callback func(nodeID string)) {
	fd.onNodeFailed = callback
}
