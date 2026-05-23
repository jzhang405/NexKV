// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package service

import (
	"context"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// WAL defines the write-ahead logging interface for crash recovery.
// The interface lives in the domain layer (matching service.BTree as precedent).
// Implementations (DiskWAL) live in infrastructure/storage/wal/.
type WAL interface {
	Append(entry *WALEntry) (LSN, error)
	AppendBatch(entries []*WALEntry) ([]LSN, error)
	Sync() error
	Recover() ([]*WALEntry, error)
	Truncate(lsn LSN) error
	AppendAsync(ctx context.Context, entry *WALEntry) model.Task[LSN]
	TruncateAsync(ctx context.Context, lsn LSN) model.Task[struct{}]
	Close() error
	CurrentLSN() LSN
}

// LSN is a log sequence number.
type LSN uint64

// LSNInvalid is the zero-value for unassigned LSNs.
const LSNInvalid LSN = 0

// WALEntry is a WAL log entry. Type=Commit encodes commitTS as a big-endian
// uint64 in the Key field with KeyLen=8, ValueLen=0.
type WALEntry struct {
	LSN       LSN
	TxID      uint64
	Timestamp int64
	Type      WALType
	Key       []byte
	Value     []byte
	PrevLSN   LSN
	ShardID   uint16 // Phase 3 reserved, fixed at 0
	Term      uint16 // Phase 3 reserved, fixed at 0
}

// WALType enumerates WAL entry types.
type WALType uint8

const (
	WALTypeInsert WALType = iota
	WALTypeUpdate
	WALTypeDelete
	WALTypeCommit
	WALTypeRollback
	WALTypeCheckpoint // 5 — Phase 4: [startLSN:8][PageCount:4][(PageID:8,ChunkPos:8)*N]; PageCount=0 = no pageLocs
	WALTypeSplit      // 6
	WALTypeTxBegin    // 7 — Phase 3.2: Key=[txID:8]; Value=[beginTS:8]
	WALTypeTxWrite    // 8 — Phase 3.2: Key=[txID:8][key]; Value=[oldFlag:1][oldBeginTS:8][newFlag:1][newValue:N]
	WALTypeTxCommit   // 9 — Phase 3.2: Key=[txID:8][commitTS:8][entryCount:4]; Value=nil
	WALTypeTxRollback // 10 — Phase 3.2: Key=[txID:8]; Value=nil
	WALTypeTxPrepare  // 11 — Phase 3.3: Key=[txID:8]; Value=[keyCount:4][(keyLen:4)(key)(oldFlag:1)(oldBeginTS:8)(oldValLen:4)(oldVal)(newFlag:1)(newValLen:4)(newVal)]*N
)
