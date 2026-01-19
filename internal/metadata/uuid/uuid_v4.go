// Package uuid 提供 UUID v4 生成实现（密码学安全随机）
package uuid

import (
	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
)

// GenerateUUIDv4 生成 UUID v4（随机 UUID）
// 格式: xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx
// 安全性: 使用 crypto/rand，失败时降级到 math/rand（保证服务可用性）
// 警告: 降级后不具备密码学安全性，不适合安全敏感场景
func GenerateUUIDv4() string {
	uuid := make([]byte, 16)

	if err := readRandomWithFallback(uuid); err != nil {
		logging.Errorf("UUID v4 生成失败（降级方案也失败）: %v", err)
		return ""
	}

	uuid[6] = (uuid[6] & 0x0F) | 0x40 // version 4
	uuid[8] = (uuid[8] & 0x3F) | 0x80 // variant RFC 4122

	return Format(uuid)
}

// GenerateUUIDv4Bytes 生成 UUID v4 并返回字节形式
func GenerateUUIDv4Bytes() []byte {
	uuid := make([]byte, 16)

	if err := readRandomWithFallback(uuid); err != nil {
		logging.Errorf("UUID v4 生成失败（降级方案也失败）: %v", err)
		return nil
	}

	uuid[6] = (uuid[6] & 0x0F) | 0x40
	uuid[8] = (uuid[8] & 0x3F) | 0x80

	return uuid
}

// IsValidUUIDv4 验证是否为有效的 UUID v4
func IsValidUUIDv4(uuidStr string) bool {
	uuid, err := Parse(uuidStr)
	if err != nil {
		return false
	}

	if GetVersion(uuid) != VersionRandom {
		return false
	}

	if GetVariant(uuid) != VariantRFC4122 {
		return false
	}

	return true
}
