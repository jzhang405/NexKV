// Package transport 传输层公共实现
//
// 包含 TCP 和 UDP 共享的验证和工具函数
package transport

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// validateTransportConfig 验证传输层配置的有效性
//
// P2-5: 配置验证函数，确保配置值在合理范围内
func validateTransportConfig(config *TransportConfig) error {
	if config.ListenAddr == "" {
		return fmt.Errorf("监听地址不能为空")
	}

	if config.MaxMessageSize <= 0 || config.MaxMessageSize > 1024*1024*1024 {
		return fmt.Errorf("最大消息大小必须在 (0, 1GB] 范围内，当前值: %d", config.MaxMessageSize)
	}

	// 验证超时配置（不能为负数）
	timeouts := []struct {
		name  string
		value time.Duration
	}{
		{"读超时", config.ReadTimeout},
		{"写超时", config.WriteTimeout},
		{"保活间隔", config.KeepAliveInterval},
		{"保活超时", config.KeepAliveTimeout},
		{"通道发送超时", config.ChannelSendTimeout},
	}

	for _, t := range timeouts {
		if t.value < 0 {
			return fmt.Errorf("%s不能为负数，当前值: %v", t.name, t.value)
		}
	}

	if config.BufferSize <= 0 || config.BufferSize > 65536 {
		return fmt.Errorf("缓冲区大小必须在 (0, 64KB] 范围内，当前值: %d", config.BufferSize)
	}

	return nil
}

// createBatchForwardResult 创建批量转发失败结果（用于未启动的情况）
func createBatchForwardResult(addrs []string, err error) BatchForwardMessageResult {
	results := make([]BatchForwardResult, len(addrs))
	for i, addr := range addrs {
		results[i] = BatchForwardResult{
			Addr:  addr,
			SeqID: 0,
			Error: err,
		}
	}
	return BatchForwardMessageResult{
		SuccessCount: 0,
		FailureCount: len(addrs),
		Results:      results,
	}
}

// generateMsgSeq 生成消息序列号的公共实现
//
// 参数:
//   - generator: 存储在 atomic.Value 中的序列号生成器函数
//   - defaultCounter: 默认的原子计数器（当 generator 为 nil 或无效时使用）
//
// 返回:
//   - uint64: 消息序列号
func generateMsgSeq(generator interface{}, defaultCounter *atomic.Uint64) uint64 {
	if generator == nil {
		return defaultCounter.Add(1)
	}

	fn, ok := generator.(func() uint64)
	if !ok || fn == nil {
		return defaultCounter.Add(1)
	}

	return fn()
}

// batchForwarder 批量转发函数类型
type batchForwarder func(ctx context.Context, addr string, msgExt MsgFrame) (uint64, error)

// executeBatchForward 执行批量转发的公共实现
//
// 参数:
//   - ctx: 上下文
//   - addrs: 目标地址列表
//   - msgExt: 要转发的消息
//   - forwarder: 单个转发函数
//
// 返回:
//   - BatchForwardMessageResult: 批量转发结果
func executeBatchForward(
	ctx context.Context,
	addrs []string,
	msgExt MsgFrame,
	forwarder batchForwarder,
) BatchForwardMessageResult {
	// 限制批量大小
	if len(addrs) > maxBatchSize {
		addrs = addrs[:maxBatchSize]
	}

	results := make([]BatchForwardResult, len(addrs))
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, maxBatchConcurrency)

	for i, addr := range addrs {
		wg.Add(1)
		go func(idx int, targetAddr string) {
			defer wg.Done()
			semaphore <- struct{}{}        // 获取信号量
			defer func() { <-semaphore }() // 释放信号量

			seqID, err := forwarder(ctx, targetAddr, msgExt)
			results[idx] = BatchForwardResult{
				Addr:  targetAddr,
				SeqID: seqID,
				Error: err,
			}
		}(i, addr)
	}

	wg.Wait()

	// 统计结果
	var success, failure int
	for _, r := range results {
		if r.Error != nil {
			failure++
		} else {
			success++
		}
	}

	return BatchForwardMessageResult{
		SuccessCount: success,
		FailureCount: failure,
		Results:      results,
	}
}
