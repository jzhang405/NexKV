// Package model 定义领域模型
package model

// GoroutinePriority 任务优先级
type GoroutinePriority int

const (
	GoroutinePriorityCritical GoroutinePriority = iota
	GoroutinePriorityHigh
	GoroutinePriorityNormal
	GoroutinePriorityLow
)

// String 返回优先级字符串
func (p GoroutinePriority) String() string {
	switch p {
	case GoroutinePriorityCritical:
		return "critical"
	case GoroutinePriorityHigh:
		return "high"
	case GoroutinePriorityNormal:
		return "normal"
	case GoroutinePriorityLow:
		return "low"
	default:
		return "unknown"
	}
}

// ==========================================
// 统计和健康状态
// ==========================================

// GoroutinePoolStats 协程池统计信息
type GoroutinePoolStats struct {
	Total      int
	ByPriority map[GoroutinePriority]int
	Running    int
	Waiting    int
	Capacity   int
}

// GoroutineHealthStatus 健康状态
type GoroutineHealthStatus int

const (
	GoroutineHealthStatusHealthy GoroutineHealthStatus = iota
	GoroutineHealthStatusUnhealthy
)

// String 返回健康状态字符串
func (s GoroutineHealthStatus) String() string {
	switch s {
	case GoroutineHealthStatusHealthy:
		return "healthy"
	case GoroutineHealthStatusUnhealthy:
		return "unhealthy"
	default:
		return "unknown"
	}
}
