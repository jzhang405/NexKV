// Package rpc 基于 libp2p Stream 的 RPC 实现
package rpc

import (
	"context"
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

// ========================================
// 错误码定义（扩展）
// ========================================

// RPC 错误码（新增，补充 types.go 中的错误码）
const (
	// 一致性错误 (3000-3999) - 复用集群层
	ErrCodeQuorumNotReached = 3001 // Quorum 未达到
	ErrCodeConflict         = 3002 // 冲突
	ErrCodeRetryLater       = 3003 // 稍后重试

	// Fanout 错误 (4000-4999)
	ErrCodeFanoutForwardFailed = 4001 // Fanout 转发失败
	ErrCodeHopsExceeded        = 4002 // 跳数超限
	ErrCodePeerUnavailable     = 4003 // Peer 不可用
	ErrCodeTooManyRequests     = 4004 // 请求过多（限流）
	ErrCodeInvalidArgument     = 4005 // 无效参数
)

// ========================================
// 边界控制和验证
// ========================================

// FanoutOptions Fanout 选项（含边界控制）
type FanoutOptions struct {
	// 响应模式
	Mode ResponseMode

	// Quorum 阈值（默认 len(Peers)/2 + 1）
	// 若未指定，从集群层全局配置获取
	Quorum int

	// 超时时间（建议默认 30ms）
	Timeout time.Duration

	// 单跳最大并发数（默认 10）
	MaxConcurrent int

	// Hops 跳数相关配置
	Hops            uint8 // 转发跳数，默认 1
	MaxForwardPeers uint8 // 单跳最大转发 peer 数，默认 20
	MaxHops         uint8 // 全局最大跳数限制，默认 8
}

// DefaultFanoutOptions 返回默认 Fanout 选项
func DefaultFanoutOptions() *FanoutOptions {
	return &FanoutOptions{
		Mode:            WaitAll,
		Timeout:         30 * time.Second,
		MaxConcurrent:   10,
		Hops:            1,
		MaxForwardPeers: 20,
		MaxHops:         8,
	}
}

// ValidateAndNormalize 验证并规范化 FanoutOptions
func ValidateAndNormalize(opts *FanoutOptions, peerCount int) (*FanoutOptions, error) {
	if opts == nil {
		opts = DefaultFanoutOptions()
	}

	// 创建副本，避免修改输入参数
	result := *opts
	resultPtr := &result

	// 分步骤验证和规范化
	if err := validateResponseMode(resultPtr.Mode); err != nil {
		return nil, err
	}

	if err := validatePeerCount(peerCount); err != nil {
		return nil, err
	}

	normalizeHops(resultPtr)
	normalizeMaxForwardPeers(resultPtr)
	normalizeMaxConcurrent(resultPtr)

	if err := normalizeAndValidateQuorum(resultPtr, peerCount); err != nil {
		return nil, err
	}

	if err := normalizeAndValidateTimeout(resultPtr); err != nil {
		return nil, err
	}

	return resultPtr, nil
}

// validateResponseMode 验证响应模式有效性
func validateResponseMode(mode ResponseMode) error {
	if mode < FireForget || mode > WaitAll {
		return fmt.Errorf("无效的响应模式: %d", mode)
	}
	return nil
}

// validatePeerCount 验证 peer 列表不能为空
func validatePeerCount(peerCount int) error {
	if peerCount == 0 {
		return fmt.Errorf("peer 列表为空")
	}
	return nil
}

// normalizeHops 规范化 Hops 配置
func normalizeHops(opts *FanoutOptions) {
	if opts.Hops == 0 {
		opts.Hops = 1
	}
	if opts.MaxHops == 0 {
		opts.MaxHops = 8 // 默认全局最大跳数限制
	}
	if opts.Hops > opts.MaxHops {
		opts.Hops = opts.MaxHops
	}
}

// normalizeMaxForwardPeers 规范化 MaxForwardPeers 配置
func normalizeMaxForwardPeers(opts *FanoutOptions) {
	if opts.MaxForwardPeers == 0 {
		opts.MaxForwardPeers = 20
	}
}

// normalizeMaxConcurrent 规范化 MaxConcurrent 配置
func normalizeMaxConcurrent(opts *FanoutOptions) {
	if opts.MaxConcurrent == 0 {
		opts.MaxConcurrent = 10
	}
}

// normalizeAndValidateQuorum 规范化并验证 Quorum 配置
func normalizeAndValidateQuorum(opts *FanoutOptions, peerCount int) error {
	if opts.Mode == Quorum && opts.Quorum == 0 {
		opts.Quorum = peerCount/2 + 1
	}

	// 验证：Quorum 不能超过 peer 数量
	if opts.Mode == Quorum && opts.Quorum > peerCount {
		return fmt.Errorf("quorum 阈值 (%d) 不能超过 peer 数量 (%d)", opts.Quorum, peerCount)
	}

	return nil
}

// normalizeAndValidateTimeout 规范化并验证超时配置
func normalizeAndValidateTimeout(opts *FanoutOptions) error {
	if opts.Timeout == 0 {
		opts.Timeout = 30 * time.Second
	}
	if opts.Timeout < 10*time.Millisecond {
		return fmt.Errorf("超时时间过短: %v (最小 10ms)", opts.Timeout)
	}
	return nil
}

// ValidatePeers 验证 peer 列表
func ValidatePeers(peers []peer.ID) error {
	if len(peers) == 0 {
		return fmt.Errorf("peer 列表为空")
	}
	return nil
}

// IsQuorumReached 判断是否达到 Quorum
func IsQuorumReached(successCount, totalPeers, quorum int) bool {
	if quorum <= 0 {
		quorum = totalPeers/2 + 1
	}
	return successCount >= quorum
}

// ========================================
// 响应模式
// ========================================

// ResponseMode 响应模式

// ResponseMode 响应模式
type ResponseMode int

const (
	// FireForget 不等待响应
	FireForget ResponseMode = iota
	// Quorum 等待多数派响应
	Quorum
	// WaitAll 等待所有响应
	WaitAll
)

// String 返回响应模式的字符串表示
func (m ResponseMode) String() string {
	switch m {
	case FireForget:
		return "Fire-and-Forget"
	case Quorum:
		return "Quorum"
	case WaitAll:
		return "WaitAll"
	default:
		return "Unknown"
	}
}

// ========================================
// Hops 转发相关
// ========================================

// FanoutResponse Fanout 单个 peer 的响应
type FanoutResponse struct {
	PeerID  peer.ID
	Body    []byte
	Error   error
	Latency time.Duration
	Hop     uint8 // 当前跳数
}

// CanForward 判断是否可以继续转发
func CanForward(currentHops, maxHops uint8) bool {
	return currentHops > 0 && currentHops < maxHops
}

// DecrementHops 跳数递减
func DecrementHops(hops uint8) uint8 {
	if hops > 0 {
		return hops - 1
	}
	return 0
}

// ========================================
// 辅助函数
// ========================================

// IsTimeout 判断错误是否为超时
func IsTimeout(err error) bool {
	if err == nil {
		return false
	}
	// 检查 context.DeadlineExceeded
	if err == context.DeadlineExceeded {
		return true
	}
	// 检查 RPC 超时错误
	if rpcErr, ok := err.(*RPCError); ok {
		return rpcErr.Code == ErrCodeTimeout
	}
	return false
}

// IsQuorumError 判断错误是否为 Quorum 错误
func IsQuorumError(err error) bool {
	if rpcErr, ok := err.(*RPCError); ok {
		return rpcErr.Code == ErrCodeQuorumNotReached
	}
	return false
}

// IsPeerUnavailable 判断错误是否为 Peer 不可用
func IsPeerUnavailable(err error) bool {
	if rpcErr, ok := err.(*RPCError); ok {
		return rpcErr.Code == ErrCodePeerUnavailable
	}
	return false
}
