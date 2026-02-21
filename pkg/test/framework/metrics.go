package framework

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// OperationType 操作类型
type OperationType string

const (
	// OpTypeGet Get 操作
	OpTypeGet OperationType = "get"
	// OpTypePut Put 操作
	OpTypePut OperationType = "put"
	// OpTypeDelete Delete 操作
	OpTypeDelete OperationType = "delete"
	// OpTypeScan Scan 操作
	OpTypeScan OperationType = "scan"
	// OpTypeBatch 批量操作
	OpTypeBatch OperationType = "batch"
)

// OperationMetrics 单个操作的指标
type OperationMetrics struct {
	mu sync.RWMutex

	// 操作类型
	OpType OperationType

	// 计数器
	TotalCount   int64
	SuccessCount int64
	ErrorCount   int64

	// 延迟统计（纳秒）
	TotalLatency int64
	MinLatency   int64
	MaxLatency   int64

	// 延迟直方图（用于百分位计算）
	latencies []int64

	// 吞吐量统计
	StartTime time.Time
	EndTime   time.Time
}

// NewOperationMetrics 创建操作指标
func NewOperationMetrics(opType OperationType) *OperationMetrics {
	return &OperationMetrics{
		OpType:     opType,
		latencies:  make([]int64, 0),
		MinLatency: -1, // -1 表示未初始化
	}
}

// Record 记录一次操作
func (m *OperationMetrics) Record(latency time.Duration, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	latencyNs := latency.Nanoseconds()

	m.TotalCount++
	m.TotalLatency += latencyNs

	if err != nil {
		m.ErrorCount++
	} else {
		m.SuccessCount++
	}

	// 更新最小/最大延迟
	if m.MinLatency < 0 || latencyNs < m.MinLatency {
		m.MinLatency = latencyNs
	}
	if latencyNs > m.MaxLatency {
		m.MaxLatency = latencyNs
	}

	// 记录延迟用于百分位计算
	m.latencies = append(m.latencies, latencyNs)
}

// GetLatencyPercentile 计算延迟百分位
// percentile 应该在 0.0 到 1.0 之间
func (m *OperationMetrics) GetLatencyPercentile(percentile float64) time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.latencies) == 0 {
		return 0
	}

	// 复制并排序
	sorted := make([]int64, len(m.latencies))
	copy(sorted, m.latencies)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})

	// 计算百分位索引
	index := int(float64(len(sorted)-1) * percentile)
	if index >= len(sorted) {
		index = len(sorted) - 1
	}

	return time.Duration(sorted[index])
}

// GetAverageLatency 获取平均延迟
func (m *OperationMetrics) GetAverageLatency() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.TotalCount == 0 {
		return 0
	}

	return time.Duration(m.TotalLatency / m.TotalCount)
}

// GetErrorRate 获取错误率
func (m *OperationMetrics) GetErrorRate() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.TotalCount == 0 {
		return 0
	}

	return float64(m.ErrorCount) / float64(m.TotalCount)
}

// GetThroughput 获取吞吐量（ops/s）
func (m *OperationMetrics) GetThroughput() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.StartTime.IsZero() || m.EndTime.IsZero() {
		return 0
	}

	duration := m.EndTime.Sub(m.StartTime).Seconds()
	if duration <= 0 {
		return 0
	}

	return float64(m.TotalCount) / duration
}

// Start 开始计时
func (m *OperationMetrics) Start() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.StartTime = time.Now()
}

// Stop 停止计时
func (m *OperationMetrics) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.EndTime = time.Now()
}

// Snapshot 获取指标快照
func (m *OperationMetrics) Snapshot() *OperationMetricsSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return &OperationMetricsSnapshot{
		OpType:       m.OpType,
		TotalCount:   m.TotalCount,
		SuccessCount: m.SuccessCount,
		ErrorCount:   m.ErrorCount,
		AvgLatency:   m.GetAverageLatency(),
		MinLatency:   time.Duration(m.MinLatency),
		MaxLatency:   time.Duration(m.MaxLatency),
		P50Latency:   m.GetLatencyPercentile(0.50),
		P95Latency:   m.GetLatencyPercentile(0.95),
		P99Latency:   m.GetLatencyPercentile(0.99),
		ErrorRate:    m.GetErrorRate(),
		Throughput:   m.GetThroughput(),
	}
}

