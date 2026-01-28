// Package client RPC Client 实现
//
// CLI 使用 RPC Client 调用 Daemon
package client

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/transport"
)

// RPCClient RPC 客户端
type RPCClient struct {
	// 服务器地址
	serverAddr string

	// Transport 层
	transport transport.Transport

	// 连接管理
	dialTimeout time.Duration
}

// Config RPC Client 配置
type Config struct {
	// ServerAddr 服务器地址（格式：host:port）
	ServerAddr string
	// Transport Transport 层（可选，用于发送 UDP 消息）
	Transport transport.Transport
	// DialTimeout 连接超时（默认 5 秒）
	DialTimeout time.Duration
}

// NewRPCClient 创建 RPC Client
func NewRPCClient(config *Config) (*RPCClient, error) {
	if config == nil {
		return nil, fmt.Errorf("配置不能为空")
	}

	if config.ServerAddr == "" {
		return nil, fmt.Errorf("服务器地址不能为空")
	}

	// Transport 是可选的（RPC Client 通过 TCP 发送请求，不需要 Transport）
	// 只有在需要发送 UDP 消息时才需要 Transport

	dialTimeout := config.DialTimeout
	if dialTimeout == 0 {
		dialTimeout = 5 * time.Second
	}

	return &RPCClient{
		serverAddr:  config.ServerAddr,
		transport:   config.Transport, // 可以为 nil
		dialTimeout: dialTimeout,
	}, nil
}

// Call 调用 RPC 方法
func (c *RPCClient) Call(ctx context.Context, payload []byte) ([]byte, error) {
	// 创建临时连接
	conn, err := net.DialTimeout("tcp", c.serverAddr, c.dialTimeout)
	if err != nil {
		logging.WithFields(map[string]any{
			"server": c.serverAddr,
			"error":  err,
		}).Error("连接 RPC Server 失败")
		return nil, fmt.Errorf("连接 RPC Server 失败: %w", err)
	}
	defer conn.Close()

	// 设置超时
	deadline, ok := ctx.Deadline()
	if ok {
		conn.SetDeadline(deadline)
	} else {
		conn.SetDeadline(time.Now().Add(30 * time.Second))
	}

	logging.WithFields(map[string]any{
		"server":      c.serverAddr,
		"payload_len": len(payload),
	}).Debug("发送 RPC 请求")

	// 1. 封装成 Frame（payload 已经是完整的 Frame 数据）
	// 如果 payload 只是消息数据，需要封装成 Frame
	// 这里假设 payload 是完整的 Frame 数据（已经由调用方封装）

	// 2. 发送请求
	if _, err := conn.Write(payload); err != nil {
		return nil, fmt.Errorf("发送请求失败: %w", err)
	}

	// 3. 接收响应（使用 FrameReader）
	reader := transport.NewFrameReader(conn)
	responseFrame, err := reader.ReadFrame()
	if err != nil {
		return nil, fmt.Errorf("接收响应失败: %w", err)
	}

	// 4. 解析响应（返回 Data 部分）
	return responseFrame.Data, nil
}

// Close 关闭客户端
func (c *RPCClient) Close() error {
	// RPC Client 是无状态的，每次 Call 创建临时连接
	// 这里保留接口以备将来需要连接池
	return nil
}
