// Package rpc 基于 libp2p Stream 的 RPC 实现
package rpc

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/config/logging"
	"github.com/jzhang405/NexKV/internal/transport"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/vmihailenco/msgpack/v5"
)

// 全局请求 ID 计数器（原子操作）
var globalRequestID uint64

// Client 基于 libp2p Stream 的 RPC 客户端
type Client struct {
	host           host.Host
	pool           *ConnectionPool
	defaultTimeout time.Duration
	maxMessageSize int
	codec          *transport.MessagePackCodec
	enablePool     bool // 是否启用连接池
}

// NewClient 创建 RPC 客户端
func NewClient(host host.Host) *Client {
	return &Client{
		host:           host,
		defaultTimeout: 30 * time.Second,
		maxMessageSize: transport.MaxMessageSize,
		codec:          transport.NewMessagePackCodec(),
		enablePool:     false, // 默认不启用连接池
	}
}

// NewClientWithPool 创建带连接池的 RPC 客户端
func NewClientWithPool(host host.Host, poolCfg *PoolConfig) *Client {
	pool := NewConnectionPool(host, poolCfg)

	return &Client{
		host:           host,
		pool:           pool,
		defaultTimeout: 30 * time.Second,
		maxMessageSize: transport.MaxMessageSize,
		codec:          transport.NewMessagePackCodec(),
		enablePool:     true, // 启用连接池
	}
}

// SetPool 启用或禁用连接池
func (c *Client) SetPool(enable bool, poolCfg *PoolConfig) {
	if enable && c.pool == nil {
		if poolCfg == nil {
			poolCfg = DefaultPoolConfig()
		}
		c.pool = NewConnectionPool(c.host, poolCfg)
	}
	c.enablePool = enable
}

// Close 关闭客户端
func (c *Client) Close() error {
	if c.pool != nil {
		return c.pool.Close()
	}
	return nil
}

// SetDefaultTimeout 设置默认超时时间
func (c *Client) SetDefaultTimeout(timeout time.Duration) {
	c.defaultTimeout = timeout
}

// Call 发送 RPC 请求
func (c *Client) Call(ctx context.Context, peerID peer.ID, method string, req []byte) ([]byte, error) {
	// 验证输入参数
	if err := validateCallParams(peerID, method); err != nil {
		return nil, err
	}

	var stream network.Stream
	var err error

	// 使用连接池或直接创建 Stream
	if c.enablePool && c.pool != nil {
		stream, err = c.pool.GetStream(ctx, peerID)
		if err != nil {
			return nil, fmt.Errorf("从连接池获取 Stream 失败: %w", err)
		}
	} else {
		stream, err = c.host.NewStream(ctx, peerID, transport.ProtocolNexKVRPC)
		if err != nil {
			return nil, fmt.Errorf("创建 Stream 失败: %w", err)
		}
	}

	// 确保 Stream 被关闭
	defer func() {
		if c.enablePool && c.pool != nil {
			// 返回到连接池
			_ = c.pool.ReturnStream(stream)
		} else {
			// 直接关闭
			_ = stream.Close()
		}
	}()

	// 设置超时
	deadline := c.getDeadline(ctx)
	if err := setStreamDeadline(stream, deadline); err != nil {
		return nil, err
	}

	// 发送请求
	if err := c.sendRequest(stream, method, req); err != nil {
		return nil, fmt.Errorf("发送请求失败: %w", err)
	}

	// 接收响应
	resp, err := c.receiveResponse(stream)
	if err != nil {
		return nil, fmt.Errorf("接收响应失败: %w", err)
	}

	return resp, nil
}

// getDeadline 获取截止时间
func (c *Client) getDeadline(ctx context.Context) time.Time {
	if deadline, ok := ctx.Deadline(); ok {
		return deadline
	}
	return time.Now().Add(c.defaultTimeout)
}

// sendRequest 发送 RPC 请求
func (c *Client) sendRequest(stream network.Stream, method string, body []byte) error {
	// 创建 RPC 请求
	rpcReq := &RPCRequest{
		Method:    method,
		RequestID: nextRequestID(),
		Body:      body,
	}

	// 序列化请求
	reqBytes, err := MarshalRPCRequest(rpcReq)
	if err != nil {
		return err
	}

	// 创建传输层消息
	msg := transport.NewMessage(transport.MessageTypeCluster)
	msg.From = c.host.ID().String()
	msg.Payload = reqBytes

	// 写入消息
	if err := c.codec.Encode(stream, msg); err != nil {
		return fmt.Errorf("编码请求失败: %w", err)
	}

	c.logRequestSent(stream, rpcReq, method, len(body))
	return nil
}

