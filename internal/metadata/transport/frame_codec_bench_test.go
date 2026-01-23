// Package transport 帧编解码性能基准测试
//
// 测试 TCP/UDP 帧编解码器的性能，包括：
//   - 编码性能
//   - 解码性能
//   - 流式解码性能（TCP 粘包处理）
//   - 协议检测性能
//   - 不同消息大小的性能表现
package transport

import (
	"encoding/binary"
	"testing"

	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// ========================================
// 基准测试辅助函数
// ========================================

// newTestFrame 创建测试帧的辅助函数
func newTestFrame(dataSize int) *Frame {
	data := make([]byte, dataSize)
	for i := range data {
		data[i] = byte(i % 256)
	}
	return NewFrame(12345, 67890, types.MessageTypePut, uint16(types.CodecTypeProtobuf), data)
}

// ========================================
// TCP 帧编解码基准测试
// ========================================

// BenchmarkTCPFrameCodec_Encode_SmallMessage TCP 编码性能 - 小消息（< 1KB）
func BenchmarkTCPFrameCodec_Encode_SmallMessage(b *testing.B) {
	codec := NewTCPFrameCodec()
	frame := newTestFrame(512) // 512 字节

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := codec.EncodeFrame(frame)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkTCPFrameCodec_Encode_MediumMessage TCP 编码性能 - 中等消息（1-50KB）
func BenchmarkTCPFrameCodec_Encode_MediumMessage(b *testing.B) {
	codec := NewTCPFrameCodec()
	frame := newTestFrame(10 * 1024) // 10KB

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := codec.EncodeFrame(frame)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkTCPFrameCodec_Encode_LargeMessage TCP 编码性能 - 大消息（> 50KB）
func BenchmarkTCPFrameCodec_Encode_LargeMessage(b *testing.B) {
	codec := NewTCPFrameCodec()
	frame := newTestFrame(100 * 1024) // 100KB

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := codec.EncodeFrame(frame)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkTCPFrameCodec_Decode_SmallMessage TCP 解码性能 - 小消息
func BenchmarkTCPFrameCodec_Decode_SmallMessage(b *testing.B) {
	codec := NewTCPFrameCodec()
	frame := newTestFrame(512)
	encoded, _ := codec.EncodeFrame(frame)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := codec.DecodeFrame(encoded)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkTCPFrameCodec_Decode_MediumMessage TCP 解码性能 - 中等消息
func BenchmarkTCPFrameCodec_Decode_MediumMessage(b *testing.B) {
	codec := NewTCPFrameCodec()
	frame := newTestFrame(10 * 1024)
	encoded, _ := codec.EncodeFrame(frame)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := codec.DecodeFrame(encoded)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkTCPFrameCodec_Decode_LargeMessage TCP 解码性能 - 大消息
func BenchmarkTCPFrameCodec_Decode_LargeMessage(b *testing.B) {
	codec := NewTCPFrameCodec()
	frame := newTestFrame(100 * 1024)
	encoded, _ := codec.EncodeFrame(frame)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := codec.DecodeFrame(encoded)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// ========================================
// UDP 帧编解码基准测试
// ========================================

// BenchmarkUDPFrameCodec_Encode_SmallMessage UDP 编码性能 - 小消息
func BenchmarkUDPFrameCodec_Encode_SmallMessage(b *testing.B) {
	codec := NewUDPFrameCodec()
	frame := newTestFrame(512)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := codec.EncodeFrame(frame)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkUDPFrameCodec_Encode_MediumMessage UDP 编码性能 - 中等消息
func BenchmarkUDPFrameCodec_Encode_MediumMessage(b *testing.B) {
	codec := NewUDPFrameCodec()
	frame := newTestFrame(10 * 1024)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := codec.EncodeFrame(frame)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkUDPFrameCodec_Decode_SmallMessage UDP 解码性能 - 小消息
func BenchmarkUDPFrameCodec_Decode_SmallMessage(b *testing.B) {
	codec := NewUDPFrameCodec()
	frame := newTestFrame(512)
	encoded, _ := codec.EncodeFrame(frame)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := codec.DecodeFrame(encoded)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkUDPFrameCodec_Decode_MediumMessage UDP 解码性能 - 中等消息
func BenchmarkUDPFrameCodec_Decode_MediumMessage(b *testing.B) {
	codec := NewUDPFrameCodec()
	frame := newTestFrame(10 * 1024)
	encoded, _ := codec.EncodeFrame(frame)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := codec.DecodeFrame(encoded)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// ========================================
// TCP 流式解码基准测试（粘包处理）
// ========================================

// BenchmarkTCPFrameStreamDecoder_Feed_SingleFrames 流式解码 - 单帧处理
func BenchmarkTCPFrameStreamDecoder_Feed_SingleFrames(b *testing.B) {
	codec := NewTCPFrameCodec()
	decoder := NewTCPFrameStreamDecoder()

	// 准备 10 个小帧
	var framesData [][]byte
	for i := 0; i < 10; i++ {
		frame := newTestFrame(512)
		encoded, _ := codec.EncodeFrame(frame)
		framesData = append(framesData, encoded)
	}

	// 合并所有帧数据（模拟粘包）
	var combinedData []byte
	for _, data := range framesData {
		combinedData = append(combinedData, data...)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := decoder.Feed(combinedData)
		if err != nil {
			b.Fatal(err)
		}
		decoder.Reset()
	}
}

// BenchmarkTCPFrameStreamDecoder_Feed_PartialFrames 流式解码 - 部分帧处理
func BenchmarkTCPFrameStreamDecoder_Feed_PartialFrames(b *testing.B) {
	codec := NewTCPFrameCodec()
	decoder := NewTCPFrameStreamDecoder()

	// 准备 1 个大帧
	frame := newTestFrame(50 * 1024) // 50KB
	encoded, _ := codec.EncodeFrame(frame)

	// 将帧数据分成多个小块（模拟分片接收）
	chunkSize := 1024 // 1KB 块

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// 模拟分片接收
		for offset := 0; offset < len(encoded); offset += chunkSize {
			end := offset + chunkSize
			if end > len(encoded) {
				end = len(encoded)
			}
			chunk := encoded[offset:end]

			_, err := decoder.Feed(chunk)
			if err != nil && end != len(encoded) {
				b.Fatal(err)
			}
		}
		decoder.Reset()
	}
}

// ========================================
// 协议检测基准测试
// ========================================

// BenchmarkAutoDetectCodec_DetectProtocol_TCP 协议检测 - TCP 帧
func BenchmarkAutoDetectCodec_DetectProtocol_TCP(b *testing.B) {
	codec := NewAutoDetectCodec()
	tcpCodec := NewTCPFrameCodec()
	frame := newTestFrame(1024)
	tcpData, _ := tcpCodec.EncodeFrame(frame)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := codec.DetectProtocol(tcpData)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkAutoDetectCodec_DetectProtocol_UDP 协议检测 - UDP 帧
func BenchmarkAutoDetectCodec_DetectProtocol_UDP(b *testing.B) {
	codec := NewAutoDetectCodec()
	udpCodec := NewUDPFrameCodec()
	frame := newTestFrame(1024)
	udpData, _ := udpCodec.EncodeFrame(frame)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := codec.DetectProtocol(udpData)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// ========================================
// 编码往返基准测试
// ========================================

// BenchmarkFrameCodec_RoundTrip_TCP_512B TCP 编解码往返 - 512B
func BenchmarkFrameCodec_RoundTrip_TCP_512B(b *testing.B) {
	codec := NewTCPFrameCodec()
	frame := newTestFrame(512)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		encoded, err := codec.EncodeFrame(frame)
		if err != nil {
			b.Fatal(err)
		}
		_, err = codec.DecodeFrame(encoded)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFrameCodec_RoundTrip_TCP_4KB TCP 编解码往返 - 4KB
func BenchmarkFrameCodec_RoundTrip_TCP_4KB(b *testing.B) {
	codec := NewTCPFrameCodec()
	frame := newTestFrame(4 * 1024)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		encoded, err := codec.EncodeFrame(frame)
		if err != nil {
			b.Fatal(err)
		}
		_, err = codec.DecodeFrame(encoded)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFrameCodec_RoundTrip_UDP_512B UDP 编解码往返 - 512B
func BenchmarkFrameCodec_RoundTrip_UDP_512B(b *testing.B) {
	codec := NewUDPFrameCodec()
	frame := newTestFrame(512)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		encoded, err := codec.EncodeFrame(frame)
		if err != nil {
			b.Fatal(err)
		}
		_, err = codec.DecodeFrame(encoded)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFrameCodec_RoundTrip_UDP_4KB UDP 编解码往返 - 4KB
func BenchmarkFrameCodec_RoundTrip_UDP_4KB(b *testing.B) {
	codec := NewUDPFrameCodec()
	frame := newTestFrame(4 * 1024)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		encoded, err := codec.EncodeFrame(frame)
		if err != nil {
			b.Fatal(err)
		}
		_, err = codec.DecodeFrame(encoded)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// ========================================
// 批量处理基准测试
// ========================================

// BenchmarkBatchEncode_TCP 批量编码 - TCP（100 帧）
func BenchmarkBatchEncode_TCP(b *testing.B) {
	codec := NewTCPFrameCodec()

	// 准备 100 个帧
	frames := make([]*Frame, 100)
	for i := 0; i < 100; i++ {
		frames[i] = newTestFrame(1024)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		for _, frame := range frames {
			_, err := codec.EncodeFrame(frame)
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkBatchDecode_TCP 批量解码 - TCP（100 帧）
func BenchmarkBatchDecode_TCP(b *testing.B) {
	codec := NewTCPFrameCodec()

	// 准备 100 个编码后的帧
	encodedFrames := make([][]byte, 100)
	for i := 0; i < 100; i++ {
		frame := newTestFrame(1024)
		encoded, _ := codec.EncodeFrame(frame)
		encodedFrames[i] = encoded
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		for _, encoded := range encodedFrames {
			_, err := codec.DecodeFrame(encoded)
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}

// ========================================
// 内存分配基准测试
// ========================================

// BenchmarkTCPFrameCodec_EstimateSize 大小估算性能
func BenchmarkTCPFrameCodec_EstimateSize(b *testing.B) {
	codec := NewTCPFrameCodec()
	frame := newTestFrame(1024)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = codec.EstimateSize(frame)
	}
}

// BenchmarkUDPFrameCodec_EstimateSize 大小估算性能
func BenchmarkUDPFrameCodec_EstimateSize(b *testing.B) {
	codec := NewUDPFrameCodec()
	frame := newTestFrame(1024)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = codec.EstimateSize(frame)
	}
}

// ========================================
// 性能对比基准测试
// ========================================

// BenchmarkTCPvsUDP_Encode TCP vs UDP 编码性能对比
func BenchmarkTCPvsUDP_Encode(b *testing.B) {
	frame := newTestFrame(1024)

	tcpCodec := NewTCPFrameCodec()
	udpCodec := NewUDPFrameCodec()

	b.Run("TCP", func(b *testing.B) {
		b.ResetTimer()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			_, err := tcpCodec.EncodeFrame(frame)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("UDP", func(b *testing.B) {
		b.ResetTimer()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			_, err := udpCodec.EncodeFrame(frame)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkTCPvsUDP_Decode TCP vs UDP 解码性能对比
func BenchmarkTCPvsUDP_Decode(b *testing.B) {
	frame := newTestFrame(1024)

	tcpCodec := NewTCPFrameCodec()
	udpCodec := NewUDPFrameCodec()

	tcpEncoded, _ := tcpCodec.EncodeFrame(frame)
	udpEncoded, _ := udpCodec.EncodeFrame(frame)

	b.Run("TCP", func(b *testing.B) {
		b.ResetTimer()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			_, err := tcpCodec.DecodeFrame(tcpEncoded)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("UDP", func(b *testing.B) {
		b.ResetTimer()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			_, err := udpCodec.DecodeFrame(udpEncoded)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// ========================================
// 辅助工具基准测试
// ========================================

// BenchmarkBinary_Write_LengthPrefix 二进制写入长度前缀性能
func BenchmarkBinary_Write_LengthPrefix(b *testing.B) {
	frameSize := uint32(1024)
	buf := make([]byte, 4)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		binary.BigEndian.PutUint32(buf, frameSize)
	}
}

// BenchmarkBinary_Read_LengthPrefix 二进制读取长度前缀性能
func BenchmarkBinary_Read_LengthPrefix(b *testing.B) {
	frameSize := uint32(1024)
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, frameSize)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		var size uint32
		binary.BigEndian.PutUint32(buf, size)
		binary.BigEndian.PutUint32(buf, frameSize)
	}
}
