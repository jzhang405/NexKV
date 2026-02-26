// Package compressor 提供压缩算法测试
package compressor

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==========================================
// 工厂函数测试
// ==========================================

func TestNew_AllTypes(t *testing.T) {
	tests := []struct {
		name         string
		compressType CompressorType
		expectedType CompressorType
	}{
		{"snappy", Snappy, Snappy},
		{"lz4", LZ4, LZ4},
		{"zstd", ZSTD, ZSTD},
		{"none", None, None},
		{"unknown defaults to snappy", CompressorType("unknown"), Snappy},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := New(tt.compressType)
			assert.Equal(t, tt.expectedType, c.Type())
		})
	}
}

// ==========================================
// Snappy 压缩器测试
// ==========================================

func TestSnappyCompressor_Type(t *testing.T) {
	c := newSnappyCompressor()
	assert.Equal(t, Snappy, c.Type())
}

func TestSnappyCompressor_CompressDecompress(t *testing.T) {
	c := newSnappyCompressor()

	tests := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"single byte", []byte{0x00}},
		{"small data", []byte("hello world")},
		{"large data", bytes.Repeat([]byte("test"), 1000)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compressed, err := c.Compress(tt.data)
			require.NoError(t, err)

			decompressed, err := c.Decompress(compressed)
			require.NoError(t, err)

			assert.Equal(t, tt.data, decompressed)
		})
	}
}

func TestSnappyCompressor_DecompressWithLimit(t *testing.T) {
	c := newSnappyCompressor()

	// 正常数据
	data := []byte("hello world")
	compressed, err := c.Compress(data)
	require.NoError(t, err)

	// 足够大的限制
	decompressed, err := c.DecompressWithLimit(compressed, 1000)
	require.NoError(t, err)
	assert.Equal(t, data, decompressed)

	// 太小的限制
	_, err = c.DecompressWithLimit(compressed, 5)
	assert.Error(t, err) // 应该返回 ErrDecompressionTooBig
}

func TestSnappyCompressor_DecompressInvalid(t *testing.T) {
	c := newSnappyCompressor()

	// 无效数据
	_, err := c.Decompress([]byte{0xff, 0xfe, 0xfd})
	assert.Error(t, err)
}

// ==========================================
// LZ4 压缩器测试
// ==========================================

func TestLZ4Compressor_Type(t *testing.T) {
	c := newLZ4Compressor()
	assert.Equal(t, LZ4, c.Type())
}

func TestLZ4Compressor_CompressDecompress(t *testing.T) {
	c := newLZ4Compressor()

	tests := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"single byte", []byte{0x00}},
		{"small data", []byte("hello world")},
		{"large data", bytes.Repeat([]byte("test"), 1000)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compressed, err := c.Compress(tt.data)
			require.NoError(t, err)

			decompressed, err := c.Decompress(compressed)
			require.NoError(t, err)

			assert.Equal(t, tt.data, decompressed)
		})
	}
}

func TestLZ4Compressor_DecompressWithLimit(t *testing.T) {
	c := newLZ4Compressor()

	data := []byte("hello world")
	compressed, err := c.Compress(data)
	require.NoError(t, err)

	// 足够大的限制
	decompressed, err := c.DecompressWithLimit(compressed, 1000)
	require.NoError(t, err)
	assert.Equal(t, data, decompressed)
}

func TestLZ4Compressor_DecompressInvalid(t *testing.T) {
	c := newLZ4Compressor()

	// 无效数据
	_, err := c.Decompress([]byte{0xff, 0xfe, 0xfd})
	assert.Error(t, err)
}

// ==========================================
// ZSTD 压缩器测试
// ==========================================

func TestZSTDCompressor_Type(t *testing.T) {
	c := newZSTDCompressor()
	assert.Equal(t, ZSTD, c.Type())
}

func TestZSTDCompressor_CompressDecompress(t *testing.T) {
	c := newZSTDCompressor()

	tests := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"single byte", []byte{0x00}},
		{"small data", []byte("hello world")},
		{"large data", bytes.Repeat([]byte("test"), 1000)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compressed, err := c.Compress(tt.data)
			require.NoError(t, err)

			decompressed, err := c.Decompress(compressed)
			require.NoError(t, err)

			assert.Equal(t, tt.data, decompressed)
		})
	}
}

