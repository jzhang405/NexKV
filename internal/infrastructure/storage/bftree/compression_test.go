// Package bftree 压缩功能测试
package bftree

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestNoneCompressor 测试不压缩
func TestNoneCompressor(t *testing.T) {
	compressor := &NoneCompressor{}
	assert.Equal(t, CompressionNone, compressor.Type())

	data := []byte("hello world")

	// 压缩
	compressed, err := compressor.Compress(data)
	assert.NoError(t, err)
	assert.Equal(t, data, compressed)

	// 解压
	decompressed, err := compressor.Decompress(compressed)
	assert.NoError(t, err)
	assert.Equal(t, data, decompressed)
}

// TestSnappyCompressor 测试 Snappy 压缩
func TestSnappyCompressor(t *testing.T) {
	compressor := &SnappyCompressor{}
	assert.Equal(t, CompressionSnappy, compressor.Type())

	tests := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"small", []byte("hello")},
		{"medium", bytes.Repeat([]byte("abc"), 100)},
		{"large", bytes.Repeat([]byte("xyz"), 1000)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 压缩
			compressed, err := compressor.Compress(tt.data)
			assert.NoError(t, err)

			// 解压
			decompressed, err := compressor.Decompress(compressed)
			assert.NoError(t, err)
			assert.Equal(t, tt.data, decompressed)

			// 验证压缩率
			if len(tt.data) > 0 {
				ratio := float64(len(compressed)) / float64(len(tt.data))
				t.Logf("Compression ratio: %.2f%%", ratio*100)
			}
		})
	}
}

// TestLZ4Compressor 测试 LZ4 压缩
func TestLZ4Compressor(t *testing.T) {
	compressor := NewLZ4Compressor()
	assert.Equal(t, CompressionLZ4, compressor.Type())

	tests := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"small", []byte("hello")},
		{"medium", bytes.Repeat([]byte("abc"), 100)},
		{"large", bytes.Repeat([]byte("xyz"), 1000)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 压缩
			compressed, err := compressor.Compress(tt.data)
			assert.NoError(t, err)

			// 解压
			decompressed, err := compressor.Decompress(compressed)
			assert.NoError(t, err)
			assert.Equal(t, tt.data, decompressed)

			// 验证压缩率
			if len(tt.data) > 0 {
				ratio := float64(len(compressed)) / float64(len(tt.data))
				t.Logf("Compression ratio: %.2f%%", ratio*100)
			}
		})
	}
}

// TestZSTDCompressor 测试 ZSTD 压缩
func TestZSTDCompressor(t *testing.T) {
	// 测试不同压缩级别
	levels := []int{1, 3, 5, 10}

	for _, level := range levels {
		t.Run(fmt.Sprintf("level-%d", level), func(t *testing.T) {
			compressor, err := NewZSTDCompressor(level)
			assert.NoError(t, err)
			assert.Equal(t, CompressionZSTD, compressor.Type())

			data := bytes.Repeat([]byte("abc"), 100)

			// 压缩
			compressed, err := compressor.Compress(data)
			assert.NoError(t, err)

			// 解压
			decompressed, err := compressor.Decompress(compressed)
			assert.NoError(t, err)
			assert.Equal(t, data, decompressed)

			// 验证压缩率
			ratio := float64(len(compressed)) / float64(len(data))
			t.Logf("Level %d compression ratio: %.2f%%", level, ratio*100)
		})
	}
}

// TestCompressedPage 测试压缩页面
func TestCompressedPage(t *testing.T) {
	data := bytes.Repeat([]byte("test data "), 100)

	compressionTypes := []CompressionType{
		CompressionNone,
		CompressionSnappy,
		CompressionLZ4,
	}

	for _, ctype := range compressionTypes {
		t.Run(string(ctype), func(t *testing.T) {
			// 创建压缩器
			compressor, err := NewCompressor(ctype, 3)
			assert.NoError(t, err)

			// 创建压缩页面
			cp, err := NewCompressedPage(ctype, data, compressor)
			assert.NoError(t, err)
			assert.Equal(t, uint32(len(data)), cp.OriginalSize)
			assert.Equal(t, ctype, cp.GetCompressionType())

			// 序列化
			serialized, err := cp.Serialize()
			assert.NoError(t, err)
			assert.Equal(t, 13+len(cp.Data), len(serialized)) // 13 = header size

			// 反序列化
			deserialized, err := DeserializeCompressedPage(serialized)
			assert.NoError(t, err)
			assert.Equal(t, cp.Magic, deserialized.Magic)
			assert.Equal(t, cp.Type, deserialized.Type)
			assert.Equal(t, cp.OriginalSize, deserialized.OriginalSize)
			assert.Equal(t, cp.CompressedSize, deserialized.CompressedSize)
			assert.Equal(t, cp.Data, deserialized.Data)

			// 解压
			decompressed, err := compressor.Decompress(deserialized.Data)
			assert.NoError(t, err)
			assert.Equal(t, data, decompressed)
		})
	}
}

