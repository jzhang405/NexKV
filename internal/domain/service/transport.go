// Package service 定义领域服务接口
package service

import (
	"context"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// Transport 传输层核心接口
type Transport interface {
	// Self 返回本地节点 ID
	Self() model.PeerID

	// Connect 连接到指定地址的节点
	Connect(ctx context.Context, addr string) (model.PeerID, error)

	// Disconnect 断开与指定节点的连接
	Disconnect(peer model.PeerID) error

	// ConnectedPeers 返回当前已连接的节点列表
	ConnectedPeers() []model.PeerID

	// IsConnected 检查是否与指定节点已连接
	IsConnected(peer model.PeerID) bool

	// OpenStream 打开到指定节点的流式连接
	OpenStream(ctx context.Context, peer model.PeerID, protocol string) (Stream, error)

	// AcceptStream 接受指定协议的入站流
	AcceptStream(protocol string) (Stream, error)

	// OpenChannel 打开到指定节点的双向通道
	OpenChannel(ctx context.Context, peer model.PeerID, protocol string) (Channel, error)

	// OpenAsyncChannel 打开到指定节点的异步双向通道
	OpenAsyncChannel(ctx context.Context, peer model.PeerID, protocol string) (AsyncChannel, error)

	// OpenAsyncStream 打开到指定节点的异步流
	OpenAsyncStream(ctx context.Context, peer model.PeerID, protocol string) (AsyncStream, error)

	// Close 关闭传输层
	Close() error
}

// Stream 流式通信接口
type Stream interface {
	// ID 返回流 ID
	ID() string

	// Protocol 返回协议名称
	Protocol() string

	// RemotePeer 返回远程节点 ID
	RemotePeer() model.PeerID

	// Read 读取数据
	Read(p []byte) (n int, err error)

	// Write 写入数据
	Write(p []byte) (n int, err error)

	// Close 关闭流
	Close() error

	// SetReadDeadline 设置读超时
	SetReadDeadline(t interface{ UnixNano() int64 }) error

	// SetWriteDeadline 设置写超时
	SetWriteDeadline(t interface{ UnixNano() int64 }) error

	// Reset 重置流（发送 RST）
	Reset() error
}

// Channel 双向通道接口
type Channel interface {
	// Send 发送消息
	Send(ctx context.Context, msg []byte) error

	// Recv 接收消息
	Recv(ctx context.Context) ([]byte, error)

	// Close 关闭通道
	Close() error
}

// MsgOrError 消息或错误（用于异步接收）
type MsgOrError struct {
	Msg []byte
	Err error
}

// AsyncChannel 异步双向通道接口（Go Channel 风格）
//
// 使用示例:
//
//	ch := transport.OpenAsyncChannel(ctx, peer, protocol)
//
//	// 发送消息
//	ch.SendChan() <- []byte("hello")
//
//	// 接收消息
//	select {
//	case msg := <-ch.RecvChan():
//	    if msg.Err != nil {
//	        // 处理错误
//	    }
//	    // 处理消息 msg.Msg
//	case <-ctx.Done():
//	    // 超时
//	}
//
//	// 关闭
//	ch.Close()
type AsyncChannel interface {
	// SendChan 返回发送通道
	// 向此 channel 写入消息会异步发送到对端
	// 注意：channel 关闭由 Close() 触发，用户无法主动关闭
	SendChan() chan<- []byte

	// RecvChan 返回接收通道
	// 从此 channel 读取 MsgOrError 可获取对端消息
	// channel 关闭时表示连接断开
	RecvChan() <-chan MsgOrError

	// Close 关闭通道
	// 会取消所有等待中的操作
	Close() error

	// WaitClosed 等待通道关闭
	// 返回发送过程中遇到的错误（如果有）
	// 注意：此方法会阻塞直到 Close() 被调用
	// 推荐使用 WaitClosedWithTimeout 避免永久阻塞
	WaitClosed() error

	// WaitClosedWithTimeout 带超时的等待通道关闭
	WaitClosedWithTimeout(timeout time.Duration) error
}

// ReadResult 异步读取结果
type ReadResult struct {
	Data []byte
	Err  error
}

// WriteRequest 异步写入请求
type WriteRequest struct {
	Data []byte
	Err  chan error // 写入完成后发送错误（nil 表示成功）
	// 注意：Err 可以为 nil（不等待结果）
	// 如果非 nil，推荐使用带缓冲的 channel: make(chan error, 1)
}

// AsyncStream 异步流接口（Go Channel 风格）
//
// 使用示例:
//
//	s := transport.OpenAsyncStream(ctx, peer, protocol)
//
//	// 写入数据（带确认）
//	errCh := make(chan error, 1) // 推荐：带缓冲
//	s.WriteChan() <- WriteRequest{Data: []byte("hello"), Err: errCh}
//	select {
//	case err := <-errCh:
//	    // 写入完成
//	case <-time.After(time.Second):
//	    // 超时
//	}
//
//	// 写入数据（不等待确认）
//	s.WriteChan() <- WriteRequest{Data: []byte("hello")}
//
//	// 读取数据
//	select {
//	case result := <-s.ReadChan():
//	    if result.Err != nil {
//	        // 处理错误
//	    }
//	    // 处理数据 result.Data
//	case <-ctx.Done():
//	}
//
//	// 关闭
//	s.Close()
type AsyncStream interface {
	// ReadChan 返回读取通道
	// 从此 channel 读取 ReadResult 可获取数据
	// channel 关闭时表示流结束（EOF）或错误
	ReadChan() <-chan ReadResult

	// WriteChan 返回写入通道
	// 向此 channel 写入 WriteRequest 会异步写入数据
	// 如果 WriteRequest.Err 非 nil，写入完成后会收到结果
	WriteChan() chan<- WriteRequest

	// Close 关闭流
	// 会取消所有等待中的操作
	Close() error

	// WaitClosed 等待流关闭
	// 返回写入过程中遇到的错误（如果有）
	// 注意：此方法会阻塞直到 Close() 被调用
	// 推荐使用 WaitClosedWithTimeout 避免永久阻塞
	WaitClosed() error

	// WaitClosedWithTimeout 带超时的等待流关闭
	WaitClosedWithTimeout(timeout time.Duration) error
}
