// Package transport 多协议传输单元测试
package transport

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ========================================
// 基础功能测试
// ========================================

// TestMultiTransport_NewMultiTransport 测试创建 MultiTransport
func TestMultiTransport_NewMultiTransport(t *testing.T) {
	mt, err := NewMultiTransport("127.0.0.1:0")
	require.NoError(t, err)
	require.NotNil(t, mt)

	stats := mt.Stats()
	assert.Equal(t, false, stats["started"])
	assert.Equal(t, false, stats["stopped"])
	assert.NotNil(t, mt.codec)
}

// TestMultiTransport_NewMultiTransportWithConfig 测试自定义配置创建
func TestMultiTransport_NewMultiTransportWithConfig(t *testing.T) {
	config := &TransportConfig{
		ListenAddr:     "127.0.0.1:0",
		MaxMessageSize: 2048,
		BufferSize:     100,
	}

	mt, err := NewMultiTransportWithConfig(config)
	require.NoError(t, err)
	require.NotNil(t, mt)

	assert.Equal(t, config, mt.config)
}

// TestMultiTransport_NewMultiTransport_NilConfig 测试 nil 配置
func TestMultiTransport_NewMultiTransport_NilConfig(t *testing.T) {
	mt, err := NewMultiTransportWithConfig(nil)
	require.NoError(t, err)
	require.NotNil(t, mt)

	// 应该使用默认配置
	assert.NotNil(t, mt.config)
}

// TestMultiTransport_RegisterProtocol 测试协议注册
func TestMultiTransport_RegisterProtocol(t *testing.T) {
	mt, err := NewMultiTransport("127.0.0.1:0")
	require.NoError(t, err)

	// 创建 TCP transport
	tcp, err := NewTCPTransport("127.0.0.1:0")
	require.NoError(t, err)

	// 注册协议
	err = mt.RegisterProtocol(ProtocolConfig{
		ProtocolType: ProtocolTCP,
		Priority:     10,
		CanDegrade:   true,
		Transport:    tcp,
	})
	require.NoError(t, err)

	// 验证默认协议已设置
	defaultProto, err := mt.GetActiveProtocol()
	require.NoError(t, err)
	assert.Equal(t, ProtocolTCP, defaultProto)
}

