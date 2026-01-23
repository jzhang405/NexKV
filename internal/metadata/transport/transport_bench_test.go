// Package transport 传输层性能基准测试
package transport

import (
	"bytes"
	"context"
	"fmt"
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
		_ = NewFrame(0, 0, types.MessageTypeGet, uint16(types.CodecTypeMessagePack), data)
	}
}

// BenchmarkFrame_Marshal 帧序列化性能
func BenchmarkFrame_Marshal(b *testing.B) {
	data := make([]byte, 1024)
	frame := NewFrame(0, 0, types.MessageTypeGet, uint16(types.CodecTypeMessagePack), data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = frame.Marshal()
	}
}

// BenchmarkFrame_Unmarshal 帧反序列化性能
func BenchmarkFrame_Unmarshal(b *testing.B) {
	data := make([]byte, 1024)
	frame := NewFrame(0, 0, types.MessageTypeGet, uint16(types.CodecTypeMessagePack), data)
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
		frame := NewFrame(0, 0, types.MessageTypeGet, uint16(types.CodecTypeMessagePack), data)
		buf, _ := frame.Marshal()
		f := &Frame{}
		_ = f.Unmarshal(buf)
	}
}

// BenchmarkFrame_NewFrame_DifferentSizes 不同大小帧创建性能
func BenchmarkFrame_NewFrame_DifferentSizes(b *testing.B) {
	sizes := []int{64, 256, 1024, 4096, 16384}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("%d", size), func(b *testing.B) {
			data := make([]byte, size)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = NewFrame(0, 0, types.MessageTypeGet, uint16(types.CodecTypeMessagePack), data)
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
		_, _ = codec.Decode(msg.Type(), data)
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
		_, _ = codec.Decode(msg.Type(), data)
	}
}

// BenchmarkJSONCodec_Encode JSON 编码性能
func BenchmarkJSONCodec_Encode(b *testing.B) {
	codec := NewJSONCodec()
	msg := &PutMessage{
		Key:   "test_key",
		Value: make([]byte, 1024),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = codec.Encode(msg)
	}
}

// BenchmarkJSONCodec_Decode JSON 解码性能
func BenchmarkJSONCodec_Decode(b *testing.B) {
	codec := NewJSONCodec()
	msg := &PutMessage{
		Key:   "test_key",
		Value: make([]byte, 1024),
	}
	data, _ := codec.Encode(msg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = codec.Decode(msg.Type(), data)
	}
}

// BenchmarkJSONCodec_RoundTrip JSON 编解码往返性能
func BenchmarkJSONCodec_RoundTrip(b *testing.B) {
	codec := NewJSONCodec()
	msg := &PutMessage{
		Key:   "test_key",
		Value: make([]byte, 1024),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		data, _ := codec.Encode(msg)
		_, _ = codec.Decode(msg.Type(), data)
	}
}

// BenchmarkProtobufCodec_Encode Protobuf 编码性能
func BenchmarkProtobufCodec_Encode(b *testing.B) {
	codec := NewProtobufCodec()
	msg := &PutMessage{
		Key:   "test_key",
		Value: make([]byte, 1024),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = codec.Encode(msg)
	}
}

// BenchmarkProtobufCodec_Decode Protobuf 解码性能
func BenchmarkProtobufCodec_Decode(b *testing.B) {
	codec := NewProtobufCodec()
	msg := &PutMessage{
		Key:   "test_key",
		Value: make([]byte, 1024),
	}
	data, _ := codec.Encode(msg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = codec.Decode(msg.Type(), data)
	}
}

// BenchmarkProtobufCodec_RoundTrip Protobuf 编解码往返性能
func BenchmarkProtobufCodec_RoundTrip(b *testing.B) {
	codec := NewProtobufCodec()
	msg := &PutMessage{
		Key:   "test_key",
		Value: make([]byte, 1024),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		data, _ := codec.Encode(msg)
		_, _ = codec.Decode(msg.Type(), data)
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
				_, _ = codec.Decode(msg.Type(), data)
			}
		})
	}
}

