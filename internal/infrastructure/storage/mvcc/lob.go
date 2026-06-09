// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package mvcc

import (
	"encoding/binary"
)

// LOBSizeThreshold is the byte size above which values are stored in overflow page chains.
// Values ≤ 2KB are stored inline in the BTree leaf page.
// Values > 2KB trigger LOB allocation via LOBManager.Allocate.
const LOBSizeThreshold = 2048

// LOBManager manages large object storage and lifecycle.
// The implementation uses overflow page chains (offheap.PageManager) for storage
// and epoch-based GC for safe reclamation.
type LOBManager interface {
	// Allocate stores a large value in overflow page chains and returns a LOB reference.
	// The returned LOBRef is embedded in the MVCC-encoded value via BuildMVCC.
	Allocate(data []byte) (LOBRef, error)

	// Read retrieves the full data for a LOB reference by walking the overflow page chain.
	Read(ref LOBRef) ([]byte, error)

	// Free releases all overflow pages in the chain referenced by ref.
	// Must only be called when no readers can access the chain (rollback or post-epoch GC).
	Free(ref LOBRef) error

	// Update replaces old LOB data with new data.
	// Frees the old overflow chain and allocates a new one.
	Update(data []byte, oldRef LOBRef) (LOBRef, error)
}

// =============================================================================
// ValueEncoder — LOB-aware MVCC encode/decode helpers
// =============================================================================

// EncodeValue encodes a value for storage in the BTree.
// If value exceeds LOBSizeThreshold and lobMgr is non-nil, the value is stored
// in overflow page chains and only the LOB reference is embedded in the BTree.
// Otherwise the value is stored inline via BuildMVCC as normal.
func EncodeValue(value []byte, beginTS uint64, prevFlag byte, prevBeginTS uint64, prevVal []byte, lobMgr LOBManager) ([]byte, error) {
	if lobMgr != nil && len(value) > LOBSizeThreshold {
		return encodeLOB(value, beginTS, prevFlag, prevBeginTS, prevVal, lobMgr)
	}
	return BuildMVCC(FlagNormal, beginTS, value, prevFlag, prevBeginTS, prevVal)
}

// EncodeDeleteValue encodes a tombstone for a key deletion.
// If the old value was a LOB, the tombstone carries the LOB reference for later GC.
// Preserving the LOB ref in the tombstone allows epoch GC to find and free overflow pages.
func EncodeDeleteValue(beginTS uint64, oldFlag byte, oldBeginTS uint64, oldVal []byte, prevFlag byte, prevBeginTS uint64, prevVal []byte) ([]byte, error) {
	flag := FlagTombstone
	newVal := []byte(nil)
	if oldFlag == FlagLOBNormal || oldFlag == FlagLOBTombstone {
		flag = FlagLOBTombstone
		newVal = oldVal // preserve LOB ref in tombstone for GC
	}
	return BuildMVCC(flag, beginTS, newVal, prevFlag, prevBeginTS, prevVal)
}

// DecodeValue parses an MVCC-encoded value and expands LOB references.
// If the parsed value contains a LOB reference and lobMgr is non-nil,
// RealVal is replaced with the expanded data from overflow pages.
func DecodeValue(raw []byte, lobMgr LOBManager) (MVCCValue, error) {
	mv, err := ParseMVCC(raw)
	if err != nil {
		return MVCCValue{}, err
	}
	if mv.LOB != nil && lobMgr != nil {
		expanded, err := lobMgr.Read(*mv.LOB)
		if err != nil {
			return MVCCValue{}, err
		}
		mv.RealVal = expanded
	}
	return mv, nil
}

// encodeLOB allocates overflow pages and builds a LOB-flagged MVCC encoding.
func encodeLOB(data []byte, beginTS uint64, prevFlag byte, prevBeginTS uint64, prevVal []byte, lobMgr LOBManager) ([]byte, error) {
	lobRef, err := lobMgr.Allocate(data)
	if err != nil {
		return nil, err
	}

	// Build LOB ref bytes: [lobRefLen:2][FirstPageID:4][TotalLen:4]
	lobRefBytes := make([]byte, 10)
	binary.BigEndian.PutUint16(lobRefBytes[0:2], 8) // lobRefLen = 8
	binary.BigEndian.PutUint32(lobRefBytes[2:6], lobRef.FirstPageID)
	binary.BigEndian.PutUint32(lobRefBytes[6:10], lobRef.TotalLen)

	return BuildMVCC(FlagLOBNormal, beginTS, lobRefBytes, prevFlag, prevBeginTS, prevVal)
}
