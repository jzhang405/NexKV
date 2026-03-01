// Package constants 提供跨包共享的常量定义
package constants

// ==========================================
// 限流配置
// ==========================================

const (
	// DefaultRequestsPerSecond 默认每秒请求数
	DefaultRequestsPerSecond = 1000

	// DefaultBurst 默认突发流量容量
	DefaultBurst = 100

	// MaxRequestsPerSecond 最大每秒请求数 (10万 QPS)
	MaxRequestsPerSecond = 100000

	// MaxBurst 最大突发流量容量
	MaxBurst = 10000
)
