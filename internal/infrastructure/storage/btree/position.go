package btree

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidPosition = errors.New("invalid page position")
	ErrInvalidChunkID  = errors.New("invalid chunk id")
	ErrInvalidOffset   = errors.New("invalid offset")
	ErrInvalidPageType = errors.New("invalid page type")
)

const (
	// ChunkIDBits ChunkID 的位数
	ChunkIDBits = 26
	// MaxChunks 最大 Chunk 数量 (2^26 = 67M)
	MaxChunks = 1 << ChunkIDBits
	// MaxOffset 最大 Offset (2^32 = 4GB)
	MaxOffset = 1 << 32
	// MaxPageType 最大页面类型 (2^5 = 32)
	MaxPageType = 1 << 5
)

// 页面类型常量（用于 64 位位置编码）
const (
	PageTypeReserved = 0 // 保留位
	PageTypeLeaf     = 1 // 叶子节点
	PageTypeInternal = 2 // 内部节点
	PageTypeRoot     = 3 // 根节点（特殊标记）
	PageTypeMeta     = 4 // 元页面
)

// EncodePagePos 编码页面位置（64 位编码）
//
// 位布局：
// ┌────────────────────────────────────────────────────────────────┐
// │  63-38 (26 bits) │ 37-6 (32 bits) │ 5-1 (5 bits) │ 0 (1 bit) │
// │    Chunk ID      │     Offset     │   Page Type  │  保留位   │
// └────────────────────────────────────────────────────────────────┘
//
// 支持规模：
// - Chunk ID：67,108,864 个（26 bits）
// - Offset：4GB per Chunk（32 bits）
// - Page Type：32 种（5 bits）
// - 总容量：67M × 4GB = 268TB（实际 256MB/Chunk）到 16PB（理论上限）
//
// 说明：
// - Chunk 使用固定 256MB，因此 32 bits Offset 远超需求
// - 保留 32 bits Offset 以支持未来扩展
func EncodePagePos(chunkID, offset, pageType int) (int64, error) {
	// 边界检查
	if chunkID < 0 || chunkID >= MaxChunks {
		return 0, fmt.Errorf("%w: chunk ID %d out of range [0, %d)", ErrInvalidChunkID, chunkID, MaxChunks)
	}
	if offset < 0 || offset >= MaxOffset {
		return 0, fmt.Errorf("%w: offset %d out of range [0, %d)", ErrInvalidOffset, offset, MaxOffset)
	}
	if pageType < 0 || pageType >= MaxPageType {
		return 0, fmt.Errorf("%w: page type %d out of range [0, %d)", ErrInvalidPageType, pageType, MaxPageType)
	}

	// 编码：[63:38] ChunkID | [37:6] Offset | [5:1] PageType | [0] 保留
	return (int64(chunkID) << 38) | (int64(offset) << 6) | (int64(pageType) << 1), nil
}

// DecodePagePos 解码页面位置
func DecodePagePos(pos int64) (chunkID, offset, pageType int) {
	chunkID = int(pos >> 38)              // [63:38]
	offset = int((pos >> 6) & 0xFFFFFFFF) // [37:6]
	pageType = int((pos >> 1) & 0x1F)     // [5:1]
	return
}

// ValidatePosition 验证位置是否有效
func ValidatePosition(pos int64) bool {
	if pos == 0 {
		return false
	}

	chunkID, offset, pageType := DecodePagePos(pos)
	return chunkID >= 0 && chunkID < MaxChunks &&
		offset >= 0 && offset < MaxOffset &&
		pageType >= 0 && pageType < MaxPageType
}

// GetChunkID 从位置编码中提取 ChunkID
func GetChunkID(pos int64) int {
	return int(pos >> 38)
}

// GetOffset 从位置编码中提取 Offset
func GetOffset(pos int64) int {
	return int((pos >> 6) & 0xFFFFFFFF)
}

// GetPageType 从位置编码中提取 PageType
func GetPageType(pos int64) int {
	return int((pos >> 1) & 0x1F)
}