// BenchmarkJSONCodec_AllMessageTypes JSON 所有消息类型编解码性能
func BenchmarkJSONCodec_AllMessageTypes(b *testing.B) {
	codec := NewJSONCodec()
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
				_, _ = codec.Decode(msg.Type(), data)
			}
		})
	}
}

// BenchmarkProtobufCodec_AllMessageTypes Protobuf 所有消息类型编解码性能
func BenchmarkProtobufCodec_AllMessageTypes(b *testing.B) {
	codec := NewProtobufCodec()
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
				_, _ = codec.Decode(msg.Type(), data)
			}
		})
	}
}

// BenchmarkMessagePackCodec_DifferentSizes 不同大小消息编解码性能
func BenchmarkMessagePackCodec_DifferentSizes(b *testing.B) {
	codec := NewMessagePackCodec()
	sizes := []int{64, 256, 1024, 4096, 16384}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("%d", size), func(b *testing.B) {
			msg := &PutMessage{
				Key:   "test_key",
				Value: make([]byte, size),
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				data, _ := codec.Encode(msg)
				_, _ = codec.Decode(msg.Type(), data)
			}
		})
	}
}

// BenchmarkJSONCodec_DifferentSizes JSON 不同大小消息编解码性能
func BenchmarkJSONCodec_DifferentSizes(b *testing.B) {
	codec := NewJSONCodec()
	sizes := []int{64, 256, 1024, 4096, 16384}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("%d", size), func(b *testing.B) {
			msg := &PutMessage{
				Key:   "test_key",
				Value: make([]byte, size),
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				data, _ := codec.Encode(msg)
				_, _ = codec.Decode(msg.Type(), data)
			}
		})
	}
}

// BenchmarkProtobufCodec_DifferentSizes Protobuf 不同大小消息编解码性能
func BenchmarkProtobufCodec_DifferentSizes(b *testing.B) {
	codec := NewProtobufCodec()
	sizes := []int{64, 256, 1024, 4096, 16384}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("%d", size), func(b *testing.B) {
			msg := &PutMessage{
				Key:   "test_key",
				Value: make([]byte, size),
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				data, _ := codec.Encode(msg)
				_, _ = codec.Decode(msg.Type(), data)
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
		_, _ = EncodeFrame(msg, 0, 0)
	}
}

// BenchmarkDecodeFrame 解码帧辅助函数性能
func BenchmarkDecodeFrame(b *testing.B) {
	msg := &PutMessage{
		Key:   "test_key",
		Value: make([]byte, 1024),
	}
	frame, _ := EncodeFrame(msg, 0, 0)

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
		frame, _ := EncodeFrame(msg, 0, 0)
		_, _ = DecodeFrame(frame)
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
	frame, _ := EncodeFrame(msg, 0, 0)
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
	codec, _ := NewCodec(types.CodecTypeProtobuf)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// 使用 Codec 进行序列化
		_, _ = codec.Encode(msg)
	}
}

// ========================================
// 三 Codec 完整性能对比
// ========================================

