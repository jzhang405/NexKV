// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package mvcc

import (
	"context"
	"sync/atomic"
	"time"
)

// GCConfig defines the GC policy for version chain pruning.
type GCConfig struct {
	// Interval between GC cycles. Default: 5s.
	Interval time.Duration

	// MaxVersions limits the maximum versions to retain per key.
	// Prune marks older versions reclaimed when exceeded. Default: 10.
	MaxVersions int
}

// DefaultGCConfig returns the recommended GC configuration.
func DefaultGCConfig() *GCConfig {
	return &GCConfig{
		Interval:    5 * time.Second,
		MaxVersions: 10,
	}
}

// GCStats tracks GC metrics for monitoring.
type GCStats struct {
	Cycles         atomic.Uint64
	TotalReclaimed atomic.Uint64
	LastWatermark  atomic.Uint64
	LastDurationMs atomic.Int64
}

// runGC is the background GC goroutine.
// It periodically computes the watermark and prunes all version chains.
func (tm *txManager) runGC(ctx context.Context) {
	if tm.gcCfg == nil {
		return
	}

	ticker := time.NewTicker(tm.gcCfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			tm.gcCycle()

		case <-ctx.Done():
			return
		}
	}
}

// gcCycle executes one full GC cycle: compute watermark, prune all chains.
func (tm *txManager) gcCycle() {
	start := time.Now()

	watermark := tm.activeTxRegistry.Watermark()
	if watermark == 0 {
		// No active transactions: use current TS as watermark to reclaim
		// all versions except the chain head and required retention versions.
		// TSGenerator starts at 1, so watermark==0 safely means "no active txs".
		watermark = tm.tsGen.CurrentTS()
	}

	var totalReclaimed int
	tm.versionStore.Range(func(key string, chain *VersionChain) bool {
		reclaimed := chain.Prune(watermark)
		totalReclaimed += reclaimed
		// CAS-bump generation so snapshotGet detects the logical chain change
		for {
			cur := chain.head.Load()
			newHead := &chainHead{node: cur.node, generation: cur.generation + 1}
			if chain.head.CompareAndSwap(cur, newHead) {
				break
			}
		}
		return true
	})

	elapsed := time.Since(start)

	// Update stats
	tm.gcStats.Cycles.Add(1)
	tm.gcStats.TotalReclaimed.Add(uint64(totalReclaimed))
	tm.gcStats.LastWatermark.Store(watermark)
	tm.gcStats.LastDurationMs.Store(elapsed.Milliseconds())
}

// GCStatsSnapshot is a safe-to-copy snapshot of GC statistics.
type GCStatsSnapshot struct {
	Cycles         uint64
	TotalReclaimed uint64
	LastWatermark  uint64
	LastDurationMs int64
}

// GCStats returns a snapshot of the current GC statistics.
func (tm *txManager) GCStats() GCStatsSnapshot {
	return GCStatsSnapshot{
		Cycles:         tm.gcStats.Cycles.Load(),
		TotalReclaimed: tm.gcStats.TotalReclaimed.Load(),
		LastWatermark:  tm.gcStats.LastWatermark.Load(),
		LastDurationMs: tm.gcStats.LastDurationMs.Load(),
	}
}
