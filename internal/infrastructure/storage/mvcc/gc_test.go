// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package mvcc

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestActiveTxRegistry_RegisterUnregister(t *testing.T) {
	r := NewActiveTxRegistry()
	r.Register(1, 100)
	assert.Equal(t, 1, r.ActiveCount())

	r.Register(2, 200)
	assert.Equal(t, 2, r.ActiveCount())

	r.Unregister(1)
	assert.Equal(t, 1, r.ActiveCount())

	// Double unregister is safe
	r.Unregister(1)
	assert.Equal(t, 1, r.ActiveCount())
}

func TestActiveTxRegistry_Watermark(t *testing.T) {
	r := NewActiveTxRegistry()
	assert.Equal(t, uint64(0), r.Watermark()) // no active txs

	r.Register(1, 100)
	r.Register(2, 200)
	assert.Equal(t, uint64(100), r.Watermark())

	r.Unregister(1)
	assert.Equal(t, uint64(200), r.Watermark())
}

func TestActiveTxRegistry_Concurrent(t *testing.T) {
	r := NewActiveTxRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id uint64) {
			defer wg.Done()
			r.Register(id, id*10)
			time.Sleep(time.Microsecond)
			r.Unregister(id)
		}(uint64(i))
	}
	wg.Wait()
	assert.Equal(t, 0, r.ActiveCount())
}

func TestVersionChain_Prune(t *testing.T) {
	chain := &VersionChain{}

	// Build: head → V5(500) → V4(400,Tombstone) → V3(300) → V2(200) → V1(100)
	chain.Prepend(100, []byte("v1"), FlagNormal)
	chain.Prepend(200, []byte("v2"), FlagNormal)
	chain.Prepend(300, []byte("v3"), FlagNormal)
	chain.Prepend(400, nil, FlagTombstone)
	chain.Prepend(500, []byte("v5"), FlagNormal)

	// Prune with watermark=450: V5 retained (head), V4 retained (Tombstone before WM),
	// V3 retained (first non-Tombstone before Tombstone), V2 reclaimed, V1 reclaimed
	marked := chain.Prune(450)
	chain.generation.Add(1)
	assert.Equal(t, 2, marked)

	// Verify snapshotGet skips reclaimed
	head := chain.Load()
	require.NotNil(t, head)
	assert.Equal(t, uint64(500), head.commitTS)
	assert.False(t, head.reclaimed.Load())
	assert.False(t, head.rolledBack.Load())

	// V2, V1 should be reclaimed
	v2 := head.next.next.next // V3 → V2
	require.NotNil(t, v2)
	assert.Equal(t, uint64(200), v2.commitTS)
	assert.True(t, v2.reclaimed.Load())
}

func TestGC_RunCycle(t *testing.T) {
	tsGen := NewLocalTS()
	tm := NewTxManagerWithGC(nil, tsGen, DefaultGCConfig()).(*txManager)

	// Start GC in background with short interval
	gcCtx, gcCancel := context.WithCancel(context.Background())
	go tm.runGC(gcCtx)

	// Create a transaction to populate watermark
	tx1, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
	tx1.Commit(context.Background())

	// Trigger one GC cycle manually
	tm.gcCycle()

	stats := tm.GCStats()
	assert.Equal(t, uint64(1), stats.Cycles)

	// Cancel GC and wait for goroutine exit
	gcCancel()
	time.Sleep(50 * time.Millisecond)
}

func TestTSGenerator_CurrentTS(t *testing.T) {
	tsGen := NewLocalTS()
	assert.Equal(t, uint64(0), tsGen.CurrentTS())

	tsGen.NextTS()
	assert.Equal(t, uint64(1), tsGen.CurrentTS())

	tsGen.NextTS()
	assert.Equal(t, uint64(2), tsGen.CurrentTS())
}

// FuzzVersionChainPrune tests Prune + Prepend + snapshotGet concurrency safety.
func FuzzVersionChainPrune(f *testing.F) {
	f.Add(int(100), int(5))
	f.Fuzz(func(t *testing.T, wm int, np int) {
		if np < 1 || np > 50 {
			return
		}
		watermark := uint64(wm)
		numPrepends := np

		chain := &VersionChain{}
		for i := 0; i < numPrepends; i++ {
			chain.Prepend(uint64(100+10*i), []byte{byte(i)}, FlagNormal)
		}
		chain.Prune(watermark)
		chain.generation.Add(1)

		head := chain.Load()
		require.NotNil(t, head)
		assert.False(t, head.reclaimed.Load())
	})
}