// allTestMessages 所有测试消息（完整覆盖）
var allTestMessages = []Message{
	// 元数据操作消息 (100-149)
	&GetMessage{Key: "test_key"},
	&PutMessage{Key: "test_key", Value: make([]byte, 1024)},
	&DeleteMessage{Key: "test_key"},

	// Gossip 协议消息 (150-199)
	&GossipSyncMessage{
		Version:   100,
		Metadata:  map[string][]byte{"k1": []byte("v1"), "k2": []byte("v2")},
		Timestamp: 1705689600000000000,
	},

	// Quorum 协议消息 (200-249)
	&QuorumProposeMessage{
		ProposalID: "prop-123",
		Key:        "test_key",
		Value:      []byte("test_value"),
		Operation:  "put",
		Proposer:   "node-1",
		Timestamp:  1705689600000000000,
	},

	// 2PC 协议消息 (250-299)
	&TwoPCPrepareMessage{
		TransactionID: "tx-123",
		Participants:  []string{"node1", "node2", "node3"},
		Operations: []Operation{
			{Type: "put", Key: "k1", Value: []byte("v1")},
			{Type: "put", Key: "k2", Value: []byte("v2")},
		},
		Timeout: 30000,
	},

	// 节点管理消息 (300-349)
	&NodePingMessage{
		NodeID:    "node-1",
		Sequence:  100,
		Timestamp: 1705689600000000000,
	},
	&NodePongMessage{
		NodeID:   "node-1",
		Sequence: 100,
		Status:   "ready",
	},
	&NodeJoinMessage{
		NodeID:   "node-2",
		Addr:     "192.168.1.10:9211",
		Role:     "follower",
		ParentID: "node-1",
	},
	&NodeLeaveMessage{
		NodeID: "node-3",
		Reason: "手动下线",
	},

	// 集群管理消息 (350-399)
	&LeaderElectionMessage{
		ElectionID:       "election-123",
		NodeID:           "node-1",
		ElectionPriority: 1,
	},
}

// BenchmarkThreeCodec_AllMessages 三种 Codec 所有消息性能对比
func BenchmarkThreeCodec_AllMessages(b *testing.B) {
	codecs := []struct {
		name  string
		codec Codec
	}{
		{"JSON", NewJSONCodec()},
		{"MessagePack", NewMessagePackCodec()},
		{"Protobuf", NewProtobufCodec()},
	}

	for _, tc := range codecs {
		b.Run(tc.name, func(b *testing.B) {
			for _, msg := range allTestMessages {
				b.Run(msg.Type().String(), func(b *testing.B) {
					data, _ := tc.codec.Encode(msg)

					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						// 编码
						encoded, _ := tc.codec.Encode(msg)
						// 解码
						_, _ = tc.codec.Decode(msg.Type(), encoded)
					}

					// 报告内存分配
					b.ReportMetric(float64(len(data)), "bytes")
				})
			}
		})
	}
}

// BenchmarkThreeCodec_EncodeOnly 三种 Codec 编码性能对比
func BenchmarkThreeCodec_EncodeOnly(b *testing.B) {
	codecs := []struct {
		name  string
		codec Codec
	}{
		{"JSON", NewJSONCodec()},
		{"MessagePack", NewMessagePackCodec()},
		{"Protobuf", NewProtobufCodec()},
	}

	for _, tc := range codecs {
		b.Run(tc.name, func(b *testing.B) {
			for _, msg := range allTestMessages {
				b.Run(msg.Type().String(), func(b *testing.B) {
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						_, _ = tc.codec.Encode(msg)
					}
				})
			}
		})
	}
}

// BenchmarkThreeCodec_DecodeOnly 三种 Codec 解码性能对比
func BenchmarkThreeCodec_DecodeOnly(b *testing.B) {
	codecs := []struct {
		name  string
		codec Codec
	}{
		{"JSON", NewJSONCodec()},
		{"MessagePack", NewMessagePackCodec()},
		{"Protobuf", NewProtobufCodec()},
	}

	for _, tc := range codecs {
		b.Run(tc.name, func(b *testing.B) {
			for _, msg := range allTestMessages {
				b.Run(msg.Type().String(), func(b *testing.B) {
					encoded, _ := tc.codec.Encode(msg)

					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						_, _ = tc.codec.Decode(msg.Type(), encoded)
					}
				})
			}
		})
	}
}

// BenchmarkThreeCodec_SerializationSize 三种 Codec 序列化后大小对比
func BenchmarkThreeCodec_SerializationSize(b *testing.B) {
	codecs := []struct {
		name  string
		codec Codec
	}{
		{"JSON", NewJSONCodec()},
		{"MessagePack", NewMessagePackCodec()},
		{"Protobuf", NewProtobufCodec()},
	}

	b.ResetTimer()
	for _, tc := range codecs {
		b.Run(tc.name, func(b *testing.B) {
			for _, msg := range allTestMessages {
				b.Run(msg.Type().String(), func(b *testing.B) {
					// 只报告大小，不运行实际 benchmark 循环
					b.StopTimer()
					encoded, _ := tc.codec.Encode(msg)
					b.ReportMetric(float64(len(encoded)), "bytes")
					b.StartTimer()
					// 空循环，b.N 由框架控制
					for i := 0; i < b.N; i++ {
						// 已在上方编码，此处为空
					}
				})
			}
		})
	}
}

