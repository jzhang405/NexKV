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
func RecoverFromWAL(ctx context.Context, dw *DiskWAL, bt BTreeAccessor) (committedTxIDs map[uint64]uint64, replayedCount int, err error) {
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
		txPrepare *WALEntry
	}
	groups := make(map[uint64]*txGroup)
	for _, e := range entries {
		g, ok := groups[e.TxID]
		if !ok {
			g = &txGroup{}
			groups[e.TxID] = g
		}
		switch e.Type {
		case WALTypeCommit, WALTypeTxCommit:
			g.hasCommit = true
			// Phase 3.2 TxCommit Key=[txID:8][commitTS:8][entryCount:4]; old Commit Key=[commitTS:8]
			commitTSOff := 0
			if e.Type == WALTypeTxCommit && len(e.Key) >= 16 {
				commitTSOff = 8
			}
			if len(e.Key) >= commitTSOff+8 {
				g.commitTS = binary.BigEndian.Uint64(e.Key[commitTSOff:])
			}
		case WALTypeRollback, WALTypeTxRollback:
			g.hasCommit = false // explicit rollback
		case WALTypeTxPrepare:
			g.txPrepare = e
		case WALTypeTxBegin:
			// Phase 3.2: TxBegin marks transaction start. Full ActiveTxRegistry rebuild
			// (using Value=[beginTS:8]) deferred to Recovery Phase C extension.
		default:
			g.entries = append(g.entries, e)
		}
	}

	// Step 4: Replay committed transactions
	committedTxIDs = make(map[uint64]uint64) // txID → commitTS

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
			// Skip entries already covered by Checkpoint
			if replayStart != LSNInvalid && e.LSN < replayStart {
				continue
			}
			// Phase 1: Read current BTree state for idempotency
			raw, beginTS, getErr := bt.GetWithMeta(ctx, e.Key)
			keyExists := getErr == nil

			// Phase 2: Idempotency check
			if keyExists && beginTS > g.commitTS {
				continue // newer version exists, skip
			}
			if keyExists && beginTS == g.commitTS {
				continue // already applied, skip
			}
			// beginTS < commitTS or key not found: replay
			replaySingleKey(ctx, bt, e, g.commitTS, raw)
			replayedCount++
		}
	}

	// Phase 3.3: 2PC Rollback — transactions with TxPrepare but no TxCommit
	for _, g := range groups {
		if g.txPrepare == nil || g.hasCommit {
			continue
		}
		_, parsed, err := mvcc.ParseTxPrepareEntry(g.txPrepare)
		if err != nil {
			continue
		}
		for _, pe := range parsed {
			key := []byte(pe.Key)
			if pe.OldValue == nil && pe.OldFlag == mvcc.FlagTombstone {
				encoded, _ := mvcc.BuildMVCC(mvcc.FlagTombstone, g.commitTS, nil, 0, 0, nil)
				_ = bt.Set(ctx, key, encoded)
			} else if pe.OldValue != nil {
				_ = bt.Set(ctx, key, pe.OldValue)
			}
		}
	}
	return committedTxIDs, replayedCount, nil
}

// replaySingleKey replays a single WAL entry against BTree (version-inline, no VersionChain needed).
func replaySingleKey(ctx context.Context, bt BTreeAccessor, e *WALEntry, commitTS uint64, oldRaw []byte) {
	oldVal, oldFlag, oldBeginTS := extractOldValueWithTS(oldRaw)

	switch e.Type {
	case WALTypeInsert:
		encoded, _ := mvcc.BuildMVCC(mvcc.FlagNormal, commitTS, e.Value, 0, 0, nil)
		_ = bt.Set(ctx, e.Key, encoded)

	case WALTypeUpdate:
		encoded, _ := mvcc.BuildMVCC(mvcc.FlagNormal, commitTS, e.Value, oldFlag, oldBeginTS, oldVal)
		_ = bt.Set(ctx, e.Key, encoded)

	case WALTypeDelete:
		encoded, _ := mvcc.BuildMVCC(mvcc.FlagTombstone, commitTS, nil, oldFlag, oldBeginTS, oldVal)
		_ = bt.Set(ctx, e.Key, encoded)
	}
}

// extractOldValueWithTS extracts old value, flag, and beginTS from raw MVCC bytes.
func extractOldValueWithTS(raw []byte) ([]byte, byte, uint64) {
	if raw == nil {
		return nil, mvcc.FlagTombstone, 0
	}
	mv, err := mvcc.ParseMVCC(raw)
	if err != nil {
		return nil, mvcc.FlagTombstone, 0
	}
	return mv.RealVal, mv.Flag, mv.BeginTS
}

// BTreeAccessor is the minimal BTree interface needed for recovery.
type BTreeAccessor interface {
	GetWithMeta(ctx context.Context, key []byte) (raw []byte, beginTS uint64, err error)
	Set(ctx context.Context, key, value []byte) error
}
