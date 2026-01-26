// Package transport RPC 性能基准测试（简化版）
package transport

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// ========================================
// 简化的基准测试 Mock
// ========================================

// simpleBenchmarkMockTransport 简化的 Mock Transport（用于基准测试）
type simpleBenchmarkMockTransport struct {
	sendCount atomic.Uint64
	nodeID    uint64
	msgSeq    atomic.Uint64
}

func newSimpleBenchmarkMockTransport() *simpleBenchmarkMockTransport {
	return &simpleBenchmarkMockTransport{
		nodeID: 1,
	}
}

func (m *simpleBenchmarkMockTransport) Start(nodeID *uint64, msgSeqGenerator func() uint64, listenAddr string) error {
	return nil
}

func (m *simpleBenchmarkMockTransport) Stop() error {
	return nil
}

func (m *simpleBenchmarkMockTransport) Send(ctx context.Context, addr string, msg Message, opt ...SendOpt) error {
	m.sendCount.Add(1)
	return nil
}

func (m *simpleBenchmarkMockTransport) Receive() <-chan MsgFrame {
	ch := make(chan MsgFrame)
	close(ch) // 立即关闭，不发送任何消息
	return ch
}

func (m *simpleBenchmarkMockTransport) ForwardMessage(ctx context.Context, addr string, msgExt MsgFrame) (uint64, error) {
	return 0, fmt.Errorf("not implemented")
}

func (m *simpleBenchmarkMockTransport) GetNodeID() uint64 {
	return m.nodeID
}

func (m *simpleBenchmarkMockTransport) GenerateMsgSeq() uint64 {
	return m.msgSeq.Add(1)
}

func (m *simpleBenchmarkMockTransport) Reply(ctx context.Context, addr string, msg Message, nodeID uint64, msgSeq uint64, connID string, opt ...SendOpt) error {
	// Mock 实现：直接调用 Send（connID 在 mock 中不使用）
	return m.Send(ctx, addr, msg, opt...)
}

// simpleBenchmarkMockMessage 简化的 Mock Message
type simpleBenchmarkMockMessage struct {
	msgType types.MessageType
}

func (m *simpleBenchmarkMockMessage) Type() types.MessageType {
	return m.msgType
}

func (m *simpleBenchmarkMockMessage) Priority() int {
	return 0
}

func (m *simpleBenchmarkMockMessage) MsgRole() types.MsgRole {
	return types.MsgRoleRequest
}

func (m *simpleBenchmarkMockMessage) ExpectResponse() types.ResponseExpectation {
	return types.ExpectResponse
}

func (m *simpleBenchmarkMockMessage) ProtocolType() types.ProtocolType {
	return types.ProtocolTCP
}

func (m *simpleBenchmarkMockMessage) CorrelationID() string {
	return "simple-benchmark-msg"
}

// ========================================
// Dispatcher 基准测试
// ========================================

// BenchmarkDispatcherCreation 测试 Dispatcher 创建性能
func BenchmarkDispatcherCreation(b *testing.B) {
	handler := &mockHandler{}
	config := DefaultDispatcherConfig()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		d, err := NewDispatcher(config, handler)
		if err != nil {
			b.Fatalf("NewDispatcher() failed: %v", err)
		}
		_ = d
	}
}

// BenchmarkDispatcherMessageProcessing 测试消息处理性能
// P0 修复：使用新的默认配置（QueueSize=50000，动态扩缩容 4~32）
func BenchmarkDispatcherMessageProcessing(b *testing.B) {
	handler := &mockHandler{}
	config := DefaultDispatcherConfig() // 使用新的默认配置

	d, err := NewDispatcher(config, handler)
	if err != nil {
		b.Fatalf("NewDispatcher() failed: %v", err)
	}

	if err := d.Start(); err != nil {
		b.Fatalf("Start() failed: %v", err)
	}
	defer func() {
		if err := d.Stop(); err != nil {
			b.Errorf("d.Stop() failed: %v", err)
		}
	}()

	msgChan := make(chan MsgFrame, 1000)
	d.RegisterConnection("benchmark", msgChan)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		msgChan <- newTestMsgFrame(uint64(i), uint64(i), types.MessageTypeGet)
	}

	// 等待处理完成
	time.Sleep(100 * time.Millisecond)
	stats := d.GetStats()

	b.ReportMetric(float64(stats.MsgCount), "msgs")
	b.ReportMetric(float64(stats.DropCount), "drops")
}

