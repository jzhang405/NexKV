// Package wal provides Write-Ahead Logging (WAL) for crash recovery.
//
// WAL 提供两种模式：
// 1. 同步模式：直接调用 Append/Truncate 等方法
// 2. 异步模式：调用 AppendAsync/TruncateAsync 返回 Task[Result]
//
// 异步模式复用 v4 的 Task[Result] 架构，通过 Pipeline 提交任务。
package wal

import (
	"context"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// WAL Write-Ahead Log 接口
type WAL interface {
	// Append 追加一条日志记录（同步）
	// 返回 LSN（日志序列号）用于标识此条日志
	Append(entry *WALEntry) (LSN, error)

	// Sync 刷盘，确保持久化
	Sync() error

	// Recover 崩溃恢复，重放日志
	// 返回所有有效的日志条目
	Recover() ([]*WALEntry, error)

	// Truncate 截断日志，删除指定 LSN 之前的所有日志（包括指定 LSN）
	Truncate(lsn LSN) error

	// AppendAsync 异步追加日志（v4 模式）
	// 返回 Task[LSN]，用户可以通过 task.Wait(ctx) 等待完成
	AppendAsync(ctx context.Context, entry *WALEntry) model.Task[LSN]

	// TruncateAsync 异步截断日志（v4 模式）
	// 返回 Task[struct{}]，用户可以通过 task.Wait(ctx) 等待完成
	TruncateAsync(ctx context.Context, lsn LSN) model.Task[struct{}]

	// Close 关闭 WAL
	// 确保所有数据已刷盘，并释放资源
	Close() error
}

// WALStats WAL 统计信息
type WALStats struct {
	// CurrentLSN 当前 LSN
	CurrentLSN LSN
	// TotalEntries 总日志条目数
	TotalEntries int64
	// TotalBytes 总字节数
	TotalBytes int64
	// SegmentCount 分段数量
	SegmentCount int
	// SyncCount 同步次数
	SyncCount int64
}