// receiveResponse 接收 RPC 响应
func (c *Client) receiveResponse(stream network.Stream) ([]byte, error) {
	// 读取响应消息
	msg, err := c.codec.Decode(stream)
	if err != nil {
		return nil, fmt.Errorf("解码响应失败: %w", err)
	}

	// 验证消息
	if !msg.IsValid() {
		return nil, fmt.Errorf("无效的响应消息")
	}

	// 解析 RPC 响应
	resp, err := UnmarshalRPCResponse(msg.Payload)
	if err != nil {
		return nil, fmt.Errorf("解析 RPC 响应失败: %w", err)
	}

	// 检查状态码
	if resp.Status != ErrCodeSuccess {
		return nil, NewRPCError(resp.Status, fmt.Sprintf("RPC 调用失败: 状态码=%d", resp.Status))
	}

	c.logResponseReceived(stream, resp)
	return resp.Body, nil
}

// CallStream 发送流式 RPC 请求（返回 Stream，由调用者管理）
func (c *Client) CallStream(ctx context.Context, peerID peer.ID) (network.Stream, error) {
	stream, err := c.host.NewStream(ctx, peerID, transport.ProtocolNexKVRPC)
	if err != nil {
		return nil, fmt.Errorf("创建 Stream 失败: %w", err)
	}
	return stream, nil
}

// Ping Ping 指定节点（用于连通性检查）
func (c *Client) Ping(ctx context.Context, peerID peer.ID) (time.Duration, error) {
	start := time.Now()

	// 创建 Ping 请求
	req := &PingRequest{Timestamp: start.UnixNano()}
	reqBytes, err := msgpack.Marshal(req)
	if err != nil {
		return 0, fmt.Errorf("编码 Ping 请求失败: %w", err)
	}

	// 发送请求
	respBytes, err := c.Call(ctx, peerID, "Ping", reqBytes)
	if err != nil {
		return 0, err
	}

	// 解析响应
	var resp PingResponse
	if err := msgpack.Unmarshal(respBytes, &resp); err != nil {
		return 0, fmt.Errorf("解码 Ping 响应失败: %w", err)
	}

	// 验证响应（允许一定的时钟偏差）
	maxClockSkew := time.Second
	timestampDiff := resp.Timestamp - req.Timestamp
	if abs(timestampDiff) > maxClockSkew.Nanoseconds() {
		return 0, fmt.Errorf("Ping 响应时间戳偏差过大: %d", timestampDiff)
	}

	return time.Since(start), nil
}

// abs 返回 int64 的绝对值
func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// validateCallParams 验证 RPC 调用参数
func validateCallParams(peerID peer.ID, method string) error {
	if peerID == "" {
		return fmt.Errorf("无效的 peer ID")
	}
	if method == "" {
		return fmt.Errorf("方法名不能为空")
	}
	if len(method) > 256 {
		return fmt.Errorf("方法名过长（最大 256 字符）")
	}
	return nil
}

// nextRequestID 生成全局唯一的请求 ID
func nextRequestID() uint64 {
	id := atomic.AddUint64(&globalRequestID, 1)
	if id == 0 { // 溢出后回绕到 1
		atomic.CompareAndSwapUint64(&globalRequestID, 0, 1)
		return 1
	}
	return id
}

// ========================================
// 日志辅助函数
// ========================================

func (c *Client) logRequestSent(stream network.Stream, req *RPCRequest, method string, payloadSize int) {
	logging.WithFields(map[string]any{
		"method":       method,
		"request_id":   req.RequestID,
		"remote_peer":  stream.Conn().RemotePeer(),
		"payload_size": payloadSize,
	}).Debug("RPC 请求已发送")
}

func (c *Client) logResponseReceived(stream network.Stream, resp *RPCResponse) {
	logging.WithFields(map[string]any{
		"remote_peer": stream.Conn().RemotePeer(),
		"status":      resp.Status,
		"body_size":   len(resp.Body),
	}).Debug("RPC 响应已接收")
}

// ========================================
// 请求/响应类型定义
// ========================================

// PingRequest Ping 请求
type PingRequest struct {
	Timestamp int64 `msgpack:"timestamp"`
}

// PingResponse Ping 响应
type PingResponse struct {
	Timestamp int64 `msgpack:"timestamp"`
}
