// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package mvcc

import (
	"encoding/binary"
)

// LOBSizeThreshold is the byte size above which values are stored in overflow page chains (Tier 1).
// Values ≤ threshold are stored inline. Default: 2048 (2KB). Can be overridden at init time.
var LOBSizeThreshold = 2048

// LOBFileThreshold is the byte size above which values are stored in independent files (Tier 2).
// Values ≤ threshold use overflow page chains (if > LOBSizeThreshold) or inline storage.
// Default: 65536 (64KB). Can be overridden at init time.
var LOBFileThreshold = 65536

// ---------------------------------------------------------------------------
// Tier 1: LOBManager (overflow page chain, 2KB ~ 64KB)
// ---------------------------------------------------------------------------

// LOBManager manages large object storage and lifecycle (Tier 1: overflow page chain).
type LOBManager interface {
	Allocate(data []byte) (LOBRef, error)
	Read(ref LOBRef) ([]byte, error)
	Free(ref LOBRef) error
}

// ---------------------------------------------------------------------------
// Tier 2: LOBFileManager (independent file, > 64KB)
// ---------------------------------------------------------------------------

// LOBFileManager manages large objects stored as independent files (Tier 2).
type LOBFileManager interface {
	// Create writes data to a new LOB file and returns its reference.
	// The file is fsync'd before returning — crash-safe via tmp+rename.
	Create(data []byte) (LOBFileRef, error)

	// Read reads the full data of a LOB file.
	// Uses mmap for files > mmap threshold, pread for smaller.
	Read(ref LOBFileRef) ([]byte, error)

	// Delete unlinks a LOB file. Called after epoch GC confirms no readers.
	Delete(ref LOBFileRef) error
}

// =============================================================================
// ValueEncoder — two-level LOB-aware MVCC encode/decode helpers
// =============================================================================

// EncodeValue encodes a value for storage in the BTree with two-level LOB routing:
//
//	len ≤ 2KB    → inline (FlagNormal)
//	2KB < len ≤ 64KB → overflow page chain (FlagLOBNormal, Tier 1)
//	len > 64KB   → LOB file (FlagLOBFile, Tier 2)
//
// lobMgr and lobFileMgr may be nil — if nil, the corresponding tier is disabled.
func EncodeValue(value []byte, beginTS uint64, prevFlag byte, prevBeginTS uint64, prevVal []byte, lobMgr LOBManager, lobFileMgr LOBFileManager) ([]byte, error) {
	// Tier 2: LOB File (>64KB)
	if lobFileMgr != nil && len(value) > LOBFileThreshold {
		return encodeLOBFile(value, beginTS, prevFlag, prevBeginTS, prevVal, lobFileMgr)
	}
	// Tier 1: Overflow Page (2KB ~ 64KB)
	if lobMgr != nil && len(value) > LOBSizeThreshold {
		return encodeLOBOverflow(value, beginTS, prevFlag, prevBeginTS, prevVal, lobMgr)
	}
	// Inline (<2KB)
	return BuildMVCC(FlagNormal, beginTS, value, prevFlag, prevBeginTS, prevVal)
}

// EncodeDeleteValue encodes a tombstone for a key deletion.
// Preserves LOB/LOBFile ref in tombstone for epoch GC to find and free external resources.
func EncodeDeleteValue(beginTS uint64, oldFlag byte, oldBeginTS uint64, oldVal []byte, prevFlag byte, prevBeginTS uint64, prevVal []byte) ([]byte, error) {
	flag := FlagTombstone
	newVal := []byte(nil)
	switch oldFlag {
	case FlagLOBNormal, FlagLOBTombstone: // IsLOBFlag
		flag = FlagLOBTombstone
		newVal = oldVal // preserve LOB ref for GC
	case FlagLOBFile, FlagLOBFileTombstone: // IsLOBFileFlag
		flag = FlagLOBFileTombstone
		newVal = oldVal // preserve LOB file ref for GC
	}
	return BuildMVCC(flag, beginTS, newVal, prevFlag, prevBeginTS, prevVal)
}

// DecodeValue parses an MVCC-encoded value and expands LOB references (both tiers).
// If the parsed value contains a LOB/LOBFile reference and the corresponding manager is non-nil,
// RealVal is replaced with the expanded data.
func DecodeValue(raw []byte, lobMgr LOBManager, lobFileMgr LOBFileManager) (MVCCValue, error) {
	mv, err := ParseMVCC(raw)
	if err != nil {
		return MVCCValue{}, err
	}
	// Tier 1 expansion
	if mv.LOB != nil && lobMgr != nil {
		expanded, err := lobMgr.Read(*mv.LOB)
		if err != nil {
			return MVCCValue{}, err
		}
		mv.RealVal = expanded
	}
	// Tier 2 expansion
	if mv.LOBFile != nil && lobFileMgr != nil {
		expanded, err := lobFileMgr.Read(*mv.LOBFile)
		if err != nil {
			return MVCCValue{}, err
		}
		mv.RealVal = expanded
	}
	return mv, nil
}

// =============================================================================
// internal encoding helpers
// =============================================================================

// encodeLOBOverflow allocates overflow pages and builds a FlagLOBNormal-flagged MVCC encoding.
func encodeLOBOverflow(data []byte, beginTS uint64, prevFlag byte, prevBeginTS uint64, prevVal []byte, lobMgr LOBManager) ([]byte, error) {
	lobRef, err := lobMgr.Allocate(data)
	if err != nil {
		return nil, err
	}
	lobRefBytes := make([]byte, 10)
	binary.BigEndian.PutUint16(lobRefBytes[0:2], 8) // lobRefLen = 8
	binary.BigEndian.PutUint32(lobRefBytes[2:6], lobRef.FirstPageID)
	binary.BigEndian.PutUint32(lobRefBytes[6:10], lobRef.TotalLen)
	return BuildMVCC(FlagLOBNormal, beginTS, lobRefBytes, prevFlag, prevBeginTS, prevVal)
}

// encodeLOBFile creates a LOB file and builds a FlagLOBFile-flagged MVCC encoding.
func encodeLOBFile(data []byte, beginTS uint64, prevFlag byte, prevBeginTS uint64, prevVal []byte, lobFileMgr LOBFileManager) ([]byte, error) {
	lobFileRef, err := lobFileMgr.Create(data)
	if err != nil {
		return nil, err
	}
	// Build LOB file ref bytes: [lobRefLen:2][LOBID:8][TotalLen:8]
	refBytes := make([]byte, 18)
	binary.BigEndian.PutUint16(refBytes[0:2], 16) // lobRefLen = 16
	binary.BigEndian.PutUint64(refBytes[2:10], lobFileRef.LOBID)
	binary.BigEndian.PutUint64(refBytes[10:18], lobFileRef.TotalLen)
	return BuildMVCC(FlagLOBFile, beginTS, refBytes, prevFlag, prevBeginTS, prevVal)
}
