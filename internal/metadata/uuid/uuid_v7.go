// Package uuid 提供 UUID v7 生成实现（时间有序）
package uuid

import (
	cryptorand "crypto/rand"
	"fmt"
	"io"
	mathrand "math/rand"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// UUID v7 版本位常量
const (
	// version7Mask: 版本位掩码（清除高 4 位）
	version7Mask = 0x0F
	// version7Value: 版本 7 的值（0100）
	version7Value = 0x70
	// variantBitsMask: RFC 4122 变体位掩码（清除低 6 位）
	variantBitsMask = 0x3F
	// variantRFC4122Value: RFC 4122 变体值（10xx）
	variantRFC4122Value = 0x80
)

// 降级随机数生成器（用于 crypto/rand 失败时的备选方案）
// 注意：不提供加密安全性，但保证唯一性（适用于生成 ID 等场景）
var (
	fallbackRand     *mathrand.Rand
	fallbackRandOnce sync.Once
)

// initFallbackRand 初始化降级随机数生成器（懒加载）
func initFallbackRand() {
	fallbackRandOnce.Do(func() {
		// 使用当前时间作为种子，确保每次运行都有不同的随机序列
		// 注意：这在生产环境中不是加密安全的，但能提供基本的唯一性保证
		fallbackRand = mathrand.New(mathrand.NewSource(time.Now().UnixNano()))
	})
}

// readRandomWithFallback 读取随机数据，失败时降级到 math/rand
//
// 生产环境降级策略：
// 1. 优先使用 crypto/rand（加密安全）
// 2. 失败时记录警告并降级到 math/rand（保证唯一性，非加密安全）
// 3. 只有在极端情况下才返回错误
func readRandomWithFallback(buf []byte) error {
	// 尝试使用 crypto/rand
	if _, err := io.ReadFull(cryptorand.Reader, buf); err == nil {
		return nil
	}

	// crypto/rand 失败，使用降级方案
	logging.Warnf("crypto/rand 读取失败，降级到 math/rand（非加密安全）")
	initFallbackRand()
	fallbackRand.Read(buf) // math/rand.Read 永不返回错误

	return nil
}

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
//
// 容错机制: crypto/rand 失败时降级到 math/rand（保证服务可用性）
// 注意: 此函数永不返回空字符串（math/rand.Read 永不失败），返回空字符串仅作为防御性编程
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

	// 填充随机数据（使用带降级机制的读取）
	// 注意: readRandomWithFallback 永不返回错误（math/rand.Read 永不失败）
	// 错误分支仅作为防御性编程保留
	_ = readRandomWithFallback(uuid[6:16])

	// 设置版本位（version 7）
	uuid[6] = (uuid[6] & version7Mask) | version7Value

	// 设置变体位（RFC 4122）
	uuid[8] = (uuid[8] & variantBitsMask) | variantRFC4122Value

	return Format(uuid)
}

// GenerateUUIDv7WithError 生成 UUID v7（返回错误版本）
//
// 与 GenerateUUIDv7 功能相同，但返回错误以明确处理失败情况
// 实际上此函数永不返回错误（math/rand.Read 永不失败）
func GenerateUUIDv7WithError() (string, error) {
	uuid := make([]byte, 16)

	timestamp := time.Now().UnixMilli()

	uuid[0] = byte(timestamp >> 40)
	uuid[1] = byte(timestamp >> 32)
	uuid[2] = byte(timestamp >> 24)
	uuid[3] = byte(timestamp >> 16)
	uuid[4] = byte(timestamp >> 8)
	uuid[5] = byte(timestamp)

	if err := readRandomWithFallback(uuid[6:16]); err != nil {
		return "", types.NewOpErr(types.ErrCodeInternal, "GenerateUUIDv7", "生成失败", err)
	}

	uuid[6] = (uuid[6] & version7Mask) | version7Value
	uuid[8] = (uuid[8] & variantBitsMask) | variantRFC4122Value

	return Format(uuid), nil
}

// GenerateUUIDv7Bytes 生成 UUID v7 并返回字节形式
//
// 容错机制: crypto/rand 失败时降级到 math/rand（保证服务可用性）
// 注意: 此函数永不返回 nil（math/rand.Read 永不失败），返回 nil 仅作为防御性编程
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

	// 填充随机数据（使用带降级机制的读取）
	// 注意: readRandomWithFallback 永不返回错误（math/rand.Read 永不失败）
	_ = readRandomWithFallback(uuid[6:16])

	// 设置版本位（version 7）
	uuid[6] = (uuid[6] & version7Mask) | version7Value

	// 设置变体位（RFC 4122）
	uuid[8] = (uuid[8] & variantBitsMask) | variantRFC4122Value

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
//
// 验证逻辑:
//  1. 检查格式是否为有效的 UUID
//  2. 检查版本号是否为 7
//  3. 检查变体位是否符合 RFC 4122
//  4. 检查时间戳是否在合理范围内（现在前后 24 小时内，允许 1 分钟时钟漂移）
//
// 时间戳一致性: 在函数开始时捕获一次当前时间，避免连续验证时跨越毫秒边界导致的竞态
func IsValidUUIDv7(uuidStr string) bool {
	// 在函数开始时捕获当前时间戳，确保后续所有时间比较使用同一基准
	// 避免在连续验证多个 UUID 时因时间流逝导致不一致的验证结果
	now := time.Now().UnixMilli()

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

	// 提取时间戳（处理可能的错误）
	timestamp, err := ExtractTimestamp(uuidStr)
	if err != nil {
		return false
	}

	// 检查时间戳是否合理（不能是未来时间）
	// 允许前后 1 分钟的误差（处理时钟漂移）
	if timestamp > now+60*1000 {
		return false
	}

	// 检查时间戳是否在合理范围内（不能太旧）
	// 防止使用过期或伪造的 UUID
	if timestamp < now-24*60*60*1000 {
		return false
	}

	return true
}
