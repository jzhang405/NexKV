// Package fault 提供故障检测器实现
//
// DEGR-1: Phi Accrual 故障检测器
//
// 核心功能：
//   - Phi Accrual 算法：基于正态分布的概率故障检测
//   - 心跳统计：记录心跳间隔历史，计算均值和方差
//   - Phi 值计算：-log(1 - CDF(timeSinceLast))，值越大表示故障概率越高
//   - 阈值判定：Phi > threshold 判定为故障
//
// 参考实现：
//   - Cassandra: https://github.com/apache/cassandra/blob/trunk/src/java/org/apache/cassandra/gms/FailureDetector.java
//   - Akka: https://github.com/akka/akka/blob/main/akka-cluster/src/main/scala/akka/cluster/PhiAccrualFailureDetector.scala
package fault

import (
	"math"
	"sync"
	"time"
)

// 默认参数
const (
	// DefaultThreshold 默认 Phi 阈值（业界标准值）
	// Cassandra 和 Akka 都使用 8.0
	DefaultThreshold = 8.0

	// DefaultMinStdDev 默认最小标准差
	// 避免样本不足时方差为 0
	DefaultMinStdDev = 500 * time.Millisecond

	// DefaultMinSamples 最小样本数
	// 样本不足时不进行故障判定
	DefaultMinSamples = 10

	// DefaultMaxSampleSize 最大样本数
	// 限制历史记录大小，避免内存无限增长
	DefaultMaxSampleSize = 1000
)

// HeartbeatStats 心跳统计
type HeartbeatStats struct {
	LastHeartbeat time.Time       // 最后心跳时间
	Intervals     []time.Duration // 心跳间隔历史
	Mean          time.Duration   // 平均间隔
	Variance      float64         // 方差（单位：纳秒²）
}

// PhiAccrualDetector Phi Accrual 故障检测器
//
// 基于 Phi Accrual 故障检测算法，通过统计心跳间隔的正态分布，
// 计算当前时间距最后心跳的 "惊讶程度"（Phi 值）。
//
// Phi 值含义：
//   - Phi = 1: 约 10% 概率是误判
//   - Phi = 2: 约 1% 概率是误判
//   - Phi = 3: 约 0.1% 概率是误判
//   - Phi = 8: 约 0.00000001% 概率是误判
type PhiAccrualDetector struct {
	mu             sync.RWMutex
	localNodeID    string
	heartbeatStats map[string]*HeartbeatStats // 节点 ID -> 心跳统计

	// 配置参数
	threshold     float64       // Phi 阈值
	minStdDev     time.Duration // 最小标准差
	minSamples    int           // 最小样本数
	maxSampleSize int           // 最大样本数
}

// PhiAccrualConfig Phi Accrual 检测器配置
type PhiAccrualConfig struct {
	// LocalNodeID 本地节点 ID
	LocalNodeID string

	// Threshold Phi 阈值（默认 8.0）
	Threshold float64

	// MinStdDev 最小标准差（默认 500ms）
	MinStdDev time.Duration

	// MinSamples 最小样本数（默认 10）
	MinSamples int

	// MaxSampleSize 最大样本数（默认 1000）
	MaxSampleSize int
}

// NewPhiAccrualDetector 创建 Phi Accrual 故障检测器
func NewPhiAccrualDetector(config *PhiAccrualConfig) *PhiAccrualDetector {
	if config == nil {
		config = &PhiAccrualConfig{}
	}

	threshold := config.Threshold
	if threshold <= 0 {
		threshold = DefaultThreshold
	}

	minStdDev := config.MinStdDev
	if minStdDev <= 0 {
		minStdDev = DefaultMinStdDev
	}

	minSamples := config.MinSamples
	if minSamples <= 0 {
		minSamples = DefaultMinSamples
	}

	maxSampleSize := config.MaxSampleSize
	if maxSampleSize <= 0 {
		maxSampleSize = DefaultMaxSampleSize
	}

	return &PhiAccrualDetector{
		localNodeID:    config.LocalNodeID,
		heartbeatStats: make(map[string]*HeartbeatStats),
		threshold:      threshold,
		minStdDev:      minStdDev,
		minSamples:     minSamples,
		maxSampleSize:  maxSampleSize,
	}
}

// RecordHeartbeat 记录心跳
//
// 当收到来自 nodeID 的心跳时调用此方法。
// 会更新心跳统计信息，包括间隔历史、均值和方差。
func (d *PhiAccrualDetector) RecordHeartbeat(nodeID string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	stats := d.heartbeatStats[nodeID]

	if stats == nil {
		stats = &HeartbeatStats{
			Intervals: make([]time.Duration, 0, d.maxSampleSize),
		}
		d.heartbeatStats[nodeID] = stats
	}

	if !stats.LastHeartbeat.IsZero() {
		interval := now.Sub(stats.LastHeartbeat)

		// 添加到历史记录
		stats.Intervals = append(stats.Intervals, interval)

		// 限制历史记录大小
		if len(stats.Intervals) > d.maxSampleSize {
			stats.Intervals = stats.Intervals[1:]
		}

		// 更新统计信息
		d.updateStats(stats)
	}

	stats.LastHeartbeat = now
}

