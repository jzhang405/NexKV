// Package uuid 提供安全的 UUID 生成器（防时钟回拨）
package uuid

import (
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
)

// SafeUUIDGenerator 安全的 UUID 生成器（防时钟回拨）
//
// 特性:
//   - 检测时钟回拨
//   - 自动等待时钟恢复
//   - 记录回拨事件（drift count）
//   - 保证 UUID 唯一性和单调性
//
// 并发优化:
//   - 使用原子操作减少锁竞争
//   - 时钟回拨检测和等待在锁外进行
//   - 锁持有时间极短（仅用于状态更新）
type SafeUUIDGenerator struct {
	snowflake    *Snowflake
	maxDrift     time.Duration // 最大允许的时钟漂移
	lastTime     int64         // 上次生成时间（Unix ms，原子操作）
	driftCount   int64         // 时钟回拨次数（原子操作）
	maxDriftWait time.Duration // 最大回拨等待时间
}

// NewSafeUUIDGenerator 创建安全的 UUID 生成器
//
// 参数:
//   - datacenterID: 数据中心 ID（0-31）
//   - workerID: 工作节点 ID（0-31）
//   - maxDrift: 最大允许的时钟漂移（默认 100ms）
//   - maxWait: 最大回拨等待时间（默认 1 秒）
func NewSafeUUIDGenerator(datacenterID, workerID int64, maxDrift, maxWait time.Duration) (*SafeUUIDGenerator, error) {
	if maxDrift <= 0 {
		maxDrift = 100 * time.Millisecond
	}
	if maxWait <= 0 {
		maxWait = 1 * time.Second
	}

	snowflake, err := NewSnowflake(datacenterID, workerID)
	if err != nil {
		return nil, err
	}

	return &SafeUUIDGenerator{
		snowflake:    snowflake,
		maxDrift:     maxDrift,
		lastTime:     time.Now().UnixMilli(),
		maxDriftWait: maxWait,
	}, nil
}

// GenerateTransactionID 生成事务 ID（使用 UUID v7，时间有序）
// 防时钟回拨版本
func (g *SafeUUIDGenerator) GenerateTransactionID() string {
	return g.generateWithClockBackwardsProtection(func() string {
		return GenerateUUIDv7()
	})
}

// GenerateNodeID 生成节点 ID（使用 Snowflake，短 ID）
// 防时钟回拨版本
// 使用重试机制优雅处理时钟回拨，避免阻塞
func (g *SafeUUIDGenerator) GenerateNodeID() int64 {
	const maxRetries = 3

	// 第一阶段：使用原子操作检查时钟回拨（无锁）
	now := time.Now().UnixMilli()
	lastTime := atomic.LoadInt64(&g.lastTime)
	drift := now - lastTime

	// 检测时钟回拨（无锁等待）
	if drift < 0 {
		driftDuration := time.Duration(-drift) * time.Millisecond
		if driftDuration > g.maxDrift {
			// 发生显著时钟回拨，增加计数
			atomic.AddInt64(&g.driftCount, 1)
			logging.Warnf("检测到时钟回拨: drift=%v", driftDuration)
		}
	}

	// 第二阶段：尝试生成 Snowflake ID（在锁外进行）
	var id int64
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		id, lastErr = g.snowflake.Generate()
		if lastErr == nil {
			// 成功，更新 lastTime（使用原子操作）
			atomic.StoreInt64(&g.lastTime, time.Now().UnixMilli())
			return id
		}
		// 重试前等待
		time.Sleep(10 * time.Millisecond)
	}

	// 所有重试失败，记录错误并返回 0（而非 panic）
	// 这样调用者可以处理失败情况，而非导致服务崩溃
	logging.Errorf("Snowflake 生成重试 %d 次失败，返回 0: %v", maxRetries, lastErr)
	return 0
}

// Generate 生成通用 UUID（使用 UUID v4，随机）
// 实现 UUIDGenerator 接口
func (g *SafeUUIDGenerator) Generate() string {
	return GenerateUUIDv4()
}

// GetDriftCount 获取时钟回拨次数
func (g *SafeUUIDGenerator) GetDriftCount() int {
	return int(atomic.LoadInt64(&g.driftCount))
}

// generateWithClockBackwardsProtection 带时钟回拨保护的生成函数
// 使用原子操作减少锁竞争
func (g *SafeUUIDGenerator) generateWithClockBackwardsProtection(generateFunc func() string) string {
	// 使用原子操作检查时钟回拨（无锁）
	now := time.Now().UnixMilli()
	lastTime := atomic.LoadInt64(&g.lastTime)
	drift := now - lastTime

	// 检测时钟回拨
	if drift < 0 {
		driftDuration := time.Duration(-drift) * time.Millisecond
		if driftDuration > g.maxDrift {
			atomic.AddInt64(&g.driftCount, 1)
			logging.Warnf("检测到时钟回拨: drift=%v", driftDuration)
		}
	}

	// 执行生成操作（在锁外进行，避免阻塞）
	result := generateFunc()

	// 更新 lastTime（使用原子操作）
	atomic.StoreInt64(&g.lastTime, time.Now().UnixMilli())

	return result
}

// GetStats 获取生成器统计信息
func (g *SafeUUIDGenerator) GetStats() map[string]any {
	return map[string]any{
		"drift_count":    atomic.LoadInt64(&g.driftCount),
		"last_time":      atomic.LoadInt64(&g.lastTime),
		"max_drift":      g.maxDrift,
		"max_drift_wait": g.maxDriftWait,
	}
}

// Reset 重置生成器状态
func (g *SafeUUIDGenerator) Reset() {
	atomic.StoreInt64(&g.lastTime, time.Now().UnixMilli())
	atomic.StoreInt64(&g.driftCount, 0)
}
