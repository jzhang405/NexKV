// Package hash 提供统一的哈希接口
//
// 设计目标：
//  1. 抽象哈希算法，方便后续替换和扩展
//  2. 支持多种哈希算法（SHA256、BLAKE3 等）
//  3. 提供类型安全的哈希计算
package hash

import (
	"lukechampine.com/blake3"
)

// ==================== Hasher 接口定义 ====================

// HashAlgorithm 哈希算法类型
type HashAlgorithm string

const (
	// HashAlgorithmSHA256 SHA256 算法
	HashAlgorithmSHA256 HashAlgorithm = "sha256"

	// HashAlgorithmBLAKE3 BLAKE3 算法（推荐，性能更优）
	HashAlgorithmBLAKE3 HashAlgorithm = "blake3"
)

// Hasher 哈希计算器接口
//
// 提供统一的哈希计算抽象，支持多种算法
type Hasher interface {
	// Algorithm 返回哈希算法名称
	Algorithm() HashAlgorithm

	// Size 返回哈希值大小（字节）
	Size() int

	// Sum256 计算 256 位哈希值（返回数组格式）
	Sum256(data []byte) [32]byte

	// Hash256 计算 256 位哈希值（返回切片格式）
	Hash256(data []byte) []byte
}

// ==================== BLAKE3 实现 ====================

// blake3Hasher BLAKE3 哈希实现
type blake3Hasher struct{}

// NewBLAKE3Hasher 创建 BLAKE3 哈希器
func NewBLAKE3Hasher() Hasher {
	return &blake3Hasher{}
}

// Algorithm 返回算法名称
func (h *blake3Hasher) Algorithm() HashAlgorithm {
	return HashAlgorithmBLAKE3
}

// Size 返回哈希值大小（BLAKE3 固定为 32 字节）
func (h *blake3Hasher) Size() int {
	return 32
}

// Sum256 计算 BLAKE3 哈希值（数组格式）
func (h *blake3Hasher) Sum256(data []byte) [32]byte {
	return blake3.Sum256(data)
}

// Hash256 计算 BLAKE3 哈希值（切片格式）
func (h *blake3Hasher) Hash256(data []byte) []byte {
	sum := blake3.Sum256(data)
	return sum[:]
}

// ==================== 全局默认实例 ====================

// DefaultHasher 默认哈希器
//
// 当前使用 BLAKE3（性能更优）
var DefaultHasher = NewBLAKE3Hasher()
