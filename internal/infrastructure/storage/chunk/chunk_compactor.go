// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package chunk

import (
	"fmt"
	errpkg "github.com/jzhang405/NexKV/pkg/errors"
	"os"
	"sort"
	"sync/atomic"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// ChunkCompactor rewrites low-fill-rate chunks to reclaim space.
// Aligns with Lealone ChunkCompactor.java.
type ChunkCompactor struct {
	cm          *DiskChunkManager
	minFillRate int         // minimum fill rate (default 30%, max 50%)
	compacting  atomic.Bool // prevents concurrent Compact() runs
}

const maxRewriteSize = 64 * 1024 * 1024 // 64MB — single compaction I/O budget

// NewChunkCompactor creates a new ChunkCompactor.
func NewChunkCompactor(cm *DiskChunkManager, minFillRate int) *ChunkCompactor {
	if minFillRate <= 0 {
		minFillRate = 30
	}
	if minFillRate > 50 {
		minFillRate = 50
	}
	return &ChunkCompactor{cm: cm, minFillRate: minFillRate}
}

// NeedCompaction returns true if any non-last chunk has fillRate <= minFillRate or is empty.
func (c *ChunkCompactor) NeedCompaction() bool {
	c.cm.mu.RLock()
	defer c.cm.mu.RUnlock()

	for _, cf := range c.cm.chunks {
		if cf == c.cm.lastChunk {
			continue
		}
		cf.mu.Lock()
		fr, _ := c.getFillRate(cf)
		cf.mu.Unlock()
		if fr <= c.minFillRate {
			return true
		}
	}
	return false
}

// getFillRate returns the chunk fill rate: 1 = empty, 99 = full (Lealone integer math),
// and the total live size in bytes. Caller must hold cf.mu.
func (c *ChunkCompactor) getFillRate(cf *ChunkFile) (int, int64) {
	var totalLen, liveLen int64
	for pos, length := range cf.pagePosToLen {
		totalLen += int64(length)
		if _, removed := cf.removedPages[pos]; !removed {
			liveLen += int64(length)
		}
	}
	if totalLen == 0 {
		return 1, 0
	}
	return 1 + int(98*liveLen/totalLen), liveLen
}

// Compact performs one compaction cycle using the Lealone algorithm.
//
//  1. Collect all removedPages (global snapshot)
//  2. Skip if empty
//  3. Classify: fillRate==1 → unused; fillRate<=minFillRate → rewritable (skip lastChunk)
//  4. Sort rewritable by (fillRate asc, liveSize asc)
//  5. Greedy select: cumulative liveSize <= maxRewriteSize
//  6. Mark selected chunks compacting (write barrier)
//  7. Snapshot live pages per chunk (hold cf.mu, copy pagePosToLen - removedPages)
//  8. Rewrite live pages to new chunk
//  9. Sync new chunk
//  10. Clean cm.removedPages for old chunks
//  11. Delete old chunks (rename → .ao.deleting → os.Remove)
//  12. Clear compacting marks
func (c *ChunkCompactor) Compact() error {
	if !c.compacting.CompareAndSwap(false, true) {
		return nil // another compaction already in progress
	}
	defer c.compacting.Store(false)

	// Step 1: Collect global removedPages
	c.cm.mu.RLock()
	removed := make(map[model.ChunkPosition]struct{}, len(c.cm.removedPages))
	for pos := range c.cm.removedPages {
		removed[pos] = struct{}{}
	}
	c.cm.mu.RUnlock()

	// Step 2: Classify chunks (even if removed is empty — empty chunks need cleanup)
	type candidate struct {
		cf       *ChunkFile
		fillRate int
		liveSize int64
	}
	var unused []*ChunkFile
	var candidates []candidate

	c.cm.mu.RLock()
	for _, cf := range c.cm.chunks {
		if cf == c.cm.lastChunk {
			continue
		}
		cf.mu.Lock()
		fr, liveSize := c.getFillRate(cf)
		cf.mu.Unlock()

		if fr == 1 {
			unused = append(unused, cf)
		} else if fr <= c.minFillRate {
			candidates = append(candidates, candidate{cf: cf, fillRate: fr, liveSize: liveSize})
		}
	}
	c.cm.mu.RUnlock()

	if len(unused) == 0 && len(candidates) == 0 {
		return nil
	}

	// Step 4: Sort by fillRate asc, then liveSize asc
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].fillRate != candidates[j].fillRate {
			return candidates[i].fillRate < candidates[j].fillRate
		}
		return candidates[i].liveSize < candidates[j].liveSize
	})

	// Step 5: Greedy select
	var selected []*ChunkFile
	var cumulative int64
	for _, cand := range candidates {
		if cumulative+cand.liveSize > maxRewriteSize {
			break
		}
		selected = append(selected, cand.cf)
		cumulative += cand.liveSize
	}
	// Always include fully empty chunks
	selected = append(selected, unused...)

	// Step 6: Mark compacting (write barrier for WritePage)
	// (Simplified: mark via a flag; WritePage checks flag and rejects if set)
	// For Phase 4.4, we skip the write barrier since compaction runs post-checkpoint
	// when no concurrent writes target cold chunks.

	// Step 7-8: Snapshot + rewrite live pages to new chunk
	type pageToCopy struct {
		pos    model.ChunkPosition
		length int32
	}
	for _, cf := range selected {
		cf.mu.Lock()
		var pages []pageToCopy
		for pos, l := range cf.pagePosToLen {
			if _, rem := cf.removedPages[pos]; !rem {
				pages = append(pages, pageToCopy{pos: pos, length: l})
			}
		}
		cf.mu.Unlock()

		for _, p := range pages {
			data := make([]byte, p.length)
			if _, err := cf.file.ReadAt(data, int64(p.pos.FileOffset())); err != nil {
				return errpkg.Wrap(err, fmt.Sprintf("chunk: compact: read page %s", p.pos))
			}
			// Allocate + write in new chunk (lastChunk or new chunk)
			newPos, err := c.cm.Allocate(int(p.length), p.pos.PageType())
			if err != nil {
				return errpkg.Wrap(err, "chunk: compact: allocate")
			}
			if err := c.cm.WritePage(newPos, data); err != nil {
				return errpkg.Wrap(err, "chunk: compact: write page")
			}
		}
	}

	// Step 9: Sync new chunk
	if err := c.cm.Sync(); err != nil {
		return errpkg.Wrap(err, "chunk: compact: sync")
	}

	// Step 10-11: Delete old chunks + clean removedPages atomically
	for _, cf := range selected {
		// Lock ordering: cm.mu → cf.mu
		c.cm.mu.Lock()
		cf.mu.Lock()

		// Remove from manager first (prevents new FreePage on this chunk)
		delete(c.cm.idToChunk, cf.id)
		delete(c.cm.seqToID, cf.seq)
		for i, ch := range c.cm.chunks {
			if ch == cf {
				c.cm.chunks = append(c.cm.chunks[:i], c.cm.chunks[i+1:]...)
				break
			}
		}
		c.cm.chunkIDs.clear(cf.id)

		// Clean removedPages for this chunk (safe: idToChunk gone, lookupChunk fails)
		for pos := range cf.removedPages {
			delete(c.cm.removedPages, pos)
		}
		c.cm.mu.Unlock()

		// Rename → close → remove (cm.mu released, cf.mu still held)
		oldPath := cf.file.Name()
		deletingPath := oldPath + ".deleting"
		if err := os.Rename(oldPath, deletingPath); err != nil {
			cf.mu.Unlock()
			continue
		}
		cf.file.Close()
		_ = os.Remove(deletingPath)
		cf.mu.Unlock()
	}

	return nil
}