// ========================================
// TCP vs UDP 性能对比基准测试
// ========================================

// BenchmarkTCPVsUDP_Send 小消息发送性能对比
func BenchmarkTCPVsUDP_Send(b *testing.B) {
	ctx := context.Background()
	msg := &GetMessage{Key: "benchmark-key"}

	b.Run("TCP", func(b *testing.B) {
		server := createTCPTransportForBench(b)
		if err := server.Start(nil, nil); err != nil {
			b.Fatalf("启动 server 失败: %v", err)
		}
		defer func() { _ = server.Stop() }()

		client := createTCPTransportForBench(b)
		if err := client.Start(nil, nil); err != nil {
			b.Fatalf("启动 client 失败: %v", err)
		}
		defer func() { _ = client.Stop() }()

		addr := server.GetLocalAddr()

		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = client.Send(ctx, addr, msg)
		}
	})

	b.Run("UDP", func(b *testing.B) {
		server := createUDPTransportForBench(b)
		if err := server.Start(nil, nil); err != nil {
			b.Fatalf("启动 server 失败: %v", err)
		}
		defer func() { _ = server.Stop() }()

		client := createUDPTransportForBench(b)
		if err := client.Start(nil, nil); err != nil {
			b.Fatalf("启动 client 失败: %v", err)
		}
		defer func() { _ = client.Stop() }()

		addr := server.GetLocalAddr()

		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = client.Send(ctx, addr, msg)
		}
	})
}

// BenchmarkTCPVsUDP_SendLarge 大消息发送性能对比（UDP 需要分片）
func BenchmarkTCPVsUDP_SendLarge(b *testing.B) {
	ctx := context.Background()
	largeValue := make([]byte, 2048) // 2KB
	msg := &PutMessage{Key: "large-key", Value: largeValue}

	b.Run("TCP", func(b *testing.B) {
		server := createTCPTransportForBench(b)
		if err := server.Start(nil, nil); err != nil {
			b.Fatalf("启动 server 失败: %v", err)
		}
		defer func() { _ = server.Stop() }()

		client := createTCPTransportForBench(b)
		if err := client.Start(nil, nil); err != nil {
			b.Fatalf("启动 client 失败: %v", err)
		}
		defer func() { _ = client.Stop() }()

		addr := server.GetLocalAddr()

		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = client.Send(ctx, addr, msg)
		}
	})

	b.Run("UDP", func(b *testing.B) {
		server := createUDPTransportForBench(b)
		serverNodeID := uint64(1)
		if err := server.Start(&serverNodeID, nil); err != nil {
			b.Fatalf("启动 server 失败: %v", err)
		}
		defer func() { _ = server.Stop() }()

		client := createUDPTransportForBench(b)
		clientNodeID := uint64(2)
		if err := client.Start(&clientNodeID, nil); err != nil {
			b.Fatalf("启动 client 失败: %v", err)
		}
		defer func() { _ = client.Stop() }()

		addr := server.GetLocalAddr()

		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = client.Send(ctx, addr, msg)
		}
	})
}