// BenchmarkDispatcherParallelProcessing 测试并发消息处理性能
// P0 修复：使用新的默认配置（QueueSize=50000，动态扩缩容 4~32）
func BenchmarkDispatcherParallelProcessing(b *testing.B) {
	handler := &mockHandler{
		handleDelay: 1 * time.Microsecond, // 模拟 1μs 处理延迟
	}
	config := DefaultDispatcherConfig() // 使用新的默认配置

	d, err := NewDispatcher(config, handler)
	if err != nil {
		b.Fatalf("NewDispatcher() failed: %v", err)
	}

	if err := d.Start(); err != nil {
		b.Fatalf("Start() failed: %v", err)
	}
	defer func() {
		if err := d.Stop(); err != nil {
			b.Errorf("d.Stop() failed: %v", err)
		}
	}()

	msgChan := make(chan MsgFrame, 10000)
	d.RegisterConnection("benchmark", msgChan)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			msgChan <- newTestMsgFrame(uint64(i), uint64(i), types.MessageTypeGet)
			i++
		}
	})

	// 等待处理完成
	time.Sleep(200 * time.Millisecond)
	stats := d.GetStats()

	b.ReportMetric(float64(stats.MsgCount), "msgs")
	b.ReportMetric(float64(stats.DropCount), "drops")
}

// ========================================
// RPC Client 基准测试
// ========================================

// BenchmarkRPCClientCreation 测试 RPC Client 创建性能
func BenchmarkRPCClientCreation(b *testing.B) {
	tcpTransport := newSimpleBenchmarkMockTransport()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		client, err := NewRPCClient(tcpTransport, nil, nil)
		if err != nil {
			b.Fatalf("NewRPCClient() failed: %v", err)
		}
		_ = client
	}
}

// BenchmarkRPCClientSend 测试 RPC 发送性能（不等待响应）
func BenchmarkRPCClientSend(b *testing.B) {
	tcpTransport := newSimpleBenchmarkMockTransport()
	_, err := NewRPCClient(tcpTransport, nil, nil)
	if err != nil {
		b.Fatalf("NewRPCClient() failed: %v", err)
	}

	ctx := context.Background()
	msg := &simpleBenchmarkMockMessage{msgType: types.MessageTypeGet}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		addr := fmt.Sprintf("127.0.0.1:92%d", i%100)
		_ = tcpTransport.Send(ctx, addr, msg)
	}

	sent := tcpTransport.sendCount.Load()
	b.ReportMetric(float64(sent), "sent")
}

// BenchmarkRPCClientParallelSend 测试并发 RPC 发送性能
func BenchmarkRPCClientParallelSend(b *testing.B) {
	tcpTransport := newSimpleBenchmarkMockTransport()
	_, err := NewRPCClient(tcpTransport, nil, nil)
	if err != nil {
		b.Fatalf("NewRPCClient() failed: %v", err)
	}

	ctx := context.Background()
	msg := &simpleBenchmarkMockMessage{msgType: types.MessageTypeGet}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			addr := fmt.Sprintf("127.0.0.1:92%d", i%100)
			_ = tcpTransport.Send(ctx, addr, msg)
			i++
		}
	})

	sent := tcpTransport.sendCount.Load()
	b.ReportMetric(float64(sent), "sent")
}

// ========================================
// Request Table 基准测试
// ========================================

// BenchmarkRequestTableOperations 测试请求表操作性能
func BenchmarkRequestTableOperations(b *testing.B) {
	rt := newRequestTable()

	b.Run("Add", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			correlationID := fmt.Sprintf("corr-%d", i)
			entry := rt.add(correlationID)
			_ = entry
		}
	})

	b.Run("Get", func(b *testing.B) {
		// 预先添加 1000 个条目
		for i := 0; i < 1000; i++ {
			correlationID := fmt.Sprintf("corr-%d", i)
			rt.add(correlationID)
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			correlationID := fmt.Sprintf("corr-%d", i%1000)
			_ = rt.get(correlationID)
		}
	})

	b.Run("Remove", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			correlationID := fmt.Sprintf("corr-%d", i)
			rt.remove(correlationID)
		}
	})
}

