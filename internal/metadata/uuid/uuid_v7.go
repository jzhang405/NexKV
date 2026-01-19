// Package uuid 提供 UUID v7 生成实现（时间有序）
package uuid

import (
	"crypto/rand"
	"fmt"
	"github.com/jzhang405/NexKV/internal/metadata/types"
	"io"
	"time"
)

// GenerateUUIDv7 生成 UUID v7（时间有序）
//
// UUID v7 格式 (RFC 9562): xxxxxxxx-xxxx-7xxx-yxxx-xxxxxxxxxxxx
//   - 48-bit: Unix 毫秒时间戳（从 1970-01-01 UTC）
//   - 74-bit: 随机数据
//   - 版本位: 7
//   - 变体位: 10xx (RFC 4122)
//
// 优势:
//   - 时间有序，适合数据库索引（B+ Tree 友好）
//   - 随机部分保证唯一性
//   - 适合分布式系统（无需中心化 ID 生成）
//
// 适用场景: 2PC 事务 ID、数据库主键、需要按时间排序的场景
func GenerateUUIDv7() string {
	uuid := make([]byte, 16)

	// 获取当前时间戳（毫秒）
	timestamp := time.Now().UnixMilli()

	// 前 6 字节：48-bit 时间戳（大端序）
	uuid[0] = byte(timestamp >> 40)
	uuid[1] = byte(timestamp >> 32)
	uuid[2] = byte(timestamp >> 24)
	uuid[3] = byte(timestamp >> 16)
	uuid[4] = byte(timestamp >> 8)
	uuid[5] = byte(timestamp)

	// 第 7 字节：版本位（version 7）+ 高 4 位随机
	// 先填充随机数据
	if _, err := io.ReadFull(rand.Reader, uuid[6:16]); err != nil {
		panic(fmt.Sprintf("生成随机数据失败: %v", err))
	}

	// 设置版本位（version 7）
	uuid[6] = (uuid[6] & 0x0F) | 0x70

	// 设置变体位（RFC 4122）
	uuid[8] = (uuid[8] & 0x3F) | 0x80

	return Format(uuid)
}

// GenerateUUIDv7Bytes 生成 UUID v7 并返回字节形式
func GenerateUUIDv7Bytes() []byte {
	uuid := make([]byte, 16)

	// 获取当前时间戳（毫秒）
	timestamp := time.Now().UnixMilli()

	// 前 6 字节：48-bit 时间戳（大端序）
	uuid[0] = byte(timestamp >> 40)
	uuid[1] = byte(timestamp >> 32)
	uuid[2] = byte(timestamp >> 24)
	uuid[3] = byte(timestamp >> 16)
	uuid[4] = byte(timestamp >> 8)
	uuid[5] = byte(timestamp)

	// 第 7 字节：版本位（version 7）+ 高 4 位随机
	if _, err := io.ReadFull(rand.Reader, uuid[6:16]); err != nil {
		panic(fmt.Sprintf("生成随机数据失败: %v", err))
	}

	// 设置版本位（version 7）
	uuid[6] = (uuid[6] & 0x0F) | 0x70

	// 设置变体位（RFC 4122）
	uuid[8] = (uuid[8] & 0x3F) | 0x80

	return uuid
}

// ExtractTimestamp 从 UUID v7 提取时间戳
func ExtractTimestamp(uuidStr string) (int64, error) {
	uuid, err := Parse(uuidStr)
	if err != nil {
		return 0, err
	}

	// 检查版本
	if GetVersion(uuid) != Version7TimeBased {
		return 0, types.NewUUIDFormatError(fmt.Sprintf("不是 UUID v7: version=%d", GetVersion(uuid)), nil)
	}

	// 提取前 6 字节作为时间戳
	timestamp := int64(uuid[0])<<40 |
		int64(uuid[1])<<32 |
		int64(uuid[2])<<24 |
		int64(uuid[3])<<16 |
		int64(uuid[4])<<8 |
		int64(uuid[5])

	return timestamp, nil
}

// IsValidUUIDv7 验证是否为有效的 UUID v7
func IsValidUUIDv7(uuidStr string) bool {
	uuid, err := Parse(uuidStr)
	if err != nil {
		return false
	}

	// 检查版本
	if GetVersion(uuid) != Version7TimeBased {
		return false
	}

	// 检查变体
	if GetVariant(uuid) != VariantRFC4122 {
		return false
	}

	// 检查时间戳是否合理（不能是未来时间）
	timestamp, _ := ExtractTimestamp(uuidStr)
	now := time.Now().UnixMilli()

	// 允许前后 1 天的误差（处理时钟漂移）
	if timestamp > now+24*3600*1000 {
		return false
	}

	return true
}
