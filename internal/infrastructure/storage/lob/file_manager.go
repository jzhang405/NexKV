// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package lob

import (
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/mvcc"
)

// DefaultLOBFileManager is the default implementation of mvcc.LOBFileManager.
type DefaultLOBFileManager struct {
	store *lobFileStore
	cfg   Config
}

// NewDefaultLOBFileManager creates a new DefaultLOBFileManager rooted at dir
// with the given options. If no options are provided, defaults are used.
func NewDefaultLOBFileManager(dir string, opts ...Option) (*DefaultLOBFileManager, error) {
	cfg := DefaultConfig().Apply(opts...)
	store, err := newLOBFileStore(dir, cfg)
	if err != nil {
		return nil, err
	}
	return &DefaultLOBFileManager{store: store, cfg: cfg}, nil
}

// Config returns a copy of the current config.
func (m *DefaultLOBFileManager) Config() Config {
	return m.cfg
}

// CleanupTmp removes leftover .tmp-* files from prior crashes.
func (m *DefaultLOBFileManager) CleanupTmp() error {
	return m.store.CleanupTmp()
}

// Close releases all resources: stops background cleaner + closes fd cache.
func (m *DefaultLOBFileManager) Close() {
	m.store.Close()
}

// FDCacheStats returns fd cache hit/miss statistics.
func (m *DefaultLOBFileManager) FDCacheStats() FDCacheStats {
	return m.store.fdCache.stats()
}

// Create writes data to a new LOB file and returns its reference.
func (m *DefaultLOBFileManager) Create(data []byte) (mvcc.LOBFileRef, error) {
	return m.store.Create(data)
}

// Read reads the full data of a LOB file.
func (m *DefaultLOBFileManager) Read(ref mvcc.LOBFileRef) ([]byte, error) {
	return m.store.Read(ref)
}

// Delete unlinks a LOB file.
func (m *DefaultLOBFileManager) Delete(ref mvcc.LOBFileRef) error {
	return m.store.Delete(ref)
}