func TestZSTDCompressor_DecompressWithLimit(t *testing.T) {
	c := newZSTDCompressor()

	data := []byte("hello world")
	compressed, err := c.Compress(data)
	require.NoError(t, err)

	// 足够大的限制
	decompressed, err := c.DecompressWithLimit(compressed, 1000)
	require.NoError(t, err)
	assert.Equal(t, data, decompressed)
}

func TestZSTDCompressor_DecompressInvalid(t *testing.T) {
	c := newZSTDCompressor()

	// 无效数据
	_, err := c.Decompress([]byte{0xff, 0xfe, 0xfd})
	assert.Error(t, err)
}

// ==========================================
// None 压缩器测试
// ==========================================

func TestNoneCompressor_Type(t *testing.T) {
	c := newNoneCompressor()
	assert.Equal(t, None, c.Type())
}

func TestNoneCompressor_CompressDecompress(t *testing.T) {
	c := newNoneCompressor()

	tests := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"single byte", []byte{0x00}},
		{"small data", []byte("hello world")},
		{"large data", bytes.Repeat([]byte("test"), 1000)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compressed, err := c.Compress(tt.data)
			require.NoError(t, err)

			// None 不压缩，应该返回原数据
			assert.Equal(t, tt.data, compressed)

			decompressed, err := c.Decompress(compressed)
			require.NoError(t, err)

			assert.Equal(t, tt.data, decompressed)
		})
	}
}

func TestNoneCompressor_DecompressWithLimit(t *testing.T) {
	c := newNoneCompressor()

	data := []byte("hello world")

	// 足够大的限制
	decompressed, err := c.DecompressWithLimit(data, 1000)
	require.NoError(t, err)
	assert.Equal(t, data, decompressed)

	// 太小的限制
	_, err = c.DecompressWithLimit(data, 5)
	assert.Error(t, err) // 应该返回 ErrDecompressionTooBig
}

// ==========================================
// 跨压缩器测试
// ==========================================

func TestAllCompressors_RoundTrip(t *testing.T) {
	compressors := []struct {
		name string
		c    Compressor
	}{
		{"snappy", New(Snappy)},
		{"lz4", New(LZ4)},
		{"zstd", New(ZSTD)},
		{"none", New(None)},
	}

	data := []byte("The quick brown fox jumps over the lazy dog. 1234567890")

	for _, tc := range compressors {
		t.Run(tc.name, func(t *testing.T) {
			compressed, err := tc.c.Compress(data)
			require.NoError(t, err)

			decompressed, err := tc.c.Decompress(compressed)
			require.NoError(t, err)

			assert.Equal(t, data, decompressed)
		})
	}
}

func TestCompressors_CompressionRatio(t *testing.T) {
	// 测试压缩比（可重复数据应该有明显的压缩效果）
	data := bytes.Repeat([]byte("abcdefghij"), 1000) // 10KB 重复数据

	compressors := []struct {
		name string
		c    Compressor
	}{
		{"snappy", New(Snappy)},
		{"lz4", New(LZ4)},
		{"zstd", New(ZSTD)},
	}

	for _, tc := range compressors {
		t.Run(tc.name, func(t *testing.T) {
			compressed, err := tc.c.Compress(data)
			require.NoError(t, err)

			// 压缩后应该明显小于原数据
			assert.Less(t, len(compressed), len(data)/2, "should compress to less than half")

			t.Logf("%s: %d -> %d bytes (%.1f%%)", tc.name, len(data), len(compressed), float64(len(compressed))/float64(len(data))*100)
		})
	}
}

// ==========================================
// 边界条件测试
// ==========================================

func TestCompressors_EmptyData(t *testing.T) {
	compressors := []struct {
		name string
		c    Compressor
	}{
		{"snappy", New(Snappy)},
		{"lz4", New(LZ4)},
		{"zstd", New(ZSTD)},
		{"none", New(None)},
	}

	for _, tc := range compressors {
		t.Run(tc.name, func(t *testing.T) {
			compressed, err := tc.c.Compress([]byte{})
			require.NoError(t, err)
			assert.Empty(t, compressed)

			decompressed, err := tc.c.Decompress([]byte{})
			require.NoError(t, err)
			assert.Empty(t, decompressed)
		})
	}
}
