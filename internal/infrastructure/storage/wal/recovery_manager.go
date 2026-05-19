// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package wal

import (
	"context"
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/mvcc"
)

// RecoveryManager orchestrates crash recovery from WAL + AO chunk files (Phase 4.3).
//
// Three-phase recovery protocol:
//
//	Phase A: Infrastructure initialization
//	  1. ChunkManager RestoreDiskChunkManager (or NewDiskChunkManager)
//	  2. PageManager (empty mmap pool)
//	  3. WAL scan → find latest CheckpointEntry (Phase 3 or Phase 4 format)
//
//	Phase B: BTree structure rebuild (if CheckpointEntry exists)
//	  4. Parse checkpoint key → rootPageID + pageLocs mapping
//	  5. set pageLocs on OffheapBTreeStorage
//	  6. Create BTree with lazy-loaded pages
//
//	Phase C: Incremental WAL replay
//	  7. Replay WAL from checkpointStartLSN
//	  8. Rebuild VersionChain
//
// TODO(phase4.3): Add cm (ChunkManager) and serializer fields for Phase B BTree rebuild.
// Currently Recover takes a pre-built BTreeAccessor; full integration will create BTree
// from pageLocs using RestoreDiskChunkManager + OffheapBTreeStorage + UpdatePageLocs.
type RecoveryManager struct {
	wal *DiskWAL
}

// RecoverResult contains the result of recovery.
type RecoverResult struct {
	BTree          BTreeAccessor
	CommittedTxIDs map[uint64]uint64
	ReplayedCount  int
}

// Recover performs the three-phase recovery protocol.
func (rm *RecoveryManager) Recover(ctx context.Context, bt BTreeAccessor, vs *mvcc.VersionStore) (*RecoverResult, error) {
	// Phase A: WAL scan
	entries, err := rm.wal.Recover()
	if err != nil {
		return nil, fmt.Errorf("recovery: scan WAL: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].LSN < entries[j].LSN })

	// Phase A.3: Find latest checkpoint and parse pageLocs (Phase 4 format support)
	var checkpointStartLSN LSN

	var pageLocs map[model.PageID]model.ChunkPosition
	for _, e := range entries {
		if e.Type == WALTypeCheckpoint {
			if len(e.Key) >= 1 && e.Key[0] == 0x04 {
				// Phase 4 format: [FormatVersion:1][StartLSN:8][PageCount:4][(PageID:8,ChunkPos:8)*N]
				// rootPageID is implicitly the max PageID in pageLocs (root always allocated last)
				if len(e.Key) >= 13 {
					cksn := LSN(binary.BigEndian.Uint64(e.Key[1:9]))
					if cksn > checkpointStartLSN {
						checkpointStartLSN = cksn

						pageCount := binary.BigEndian.Uint32(e.Key[9:13])
						pageLocs = make(map[model.PageID]model.ChunkPosition, pageCount)
						offset := 13
						for i := uint32(0); i < pageCount && offset+16 <= len(e.Key); i++ {
							pid := model.PageID(binary.BigEndian.Uint64(e.Key[offset : offset+8]))
							cpos := model.ChunkPosition(binary.BigEndian.Uint64(e.Key[offset+8 : offset+16]))
							pageLocs[pid] = cpos
							offset += 16
						}
					}
				}
			} else if len(e.Key) == 8 {
				// Phase 3 format: [StartLSN:8]
				cksn := LSN(binary.BigEndian.Uint64(e.Key))
				if cksn > checkpointStartLSN {
					checkpointStartLSN = cksn
				}
			}
		}
	}

	_ = pageLocs
	_ = pageLocs

	// Phase C: Replay WAL (existing RecoverFromWAL logic, modified for Phase 4)
	committedTxIDs, replayedCount, err := rm.replayWAL(ctx, entries, checkpointStartLSN, bt, vs)
	if err != nil {
		return nil, fmt.Errorf("recovery: replay WAL: %w", err)
	}

	return &RecoverResult{
		BTree:          bt,
		CommittedTxIDs: committedTxIDs,
		ReplayedCount:  replayedCount,
	}, nil
}

// replayWAL replays committed transactions starting from checkpointStartLSN.
func (rm *RecoveryManager) replayWAL(ctx context.Context, entries []*WALEntry, replayStart LSN, bt BTreeAccessor, vs *mvcc.VersionStore) (map[uint64]uint64, int, error) {
	// Group by TxID
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
			if len(e.Key) == 8 {
				g.commitTS = binary.BigEndian.Uint64(e.Key)
			}
		case WALTypeRollback:
			g.hasCommit = false
		default:
			g.entries = append(g.entries, e)
		}
	}

	committedTxIDs := make(map[uint64]uint64)
	dedup := newCommitTSDedupSet()
	replayedCount := 0

	for txID, g := range groups {
		select {
		case <-ctx.Done():
			return committedTxIDs, replayedCount, ctx.Err()
		default:
		}
		if !g.hasCommit {
			continue
		}
		committedTxIDs[txID] = g.commitTS
		for _, e := range g.entries {
			if replayStart != LSNInvalid && e.LSN < replayStart {
				continue
			}
			raw, beginTS, getErr := bt.GetWithMeta(ctx, e.Key)
			keyExists := getErr == nil
			if keyExists && beginTS > g.commitTS {
				continue
			}
			if keyExists && beginTS == g.commitTS {
				if dedup.AlreadyPrepended(string(e.Key), g.commitTS) {
					continue
				}
				replaySingleKey(ctx, bt, vs, e, g.commitTS, raw, dedup)
				replayedCount++
				continue
			}
			replaySingleKey(ctx, bt, vs, e, g.commitTS, raw, dedup)
			replayedCount++
		}
	}

	return committedTxIDs, replayedCount, nil
}
