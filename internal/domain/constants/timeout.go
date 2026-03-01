// Package constants 提供跨包共享的常量定义
package constants

import "time"

// ==========================================
// RPC 超时配置
// ==========================================

const (
	// DefaultCallTimeout 默认单播调用超时
	DefaultCallTimeout = 30 * time.Second

	// DefaultBroadcastTimeout 默认广播调用超时
	DefaultBroadcastTimeout = 60 * time.Second

	// DefaultConnectTimeout 默认连接超时
	DefaultConnectTimeout = 10 * time.Second

	// DefaultAcceptStreamTimeout 默认接受流超时
	DefaultAcceptStreamTimeout = 30 * time.Second

	// DefaultRetryBackoff 默认重试退避时间
	DefaultRetryBackoff = time.Second
)

// ==========================================
// 超时配置（毫秒）
// ==========================================

const (
	// DefaultCallTimeoutMs 默认单播调用超时（毫秒）
	DefaultCallTimeoutMs = int64(30000)

	// DefaultBroadcastTimeoutMs 默认广播调用超时（毫秒）
	DefaultBroadcastTimeoutMs = int64(60000)
)

// ==========================================
// 测试超时配置
// ==========================================

const (
	// DefaultTestTimeout 默认测试超时
	DefaultTestTimeout = 30 * time.Second

	// LongTestTimeout 长时间测试超时
	LongTestTimeout = 60 * time.Second

	// NetworkOperationTimeout 网络操作超时
	NetworkOperationTimeout = 5 * time.Second
)
