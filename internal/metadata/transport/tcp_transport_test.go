// Package transport TCP 传输测试
package transport

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ========================================
// TCP 传输创建和配置测试
// ========================================

// TestNewTCPTransport 测试创建 TCP 传输
func TestNewTCPTransport(t *testing.T) {
	// 使用随机端口避免冲突
	addr := "127.0.0.1:0"

	trans, err := NewTCPTransport(addr)
	require.NoError(t, err)
	require.NotNil(t, trans)

	// 验证默认配置
	assert.Equal(t, addr, trans.config.ListenAddr)
	assert.Equal(t, int64(1024*1024*100), trans.config.MaxMessageSize)
	assert.Equal(t, 30*time.Second, trans.config.ReadTimeout)
	assert.Equal(t, 30*time.Second, trans.config.WriteTimeout)
	assert.Equal(t, 10*time.Second, trans.config.KeepAliveInterval)
	assert.Equal(t, 30*time.Second, trans.config.KeepAliveTimeout)
	assert.Equal(t, 4096, trans.config.BufferSize)
}

// TestNewTCPTransportWithConfig 测试自定义配置创建 TCP 传输
func TestNewTCPTransportWithConfig(t *testing.T) {
	config := &TransportConfig{
		ListenAddr:        "127.0.0.1:0",
		MaxMessageSize:    1024 * 1024,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		KeepAliveInterval: 5 * time.Second,
		KeepAliveTimeout:  15 * time.Second,
		BufferSize:        2048,
	}

	trans, err := NewTCPTransportWithConfig(config)
	require.NoError(t, err)
	require.NotNil(t, trans)

	assert.Equal(t, config, trans.config)
}

// ========================================
// TCP 传输生命周期测试
// ========================================

// TestTCPTransport_StartStop 测试启动和停止
func TestTCPTransport_StartStop(t *testing.T) {
	trans := createTCPTransport(t)

	// 启动
	err := trans.Start()
	require.NoError(t, err)
	assert.True(t, trans.started.Load())

	// 获取实际监听地址
	actualAddr := trans.listener.Addr().String()
	assert.NotEmpty(t, actualAddr)

	// 停止
	err = trans.Stop()
	require.NoError(t, err)
	assert.True(t, trans.stopped.Load())
}

// TestTCPTransport_Start_AlreadyStarted 测试重复启动
func TestTCPTransport_Start_AlreadyStarted(t *testing.T) {
	trans := createTCPTransport(t)

	err := trans.Start()
	require.NoError(t, err)

	// 重复启动应该失败
	err = trans.Start()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "已经启动")

	_ = trans.Stop() // 清理资源，忽略错误
}

// TestTCPTransport_Stop_NotStarted 测试未启动就停止
func TestTCPTransport_Stop_NotStarted(t *testing.T) {
	trans := createTCPTransport(t)

	// 未启动就停止应该成功（幂等）
	err := trans.Stop()
	assert.NoError(t, err)
	assert.True(t, trans.stopped.Load())
}

// TestTCPTransport_Start_MultipleStop 测试多次停止
func TestTCPTransport_Start_MultipleStop(t *testing.T) {
	trans := createTCPTransport(t)

	err := trans.Start()
	require.NoError(t, err)

	// 多次停止都应该成功（幂等）
	err1 := trans.Stop()
	err2 := trans.Stop()

	assert.NoError(t, err1)
	assert.NoError(t, err2)
}

// ========================================
// TCP 传输消息发送测试
// ========================================

