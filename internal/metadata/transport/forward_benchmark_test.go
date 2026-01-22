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
	msgExt := MsgExt{
		Message:  NewBaseMessage(MessageTypeGet, []byte("test payload")),
		HopCount: &HopExt{Hop: 5, TotalHop: 10},
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
	for i := 0; i < 100; i++ {
		_, _ = tcpTransport.ForwardMessage(ctx, "127.0.0.1:9999", msgExt)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = tcpTransport.ForwardMessage(ctx, "127.0.0.1:9999", msgExt)
	}
}

// BenchmarkForwardMessage_Single_UDP UDP 单次转发性能测试
func BenchmarkForwardMessage_Single_UDP(b *testing.B) {
	ctx := context.Background()
	msgExt := MsgExt{
		Message:  NewBaseMessage(MessageTypeGet, []byte("test payload")),
		HopCount: &HopExt{Hop: 5, TotalHop: 10},
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
	for i := 0; i < 100; i++ {
		_, _ = udpTransport.ForwardMessage(ctx, "127.0.0.1:9999", msgExt)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = udpTransport.ForwardMessage(ctx, "127.0.0.1:9999", msgExt)
	}
}

// BenchmarkForwardMessage_HopCount Hop Count 递减性能测试
// 验证 Hop Count 递减开销 < 100ns
func BenchmarkForwardMessage_HopCount(b *testing.B) {
	msgExt := MsgExt{
		Message:  NewBaseMessage(MessageTypeGet, []byte("test payload")),
		HopCount: &HopExt{Hop: 5, TotalHop: 10},
	}

	// 预热
	for i := 0; i < 1000; i++ {
		_, _ = prepareForwardMessage(msgExt)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = prepareForwardMessage(msgExt)
	}
}

// BenchmarkForwardMessage_DeepCopy 深拷贝性能测试
// 验证深拷贝开销 < 200ns
func BenchmarkForwardMessage_DeepCopy(b *testing.B) {
	msgExt := MsgExt{
		Message:  NewBaseMessage(MessageTypeGet, []byte("test payload")),
		HopCount: &HopExt{Hop: 5, TotalHop: 10},
		Compress: &CompressExt{CompressID: 2},
		Encrypt:  &EncryptExt{EncryptID: 1, Nonce: []byte("nonce12345678"), Version: "1.0"},
		Segment:  &SegmentExt{Index: 0, Total: 1},
	}

	// 预热
	for i := 0; i < 1000; i++ {
		_ = msgExt.DeepCopy()
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = msgExt.DeepCopy()
	}
}

// BenchmarkForwardMessage_TLV TLV 编码性能测试
// 验证 TLV 编码开销 < 300ns
func BenchmarkForwardMessage_TLV(b *testing.B) {
	msgExt := MsgExt{
		Message:     NewBaseMessage(MessageTypeGet, []byte("test payload")),
		HopCount:    &HopExt{Hop: 5, TotalHop: 10},
		Compress:    &CompressExt{CompressID: 2},
		PriorityExt: &PriorityExt{Priority: PriorityHigh},
	}

	// 预热
	for i := 0; i < 1000; i++ {
		_, _ = msgExt.EncodeTLVs()
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = msgExt.EncodeTLVs()
	}
}

// ========================================
// BatchForwardMessage 性能基准测试
// ========================================

// BenchmarkBatchForwardMessage_TCP TCP 批量转发性能测试
func BenchmarkBatchForwardMessage_TCP(b *testing.B) {
	ctx := context.Background()
	msgExt := MsgExt{
		Message:  NewBaseMessage(MessageTypeGet, []byte("test payload")),
		HopCount: &HopExt{Hop: 5, TotalHop: 10},
	}
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
		_ = tcpTransport.BatchForwardMessage(ctx, addrs, msgExt)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = tcpTransport.BatchForwardMessage(ctx, addrs, msgExt)
	}
}

// BenchmarkBatchForwardMessage_UDP UDP 批量转发性能测试
func BenchmarkBatchForwardMessage_UDP(b *testing.B) {
	ctx := context.Background()
	msgExt := MsgExt{
		Message:  NewBaseMessage(MessageTypeGet, []byte("test payload")),
		HopCount: &HopExt{Hop: 5, TotalHop: 10},
	}
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
		_ = udpTransport.BatchForwardMessage(ctx, addrs, msgExt)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = udpTransport.BatchForwardMessage(ctx, addrs, msgExt)
	}
}

// BenchmarkBatchForwardMessage_Scale 批量转发规模性能测试
func BenchmarkBatchForwardMessage_Scale(b *testing.B) {
	ctx := context.Background()
	msgExt := MsgExt{
		Message:  NewBaseMessage(MessageTypeGet, []byte("test payload")),
		HopCount: &HopExt{Hop: 5, TotalHop: 10},
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

	sizes := []int{1, 5, 10, 50, 100}
	for _, size := range sizes {
		addrs := make([]string, size)
		for i := range addrs {
			addrs[i] = "127.0.0.1:9999"
		}

		b.Run(fmt.Sprintf("Size_%d", size), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_ = tcpTransport.BatchForwardMessage(ctx, addrs, msgExt)
			}
		})
	}
}
