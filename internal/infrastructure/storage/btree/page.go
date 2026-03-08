// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"sync/atomic"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

const (
	// PageSize is the fixed size of a page in bytes (4KB, aligned with filesystem blocks).
	PageSize = 4096

	// PageHeaderSize is the size of page metadata in bytes.
	PageHeaderSize = 21 // Type(1) + Version(8) + ID(8) + RefCount(4)

	// PageDataSize is the usable data size in a page.
	PageDataSize = PageSize - PageHeaderSize
)

// Page represents a fixed-size disk block.
// Pages are the fundamental unit of storage and I/O.
type Page struct {
	// ID is the unique page identifier.
	ID model.PageID

	// Type is the page type (leaf, internal, or meta).
	Type model.PageType

	// Version is the page version number for CCOW.
	Version uint64

	// Data contains the raw page data.
	Data [PageDataSize]byte

	// RefCount is the reference count for concurrent access.
	RefCount atomic.Int32

	// dirty indicates whether the page has been modified.
	dirty bool
}

// NewPage creates a new page with the given ID and type.
func NewPage(id model.PageID, pageType model.PageType) *Page {
	p := &Page{
		ID:      id,
		Type:    pageType,
		Version: 0,
		dirty:   false,
	}
	p.RefCount.Store(1) // Initial reference count
	return p
}

// Acquire increments the reference count.
// The page will not be freed until the reference count reaches zero.
func (p *Page) Acquire() {
	p.RefCount.Add(1)
}

// Release decrements the reference count.
// When the reference count reaches zero, the page can be freed.
func (p *Page) Release() int32 {
	return p.RefCount.Add(-1)
}

// GetRefCount returns the current reference count.
func (p *Page) GetRefCount() int32 {
	return p.RefCount.Load()
}

// IsLeaf returns true if this is a leaf page.
func (p *Page) IsLeaf() bool {
	return p.Type == model.LeafPage
}

// IsInternal returns true if this is an internal page.
func (p *Page) IsInternal() bool {
	return p.Type == model.InternalPage
}

// IsMeta returns true if this is a meta page.
func (p *Page) IsMeta() bool {
	return p.Type == model.MetaPage
}

// MarkDirty marks the page as dirty (modified).
func (p *Page) MarkDirty() {
	p.dirty = true
}

// ClearDirty clears the dirty flag.
func (p *Page) ClearDirty() {
	p.dirty = false
}

// IsDirty returns true if the page is dirty.
func (p *Page) IsDirty() bool {
	return p.dirty
}

// SetVersion sets the page version.
func (p *Page) SetVersion(version uint64) {
	p.Version = version
}

// GetVersion returns the page version.
func (p *Page) GetVersion() uint64 {
	return p.Version
}
