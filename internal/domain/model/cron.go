// Package model 定义领域模型
package model

import "time"

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
