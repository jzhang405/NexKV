// Package service 定义领域服务接口
package service

import (
	"context"
	"time"
)

// ==========================================
// CronJobProvider 定时任务提供者接口
// ==========================================

// CronSpec Cron 表达式
type CronSpec string

// CronJobStatus 定时任务状态
type CronJobStatus int32

const (
	CronJobStatusScheduled CronJobStatus = iota
	CronJobStatusRunning
	CronJobStatusPaused
	CronJobStatusStopped
)

// String 返回状态字符串
func (s CronJobStatus) String() string {
	switch s {
	case CronJobStatusScheduled:
		return "scheduled"
	case CronJobStatusRunning:
		return "running"
	case CronJobStatusPaused:
		return "paused"
	case CronJobStatusStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

// CronJobInfo 定时任务信息
type CronJobInfo struct {
	ID        string
	Name      string
	Spec      CronSpec
	Status    CronJobStatus
	NextRun   time.Time
	LastRun   *time.Time
	CreatedAt time.Time
}

// CronJobProvider 定时任务提供者接口
// 注意：由于 Go 接口限制，接口方法不能有类型参数
// 泛型方法通过独立辅助函数提供（由基础设施层实现）
type CronJobProvider interface {
	// ======================================
	// 生命周期
	// ======================================

	// Start 启动定时任务调度器
	Start()
	// Stop 停止定时任务调度器，返回 context 用于等待所有任务完成
	Stop() context.Context

	// ======================================
	// 基础方法（无参数）
	// ======================================

	// Register 注册定时任务（无参数）
	Register(spec CronSpec, name string, task func(context.Context)) (string, error)

	// RegisterWithPriority 注册带优先级的定时任务（无参数）
	RegisterWithPriority(spec CronSpec, name string, priority GoroutinePriority, task func(context.Context)) (string, error)

	// ======================================
	// 带参数方法（避免闭包陷阱）
	// ======================================

	// RegisterWithArg 注册带参数的定时任务（使用 any 类型）
	RegisterWithArg(spec CronSpec, name string, task func(context.Context, any), arg any) (string, error)

	// RegisterWithPriorityAndArg 注册带参数和优先级的定时任务（使用 any 类型）
	RegisterWithPriorityAndArg(spec CronSpec, name string, priority GoroutinePriority, task func(context.Context, any), arg any) (string, error)

	// ======================================
	// 任务控制
	// ======================================

	// Pause 暂停定时任务
	Pause(jobID string) error
	// Resume 恢复定时任务
	Resume(jobID string) error
	// Unregister 取消注册定时任务
	Unregister(jobID string) error

	// ======================================
	// 任务查询
	// ======================================

	// GetJob 获取定时任务信息
	GetJob(jobID string) (*CronJobInfo, error)
	// ListJobs 列出所有定时任务
	ListJobs() []*CronJobInfo
}
