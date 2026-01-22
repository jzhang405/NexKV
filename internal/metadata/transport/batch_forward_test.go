// Package transport 批量转发单元测试
package transport

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ========================================
// TCP BatchForwardMessage 测试
// ========================================

func TestBatchForwardMessage_TCP_NotStarted(t *testing.T) {
	ctx := context.Background()
	msgExt := MsgExt{
		Message:  NewBaseMessage(MessageTypeGet, []byte("test")),
		HopCount: &HopExt{Hop: 5, TotalHop: 10},
	}
	addrs := []string{"127.0.0.1:9999", "127.0.0.1:9998"}

	tcpTransport, err := NewTCPTransport("127.0.0.1:0")
	require.NoError(t, err)
	tcpTransport.SetNodeID(12345)
	// 不启动 Transport

	result := tcpTransport.BatchForwardMessage(ctx, addrs, msgExt)

	assert.Equal(t, 0, result.SuccessCount)
	assert.Equal(t, 2, result.FailureCount)
	assert.Len(t, result.Results, 2)
}

func TestBatchForwardMessage_TCP_EmptyAddrs(t *testing.T) {
	ctx := context.Background()
	msgExt := MsgExt{
		Message:  NewBaseMessage(MessageTypeGet, []byte("test")),
		HopCount: &HopExt{Hop: 5, TotalHop: 10},
	}
	addrs := []string{}

	tcpTransport, err := NewTCPTransport("127.0.0.1:0")
	require.NoError(t, err)
	tcpTransport.SetNodeID(12345)
	require.NoError(t, tcpTransport.Start())
	defer func() { _ = tcpTransport.Stop() }()

	result := tcpTransport.BatchForwardMessage(ctx, addrs, msgExt)

	assert.Equal(t, 0, result.SuccessCount)
	assert.Equal(t, 0, result.FailureCount)
	assert.Len(t, result.Results, 0)
}

func TestBatchForwardMessage_TCP_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	msgExt := MsgExt{
		Message:  NewBaseMessage(MessageTypeGet, []byte("test")),
		HopCount: &HopExt{Hop: 5, TotalHop: 10},
	}
	addrs := []string{"127.0.0.1:9999", "127.0.0.1:9998"}

	tcpTransport, err := NewTCPTransport("127.0.0.1:0")
	require.NoError(t, err)
	tcpTransport.SetNodeID(12345)
	require.NoError(t, tcpTransport.Start())
	defer func() { _ = tcpTransport.Stop() }()

	result := tcpTransport.BatchForwardMessage(ctx, addrs, msgExt)

	// 所有请求应该失败
	assert.Equal(t, 0, result.SuccessCount)
	assert.Equal(t, 2, result.FailureCount)

	// 检查错误信息
	for _, r := range result.Results {
		assert.Error(t, r.Error)
		assert.Contains(t, r.Error.Error(), "context canceled")
	}
}

func TestBatchForwardMessage_TCP_PartialFailure(t *testing.T) {
	ctx := context.Background()
	msgExt := MsgExt{
		Message:  NewBaseMessage(MessageTypeGet, []byte("test")),
		HopCount: &HopExt{Hop: 5, TotalHop: 10},
	}
	addrs := []string{"127.0.0.1:9999", "127.0.0.1:9998"}

	tcpTransport, err := NewTCPTransport("127.0.0.1:0")
	require.NoError(t, err)
	tcpTransport.SetNodeID(12345)
	require.NoError(t, tcpTransport.Start())
	defer func() { _ = tcpTransport.Stop() }()

	result := tcpTransport.BatchForwardMessage(ctx, addrs, msgExt)

	// 部分失败（连接失败）
	assert.Equal(t, 0, result.SuccessCount)
	assert.Equal(t, 2, result.FailureCount)
	assert.Len(t, result.Results, 2)

	// 验证结果结构
	for i, r := range result.Results {
		assert.Equal(t, addrs[i], r.Addr)
		assert.Error(t, r.Error)
		assert.Equal(t, uint32(0), r.SeqID)
	}
}

func TestBatchForwardMessage_TCP_MaxBatchSize(t *testing.T) {
	ctx := context.Background()
	msgExt := MsgExt{
		Message:  NewBaseMessage(MessageTypeGet, []byte("test")),
		HopCount: &HopExt{Hop: 5, TotalHop: 10},
	}

	// 创建超过 maxBatchSize 的地址列表
	addrs := make([]string, maxBatchSize+10)
	for i := range addrs {
		addrs[i] = fmt.Sprintf("127.0.0.1:%d", 9999+i)
	}

	tcpTransport, err := NewTCPTransport("127.0.0.1:0")
	require.NoError(t, err)
	tcpTransport.SetNodeID(12345)
	require.NoError(t, tcpTransport.Start())
	defer func() { _ = tcpTransport.Stop() }()

	result := tcpTransport.BatchForwardMessage(ctx, addrs, msgExt)

	// 应该只处理前 maxBatchSize 个地址
	assert.Len(t, result.Results, maxBatchSize)
}

