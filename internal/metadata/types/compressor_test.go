// Package types 压缩器单元测试
package types

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// TestNoneCompressor 测试无压缩器
func TestNoneCompressor(t *testing.T) {
	compressor := NewNoneCompressor()

	// 测试压缩
	data := []byte("hello world")
	compressed, err := compressor.Compress(data)
	if err != nil {
		t.Fatalf("压缩失败: %v", err)
	}

	// 无压缩应该返回相同的数据
	if !bytes.Equal(compressed, data) {
		t.Errorf("无压缩器应该返回相同的数据: 期望 %v, 实际 %v", data, compressed)
	}

	// 测试解压
	decompressed, err := compressor.Decompress(compressed)
	if err != nil {
		t.Fatalf("解压失败: %v", err)
	}

	if !bytes.Equal(decompressed, data) {
		t.Errorf("解压后数据不匹配: 期望 %v, 实际 %v", data, decompressed)
	}

	// 测试 Type 和 Name
	if compressor.Type() != CompressionTypeNone {
		t.Errorf("类型不匹配: 期望 %d, 实际 %d", CompressionTypeNone, compressor.Type())
	}

	if compressor.Name() != "none" {
		t.Errorf("名称不匹配: 期望 %s, 实际 %s", "none", compressor.Name())
	}
}

// TestSnappyCompressor 测试 Snappy 压缩器
func TestSnappyCompressor(t *testing.T) {
	compressor, err := NewSnappyCompressor()
	if err != nil {
		t.Fatalf("创建 Snappy 压缩器失败: %v", err)
	}

	testCompressor(t, compressor, CompressionTypeSnappy, "snappy")
}

// TestZSTDCompressor 测试 ZSTD 压缩器
func TestZSTDCompressor(t *testing.T) {
	compressor, err := NewZSTDCompressor()
	if err != nil {
		t.Fatalf("创建 ZSTD 压缩器失败: %v", err)
	}

	testCompressor(t, compressor, CompressionTypeZSTD, "zstd")
}

// TestZSTDCompressorWithLevel 测试不同级别的 ZSTD 压缩器
func TestZSTDCompressorWithLevel(t *testing.T) {
	levels := []int{1, 3, 9} // 最小、默认、最大

	for _, level := range levels {
		t.Run(fmt.Sprintf("Level%d", level), func(t *testing.T) {
			compressor, err := NewZSTDCompressorWithLevel(level)
			if err != nil {
				t.Fatalf("创建 ZSTD 压缩器失败 (级别 %d): %v", level, err)
			}

			// 测试压缩和解压
			data := []byte("test data for compression level " + string(rune('0'+level)))
			compressed, err := compressor.Compress(data)
			if err != nil {
				t.Fatalf("压缩失败: %v", err)
			}

			decompressed, err := compressor.Decompress(compressed)
			if err != nil {
				t.Fatalf("解压失败: %v", err)
			}

			if !bytes.Equal(decompressed, data) {
				t.Errorf("解压后数据不匹配")
			}
		})
	}
}

// TestLZ4Compressor 测试 LZ4 压缩器
func TestLZ4Compressor(t *testing.T) {
	compressor, err := NewLZ4Compressor()
	if err != nil {
		t.Fatalf("创建 LZ4 压缩器失败: %v", err)
	}

	testCompressor(t, compressor, CompressionTypeLZ4, "lz4 (hc)")
}

// TestLZ4FastCompressor 测试 LZ4 快速压缩器
func TestLZ4FastCompressor(t *testing.T) {
	compressor, err := NewLZ4FastCompressor()
	if err != nil {
		t.Fatalf("创建 LZ4 快速压缩器失败: %v", err)
	}

	testCompressor(t, compressor, CompressionTypeLZ4, "lz4 (fast)")
}

// testCompressor 通用压缩器测试函数
func testCompressor(t *testing.T, compressor Compressor, expectedType CompressionType, expectedNamePrefix string) {
	t.Helper()

	// 测试数据
	testCases := []struct {
		name string
		data []byte
	}{
		{"空数据", []byte{}},
		{"短数据", []byte("hello")},
		{"中等数据", []byte("hello world, this is a test data for compression")},
		{"长数据", bytes.Repeat([]byte("a"), 1000)},
		{"重复数据", bytes.Repeat([]byte("abc"), 100)},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// 测试压缩
			compressed, err := compressor.Compress(tc.data)
			if err != nil {
				t.Fatalf("压缩失败: %v", err)
			}

			// 测试解压
			decompressed, err := compressor.Decompress(compressed)
			if err != nil {
				t.Fatalf("解压失败: %v", err)
			}

			// 验证数据一致性
			if !bytes.Equal(decompressed, tc.data) {
				t.Errorf("解压后数据不匹配:\n期望: %v\n实际: %v", tc.data, decompressed)
			}

			// 验证压缩效果（空数据和短数据可能无法压缩）
			if len(tc.data) > 10 && len(compressed) >= len(tc.data) {
				t.Logf("警告: 压缩后大小没有减少 (原始=%d, 压缩=%d)", len(tc.data), len(compressed))
			}
		})
	}

	// 测试 Type 和 Name
	if compressor.Type() != expectedType {
		t.Errorf("类型不匹配: 期望 %d, 实际 %d", expectedType, compressor.Type())
	}

	name := compressor.Name()
	// P2-1 修复：使用 strings.HasPrefix() 正确检查前缀
	// 原逻辑：name[:len(expectedNamePrefix)] 对于 "zstd" 会取 name[:4]
	//       但 name 可能是 "zst" 开头（如 "zstd (level=3)"），导致切片越界或比较错误
	if !strings.HasPrefix(name, expectedNamePrefix) {
		t.Errorf("名称不匹配: 期望前缀 %s, 实际 %s", expectedNamePrefix, name)
	}
}

