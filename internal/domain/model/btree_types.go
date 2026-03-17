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
		PageSize:    DefaultPageSize,
		MaxKeys:     DefaultMaxKeys,
		MinKeys:     DefaultMinKeys,
		MaxVersions: DefaultMaxVersions,
		EnablePool:  true, // Phase 0.5 validation: Node using pool has 14.9x improvement
		Compression: CompressionNone,
		GCPercent:   100, // 默认 GC 行为（生产环境）
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