func TestBatchForwardMessage_TCP_ConcurrentSafe(t *testing.T) {
	ctx := context.Background()
	msgExt := MsgExt{
		Message:  NewBaseMessage(MessageTypeGet, []byte("test")),
		HopCount: &HopExt{Hop: 5, TotalHop: 10},
	}
	addrs := []string{"127.0.0.1:9999", "127.0.0.1:9998"}

	tcpTransport, err := NewTCPTransport("127.0.0.1:0")
	require.NoError(t, err)
	tcpTransport.SetNodeID(12345)
	require.NoError(t, tcpTransport.Start())
	defer func() { _ = tcpTransport.Stop() }()

	// 并发调用
	var wg sync.WaitGroup
	results := make(chan BatchForwardMessageResult, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := tcpTransport.BatchForwardMessage(ctx, addrs, msgExt)
			results <- result
		}()
	}

	wg.Wait()
	close(results)

	// 验证所有调用都返回了结果
	count := 0
	for range results {
		count++
	}
	assert.Equal(t, 10, count)
}

// ========================================
// UDP BatchForwardMessage 测试
// ========================================

func TestBatchForwardMessage_UDP_NotStarted(t *testing.T) {
	ctx := context.Background()
	msgExt := MsgExt{
		Message:  NewBaseMessage(MessageTypeGet, []byte("test")),
		HopCount: &HopExt{Hop: 5, TotalHop: 10},
	}
	addrs := []string{"127.0.0.1:9999", "127.0.0.1:9998"}

	udpTransport, err := NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	udpTransport.SetNodeID(12345)
	// 不启动 Transport

	result := udpTransport.BatchForwardMessage(ctx, addrs, msgExt)

	assert.Equal(t, 0, result.SuccessCount)
	assert.Equal(t, 2, result.FailureCount)
	assert.Len(t, result.Results, 2)
}

func TestBatchForwardMessage_UDP_EmptyAddrs(t *testing.T) {
	ctx := context.Background()
	msgExt := MsgExt{
		Message:  NewBaseMessage(MessageTypeGet, []byte("test")),
		HopCount: &HopExt{Hop: 5, TotalHop: 10},
	}
	addrs := []string{}

	udpTransport, err := NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	udpTransport.SetNodeID(12345)
	require.NoError(t, udpTransport.Start())
	defer func() { _ = udpTransport.Stop() }()

	result := udpTransport.BatchForwardMessage(ctx, addrs, msgExt)

	assert.Equal(t, 0, result.SuccessCount)
	assert.Equal(t, 0, result.FailureCount)
	assert.Len(t, result.Results, 0)
}

func TestBatchForwardMessage_UDP_Success(t *testing.T) {
	ctx := context.Background()
	msgExt := MsgExt{
		Message:  &GetMessage{Key: "test-key"},
		HopCount: &HopExt{Hop: 5, TotalHop: 10},
	}

	// 创建 UDP 监听器
	serverConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = serverConn.Close() }()

	serverAddr := serverConn.LocalAddr().String()
	addrs := []string{serverAddr}

	udpTransport, err := NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	udpTransport.SetNodeID(12345)
	require.NoError(t, udpTransport.Start())
	defer func() { _ = udpTransport.Stop() }()

	// 启动接收 goroutine
	go func() {
		buf := make([]byte, 1024)
		for i := 0; i < len(addrs); i++ {
			_, _, _ = serverConn.ReadFrom(buf)
		}
	}()

	result := udpTransport.BatchForwardMessage(ctx, addrs, msgExt)

	// UDP 发送不等待响应，应该都"成功"
	assert.Equal(t, 1, result.SuccessCount)
	assert.Equal(t, 0, result.FailureCount)
	assert.Len(t, result.Results, 1)

	// 验证结果
	r := result.Results[0]
	assert.Equal(t, serverAddr, r.Addr)
	assert.NoError(t, r.Error)
	assert.NotEqual(t, uint32(0), r.SeqID)
}

func TestBatchForwardMessage_UDP_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	msgExt := MsgExt{
		Message:  NewBaseMessage(MessageTypeGet, []byte("test")),
		HopCount: &HopExt{Hop: 5, TotalHop: 10},
	}
	addrs := []string{"127.0.0.1:9999", "127.0.0.1:9998"}

	udpTransport, err := NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	udpTransport.SetNodeID(12345)
	require.NoError(t, udpTransport.Start())
	defer func() { _ = udpTransport.Stop() }()

	result := udpTransport.BatchForwardMessage(ctx, addrs, msgExt)

	// 所有请求应该失败
	assert.Equal(t, 0, result.SuccessCount)
	assert.Equal(t, 2, result.FailureCount)

	// 检查错误信息
	for _, r := range result.Results {
		assert.Error(t, r.Error)
		assert.Contains(t, r.Error.Error(), "context canceled")
	}
}

