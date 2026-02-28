// Package model 定义领域模型
package model

// TaskPriority 任务优先级
// 遵循 Unix 传统定义 10 级优先级（0 最高，9 最低）
type TaskPriority int

const (
	// Unix 传统：0 最高，9 最低
	TaskPriorityCritical   TaskPriority = iota // 0级：核心优先级（最高）- 如系统关键任务、心跳检测
	TaskPriorityHigh                           // 1级：高优先级 - 如实时数据同步、用户核心操作
	TaskPriorityUrgent                         // 2级：紧急优先级 - 如交易结算、订单处理
	TaskPriorityImportant                      // 3级：重要优先级 - 如业务逻辑计算
	TaskPriorityNormalHigh                     // 4级：次高正常优先级 - 如高频查询
	TaskPriorityNormal                         // 5级：正常优先级 - 常规业务操作（默认）
	TaskPriorityNormalLow                      // 6级：次低正常优先级 - 如非实时统计
	TaskPriorityLow                            // 7级：低优先级 - 如日志批量处理
	TaskPriorityBackground                     // 8级：后台优先级 - 如数据归档
	TaskPriorityIdle                           // 9级：空闲优先级（最低）- 如资源清理、冷数据同步
)

// String 返回优先级字符串
func (p TaskPriority) String() string {
	switch p {
	case TaskPriorityCritical:
		return "critical"
	case TaskPriorityHigh:
		return "high"
	case TaskPriorityUrgent:
		return "urgent"
	case TaskPriorityImportant:
		return "important"
	case TaskPriorityNormalHigh:
		return "normal-high"
	case TaskPriorityNormal:
		return "normal"
	case TaskPriorityNormalLow:
		return "normal-low"
	case TaskPriorityLow:
		return "low"
	case TaskPriorityBackground:
		return "background"
	case TaskPriorityIdle:
		return "idle"
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
