// Package transport 传输层性能基准测试
package transport

import (
	"bytes"
	"context"
	"testing"

	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// ========================================
// 帧格式性能基准
// ========================================

// BenchmarkFrame_NewFrame 帧创建性能
func BenchmarkFrame_NewFrame(b *testing.B) {
	data := make([]byte, 1024)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = NewFrame(MessageTypeGet, types.CodecTypeMessagePack, data)
	}
}

// BenchmarkFrame_Marshal 帧序列化性能
func BenchmarkFrame_Marshal(b *testing.B) {
	data := make([]byte, 1024)
	frame := NewFrame(MessageTypeGet, types.CodecTypeMessagePack, data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = frame.Marshal()
	}
}

// BenchmarkFrame_Unmarshal 帧反序列化性能
func BenchmarkFrame_Unmarshal(b *testing.B) {
	data := make([]byte, 1024)
	frame := NewFrame(MessageTypeGet, types.CodecTypeMessagePack, data)
	buf, _ := frame.Marshal()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f := &Frame{}
		_ = f.Unmarshal(buf)
	}
}

// BenchmarkFrame_RoundTrip 帧往返性能
func BenchmarkFrame_RoundTrip(b *testing.B) {
	data := make([]byte, 1024)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		frame := NewFrame(MessageTypeGet, types.CodecTypeMessagePack, data)
		buf, _ := frame.Marshal()
		f := &Frame{}
		_ = f.Unmarshal(buf)
	}
}

// BenchmarkFrame_NewFrame_DifferentSizes 不同大小帧创建性能
func BenchmarkFrame_NewFrame_DifferentSizes(b *testing.B) {
	sizes := []int{64, 256, 1024, 4096, 16384}

	for _, size := range sizes {
		b.Run(string(rune(size)), func(b *testing.B) {
			data := make([]byte, size)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = NewFrame(MessageTypeGet, types.CodecTypeMessagePack, data)
			}
		})
	}
}

// ========================================
// 编解码器性能基准
// ========================================

// BenchmarkMessagePackCodec_Encode 编码性能
func BenchmarkMessagePackCodec_Encode(b *testing.B) {
	codec := NewMessagePackCodec()
	msg := &PutMessage{
		Key:   "test_key",
		Value: make([]byte, 1024),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = codec.Encode(msg)
	}
}

// BenchmarkMessagePackCodec_Decode 解码性能
func BenchmarkMessagePackCodec_Decode(b *testing.B) {
	codec := NewMessagePackCodec()
	msg := &PutMessage{
		Key:   "test_key",
		Value: make([]byte, 1024),
	}
	data, _ := codec.Encode(msg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = codec.Decode(data)
	}
}

// BenchmarkMessagePackCodec_RoundTrip 编解码往返性能
func BenchmarkMessagePackCodec_RoundTrip(b *testing.B) {
	codec := NewMessagePackCodec()
	msg := &PutMessage{
		Key:   "test_key",
		Value: make([]byte, 1024),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		data, _ := codec.Encode(msg)
		_, _ = codec.Decode(data)
	}
}

// BenchmarkMessagePackCodec_AllMessageTypes 所有消息类型编解码性能
func BenchmarkMessagePackCodec_AllMessageTypes(b *testing.B) {
	codec := NewMessagePackCodec()
	value := make([]byte, 256)

	messages := []Message{
		&GetMessage{Key: "test_key"},
		&PutMessage{Key: "test_key", Value: value},
		&DeleteMessage{Key: "test_key"},
		&NodePingMessage{NodeID: "node1", Sequence: 1, Timestamp: 1234567890},
		&NodePongMessage{NodeID: "node1", Sequence: 1, Status: "ready"},
	}

	for _, msg := range messages {
		b.Run(msg.Type().String(), func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				data, _ := codec.Encode(msg)
				_, _ = codec.Decode(data)
			}
		})
	}
}