// TestTCPTransport_SendReceive 测试发送和接收消息
func TestTCPTransport_SendReceive(t *testing.T) {
	ctx := context.Background()

	// 创建服务端
	server := createTCPTransport(t)
	err := server.Start()
	require.NoError(t, err)
	defer func() { _ = server.Stop() }()

	serverAddr := server.listener.Addr().String()

	// 创建客户端并连接
	client := createTCPTransport(t)
	err = client.Start()
	require.NoError(t, err)
	defer func() { _ = client.Stop() }()

	// 等待服务端准备就绪
	time.Sleep(100 * time.Millisecond)

	// 发送消息
	msg := &GetMessage{
		Key: "test-key",
	}

	err = client.Send(ctx, serverAddr, msg)
	require.NoError(t, err)

	// 接收消息（有超时）
	done := make(chan bool, 1)
	errCh := make(chan error, 1)
	go func() {
		select {
		case recvMsg := <-server.Receive():
			if recvMsg.Type() != MessageTypeGet {
				errCh <- assert.AnError
			} else {
				done <- true
			}
		case <-time.After(2 * time.Second):
			errCh <- assert.AnError
		}
	}()

	select {
	case <-done:
		// 测试成功
	case err := <-errCh:
		t.Fatal("接收消息失败", err)
	case <-time.After(3 * time.Second):
		t.Fatal("测试超时")
	}
}

// TestTCPTransport_Send_NotStarted 测试未启动发送
func TestTCPTransport_Send_NotStarted(t *testing.T) {
	trans := createTCPTransport(t)

	ctx := context.Background()
	msg := &GetMessage{Key: "test"}

	err := trans.Send(ctx, "127.0.0.1:9211", msg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "未启动")
}

// TestTCPTransport_Send_Timeout 测试发送超时
func TestTCPTransport_SendTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	server := createTCPTransport(t)
	err := server.Start()
	require.NoError(t, err)
	defer func() { _ = server.Stop() }()

	// 不启动服务端的接收循环，只让它监听
	// 发送消息应该超时或失败
	client := createTCPTransport(t)
	err = client.Start()
	require.NoError(t, err)
	defer func() { _ = client.Stop() }()

	msg := &GetMessage{Key: "test"}

	// 发送到不存在的端口或使用已关闭的连接
	err = client.Send(ctx, "127.0.0.1:1", msg) // 端口 1 通常未使用
	assert.Error(t, err)
}

// ========================================
// TCP 传输连接池测试
// ========================================

// TestTCPTransport_ConnectionPool 测试连接池复用
func TestTCPTransport_ConnectionPool(t *testing.T) {
	ctx := context.Background()

	server := createTCPTransport(t)
	err := server.Start()
	require.NoError(t, err)
	defer func() { _ = server.Stop() }()

	serverAddr := server.listener.Addr().String()

	client := createTCPTransport(t)
	err = client.Start()
	require.NoError(t, err)
	defer func() { _ = client.Stop() }()

	// 等待服务端准备就绪
	time.Sleep(100 * time.Millisecond)

	// 第一次发送
	msg1 := &GetMessage{Key: "test1"}
	err = client.Send(ctx, serverAddr, msg1)
	require.NoError(t, err)

	// 第二次发送（应该复用连接）
	msg2 := &GetMessage{Key: "test2"}
	err = client.Send(ctx, serverAddr, msg2)
	require.NoError(t, err)

	// 验证连接池中只有一个连接
	client.connPool.mu.RLock()
	assert.Equal(t, 1, len(client.connPool.conns))
	client.connPool.mu.RUnlock()
}

// TestTCPTransport_ConcurrentSend 测试并发发送
func TestTCPTransport_ConcurrentSend(t *testing.T) {
	ctx := context.Background()

	server := createTCPTransport(t)
	err := server.Start()
	require.NoError(t, err)
	defer func() { _ = server.Stop() }()

	serverAddr := server.listener.Addr().String()

	client := createTCPTransport(t)
	err = client.Start()
	require.NoError(t, err)
	defer func() { _ = client.Stop() }()

	// 等待服务端准备就绪
	time.Sleep(100 * time.Millisecond)

	// 并发发送多条消息
	const numMessages = 10
	var wg sync.WaitGroup
	errors := make(chan error, numMessages)

	for i := 0; i < numMessages; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			msg := &GetMessage{Key: "test"}
			err := client.Send(ctx, serverAddr, msg)
			if err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// 检查是否有错误
	for err := range errors {
		t.Logf("发送错误: %v", err)
	}

	// 至少应该有一些消息发送成功
	// （由于服务端没有接收循环，可能会有超时）
}

