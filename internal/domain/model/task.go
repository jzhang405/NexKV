// Package model 定义领域模型
package model

// TaskPriority 任务优先级
type TaskPriority int

const (
	TaskPriorityCritical TaskPriority = iota
	TaskPriorityHigh
	TaskPriorityNormal
	TaskPriorityLow
)

// String 返回优先级字符串
func (p TaskPriority) String() string {
	switch p {
	case TaskPriorityCritical:
		return "critical"
	case TaskPriorityHigh:
		return "high"
	case TaskPriorityNormal:
		return "normal"
	case TaskPriorityLow:
		return "low"
	default:
		return "unknown"
	}
}

// ==========================================
// 统计和健康状态
// ==========================================

// TaskPoolStats 任务池统计信息
type TaskPoolStats struct {
	Total      int
	ByPriority map[TaskPriority]int
	Running    int
	Waiting    int
	Capacity   int
}

// TaskHealthStatus 健康状态
type TaskHealthStatus int

const (
	TaskHealthStatusHealthy TaskHealthStatus = iota
	TaskHealthStatusUnhealthy
)

// String 返回健康状态字符串
func (s TaskHealthStatus) String() string {
	switch s {
	case TaskHealthStatusHealthy:
		return "healthy"
	case TaskHealthStatusUnhealthy:
		return "unhealthy"
	default:
		return "unknown"
	}
}