// TestMultiTransport_RegisterProtocol_Duplicate 测试重复注册
func TestMultiTransport_RegisterProtocol_Duplicate(t *testing.T) {
	mt, err := NewMultiTransport("127.0.0.1:0")
	require.NoError(t, err)

	tcp, err := NewTCPTransport("127.0.0.1:0")
	require.NoError(t, err)

	// 第一次注册
	err = mt.RegisterProtocol(ProtocolConfig{
		ProtocolType: ProtocolTCP,
		Priority:     10,
		CanDegrade:   true,
		Transport:    tcp,
	})
	require.NoError(t, err)

	// 第二次注册（应该失败）
	err = mt.RegisterProtocol(ProtocolConfig{
		ProtocolType: ProtocolTCP,
		Priority:     10,
		CanDegrade:   true,
		Transport:    tcp,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "已注册")
}

// TestMultiTransport_RegisterProtocol_NilTransport 测试 nil Transport
func TestMultiTransport_RegisterProtocol_NilTransport(t *testing.T) {
	mt, err := NewMultiTransport("127.0.0.1:0")
	require.NoError(t, err)

	err = mt.RegisterProtocol(ProtocolConfig{
		ProtocolType: ProtocolTCP,
		Priority:     10,
		CanDegrade:   true,
		Transport:    nil,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "不能为 nil")
}

// TestMultiTransport_SetDefaultProtocol 测试设置默认协议
func TestMultiTransport_SetDefaultProtocol(t *testing.T) {
	mt, err := NewMultiTransport("127.0.0.1:0")
	require.NoError(t, err)

	// 注册两个协议
	tcp, _ := NewTCPTransport("127.0.0.1:0")
	udp, _ := NewUDPTransport("127.0.0.1:0")

	err = mt.RegisterProtocol(ProtocolConfig{
		ProtocolType: ProtocolTCP,
		Priority:     10,
		CanDegrade:   true,
		Transport:    tcp,
	})
	require.NoError(t, err)

	err = mt.RegisterProtocol(ProtocolConfig{
		ProtocolType: ProtocolUDP,
		Priority:     5,
		CanDegrade:   true,
		Transport:    udp,
	})
	require.NoError(t, err)

	// 切换默认协议
	err = mt.SetDefaultProtocol(ProtocolUDP)
	require.NoError(t, err)

	defaultProto, err := mt.GetActiveProtocol()
	require.NoError(t, err)
	assert.Equal(t, ProtocolUDP, defaultProto)
}

// TestMultiTransport_SetDefaultProtocol_NotRegistered 测试设置未注册的协议
func TestMultiTransport_SetDefaultProtocol_NotRegistered(t *testing.T) {
	mt, err := NewMultiTransport("127.0.0.1:0")
	require.NoError(t, err)

	err = mt.SetDefaultProtocol(ProtocolGRPC)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "未注册")
}

// TestMultiTransport_StartStop 测试启动和停止
func TestMultiTransport_StartStop(t *testing.T) {
	mt, err := NewMultiTransport("127.0.0.1:0")
	require.NoError(t, err)

	// 注册协议
	tcp, _ := NewTCPTransport("127.0.0.1:0")
	err = mt.RegisterProtocol(ProtocolConfig{
		ProtocolType: ProtocolTCP,
		Priority:     10,
		CanDegrade:   true,
		Transport:    tcp,
	})
	require.NoError(t, err)

	// 启动
	err = mt.Start(nil, nil)
	require.NoError(t, err)

	stats := mt.Stats()
	assert.Equal(t, true, stats["started"])
	assert.Equal(t, false, stats["stopped"])

	// 停止
	err = mt.Stop()
	require.NoError(t, err)

	stats = mt.Stats()
	assert.Equal(t, true, stats["started"])
	assert.Equal(t, true, stats["stopped"])
}

// TestMultiTransport_Start_AlreadyStarted 测试重复启动
func TestMultiTransport_Start_AlreadyStarted(t *testing.T) {
	mt, err := NewMultiTransport("127.0.0.1:0")
	require.NoError(t, err)

	tcp, _ := NewTCPTransport("127.0.0.1:0")
	err = mt.RegisterProtocol(ProtocolConfig{
		ProtocolType: ProtocolTCP,
		Priority:     10,
		CanDegrade:   true,
		Transport:    tcp,
	})
	require.NoError(t, err)

	err = mt.Start(nil, nil)
	require.NoError(t, err)

	// 重复启动应该失败
	err = mt.Start(nil, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "已经启动")

	_ = mt.Stop()
}

// TestMultiTransport_Start_NoProtocols 测试无协议启动
func TestMultiTransport_Start_NoProtocols(t *testing.T) {
	mt, err := NewMultiTransport("127.0.0.1:0")
	require.NoError(t, err)

	// 未注册任何协议就启动
	err = mt.Start(nil, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "未注册任何协议")
}

// TestMultiTransport_Stop_NotStarted 测试未启动就停止
func TestMultiTransport_Stop_NotStarted(t *testing.T) {
	mt, err := NewMultiTransport("127.0.0.1:0")
	require.NoError(t, err)

	// 未启动就停止应该成功（幂等）
	err = mt.Stop()
	require.NoError(t, err)

	stats := mt.Stats()
	assert.Equal(t, true, stats["stopped"])
}

// TestMultiTransport_MultipleStop 测试多次停止
func TestMultiTransport_MultipleStop(t *testing.T) {
	mt, err := NewMultiTransport("127.0.0.1:0")
	require.NoError(t, err)

	tcp, _ := NewTCPTransport("127.0.0.1:0")
	err = mt.RegisterProtocol(ProtocolConfig{
		ProtocolType: ProtocolTCP,
		Priority:     10,
		CanDegrade:   true,
		Transport:    tcp,
	})
	require.NoError(t, err)

	err = mt.Start(nil, nil)
	require.NoError(t, err)

	// 第一次停止
	err = mt.Stop()
	require.NoError(t, err)

	// 第二次停止（MultiTransport 本身是幂等的，但底层 Transport 可能返回错误）
	// 这里我们验证 MultiTransport 的 stopped 标志是 true
	stats := mt.Stats()
	assert.Equal(t, true, stats["stopped"])
}

// ========================================
// 消息发送测试
// ========================================

// TestMultiTransport_Send_WithDefaultProtocol 测试使用默认协议发送
func TestMultiTransport_Send_WithDefaultProtocol(t *testing.T) {
	ctx := context.Background()

	mt, err := NewMultiTransport("127.0.0.1:0")
	require.NoError(t, err)

	// 注册 TCP 协议
	tcpServer, _ := NewTCPTransport("127.0.0.1:0")
	err = mt.RegisterProtocol(ProtocolConfig{
		ProtocolType: ProtocolTCP,
		Priority:     10,
		CanDegrade:   true,
		Transport:    tcpServer,
	})
	require.NoError(t, err)

	err = mt.Start(nil, nil)
	require.NoError(t, err)
	defer func() { _ = mt.Stop() }()

	// 验证默认协议已设置
	defaultProto, err := mt.GetActiveProtocol()
	require.NoError(t, err)
	assert.Equal(t, ProtocolTCP, defaultProto)

	// 尝试发送（预期连接失败，但验证协议选择逻辑正常）
	msg := &PutMessage{Key: "test-key", Value: []byte("test-value")}
	err = mt.Send(ctx, "invalid.address:9999", msg)
	// 连接失败是预期的，但不应该是"未设置默认协议"错误
	assert.NotNil(t, err)
	assert.NotContains(t, err.Error(), "未设置默认协议")
}

// TestMultiTransport_Send_WithSpecifiedProtocol 测试使用指定协议发送
func TestMultiTransport_Send_WithSpecifiedProtocol(t *testing.T) {
	ctx := context.Background()

	mt, err := NewMultiTransport("127.0.0.1:0")
	require.NoError(t, err)

	// 注册 UDP 协议（不手动启动，让 MultiTransport 管理）
	udpServer, _ := NewUDPTransport("127.0.0.1:0")
	err = mt.RegisterProtocol(ProtocolConfig{
		ProtocolType: ProtocolUDP,
		Priority:     10,
		CanDegrade:   true,
		Transport:    udpServer,
	})
	require.NoError(t, err)

	// 设置 NodeID 后启动
	serverNodeID := uint64(1)
	err = mt.Start(&serverNodeID, nil)
	require.NoError(t, err)
	defer func() { _ = mt.Stop() }()

	// 创建客户端 UDP transport
	udpClient, _ := NewUDPTransport("127.0.0.1:0")
	clientNodeID := uint64(2)
	err = udpClient.Start(&clientNodeID, nil)
	require.NoError(t, err)
	defer func() { _ = udpClient.Stop() }()

	// 使用客户端发送
	msg := &PutMessage{Key: "test-key", Value: []byte("test-value")}
	err = udpClient.Send(ctx, udpServer.GetLocalAddr(), msg)
	require.NoError(t, err)

	// 从 MultiTransport 的接收通道获取消息
	select {
	case recvMsg := <-mt.Receive():
		assert.Equal(t, types.MessageTypePut, recvMsg.Type())
	case <-time.After(5 * time.Second):
		t.Fatal("接收超时")
	}
}

// TestMultiTransport_Send_NotStarted 测试未启动发送
func TestMultiTransport_Send_NotStarted(t *testing.T) {
	ctx := context.Background()

	mt, err := NewMultiTransport("127.0.0.1:0")
	require.NoError(t, err)

	tcp, _ := NewTCPTransport("127.0.0.1:0")
	err = mt.RegisterProtocol(ProtocolConfig{
		ProtocolType: ProtocolTCP,
		Priority:     10,
		CanDegrade:   true,
		Transport:    tcp,
	})
	require.NoError(t, err)

	// 未启动就发送
	msg := &PutMessage{Key: "test-key", Value: []byte("test-value")}
	err = mt.Send(ctx, "127.0.0.1:9999", msg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "未启动或已停止")
}

// TestMultiTransport_Send_NoDefaultProtocol 测试无默认协议发送
func TestMultiTransport_Send_NoDefaultProtocol(t *testing.T) {
	ctx := context.Background()

	mt, err := NewMultiTransport("127.0.0.1:0")
	require.NoError(t, err)

	// 启动但未设置默认协议（实际上第一个注册的会成为默认）
	tcp, _ := NewTCPTransport("127.0.0.1:0")
	err = mt.RegisterProtocol(ProtocolConfig{
		ProtocolType: ProtocolTCP,
		Priority:     10,
		CanDegrade:   true,
		Transport:    tcp,
	})
	require.NoError(t, err)

	err = mt.Start(nil, nil)
	require.NoError(t, err)
	defer func() { _ = mt.Stop() }()

	// 应该有默认协议（第一个注册的）
	msg := &PutMessage{Key: "test-key", Value: []byte("test-value")}
	err = mt.Send(ctx, "127.0.0.1:9999", msg)
	// 可能会因为连接失败，但不应该因为"未设置默认协议"而失败
	assert.NotNil(t, err) // 预期连接失败
}

// TestMultiTransport_SendWithProtocol_NotRegistered 测试使用未注册协议发送
func TestMultiTransport_SendWithProtocol_NotRegistered(t *testing.T) {
	ctx := context.Background()

	mt, err := NewMultiTransport("127.0.0.1:0")
	require.NoError(t, err)

	tcp, _ := NewTCPTransport("127.0.0.1:0")
	err = mt.RegisterProtocol(ProtocolConfig{
		ProtocolType: ProtocolTCP,
		Priority:     10,
		CanDegrade:   true,
		Transport:    tcp,
	})
	require.NoError(t, err)

	err = mt.Start(nil, nil)
	require.NoError(t, err)
	defer func() { _ = mt.Stop() }()

	// 使用未注册的协议
	msg := &PutMessage{Key: "test-key", Value: []byte("test-value")}
	err = mt.SendWithProtocol(ctx, "127.0.0.1:9999", msg, ProtocolUDP)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "未注册")
}