func TestBatchForwardMessage_UDP_MaxBatchSize(t *testing.T) {
	ctx := context.Background()
	msgExt := MsgExt{
		Message:  NewBaseMessage(MessageTypeGet, []byte("test")),
		HopCount: &HopExt{Hop: 5, TotalHop: 10},
	}

	// 创建超过 maxBatchSize 的地址列表
	addrs := make([]string, maxBatchSize+10)
	for i := range addrs {
		addrs[i] = fmt.Sprintf("127.0.0.1:%d", 9999+i)
	}

	udpTransport, err := NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	udpTransport.SetNodeID(12345)
	require.NoError(t, udpTransport.Start())
	defer func() { _ = udpTransport.Stop() }()

	result := udpTransport.BatchForwardMessage(ctx, addrs, msgExt)

	// 应该只处理前 maxBatchSize 个地址
	assert.Len(t, result.Results, maxBatchSize)
}

func TestBatchForwardMessage_UDP_ConcurrentSafe(t *testing.T) {
	ctx := context.Background()
	msgExt := MsgExt{
		Message:  NewBaseMessage(MessageTypeGet, []byte("test")),
		HopCount: &HopExt{Hop: 5, TotalHop: 10},
	}
	addrs := []string{"127.0.0.1:9999", "127.0.0.1:9998"}

	udpTransport, err := NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	udpTransport.SetNodeID(12345)
	require.NoError(t, udpTransport.Start())
	defer func() { _ = udpTransport.Stop() }()

	// 并发调用
	var wg sync.WaitGroup
	results := make(chan BatchForwardMessageResult, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := udpTransport.BatchForwardMessage(ctx, addrs, msgExt)
			results <- result
		}()
	}

	wg.Wait()
	close(results)

	// 验证所有调用都返回了结果
	count := 0
	for range results {
		count++
	}
	assert.Equal(t, 10, count)
}

// ========================================
// 集成测试
// ========================================

func TestBatchForwardMessage_Integration_TCP(t *testing.T) {
	ctx := context.Background()
	msgExt := MsgExt{
		Message:  &GetMessage{Key: "test-key"},
		HopCount: &HopExt{Hop: 5, TotalHop: 10},
	}

	// 创建 TCP 监听器
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()

	serverAddr := listener.Addr().String()
	addrs := []string{serverAddr}

	// 启动接收 goroutine
	go func() {
		for i := 0; i < len(addrs); i++ {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			buf := make([]byte, 1024)
			_, _ = conn.Read(buf)
			_ = conn.Close()
		}
	}()

	tcpTransport, err := NewTCPTransport("127.0.0.1:0")
	require.NoError(t, err)
	tcpTransport.SetNodeID(12345)
	require.NoError(t, tcpTransport.Start())
	defer func() { _ = tcpTransport.Stop() }()

	// 等待服务器启动
	time.Sleep(100 * time.Millisecond)

	result := tcpTransport.BatchForwardMessage(ctx, addrs, msgExt)

	assert.Equal(t, 1, result.SuccessCount)
	assert.Equal(t, 0, result.FailureCount)
	assert.Len(t, result.Results, 1)

	r := result.Results[0]
	assert.Equal(t, serverAddr, r.Addr)
	assert.NoError(t, r.Error)
	assert.NotEqual(t, uint32(0), r.SeqID)
}

func TestBatchForwardMessage_Integration_UDP(t *testing.T) {
	ctx := context.Background()
	msgExt := MsgExt{
		Message:  &GetMessage{Key: "test-key"},
		HopCount: &HopExt{Hop: 5, TotalHop: 10},
	}

	// 创建 UDP 监听器
	serverConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = serverConn.Close() }()

	serverAddr := serverConn.LocalAddr().String()
	addrs := []string{serverAddr}

	// 启动接收 goroutine
	received := make(chan bool, 1)
	go func() {
		buf := make([]byte, 1024)
		_, _, _ = serverConn.ReadFrom(buf)
		received <- true
	}()

	udpTransport, err := NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	udpTransport.SetNodeID(12345)
	require.NoError(t, udpTransport.Start())
	defer func() { _ = udpTransport.Stop() }()

	result := udpTransport.BatchForwardMessage(ctx, addrs, msgExt)

	assert.Equal(t, 1, result.SuccessCount)
	assert.Equal(t, 0, result.FailureCount)
	assert.Len(t, result.Results, 1)

	r := result.Results[0]
	assert.Equal(t, serverAddr, r.Addr)
	assert.NoError(t, r.Error)
	assert.NotEqual(t, uint32(0), r.SeqID)

	// 等待接收完成
	select {
	case <-received:
		// 成功接收
	case <-time.After(1 * time.Second):
		t.Error("未接收到数据")
	}
}
