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
// Walks the leaf chain via nextPage pointers to identify candidates, then uses searchPath
// to obtain the real in-tree PageRef for COW-safe compaction.
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

	chainRef := b.findLeftmostLeaf(rootPI)
	if chainRef == nil {
		return nil
	}
	chainRef.Retain()
	defer chainRef.Release()

	compacted := 0
	for chainRef != nil {
		pi := chainRef.GetPageInfo()
		if pi == nil || !pi.IsLeaf {
			break
		}

		if pi.IsBusy() {
			nextID := b.getNextPageID(pi.PageID)
			if nextID == 0 {
				break
			}
			chainRef = NewPageRef(model.PageID(nextID), 0, b.rootRef.freeFunc)
			chainRef.Retain()
			continue
		}

		leaf, err := b.storage.GetLeafPage(pi.PageID)
		if err != nil {
			break
		}

		count := leaf.Count()
		// Count tombstones by scanning — the page header tombstoneCount field
		// is not maintained by Delete operations (Phase 6.5 follow-up).
		tombstoneCount := 0
		for i := 0; i < count; i++ {
			if mv, err := mvcc.ParseMVCC(leaf.GetValue(i)); err == nil && mv.IsTombstone() {
				tombstoneCount++
			}
		}
		if count > 0 && float64(tombstoneCount)/float64(count) > cfg.Threshold {
			if b.tryCompactLeaf(wp, leaf, pi.PageID) {
				compacted++
			}
		}

		nextID := b.getNextPageID(pi.PageID)
		if nextID == 0 {
			break
		}
		chainRef.Release()
		chainRef = NewPageRef(model.PageID(nextID), 0, b.rootRef.freeFunc)
		chainRef.Retain()
	}

	if b.metrics != nil && compacted > 0 {
		b.metrics.IncrementCompact()
	}
	return nil
}

// tryCompactLeaf uses searchPath to obtain the real in-tree PageRef and parent path,
// then calls compactPageWithParent to perform COW-safe compaction.
func (b *BTree) tryCompactLeaf(wp WatermarkProvider, leaf LeafPage, chainPageID model.PageID) bool {
	if leaf.Count() == 0 {
		return false
	}

	firstKey := leaf.GetKey(0) // defensive copy (GetKey copies from mmap)

	path, err := searchPath(b.rootRef, firstKey)
	if err != nil {
		return false
	}
	defer path.ReleaseAll()

	leafRef := path.Leaf().Ref
	realPI := leafRef.GetPageInfo()

	// Verify chain pageID matches tree pageID (catch concurrent splits/merges)
	if realPI == nil || realPI.IsBusy() || realPI.PageID != chainPageID {
		return false
	}

	// Re-read leaf via the tree-confirmed page ID
	realLeaf, err := b.storage.GetLeafPage(chainPageID)
	if err != nil {
		return false
	}

	ok, _ := b.compactPageWithParent(leafRef, realPI, realLeaf, wp, path)
	return ok
}