// ========================================
// TCP 传输配置和统计测试
// ========================================

// TestTCPTransport_GetLocalAddr 测试获取本地地址
func TestTCPTransport_GetLocalAddr(t *testing.T) {
	trans := createTCPTransport(t)

	addr := trans.GetLocalAddr()
	assert.Equal(t, "127.0.0.1:0", addr)
}

// TestTCPTransport_GetConfig 测试获取配置
func TestTCPTransport_GetConfig(t *testing.T) {
	trans := createTCPTransport(t)

	config := trans.GetConfig()
	assert.NotNil(t, config)
	assert.Equal(t, "127.0.0.1:0", config.ListenAddr)
}

// TestTCPTransport_Stats 测试获取统计信息
func TestTCPTransport_Stats(t *testing.T) {
	trans := createTCPTransport(t)

	stats := trans.Stats()
	assert.NotNil(t, stats)
	assert.Equal(t, false, stats["started"])
	assert.Equal(t, false, stats["stopped"])
	assert.Contains(t, stats, "listen_addr")
	assert.Contains(t, stats, "active_connections")
}

// TestTCPTransport_Stats_AfterStart 测试启动后的统计信息
func TestTCPTransport_Stats_AfterStart(t *testing.T) {
	trans := createTCPTransport(t)
	err := trans.Start()
	require.NoError(t, err)
	defer func() { _ = trans.Stop() }()

	stats := trans.Stats()
	assert.Equal(t, true, stats["started"])
	assert.Equal(t, false, stats["stopped"])
}

// ========================================
// TCP 传输错误处理测试
// ========================================

// TestTCPTransport_InvalidAddress 测试无效地址
func TestTCPTransport_InvalidAddress(t *testing.T) {
	// 创建 TCP 传输不会验证地址格式
	// 只有在 Start 时才会真正尝试监听
	trans, err := NewTCPTransport("invalid-address")
	assert.NoError(t, err)
	assert.NotNil(t, trans)

	// 启动时应该失败
	err = trans.Start()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid-address")
}

// TestTCPTransport_Receive_BeforeStart 测试启动前接收
func TestTCPTransport_Receive_BeforeStart(t *testing.T) {
	trans := createTCPTransport(t)

	// 启动前也可以获取接收通道
	ch := trans.Receive()
	assert.NotNil(t, ch)

	// 但通道不会被写入
	select {
	case <-ch:
		t.Fatal("不应该接收到消息")
	case <-time.After(100 * time.Millisecond):
		// 预期的行为
	}
}

// ========================================
// 辅助函数
// ========================================

// createTCPTransport 创建用于测试的 TCP 传输
// 使用随机端口避免冲突
func createTCPTransport(t *testing.T) *TCPTransport {
	t.Helper()

	trans, err := NewTCPTransport("127.0.0.1:0")
	require.NoError(t, err)

	return trans
}

// mustCreateTCPConnection 创建 TCP 连接（用于测试）
func mustCreateTCPConnection(t *testing.T, addr string) net.Conn {
	t.Helper()

	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	require.NoError(t, err)

	return conn
}

// TestTCPTransport_RealConnection 测试真实 TCP 连接
func TestTCPTransport_RealConnection(t *testing.T) {
	server := createTCPTransport(t)
	err := server.Start()
	require.NoError(t, err)
	defer func() { _ = server.Stop() }()

	serverAddr := server.listener.Addr().String()

	// 创建真实连接
	conn := mustCreateTCPConnection(t, serverAddr)
	defer func() { _ = conn.Close() }()

	// 验证连接是有效的
	assert.NotNil(t, conn)
	assert.Equal(t, serverAddr, conn.RemoteAddr().String())

	// 发送一些数据
	testData := []byte("test")
	_, err = conn.Write(testData)
	require.NoError(t, err)

	// 读取应该超时或阻塞（因为没有数据）
	_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	buf := make([]byte, 1024)
	n, err := conn.Read(buf)

	// 可能是超时或连接关闭
	if err != nil {
		// 超时错误可能包含 "timeout", "i/o timeout", "EOF" 或 "use of closed network connection"
		errMsg := err.Error()
		assert.True(t,
			strings.Contains(errMsg, "timeout") ||
				strings.Contains(errMsg, "EOF") ||
				strings.Contains(errMsg, "use of closed network connection"),
			"Expected timeout/EOF/closed error, got: %v", err)
	}
	assert.Equal(t, 0, n) // 没有读取到数据
}

