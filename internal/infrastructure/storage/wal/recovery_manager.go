// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package wal

import (
	"context"
	"encoding/binary"
	errpkg "github.com/jzhang405/NexKV/pkg/errors"
	"sort"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// RecoveryManager orchestrates crash recovery from WAL + AO chunk files (Phase 4.3).
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
func (rm *RecoveryManager) Recover(ctx context.Context, bt BTreeAccessor) (*RecoverResult, error) {
	entries, err := rm.wal.Recover()
	if err != nil {
		return nil, errpkg.Wrap(err, "recovery: scan WAL")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].LSN < entries[j].LSN })

	var checkpointStartLSN LSN
	var pageLocs map[model.PageID]model.ChunkPosition
	for _, e := range entries {
		if e.Type != WALTypeCheckpoint || len(e.Key) < 12 {
			continue
		}
		cksn := LSN(binary.BigEndian.Uint64(e.Key[0:8]))
		if cksn > checkpointStartLSN {
			checkpointStartLSN = cksn
			pageCount := binary.BigEndian.Uint32(e.Key[8:12])
			pageLocs = make(map[model.PageID]model.ChunkPosition, pageCount)
			for i, off := uint32(0), 12; i < pageCount && off+16 <= len(e.Key); i, off = i+1, off+16 {
				pid := model.PageID(binary.BigEndian.Uint64(e.Key[off : off+8]))
				cpos := model.ChunkPosition(binary.BigEndian.Uint64(e.Key[off+8 : off+16]))
				pageLocs[pid] = cpos
			}
		}
	}
	_ = pageLocs

	committedTxIDs, replayedCount, err := rm.replayWAL(ctx, entries, checkpointStartLSN, bt)
	if err != nil {
		return nil, errpkg.Wrap(err, "recovery: replay WAL")
	}

	return &RecoverResult{
		BTree:          bt,
		CommittedTxIDs: committedTxIDs,
		ReplayedCount:  replayedCount,
	}, nil
}

func (rm *RecoveryManager) replayWAL(ctx context.Context, entries []*WALEntry, replayStart LSN, bt BTreeAccessor) (map[uint64]uint64, int, error) {
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
			if getErr == nil && beginTS >= g.commitTS {
				continue // already applied or newer
			}
			replaySingleKey(ctx, bt, e, g.commitTS, raw)
			replayedCount++
		}
	}

	return committedTxIDs, replayedCount, nil
}
