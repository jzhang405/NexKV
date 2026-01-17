// Package clock 提供时钟服务接口
package clock

import (
	"context"
	"time"
)

// ClockService 时钟服务接口
//
// 提供 HLC 时间戳的生成、更新和比较功能
type ClockService interface {
	// Now 返回当前 HLC 时间戳
	Now() *HLC

	// Update 更新 HLC 时间戳（处理时钟同步）
	Update(eventTime int64, remoteHLC *HLC) *HLC

	// Compare 比较两个 HLC 时间戳
	Compare(hlc1, hlc2 *HLC) int
}

// ClockSync 时钟同步服务接口
//
// 通过 Gossip 协议同步不同节点的时钟
type ClockSync interface {
	// SyncWithPeer 与指定节点同步时钟
	SyncWithPeer(ctx context.Context, addr string) error

	// Start 启动时钟同步服务
	Start() error

	// Stop 停止时钟同步服务
	Stop() error
}

// ClockSyncConfig 时钟同步配置
type ClockSyncConfig struct {
	// SyncInterval 同步间隔
	SyncInterval time.Duration

	// MaxDrift 最大时间漂移（毫秒）
	MaxDrift int64

	// Timeout 同步超时
	Timeout time.Duration

	// RetryCount 重试次数
	RetryCount int
}

// DefaultClockSyncConfig 返回默认时钟同步配置
func DefaultClockSyncConfig() *ClockSyncConfig {
	return &ClockSyncConfig{
		SyncInterval: 10 * time.Second,
		MaxDrift:     100, // 100ms
		Timeout:      5 * time.Second,
		RetryCount:   3,
	}
}
