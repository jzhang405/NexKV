// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

// Package lob provides large object (LOB) storage management using overflow page chains.
// LOB values exceeding LOBSizeThreshold (2KB) are stored in linked overflow pages
// instead of inline in BTree leaf pages.
package lob

import (
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/mvcc"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/offheap"
)

// DefaultLOBManager is the default implementation of mvcc.LOBManager.
// It delegates overflow page allocation, reading, and freeing to offheap.PageManager.
type DefaultLOBManager struct {
	pm *offheap.PageManager
}

// NewDefaultLOBManager creates a new DefaultLOBManager backed by the given PageManager.
func NewDefaultLOBManager(pm *offheap.PageManager) *DefaultLOBManager {
	return &DefaultLOBManager{pm: pm}
}

// Allocate stores data in overflow page chains and returns a LOB reference.
func (m *DefaultLOBManager) Allocate(data []byte) (mvcc.LOBRef, error) {
	firstPageID, err := m.pm.AllocOverflow(data)
	if err != nil {
		return mvcc.LOBRef{}, err
	}
	return mvcc.LOBRef{
		FirstPageID: firstPageID,
		TotalLen:    uint32(len(data)),
	}, nil
}

// Read retrieves full data for a LOB reference by walking the overflow page chain.
func (m *DefaultLOBManager) Read(ref mvcc.LOBRef) ([]byte, error) {
	return m.pm.ReadOverflow(ref.FirstPageID, ref.TotalLen)
}

// Free releases all overflow pages in the chain.
func (m *DefaultLOBManager) Free(ref mvcc.LOBRef) error {
	return m.pm.FreeOverflow(ref.FirstPageID)
}