// ========================================
// 内存分配基准测试
// ========================================

// BenchmarkMsgFrameAllocation 测试 MsgFrame 内存分配
func BenchmarkMsgFrameAllocation(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = newTestMsgFrame(uint64(i), uint64(i), types.MessageTypeGet)
	}
}

// BenchmarkRPCClientAllocation 测试 RPC Client 内存分配
func BenchmarkRPCClientAllocation(b *testing.B) {
	tcpTransport := newSimpleBenchmarkMockTransport()

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		client, err := NewRPCClient(tcpTransport, nil, nil)
		if err != nil {
			b.Fatalf("NewRPCClient() failed: %v", err)
		}
		_ = client
	}
}

// BenchmarkRPCServerAllocation 测试 RPC Server 内存分配
func BenchmarkRPCServerAllocation(b *testing.B) {
	tcpTransport := newSimpleBenchmarkMockTransport()
	handler := &mockRPCHandler{}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		server, err := NewRPCServer(tcpTransport, nil, handler, nil)
		if err != nil {
			b.Fatalf("NewRPCServer() failed: %v", err)
		}
		_ = server
	}
}

// ========================================
// QPS 基准测试
// ========================================

// BenchmarkTransportSendQPS 测试 Transport 发送 QPS
func BenchmarkTransportSendQPS(b *testing.B) {
	tcpTransport := newSimpleBenchmarkMockTransport()
	ctx := context.Background()
	msg := &simpleBenchmarkMockMessage{msgType: types.MessageTypeGet}

	b.ResetTimer()

	startTime := time.Now()
	for i := 0; i < b.N; i++ {
		addr := fmt.Sprintf("127.0.0.1:92%d", i%100)
		_ = tcpTransport.Send(ctx, addr, msg)
	}
	elapsed := time.Since(startTime)

	qps := float64(b.N) / elapsed.Seconds()
	b.ReportMetric(qps, "qps")
}

// BenchmarkDispatcherThroughput 测试 Dispatcher 吞吐量
func BenchmarkDispatcherThroughput(b *testing.B) {
	handler := &mockHandler{}
	config := DefaultDispatcherConfig() // P0 修复：使用新的默认配置（QueueSize=50000，动态扩缩容 4~32）

	d, err := NewDispatcher(config, handler)
	if err != nil {
		b.Fatalf("NewDispatcher() failed: %v", err)
	}

	if err := d.Start(); err != nil {
		b.Fatalf("Start() failed: %v", err)
	}
	defer func() {
		if err := d.Stop(); err != nil {
			b.Errorf("d.Stop() failed: %v", err)
		}
	}()

	msgChan := make(chan MsgFrame, 10000)
	d.RegisterConnection("benchmark", msgChan)

	b.ResetTimer()

	startTime := time.Now()
	for i := 0; i < b.N; i++ {
		msgChan <- newTestMsgFrame(uint64(i), uint64(i), types.MessageTypeGet)
	}
	elapsed := time.Since(startTime)

	// 等待处理完成
	time.Sleep(100 * time.Millisecond)
	stats := d.GetStats()

	qps := float64(stats.MsgCount) / elapsed.Seconds()
	b.ReportMetric(qps, "qps")
	b.ReportMetric(float64(stats.DropCount), "drops")
}

// ========================================
// 并发性能基准测试
// ========================================