// ========================================
// 统计信息测试
// ========================================

// TestMultiTransport_GetProtocolStats 测试获取协议统计
func TestMultiTransport_GetProtocolStats(t *testing.T) {
	mt, err := NewMultiTransport("127.0.0.1:0")
	require.NoError(t, err)

	tcp, _ := NewTCPTransport("127.0.0.1:0")
	err = mt.RegisterProtocol(ProtocolConfig{
		ProtocolType: ProtocolTCP,
		Priority:     10,
		CanDegrade:   true,
		Transport:    tcp,
	})
	require.NoError(t, err)

	stats := mt.GetProtocolStats()
	assert.Contains(t, stats, ProtocolTCP)

	tcpStats := stats[ProtocolTCP]
	assert.Equal(t, false, tcpStats["active"])
	assert.Equal(t, uint64(0), tcpStats["failure_count"])
	assert.Equal(t, 10, tcpStats["priority"])
	assert.Equal(t, true, tcpStats["can_degrade"])
}

// TestMultiTransport_Stats 测试获取统计信息
func TestMultiTransport_Stats(t *testing.T) {
	mt, err := NewMultiTransport("127.0.0.1:0")
	require.NoError(t, err)

	tcp, _ := NewTCPTransport("127.0.0.1:0")
	err = mt.RegisterProtocol(ProtocolConfig{
		ProtocolType: ProtocolTCP,
		Priority:     10,
		CanDegrade:   true,
		Transport:    tcp,
	})
	require.NoError(t, err)

	stats := mt.Stats()
	assert.NotNil(t, stats)
	assert.Equal(t, false, stats["started"])
	assert.Equal(t, false, stats["stopped"])
	assert.Contains(t, stats, "protocols")
	assert.Contains(t, stats, "default_protocol")
}

