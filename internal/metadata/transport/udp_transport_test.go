// Package transport UDP 传输测试
package transport

import (
	"context"
	"crypto/md5"
	cryptorand "crypto/rand"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ========================================
// UDP 传输创建和配置测试
// ========================================

// TestNewUDPTransport 测试创建 UDP 传输
func TestNewUDPTransport(t *testing.T) {
	// 使用随机端口避免冲突
	addr := "127.0.0.1:0"

	trans, err := NewUDPTransport(addr)
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

// TestNewUDPTransportWithConfig 测试自定义配置创建 UDP 传输
func TestNewUDPTransportWithConfig(t *testing.T) {
	config := &TransportConfig{
		ListenAddr:        "127.0.0.1:0",
		MaxMessageSize:    1024 * 1024,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		KeepAliveInterval: 5 * time.Second,
		KeepAliveTimeout:  15 * time.Second,
		BufferSize:        2048,
	}

	trans, err := NewUDPTransportWithConfig(config)
	require.NoError(t, err)
	require.NotNil(t, trans)

	assert.Equal(t, config, trans.config)
}

// ========================================
// UDP 传输生命周期测试
// ========================================

// TestUDPTransport_StartStop 测试启动和停止
func TestUDPTransport_StartStop(t *testing.T) {
	trans := createUDPTransport(t)

	// 启动
	err := trans.Start()
	require.NoError(t, err)
	assert.True(t, trans.started.Load())

	// 获取实际监听地址
	actualAddr := trans.GetLocalAddr()
	assert.NotEmpty(t, actualAddr)

	// 停止
	err = trans.Stop()
	require.NoError(t, err)
	assert.True(t, trans.stopped.Load())
}

// TestUDPTransport_Start_AlreadyStarted 测试重复启动
func TestUDPTransport_Start_AlreadyStarted(t *testing.T) {
	trans := createUDPTransport(t)

	err := trans.Start()
	require.NoError(t, err)

	// 重复启动应该失败
	err = trans.Start()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "已经启动")

	_ = trans.Stop() // 清理资源，忽略错误
}

// TestUDPTransport_Stop_NotStarted 测试未启动就停止
func TestUDPTransport_Stop_NotStarted(t *testing.T) {
	trans := createUDPTransport(t)

	// 未启动就停止应该成功（幂等）
	err := trans.Stop()
	assert.NoError(t, err)
	assert.True(t, trans.stopped.Load())
}

// TestUDPTransport_MultipleStop 测试多次停止
func TestUDPTransport_MultipleStop(t *testing.T) {
	trans := createUDPTransport(t)

	err := trans.Start()
	require.NoError(t, err)

	// 多次停止都应该成功（幂等）
	err1 := trans.Stop()
	err2 := trans.Stop()

	assert.NoError(t, err1)
	assert.NoError(t, err2)
}

// ========================================
// UDP 传输消息发送测试
// ========================================

// TestUDPTransport_SendReceive 测试发送和接收消息
func TestUDPTransport_SendReceive(t *testing.T) {
	ctx := context.Background()

	client, server, serverAddr := setupTestPair(t)
	defer func() { _ = client.Stop() }()
	defer func() { _ = server.Stop() }()

	// 发送消息
	msg := &GetMessage{Key: "test-key"}
	err := client.Send(ctx, serverAddr, msg)
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

// TestUDPTransport_Send_NotStarted 测试未启动发送
func TestUDPTransport_Send_NotStarted(t *testing.T) {
	trans := createUDPTransport(t)

	ctx := context.Background()
	msg := &GetMessage{Key: "test"}

	err := trans.Send(ctx, "127.0.0.1:9211", msg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "未启动")
}

// ========================================
// UDP 分片重组测试
// ========================================

// TestUDPTransport_Fragmentation 测试大消息分片发送
func TestUDPTransport_Fragmentation(t *testing.T) {
	ctx := context.Background()

	client, server, serverAddr := setupTestPair(t)
	client.SetNodeID(1002)
	server.SetNodeID(1001)

	defer func() { _ = client.Stop() }()
	defer func() { _ = server.Stop() }()

	// 创建大消息（超过 MaxUDPPacketSize = 1400）
	largeValue := make([]byte, 2000) // 大于 1400
	for i := range largeValue {
		largeValue[i] = byte('a' + (i % 26))
	}

	msg := &PutMessage{
		Key:   "large-test-key",
		Value: largeValue,
	}

	// 发送大消息（应该自动分片）
	err := client.Send(ctx, serverAddr, msg)
	require.NoError(t, err)

	// 接收消息
	done := make(chan bool, 1)
	errCh := make(chan error, 1)
	go func() {
		select {
		case recvMsg := <-server.Receive():
			if recvMsg.Type() != MessageTypePut {
				errCh <- assert.AnError
			} else {
				// 验证消息内容
				putMsg, ok := recvMsg.Message.(*PutMessage)
				if !ok {
					errCh <- assert.AnError
				} else {
					assert.Equal(t, "large-test-key", putMsg.Key)
					assert.Equal(t, 2000, len(putMsg.Value))
					done <- true
				}
			}
		case <-time.After(5 * time.Second):
			errCh <- assert.AnError
		}
	}()

	select {
	case <-done:
		t.Log("大消息分片重组成功")
	case err := <-errCh:
		t.Fatal("接收大消息失败", err)
	case <-time.After(6 * time.Second):
		t.Fatal("测试超时")
	}
}

// TestUDPTransport_Fragmentation_Sizes 测试不同大小的消息分片
//
// 测试场景：
// 1. 小消息（<1400 字节）- 不分片
// 2. 中等消息（~2000 字节）- 2 个分片
// 3. 大消息（~10000 字节）- 8 个分片
// 4. 超大消息（~100KB）- 约 72 个分片
func TestUDPTransport_Fragmentation_Sizes(t *testing.T) {
	ctx := context.Background()

	testSizes := []struct {
		name     string
		size     int
		minFrags int
		maxFrags int
	}{
		{"小消息（不分片）", 1000, 1, 1},
		{"中等消息（2分片）", 2000, 2, 2},
		{"大消息（8分片）", 10000, 8, 8},
		{"超大消息（72分片）", 100 * 1024, 72, 72},
	}

	for _, tc := range testSizes {
		t.Run(tc.name, func(t *testing.T) {
			client, server, serverAddr := setupTestPair(t)
			client.SetNodeID(2000 + uint64(len(tc.name)))
			server.SetNodeID(3000 + uint64(len(tc.name)))

			defer func() { _ = client.Stop() }()
			defer func() { _ = server.Stop() }()

			// 创建指定大小的消息
			value := make([]byte, tc.size)
			for i := range value {
				value[i] = byte(i % 256)
			}

			msg := &PutMessage{
				Key:   fmt.Sprintf("frag-test-%s", tc.name),
				Value: value,
			}

			// 发送消息
			err := client.Send(ctx, serverAddr, msg)
			require.NoError(t, err)

			// 接收并验证
			receivedCh := make(chan *PutMessage, 1)
			errCh := make(chan error, 1)

			go func() {
				for recvMsg := range server.Receive() {
					if putMsg, ok := recvMsg.Message.(*PutMessage); ok {
						receivedCh <- putMsg
						return
					}
				}
			}()

			select {
			case putMsg := <-receivedCh:
				assert.Equal(t, tc.size, len(putMsg.Value), "数据长度不匹配")
				assert.Equal(t, value, putMsg.Value, "数据内容不匹配")
				t.Logf("✅ %s 测试通过: %d 字节", tc.name, tc.size)

			case err := <-errCh:
				t.Fatalf("接收失败: %v", err)

			case <-time.After(10 * time.Second):
				t.Fatalf("接收超时: %s", tc.name)
			}
		})
	}
}

// TestUDPTransport_Fragmentation_ByteBufferPrecision 测试字节缓冲区精确性
//
// 测试场景：
// 1. 发送包含精确字节数据的消息
// 2. 验证每个字节在分片和重组后完全一致
// 3. 测试边界值（1400, 1401, 2800, 2801 等）
func TestUDPTransport_Fragmentation_ByteBufferPrecision(t *testing.T) {
	ctx := context.Background()

	// 边界值测试
	boundarySizes := []int{
		1399, // 小于 MaxUDPPacketSize
		1400, // 等于 MaxUDPPacketSize
		1401, // 大于 MaxUDPPacketSize（2 个分片）
		2799, // 接近 2 个分片边界
		2800, // 2 个分片边界
		2801, // 超过 2 个分片（3 个分片）
	}

	for _, size := range boundarySizes {
		t.Run(fmt.Sprintf("size_%d", size), func(t *testing.T) {
			client, server, serverAddr := setupTestPair(t)
			client.SetNodeID(5000 + uint64(size))
			server.SetNodeID(6000 + uint64(size))

			defer func() { _ = client.Stop() }()
			defer func() { _ = server.Stop() }()

			// 创建精确字节数据
			value := make([]byte, size)
			for i := range value {
				value[i] = byte(i % 256)
			}

			msg := &PutMessage{
				Key:   fmt.Sprintf("precision-test-%d", size),
				Value: value,
			}

			// 发送消息
			err := client.Send(ctx, serverAddr, msg)
			require.NoError(t, err)

			// 接收并精确验证
			receivedCh := make(chan *PutMessage, 1)

			go func() {
				for recvMsg := range server.Receive() {
					if putMsg, ok := recvMsg.Message.(*PutMessage); ok {
						receivedCh <- putMsg
						return
					}
				}
			}()

			select {
			case putMsg := <-receivedCh:
				assert.Equal(t, size, len(putMsg.Value), "数据长度不匹配")

				// 逐字节验证
				mismatches := 0
				for i := 0; i < size; i++ {
					expected := byte(i % 256)
					if putMsg.Value[i] != expected {
						mismatches++
						if mismatches <= 10 { // 只打印前 10 个错误
							t.Errorf("字节 #%d 不匹配: 期望 %d, 实际 %d", i, expected, putMsg.Value[i])
						}
					}
				}

				assert.Equal(t, 0, mismatches, "发现 %d 个字节不匹配", mismatches)
				t.Logf("✅ 精确性测试通过: %d 字节，逐字节验证成功", size)

			case <-time.After(10 * time.Second):
				t.Fatalf("接收超时: size=%d", size)
			}
		})
	}
}

// TestUDPTransport_FragmentBufferTimeout 测试分片缓冲区超时清理
func TestUDPTransport_FragmentBufferTimeout(t *testing.T) {
	trans := createUDPTransport(t)

	// 初始化分片缓冲区
	trans.initFragmentBuffer()
	assert.NotNil(t, trans.fragmentBuf)

	// 手动添加一个部分消息
	key := fragmentKey{nodeID: 1, msgID: 100}
	partial := &partialMessage{
		total:      3,
		received:   1,
		fragments:  make([][]byte, 3),
		lastUpdate: time.Now().Add(-10 * time.Second), // 模拟超时
	}
	partial.fragments[0] = []byte("data1")

	trans.fragmentBuf.mu.Lock()
	trans.fragmentBuf.buffers[key] = partial
	trans.fragmentBuf.mu.Unlock()

	// 触发清理
	trans.fragmentBuf.cleanupExpiredFragments()

	// 验证已被清理
	trans.fragmentBuf.mu.RLock()
	_, exists := trans.fragmentBuf.buffers[key]
	trans.fragmentBuf.mu.RUnlock()

	assert.False(t, exists, "超时的分片应该被清理")
}

// ========================================
// UDP 传输并发测试
// ========================================

// TestUDPTransport_ConcurrentSend 测试并发发送
func TestUDPTransport_ConcurrentSend(t *testing.T) {
	ctx := context.Background()
	recvCtx, cancelRecv := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelRecv()

	client, server, serverAddr := setupTestPair(t)
	defer func() { _ = client.Stop() }()
	defer func() { _ = server.Stop() }()

	// 并发发送多条消息
	const numMessages = 50
	var wg sync.WaitGroup
	errors := make(chan error, numMessages)

	receivedCount := 0
	var mu sync.Mutex

	// 启动接收协程（带超时和退出机制，防止 goroutine 泄漏）
	go func() {
		for {
			select {
			case msg, ok := <-server.Receive():
				if !ok {
					return
				}
				if msg.Message != nil {
					mu.Lock()
					receivedCount++
					mu.Unlock()
				}
			case <-recvCtx.Done():
				return
			}
		}
	}()

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
	errorCount := 0
	for err := range errors {
		t.Logf("发送错误: %v", err)
		errorCount++
	}

	// 大部分消息应该发送成功
	assert.Less(t, errorCount, numMessages/2, "过多的发送失败")

	// 等待接收完成
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	t.Logf("并发发送 %d 条消息，成功接收 %d 条", numMessages, receivedCount)
	mu.Unlock()
}

// ========================================
// UDP 传输配置和统计测试
// ========================================

// TestUDPTransport_GetLocalAddr 测试获取本地地址
func TestUDPTransport_GetLocalAddr(t *testing.T) {
	trans := createUDPTransport(t)

	addr := trans.GetLocalAddr()
	assert.Equal(t, "127.0.0.1:0", addr)
}

// TestUDPTransport_GetConfig 测试获取配置
func TestUDPTransport_GetConfig(t *testing.T) {
	trans := createUDPTransport(t)

	config := trans.GetConfig()
	assert.NotNil(t, config)
	assert.Equal(t, "127.0.0.1:0", config.ListenAddr)
}

// TestUDPTransport_Stats 测试获取统计信息
func TestUDPTransport_Stats(t *testing.T) {
	trans := createUDPTransport(t)

	stats := trans.Stats()
	assert.NotNil(t, stats)
	assert.Equal(t, false, stats["started"])
	assert.Equal(t, false, stats["stopped"])
	assert.Contains(t, stats, "listen_addr")
	assert.Contains(t, stats, "local_node_id")
	assert.Contains(t, stats, "msg_id_counter")
}

// TestUDPTransport_Stats_AfterStart 测试启动后的统计信息
func TestUDPTransport_Stats_AfterStart(t *testing.T) {
	trans := createUDPTransport(t)
	err := trans.Start()
	require.NoError(t, err)
	defer func() { _ = trans.Stop() }()

	stats := trans.Stats()
	assert.Equal(t, true, stats["started"])
	assert.Equal(t, false, stats["stopped"])
	assert.Contains(t, stats, "pending_fragments")
}

// ========================================
// UDP 传输错误处理测试
// ========================================

// TestUDPTransport_InvalidAddress 测试无效地址
func TestUDPTransport_InvalidAddress(t *testing.T) {
	// 创建 UDP 传输不会验证地址格式
	// 只有在 Start 时才会真正尝试监听
	trans, err := NewUDPTransport("invalid-address")
	assert.NoError(t, err)
	assert.NotNil(t, trans)

	// 启动时应该失败
	err = trans.Start()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid-address")
}

// TestUDPTransport_Receive_BeforeStart 测试启动前接收
func TestUDPTransport_Receive_BeforeStart(t *testing.T) {
	trans := createUDPTransport(t)

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
// Ping/Pong 消息测试
// ========================================

// TestUDPTransport_PingPong 双向 Ping/Pong 通信测试
func TestUDPTransport_PingPong(t *testing.T) {
	ctx := context.Background()

	client, server, serverAddr := setupTestPair(t)
	clientAddr := client.GetLocalAddr()

	client.SetNodeID(2)
	server.SetNodeID(1)

	defer func() { _ = client.Stop() }()
	defer func() { _ = server.Stop() }()

	// 测试：客户端发送 Ping 到服务端
	t.Log("测试: 客户端 -> Ping -> 服务端 -> Pong -> 客户端")

	serverReceivedPing := make(chan *NodePingMessage, 1)
	clientReceivedPong := make(chan *NodePongMessage, 1)

	// 服务端接收协程（自动回复 Pong）
	go func() {
		for msg := range server.Receive() {
			if ping, ok := msg.Message.(*NodePingMessage); ok {
				serverReceivedPing <- ping

				// 自动回复 Pong
				pong := &NodePongMessage{
					NodeID:    "server-node",
					Sequence:  ping.Sequence,
					Status:    "ready",
					Timestamp: time.Now().UnixMilli(),
				}
				_ = server.Send(ctx, clientAddr, pong)
				return
			}
		}
	}()

	// 客户端接收协程
	go func() {
		for msg := range client.Receive() {
			if pong, ok := msg.Message.(*NodePongMessage); ok {
				clientReceivedPong <- pong
				return
			}
		}
	}()

	// 发送 Ping
	ping := &NodePingMessage{
		NodeID:    "client-node",
		Sequence:  1001,
		Timestamp: time.Now().UnixMilli(),
	}

	err := client.Send(ctx, serverAddr, ping)
	require.NoError(t, err)

	// 验证服务端收到 Ping
	select {
	case recvPing := <-serverReceivedPing:
		assert.Equal(t, "client-node", recvPing.NodeID)
		assert.Equal(t, int64(1001), recvPing.Sequence)
		t.Logf("服务端成功接收 Ping 来自节点 %s", recvPing.NodeID)
	case <-time.After(2 * time.Second):
		t.Fatal("服务端未收到 Ping")
	}

	// 验证客户端收到 Pong
	select {
	case pong := <-clientReceivedPong:
		assert.Equal(t, "server-node", pong.NodeID)
		assert.Equal(t, int64(1001), pong.Sequence)
		assert.Equal(t, "ready", pong.Status)
		t.Logf("客户端成功接收 Pong 来自节点 %s", pong.NodeID)
	case <-time.After(2 * time.Second):
		t.Fatal("客户端未收到 Pong")
	}

	t.Log("UDP Ping/Pong 通信测试通过")
}

// TestUDPTransport_PingPong_Bidirectional 双向互发 Ping/Pong 测试
//
// 测试场景：
// 1. 节点 A 和节点 B 互发 Ping
// 2. 双方都能正确接收对方的 Ping 并回复 Pong
// 3. 验证 Sequence 匹配和 NodeID 正确性
func TestUDPTransport_PingPong_Bidirectional(t *testing.T) {
	ctx := context.Background()

	nodeA, nodeB, nodeBAddr := setupTestPair(t)
	nodeAAddr := nodeA.GetLocalAddr()

	nodeA.SetNodeID(100)
	nodeB.SetNodeID(200)

	defer func() { _ = nodeA.Stop() }()
	defer func() { _ = nodeB.Stop() }()

	t.Log("测试: 双向 Ping/Pong 通信")

	// 节点 A 收到的 Ping 和 Pong
	nodeAReceivedPing := make(chan *NodePingMessage, 10)
	nodeAReceivedPong := make(chan *NodePongMessage, 10)

	// 节点 B 收到的 Ping 和 Pong
	nodeBReceivedPing := make(chan *NodePingMessage, 10)
	nodeBReceivedPong := make(chan *NodePongMessage, 10)

	// 节点 A 接收协程（自动回复 Pong）
	go func() {
		for msg := range nodeA.Receive() {
			switch m := msg.Message.(type) {
			case *NodePingMessage:
				nodeAReceivedPing <- m
				// 自动回复 Pong
				pong := &NodePongMessage{
					NodeID:    "node-a",
					Sequence:  m.Sequence,
					Status:    "active",
					Timestamp: time.Now().UnixMilli(),
				}
				_ = nodeA.Send(ctx, nodeBAddr, pong)
			case *NodePongMessage:
				nodeAReceivedPong <- m
			}
		}
	}()

	// 节点 B 接收协程（自动回复 Pong）
	go func() {
		for msg := range nodeB.Receive() {
			switch m := msg.Message.(type) {
			case *NodePingMessage:
				nodeBReceivedPing <- m
				// 自动回复 Pong
				pong := &NodePongMessage{
					NodeID:    "node-b",
					Sequence:  m.Sequence,
					Status:    "active",
					Timestamp: time.Now().UnixMilli(),
				}
				_ = nodeB.Send(ctx, nodeAAddr, pong)
			case *NodePongMessage:
				nodeBReceivedPong <- m
			}
		}
	}()

	// 节点 A 发送 Ping 到节点 B
	pingA := &NodePingMessage{
		NodeID:    "node-a",
		Sequence:  1001,
		Timestamp: time.Now().UnixMilli(),
	}
	err := nodeA.Send(ctx, nodeBAddr, pingA)
	require.NoError(t, err)

	// 节点 B 发送 Ping 到节点 A
	pingB := &NodePingMessage{
		NodeID:    "node-b",
		Sequence:  2001,
		Timestamp: time.Now().UnixMilli(),
	}
	err = nodeB.Send(ctx, nodeAAddr, pingB)
	require.NoError(t, err)

	// 验证节点 B 收到节点 A 的 Ping
	select {
	case recvPing := <-nodeBReceivedPing:
		assert.Equal(t, "node-a", recvPing.NodeID)
		assert.Equal(t, int64(1001), recvPing.Sequence)
		t.Logf("节点 B 收到节点 A 的 Ping: seq=%d", recvPing.Sequence)
	case <-time.After(2 * time.Second):
		t.Fatal("节点 B 未收到节点 A 的 Ping")
	}

	// 验证节点 A 收到节点 B 的 Ping
	select {
	case recvPing := <-nodeAReceivedPing:
		assert.Equal(t, "node-b", recvPing.NodeID)
		assert.Equal(t, int64(2001), recvPing.Sequence)
		t.Logf("节点 A 收到节点 B 的 Ping: seq=%d", recvPing.Sequence)
	case <-time.After(2 * time.Second):
		t.Fatal("节点 A 未收到节点 B 的 Ping")
	}

	// 验证节点 A 收到节点 B 的 Pong
	select {
	case pong := <-nodeAReceivedPong:
		assert.Equal(t, "node-b", pong.NodeID)
		assert.Equal(t, int64(1001), pong.Sequence)
		assert.Equal(t, "active", pong.Status)
		t.Logf("节点 A 收到节点 B 的 Pong: seq=%d", pong.Sequence)
	case <-time.After(2 * time.Second):
		t.Fatal("节点 A 未收到节点 B 的 Pong")
	}

	// 验证节点 B 收到节点 A 的 Pong
	select {
	case pong := <-nodeBReceivedPong:
		assert.Equal(t, "node-a", pong.NodeID)
		assert.Equal(t, int64(2001), pong.Sequence)
		assert.Equal(t, "active", pong.Status)
		t.Logf("节点 B 收到节点 A 的 Pong: seq=%d", pong.Sequence)
	case <-time.After(2 * time.Second):
		t.Fatal("节点 B 未收到节点 A 的 Pong")
	}

	t.Log("✅ 双向 Ping/Pong 测试通过")
}

// TestUDPTransport_PingPong_MultipleRounds 多轮 Ping/Pong 测试
//
// 测试场景：
// 1. 客户端连续发送多轮 Ping
// 2. 服务端每轮都回复 Pong
// 3. 验证所有轮次的 Sequence 都正确匹配
func TestUDPTransport_PingPong_MultipleRounds(t *testing.T) {
	ctx := context.Background()

	client, server, serverAddr := setupTestPair(t)
	clientAddr := client.GetLocalAddr()

	client.SetNodeID(300)
	server.SetNodeID(400)

	defer func() { _ = client.Stop() }()
	defer func() { _ = server.Stop() }()

	t.Log("测试: 多轮 Ping/Pong 通信")

	const numRounds = 5
	serverReceivedPings := make(chan *NodePingMessage, numRounds)
	clientReceivedPongs := make(chan *NodePongMessage, numRounds)

	var wg sync.WaitGroup
	wg.Add(2)

	// 服务端接收协程（自动回复 Pong）
	go func() {
		defer wg.Done()
		count := 0
		for msg := range server.Receive() {
			if ping, ok := msg.Message.(*NodePingMessage); ok {
				serverReceivedPings <- ping

				// 自动回复 Pong
				pong := &NodePongMessage{
					NodeID:    "server-node",
					Sequence:  ping.Sequence,
					Status:    "ready",
					Timestamp: time.Now().UnixMilli(),
				}
				_ = server.Send(ctx, clientAddr, pong)

				count++
				if count >= numRounds {
					return
				}
			}
		}
	}()

	// 客户端接收协程
	go func() {
		defer wg.Done()
		count := 0
		for msg := range client.Receive() {
			if pong, ok := msg.Message.(*NodePongMessage); ok {
				clientReceivedPongs <- pong

				count++
				if count >= numRounds {
					return
				}
			}
		}
	}()

	// 发送多轮 Ping
	for i := 0; i < numRounds; i++ {
		ping := &NodePingMessage{
			NodeID:    "client-node",
			Sequence:  int64(5000 + i),
			Timestamp: time.Now().UnixMilli(),
		}

		err := client.Send(ctx, serverAddr, ping)
		require.NoError(t, err, "第 %d 轮发送失败", i+1)
		t.Logf("发送第 %d 轮 Ping: seq=%d", i+1, ping.Sequence)

		// 短暂延迟，避免网络拥塞
		time.Sleep(50 * time.Millisecond)
	}

	// 等待所有 Pong 到达
	wg.Wait()

	// 验证所有轮次
	close(serverReceivedPings)
	close(clientReceivedPongs)

	receivedPingCount := 0
	receivedPongCount := 0
	expectedSequences := make(map[int64]bool)

	for ping := range serverReceivedPings {
		expectedSequences[ping.Sequence] = true
		receivedPingCount++
		t.Logf("服务端收到 Ping: seq=%d", ping.Sequence)
	}

	for pong := range clientReceivedPongs {
		if expectedSequences[pong.Sequence] {
			receivedPongCount++
			t.Logf("客户端收到 Pong: seq=%d, status=%s", pong.Sequence, pong.Status)
		}
	}

	assert.Equal(t, numRounds, receivedPingCount, "应该收到所有 Ping")
	assert.Equal(t, numRounds, receivedPongCount, "应该收到所有 Pong")

	t.Logf("✅ 多轮 Ping/Pong 测试通过（%d/%d 成功）", receivedPongCount, numRounds)
}

// ========================================
// 辅助函数
// ========================================

// createUDPTransport 创建用于测试的 UDP 传输
// 使用随机端口避免冲突
func createUDPTransport(t *testing.T) *UDPTransport {
	t.Helper()

	trans, err := NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)

	return trans
}

// startTestTransport 启动测试传输并返回其地址
func startTestTransport(t *testing.T, trans *UDPTransport) string {
	t.Helper()

	err := trans.Start()
	require.NoError(t, err)

	return trans.GetLocalAddr()
}

// setupTestPair 设置测试用的客户端-服务端对
func setupTestPair(t *testing.T) (client, server *UDPTransport, serverAddr string) {
	t.Helper()

	server = createUDPTransport(t)
	serverAddr = startTestTransport(t, server)

	client = createUDPTransport(t)
	startTestTransport(t, client)

	// 等待准备就绪
	time.Sleep(100 * time.Millisecond)

	return client, server, serverAddr
}

// ========================================
// 高级测试：分片重组、异常场景、性能测试
// ========================================

// TestUDPFragmentation_MD5Integrity 测试大包分片组合的完整性
//
// 测试步骤：
// 1. 生成 1MB 随机二进制数据
// 2. 将数据分割为多个 UDP 包（每个 ≤ 1400 字节）
// 3. 发送所有包，接收方自动重组
// 4. 对比重组后数据与原始数据的 MD5 值
func TestUDPFragmentation_MD5Integrity(t *testing.T) {
	ctx := context.Background()

	// 步骤 1：生成 1MB 随机数据
	dataSize := 1024 * 1024 // 1MB
	originalData := generateRandomData(dataSize)
	originalMD5 := md5.Sum(originalData)
	t.Logf("原始数据: %d 字节, MD5: %x", dataSize, originalMD5)

	// 创建服务端
	server := createUDPTransport(t)
	server.SetNodeID(9001)
	err := server.Start()
	require.NoError(t, err)
	defer func() { _ = server.Stop() }()

	serverAddr := server.GetLocalAddr()

	// 创建客户端
	client := createUDPTransport(t)
	client.SetNodeID(9002)
	err = client.Start()
	require.NoError(t, err)
	defer func() { _ = client.Stop() }()

	// 等待准备就绪
	time.Sleep(100 * time.Millisecond)

	// 步骤 2：发送大消息（会自动分片）
	msg := &PutMessage{
		Key:   fmt.Sprintf("md5-test-%d", time.Now().UnixNano()),
		Value: originalData,
	}

	err = client.Send(ctx, serverAddr, msg)
	require.NoError(t, err, "发送大消息失败")

	// 步骤 3：接收并重组消息
	receivedCh := make(chan *PutMessage, 1)
	errCh := make(chan error, 1)

	go func() {
		for receivedMsg := range server.Receive() {
			if putMsg, ok := receivedMsg.Message.(*PutMessage); ok {
				receivedCh <- putMsg
				return
			}
		}
	}()

	select {
	case putMsg := <-receivedCh:
		// 步骤 4：验证 MD5
		receivedMD5 := md5.Sum(putMsg.Value)
		t.Logf("接收数据: %d 字节, MD5: %x", len(putMsg.Value), receivedMD5)

		assert.Equal(t, originalMD5, receivedMD5, "MD5 不匹配，数据损坏")
		assert.Equal(t, dataSize, len(putMsg.Value), "数据长度不匹配")
		t.Log("✅ MD5 完整性验证通过")

	case err := <-errCh:
		t.Fatalf("接收失败: %v", err)

	case <-time.After(30 * time.Second):
		t.Fatal("接收超时")
	}
}

// TestUDPFragmentation_PacketLoss 模拟丢包场景
func TestUDPFragmentation_PacketLoss(t *testing.T) {
	trans := createUDPTransport(t)
	trans.initFragmentBuffer()

	totalFragments := uint16(10)
	fragmentSize := 1000
	nodeID := uint64(5001)
	msgID := uint64(10001)

	// 发送所有分片，除了第 3、7 个（索引 2 和 6）
	for i := 0; i < int(totalFragments); i++ {
		if i == 2 || i == 6 {
			t.Logf("📦 丢包：跳过分片 #%d", i+1)
			continue
		}

		data := make([]byte, fragmentSize)
		for j := range data {
			data[j] = byte(i)
		}

		// 使用 Builder 模式构造带分片扩展的 TLV Frame
		frame := NewFrame(nodeID, msgID, MessageTypeGossipSync, uint16(trans.codec.Type()), data).
			WithFragment(uint16(i), totalFragments).
			Finalize()
		fragmentBytes, _ := frame.Marshal()
		_ = trans.processReceivedData(fragmentBytes)
	}

	// 等待超时清理（超时时间是 5 秒，等待 7 秒确保清理协程有足够时间运行）
	time.Sleep(7 * time.Second)

	trans.fragmentBuf.mu.RLock()
	pendingCount := len(trans.fragmentBuf.buffers)
	trans.fragmentBuf.mu.RUnlock()

	t.Logf("清理后待处理分片数: %d", pendingCount)
	assert.Equal(t, 0, pendingCount, "超时的分片应该被清理")
	t.Log("✅ 丢包场景测试通过：分片超时清理正常")
}

// TestUDPFragmentation_OutOfOrder 模拟乱序场景
func TestUDPFragmentation_OutOfOrder(t *testing.T) {
	trans := createUDPTransport(t)
	trans.initFragmentBuffer()

	totalFragments := uint16(10)
	fragmentSize := 500
	nodeID := uint64(5002)
	msgID := uint64(10002)

	// 乱序发送序列
	outOfOrderSequence := []int{5, 2, 8, 0, 7, 3, 9, 1, 6, 4}
	t.Logf("乱序发送序列: %v", outOfOrderSequence)

	// 发送前 9 个分片（不发送最后一个，避免触发解帧失败）
	for _, seq := range outOfOrderSequence[:9] {
		fragmentData := make([]byte, fragmentSize)
		for j := range fragmentData {
			fragmentData[j] = byte(seq)
		}

		// 使用 Builder 模式构造带分片扩展的 TLV Frame
		frame := NewFrame(nodeID, msgID, MessageTypeGossipSync, uint16(trans.codec.Type()), fragmentData).
			WithFragment(uint16(seq), totalFragments).
			Finalize()
		fragmentBytes, _ := frame.Marshal()
		_ = trans.processReceivedData(fragmentBytes)
		t.Logf("📦 接收乱序分片 #%d/%d", seq+1, totalFragments)
	}

	// 验证分片缓冲区状态
	trans.fragmentBuf.mu.RLock()
	key := fragmentKey{nodeID: nodeID, msgID: msgID}
	partial, exists := trans.fragmentBuf.buffers[key]
	trans.fragmentBuf.mu.RUnlock()

	if !exists {
		t.Fatal("分片缓冲区中应该存在部分消息")
	}

	// 验证收到的分片数
	assert.Equal(t, uint16(9), partial.received, "应该收到 9 个分片")

	// 验证分片是否按正确顺序缓存（乱序发送，但应按索引存储）
	for i := 0; i < 9; i++ {
		seq := outOfOrderSequence[i]
		if partial.fragments[seq] == nil {
			t.Errorf("分片 #%d（乱序索引 %d）应该存在", seq, i)
		} else {
			// 验证数据内容
			expectedValue := byte(seq)
			for j, b := range partial.fragments[seq] {
				if b != expectedValue {
					t.Errorf("分片 #%d 字节 #%d 期望 %d，实际 %d", seq, j, expectedValue, b)
				}
			}
		}
	}

	t.Log("✅ 乱序场景测试通过：分片正确按索引缓存")
}

// TestUDPFragmentation_Boundary_MinFragments 最小包数测试（2 个包）
func TestUDPFragmentation_Boundary_MinFragments(t *testing.T) {
	ctx := context.Background()

	server := createUDPTransport(t)
	server.SetNodeID(7101)
	err := server.Start()
	require.NoError(t, err)
	defer func() { _ = server.Stop() }()

	client := createUDPTransport(t)
	client.SetNodeID(7102)
	err = client.Start()
	require.NoError(t, err)
	defer func() { _ = client.Stop() }()

	time.Sleep(100 * time.Millisecond)

	data := make([]byte, 1500)
	for i := range data {
		data[i] = byte(i % 256)
	}

	msg := &PutMessage{
		Key:   "min-fragments-test",
		Value: data,
	}

	err = client.Send(ctx, server.GetLocalAddr(), msg)
	require.NoError(t, err)

	receivedCh := make(chan *PutMessage, 1)
	go func() {
		for receivedMsg := range server.Receive() {
			if putMsg, ok := receivedMsg.Message.(*PutMessage); ok {
				receivedCh <- putMsg
				return
			}
		}
	}()

	select {
	case putMsg := <-receivedCh:
		assert.Equal(t, 1500, len(putMsg.Value), "数据长度不匹配")
		assert.Equal(t, data, putMsg.Value, "数据内容不匹配")
		t.Log("✅ 最小包数测试通过（2 个包）")

	case <-time.After(5 * time.Second):
		t.Fatal("接收超时")
	}
}

// TestUDPFragmentation_Boundary_MaxFragments 最大包数测试（1000 个包）
func TestUDPFragmentation_Boundary_MaxFragments(t *testing.T) {
	ctx := context.Background()

	server := createUDPTransport(t)
	server.SetNodeID(7103)
	err := server.Start()
	require.NoError(t, err)
	defer func() { _ = server.Stop() }()

	client := createUDPTransport(t)
	client.SetNodeID(7104)
	err = client.Start()
	require.NoError(t, err)
	defer func() { _ = client.Stop() }()

	time.Sleep(100 * time.Millisecond)

	dataSize := 1400 * 1000
	data := make([]byte, dataSize)
	for i := range data {
		data[i] = byte(i % 256)
	}

	msg := &PutMessage{
		Key:   "max-fragments-test",
		Value: data,
	}

	var memStatsBefore, memStatsAfter runtime.MemStats
	runtime.ReadMemStats(&memStatsBefore)

	startTime := time.Now()

	err = client.Send(ctx, server.GetLocalAddr(), msg)
	require.NoError(t, err)

	receivedCh := make(chan *PutMessage, 1)
	go func() {
		for receivedMsg := range server.Receive() {
			if putMsg, ok := receivedMsg.Message.(*PutMessage); ok {
				receivedCh <- putMsg
				return
			}
		}
	}()

	select {
	case putMsg := <-receivedCh:
		duration := time.Since(startTime)
		runtime.ReadMemStats(&memStatsAfter)

		assert.Equal(t, dataSize, len(putMsg.Value), "数据长度不匹配")
		assert.Equal(t, data, putMsg.Value, "数据内容不匹配")

		memUsed := memStatsAfter.Alloc - memStatsBefore.Alloc
		t.Logf("📊 性能报告（1000 个包）:")
		t.Logf("   - 数据大小: %d 字节 (%.2f MB)", dataSize, float64(dataSize)/(1024*1024))
		t.Logf("   - 处理耗时: %v", duration)
		t.Logf("   - 内存占用: %d 字节 (%.2f MB)", memUsed, float64(memUsed)/(1024*1024))
		t.Logf("   - 平均吞吐: %.2f MB/s", float64(dataSize)/(1024*1024)/duration.Seconds())
		t.Log("✅ 最大包数测试通过（1000 个包）")

	case <-time.After(60 * time.Second):
		t.Fatal("接收超时")
	}
}

// TestUDPFragmentation_Boundary_EmptyPacket 空包测试（零长度包）
func TestUDPFragmentation_Boundary_EmptyPacket(t *testing.T) {
	ctx := context.Background()

	server := createUDPTransport(t)
	server.SetNodeID(7105)
	err := server.Start()
	require.NoError(t, err)
	defer func() { _ = server.Stop() }()

	client := createUDPTransport(t)
	client.SetNodeID(7106)
	err = client.Start()
	require.NoError(t, err)
	defer func() { _ = client.Stop() }()

	time.Sleep(100 * time.Millisecond)

	msg := &PutMessage{
		Key:   "empty-packet-test",
		Value: []byte{},
	}

	err = client.Send(ctx, server.GetLocalAddr(), msg)
	require.NoError(t, err)

	receivedCh := make(chan *PutMessage, 1)
	go func() {
		for receivedMsg := range server.Receive() {
			if putMsg, ok := receivedMsg.Message.(*PutMessage); ok {
				receivedCh <- putMsg
				return
			}
		}
	}()

	select {
	case putMsg := <-receivedCh:
		assert.Equal(t, 0, len(putMsg.Value), "空包长度应该为 0")
		t.Log("✅ 空包测试通过（零长度包）")

	case <-time.After(5 * time.Second):
		t.Fatal("接收超时")
	}
}

// TestUDPFragmentation_PerformanceReport 性能报告测试
func TestUDPFragmentation_PerformanceReport(t *testing.T) {
	ctx := context.Background()

	testSizes := []struct {
		name string
		size int
	}{
		{"小数据 (10KB)", 10 * 1024},
		{"中数据 (100KB)", 100 * 1024},
		{"大数据 (1MB)", 1024 * 1024},
	}

	for _, tc := range testSizes {
		t.Run(tc.name, func(t *testing.T) {
			server := createUDPTransport(t)
			server.SetNodeID(8000 + uint64(len(tc.name)))
			err := server.Start()
			require.NoError(t, err)
			defer func() { _ = server.Stop() }()

			client := createUDPTransport(t)
			client.SetNodeID(9000 + uint64(len(tc.name)))
			err = client.Start()
			require.NoError(t, err)
			defer func() { _ = client.Stop() }()

			time.Sleep(100 * time.Millisecond)

			data := make([]byte, tc.size)
			for i := range data {
				data[i] = byte(i % 256)
			}

			msg := &PutMessage{
				Key:   fmt.Sprintf("perf-test-%d", time.Now().UnixNano()),
				Value: data,
			}

			var memStatsBefore, memStatsAfter runtime.MemStats
			runtime.ReadMemStats(&memStatsBefore)

			startTime := time.Now()

			err = client.Send(ctx, server.GetLocalAddr(), msg)
			require.NoError(t, err)

			receivedCh := make(chan *PutMessage, 1)
			go func() {
				for receivedMsg := range server.Receive() {
					if putMsg, ok := receivedMsg.Message.(*PutMessage); ok {
						receivedCh <- putMsg
						return
					}
				}
			}()

			select {
			case putMsg := <-receivedCh:
				duration := time.Since(startTime)
				runtime.ReadMemStats(&memStatsAfter)

				assert.Equal(t, tc.size, len(putMsg.Value), "数据长度不匹配")

				memUsed := memStatsAfter.Alloc - memStatsBefore.Alloc
				fragmentsCount := (tc.size + MaxUDPPacketSize - 1) / MaxUDPPacketSize

				t.Logf("📊 性能报告 [%s]:", tc.name)
				t.Logf("   - 数据大小: %d 字节 (%.2f KB)", tc.size, float64(tc.size)/1024)
				t.Logf("   - 分片数量: 约 %d 个", fragmentsCount)
				t.Logf("   - 处理耗时: %v", duration)
				t.Logf("   - 内存占用: %d 字节 (%.2f KB)", memUsed, float64(memUsed)/1024)
				t.Logf("   - 平均吞吐: %.2f MB/s", float64(tc.size)/(1024*1024)/duration.Seconds())

			case <-time.After(30 * time.Second):
				t.Fatalf("接收超时: %s", tc.name)
			}
		})
	}
}

// generateRandomData 生成指定长度的随机二进制数据
func generateRandomData(size int) []byte {
	data := make([]byte, size)
	_, err := cryptorand.Read(data)
	if err != nil {
		// 测试中不应该失败，如果失败则使用零填充
		panic(fmt.Sprintf("生成随机数据失败: %v", err))
	}
	return data
}

// ========================================
// P0 安全问题测试
// ========================================

// TestUDP_P0_LocalNodeIDValidation 测试 NodeID 验证
func TestUDP_P0_LocalNodeIDValidation(t *testing.T) {
	ctx := context.Background()

	// 创建 UDP transport（不设置 NodeID）
	trans, err := NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)

	err = trans.Start()
	require.NoError(t, err)
	defer func() {
		if err := trans.Stop(); err != nil {
			t.Logf("trans.Stop() failed: %v", err)
		}
	}()

	serverAddr := trans.GetLocalAddr()

	// 创建客户端（也不设置 NodeID）
	client, err := NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)

	err = client.Start()
	require.NoError(t, err)
	defer func() {
		if err := client.Stop(); err != nil {
			t.Logf("client.Stop() failed: %v", err)
		}
	}()

	// 尝试发送大消息（需要分片），应该因为 NodeID=0 而失败
	largeValue := make([]byte, 2000) // 大于 MaxUDPPacketSize
	msg := &PutMessage{Key: "test-key", Value: largeValue}

	err = client.Send(ctx, serverAddr, msg)
	assert.Error(t, err, "NodeID 未设置时应该返回错误")
	assert.Contains(t, err.Error(), "NodeID 未设置", "错误信息应该提到 NodeID")

	// 设置 NodeID 后应该成功
	client.SetNodeID(1002)
	err = client.Send(ctx, serverAddr, msg)
	assert.NoError(t, err, "设置 NodeID 后应该成功发送")

	t.Log("✅ P0-1: NodeID 验证测试通过")
}