// compactPageWithParent performs COW compaction on a leaf page, using the real
// in-tree PageRef from the parent's ChildrenCache (obtained via searchPath).
//
// Phases:
//
//	A: CAS leafRef to NodeCompacting (prevents concurrent splits/merges)
//	B: Collect keep entries (non-tombstone or tombstone with commitTS >= watermark)
//	C: Allocate COW page, insert keep entries, preserve leaf chain pointers
//	D: COW parent node (ReplaceChild) + CAS parentRef (best-effort)
//	E: CAS leafRef to final state (new page ID, NodeNormal)
//
// The old page is NOT explicitly freed — PageRef.Release handles lifecycle
// when refCount drops to zero, consistent with writeOperation.
func (b *BTree) compactPageWithParent(
	leafRef *PageRef,
	oldPI *PageInfo,
	leaf LeafPage,
	wp WatermarkProvider,
	path SearchPath,
) (bool, error) {
	count := leaf.Count()
	if count == 0 {
		return false, nil
	}

	// Phase A: CAS to NodeCompacting (prevents concurrent structural changes)
	compactingInfo := &PageInfo{
		PageID:    oldPI.PageID,
		Version:   oldPI.Version + 1,
		IsLeaf:    true,
		NodeState: NodeCompacting,
	}
	if !leafRef.CAS(oldPI, compactingInfo) {
		return false, nil
	}

	// Rollback on any failure below
	var newRawID uint32
	cleanup := true
	defer func() {
		if cleanup {
			if newRawID != 0 {
				_ = b.storage.pm.Free(newRawID)
			}
			leafRef.CAS(compactingInfo, oldPI)
		}
	}()

	// Phase B: Collect keep entries
	watermark := wp.Watermark()
	rawID := uint32(oldPI.PageID)

	oldPrev := b.storage.pa.GetPrevPage(rawID)
	oldNext := b.storage.pa.GetNextPage(rawID)

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
			continue
		}
		keepKeys = append(keepKeys, leaf.GetKey(i))
		keepVals = append(keepVals, val)
	}

	if len(keepKeys) == count {
		// No entries reclaimed — rollback
		return false, nil
	}

	// Phase C: Allocate COW page and insert keep entries
	var err error
	newRawID, err = b.storage.pm.Alloc()
	if err != nil {
		return false, fmt.Errorf("btree: compact alloc: %w", err)
	}

	srcVersion := b.storage.pa.GetVersion(rawID)
	b.storage.pa.InitLeafPage(newRawID, srcVersion+1)

	// Preserve leaf chain position
	b.storage.pa.SetPrevPage(newRawID, oldPrev)
	b.storage.pa.SetNextPage(newRawID, oldNext)

	dataEnd := uint16(0)
	for i := range keepKeys {
		if err := b.storage.pa.InsertLeafEntry(newRawID, i, keepKeys[i], keepVals[i], &dataEnd); err != nil {
			return false, fmt.Errorf("btree: compact insert: %w", err)
		}
	}
	b.storage.pa.SetTombstoneCount(newRawID, 0)

	// Phase D: COW parent node (best-effort; runtime correctness only needs leafRef CAS)
	if len(path) >= 2 {
		parentEntry := path[len(path)-2]
		parentRef := parentEntry.Ref

		parentPI := parentRef.GetPageInfo()
		if parentPI != nil && !parentPI.IsBusy() {
			parent, perr := b.storage.GetNodePage(parentPI.PageID)
			if perr == nil {
				// Re-derive child index from parent page (path index may be stale)
				actualIdx := parentEntry.Index
				found := false
				for ci := 0; ci < parent.ChildCount(); ci++ {
					if parent.GetChild(ci) == leafRef.pageID {
						actualIdx = ci
						found = true
						break
					}
				}
				if found && actualIdx < parent.ChildCount() {
					newParent, rerr := parent.ReplaceChild(actualIdx, model.PageID(newRawID))
					if rerr == nil {
						newParPI := &PageInfo{
							PageID:    newParent.PageID(),
							Version:   parentPI.Version + 1,
							IsLeaf:    false,
							NodeState: parentPI.NodeState,
						}
						if !parentRef.CAS(parentPI, newParPI) {
							_ = b.storage.FreePage(newParent.PageID())
						}
						// ChildrenCache is automatically correct:
						// leafRef (in the cache) is CAS'd to finalPI in Phase E
					}
				}
			}
		}
	}

	// Phase E: CAS leafRef to final state
	finalPI := &PageInfo{
		PageID:    model.PageID(newRawID),
		Version:   compactingInfo.Version + 1,
		IsLeaf:    true,
		NodeState: NodeNormal,
	}
	if !leafRef.CAS(compactingInfo, finalPI) {
		return false, nil
	}

	cleanup = false
	return true, nil
}

func (b *BTree) findLeftmostLeaf(rootPI *PageInfo) *PageRef {
	if rootPI.IsLeaf {
		return NewPageRef(rootPI.PageID, 0, b.rootRef.freeFunc)
	}
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

// NOTE: tombstoneCount in page header is not maintained by Delete operations.
// The compaction threshold check counts tombstones by scanning leaf entries.
// Fixing the header maintenance is a Phase 6.5 follow-up.