// TestNewCompressor 测试压缩器工厂函数
func TestNewCompressor(t *testing.T) {
	testCases := []struct {
		name         string
		compression  CompressionType
		expectError  bool
		expectedName string
	}{
		{"None", CompressionTypeNone, false, "none"},
		{"Snappy", CompressionTypeSnappy, false, "snappy"},
		{"ZSTD", CompressionTypeZSTD, false, "zstd"},
		{"LZ4", CompressionTypeLZ4, false, "lz4"},
		{"Invalid", CompressionType(999), true, ""},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			compressor, err := NewCompressor(tc.compression)

			if tc.expectError {
				if err == nil {
					t.Errorf("期望返回错误，但没有")
				}
				return
			}

			if err != nil {
				t.Fatalf("创建压缩器失败: %v", err)
			}

			if compressor.Type() != tc.compression {
				t.Errorf("类型不匹配: 期望 %d, 实际 %d", tc.compression, compressor.Type())
			}

			// 简单测试压缩和解压
			data := []byte("test data")
			compressed, err := compressor.Compress(data)
			if err != nil {
				t.Fatalf("压缩失败: %v", err)
			}

			decompressed, err := compressor.Decompress(compressed)
			if err != nil {
				t.Fatalf("解压失败: %v", err)
			}

			if !bytes.Equal(decompressed, data) {
				t.Errorf("解压后数据不匹配")
			}
		})
	}
}

// TestCompressionTypeValidate 测试 CompressionType 验证
func TestCompressionTypeValidate(t *testing.T) {
	testCases := []struct {
		name        string
		compression CompressionType
		expectError bool
	}{
		{"None", CompressionTypeNone, false},
		{"Snappy", CompressionTypeSnappy, false},
		{"ZSTD", CompressionTypeZSTD, false},
		{"LZ4", CompressionTypeLZ4, false},
		{"Invalid", CompressionType(999), true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.compression.Validate()
			if tc.expectError && err == nil {
				t.Errorf("期望返回错误，但没有")
			}
			if !tc.expectError && err != nil {
				t.Errorf("不期望返回错误: %v", err)
			}
		})
	}
}

// TestCompressionTypeString 测试 CompressionType 字符串表示
func TestCompressionTypeString(t *testing.T) {
	testCases := []struct {
		compression CompressionType
		expected    string
	}{
		{CompressionTypeNone, "none"},
		{CompressionTypeSnappy, "snappy"},
		{CompressionTypeZSTD, "zstd"},
		{CompressionTypeLZ4, "lz4"},
		{CompressionType(999), "unknown"},
	}

	for _, tc := range testCases {
		t.Run(tc.expected, func(t *testing.T) {
			if tc.compression.String() != tc.expected {
				t.Errorf("字符串不匹配: 期望 %s, 实际 %s", tc.expected, tc.compression.String())
			}
		})
	}
}

// TestCompressionTypeDefaultCompressionLevel 测试默认压缩级别
func TestCompressionTypeDefaultCompressionLevel(t *testing.T) {
	testCases := []struct {
		compression CompressionType
		expected    int
	}{
		{CompressionTypeNone, 0},
		{CompressionTypeSnappy, 0}, // Snappy 不支持压缩级别
		{CompressionTypeZSTD, 3},   // ZSTD 默认级别
		{CompressionTypeLZ4, 0},    // LZ4 默认级别
	}

	for _, tc := range testCases {
		t.Run(tc.compression.String(), func(t *testing.T) {
			if level := tc.compression.DefaultCompressionLevel(); level != tc.expected {
				t.Errorf("默认级别不匹配: 期望 %d, 实际 %d", tc.expected, level)
			}
		})
	}
}

// BenchmarkNoneCompressor 无压缩器基准测试
func BenchmarkNoneCompressor(b *testing.B) {
	compressor := NewNoneCompressor()
	data := bytes.Repeat([]byte("hello world "), 100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = compressor.Compress(data)
	}
}

// BenchmarkSnappyCompressor Snappy 压缩器基准测试
func BenchmarkSnappyCompressor(b *testing.B) {
	compressor, _ := NewSnappyCompressor()
	data := bytes.Repeat([]byte("hello world "), 100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = compressor.Compress(data)
	}
}

// BenchmarkZSTDCompressor ZSTD 压缩器基准测试
func BenchmarkZSTDCompressor(b *testing.B) {
	compressor, _ := NewZSTDCompressor()
	data := bytes.Repeat([]byte("hello world "), 100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = compressor.Compress(data)
	}
}

// BenchmarkLZ4Compressor LZ4 压缩器基准测试
func BenchmarkLZ4Compressor(b *testing.B) {
	compressor, _ := NewLZ4Compressor()
	data := bytes.Repeat([]byte("hello world "), 100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = compressor.Compress(data)
	}
}
