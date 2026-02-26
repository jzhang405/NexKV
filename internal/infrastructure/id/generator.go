// Package id 提供唯一 ID 生成基础设施
//
// RequestIDGenerator - 分布式请求 ID 生成器
package id

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/sirupsen/logrus"
)

// ============================================================================
// RequestIDGenerator - ID 生成器（基础设施层）
// ============================================================================

// 时钟回退监控指标（P1-04）
var (
	clockBackoffCount atomic.Int64 // 时钟回退次数
	seqOverflowCount  atomic.Int64 // 序列号溢出次数
)

// GetClockBackoffCount 获取时钟回退次数（用于监控）
func GetClockBackoffCount() int64 {
	return clockBackoffCount.Load()
}

// GetSeqOverflowCount 获取序列号溢出次数（用于监控）
func GetSeqOverflowCount() int64 {
	return seqOverflowCount.Load()
}

// RequestIDGenerator 请求 ID 生成器（线程安全 + 时钟漂移保护）
//
// 这是一个技术性基础设施组件，负责生成全局唯一的 RequestID。
// 它封装了复杂的生成逻辑，包括时钟漂移保护和序列号溢出处理。
//
// 设计特点：
// - 线程安全：使用 atomic 操作保证并发安全
// - 时钟漂移保护：检测 NTP 同步导致的时间回退
// - 序列号溢出保护：每秒最多 65535 个请求
// - 条件变量等待：避免忙等待（P1-02 修复）
type RequestIDGenerator struct {
	nodeID     string        // 节点 ID（启动时分配）
	lastSecond atomic.Int64  // 上次生成时间戳（秒）
	secondSeq  atomic.Uint32 // 当前秒内序列号
	mu         sync.Mutex    // 保护条件变量
	cond       *sync.Cond    // 条件变量（用于等待下一秒）
}

// NewRequestIDGenerator 创建请求 ID 生成器
//
// 参数:
//   - nodeID: 节点唯一标识，用于确保跨节点生成的 ID 不冲突
//
// 示例:
//
//	generator := id.NewRequestIDGenerator("node-001")
//	id := generator.Next() // 生成如 "node-001-65d4a3f0-0001"
func NewRequestIDGenerator(nodeID string) *RequestIDGenerator {
	g := &RequestIDGenerator{
		nodeID:     nodeID,
		lastSecond: atomic.Int64{},
		secondSeq:  atomic.Uint32{},
	}
	g.cond = sync.NewCond(&g.mu)
	return g
}

// Next 生成下一个请求 ID（线程安全 + 时钟漂移保护 + 序列号溢出保护）
//
// 时钟回退处理策略：
//   - 当检测到系统时间回退（now < lastSecond）时，使用 lastSecond 作为时间戳
//   - 这保证了 RequestID 单调递增，避免 ID 冲突
//   - 场景：NTP 同步、闰秒、手动修改系统时间
//   - 添加监控指标（P1-04 修复）
//
// 序列号溢出保护:
//   - 序列号格式为 4 位 16 进制（最大 0xFFFF = 65535）
//   - 当序列号超过 65535 时，使用条件变量等待下一秒（P1-02 修复）
//   - 这样可以保持 ID 格式的一致性，避免忙等待
func (g *RequestIDGenerator) Next() model.RequestID {
	const maxSeq uint32 = 0xFFFF // 4 位 16 进制最大值

	for {
		now := time.Now().Unix()
		lastSec := g.lastSecond.Load()

		// 时钟漂移保护：检测时钟回退（P1-04: 添加监控）
		if now < lastSec {
			// 记录时钟回退事件
			backoffSecs := lastSec - now
			clockBackoffCount.Add(1)
			logrus.WithFields(logrus.Fields{
				"current_time":   now,
				"last_time":      lastSec,
				"backoff_secs":   backoffSecs,
				"total_backoffs": clockBackoffCount.Load(),
			}).Warn("clock backoff detected, using last timestamp")
			now = lastSec
		}

		// 同一秒内，递增序列号
		if now == lastSec {
			seq := g.secondSeq.Add(1) - 1 // 返回递增前的值
			if seq > maxSeq {
				// P1-02 修复：使用条件变量等待下一秒，避免忙等待
				seqOverflowCount.Add(1)
				logrus.WithFields(logrus.Fields{
					"sequence":        seq,
					"max_sequence":    maxSeq,
					"total_overflows": seqOverflowCount.Load(),
				}).Debug("sequence overflow, waiting for next second")

				g.mu.Lock()
				// 双重检查：确认仍在同一秒
				for time.Now().Unix() == lastSec {
					g.cond.Wait()
				}
				g.mu.Unlock()
				continue
			}
			return g.format(now, seq)
		}

		// 新的一秒，尝试更新时间戳
		if g.lastSecond.CompareAndSwap(lastSec, now) {
			g.secondSeq.Store(1)
			// 唤醒所有等待的 goroutine
			g.cond.Broadcast()
			return g.format(now, 0)
		}
		// CAS 失败，重试（其他 goroutine 已更新时间戳）
	}
}

// format 格式化 RequestID
// 格式: {NodeID}-{Timestamp:08x}-{Sequence:04x}
func (g *RequestIDGenerator) format(timestamp int64, sequence uint32) model.RequestID {
	return model.RequestID(fmt.Sprintf("%s-%08x-%04x", g.nodeID, timestamp, sequence))
}

// NodeID 返回生成器关联的节点 ID
func (g *RequestIDGenerator) NodeID() string {
	return g.nodeID
}