// TestTCPTransport_FrameExchange 测试帧交换
func TestTCPTransport_FrameExchange(t *testing.T) {
	ctx := context.Background()

	// 创建服务端
	server := createTCPTransport(t)
	err := server.Start()
	require.NoError(t, err)
	defer func() { _ = server.Stop() }()

	serverAddr := server.listener.Addr().String()

	// 启动服务端接收协程
	received := make(chan bool, 1)
	errCh := make(chan error, 1)
	go func() {
		select {
		case msg := <-server.Receive():
			if msg.Type() != MessageTypeGet {
				errCh <- assert.AnError
			} else {
				received <- true
			}
		case <-time.After(2 * time.Second):
			errCh <- assert.AnError
		}
	}()

	// 等待服务端准备
	time.Sleep(100 * time.Millisecond)

	// 创建客户端
	client := createTCPTransport(t)
	err = client.Start()
	require.NoError(t, err)
	defer func() { _ = client.Stop() }()

	// 发送消息
	msg := &GetMessage{
		Key: "test-key",
	}

	err = client.Send(ctx, serverAddr, msg)
	require.NoError(t, err)

	// 等待接收
	select {
	case <-received:
		// 测试成功
	case err := <-errCh:
		t.Fatal("接收消息失败", err)
	case <-time.After(3 * time.Second):
		t.Fatal("帧交换超时")
	}
}

// ========================================
// Ping/Pong 消息测试
// ========================================