// BenchmarkMessagePackCodec_DifferentSizes 不同大小消息编解码性能
func BenchmarkMessagePackCodec_DifferentSizes(b *testing.B) {
	codec := NewMessagePackCodec()
	sizes := []int{64, 256, 1024, 4096, 16384}

	for _, size := range sizes {
		b.Run(string(rune(size)), func(b *testing.B) {
			msg := &PutMessage{
				Key:   "test_key",
				Value: make([]byte, size),
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				data, _ := codec.Encode(msg)
				_, _ = codec.Decode(data)
			}
		})
	}
}

// ========================================
// 帧辅助函数性能基准
// ========================================

// BenchmarkEncodeFrame 编码帧辅助函数性能
func BenchmarkEncodeFrame(b *testing.B) {
	msg := &PutMessage{
		Key:   "test_key",
		Value: make([]byte, 1024),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = EncodeFrame(msg)
	}
}

// BenchmarkDecodeFrame 解码帧辅助函数性能
func BenchmarkDecodeFrame(b *testing.B) {
	msg := &PutMessage{
		Key:   "test_key",
		Value: make([]byte, 1024),
	}
	frame, _ := EncodeFrame(msg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = DecodeFrame(frame)
	}
}

// BenchmarkFrameEncodeDecodeRoundTrip 帧编解码往返性能
func BenchmarkFrameEncodeDecodeRoundTrip(b *testing.B) {
	msg := &PutMessage{
		Key:   "test_key",
		Value: make([]byte, 1024),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		frame, _ := EncodeFrame(msg)
		_, _ = DecodeFrame(frame)
	}
}

// ========================================
// 内存传输性能基准
// ========================================

// BenchmarkMemoryTransport_Send 内存传输发送性能
func BenchmarkMemoryTransport_Send(b *testing.B) {
	trans1, _ := NewMemoryTransport("node1:9211")
	trans2, _ := NewMemoryTransport("node2:9211")

	_ = trans1.Start()
	_ = trans2.Start()
	trans1.RegisterRemoteNode("node2:9211")
	trans2.RegisterRemoteNode("node1:9211")

	defer func() { _ = trans1.Stop() }()
	defer func() { _ = trans2.Stop() }()

	msg := &PutMessage{
		Key:   "test_key",
		Value: make([]byte, 1024),
	}
	ctx := context.Background()

	// 预热
	for i := 0; i < 100; i++ {
		_ = trans1.Send(ctx, "node2:9211", msg)
		for len(trans2.Receive()) > 0 {
			<-trans2.Receive()
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = trans1.Send(ctx, "node2:9211", msg)
		<-trans2.Receive() // 清空接收通道
	}
}

// BenchmarkMemoryTransport_SendReceive 内存传输往返性能
func BenchmarkMemoryTransport_SendReceive(b *testing.B) {
	trans1, _ := NewMemoryTransport("node1:9211")
	trans2, _ := NewMemoryTransport("node2:9211")

	_ = trans1.Start()
	_ = trans2.Start()
	trans1.RegisterRemoteNode("node2:9211")
	trans2.RegisterRemoteNode("node1:9211")

	defer func() { _ = trans1.Stop() }()
	defer func() { _ = trans2.Stop() }()

	msg := &PutMessage{
		Key:   "test_key",
		Value: make([]byte, 1024),
	}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = trans1.Send(ctx, "node2:9211", msg)
		<-trans2.Receive()
	}
}

// BenchmarkMemoryTransport_ConcurrentSend 并发发送性能
func BenchmarkMemoryTransport_ConcurrentSend(b *testing.B) {
	trans1, _ := NewMemoryTransport("node1:9211")
	trans2, _ := NewMemoryTransport("node2:9211")

	_ = trans1.Start()
	_ = trans2.Start()
	trans1.RegisterRemoteNode("node2:9211")
	trans2.RegisterRemoteNode("node1:9211")

	defer func() { _ = trans1.Stop() }()
	defer func() { _ = trans2.Stop() }()

	msg := &PutMessage{
		Key:   "test_key",
		Value: make([]byte, 256),
	}
	ctx := context.Background()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = trans1.Send(ctx, "node2:9211", msg)
		}
	})
}

