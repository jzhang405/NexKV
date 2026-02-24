// Package service 定义领域服务接口
package service

import (
	"context"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// BroadcastProgress 广播进度追踪器接口
//
// 提供广播操作的进度追踪和回调机制
// 实现位于 infrastructure/rpc/broadcast_progress.go
type BroadcastProgress interface {
	// WaitFull 等待所有节点响应（包括失败的）
	WaitFull(ctx context.Context) error

	// WaitMajority 等待多数派（> N/2）节点响应
	WaitMajority(ctx context.Context) error

	// Stats 获取实时统计信息
	Stats() (success, failed, pending int)

	// SetCallback 设置进度回调（必须在开始之前设置）
	SetCallback(cb BroadcastListener)

	// EnableCallbacks 启用/禁用回调
	EnableCallbacks(enabled bool)

	// IsMajorityReached 检查是否已达到多数派
	IsMajorityReached() bool

	// IsFullDone 检查是否全部完成
	IsFullDone() bool

	// RecordSuccess 记录成功响应（由 RPC 实现调用）
	RecordSuccess(peer model.PeerID, resp model.Message)

	// RecordFailure 记录失败响应（由 RPC 实现调用）
	RecordFailure(peer model.PeerID, err error)
}

// BroadcastProgressFactory 创建 BroadcastProgress 的工厂函数类型
type BroadcastProgressFactory func(taskID string, targets []model.PeerID) BroadcastProgress
