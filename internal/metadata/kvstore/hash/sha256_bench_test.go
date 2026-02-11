package hash

import (
	"crypto/sha256"
	"testing"
)

// BenchmarkSHA256_SIMD 性能测试：SIMD SHA256
func BenchmarkSHA256_SIMD(b *testing.B) {
	data := make([]byte, 1024) // 1KB 数据
	for i := range data {
		data[i] = byte(i % 256)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Sum256(data)
	}
}

// BenchmarkSHA256_Std 性能测试：标准库 SHA256
func BenchmarkSHA256_Std(b *testing.B) {
	data := make([]byte, 1024) // 1KB 数据
	for i := range data {
		data[i] = byte(i % 256)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = sha256.Sum256(data)
	}
}

// BenchmarkSHA256_SIMD_Small 性能测试：SIMD SHA256 (小数据)
func BenchmarkSHA256_SIMD_Small(b *testing.B) {
	data := make([]byte, 64) // 64 字节数据
	for i := range data {
		data[i] = byte(i % 256)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Sum256(data)
	}
}

// BenchmarkSHA256_Std_Small 性能测试：标准库 SHA256 (小数据)
func BenchmarkSHA256_Std_Small(b *testing.B) {
	data := make([]byte, 64) // 64 字节数据
	for i := range data {
		data[i] = byte(i % 256)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = sha256.Sum256(data)
	}
}

// BenchmarkSHA256_SIMD_Large 性能测试：SIMD SHA256 (大数据)
func BenchmarkSHA256_SIMD_Large(b *testing.B) {
	data := make([]byte, 8192) // 8KB 数据
	for i := range data {
		data[i] = byte(i % 256)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Sum256(data)
	}
}

// BenchmarkSHA256_Std_Large 性能测试：标准库 SHA256 (大数据)
func BenchmarkSHA256_Std_Large(b *testing.B) {
	data := make([]byte, 8192) // 8KB 数据
	for i := range data {
		data[i] = byte(i % 256)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = sha256.Sum256(data)
	}
}
