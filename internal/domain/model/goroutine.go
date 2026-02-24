// Package model 定义领域模型
package model

// TaskPriority 任务优先级（业务化命名，原 GoroutinePriority）
type TaskPriority int

const (
	TaskPriorityCritical TaskPriority = iota
	TaskPriorityHigh
	TaskPriorityNormal
	TaskPriorityLow
)

// GoroutinePriority 是 TaskPriority 的别名，保持向后兼容
// Deprecated: 使用 TaskPriority 代替
type GoroutinePriority = TaskPriority

// 常量别名（向后兼容）
const (
	GoroutinePriorityCritical = TaskPriorityCritical
	GoroutinePriorityHigh     = TaskPriorityHigh
	GoroutinePriorityNormal   = TaskPriorityNormal
	GoroutinePriorityLow      = TaskPriorityLow
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

// TaskPoolStats 任务池统计信息（业务化命名，原 GoroutinePoolStats）
type TaskPoolStats struct {
	Total      int
	ByPriority map[TaskPriority]int
	Running    int
	Waiting    int
	Capacity   int
}

// GoroutinePoolStats 是 TaskPoolStats 的别名，保持向后兼容
// Deprecated: 使用 TaskPoolStats 代替
type GoroutinePoolStats = TaskPoolStats

// TaskHealthStatus 健康状态（业务化命名，原 GoroutineHealthStatus）
type TaskHealthStatus int

const (
	TaskHealthStatusHealthy TaskHealthStatus = iota
	TaskHealthStatusUnhealthy
)

// GoroutineHealthStatus 是 TaskHealthStatus 的别名，保持向后兼容
// Deprecated: 使用 TaskHealthStatus 代替
type GoroutineHealthStatus = TaskHealthStatus

// 常量别名（向后兼容）
const (
	GoroutineHealthStatusHealthy   = TaskHealthStatusHealthy
	GoroutineHealthStatusUnhealthy = TaskHealthStatusUnhealthy
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