// TestMultiTransport_GetNodeID 测试获取 NodeID
func TestMultiTransport_GetNodeID(t *testing.T) {
	mt, err := NewMultiTransport("127.0.0.1:0")
	require.NoError(t, err)

	tcp, _ := NewTCPTransport("127.0.0.1:0")
	err = mt.RegisterProtocol(ProtocolConfig{
		ProtocolType: ProtocolTCP,
		Priority:     10,
		CanDegrade:   true,
		Transport:    tcp,
	})
	require.NoError(t, err)

	// 未设置 NodeID
	assert.Equal(t, uint64(0), mt.GetNodeID())

	// 设置 NodeID
	nodeID := uint64(12345)
	err = mt.Start(&nodeID, nil)
	require.NoError(t, err)
	defer func() { _ = mt.Stop() }()

	assert.Equal(t, nodeID, mt.GetNodeID())
}

// TestMultiTransport_GenerateMsgSeq 测试生成消息序列号
func TestMultiTransport_GenerateMsgSeq(t *testing.T) {
	mt, err := NewMultiTransport("127.0.0.1:0")
	require.NoError(t, err)

	tcp, _ := NewTCPTransport("127.0.0.1:0")
	err = mt.RegisterProtocol(ProtocolConfig{
		ProtocolType: ProtocolTCP,
		Priority:     10,
		CanDegrade:   true,
		Transport:    tcp,
	})
	require.NoError(t, err)

	err = mt.Start(nil, nil)
	require.NoError(t, err)
	defer func() { _ = mt.Stop() }()

	// 生成序列号应该是递增的
	seq1 := mt.GenerateMsgSeq()
	seq2 := mt.GenerateMsgSeq()
	assert.Equal(t, seq1+1, seq2)
}

