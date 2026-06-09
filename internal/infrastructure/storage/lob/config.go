// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package lob

import (
	"time"
)

// Config holds all configurable parameters for LOB storage.
// Zero values are replaced with defaults via DefaultConfig().
type Config struct {
	// OverflowThreshold is the byte size above which values use overflow page chains (Tier 1).
	// Values ≤ threshold are stored inline in the BTree leaf page.
	// Default: 2048 (2KB)
	OverflowThreshold int

	// FileThreshold is the byte size above which values use independent LOB files (Tier 2).
	// Values ≤ threshold use overflow page chains (if > OverflowThreshold) or inline storage.
	// Default: 65536 (64KB)
	FileThreshold int

	// FileMMapThreshold is the file size above which mmap is used for reading LOB files.
	// Files ≤ threshold use ReadAt. Default: 65536 (64KB)
	FileMMapThreshold int64

	// FDCacheCapacity is the max number of open fd's in the LRU cache.
	// Default: 64
	FDCacheCapacity int

	// FsyncInterval is the batch window for group-commit fsync.
	// Writers within this window have their fsync calls merged.
	// Default: 1ms
	FsyncInterval time.Duration

	// FsyncMaxBatch is the max entries in a single fsync batch before forced flush.
	// Default: 32
	FsyncMaxBatch int

	// FsyncQueueSize is the capacity of the fsync entry channel.
	// Default: 256
	FsyncQueueSize int

	// CleanerInterval is the interval for the background empty-directory cleaner.
	// Set to 0 to disable. Default: 5 minutes
	CleanerInterval time.Duration

	// MaxLOBRetiredLen is the upper bound on pending LOB retire entries in the epoch queue.
	// When exceeded, a forced tryReclaim is triggered to drain the queue.
	// Default: 65536
	MaxLOBRetiredLen int
}

// DefaultConfig returns a Config populated with sensible defaults.
func DefaultConfig() Config {
	return Config{
		OverflowThreshold: 2048,
		FileThreshold:     65536,
		FileMMapThreshold: 65536,
		FDCacheCapacity:   64,
		FsyncInterval:     1 * time.Millisecond,
		FsyncMaxBatch:     32,
		FsyncQueueSize:    256,
		CleanerInterval:   5 * time.Minute,
		MaxLOBRetiredLen:  65536,
	}
}

// Option is a functional option for configuring LOB storage.
type Option func(*Config)

// WithOverflowThreshold sets the Tier 1 overflow page threshold.
func WithOverflowThreshold(bytes int) Option {
	return func(c *Config) { c.OverflowThreshold = bytes }
}

// WithFileThreshold sets the Tier 2 LOB file threshold.
func WithFileThreshold(bytes int) Option {
	return func(c *Config) { c.FileThreshold = bytes }
}

// WithFileMMapThreshold sets the mmap read threshold.
func WithFileMMapThreshold(bytes int64) Option {
	return func(c *Config) { c.FileMMapThreshold = bytes }
}

// WithFDCacheCapacity sets the fd cache LRU capacity.
func WithFDCacheCapacity(n int) Option {
	return func(c *Config) { c.FDCacheCapacity = n }
}

// WithFsyncInterval sets the group-commit batch window.
func WithFsyncInterval(d time.Duration) Option {
	return func(c *Config) { c.FsyncInterval = d }
}

// WithFsyncMaxBatch sets the max entries per fsync batch.
func WithFsyncMaxBatch(n int) Option {
	return func(c *Config) { c.FsyncMaxBatch = n }
}

// WithFsyncQueueSize sets the fsync entry channel buffer size.
func WithFsyncQueueSize(n int) Option {
	return func(c *Config) { c.FsyncQueueSize = n }
}

// WithCleanerInterval sets the empty-dir cleaner interval. 0 disables.
func WithCleanerInterval(d time.Duration) Option {
	return func(c *Config) { c.CleanerInterval = d }
}

// WithMaxLOBRetiredLen sets the pending LOB retire queue limit.
func WithMaxLOBRetiredLen(n int) Option {
	return func(c *Config) { c.MaxLOBRetiredLen = n }
}

// Apply applies the given options to cfg and returns it.
func (c Config) Apply(opts ...Option) Config {
	for _, o := range opts {
		o(&c)
	}
	return c
}
