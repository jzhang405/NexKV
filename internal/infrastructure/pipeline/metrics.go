package pipeline

import (
	"sync"
	"time"
)

// Metrics Pipeline 指标
type Metrics struct {
	mu     sync.Mutex
	stages map[string]*stageMetrics
}

// newMetrics 创建 Metrics
func newMetrics() *Metrics {
	return &Metrics{
		stages: make(map[string]*stageMetrics),
	}
}

// getOrCreate 获取或创建 Stage 指标
func (m *Metrics) getOrCreate(name string) *stageMetrics {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.stages[name]; !ok {
		m.stages[name] = &stageMetrics{
			name:      name,
			processed: 0,
			failed:    0,
		}
	}

	return m.stages[name]
}

// Snapshot 获取快照
func (m *Metrics) Snapshot() *Stats {
	m.mu.Lock()
	defer m.mu.Unlock()

	stages := make(map[string]*StageStats, len(m.stages))
	for name, metrics := range m.stages {
		metrics.mu.Lock()
		avgLatency := time.Duration(0)
		if metrics.processed > 0 {
			avgLatency = time.Duration(metrics.totalLatency / metrics.processed)
		}

		stages[name] = &StageStats{
			Name:       metrics.name,
			Processed:  metrics.processed,
			Failed:     metrics.failed,
			AvgLatency: avgLatency,
			P50Latency: metrics.p50Latency,
			P95Latency: metrics.p95Latency,
			P99Latency: metrics.p99Latency,
		}
		metrics.mu.Unlock()
	}

	return &Stats{Stages: stages}
}

// now 获取当前时间（可覆盖用于测试）
func (m *Metrics) now() time.Time {
	return time.Now()
}

// stageMetrics Stage 指标
type stageMetrics struct {
	mu           sync.Mutex
	name         string
	processed    int64
	failed       int64
	totalLatency int64 // nanoseconds
	p50Latency   time.Duration
	p95Latency   time.Duration
	p99Latency   time.Duration
}

// Record 记录一次处理
func (m *stageMetrics) Record(latency time.Duration, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.processed++
	m.totalLatency += int64(latency)

	if err != nil {
		m.failed++
	}

	// TODO: 计算 P50/P95/P99
	// 简化实现：使用滑动窗口或 HDR Histogram
}

// Stats 统计信息
type Stats struct {
	Stages map[string]*StageStats
}

// StageStats 阶段统计
type StageStats struct {
	Name       string
	Processed  int64
	Failed     int64
	AvgLatency time.Duration
	P50Latency time.Duration
	P95Latency time.Duration
	P99Latency time.Duration
}
