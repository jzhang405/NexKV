// Package porcupine 提供 Porcupine 线性一致性验证集成
// 本文件实现时间戳生成器，用于记录操作事件的时序
package porcupine

import (
	"sync/atomic"
	"time"
)

// TimestampGenerator 时间戳生成器接口
// 上层调用无需关心具体实现，只面向接口使用
type TimestampGenerator interface {
	// Now 生成当前时间戳
	// 返回值必须保证：
	// 1. 同一调用者连续调用时单调递增
	// 2. 并发安全
	Now() int64
}

// MonotonicTimestamp 单调时间戳生成器
// 使用原子操作确保时间戳单调递增，解决时间戳回退问题
// 适用场景：单机测试 / 单节点场景
type MonotonicTimestamp struct {
	last int64
}

// NewMonotonicTimestamp 创建单调时间戳生成器
func NewMonotonicTimestamp() *MonotonicTimestamp {
	return &MonotonicTimestamp{
		last: time.Now().UnixNano(),
	}
}

// Now 获取单调递增的时间戳
// 算法：比较系统时间与上次时间戳，取较大值
// 如果系统时间回退，则使用 last + 1
func (t *MonotonicTimestamp) Now() int64 {
	for {
		last := atomic.LoadInt64(&t.last)
		now := time.Now().UnixNano()

		// 确保时间戳单调递增
		if now <= last {
			now = last + 1
		}

		// CAS 操作确保并发安全
		if atomic.CompareAndSwapInt64(&t.last, last, now) {
			return now
		}
	}
}

// LogicalTimestamp 逻辑时间戳
// 完全不依赖物理时钟，避免跨机器时钟同步问题
// 适用场景：集群测试 / 多节点场景
// 格式: (clientID << 48) | localSeq
// - 高 16 位：客户端 ID（支持最多 65535 个客户端）
// - 低 48 位：本地序列号（支持约 281 万亿次操作）
type LogicalTimestamp struct {
	clientID int64  // 客户端 ID（高 16 位）
	seq      int64  // 本地序列号（原子操作）
}

// NewLogicalTimestamp 创建逻辑时间戳生成器
// clientID: 客户端唯一标识，范围 0-65535
func NewLogicalTimestamp(clientID int) *LogicalTimestamp {
	return &LogicalTimestamp{
		clientID: int64(clientID & 0xFFFF), // 限制在 16 位范围内
		seq:      0,
	}
}

// Now 生成逻辑时间戳
// 格式: (clientID << 48) | localSeq
func (t *LogicalTimestamp) Now() int64 {
	newSeq := atomic.AddInt64(&t.seq, 1)
	return t.clientID<<48 | (newSeq & 0xFFFFFFFFFFFF)
}

// NewTimestampGenerator 时间戳生成器工厂
// 根据节点数量自动选择合适的方案
//
// 选择规则：
//   - totalNodes == 1: 使用 MonotonicTimestamp（单机物理时钟+原子自增）
//   - totalNodes > 1:  使用 LogicalTimestamp（节点ID+本地序列）
//
// 参数：
//   - nodeID: 节点标识，用于提取客户端 ID
//   - totalNodes: 集群总节点数
//
// 返回：TimestampGenerator 接口，上层无需关心具体实现
func NewTimestampGenerator(nodeID string, totalNodes int) TimestampGenerator {
	if totalNodes == 1 {
		// 单节点场景：使用单调物理时间戳
		return NewMonotonicTimestamp()
	}

	// 多节点场景：使用逻辑时间戳
	// 从 nodeID 提取数字 ID（如 "node-1" -> 1）
	clientID := extractClientID(nodeID)
	return NewLogicalTimestamp(clientID)
}

// extractClientID 从节点 ID 提取数字标识
// 支持格式：node-1, node1, 1 等
// 如果无法提取，返回基于字符串哈希的值
func extractClientID(nodeID string) int {
	// 简单实现：尝试从字符串末尾提取数字
	// 对于 "node-1" 这样的格式，提取 1
	id := 0
	for i := len(nodeID) - 1; i >= 0; i-- {
		c := nodeID[i]
		if c >= '0' && c <= '9' {
			id = id*10 + int(c-'0')
		} else if id > 0 {
			// 已经读取了数字，遇到非数字字符停止
			break
		}
	}

	// 如果没有提取到数字，使用哈希值
	if id == 0 {
		// 简单哈希
		for _, c := range nodeID {
			id = id*31 + int(c)
		}
		id = id & 0xFFFF // 限制在 16 位范围内
	}

	// 确保 ID 在有效范围内（1-65535，避免 0）
	if id == 0 {
		id = 1
	}
	return id
}
