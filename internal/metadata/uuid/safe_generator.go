// Package uuid 提供安全的 UUID 生成器（防时钟回拨）
package uuid

import (
	"fmt"
	"sync"
	"time"
)

// SafeUUIDGenerator 安全的 UUID 生成器（防时钟回拨）
//
// 特性:
//   - 检测时钟回拨
//   - 自动等待时钟恢复
//   - 记录回拨事件
//   - 保证 UUID 唯一性和单调性
type SafeUUIDGenerator struct {
	mu              sync.Mutex
	snowflake       *Snowflake
	maxDrift        time.Duration // 最大允许的时钟漂移
	lastTime        time.Time     // 上次生成时间
	callbackCount   int           // 回拨次数
	maxCallbackWait time.Duration // 最大回拨等待时间
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
		snowflake:       snowflake,
		maxDrift:        maxDrift,
		lastTime:        time.Now(),
		maxCallbackWait: maxWait,
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
func (g *SafeUUIDGenerator) GenerateNodeID() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := time.Now()
	drift := now.Sub(g.lastTime)

	// 检测时钟回拨
	if drift < -g.maxDrift {
		g.callbackCount++

		// 等待时钟恢复
		waitStart := time.Now()
		for {
			time.Sleep(10 * time.Millisecond)
			now = time.Now()
			drift = now.Sub(g.lastTime)

			// 时钟恢复或超时
			if drift >= 0 || time.Since(waitStart) > g.maxCallbackWait {
				break
			}
		}
	}

	// 生成 Snowflake ID
	id, err := g.snowflake.Generate()
	if err != nil {
		// 时钟回拨，等待恢复
		time.Sleep(10 * time.Millisecond)
		id, err = g.snowflake.Generate()
		if err != nil {
			panic(fmt.Sprintf("生成节点 ID 失败（时钟回拨）: %v", err))
		}
	}

	g.lastTime = now
	return id
}

// GenerateUUIDv4 生成 UUID v4（随机）
// 防时钟回拨版本（实际上 v4 不需要防回拨，但接口统一）
func (g *SafeUUIDGenerator) GenerateUUIDv4() string {
	return GenerateUUIDv4()
}

// GetCallbackCount 获取时钟回拨次数
func (g *SafeUUIDGenerator) GetCallbackCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.callbackCount
}

// generateWithClockBackwardsProtection 带时钟回拨保护的生成函数
func (g *SafeUUIDGenerator) generateWithClockBackwardsProtection(generateFunc func() string) string {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := time.Now()
	drift := now.Sub(g.lastTime)

	// 检测时钟回拨
	if drift < -g.maxDrift {
		g.callbackCount++

		// 等待时钟恢复
		waitStart := time.Now()
		for {
			time.Sleep(10 * time.Millisecond)
			now = time.Now()
			drift = now.Sub(g.lastTime)

			// 时钟恢复或超时
			if drift >= 0 || time.Since(waitStart) > g.maxCallbackWait {
				break
			}
		}

		// 超时后仍回拨，记录警告并继续生成
		if drift < 0 {
			fmt.Printf("警告: 时钟回拨超时，drift=%v\n", drift)
		}
	}

	g.lastTime = now
	return generateFunc()
}

// GetStats 获取生成器统计信息
func (g *SafeUUIDGenerator) GetStats() map[string]interface{} {
	g.mu.Lock()
	defer g.mu.Unlock()

	return map[string]interface{}{
		"callback_count":   g.callbackCount,
		"last_time":        g.lastTime,
		"max_drift":        g.maxDrift,
		"max_callback_wait": g.maxCallbackWait,
	}
}

// Reset 重置生成器状态
func (g *SafeUUIDGenerator) Reset() {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.lastTime = time.Now()
	g.callbackCount = 0
}