// BenchmarkConcurrentDispatcherRegistration 测试并发注册连接性能
func BenchmarkConcurrentDispatcherRegistration(b *testing.B) {
	handler := &mockHandler{}
	config := DefaultDispatcherConfig()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			d, err := NewDispatcher(config, handler)
			if err != nil {
				b.Fatalf("NewDispatcher() failed: %v", err)
			}

			if err := d.Start(); err != nil {
				b.Fatalf("Start() failed: %v", err)
			}

			// 注册 100 个连接
			for j := 0; j < 100; j++ {
				addr := fmt.Sprintf("127.0.0.%d:9211", j)
				msgChan := make(chan MsgFrame, 10)
				d.RegisterConnection(addr, msgChan)
			}

			if err := d.Stop(); err != nil {
				b.Errorf("d.Stop() failed: %v", err)
			}
			i++
		}
	})
}

// BenchmarkConcurrentRPCClientCreation 测试并发创建 RPC Client 性能
func BenchmarkConcurrentRPCClientCreation(b *testing.B) {
	tcpTransport := newSimpleBenchmarkMockTransport()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			client, err := NewRPCClient(tcpTransport, nil, nil)
			if err != nil {
				b.Fatalf("NewRPCClient() failed: %v", err)
			}
			_ = client
		}
	})
}

// ========================================
// 延迟基准测试
// ========================================

// BenchmarkDispatcherLatency 测试 Dispatcher 处理延迟
func BenchmarkDispatcherLatency(b *testing.B) {
	handler := &mockHandler{
		handleDelay: 10 * time.Microsecond, // 10μs 处理延迟
	}
	config := DefaultDispatcherConfig() // P0 修复：使用新的默认配置（QueueSize=50000，动态扩缩容 4~32）

	d, err := NewDispatcher(config, handler)
	if err != nil {
		b.Fatalf("NewDispatcher() failed: %v", err)
	}

	if err := d.Start(); err != nil {
		b.Fatalf("Start() failed: %v", err)
	}
	defer func() {
		if err := d.Stop(); err != nil {
			b.Errorf("d.Stop() failed: %v", err)
		}
	}()

	msgChan := make(chan MsgFrame, 100)
	d.RegisterConnection("benchmark", msgChan)

	var totalLatency time.Duration
	sampleCount := 100

	b.ResetTimer()

	for i := 0; i < sampleCount; i++ {
		msg := newTestMsgFrame(uint64(i), uint64(i), types.MessageTypeGet)
		start := time.Now()
		msgChan <- msg

		// 等待处理
		time.Sleep(50 * time.Microsecond)
		totalLatency += time.Since(start)
	}

	avgLatency := totalLatency / time.Duration(sampleCount)
	b.ReportMetric(float64(avgLatency.Microseconds()), "avg_latency_us")

	stats := d.GetStats()
	b.ReportMetric(float64(stats.MsgCount), "processed")
}

// ========================================
// 扩展性基准测试
// ========================================

// BenchmarkDispatcherScalability 测试 Dispatcher 扩展性（不同 Worker 数量）
// P0 修复：使用新的默认配置（QueueSize=50000，动态扩缩容 4~32）
func BenchmarkDispatcherScalability(b *testing.B) {
	workerCounts := []int{1, 2, 4, 8, 16, 32}

	for _, workerCount := range workerCounts {
		b.Run(fmt.Sprintf("workers-%d", workerCount), func(b *testing.B) {
			handler := &mockHandler{
				handleDelay: 10 * time.Microsecond,
			}
			// P0 修复：基于默认配置，只修改 WorkerCount
			config := DefaultDispatcherConfig()
			config.WorkerCount = workerCount

			d, err := NewDispatcher(config, handler)
			if err != nil {
				b.Fatalf("NewDispatcher() failed: %v", err)
			}

			if err := d.Start(); err != nil {
				b.Fatalf("Start() failed: %v", err)
			}
			defer func() {
				if err := d.Stop(); err != nil {
					b.Errorf("d.Stop() failed: %v", err)
				}
			}()

			msgChan := make(chan MsgFrame, 1000)
			d.RegisterConnection("benchmark", msgChan)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				msgChan <- newTestMsgFrame(uint64(i), uint64(i), types.MessageTypeGet)
			}

			time.Sleep(100 * time.Millisecond)
			stats := d.GetStats()

			b.ReportMetric(float64(stats.MsgCount), "msgs")
			b.ReportMetric(float64(workerCount), "workers")
		})
	}
}