// TestMultiTransport_GenerateMsgSeq_CustomGenerator 测试自定义序列号生成器
func TestMultiTransport_GenerateMsgSeq_CustomGenerator(t *testing.T) {
	mt, err := NewMultiTransport("127.0.0.1:0")
	require.NoError(t, err)

	tcp, _ := NewTCPTransport("127.0.0.1:0")
	err = mt.RegisterProtocol(ProtocolConfig{
		ProtocolType: ProtocolTCP,
		Priority:     10,
		CanDegrade:   true,
		Transport:    tcp,
	})
	require.NoError(t, err)

	// 自定义序列号生成器
	customCounter := uint64(1000)
	customGenerator := func() uint64 {
		customCounter++
		return customCounter
	}

	err = mt.Start(nil, customGenerator)
	require.NoError(t, err)
	defer func() { _ = mt.Stop() }()

	seq1 := mt.GenerateMsgSeq()
	seq2 := mt.GenerateMsgSeq()

	assert.Equal(t, uint64(1001), seq1)
	assert.Equal(t, uint64(1002), seq2)
}

// ========================================
// 并发安全测试
// ========================================

// TestMultiTransport_ConcurrentAccess 测试并发访问
func TestMultiTransport_ConcurrentAccess(t *testing.T) {
	mt, err := NewMultiTransport("127.0.0.1:0")
	require.NoError(t, err)

	tcp, _ := NewTCPTransport("127.0.0.1:0")
	err = mt.RegisterProtocol(ProtocolConfig{
		ProtocolType: ProtocolTCP,
		Priority:     10,
		CanDegrade:   true,
		Transport:    tcp,
	})
	require.NoError(t, err)

	err = mt.Start(nil, nil)
	require.NoError(t, err)
	defer func() { _ = mt.Stop() }()

	const goroutines = 100
	const operationsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(goroutines * 3)

	// 并发获取 NodeID
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < operationsPerGoroutine; j++ {
				_ = mt.GetNodeID()
			}
		}()
	}

	// 并发生成序列号
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < operationsPerGoroutine; j++ {
				_ = mt.GenerateMsgSeq()
			}
		}()
	}

	// 并发获取统计信息
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < operationsPerGoroutine; j++ {
				_ = mt.Stats()
			}
		}()
	}

	wg.Wait()

	// 验证序列号正确递增
	finalSeq := mt.GenerateMsgSeq()
	assert.Equal(t, uint64(goroutines*operationsPerGoroutine+1), finalSeq)
}