// TestCompressedPage_ZSTD 测试 ZSTD 压缩页面
func TestCompressedPage_ZSTD(t *testing.T) {
	data := bytes.Repeat([]byte("test data "), 100)

	// 创建 ZSTD 压缩器
	compressor, err := NewZSTDCompressor(3)
	assert.NoError(t, err)

	// 创建压缩页面
	cp, err := NewCompressedPage(CompressionZSTD, data, compressor)
	assert.NoError(t, err)

	// 序列化
	serialized, err := cp.Serialize()
	assert.NoError(t, err)

	// 反序列化
	deserialized, err := DeserializeCompressedPage(serialized)
	assert.NoError(t, err)

	// 解压
	decompressed, err := compressor.Decompress(deserialized.Data)
	assert.NoError(t, err)
	assert.Equal(t, data, decompressed)
}

// TestNewCompressor 测试创建压缩器
func TestNewCompressor(t *testing.T) {
	tests := []struct {
		name           string
		compressionType CompressionType
		expectError    bool
	}{
		{"none", CompressionNone, false},
		{"snappy", CompressionSnappy, false},
		{"lz4", CompressionLZ4, false},
		{"zstd", CompressionZSTD, false},
		{"invalid", CompressionType("invalid"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compressor, err := NewCompressor(tt.compressionType, 3)
			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, compressor)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, compressor)
				assert.Equal(t, tt.compressionType, compressor.Type())
			}
		})
	}
}

// TestCompressedPage_InvalidMagic 测试无效魔数
func TestCompressedPage_InvalidMagic(t *testing.T) {
	data := []byte("invalid magic")

	_, err := DeserializeCompressedPage(data)
	assert.Error(t, err)
}

// TestCompressedPage_TooShort 测试数据过短
func TestCompressedPage_TooShort(t *testing.T) {
	data := []byte("short")

	_, err := DeserializeCompressedPage(data)
	assert.Error(t, err)
}

// BenchmarkSnappyCompress Snappy 压缩基准测试
func BenchmarkSnappyCompress(b *testing.B) {
	compressor := &SnappyCompressor{}
	data := bytes.Repeat([]byte("benchmark data"), 1000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = compressor.Compress(data)
	}
}

// BenchmarkSnappyDecompress Snappy 解压基准测试
func BenchmarkSnappyDecompress(b *testing.B) {
	compressor := &SnappyCompressor{}
	data := bytes.Repeat([]byte("benchmark data"), 1000)
	compressed, _ := compressor.Compress(data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = compressor.Decompress(compressed)
	}
}

// BenchmarkLZ4Compress LZ4 压缩基准测试
func BenchmarkLZ4Compress(b *testing.B) {
	compressor := NewLZ4Compressor()
	data := bytes.Repeat([]byte("benchmark data"), 1000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = compressor.Compress(data)
	}
}

// BenchmarkLZ4Decompress LZ4 解压基准测试
func BenchmarkLZ4Decompress(b *testing.B) {
	compressor := NewLZ4Compressor()
	data := bytes.Repeat([]byte("benchmark data"), 1000)
	compressed, _ := compressor.Compress(data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = compressor.Decompress(compressed)
	}
}

// BenchmarkZSTDCompress ZSTD 压缩基准测试
func BenchmarkZSTDCompress(b *testing.B) {
	compressor, _ := NewZSTDCompressor(3)
	data := bytes.Repeat([]byte("benchmark data"), 1000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = compressor.Compress(data)
	}
}

// BenchmarkZSTDDecompress ZSTD 解压基准测试
func BenchmarkZSTDDecompress(b *testing.B) {
	compressor, _ := NewZSTDCompressor(3)
	data := bytes.Repeat([]byte("benchmark data"), 1000)
	compressed, _ := compressor.Compress(data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = compressor.Decompress(compressed)
	}
}
