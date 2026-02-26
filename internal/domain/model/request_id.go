// Package model 定义领域模型
//
// RequestID 值对象 - 请求唯一标识符
// 遵循 DDD 原则：不可变值对象，封装业务规则和验证逻辑
package model

import (
	"fmt"
	"strconv"
	"strings"
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
// - nodeID: 节点唯一标识（单个部分，不包含连字符），确保跨节点不冲突
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

// parse 解析 RequestID，返回各部分（nodeID, timestamp, sequence, error）
// 仅用于严格验证（Validate 方法）
func (r RequestID) parse() (nodeID string, timestamp int64, sequence uint32, err error) {
	parts := strings.Split(string(r), "-")
	if len(parts) != 4 {
		return "", 0, 0, fmt.Errorf("invalid request id format: expected 4 parts, got %d", len(parts))
	}

	nodeID = parts[0]
	timestamp, err = strconv.ParseInt(parts[2], 16, 64)
	if err != nil {
		return "", 0, 0, fmt.Errorf("invalid timestamp in request id: %w", err)
	}

	seq, err := strconv.ParseUint(parts[3], 16, 32)
	if err != nil {
		return "", 0, 0, fmt.Errorf("invalid sequence in request id: %w", err)
	}
	sequence = uint32(seq)

	return nodeID, timestamp, sequence, nil
}

// parts 分割 RequestID，返回部分数组
// 宽松解析，用于 NodeID/Timestamp/Sequence 方法
func (r RequestID) parts() []string {
	return strings.Split(string(r), "-")
}

// Validate 验证 RequestID 格式是否合法
// 返回错误如果格式不符合规范
func (r RequestID) Validate() error {
	if r.IsEmpty() {
		return fmt.Errorf("request id cannot be empty")
	}

	_, _, _, err := r.parse()
	return err
}

// NodeID 从 RequestID 中提取节点 ID
// 如果格式无效，返回空字符串
func (r RequestID) NodeID() string {
	parts := r.parts()
	if len(parts) >= 3 {
		return strings.Join(parts[:len(parts)-2], "-")
	}
	return ""
}

// Timestamp 从 RequestID 中提取时间戳
// 如果格式无效，返回 0
func (r RequestID) Timestamp() int64 {
	parts := r.parts()
	if len(parts) >= 3 {
		if ts, err := strconv.ParseInt(parts[len(parts)-2], 16, 64); err == nil {
			return ts
		}
	}
	return 0
}

// Sequence 从 RequestID 中提取序列号
// 如果格式无效，返回 0
func (r RequestID) Sequence() uint32 {
	parts := r.parts()
	if len(parts) >= 4 {
		if seq, err := strconv.ParseUint(parts[len(parts)-1], 16, 32); err == nil {
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