// OperationMetricsSnapshot 操作指标快照（不可变）
type OperationMetricsSnapshot struct {
	OpType       OperationType
	TotalCount   int64
	SuccessCount int64
	ErrorCount   int64
	AvgLatency   time.Duration
	MinLatency   time.Duration
	MaxLatency   time.Duration
	P50Latency   time.Duration
	P95Latency   time.Duration
	P99Latency   time.Duration
	ErrorRate    float64
	Throughput   float64
}

// MetricsCollector 指标收集器接口
type MetricsCollector interface {
	// RecordOperation 记录操作
	RecordOperation(opType OperationType, latency time.Duration, err error)

	// GetMetrics 获取指定操作类型的指标
	GetMetrics(opType OperationType) *OperationMetrics

	// GetAllMetrics 获取所有操作类型的指标
	GetAllMetrics() map[OperationType]*OperationMetrics

	// Snapshot 获取所有指标的快照
	Snapshot() *MetricsSnapshot

	// Reset 重置所有指标
	Reset()
}

// DefaultMetricsCollector 默认指标收集器
type DefaultMetricsCollector struct {
	mu        sync.RWMutex
	metrics   map[OperationType]*OperationMetrics
	startTime time.Time
}

// NewMetricsCollector 创建指标收集器
func NewMetricsCollector() *DefaultMetricsCollector {
	return &DefaultMetricsCollector{
		metrics:   make(map[OperationType]*OperationMetrics),
		startTime: time.Now(),
	}
}

// RecordOperation 记录操作
func (c *DefaultMetricsCollector) RecordOperation(opType OperationType, latency time.Duration, err error) {
	c.mu.RLock()
	metrics, exists := c.metrics[opType]
	c.mu.RUnlock()

	if !exists {
		c.mu.Lock()
		// 双重检查
		if metrics, exists = c.metrics[opType]; !exists {
			metrics = NewOperationMetrics(opType)
			metrics.Start()
			c.metrics[opType] = metrics
		}
		c.mu.Unlock()
	}

	metrics.Record(latency, err)
}

// GetMetrics 获取指定操作类型的指标
func (c *DefaultMetricsCollector) GetMetrics(opType OperationType) *OperationMetrics {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.metrics[opType]
}

// GetAllMetrics 获取所有操作类型的指标
func (c *DefaultMetricsCollector) GetAllMetrics() map[OperationType]*OperationMetrics {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make(map[OperationType]*OperationMetrics, len(c.metrics))
	for k, v := range c.metrics {
		result[k] = v
	}
	return result
}

// Snapshot 获取所有指标的快照
func (c *DefaultMetricsCollector) Snapshot() *MetricsSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// 停止所有计时
	for _, m := range c.metrics {
		m.Stop()
	}

	snapshot := &MetricsSnapshot{
		Timestamp:  time.Now(),
		Duration:   time.Since(c.startTime),
		Operations: make(map[OperationType]*OperationMetricsSnapshot),
	}

	for opType, metrics := range c.metrics {
		snapshot.Operations[opType] = metrics.Snapshot()
	}

	// 计算汇总指标
	snapshot.calculateTotals()

	return snapshot
}

// Reset 重置所有指标
func (c *DefaultMetricsCollector) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.metrics = make(map[OperationType]*OperationMetrics)
	c.startTime = time.Now()
}

// MetricsSnapshot 指标快照
type MetricsSnapshot struct {
	Timestamp time.Time
	Duration  time.Duration

	// 各操作类型的指标
	Operations map[OperationType]*OperationMetricsSnapshot

	// 汇总指标
	TotalOperations   int64
	TotalErrors       int64
	OverallErrorRate  float64
	OverallThroughput float64
}

// calculateTotals 计算汇总指标
func (s *MetricsSnapshot) calculateTotals() {
	var totalCount, totalErrors int64

	for _, op := range s.Operations {
		totalCount += op.TotalCount
		totalErrors += op.ErrorCount
	}

	s.TotalOperations = totalCount
	s.TotalErrors = totalErrors

	if totalCount > 0 {
		s.OverallErrorRate = float64(totalErrors) / float64(totalCount)
	}

	if s.Duration.Seconds() > 0 {
		s.OverallThroughput = float64(totalCount) / s.Duration.Seconds()
	}
}

