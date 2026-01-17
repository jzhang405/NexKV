// Package uuid 提供 UUID v4 生成实现（密码学安全随机）
package uuid

import (
	"crypto/rand"
	"fmt"
	"io"
)

// GenerateUUIDv4 生成 UUID v4（随机 UUID）
//
// UUID v4 格式: xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx
//   - 4 表示版本 4（随机）
//   - y 是变体标识符（10xx 表示 RFC 4122）
//
// 使用 crypto/rand 确保密码学安全性
// 适用于: 通用场景、需要不可预测性的场景（如 Quorum 提案 ID）
func GenerateUUIDv4() string {
	uuid := make([]byte, 16)

	// 使用密码学安全的随机数生成器
	if _, err := io.ReadFull(rand.Reader, uuid); err != nil {
		// 极低概率发生，如果发生则 panic
		panic(fmt.Sprintf("生成 UUID v4 失败: %v", err))
	}

	// 设置版本位（version 4）
	uuid[6] = (uuid[6] & 0x0F) | 0x40

	// 设置变体位（RFC 4122）
	uuid[8] = (uuid[8] & 0x3F) | 0x80

	return Format(uuid)
}

// GenerateUUIDv4Bytes 生成 UUID v4 并返回字节形式
func GenerateUUIDv4Bytes() []byte {
	uuid := make([]byte, 16)

	if _, err := io.ReadFull(rand.Reader, uuid); err != nil {
		panic(fmt.Sprintf("生成 UUID v4 失败: %v", err))
	}

	// 设置版本位（version 4）
	uuid[6] = (uuid[6] & 0x0F) | 0x40

	// 设置变体位（RFC 4122）
	uuid[8] = (uuid[8] & 0x3F) | 0x80

	return uuid
}

// IsValidUUIDv4 验证是否为有效的 UUID v4
func IsValidUUIDv4(uuidStr string) bool {
	uuid, err := Parse(uuidStr)
	if err != nil {
		return false
	}

	// 检查版本
	if GetVersion(uuid) != VersionRandom {
		return false
	}

	// 检查变体
	if GetVariant(uuid) != VariantRFC4122 {
		return false
	}

	return true
}
