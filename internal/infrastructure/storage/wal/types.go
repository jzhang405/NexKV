// Package wal provides Write-Ahead Logging (WAL) for crash recovery.
package wal

import (
	"encoding/binary"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/service"
)

// LSN is a log sequence number (alias to service.LSN).
type LSN = service.LSN

const LSNInvalid LSN = 0

// WALType enumerates WAL entry types (alias to service.WALType).
type WALType = service.WALType

const (
	WALTypeInsert      = service.WALTypeInsert
	WALTypeUpdate      = service.WALTypeUpdate
	WALTypeDelete      = service.WALTypeDelete
	WALTypeCommit      = service.WALTypeCommit
	WALTypeRollback    = service.WALTypeRollback
	WALTypeCheckpoint  = service.WALTypeCheckpoint
	WALTypeSplit       = service.WALTypeSplit
	WALTypeCheckpointV2 = service.WALTypeCheckpointV2 // Phase 4
)

// WALTypeString returns the human-readable name of a WALType.
func WALTypeString(wt WALType) string {
	switch wt {
	case WALTypeInsert:
		return "Insert"
	case WALTypeUpdate:
		return "Update"
	case WALTypeDelete:
		return "Delete"
	case WALTypeCommit:
		return "Commit"
	case WALTypeRollback:
		return "Rollback"
	case WALTypeCheckpoint:
		return "Checkpoint"
	case WALTypeSplit:
		return "Split"
	case WALTypeCheckpointV2:
		return "CheckpointV2"
	default:
		return "Unknown"
	}
}

// WALEntry is a WAL log entry (alias to service.WALEntry).
//
// Phase 3 wire format:
//
//	[CRC32C:4][Length:4][LSN:8][Type:1][ShardID:2][Term:2][TxID:8]
//	[Timestamp:8][PrevLSN:8][KeyLen:4][ValueLen:4][Key:N][Value:M]
//	[Padding:0~7][Trailer:4(0xDEADBEEF)]
//
// CRC covers [Length:Padding]. Type=Commit encodes commitTS as big-endian
// uint64 in Key with KeyLen=8, ValueLen=0.
type WALEntry = service.WALEntry

// NewWALEntry creates a new WAL entry.
func NewWALEntry(entryType WALType, txID uint64, key, value []byte, prevLSN LSN) *WALEntry {
	return &WALEntry{
		TxID:      txID,
		Timestamp: time.Now().UnixMicro(),
		Type:      entryType,
		Key:       key,
		Value:     value,
		PrevLSN:   prevLSN,
	}
}

// MarshalWALEntry serializes the entry to the Phase 3 wire format.
func MarshalWALEntry(e *WALEntry) ([]byte, error) {
	keyLen := len(e.Key)
	valueLen := len(e.Value)

	// Payload: everything after Length through Value.
	payloadLen := 8 + 1 + 2 + 2 + 8 + 8 + 8 + 4 + 4 + keyLen + valueLen
	// Align to 8 bytes: (payloadLen + 7) &^ 7 = padded payload length.
	paddedLen := (payloadLen + 7) &^ 7
	paddingLen := paddedLen - payloadLen

	// Total: CRC + Length + padded payload + Trailer.
	totalLen := 4 + 4 + paddedLen + 4
	buf := make([]byte, totalLen)
	offset := 0

	// CRC placeholder (offset 0-3)
	offset += 4

	// Length (offset 4-7): number of bytes from LSN through Padding end.
	binary.BigEndian.PutUint32(buf[offset:], uint32(paddedLen))
	offset += 4

	// LSN (offset 8-15)
	binary.BigEndian.PutUint64(buf[offset:], uint64(e.LSN))
	offset += 8

	// Type (offset 16)
	buf[offset] = byte(e.Type)
	offset++

	// ShardID (offset 17-18)
	binary.BigEndian.PutUint16(buf[offset:], e.ShardID)
	offset += 2

	// Term (offset 19-20)
	binary.BigEndian.PutUint16(buf[offset:], e.Term)
	offset += 2

	// TxID (offset 21-28)
	binary.BigEndian.PutUint64(buf[offset:], e.TxID)
	offset += 8

	// Timestamp (offset 29-36)
	binary.BigEndian.PutUint64(buf[offset:], uint64(e.Timestamp))
	offset += 8

	// PrevLSN (offset 37-44)
	binary.BigEndian.PutUint64(buf[offset:], uint64(e.PrevLSN))
	offset += 8

	// KeyLen (offset 45-48)
	binary.BigEndian.PutUint32(buf[offset:], uint32(keyLen))
	offset += 4

	// ValueLen (offset 49-52)
	binary.BigEndian.PutUint32(buf[offset:], uint32(valueLen))
	offset += 4

	// Key
	if keyLen > 0 {
		copy(buf[offset:], e.Key)
		offset += keyLen
	}

	// Value
	if valueLen > 0 {
		copy(buf[offset:], e.Value)
		offset += valueLen
	}

	// Padding (zero-filled)
	for i := 0; i < paddingLen; i++ {
		buf[offset] = 0
		offset++
	}

	// Trailer
	binary.BigEndian.PutUint32(buf[offset:], trailerMagic)

	// CRC32C — covers [Length:Padding]
	crc := CRC32C(buf[4 : 4+4+paddedLen])
	binary.BigEndian.PutUint32(buf[0:], crc)

	return buf, nil
}

