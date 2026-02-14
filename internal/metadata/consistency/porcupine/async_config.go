// Package porcupine 提供 Porcupine 线性一致性验证集成
// 本文件定义异步记录配置，供 hooks 和 runtime 共享
package porcupine

// ==================== 异步记录配置 ====================

// AsyncRecordConfig 异步记录配置
// 用于 hooks 包的异步操作记录
type AsyncRecordConfig struct {
	// Enabled 是否启用异步记录
	Enabled bool

	// BufferSize 异步队列大小（默认 10000）
	BufferSize int

	// DropOnFull 队列满时是否丢弃（true）或阻塞（false）
	// 推荐使用 true 避免阻塞关键路径
	DropOnFull bool
}

// DefaultAsyncRecordConfig 默认异步记录配置
func DefaultAsyncRecordConfig() AsyncRecordConfig {
	return AsyncRecordConfig{
		Enabled:    true,
		BufferSize: 10000,
		DropOnFull: true, // 关键：不阻塞业务
	}
}
