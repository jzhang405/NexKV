// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	errpkg "github.com/jzhang405/NexKV/pkg/errors"

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

// compactionResult holds pre-scanned data from the threshold check,
// passed to compactPageWithParent to avoid re-parsing MVCC entries.
type compactionResult struct {
	keepKeys [][]byte
	keepVals [][]byte
}

func (b *BTree) compactCycle(wp WatermarkProvider, cfg CompactionConfig) error {
	rootPI := b.rootRef.GetPageInfo()
	if rootPI == nil {
		return nil
	}

	// Walk leaf chain using raw page IDs instead of temp PageRefs.
	// Temp PageRefs are never updated by CAS (no concurrent writer holds them),
	// so pInfo.NodeState is always NodeNormal — IsBusy() is always false.
	pageID := b.findLeftmostLeafID(rootPI)
	if pageID == 0 {
		return nil
	}

	watermark := wp.Watermark()
	compacted := 0

	for pageID != 0 {
		if !b.storage.IsLeafPage(pageID) {
			break
		}

		leaf, err := b.storage.GetLeafPage(pageID)
		if err != nil {
			// Transient error (e.g. concurrent page type change) — skip, continue chain
			pageID = b.nextLeafPageID(pageID)
			continue
		}

		count := leaf.Count()
		if count > 0 {
			// Single-pass scan: collect keep entries AND count tombstones.
			cr := b.scanLeafEntries(leaf, count, watermark)
			kept := len(cr.keepKeys)
			tombstoneCount := count - kept
			if float64(tombstoneCount)/float64(count) > cfg.Threshold && kept < count {
				if b.tryCompactLeaf(pageID, leaf, cr) {
					compacted++
				}
			}
		}

		pageID = b.nextLeafPageID(pageID)
	}

	if b.metrics != nil && compacted > 0 {
		b.metrics.IncrementCompact()
	}
	return nil
}

// scanLeafEntries parses MVCC values once, returning entries that should be kept.
// Entries with parse errors are kept (conservative).
func (b *BTree) scanLeafEntries(leaf LeafPage, count int, watermark uint64) compactionResult {
	keepKeys := make([][]byte, 0, count)
	keepVals := make([][]byte, 0, count)
	for i := 0; i < count; i++ {
		val := leaf.GetValue(i)
		mv, err := mvcc.ParseMVCC(val)
		if err != nil {
			keepKeys = append(keepKeys, leaf.GetKey(i))
			keepVals = append(keepVals, val)
			continue
		}
		if mv.IsTombstone() && mv.BeginTS < watermark {
			continue
		}
		keepKeys = append(keepKeys, leaf.GetKey(i))
		keepVals = append(keepVals, val)
	}
	return compactionResult{keepKeys: keepKeys, keepVals: keepVals}
}

// tryCompactLeaf uses searchPath to obtain the real in-tree PageRef,
// then calls compactPageWithParent with pre-scanned keep lists.
func (b *BTree) tryCompactLeaf(chainPageID model.PageID, leaf LeafPage, cr compactionResult) bool {
	firstKey := leaf.GetKey(0)
	path, err := searchPath(b.rootRef, firstKey)
	if err != nil {
		return false
	}
	defer path.ReleaseAll()

	leafRef := path.Leaf().Ref
	realPI := leafRef.GetPageInfo()
	if realPI == nil || realPI.IsBusy() || realPI.PageID != chainPageID {
		return false
	}

	realLeaf, err := b.storage.GetLeafPage(chainPageID)
	if err != nil {
		return false
	}

	ok, _ := b.compactPageWithParent(leafRef, realPI, realLeaf, cr, path)
	return ok
}

