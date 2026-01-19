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
	version7Mask        = 0x0F
	version7Value       = 0x70
	variantBitsMask     = 0x3F
	variantRFC4122Value = 0x80
)

var (
	fallbackRand     *mathrand.Rand
	fallbackRandOnce sync.Once
)

// initFallbackRand 初始化降级随机数生成器（懒加载）
func initFallbackRand() {
	fallbackRandOnce.Do(func() {
		fallbackRand = mathrand.New(mathrand.NewSource(time.Now().UnixNano()))
	})
}

// readRandomWithFallback 读取随机数据，失败时降级到 math/rand
func readRandomWithFallback(buf []byte) error {
	if _, err := io.ReadFull(cryptorand.Reader, buf); err == nil {
		return nil
	}

	logging.Warnf("crypto/rand 读取失败，降级到 math/rand（非加密安全）")
	initFallbackRand()
	fallbackRand.Read(buf)

	return nil
}

// GenerateUUIDv7 生成 UUID v7（时间有序）
// 格式: xxxxxxxx-xxxx-7xxx-yxxx-xxxxxxxxxxxx
// 特性: 时间有序、B+ Tree 友好、适合分布式系统
func GenerateUUIDv7() string {
	uuid := make([]byte, 16)
	timestamp := time.Now().UnixMilli()

	uuid[0] = byte(timestamp >> 40)
	uuid[1] = byte(timestamp >> 32)
	uuid[2] = byte(timestamp >> 24)
	uuid[3] = byte(timestamp >> 16)
	uuid[4] = byte(timestamp >> 8)
	uuid[5] = byte(timestamp)

	_ = readRandomWithFallback(uuid[6:16])

	uuid[6] = (uuid[6] & version7Mask) | version7Value
	uuid[8] = (uuid[8] & variantBitsMask) | variantRFC4122Value

	return Format(uuid)
}

// GenerateUUIDv7WithError 生成 UUID v7（返回错误版本）
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
func GenerateUUIDv7Bytes() []byte {
	uuid := make([]byte, 16)
	timestamp := time.Now().UnixMilli()

	uuid[0] = byte(timestamp >> 40)
	uuid[1] = byte(timestamp >> 32)
	uuid[2] = byte(timestamp >> 24)
	uuid[3] = byte(timestamp >> 16)
	uuid[4] = byte(timestamp >> 8)
	uuid[5] = byte(timestamp)

	_ = readRandomWithFallback(uuid[6:16])

	uuid[6] = (uuid[6] & version7Mask) | version7Value
	uuid[8] = (uuid[8] & variantBitsMask) | variantRFC4122Value

	return uuid
}

// ExtractTimestamp 从 UUID v7 提取时间戳
func ExtractTimestamp(uuidStr string) (int64, error) {
	uuid, err := Parse(uuidStr)
	if err != nil {
		return 0, err
	}

	if GetVersion(uuid) != Version7TimeBased {
		return 0, types.NewUUIDFormatError(fmt.Sprintf("不是 UUID v7: version=%d", GetVersion(uuid)), nil)
	}

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
	now := time.Now().UnixMilli()

	uuid, err := Parse(uuidStr)
	if err != nil {
		return false
	}

	if GetVersion(uuid) != Version7TimeBased {
		return false
	}

	if GetVariant(uuid) != VariantRFC4122 {
		return false
	}

	timestamp, err := ExtractTimestamp(uuidStr)
	if err != nil {
		return false
	}

	if timestamp > now+60*1000 {
		return false
	}

	if timestamp < now-24*60*60*1000 {
		return false
	}

	return true
}