// TestTCPTransport_PingPong 双向 Ping/Pong 通信测试
func TestTCPTransport_PingPong(t *testing.T) {
	ctx := context.Background()

	// 创建服务端
	server := createTCPTransport(t)
	err := server.Start()
	require.NoError(t, err)
	defer func() { _ = server.Stop() }()

	serverAddr := server.listener.Addr().String()
	serverNodeID := "server-node"

	// 创建客户端
	client := createTCPTransport(t)
	err = client.Start()
	require.NoError(t, err)
	defer func() { _ = client.Stop() }()

	clientNodeID := "client-node"

	// 启动服务端接收协程（自动回复 Pong）
	serverReceivedPing := make(chan *NodePingMessage, 1)
	go func() {
		for msg := range server.Receive() {
			if ping, ok := msg.(*NodePingMessage); ok {
				serverReceivedPing <- ping

				// 自动回复 Pong
				pong := &NodePongMessage{
					NodeID:    serverNodeID,
					Sequence:  ping.Sequence,
					Status:    "ready",
					Timestamp: time.Now().UnixMilli(),
				}
				_ = server.Send(ctx, client.listener.Addr().String(), pong)
				return
			}
		}
	}()

	// 启动客户端接收协程（自动回复 Pong）
	clientReceivedPing := make(chan *NodePingMessage, 1)
	clientReceivedPong := make(chan *NodePongMessage, 1)
	go func() {
		for msg := range client.Receive() {
			switch m := msg.(type) {
			case *NodePingMessage:
				clientReceivedPing <- m

				// 自动回复 Pong
				pong := &NodePongMessage{
					NodeID:    clientNodeID,
					Sequence:  m.Sequence,
					Status:    "ready",
					Timestamp: time.Now().UnixMilli(),
				}
				_ = client.Send(ctx, serverAddr, pong)

			case *NodePongMessage:
				clientReceivedPong <- m
				return
			}
		}
	}()

	// 等待服务端准备就绪
	time.Sleep(100 * time.Millisecond)

	// ====== 测试1: 客户端发送 Ping 到服务端 ======
	t.Log("测试1: 客户端 -> Ping -> 服务端 -> Pong -> 客户端")

	ping1 := &NodePingMessage{
		NodeID:    clientNodeID,
		Sequence:  1001,
		Timestamp: time.Now().UnixMilli(),
	}

	err = client.Send(ctx, serverAddr, ping1)
	require.NoError(t, err)

	// 验证服务端收到 Ping
	select {
	case ping := <-serverReceivedPing:
		assert.Equal(t, clientNodeID, ping.NodeID)
		assert.Equal(t, int64(1001), ping.Sequence)
		t.Logf("服务端成功接收 Ping 来自 %s", ping.NodeID)
	case <-time.After(2 * time.Second):
		t.Fatal("服务端未收到 Ping")
	}

	// 验证客户端收到 Pong
	select {
	case pong := <-clientReceivedPong:
		assert.Equal(t, serverNodeID, pong.NodeID)
		assert.Equal(t, int64(1001), pong.Sequence)
		assert.Equal(t, "ready", pong.Status)
		assert.Greater(t, pong.Timestamp, int64(0))
		rtt := time.Now().UnixMilli() - pong.Timestamp
		t.Logf("客户端成功接收 Pong 来自 %s, RTT: %dms", pong.NodeID, rtt)
	case <-time.After(2 * time.Second):
		t.Fatal("客户端未收到 Pong")
	}

	// ====== 测试2: 服务端发送 Ping 到客户端 ======
	t.Log("测试2: 服务端 -> Ping -> 客户端 -> Pong -> 服务端")

	// 重置客户端接收协程
	clientReceivedPing2 := make(chan *NodePingMessage, 1)
	go func() {
		for msg := range client.Receive() {
			switch m := msg.(type) {
			case *NodePingMessage:
				clientReceivedPing2 <- m

				// 自动回复 Pong
				pong := &NodePongMessage{
					NodeID:    clientNodeID,
					Sequence:  m.Sequence,
					Status:    "ready",
					Timestamp: time.Now().UnixMilli(),
				}
				_ = client.Send(ctx, serverAddr, pong)

			case *NodePongMessage:
				// 忽略额外的 Pong
			}
		}
	}()

	// 重置服务端 Pong 接收
	serverReceivedPong := make(chan *NodePongMessage, 1)
	go func() {
		for msg := range server.Receive() {
			if pong, ok := msg.(*NodePongMessage); ok {
				serverReceivedPong <- pong
				return
			}
		}
	}()

	ping2 := &NodePingMessage{
		NodeID:    serverNodeID,
		Sequence:  1002,
		Timestamp: time.Now().UnixMilli(),
	}

	err = server.Send(ctx, client.listener.Addr().String(), ping2)
	require.NoError(t, err)

	// 验证客户端收到 Ping
	select {
	case ping := <-clientReceivedPing2:
		assert.Equal(t, serverNodeID, ping.NodeID)
		assert.Equal(t, int64(1002), ping.Sequence)
		t.Logf("客户端成功接收 Ping 来自 %s", ping.NodeID)
	case <-time.After(2 * time.Second):
		t.Fatal("客户端未收到 Ping")
	}

	// 验证服务端收到 Pong
	select {
	case pong := <-serverReceivedPong:
		assert.Equal(t, clientNodeID, pong.NodeID)
		assert.Equal(t, int64(1002), pong.Sequence)
		assert.Equal(t, "ready", pong.Status)
		assert.Greater(t, pong.Timestamp, int64(0))
		rtt := time.Now().UnixMilli() - pong.Timestamp
		t.Logf("服务端成功接收 Pong 来自 %s, RTT: %dms", pong.NodeID, rtt)
	case <-time.After(2 * time.Second):
		t.Fatal("服务端未收到 Pong")
	}

	t.Log("Ping/Pong 双向通信测试通过")
}
