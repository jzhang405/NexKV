// Package uuid 提供 UUID v4 生成实现（密码学安全随机）
package uuid

import (
	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
)

// GenerateUUIDv4 生成 UUID v4（随机 UUID）
//
// UUID v4 格式: xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx
//   - 4 表示版本 4（随机）
//   - y 是变体标识符（10xx 表示 RFC 4122）
//
// 安全性说明：
//   - 正常情况：使用 crypto/rand（密码学安全）
//   - 降级情况：crypto/rand 失败时降级到 math/rand（非密码学安全）
//
// ⚠️ 安全警告：
//
//	降级到 math/rand 后，生成的 UUID 不再具有密码学安全性。
//	如果您的应用场景要求强随机性（如生成 token、密钥等），
//	建议直接使用 crypto/rand 或专用加密库，而非此函数。
//
// 适用场景：
//   - 通用唯一标识符生成（非安全敏感）
//   - 分布式系统中的请求追踪 ID
//   - Quorum 提案 ID（内部使用，无安全要求）
//
// 容错机制: crypto/rand 失败时降级到 math/rand（保证服务可用性）
func GenerateUUIDv4() string {
	uuid := make([]byte, 16)

	// 使用带降级机制的随机数读取（与 UUID v7 共享实现）
	if err := readRandomWithFallback(uuid); err != nil {
		// 降级方案也失败，记录错误并返回空字符串
		// 这种情况理论上不应该发生（math/rand.Read 永不失败）
		logging.Errorf("UUID v4 生成失败（降级方案也失败）: %v", err)
		return ""
	}

	// 设置版本位（version 4）
	uuid[6] = (uuid[6] & 0x0F) | 0x40

	// 设置变体位（RFC 4122）
	uuid[8] = (uuid[8] & 0x3F) | 0x80

	return Format(uuid)
}

// GenerateUUIDv4Bytes 生成 UUID v4 并返回字节形式
//
// 容错机制: crypto/rand 失败时降级到 math/rand（保证服务可用性）
func GenerateUUIDv4Bytes() []byte {
	uuid := make([]byte, 16)

	// 使用带降级机制的随机数读取（与 UUID v7 共享实现）
	if err := readRandomWithFallback(uuid); err != nil {
		// 降级方案也失败，记录错误并返回 nil
		// 这种情况理论上不应该发生（math/rand.Read 永不失败）
		logging.Errorf("UUID v4 生成失败（降级方案也失败）: %v", err)
		return nil
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
