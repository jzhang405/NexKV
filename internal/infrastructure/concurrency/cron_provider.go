// Package concurrency 提供协程池和定时任务管理
package concurrency

import (
	"github.com/jzhang405/NexKV/internal/domain/service"
)

// ==========================================
// 类型别名（向后兼容）
// ==========================================

// CronSpec Cron 表达式（类型别名）
// 实际类型定义在 domain/service/cron.go
type CronSpec = service.CronSpec

// CronJobStatus 定时任务状态（类型别名）
type CronJobStatus = service.CronJobStatus

// CronJobInfo 定时任务信息（类型别名）
type CronJobInfo = service.CronJobInfo

// CronJobProvider 定时任务提供者接口（类型别名）
type CronJobProvider = service.CronJobProvider

// ==========================================
// 状态常量（向后兼容）
// ==========================================

const (
	// CronJobStatusScheduled 已调度
	CronJobStatusScheduled = service.CronJobStatusScheduled
	// CronJobStatusRunning 运行中
	CronJobStatusRunning = service.CronJobStatusRunning
	// CronJobStatusPaused 已暂停
	CronJobStatusPaused = service.CronJobStatusPaused
	// CronJobStatusStopped 已停止
	CronJobStatusStopped = service.CronJobStatusStopped
)
