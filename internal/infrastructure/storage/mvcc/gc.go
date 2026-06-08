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

// gcCycle executes one GC cycle (no-op since Phase 3 version-inline eliminates VersionChain).
// GC is now handled by BTree epoch-based page recycling.
func (tm *txManager) gcCycle() {
	start := time.Now()
	watermark := tm.activeTxRegistry.Watermark()
	if watermark == 0 {
		watermark = tm.tsGen.CurrentTS()
	}

	elapsed := time.Since(start)
	tm.gcStats.Cycles.Add(1)
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
