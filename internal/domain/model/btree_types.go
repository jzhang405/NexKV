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
}

const (
	// DefaultPageSize is the default page size (4KB, aligned with filesystem blocks).
	DefaultPageSize = 4096

	// DefaultMaxKeys is the default maximum number of keys per page.
	DefaultMaxKeys = 128

	// DefaultMinKeys is the default minimum number of keys per page.
	DefaultMinKeys = 64

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
	TotalKeys int64
	TotalPages int
	TreeHeight int

	// Performance statistics
	ReadCount  int64
	WriteCount int64
	SplitCount int64
	MergeCount int64

	// Version statistics
	CurrentVersion  uint64
	ActiveVersions int

	// Pool statistics
	PoolHits   int64
	PoolMisses  int64
}

// SnapshotInfo holds snapshot information.
type SnapshotInfo struct {
	ID        SnapshotID
	RootID    PageID
	Version   uint64
	CreatedAt time.Time
	RefCount  int32
}