// BenchmarkTCPVsUDP_ConcurrentSend 并发发送性能对比
func BenchmarkTCPVsUDP_ConcurrentSend(b *testing.B) {
	ctx := context.Background()
	msg := &GetMessage{Key: "benchmark-key"}

	b.Run("TCP", func(b *testing.B) {
		server := createTCPTransportForBench(b)
		if err := server.Start(nil, nil); err != nil {
			b.Fatalf("启动 server 失败: %v", err)
		}
		defer func() { _ = server.Stop() }()

		client := createTCPTransportForBench(b)
		if err := client.Start(nil, nil); err != nil {
			b.Fatalf("启动 client 失败: %v", err)
		}
		defer func() { _ = client.Stop() }()

		addr := server.GetLocalAddr()

		b.ResetTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				_ = client.Send(ctx, addr, msg)
			}
		})
	})

	b.Run("UDP", func(b *testing.B) {
		server := createUDPTransportForBench(b)
		if err := server.Start(nil, nil); err != nil {
			b.Fatalf("启动 server 失败: %v", err)
		}
		defer func() { _ = server.Stop() }()

		client := createUDPTransportForBench(b)
		if err := client.Start(nil, nil); err != nil {
			b.Fatalf("启动 client 失败: %v", err)
		}
		defer func() { _ = client.Stop() }()

		addr := server.GetLocalAddr()

		b.ResetTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				_ = client.Send(ctx, addr, msg)
			}
		})
	})
}

// BenchmarkTCPVsUDP_VaryingSizes 不同消息大小性能对比
func BenchmarkTCPVsUDP_VaryingSizes(b *testing.B) {
	sizes := []int{64, 256, 1024, 4096, 16384}

	for _, size := range sizes {
		value := make([]byte, size)
		msg := &PutMessage{Key: "test-key", Value: value}

		b.Run(fmt.Sprintf("%d_TCP", size), func(b *testing.B) {
			server := createTCPTransportForBench(b)
			if err := server.Start(nil, nil); err != nil {
				b.Fatalf("启动 server 失败: %v", err)
			}
			defer func() { _ = server.Stop() }()

			client := createTCPTransportForBench(b)
			if err := client.Start(nil, nil); err != nil {
				b.Fatalf("启动 client 失败: %v", err)
			}
			defer func() { _ = client.Stop() }()

			addr := server.GetLocalAddr()
			ctx := context.Background()

			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = client.Send(ctx, addr, msg)
			}
		})

		b.Run(fmt.Sprintf("%d_UDP", size), func(b *testing.B) {
			server := createUDPTransportForBench(b)
			serverNodeID := uint64(1)
			if err := server.Start(&serverNodeID, nil); err != nil {
				b.Fatalf("启动 server 失败: %v", err)
			}
			defer func() { _ = server.Stop() }()

			client := createUDPTransportForBench(b)
			clientNodeID := uint64(2)
			if err := client.Start(&clientNodeID, nil); err != nil {
				b.Fatalf("启动 client 失败: %v", err)
			}
			defer func() { _ = client.Stop() }()

			addr := server.GetLocalAddr()
			ctx := context.Background()

			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = client.Send(ctx, addr, msg)
			}
		})
	}
}

// ========================================
// TCP vs UDP 辅助函数
// ========================================

// createTCPTransportForBench 创建基准测试用的 TCP Transport
func createTCPTransportForBench(b *testing.B) *TCPTransport {
	b.Helper()
	trans, err := NewTCPTransport("127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	return trans
}

// createUDPTransportForBench 创建基准测试用的 UDP Transport
func createUDPTransportForBench(b *testing.B) *UDPTransport {
	b.Helper()
	trans, err := NewUDPTransport("127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	return trans
}

/*
性能对比说明：

预期结果：
1. 小消息（< 1KB）：
   - UDP 延迟 < TCP（无连接开销）
   - UDP 吞吐量 > TCP（无连接池锁竞争）

2. 大消息（> 2KB，需要 UDP 分片）：
   - TCP 延迟 ≈ UDP（TCP 流式传输 vs UDP 分片重组）
   - TCP 吞吐量 > UDP（UDP 分片有额外处理开销）

3. 并发场景：
   - UDP > TCP（无连接池锁竞争）

4. 内存分配：
   - TCP < UDP（UDP 需要分片缓冲区）

运行基准测试对比：
  go test -bench=BenchmarkTCPVsUDP -benchmem ./internal/metadata/transport/...

查看详细对比：
  go test -bench=. -benchmem -cpuprofile=cpu.prof ./internal/metadata/transport/
  go tool pprof cpu.prof
*/
