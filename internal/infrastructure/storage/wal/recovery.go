// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package wal

import (
	"context"
	"encoding/binary"
	"sort"

	"github.com/jzhang405/NexKV/internal/infrastructure/storage/mvcc"
)

// RecoverFromWAL reads all WAL segments, reconstructs committed transactions,
// and replays them with three-phase idempotency checks.
//
// Returns the set of committed TxIDs and the total entries replayed.
func RecoverFromWAL(ctx context.Context, dw *DiskWAL, bt BTreeAccessor, vs *mvcc.VersionStore) (committedTxIDs map[uint64]uint64, replayedCount int, err error) {
	// Step 1: Scan all .wal segments and collect entries
	entries, err := dw.Recover()
	if err != nil {
		return nil, 0, err
	}

	// Step 2: Sort by LSN (required: cross-file global ordering)
	sort.Slice(entries, func(i, j int) bool { return entries[i].LSN < entries[j].LSN })

	// Step 2.5: Find latest Checkpoint entry and compute replay start LSN.
	// Entries with LSN < checkpointStartLSN are already covered by Checkpoint data.
	replayStart := LSNInvalid
	for _, e := range entries {
		if e.Type == WALTypeCheckpoint && len(e.Key) >= 12 {
			cksn := LSN(binary.BigEndian.Uint64(e.Key[0:8]))
			if cksn > replayStart {
				replayStart = cksn
			}
		}
	}

	// Step 3: Group by TxID, find committed transactions (those with Commit marker)
	type txGroup struct {
		entries   []*WALEntry
		commitTS  uint64
		hasCommit bool
	}
	groups := make(map[uint64]*txGroup)
	for _, e := range entries {
		g, ok := groups[e.TxID]
		if !ok {
			g = &txGroup{}
			groups[e.TxID] = g
		}
		switch e.Type {
		case WALTypeCommit:
			g.hasCommit = true
			if len(e.Key) >= 12 {
				g.commitTS = binary.BigEndian.Uint64(e.Key)
			}
		case WALTypeRollback:
			g.hasCommit = false // explicit rollback
		default:
			g.entries = append(g.entries, e)
		}
	}

	// Step 4: Replay committed transactions with three-phase idempotency
	committedTxIDs = make(map[uint64]uint64) // txID → commitTS
	dedup := newCommitTSDedupSet()

	for txID, g := range groups {
		select {
		case <-ctx.Done():
			return committedTxIDs, replayedCount, ctx.Err()
		default:
		}

		if !g.hasCommit {
			continue // discard uncommitted / rolled back
		}
		committedTxIDs[txID] = g.commitTS

		for _, e := range g.entries {
			// Skip entries already covered by Checkpoint (C1 CRITICAL: replay from checkpointStartLSN)
			if replayStart != LSNInvalid && e.LSN < replayStart {
				continue
			}
			// Phase 1: Read current BTree state
			raw, beginTS, getErr := bt.GetWithMeta(ctx, e.Key)
			keyExists := getErr == nil

			// Phase 2: Three-phase idempotency check
			if keyExists && beginTS > g.commitTS {
				continue // newer version exists, skip
			}
			if keyExists && beginTS == g.commitTS {
				// Check VersionChain — if node already exists, skip
				if dedup.AlreadyPrepended(string(e.Key), g.commitTS) {
					continue
				}
				// btree done, chain not done — just Prepend
				replaySingleKey(ctx, bt, vs, e, g.commitTS, raw, dedup)
				replayedCount++
				continue
			}
			// beginTS < commitTS or key not found: full replay
			replaySingleKey(ctx, bt, vs, e, g.commitTS, raw, dedup)
			replayedCount++
		}
	}

	return committedTxIDs, replayedCount, nil
}

// replaySingleKey replays a single WAL entry against BTree + VersionChain.
func replaySingleKey(ctx context.Context, bt BTreeAccessor, vs *mvcc.VersionStore, e *WALEntry, commitTS uint64, oldRaw []byte, dedup *commitTSDedupSet) {
	keyStr := string(e.Key)

	switch e.Type {
	case WALTypeInsert:
		// Insert: Mark BTree with MVCC-encoded value, Prepend Tombstone marker.
		encoded, _ := mvcc.BuildMVCC(mvcc.FlagNormal, commitTS, e.Value)
		_ = bt.Set(ctx, e.Key, encoded)
		if !dedup.AlreadyPrepended(keyStr, commitTS) {
			_ = vs.Prepend(keyStr, commitTS, nil, mvcc.FlagTombstone)
		}

	case WALTypeUpdate:
		// Update: Get new value from entry, old value from BTree or raw.
		encoded, _ := mvcc.BuildMVCC(mvcc.FlagNormal, commitTS, e.Value)
		_ = bt.Set(ctx, e.Key, encoded)
		if !dedup.AlreadyPrepended(keyStr, commitTS) {
			oldVal, oldFlag := extractOldValue(oldRaw)
			_ = vs.Prepend(keyStr, commitTS, oldVal, oldFlag)
		}

	case WALTypeDelete:
		// Delete: Write Tombstone to BTree, Prepend old value.
		encoded, _ := mvcc.BuildMVCC(mvcc.FlagTombstone, commitTS, nil)
		_ = bt.Set(ctx, e.Key, encoded)
		if !dedup.AlreadyPrepended(keyStr, commitTS) {
			oldVal, oldFlag := extractOldValue(oldRaw)
			_ = vs.Prepend(keyStr, commitTS, oldVal, oldFlag)
		}
	}
}

// extractOldValue extracts the old value and flag from raw MVCC-encoded bytes.
func extractOldValue(raw []byte) ([]byte, byte) {
	if raw == nil {
		return nil, mvcc.FlagTombstone
	}
	mv, err := mvcc.ParseMVCC(raw)
	if err != nil {
		return nil, mvcc.FlagTombstone
	}
	return deepCopySlice(mv.RealVal), mv.Flag
}

// BTreeAccessor is the minimal BTree interface needed for recovery.
type BTreeAccessor interface {
	GetWithMeta(ctx context.Context, key []byte) (raw []byte, beginTS uint64, err error)
	Set(ctx context.Context, key, value []byte) error
}

// commitTSDedupSet prevents duplicate Prepend during recovery.
type commitTSDedupSet struct {
	seen map[string]uint64 // key → max commitTS already prepended
}

func newCommitTSDedupSet() *commitTSDedupSet {
	return &commitTSDedupSet{seen: make(map[string]uint64)}
}

func (d *commitTSDedupSet) AlreadyPrepended(key string, commitTS uint64) bool {
	prev, ok := d.seen[key]
	if ok && prev >= commitTS {
		return true
	}
	d.seen[key] = commitTS
	return false
}

func deepCopySlice(src []byte) []byte {
	if src == nil {
		return nil
	}
	dst := make([]byte, len(src))
	copy(dst, src)
	return dst
}