// String 返回指标快照的字符串表示
func (s *MetricsSnapshot) String() string {
	var result string

	result += "=== Metrics Snapshot ===\n"
	result += fmt.Sprintf("Timestamp: %s\n", s.Timestamp.Format(time.RFC3339))
	result += fmt.Sprintf("Duration: %v\n", s.Duration)
	result += fmt.Sprintf("Total Operations: %d\n", s.TotalOperations)
	result += fmt.Sprintf("Total Errors: %d\n", s.TotalErrors)
	result += fmt.Sprintf("Overall Error Rate: %.2f%%\n", s.OverallErrorRate*100)
	result += fmt.Sprintf("Overall Throughput: %.2f ops/s\n", s.OverallThroughput)
	result += "\n"

	for opType, op := range s.Operations {
		result += fmt.Sprintf("--- %s ---\n", opType)
		result += fmt.Sprintf("  Count: %d (success: %d, error: %d)\n",
			op.TotalCount, op.SuccessCount, op.ErrorCount)
		result += fmt.Sprintf("  Latency: avg=%v, min=%v, max=%v\n",
			op.AvgLatency, op.MinLatency, op.MaxLatency)
		result += fmt.Sprintf("  Percentiles: P50=%v, P95=%v, P99=%v\n",
			op.P50Latency, op.P95Latency, op.P99Latency)
		result += fmt.Sprintf("  Error Rate: %.2f%%\n", op.ErrorRate*100)
		result += fmt.Sprintf("  Throughput: %.2f ops/s\n", op.Throughput)
	}

	return result
}

// LatencyHistogram 延迟直方图
type LatencyHistogram struct {
	mu      sync.RWMutex
	buckets []LatencyBucket
	counts  []int64
	total   int64
}

// LatencyBucket 延迟桶
type LatencyBucket struct {
	Min time.Duration
	Max time.Duration
}

// NewLatencyHistogram 创建延迟直方图
// buckets 定义各桶的边界
func NewLatencyHistogram(buckets []LatencyBucket) *LatencyHistogram {
	return &LatencyHistogram{
		buckets: buckets,
		counts:  make([]int64, len(buckets)+1), // +1 用于超出最大桶的值
	}
}

// DefaultLatencyBuckets 默认延迟桶（适用于大多数场景）
func DefaultLatencyBuckets() []LatencyBucket {
	return []LatencyBucket{
		{Min: 0, Max: 100 * time.Microsecond},
		{Min: 100 * time.Microsecond, Max: 500 * time.Microsecond},
		{Min: 500 * time.Microsecond, Max: time.Millisecond},
		{Min: time.Millisecond, Max: 5 * time.Millisecond},
		{Min: 5 * time.Millisecond, Max: 10 * time.Millisecond},
		{Min: 10 * time.Millisecond, Max: 50 * time.Millisecond},
		{Min: 50 * time.Millisecond, Max: 100 * time.Millisecond},
		{Min: 100 * time.Millisecond, Max: 500 * time.Millisecond},
		{Min: 500 * time.Millisecond, Max: time.Second},
		{Min: time.Second, Max: 5 * time.Second},
	}
}

// Record 记录延迟
func (h *LatencyHistogram) Record(latency time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.total++

	// 找到合适的桶
	for i, bucket := range h.buckets {
		if latency >= bucket.Min && latency < bucket.Max {
			h.counts[i]++
			return
		}
	}

	// 超出最大桶
	h.counts[len(h.buckets)]++
}

// GetCounts 获取各桶计数
func (h *LatencyHistogram) GetCounts() []int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()

	counts := make([]int64, len(h.counts))
	copy(counts, h.counts)
	return counts
}

// GetTotal 获取总计数
func (h *LatencyHistogram) GetTotal() int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.total
}

// GetPercentile 获取百分位延迟
func (h *LatencyHistogram) GetPercentile(percentile float64) time.Duration {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.total == 0 {
		return 0
	}

	target := int64(float64(h.total) * percentile)
	var cumulative int64

	for i, count := range h.counts {
		cumulative += count
		if cumulative >= target {
			if i < len(h.buckets) {
				// 返回桶的中值
				return (h.buckets[i].Min + h.buckets[i].Max) / 2
			}
			// 超出最大桶
			return h.buckets[len(h.buckets)-1].Max
		}
	}

	return h.buckets[len(h.buckets)-1].Max
}

// Reset 重置直方图
func (h *LatencyHistogram) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()

	for i := range h.counts {
		h.counts[i] = 0
	}
	h.total = 0
}
