// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

// Package service provides storage service interfaces.
package service

import (
	"context"
)

// KVStore defines the unified storage interface.
type KVStore interface {
	// Basic CRUD operations
	Get(ctx context.Context, key []byte) ([]byte, error)
	Set(ctx context.Context, key, value []byte) error
	Delete(ctx context.Context, key []byte) error

	// Batch operations
	GetBatch(ctx context.Context, keys [][]byte) ([][]byte, error)
	SetBatch(ctx context.Context, pairs []KVPair) error
	DeleteBatch(ctx context.Context, keys [][]byte) error

	// Range scan
	RangeScan(ctx context.Context, start, end []byte) (Iterator, error)

	// Transaction support (reserved for Phase 4)
	BeginTx(ctx context.Context, opts ...TxOption) (Transaction, error)

	// Snapshot support (reserved for Phase 2)
	CreateSnapshot(ctx context.Context) (SnapshotID, error)
	ReleaseSnapshot(ctx context.Context, id SnapshotID) error

	// Statistics
	Stats(ctx context.Context) (*StoreStats, error)

	// Lifecycle
	Close() error
}

// KVPair represents a key-value pair for batch operations.
type KVPair struct {
	Key   []byte
	Value []byte
}

// Iterator provides range scan functionality.
type Iterator interface {
	Next() bool
	Key() []byte
	Value() []byte
	Err() error
	Close() error
}

// Transaction provides transactional operations (reserved for Phase 4).
type Transaction interface {
	Get(ctx context.Context, key []byte) ([]byte, error)
	Set(ctx context.Context, key, value []byte) error
	Delete(ctx context.Context, key []byte) error
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// TxOption configures transaction options.
type TxOption func(*TxOptions)

// TxOptions holds transaction configuration.
type TxOptions struct {
	ReadOnly bool
	Snapshot SnapshotID
}

// WithReadOnly creates a read-only transaction option.
func WithReadOnly() TxOption {
	return func(opts *TxOptions) {
		opts.ReadOnly = true
	}
}

// WithSnapshot creates a transaction with a specific snapshot.
func WithSnapshot(id SnapshotID) TxOption {
	return func(opts *TxOptions) {
		opts.Snapshot = id
	}
}

// SnapshotID uniquely identifies a snapshot.
type SnapshotID uint64

// StoreStats holds storage statistics.
type StoreStats struct {
	TotalKeys   int64
	TotalSize   int64
	TreeHeight  int
	PageCount   int
	Version     uint64
	ReadCount   int64
	WriteCount  int64
	SplitCount  int64
	MergeCount  int64
	ActiveVersions int
}

// IOStats holds I/O statistics (reserved for Phase 4).
type IOStats struct {
	ReadBytes  uint64
	WriteBytes uint64
	ReadOps    uint64
	WriteOps   uint64
}

// BTreeStats holds BTree-specific statistics.
type BTreeStats struct {
	StoreStats

	// BTree specific stats
	PoolHits   int64
	PoolMisses int64
}

// WALStats holds WAL statistics (reserved for Phase 4).
type WALStats struct {
	SequenceNumber uint64
	Entries        uint64
	SyncCount       uint64
}
