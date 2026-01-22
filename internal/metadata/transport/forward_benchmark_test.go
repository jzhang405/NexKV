// Package transport Transport 性能基准测试
//
// 测试目标：
//   - ForwardMessage 单次转发性能 < 500ns
//   - BatchForwardMessage 批量转发性能 > 单次累加
//   - Hop Count 递减性能 < 100ns
//   - 深拷贝性能 < 200ns
//   - TLV 编码性能 < 300ns
package transport

import (
	"context"
	"fmt"
	"testing"
)

// ========================================
// ForwardMessage 性能基准测试
// ========================================

// BenchmarkForwardMessage_Single TCP 单次转发性能测试
// 验证单次转发开销 < 500ns
func BenchmarkForwardMessage_Single_TCP(b *testing.B) {
	ctx := context.Background()
	baseMsg := NewBaseMessage(MessageTypeGet, []byte("test payload"))
	frame := NewMsgFrame(12345, 1, MessageTypeGet, 1, baseMsg)
	// 添加 Hop Count TLV
	hopTLV := *EncodeHopExt(5, 10)
	frame.TLVs = append(frame.TLVs, hopTLV)

	tcpTransport, err := NewTCPTransport("127.0.0.1:0")
	if err != nil {
		b.Fatalf("创建 TCP Transport 失败: %v", err)
	}
	tcpTransport.SetNodeID(12345)
	if err := tcpTransport.Start(); err != nil {
		b.Fatalf("启动 TCP Transport 失败: %v", err)
	}
	defer func() { _ = tcpTransport.Stop() }()

	// 预热
	for i := 0; i < 100; i++ {
		_, _ = tcpTransport.ForwardMessage(ctx, "127.0.0.1:9999", *frame)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = tcpTransport.ForwardMessage(ctx, "127.0.0.1:9999", *frame)
	}
}

// BenchmarkForwardMessage_Single_UDP UDP 单次转发性能测试
func BenchmarkForwardMessage_Single_UDP(b *testing.B) {
	ctx := context.Background()
	baseMsg := NewBaseMessage(MessageTypeGet, []byte("test payload"))
	frame := NewMsgFrame(12345, 1, MessageTypeGet, 1, baseMsg)
	// 添加 Hop Count TLV
	hopTLV := *EncodeHopExt(5, 10)
	frame.TLVs = append(frame.TLVs, hopTLV)

	udpTransport, err := NewUDPTransport("127.0.0.1:0")
	if err != nil {
		b.Fatalf("创建 UDP Transport 失败: %v", err)
	}
	udpTransport.SetNodeID(12345)
	if err := udpTransport.Start(); err != nil {
		b.Fatalf("启动 UDP Transport 失败: %v", err)
	}
	defer func() { _ = udpTransport.Stop() }()

	// 预热
	for i := 0; i < 100; i++ {
		_, _ = udpTransport.ForwardMessage(ctx, "127.0.0.1:9999", *frame)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = udpTransport.ForwardMessage(ctx, "127.0.0.1:9999", *frame)
	}
}

// BenchmarkForwardMessage_HopCount Hop Count 递减性能测试
// 验证 Hop Count 递减开销 < 100ns
func BenchmarkForwardMessage_HopCount(b *testing.B) {
	baseMsg := NewBaseMessage(MessageTypeGet, []byte("test payload"))
	frame := NewMsgFrame(12345, 1, MessageTypeGet, 1, baseMsg)
	// 添加 Hop Count TLV
	hopTLV := *EncodeHopExt(5, 10)
	frame.TLVs = append(frame.TLVs, hopTLV)

	// 预热
	for i := 0; i < 1000; i++ {
		_, _ = prepareForwardMessage(frame)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = prepareForwardMessage(frame)
	}
}

// BenchmarkForwardMessage_DeepCopy 深拷贝性能测试
// 验证深拷贝开销 < 200ns
func BenchmarkForwardMessage_DeepCopy(b *testing.B) {
	baseMsg := NewBaseMessage(MessageTypeGet, []byte("test payload"))
	frame := NewMsgFrame(12345, 1, MessageTypeGet, 1, baseMsg)
	// 添加多个 TLV
	hopTLV := *EncodeHopExt(5, 10)
	compressTLV := *EncodeCompressExt(2)
	encryptTLV, _ := EncodeEncryptExt(1, []byte("nonce12345678"), "1.0")
	segmentTLV := *EncodeFragmentExt(0, 1)
	frame.TLVs = append(frame.TLVs, hopTLV, compressTLV, *encryptTLV, segmentTLV)

	// 预热
	for i := 0; i < 1000; i++ {
		_ = frame.DeepCopy()
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = frame.DeepCopy()
	}
}