// updateStats 更新心跳统计信息
func (d *PhiAccrualDetector) updateStats(stats *HeartbeatStats) {
	if len(stats.Intervals) == 0 {
		return
	}

	// 计算均值
	var sum time.Duration
	for _, interval := range stats.Intervals {
		sum += interval
	}
	stats.Mean = sum / time.Duration(len(stats.Intervals))

	// 计算方差（使用纳秒单位以避免精度丢失）
	if len(stats.Intervals) < 2 {
		stats.Variance = float64(d.minStdDev * d.minStdDev)
		return
	}

	var varianceSum float64
	meanNs := float64(stats.Mean)
	for _, interval := range stats.Intervals {
		diff := float64(interval) - meanNs
		varianceSum += diff * diff
	}
	stats.Variance = varianceSum / float64(len(stats.Intervals)-1)

	// 确保方差不低于最小标准差的平方
	minVariance := float64(d.minStdDev * d.minStdDev)
	if stats.Variance < minVariance {
		stats.Variance = minVariance
	}
}

// Phi 计算 Phi 值
//
// Phi 值表示当前时间距最后心跳的 "惊讶程度"。
// Phi 值越大，表示节点越可能已经故障。
//
// 计算公式：Phi = -log10(1 - CDF(timeSinceLast))
// 其中 CDF 是累积分布函数。
func (d *PhiAccrualDetector) Phi(nodeID string) float64 {
	d.mu.RLock()
	defer d.mu.RUnlock()

	stats := d.heartbeatStats[nodeID]
	if stats == nil || stats.LastHeartbeat.IsZero() {
		return 0 // 无记录，不判定为故障
	}

	// 样本不足时不判定故障
	if len(stats.Intervals) < d.minSamples {
		return 0
	}

	timeSinceLast := time.Since(stats.LastHeartbeat)

	// 使用正态分布计算 Phi
	// Phi = -log10(1 - CDF(timeSinceLast))
	// CDF(x) = 0.5 * (1 + erf((x - mean) / (stdDev * sqrt(2))))

	mean := float64(stats.Mean)
	stdDev := math.Sqrt(stats.Variance)

	// 标准化
	z := (float64(timeSinceLast) - mean) / stdDev

	// 计算 CDF（使用 erf 近似）
	// erf(z) ≈ 1 - 1/(1 + a1*z + a2*z² + a3*z³ + a4*z⁴)^4
	cdf := 0.5 * (1 + erf(z))

	// 避免 log(0)
	if cdf >= 1.0 {
		return 100 // 极高 Phi 值
	}

	// Phi = -log10(1 - CDF)
	phi := -math.Log10(1 - cdf)

	return phi
}

// erf 误差函数（使用标准库实现）
var erf = math.Erf

// IsNodeFailed 判断节点是否故障
//
// 当 Phi 值超过阈值时，判定节点为故障状态。
func (d *PhiAccrualDetector) IsNodeFailed(nodeID string) bool {
	return d.Phi(nodeID) > d.threshold
}

// Reset 重置节点状态
//
// 当节点重新加入或需要清除历史记录时调用。
func (d *PhiAccrualDetector) Reset(nodeID string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	delete(d.heartbeatStats, nodeID)
}

// ResetAll 重置所有节点状态
func (d *PhiAccrualDetector) ResetAll() {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.heartbeatStats = make(map[string]*HeartbeatStats)
}

// GetStats 获取节点心跳统计信息（用于监控和调试）
func (d *PhiAccrualDetector) GetStats(nodeID string) *HeartbeatStats {
	d.mu.RLock()
	defer d.mu.RUnlock()

	stats := d.heartbeatStats[nodeID]
	if stats == nil {
		return nil
	}

	// 返回副本，避免外部修改
	intervalsCopy := make([]time.Duration, len(stats.Intervals))
	copy(intervalsCopy, stats.Intervals)

	return &HeartbeatStats{
		LastHeartbeat: stats.LastHeartbeat,
		Mean:          stats.Mean,
		Variance:      stats.Variance,
		Intervals:     intervalsCopy,
	}
}

// GetAllNodeIDs 获取所有已跟踪的节点 ID
func (d *PhiAccrualDetector) GetAllNodeIDs() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	ids := make([]string, 0, len(d.heartbeatStats))
	for id := range d.heartbeatStats {
		ids = append(ids, id)
	}
	return ids
}

// GetThreshold 获取当前阈值
func (d *PhiAccrualDetector) GetThreshold() float64 {
	return d.threshold
}

// SetThreshold 设置阈值（支持运行时调整）
func (d *PhiAccrualDetector) SetThreshold(threshold float64) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if threshold > 0 {
		d.threshold = threshold
	}
}

// GetMinSamples 获取最小样本数
func (d *PhiAccrualDetector) GetMinSamples() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.minSamples
}

// SetMinSamples 设置最小样本数
func (d *PhiAccrualDetector) SetMinSamples(minSamples int) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if minSamples > 0 {
		d.minSamples = minSamples
	}
}