// compactPageWithParent performs COW compaction using pre-scanned keep entries.
//
// Phases:
//
//	A: CAS leafRef to NodeCompacting (prevents concurrent splits/merges)
//	B: (skip — keepKeys/keepVals already computed in scanLeafEntries)
//	C: Allocate COW page, insert keep entries, preserve leaf chain pointers
//	D: COW parent node ReplaceChild + CAS parentRef (best-effort; runtime correctness
//	   only needs leafRef CAS since searchPath navigates via ChildrenCache)
//	E: CAS leafRef to final state (new page ID, NodeNormal)
func (b *BTree) compactPageWithParent(
	leafRef *PageRef,
	oldPI *PageInfo,
	leaf LeafPage,
	cr compactionResult,
	path SearchPath,
) (bool, error) {
	// Phase A: CAS to NodeCompacting
	compactingInfo := &PageInfo{
		PageID:    oldPI.PageID,
		Version:   oldPI.Version + 1,
		IsLeaf:    true,
		NodeState: NodeCompacting,
	}
	if !leafRef.CAS(oldPI, compactingInfo) {
		return false, nil
	}

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

	// Phase C: Allocate COW page with keep entries
	rawID := uint32(oldPI.PageID)
	oldPrev := b.storage.pa.GetPrevPage(rawID)
	oldNext := b.storage.pa.GetNextPage(rawID)

	var err error
	newRawID, err = b.storage.pm.Alloc()
	if err != nil {
		return false, errpkg.Wrapf(err, "btree: compact alloc")
	}

	srcVersion := b.storage.pa.GetVersion(rawID)
	b.storage.pa.InitLeafPage(newRawID, srcVersion+1)
	b.storage.pa.SetPrevPage(newRawID, oldPrev)
	b.storage.pa.SetNextPage(newRawID, oldNext)

	dataEnd := uint16(0)
	for i := range cr.keepKeys {
		if err := b.storage.pa.InsertLeafEntry(newRawID, i, cr.keepKeys[i], cr.keepVals[i], &dataEnd); err != nil {
			return false, errpkg.Wrapf(err, "btree: compact insert")
		}
	}
	b.storage.pa.SetTombstoneCount(newRawID, 0)

	// Phase D: COW parent node (best-effort)
	if len(path) >= 2 {
		parentEntry := path[len(path)-2]
		parentRef := parentEntry.Ref
		parentPI := parentRef.GetPageInfo()
		if parentPI != nil && !parentPI.IsBusy() {
			if parent, perr := b.storage.GetNodePage(parentPI.PageID); perr == nil {
				actualIdx := parentEntry.Index
				for ci := 0; ci < parent.ChildCount(); ci++ {
					if parent.GetChild(ci) == leafRef.pageID {
						actualIdx = ci
						break
					}
				}
				if actualIdx < parent.ChildCount() {
					if newParent, rerr := parent.ReplaceChild(actualIdx, model.PageID(newRawID)); rerr == nil {
						newParPI := &PageInfo{
							PageID:    newParent.PageID(),
							Version:   parentPI.Version + 1,
							IsLeaf:    false,
							NodeState: parentPI.NodeState,
						}
						if !parentRef.CAS(parentPI, newParPI) {
							_ = b.storage.FreePage(newParent.PageID())
						} else if b.epochMgr != nil {
							b.epochMgr.Retire(b.epochMgr.AllocSlot(), parentPI.PageID)
						}
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

	// Phase E success — retire old leaf page (P-page)
	if b.epochMgr != nil {
		b.epochMgr.Retire(b.epochMgr.AllocSlot(), oldPI.PageID)
	}

	cleanup = false
	return true, nil
}

// findLeftmostLeafID walks down the leftmost child path and returns the first leaf page ID.
func (b *BTree) findLeftmostLeafID(rootPI *PageInfo) model.PageID {
	pageID := rootPI.PageID
	if rootPI.IsLeaf {
		return pageID
	}
	for {
		node, err := b.storage.GetNodePage(pageID)
		if err != nil {
			return 0
		}
		pageID = node.GetChild(0)
		if b.storage.IsLeafPage(pageID) {
			return pageID
		}
	}
}

// nextLeafPageID returns the next leaf page ID in the physical leaf chain,
// or 0 if this is the last leaf.
func (b *BTree) nextLeafPageID(pageID model.PageID) model.PageID {
	next := b.storage.pa.GetNextPage(uint32(pageID))
	if next == sentinelNoPage {
		return 0
	}
	return model.PageID(next)
}

// sentinelNoPage (0xFFFFFFFF = math.MaxUint32) is the offheap sentinel for "no page".
const sentinelNoPage = 0xFFFFFFFF