// BenchmarkForwardMessage_TLV TLV 编码性能测试
// 验证 TLV 编码开销 < 300ns
func BenchmarkForwardMessage_TLV(b *testing.B) {
	baseMsg := NewBaseMessage(MessageTypeGet, []byte("test payload"))
	frame := NewMsgFrame(12345, 1, MessageTypeGet, 1, baseMsg)
	// 添加多个 TLV
	hopTLV := *EncodeHopExt(5, 10)
	compressTLV := *EncodeCompressExt(2)
	priorityTLV := *EncodePriorityExt(PriorityHigh)
	frame.TLVs = append(frame.TLVs, hopTLV, compressTLV, priorityTLV)

	// 预热
	for i := 0; i < 1000; i++ {
		_, _ = frame.EncodeTLVs()
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = frame.EncodeTLVs()
	}
}

// ========================================
// BatchForwardMessage 性能基准测试
// ========================================

// BenchmarkBatchForwardMessage_TCP TCP 批量转发性能测试
func BenchmarkBatchForwardMessage_TCP(b *testing.B) {
	ctx := context.Background()
	baseMsg := NewBaseMessage(MessageTypeGet, []byte("test payload"))
	frame := NewMsgFrame(12345, 1, MessageTypeGet, 1, baseMsg)
	// 添加 Hop Count TLV
	hopTLV := *EncodeHopExt(5, 10)
	frame.TLVs = append(frame.TLVs, hopTLV)

	addrs := make([]string, 10)
	for i := range addrs {
		addrs[i] = "127.0.0.1:9999"
	}

	tcpTransport, err := NewTCPTransport("127.0.0.1:0")
	if err != nil {
		b.Fatalf("创建 TCP Transport 失败: %v", err)
	}
	tcpTransport.SetNodeID(12345)
	if err := tcpTransport.Start(); err != nil {
		b.Fatalf("启动 TCP Transport 失败: %v", err)
	}
	defer func() { _ = tcpTransport.Stop() }()

	// 预热
	for i := 0; i < 10; i++ {
		_ = tcpTransport.BatchForwardMessage(ctx, addrs, *frame)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = tcpTransport.BatchForwardMessage(ctx, addrs, *frame)
	}
}

// BenchmarkBatchForwardMessage_UDP UDP 批量转发性能测试
func BenchmarkBatchForwardMessage_UDP(b *testing.B) {
	ctx := context.Background()
	baseMsg := NewBaseMessage(MessageTypeGet, []byte("test payload"))
	frame := NewMsgFrame(12345, 1, MessageTypeGet, 1, baseMsg)
	// 添加 Hop Count TLV
	hopTLV := *EncodeHopExt(5, 10)
	frame.TLVs = append(frame.TLVs, hopTLV)

	addrs := make([]string, 10)
	for i := range addrs {
		addrs[i] = "127.0.0.1:9999"
	}

	udpTransport, err := NewUDPTransport("127.0.0.1:0")
	if err != nil {
		b.Fatalf("创建 UDP Transport 失败: %v", err)
	}
	udpTransport.SetNodeID(12345)
	if err := udpTransport.Start(); err != nil {
		b.Fatalf("启动 UDP Transport 失败: %v", err)
	}
	defer func() { _ = udpTransport.Stop() }()

	// 预热
	for i := 0; i < 10; i++ {
		_ = udpTransport.BatchForwardMessage(ctx, addrs, *frame)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = udpTransport.BatchForwardMessage(ctx, addrs, *frame)
	}
}

// BenchmarkBatchForwardMessage_Scale 批量转发规模性能测试
func BenchmarkBatchForwardMessage_Scale(b *testing.B) {
	ctx := context.Background()
	baseMsg := NewBaseMessage(MessageTypeGet, []byte("test payload"))
	frame := NewMsgFrame(12345, 1, MessageTypeGet, 1, baseMsg)
	// 添加 Hop Count TLV
	hopTLV := *EncodeHopExt(5, 10)
	frame.TLVs = append(frame.TLVs, hopTLV)

	tcpTransport, err := NewTCPTransport("127.0.0.1:0")
	if err != nil {
		b.Fatalf("创建 TCP Transport 失败: %v", err)
	}
	tcpTransport.SetNodeID(12345)
	if err := tcpTransport.Start(); err != nil {
		b.Fatalf("启动 TCP Transport 失败: %v", err)
	}
	defer func() { _ = tcpTransport.Stop() }()

	sizes := []int{1, 5, 10, 50, 100}
	for _, size := range sizes {
		addrs := make([]string, size)
		for i := range addrs {
			addrs[i] = "127.0.0.1:9999"
		}

		b.Run(fmt.Sprintf("Size_%d", size), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_ = tcpTransport.BatchForwardMessage(ctx, addrs, *frame)
			}
		})
	}
}
