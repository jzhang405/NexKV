// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

// Package model provides BTree-specific domain models.
package model

import "time"

// PageID uniquely identifies a page.
type PageID uint64

// SnapshotID uniquely identifies a snapshot.
type SnapshotID uint64

const (
	// RootPageID is the ID of the root page.
	RootPageID PageID = 1

	// InvalidPageID represents an invalid page ID.
	InvalidPageID PageID = 0

	// InvalidSnapshotID represents an invalid snapshot ID.
	InvalidSnapshotID SnapshotID = 0
)

// PageType represents the type of a page.
type PageType uint8

const (
	// LeafPage is a leaf node page (stores key-value pairs).
	LeafPage PageType = iota

	// InternalPage is an internal node page (stores indexes).
	InternalPage

	// MetaPage is a metadata page (stores tree information).
	MetaPage
)

// String returns the string representation of PageType.
func (pt PageType) String() string {
	switch pt {
	case LeafPage:
		return "Leaf"
	case InternalPage:
		return "Internal"
	case MetaPage:
		return "Meta"
	default:
		return "Unknown"
	}
}

// BTreeConfig holds BTree configuration.
type BTreeConfig struct {
	// PageSize is the page size in bytes.
	PageSize int

	// MaxKeys is the maximum number of keys per page.
	MaxKeys int

	// MinKeys is the minimum number of keys per page (used for merge judgment).
	MinKeys int

	// MaxVersions is the maximum number of versions to keep (for GC).
	MaxVersions int

	// EnablePool enables object pooling (based on Phase 0.5 validation).
	EnablePool bool

	// Compression is the compression type.
	Compression CompressionType

	// GCPercent is the GC trigger threshold (GOGC).
	// 值为 100 表示默认行为（堆增长 100% 时触发 GC）
	// 值为 off 表示禁用 GC（仅用于特殊场景）
	// 优化：设置为 400 可以减少 GC 触发频率，提升性能
	GCPercent int

	// DeltaChainThreshold is the maximum number of deltas before auto-materialization.
	// 物化阈值：Delta 链长度超过此值时自动物化
	// 默认：10（可根据工作负载调整）
	// - 读密集型：可以设置更高（15-20），减少物化频率
	// - 写密集型：可以设置更低（5-8），更快物化
	DeltaChainThreshold int

	// DeltaChainRatio is the ratio threshold (0-1) for auto-materialization.
	// 物化比例阈值：Delta 数量占页面大小的比例超过此值时物化
	// 默认：0.2（20%）
	// 例如：100 键的页面，增量超过 20 个时物化
	DeltaChainRatio float64

	// HotPageThreshold is the read count threshold for hot data identification.
	// 热数据阈值：页面读取次数超过此值时识别为热数据
	// 默认：1000 次
	// - 读密集场景：可以设置更高（2000-5000）
	// - 写密集场景：可以设置更低（500-800）
	HotPageThreshold int64

	// MemoryPressureThreshold is the memory usage threshold (0-1) for pressure detection.
	// 内存压力阈值：内存使用率超过此值时触发内存压力物化
	// 默认：0.8（80%）
	// 当内存紧张且 refCount=1 时，立即物化以释放内存
	MemoryPressureThreshold float64
}

const (
	// DefaultPageSize is the default page size (4KB, aligned with filesystem blocks).
	DefaultPageSize = 4096

	// DefaultMaxKeys is the default maximum number of keys per page.
	// 优化：从 128 增加到 256，降低 Split 触发频率 50%
	// 摊销开销：从 8.2 ns/插入 → 4.1 ns/插入
	// 权衡：节点变大但 Split 频率降低，整体性能提升
	DefaultMaxKeys = 256

	// DefaultMinKeys is the default minimum number of keys per page.
	// 优化：保持为 DefaultMaxKeys 的一半
	DefaultMinKeys = 128

	// DefaultMaxVersions is the default maximum number of versions.
	DefaultMaxVersions = 10
)

// NewDefaultBTreeConfig creates a default BTree configuration.
func NewDefaultBTreeConfig() *BTreeConfig {
	return &BTreeConfig{
		PageSize:                DefaultPageSize,
		MaxKeys:                 DefaultMaxKeys,
		MinKeys:                 DefaultMinKeys,
		MaxVersions:             DefaultMaxVersions,
		EnablePool:              true, // Phase 0.5 validation: Node using pool has 14.9x improvement
		Compression:             CompressionNone,
		GCPercent:               100,  // 默认 GC 行为（生产环境）
		DeltaChainThreshold:     10,   // Delta 链长度阈值
		DeltaChainRatio:         0.2,  // 20% 比例阈值
		HotPageThreshold:        1000, // 热数据读取阈值
		MemoryPressureThreshold: 0.8,  // 80% 内存压力阈值
	}
}

// CompressionType represents the compression type.
type CompressionType uint8

const (
	// CompressionNone disables compression.
	CompressionNone CompressionType = iota

	// CompressionSnappy uses Snappy compression.
	CompressionSnappy

	// CompressionLZ4 uses LZ4 compression.
	CompressionLZ4

	// CompressionZSTD uses ZSTD compression.
	CompressionZSTD
)

// BTreeStats holds BTree statistics.
type BTreeStats struct {
	// Basic statistics
	TotalKeys  int64
	TotalPages int
	TreeHeight int

	// Performance statistics
	ReadCount  int64
	WriteCount int64
	SplitCount int64
	MergeCount int64

	// Version statistics
	CurrentVersion uint64
	ActiveVersions int

	// Pool statistics
	PoolHits   int64
	PoolMisses int64
}

// SnapshotInfo holds snapshot information.
type SnapshotInfo struct {
	ID        SnapshotID
	RootID    PageID
	Version   uint64
	CreatedAt time.Time
	RefCount  int32
}
