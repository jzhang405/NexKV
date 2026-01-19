// Package uuid 提供多种 UUID 生成器实现
package uuid

import (
	"bytes"
	"encoding/hex"
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
// 使用 encoding/hex 提升性能
// 验证连字符位置，确保符合 xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx 格式
func Parse(uuidStr string) ([]byte, error) {
	if len(uuidStr) != UUIDLength {
		return nil, types.NewUUIDFormatError(fmt.Sprintf("invalid UUID length: %d", len(uuidStr)), nil)
	}

	// 验证连字符位置
	if uuidStr[8] != '-' || uuidStr[13] != '-' || uuidStr[18] != '-' || uuidStr[23] != '-' {
		return nil, types.NewUUIDFormatError("invalid UUID format: hyphen positions are incorrect", nil)
	}

	// 移除连字符
	hexStr := uuidStr[0:8] + uuidStr[9:13] + uuidStr[14:18] + uuidStr[19:23] + uuidStr[24:36]

	// 使用 hex.DecodeString 解析（比 fmt.Sscanf 更快）
	uuid, err := hex.DecodeString(hexStr)
	if err != nil {
		return nil, types.NewUUIDFormatError("invalid UUID format", err)
	}
	if len(uuid) != 16 {
		return nil, types.NewUUIDFormatError("invalid UUID hex length", nil)
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

	// 变体位掩码（用于从字节中提取变体）
	variantMask = 0xC0 // 取高 2 位
)

// GetVariant 获取 UUID 变体
func GetVariant(uuid []byte) byte {
	if len(uuid) != 16 {
		return 0xFF
	}

	v := uuid[8] & variantMask // 取高 2 位

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

	// 版本位掩码（用于从字节中提取版本）
	versionMask = 0xF0 // 取高 4 位
)

// GetVersion 获取 UUID 版本
func GetVersion(uuid []byte) byte {
	if len(uuid) != 16 {
		return 0
	}

	return (uuid[6] & versionMask) >> 4
}

// IsNil 判断是否为 nil UUID
// 使用 bytes.Equal 提升性能
func IsNil(uuid []byte) bool {
	if len(uuid) != 16 {
		return false
	}

	var nilUUID [16]byte
	return bytes.Equal(uuid, nilUUID[:])
}
