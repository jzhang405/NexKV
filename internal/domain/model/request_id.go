// Package model 定义领域模型
//
// RequestID 值对象 - 请求唯一标识符
// 遵循 DDD 原则：不可变值对象，封装业务规则和验证逻辑
package model

import (
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// ============================================================================
// RequestID - 值对象
// ============================================================================

// RequestID 请求唯一标识符（值对象）
//
// 格式: {NodeID}-{Timestamp:08x}-{Sequence:04x}
// 示例: node-001-65d4a3f0-0001
//
// 设计说明：
// - nodeID: 节点唯一标识，确保跨节点不冲突
// - timestamp: Unix 时间戳（16 进制，8 位），支持跨节点时间排序
// - sequence: 自增序列号（16 进制，4 位），每秒最多 65535 个请求
//
// 优势：
// - 固定宽度：便于解析和索引
// - 16 进制：减少长度（vs 10 进制）
// - 时间排序：支持分布式追踪按时间排序
type RequestID string

// String 返回 RequestID 的字符串表示
func (r RequestID) String() string {
	return string(r)
}

// IsEmpty 检查 RequestID 是否为空
func (r RequestID) IsEmpty() bool {
	return r == ""
}

// Validate 验证 RequestID 格式是否合法
// 返回错误如果格式不符合规范
func (r RequestID) Validate() error {
	if r.IsEmpty() {
		return fmt.Errorf("request id cannot be empty")
	}

	parts := strings.Split(string(r), "-")
	if len(parts) != 4 {
		return fmt.Errorf("invalid request id format: expected 4 parts, got %d", len(parts))
	}

	// 验证 timestamp 部分（第3部分）是有效的 16 进制
	if _, err := strconv.ParseUint(parts[2], 16, 32); err != nil {
		return fmt.Errorf("invalid timestamp in request id: %w", err)
	}

	// 验证 sequence 部分（第4部分）是有效的 16 进制
	if _, err := strconv.ParseUint(parts[3], 16, 16); err != nil {
		return fmt.Errorf("invalid sequence in request id: %w", err)
	}

	return nil
}

// NodeID 从 RequestID 中提取节点 ID
// 格式: {NodeID}-{Timestamp:08x}-{Sequence:04x}
// nodeID 可能包含连字符，所以取除最后两部分外的所有内容
// 如果格式无效，返回空字符串
func (r RequestID) NodeID() string {
	parts := strings.Split(string(r), "-")
	if len(parts) >= 3 {
		// 最后两部分是 timestamp 和 sequence
		return strings.Join(parts[:len(parts)-2], "-")
	}
	return ""
}

// Timestamp 从 RequestID 中提取时间戳
// 如果格式无效，返回 0
func (r RequestID) Timestamp() int64 {
	parts := strings.Split(string(r), "-")
	if len(parts) >= 3 {
		if ts, err := strconv.ParseInt(parts[2], 16, 64); err == nil {
			return ts
		}
	}
	return 0
}

// Sequence 从 RequestID 中提取序列号
// 如果格式无效，返回 0
func (r RequestID) Sequence() uint32 {
	parts := strings.Split(string(r), "-")
	if len(parts) >= 4 {
		if seq, err := strconv.ParseUint(parts[3], 16, 32); err == nil {
			return uint32(seq)
		}
	}
	return 0
}

// Time 返回请求 ID 中的时间戳（用于排序）
// 如果格式无效，返回零值 time.Time
func (r RequestID) Time() time.Time {
	ts := r.Timestamp()
	if ts == 0 {
		return time.Time{}
	}
	return time.Unix(ts, 0)
}

// ============================================================================
// RequestIDGenerator - 领域服务
// ============================================================================

// RequestIDGenerator 请求 ID 生成器（线程安全 + 时钟漂移保护）
//
// 这是一个领域服务，负责生成全局唯一的 RequestID。
// 它封装了复杂的生成逻辑，包括时钟漂移保护和序列号溢出处理。
type RequestIDGenerator struct {
	nodeID     string        // 节点 ID（启动时分配）
	lastSecond atomic.Int64  // 上次生成时间戳（秒）
	secondSeq  atomic.Uint32 // 当前秒内序列号
}

// NewRequestIDGenerator 创建请求 ID 生成器
//
// 参数:
//   - nodeID: 节点唯一标识，用于确保跨节点生成的 ID 不冲突
//
// 示例:
//
//	generator := model.NewRequestIDGenerator("node-001")
//	id := generator.Next() // 生成如 "node-001-65d4a3f0-0001"
func NewRequestIDGenerator(nodeID string) *RequestIDGenerator {
	return &RequestIDGenerator{
		nodeID:     nodeID,
		lastSecond: atomic.Int64{},
		secondSeq:  atomic.Uint32{},
	}
}

// Next 生成下一个请求 ID（线程安全 + 时钟漂移保护 + 序列号溢出保护）
//
// 时钟回退处理策略：
//   - 当检测到系统时间回退（now < lastSecond）时，使用 lastSecond 作为时间戳
//   - 这保证了 RequestID 单调递增，避免 ID 冲突
//   - 场景：NTP 同步、闰秒、手动修改系统时间
//
// 序列号溢出保护：
//   - 序列号格式为 4 位 16 进制（最大 0xFFFF = 65535）
//   - 当序列号超过 65535 时，等待下一秒再生成
//   - 这样可以保持 ID 格式的一致性
func (g *RequestIDGenerator) Next() RequestID {
	const maxSeq uint32 = 0xFFFF // 4 位 16 进制最大值

	for {
		now := time.Now().Unix()

		// 时钟漂移保护：检测时间回退
		lastSec := g.lastSecond.Load()
		if now < lastSec {
			// 时钟回退，使用上次的时间戳
			now = lastSec
		}

		if now == lastSec {
			// 同一秒内，递增序列号
			seq := g.secondSeq.Add(1) - 1 // 返回递增前的值

			// 序列号溢出保护
			if seq > maxSeq {
				// 序列号溢出，等待下一秒
				time.Sleep(10 * time.Millisecond)
				continue
			}

			return g.format(now, seq)
		}

		// 新的一秒，重置序列号
		if g.lastSecond.CompareAndSwap(lastSec, now) {
			g.secondSeq.Store(1)
			return g.format(now, 0)
		}

		// CAS 失败，重试
	}
}

// format 格式化 RequestID
// 格式: {NodeID}-{Timestamp:08x}-{Sequence:04x}
func (g *RequestIDGenerator) format(timestamp int64, sequence uint32) RequestID {
	return RequestID(fmt.Sprintf("%s-%08x-%04x", g.nodeID, timestamp, sequence))
}

// NodeID 返回生成器关联的节点 ID
func (g *RequestIDGenerator) NodeID() string {
	return g.nodeID
}