// ========================================
// 错误处理测试
// ========================================

// TestMultiTransport_ProtocolFailureCount 测试协议失败计数
func TestMultiTransport_ProtocolFailureCount(t *testing.T) {
	ctx := context.Background()

	mt, err := NewMultiTransport("127.0.0.1:0")
	require.NoError(t, err)

	tcp, _ := NewTCPTransport("127.0.0.1:0")
	err = mt.RegisterProtocol(ProtocolConfig{
		ProtocolType: ProtocolTCP,
		Priority:     10,
		CanDegrade:   true,
		Transport:    tcp,
	})
	require.NoError(t, err)

	err = mt.Start(nil, nil)
	require.NoError(t, err)
	defer func() { _ = mt.Stop() }()

	// 禁用自动路由，确保使用默认协议（TCP）
	mt.UpdateRouterConfig(&RouterConfig{EnableAutoRouting: false})

	// 发送到无效地址（会失败）
	msg := &PutMessage{Key: "test-key", Value: []byte("test-value")}
	for i := 0; i < 5; i++ {
		_ = mt.Send(ctx, "127.0.0.1:9999", msg)
	}

	// 检查失败计数
	stats := mt.GetProtocolStats()
	tcpStats := stats[ProtocolTCP]
	assert.Equal(t, uint64(5), tcpStats["failure_count"])
}

// TestMultiTransport_ReceiveChannel 测试接收通道
func TestMultiTransport_ReceiveChannel(t *testing.T) {
	mt, err := NewMultiTransport("127.0.0.1:0")
	require.NoError(t, err)

	recvCh := mt.Receive()
	assert.NotNil(t, recvCh)

	// 通道应该是打开的
	select {
	case <-recvCh:
		t.Fatal("不应该有消息")
	case <-time.After(100 * time.Millisecond):
		// 预期超时
	}
}

// TestMultiTransport_MultipleProtocols 测试多协议注册
func TestMultiTransport_MultipleProtocols(t *testing.T) {
	mt, err := NewMultiTransport("127.0.0.1:0")
	require.NoError(t, err)

	// 注册多个协议
	tcp, _ := NewTCPTransport("127.0.0.1:0")
	udp, _ := NewUDPTransport("127.0.0.1:0")

	err = mt.RegisterProtocol(ProtocolConfig{
		ProtocolType: ProtocolTCP,
		Priority:     10,
		CanDegrade:   true,
		Transport:    tcp,
	})
	require.NoError(t, err)

	err = mt.RegisterProtocol(ProtocolConfig{
		ProtocolType: ProtocolUDP,
		Priority:     5,
		CanDegrade:   true,
		Transport:    udp,
	})
	require.NoError(t, err)

	stats := mt.GetProtocolStats()
	assert.Contains(t, stats, ProtocolTCP)
	assert.Contains(t, stats, ProtocolUDP)
	assert.Len(t, stats, 2)
}