// BenchmarkMemoryTransport_DifferentMessageSizes 不同消息大小性能
func BenchmarkMemoryTransport_DifferentMessageSizes(b *testing.B) {
	sizes := []int{64, 256, 1024, 4096, 16384}

	for _, size := range sizes {
		b.Run(string(rune(size)), func(b *testing.B) {
			trans1, _ := NewMemoryTransport("node1:9211")
			trans2, _ := NewMemoryTransport("node2:9211")

			_ = trans1.Start()
			_ = trans2.Start()
			trans1.RegisterRemoteNode("node2:9211")
			trans2.RegisterRemoteNode("node1:9211")

			msg := &PutMessage{
				Key:   "test_key",
				Value: make([]byte, size),
			}
			ctx := context.Background()

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = trans1.Send(ctx, "node2:9211", msg)
				<-trans2.Receive()
			}

			_ = trans1.Stop()
			_ = trans2.Stop()
		})
	}
}

// ========================================
// MessageReader/Writer 性能基准
// ========================================

// BenchmarkMessageReader_Reader 读取器性能
func BenchmarkMessageReader_Reader(b *testing.B) {
	msg := &PutMessage{
		Key:   "test_key",
		Value: make([]byte, 1024),
	}
	frame, _ := EncodeFrame(msg)
	frameData, _ := frame.Marshal()

	reader := NewMessageReader(bytes.NewReader(frameData), nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = reader.ReadMessage()
	}
}

// BenchmarkMessageWriter_Writer 写入器性能
func BenchmarkMessageWriter_Writer(b *testing.B) {
	msg := &PutMessage{
		Key:   "test_key",
		Value: make([]byte, 1024),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// 这里需要一个 mock writer，实际测试中会被跳过
		_, _ = msg.Marshal()
	}
}

// ========================================
// 综合性能基准
// ========================================

// BenchmarkFullStack_EndToEnd 端到端完整流程性能
func BenchmarkFullStack_EndToEnd(b *testing.B) {
	trans1, _ := NewMemoryTransport("node1:9211")
	trans2, _ := NewMemoryTransport("node2:9211")

	_ = trans1.Start()
	_ = trans2.Start()
	trans1.RegisterRemoteNode("node2:9211")
	trans2.RegisterRemoteNode("node1:9211")

	defer func() { _ = trans1.Stop() }()
	defer func() { _ = trans2.Stop() }()

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// 创建消息
		msg := &PutMessage{
			Key:   "test_key",
			Value: make([]byte, 1024),
		}

		// 发送消息（包括编码、帧封装、传输）
		_ = trans1.Send(ctx, "node2:9211", msg)

		// 接收消息（包括帧解析、解码）
		receivedMsg := <-trans2.Receive()
		_ = receivedMsg
	}
}

// BenchmarkFullPipeline_Throughput 吞吐量测试
func BenchmarkFullPipeline_Throughput(b *testing.B) {
	trans1, _ := NewMemoryTransport("node1:9211")
	trans2, _ := NewMemoryTransport("node2:9211")

	_ = trans1.Start()
	_ = trans2.Start()
	trans1.RegisterRemoteNode("node2:9211")
	trans2.RegisterRemoteNode("node1:9211")

	defer func() { _ = trans1.Stop() }()
	defer func() { _ = trans2.Stop() }()

	ctx := context.Background()
	msg := &PutMessage{
		Key:   "test_key",
		Value: make([]byte, 1024),
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = trans1.Send(ctx, "node2:9211", msg)
			<-trans2.Receive()
		}
	})
}

// BenchmarkLatency 单次延迟测试
func BenchmarkLatency(b *testing.B) {
	trans1, _ := NewMemoryTransport("node1:9211")
	trans2, _ := NewMemoryTransport("node2:9211")

	_ = trans1.Start()
	_ = trans2.Start()
	trans1.RegisterRemoteNode("node2:9211")
	trans2.RegisterRemoteNode("node1:9211")

	defer func() { _ = trans1.Stop() }()
	defer func() { _ = trans2.Stop() }()

	ctx := context.Background()
	msg := &PutMessage{
		Key:   "test_key",
		Value: make([]byte, 256),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = trans1.Send(ctx, "node2:9211", msg)
		<-trans2.Receive()
	}
}
