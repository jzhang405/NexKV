package bftree

import (
	"math/bits"
)

// SetBit 设置字节数组中指定位的值
//
// Parameters:
//   - data: 字节数组
//   - offset: 位偏移量（从 0 开始）
//   - value: 要设置的值（true=1, false=0）
func SetBit(data []byte, offset uint64, value bool) {
	byteIndex := offset / 8
	bitIndex := offset % 8

	if byteIndex >= uint64(len(data)) {
		return // 超出范围，忽略
	}

	if value {
		data[byteIndex] |= 1 << bitIndex
	} else {
		data[byteIndex] &^= 1 << bitIndex
	}
}

// GetBit 获取字节数组中指定位的值
//
// Parameters:
//   - data: 字节数组
//   - offset: 位偏移量（从 0 开始）
//
// Returns:
//   - 位的值（true=1, false=0）
func GetBit(data []byte, offset uint64) bool {
	byteIndex := offset / 8
	bitIndex := offset % 8

	if byteIndex >= uint64(len(data)) {
		return false // 超出范围，返回 false
	}

	return (data[byteIndex] & (1 << bitIndex)) != 0
}

// CountBits 计算位图中设置的位的数量
//
// 使用 Brian Kernighan 算法，时间复杂度 O(k)，k 是设置位的数量
//
// Parameters:
//   - bitmap: 位图（64 位）
//
// Returns:
//   - 设置位的数量
func CountBits(bitmap uint64) int {
	return bits.OnesCount64(bitmap)
}

// NextFreeSlot 查找位图中下一个空闲的槽位
//
// Parameters:
//   - bitmap: 位图（64 位）
//
// Returns:
//   - 空闲槽位的索引（0-63），如果没有空闲槽位返回 -1
func NextFreeSlot(bitmap uint64) int {
	if bitmap == 0xffffffffffffffff {
		return -1 // 全部占用
	}

	// 使用 TrailingZeros64 找到第一个 0 位
	// 取反后，第一个 1 就是原来的第一个 0
	return bits.TrailingZeros64(^bitmap)
}

// FindFirstSet 查找位图中第一个设置的位
//
// Parameters:
//   - bitmap: 位图（64 位）
//
// Returns:
//   - 第一个设置位的索引（0-63），如果没有设置位返回 -1
func FindFirstSet(bitmap uint64) int {
	if bitmap == 0 {
		return -1
	}

	return bits.TrailingZeros64(bitmap)
}

// SetBitRange 设置位图中范围内的所有位
//
// Parameters:
//   - bitmap: 位图指针（64 位）
//   - start: 起始位索引
//   - end: 结束位索引（不包含）
//   - value: 要设置的值（true=1, false=0）
func SetBitRange(bitmap *uint64, start, end uint, value bool) {
	if start >= end || end > 64 {
		return
	}

	var mask uint64
	if value {
		// 创建掩码：start 到 end 位为 1
		mask = (^uint64(0) << start) & (^uint64(0) >> (64 - end))
		*bitmap |= mask
	} else {
		// 创建掩码：start 到 end 位为 1
		mask = (^uint64(0) << start) & (^uint64(0) >> (64 - end))
		*bitmap &^= mask
	}
}

// IsBitRangeFull 检查位图中的范围是否全部设置
//
// Parameters:
//   - bitmap: 位图（64 位）
//   - start: 起始位索引
//   - end: 结束位索引（不包含）
//
// Returns:
//   - true 如果范围内的位全部设置，否则 false
func IsBitRangeFull(bitmap uint64, start, end uint) bool {
	if start >= end || end > 64 {
		return false
	}

	// 创建掩码：start 到 end 位为 1
	mask := (^uint64(0) << start) & (^uint64(0) >> (64 - end))

	return (bitmap & mask) == mask
}

// CountBitsInRange 计算位图中范围内设置的位的数量
//
// Parameters:
//   - bitmap: 位图（64 位）
//   - start: 起始位索引
//   - end: 结束位索引（不包含）
//
// Returns:
//   - 范围内设置位的数量
func CountBitsInRange(bitmap uint64, start, end uint) int {
	if start >= end || end > 64 {
		return 0
	}

	// 创建掩码：start 到 end 位为 1
	mask := (^uint64(0) << start) & (^uint64(0) >> (64 - end))

	return CountBits(bitmap & mask)
}

// BytesToUint64 将字节数组转换为 uint64（小端序）
//
// Parameters:
//   - data: 字节数组（最多 8 字节）
//
// Returns:
//   - 转换后的 uint64 值
func BytesToUint64(data []byte) uint64 {
	var result uint64
	for i, b := range data {
		if i >= 8 {
			break
		}
		result |= uint64(b) << (i * 8)
	}
	return result
}

// Uint64ToBytes 将 uint64 转换为字节数组（小端序）
//
// Parameters:
//   - value: uint64 值
//
// Returns:
//   - 8 字节的数组
func Uint64ToBytes(value uint64) []byte {
	return []byte{
		byte(value),
		byte(value >> 8),
		byte(value >> 16),
		byte(value >> 24),
		byte(value >> 32),
		byte(value >> 40),
		byte(value >> 48),
		byte(value >> 56),
	}
}