// UnmarshalWALEntry deserializes from the Phase 3 wire format.
func UnmarshalWALEntry(e *WALEntry, data []byte) error {
	if len(data) < 4+4+8+1+2+2+8+8+8+4+4+4 {
		return ErrWALEntryCorrupted
	}

	offset := 0

	// CRC (offset 0-3)
	crc := binary.BigEndian.Uint32(data[offset:])
	offset += 4

	// Length (offset 4-7)
	length := binary.BigEndian.Uint32(data[offset:])
	offset += 4

	// Verify CRC (covers [Length:Padding])
	paddedEnd := 4 + 4 + int(length)
	if len(data) < paddedEnd+4 {
		return ErrWALEntryCorrupted
	}
	if CRC32C(data[4:paddedEnd]) != crc {
		return ErrWALChecksumMismatch
	}

	// Verify trailer
	trailer := binary.BigEndian.Uint32(data[paddedEnd : paddedEnd+4])
	if trailer != trailerMagic {
		return ErrWALCorruptedTruncatedEntry
	}

	// LSN (offset 8-15)
	e.LSN = LSN(binary.BigEndian.Uint64(data[offset:]))
	offset += 8

	// Type (offset 16)
	e.Type = WALType(data[offset])
	offset++

	// ShardID (offset 17-18)
	e.ShardID = binary.BigEndian.Uint16(data[offset:])
	offset += 2

	// Term (offset 19-20)
	e.Term = binary.BigEndian.Uint16(data[offset:])
	offset += 2

	// TxID (offset 21-28)
	e.TxID = binary.BigEndian.Uint64(data[offset:])
	offset += 8

	// Timestamp (offset 29-36)
	e.Timestamp = int64(binary.BigEndian.Uint64(data[offset:]))
	offset += 8

	// PrevLSN (offset 37-44)
	e.PrevLSN = LSN(binary.BigEndian.Uint64(data[offset:]))
	offset += 8

	// KeyLen (offset 45-48)
	keyLen := int(binary.BigEndian.Uint32(data[offset:]))
	offset += 4

	// ValueLen (offset 49-52)
	valueLen := int(binary.BigEndian.Uint32(data[offset:]))
	offset += 4

	// Key
	if keyLen > 0 {
		e.Key = make([]byte, keyLen)
		copy(e.Key, data[offset:offset+keyLen])
		offset += keyLen
	} else {
		e.Key = nil
	}

	// Value
	if valueLen > 0 {
		e.Value = make([]byte, valueLen)
		copy(e.Value, data[offset:offset+valueLen])
		// offset += valueLen — not needed, we're done
	} else {
		e.Value = nil
	}

	return nil
}

// WALConfig holds WAL configuration.
type WALConfig struct {
	Dir         string
	SegmentSize int64
	SyncPolicy  SyncPolicy
}

type SyncPolicy int

const (
	SyncPolicyEveryWrite SyncPolicy = iota
	SyncPolicyEverySecond
	SyncPolicyBatch
	SyncPolicyGroupCommit // Phase 3: batch fsync with size+time triggers
)

// WALGroupCommitConfig tunes Group Commit behavior.
type WALGroupCommitConfig struct {
	PreferredBatchSize int   // default 16
	BatchTimeoutMs     int64 // default 1 (millisecond)
}

func DefaultGroupCommitConfig() *WALGroupCommitConfig {
	return &WALGroupCommitConfig{
		PreferredBatchSize: 16,
		BatchTimeoutMs:     1,
	}
}

func DefaultWALConfig() *WALConfig {
	return &WALConfig{
		SegmentSize: 64 * 1024 * 1024,
		SyncPolicy:  SyncPolicyEveryWrite,
	}
}

func (c *WALConfig) Validate() error {
	if c.Dir == "" {
		return ErrInvalidWALConfig
	}
	if c.SegmentSize < 1024*1024 {
		return ErrInvalidWALConfig
	}
	return nil
}