// TestUDP_P0_MaxFragmentCount 测试分片数量上限
func TestUDP_P0_MaxFragmentCount(t *testing.T) {
	trans, err := NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)

	err = trans.Start()
	require.NoError(t, err)
	defer func() {
		if err := trans.Stop(); err != nil {
			t.Logf("trans.Stop() failed: %v", err)
		}
	}()

	trans.SetNodeID(1001)

	// 构造恶意分片帧：total = 0（非法值）
	// 使用新的 TLV Frame 格式，包含 ExtFragment 扩展字段
	frame := NewFrame(1001, 1, MessageTypeGet, uint16(types.CodecTypeMessagePack), []byte("test data"))

	// 添加分片扩展字段：index=0, total=0（非法值）
	fragmentExt := EncodeFragmentExt(0, 0) // total=0 应该被拒绝
	frame.VarExtHeader.Fields = append(frame.VarExtHeader.Fields, fragmentExt)

	// 序列化帧
	maliciousPacket, err := frame.Marshal()
	require.NoError(t, err)

	// 处理恶意数据包，应该被拒绝（total=0 是非法的）
	result := trans.processReceivedData(maliciousPacket)
	assert.Nil(t, result.Message, "total=0 的分片应该被拒绝")

	// 注意：MaxFragmentCount = 65535 是 uint16 的最大值，无法构造超过此值的测试
	// 实际场景中，65535 个分片 * 1400 字节 ≈ 91 MB，已经是合理的上限
	t.Logf("✅ MaxFragmentCount = %d (uint16 最大值，约 %.1f MB)", MaxFragmentCount, float64(MaxFragmentCount*MaxUDPPacketSize)/(1024*1024))

	// 验证错误计数器增加
	stats := trans.Stats()
	fragmentErrors, ok := stats["fragment_errors"].(uint64)
	if ok {
		assert.Greater(t, fragmentErrors, uint64(0), "应该记录分片错误")
	}

	t.Log("✅ P0-2: 分片数量上限测试通过")
}
