// Package uuid 提供安全的 UUID 生成器（防时钟回拨）
package uuid

import (
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
)

// SafeUUIDGenerator 安全的 UUID 生成器（防时钟回拨）
// 使用原子操作减少锁竞争，检测并记录时钟回拨事件
type SafeUUIDGenerator struct {
	snowflake    *Snowflake
	maxDrift     time.Duration
	lastTime     int64
	driftCount   int64
	maxDriftWait time.Duration
}

// NewSafeUUIDGenerator 创建安全的 UUID 生成器
// 参数: datacenterID, workerID: 0-31; maxDrift: 最大时钟漂移（默认 100ms）; maxWait: 最大等待时间（默认 1s）
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

// GenerateTransactionID 实现 UUIDGenerator 接口（使用 UUID v7）
func (g *SafeUUIDGenerator) GenerateTransactionID() string {
	return g.generateWithClockBackwardsProtection(func() string {
		return GenerateUUIDv7()
	})
}

// GenerateNodeID 实现 UUIDGenerator 接口（使用 Snowflake，带重试机制）
func (g *SafeUUIDGenerator) GenerateNodeID() int64 {
	const maxRetries = 3

	now := time.Now().UnixMilli()
	lastTime := atomic.LoadInt64(&g.lastTime)
	drift := now - lastTime

	if drift < 0 {
		driftDuration := time.Duration(-drift) * time.Millisecond
		if driftDuration > g.maxDrift {
			atomic.AddInt64(&g.driftCount, 1)
			logging.Warnf("检测到时钟回拨: drift=%v", driftDuration)
		}
	}

	var id int64
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		id, lastErr = g.snowflake.Generate()
		if lastErr == nil {
			atomic.StoreInt64(&g.lastTime, time.Now().UnixMilli())
			return id
		}
		time.Sleep(10 * time.Millisecond)
	}

	logging.Errorf("Snowflake 生成重试 %d 次失败，返回 0: %v", maxRetries, lastErr)
	return 0
}

// Generate 实现 UUIDGenerator 接口（使用 UUID v4）
func (g *SafeUUIDGenerator) Generate() string {
	return GenerateUUIDv4()
}

// GetDriftCount 获取时钟回拨次数
func (g *SafeUUIDGenerator) GetDriftCount() int {
	return int(atomic.LoadInt64(&g.driftCount))
}

// generateWithClockBackwardsProtection 带时钟回拨保护的生成函数
func (g *SafeUUIDGenerator) generateWithClockBackwardsProtection(generateFunc func() string) string {
	now := time.Now().UnixMilli()
	lastTime := atomic.LoadInt64(&g.lastTime)
	drift := now - lastTime

	if drift < 0 {
		driftDuration := time.Duration(-drift) * time.Millisecond
		if driftDuration > g.maxDrift {
			atomic.AddInt64(&g.driftCount, 1)
			logging.Warnf("检测到时钟回拨: drift=%v", driftDuration)
		}
	}

	result := generateFunc()
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
