// Package uuid 提供多种 UUID 生成器实现
package uuid

import (
	"fmt"
	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// UUIDGenerator UUID 生成器接口
type UUIDGenerator interface {
	// Generate 生成通用 UUID
	Generate() string

	// GenerateTransactionID 生成事务 ID（使用 UUID v7，时间有序）
	GenerateTransactionID() string

	// GenerateNodeID 生成节点 ID（使用 Snowflake，短 ID）
	GenerateNodeID() int64
}

// UUID 长度常量
const (
	UUIDLength = 36 // "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx" 格式
)

// Parse 解析 UUID 字符串
func Parse(uuidStr string) ([]byte, error) {
	if len(uuidStr) != UUIDLength {
		return nil, types.NewUUIDFormatError(fmt.Sprintf("invalid UUID length: %d", len(uuidStr)), nil)
	}

	// 移除连字符
	hex := uuidStr[0:8] + uuidStr[9:13] + uuidStr[14:18] + uuidStr[19:23] + uuidStr[24:36]

	// 解析为字节
	uuid := make([]byte, 16)
	_, err := fmt.Sscanf(hex, "%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x",
		&uuid[0], &uuid[1], &uuid[2], &uuid[3],
		&uuid[4], &uuid[5], &uuid[6], &uuid[7],
		&uuid[8], &uuid[9], &uuid[10], &uuid[11],
		&uuid[12], &uuid[13], &uuid[14], &uuid[15])

	if err != nil {
		return nil, types.NewUUIDFormatError("invalid UUID format", err)
	}

	return uuid, nil
}

// Format 格式化 UUID 字节为字符串
func Format(uuid []byte) string {
	if len(uuid) != 16 {
		return ""
	}

	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16])
}

// Variant UUID 变体
const (
	VariantNCS       = 0 // NCS 向后兼容
	VariantRFC4122   = 2 // RFC 4122 标准
	VariantMicrosoft = 6 // Microsoft 向后兼容
	VariantFuture    = 7 // 未来保留
)

// GetVariant 获取 UUID 变体
func GetVariant(uuid []byte) byte {
	if len(uuid) != 16 {
		return 0xFF
	}

	v := uuid[8] & 0xC0 // 取高 2 位

	switch v {
	case 0x80:
		return VariantRFC4122
	case 0xC0:
		return VariantMicrosoft
	case 0xE0:
		return VariantFuture
	default:
		return VariantNCS
	}
}

// Version UUID 版本
const (
	VersionTimeBased     = 1 // v1: 基于时间
	VersionDCESecurity   = 2 // v2: DCE 安全
	VersionMD5NameBased  = 3 // v3: 基于名称和 MD5
	VersionRandom        = 4 // v4: 随机
	VersionSHA1NameBased = 5 // v5: 基于名称和 SHA1
	Version7TimeBased    = 7 // v7: 基于时间（RFC 9562）
)

// GetVersion 获取 UUID 版本
func GetVersion(uuid []byte) byte {
	if len(uuid) != 16 {
		return 0
	}

	return (uuid[6] & 0xF0) >> 4
}

// IsNil 判断是否为 nil UUID
func IsNil(uuid []byte) bool {
	if len(uuid) != 16 {
		return false
	}

	for _, b := range uuid {
		if b != 0 {
			return false
		}
	}

	return true
}
