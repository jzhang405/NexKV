// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package chunk

// ChunkCompactor rewrites low-fill-rate chunks to reclaim space.
// Aligns with Lealone ChunkCompactor.java. Full implementation deferred to Phase 5.
type ChunkCompactor struct {
	cm          *DiskChunkManager
	minFillRate int // minimum fill rate (default 30%, max 50%)
}

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

// NeedCompaction returns false in Phase 4 (stub).
func (c *ChunkCompactor) NeedCompaction() bool {
	return false
}

// Compact is a no-op in Phase 4 (stub, Phase 5 implements Lealone compaction algorithm).
func (c *ChunkCompactor) Compact() error {
	return nil
}
