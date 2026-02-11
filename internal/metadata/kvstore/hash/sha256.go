// Package hash 提供 SHA256 哈希计算功能
//
// 使用 SIMD 加速的 SHA256 实现（如果可用），
// 否则回退到标准库实现。
//
// 性能对比（基于 minio/sha256-simd 基准测试）：
//   - AVX2/AVX512: ~400-600 MB/s per core
//   - 标准库:     ~200 MB/s per core
//   - 加速比:     2-3x
package hash

import (
	sha256simd "github.com/minio/sha256-simd"
)

// Sum256 计算数据的 SHA256 哈希值（返回数组格式）
//
// 与 crypto/sha256.Sum256 API 兼容
func Sum256(data []byte) [32]byte {
	return sha256simd.Sum256(data)
}

// Hash256 计算数据的 SHA256 哈希值（返回切片格式）
func Hash256(data []byte) []byte {
	sum := sha256simd.Sum256(data)
	return sum[:]
}
