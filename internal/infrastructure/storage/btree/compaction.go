// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"fmt"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/mvcc"
)

// WatermarkProvider supplies the GC safe watermark for Compaction.
// Compaction only physically removes Tombstone entries whose commitTS < Watermark().
// Defined in btree package per DIP — Compaction depends on this abstraction, not on mvcc directly.
type WatermarkProvider interface {
	Watermark() uint64
}

// CompactionConfig configures the compaction cycle.
type CompactionConfig struct {
	Threshold float64 // TombstoneCount/Count threshold (default 0.5)
}

// Compact performs one compaction cycle, reclaiming space from pages with high tombstone ratios.
// Walks the leaf chain via nextPage pointers, skipping NodeMerging/NodeRedirect pages.
func (b *BTree) Compact(wp WatermarkProvider) error {
	if err := b.checkOpen(); err != nil {
		return err
	}
	cfg := CompactionConfig{Threshold: 0.5}
	return b.compactCycle(wp, cfg)
}

func (b *BTree) compactCycle(wp WatermarkProvider, cfg CompactionConfig) error {
	rootPI := b.rootRef.GetPageInfo()
	if rootPI == nil {
		return nil
	}

	// Traverse leaf chain from the leftmost leaf
	leafRef := b.findLeftmostLeaf(rootPI)
	if leafRef == nil {
		return nil
	}
	leafRef.Retain()
	defer leafRef.Release()

	compacted := 0
	for leafRef != nil {
		pi := leafRef.GetPageInfo()
		if pi == nil || !pi.IsLeaf {
			break
		}

		// Skip busy pages (being merged/split)
		if pi.IsBusy() {
			nextID := b.getNextPageID(pi.PageID)
			if nextID == 0 {
				break
			}
			leafRef = NewPageRef(model.PageID(nextID), 0, b.rootRef.freeFunc)
			leafRef.Retain()
			continue
		}

		leaf, err := b.storage.GetLeafPage(pi.PageID)
		if err != nil {
			break
		}

		tombstoneCount := b.getTombstoneCount(pi.PageID)
		count := leaf.Count()
		if count > 0 && float64(tombstoneCount)/float64(count) > cfg.Threshold {
			if _, err := b.compactPage(leafRef, pi, leaf, wp); err == nil {
				compacted++
			}
		}

		nextID := b.getNextPageID(pi.PageID)
		if nextID == 0 {
			break
		}
		leafRef.Release()
		leafRef = NewPageRef(model.PageID(nextID), 0, b.rootRef.freeFunc)
		leafRef.Retain()
	}

	if b.metrics != nil && compacted > 0 {
		b.metrics.IncrementCompact()
	}
	return nil
}

func (b *BTree) findLeftmostLeaf(rootPI *PageInfo) *PageRef {
	if rootPI.IsLeaf {
		return NewPageRef(rootPI.PageID, 0, b.rootRef.freeFunc)
	}
	// Walk down the leftmost child path
	currentRef := NewPageRef(rootPI.PageID, 0, b.rootRef.freeFunc)
	currentRef.Retain()
	defer currentRef.Release()

	for {
		pi := currentRef.GetPageInfo()
		if pi == nil {
			return nil
		}
		if pi.IsLeaf {
			return NewPageRef(pi.PageID, 0, b.rootRef.freeFunc)
		}
		node, err := b.storage.GetNodePage(pi.PageID)
		if err != nil {
			return nil
		}
		childID := node.GetChild(0)
		currentRef.Release()
		currentRef = NewPageRef(childID, 0, b.rootRef.freeFunc)
		currentRef.Retain()
	}
}

func (b *BTree) getNextPageID(pageID model.PageID) model.PageID {
	rawID := uint32(pageID)
	next := b.storage.pa.GetNextPage(rawID)
	if next == 0xFFFFFFFF {
		return 0
	}
	return model.PageID(next)
}

func (b *BTree) getTombstoneCount(pageID model.PageID) int {
	return int(b.storage.pa.GetTombstoneCount(uint32(pageID)))
}

// FIXME(CRITICAL): compactPage operates on a temporary PageRef created via NewPageRef
// during leaf chain traversal, NOT the actual in-tree PageRef stored in the parent's
// ChildrenCache. The CAS on line 188 therefore only updates the temporary object's pInfo;
// the real in-tree PageRef retains the old pInfo pointing to the now-freed page ID.
//
// Additionally, even if CAS targeted the correct PageRef, the parent node's child pointer
// (node.GetChild(idx)) would still reference the old page ID — compaction must also
// COW-update the parent node to point to the newRawID.
//
// Correct fix requires:
//  1. Walk from root (via searchPath) to find the leaf's parent node
//  2. Get the real child PageRef from the parent's ChildrenCache
//  3. CAS on the real PageRef
//  4. COW the parent node to replace the child pointer with newRawID
//  5. CAS on the parent's PageRef
//  6. Update the parent's ChildrenCache
//
// This is equivalent to the split/merge COW mechanism and should be implemented
// as part of a comprehensive compaction correctness pass.
func (b *BTree) compactPage(ref *PageRef, oldPI *PageInfo, leaf LeafPage, wp WatermarkProvider) (LeafPage, error) {
	count := leaf.Count()
	if count == 0 {
		return nil, nil
	}

	watermark := wp.Watermark()

	// Collect non-tombstone entries whose commitTS < watermark
	rawID := uint32(oldPI.PageID)
	var keepKeys, keepVals [][]byte
	for i := 0; i < count; i++ {
		val := leaf.GetValue(i)
		mvccVal, err := mvcc.ParseMVCC(val)
		if err != nil {
			keepKeys = append(keepKeys, leaf.GetKey(i))
			keepVals = append(keepVals, val)
			continue
		}
		if mvccVal.IsTombstone() && mvccVal.BeginTS < watermark {
			continue // safe to reclaim
		}
		keepKeys = append(keepKeys, leaf.GetKey(i))
		keepVals = append(keepVals, val)
	}

	// Allocate COW page with only kept entries
	newRawID, err := b.storage.pm.Alloc()
	if err != nil {
		return nil, fmt.Errorf("btree: compact page alloc: %w", err)
	}
	srcVersion := b.storage.pa.GetVersion(rawID)
	b.storage.pa.InitLeafPage(newRawID, srcVersion+1)

	dataEnd := uint16(0)
	for i := range keepKeys {
		if err := b.storage.pa.InsertLeafEntry(newRawID, i, keepKeys[i], keepVals[i], &dataEnd); err != nil {
			b.storage.pm.Free(newRawID)
			return nil, fmt.Errorf("btree: compact page insert: %w", err)
		}
	}
	b.storage.pa.SetTombstoneCount(newRawID, 0)

	newPI := &PageInfo{
		PageID:    model.PageID(newRawID),
		Version:   oldPI.Version + 1,
		IsLeaf:    true,
		NodeState: NodeNormal,
	}

	if !ref.CAS(oldPI, newPI) {
		b.storage.pm.Free(newRawID)
		return nil, nil // CAS conflict (expected — another goroutine compacted this page)
	}

	_ = b.storage.pm.Free(uint32(oldPI.PageID))
	return &leafPageHandle{id: model.PageID(newRawID), pa: b.storage.pa, storage: b.storage}, nil
}

// offheap imported for SizeofPageHeader init-time assertion in offheap_storage.go
