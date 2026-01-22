// Package transport 传输层公共实现
//
// 包含 TCP 和 UDP 共享的验证和工具函数
package transport

import (
	"fmt"
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
