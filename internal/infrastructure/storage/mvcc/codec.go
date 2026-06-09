// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package mvcc

import (
	"encoding/binary"
	"fmt"

	errpkg "github.com/jzhang405/NexKV/pkg/errors"
)

// Value flag constants for MVCC-encoded values.
const (
	FlagNormal           byte = 0x00 // Normal data (inline)
	FlagTombstone        byte = 0x01 // Logically deleted (tombstone marker)
	FlagLOBNormal        byte = 0x02 // LOB Tier 1 — value stored in overflow page chain (mmap)
	FlagLOBTombstone     byte = 0x03 // LOB Tier 1 Tombstone (0x02 | 0x01)
	FlagLOBFile          byte = 0x04 // LOB Tier 2 — value stored in independent file (disk)
	FlagLOBFileTombstone byte = 0x05 // LOB Tier 2 Tombstone (0x04 | 0x01)
)

// LOBRef is a reference to a LOB stored in overflow page chains (Tier 1).
// The BTree leaf page stores only this 8-byte reference, not the actual data.
type LOBRef struct {
	FirstPageID uint32 // first overflow page ID in the chain
	TotalLen    uint32 // total original data length (max 4GB)
}

// LOBFileRef is a reference to a LOB stored as an independent file (Tier 2).
// Stored as 16 bytes in the BTree leaf page.
type LOBFileRef struct {
	LOBID    uint64 // unique LOB identifier (monotonic counter)
	TotalLen uint64 // original data length (max 16EB, limited by filesystem)
}

// MVCCHeaderSize is the fixed header size for the new format: 1(Flag) + 1(prevFlag) + 8(prevBeginTS) + 2(prevValLen) = 12.
const MVCCHeaderSize = 12

// MVCCValue represents a decoded MVCC value with its metadata.
type MVCCValue struct {
	Flag    byte
	BeginTS uint64
	RealVal []byte

	PrevFlag    byte
	PrevBeginTS uint64
	PrevVal     []byte

	LOB     *LOBRef     // non-nil when Flag is FlagLOBNormal or FlagLOBTombstone (Tier 1)
	LOBFile *LOBFileRef // non-nil when Flag is FlagLOBFile or FlagLOBFileTombstone (Tier 2)
}

// IsTombstone returns true if the value is a tombstone marker (includes all tombstone flags).
func (v *MVCCValue) IsTombstone() bool {
	return IsTombstoneFlag(v.Flag)
}

// IsTombstoneFlag returns true if the flag byte represents a tombstone.
// Bit 0 distinguishes normal (0x00, 0x02, 0x04) from tombstone (0x01, 0x03, 0x05).
func IsTombstoneFlag(flag byte) bool {
	return flag&0x01 == FlagTombstone
}

// ParseMVCC decodes the version-inline MVCC format:
//
//	[Flag:1][prevFlag:1][prevBeginTS:8][prevValLen:2][prevVal:N][beginTS:8][realVal:M]
//
// prevFlag is stored as raw value (0x00/0x01/0x02/0x03/0x04/0x05) to preserve LOB information.
// Use IsTombstoneFlag(prevFlag) instead of == FlagTombstone.
// prevBeginTS==0 means no previous version (Insert).
//
// Returns MVCCValue by value (stack-allocated when not escaping).
func ParseMVCC(val []byte) (MVCCValue, error) {
	if len(val) < MVCCHeaderSize {
		return MVCCValue{}, errpkg.Wrap(ErrValueTooShort, fmt.Sprintf("got %d bytes, need %d", len(val), MVCCHeaderSize))
	}

	flag := val[0]
	if flag != FlagNormal && flag != FlagTombstone &&
		flag != FlagLOBNormal && flag != FlagLOBTombstone &&
		flag != FlagLOBFile && flag != FlagLOBFileTombstone {
		return MVCCValue{}, errpkg.Wrap(ErrInvalidFlag, fmt.Sprintf("0x%02X", flag))
	}

	prevFlag := val[1] // raw flag — preserves LOB information for prev version expansion
	prevBeginTS := binary.BigEndian.Uint64(val[2:10])
	prevValLen := binary.BigEndian.Uint16(val[10:12])

	pos := uint16(12)
	var prevVal []byte
	if prevBeginTS != 0 && prevValLen > 0 {
		if int(pos+prevValLen) > len(val) {
			return MVCCValue{}, errpkg.Wrap(ErrValueTooShort, fmt.Sprintf("prevValLen=%d exceeds remaining bytes", prevValLen))
		}
		prevVal = val[pos : pos+prevValLen]
		pos += prevValLen
	}

	// Read current version: [beginTS:8][realVal]
	if int(pos+8) > len(val) {
		return MVCCValue{}, errpkg.Wrap(ErrValueTooShort, "missing beginTS for current version")
	}
	beginTS := binary.BigEndian.Uint64(val[pos : pos+8])
	pos += 8

	realVal := val[pos:]

	// Phase 6: if LOB, parse the LOB reference from realVal
	var lob *LOBRef
	var lobFile *LOBFileRef
	switch flag {
	case FlagLOBNormal, FlagLOBTombstone:
		// Tier 1: realVal = [lobRefLen:2][FirstPageID:4][TotalLen:4]
		if len(realVal) >= 10 {
			firstPageID := binary.BigEndian.Uint32(realVal[2:6])
			totalLen := binary.BigEndian.Uint32(realVal[6:10])
			lob = &LOBRef{FirstPageID: firstPageID, TotalLen: totalLen}
		}
	case FlagLOBFile, FlagLOBFileTombstone:
		// Tier 2: realVal = [lobRefLen:2][LOBID:8][TotalLen:8]
		if len(realVal) >= 18 {
			lobID := binary.BigEndian.Uint64(realVal[2:10])
			totalLen := binary.BigEndian.Uint64(realVal[10:18])
			lobFile = &LOBFileRef{LOBID: lobID, TotalLen: totalLen}
		}
	}

	return MVCCValue{
		Flag:        flag,
		BeginTS:     beginTS,
		RealVal:     realVal,
		PrevFlag:    prevFlag,
		PrevBeginTS: prevBeginTS,
		PrevVal:     prevVal,
		LOB:         lob,
		LOBFile:     lobFile,
	}, nil
}

// BuildMVCC encodes the version-inline MVCC format:
//
//	[Flag:1][prevFlag:1][prevBeginTS:8][prevValLen:2][prevVal:N][beginTS:8][realVal:M]
//
// prevFlag is stored as raw value (0x00/0x01/0x02/0x03/0x04/0x05) — no normalization.
// This preserves LOB Tier 1/2 information for prev version expansion.
// For Insert (no previous version): pass prevFlag=0, prevBeginTS=0, prevVal=nil.
func BuildMVCC(flag byte, beginTS uint64, realVal []byte, prevFlag byte, prevBeginTS uint64, prevVal []byte) ([]byte, error) {
	if flag != FlagNormal && flag != FlagTombstone &&
		flag != FlagLOBNormal && flag != FlagLOBTombstone &&
		flag != FlagLOBFile && flag != FlagLOBFileTombstone {
		return nil, errpkg.Wrap(ErrInvalidFlag, fmt.Sprintf("0x%02X", flag))
	}
	if beginTS == 0 {
		return nil, ErrZeroTimestamp
	}

	prevValLen := uint16(len(prevVal))
	totalSize := MVCCHeaderSize + int(prevValLen) + 8 + len(realVal)

	result := make([]byte, totalSize)
	result[0] = flag
	result[1] = prevFlag
	binary.BigEndian.PutUint64(result[2:10], prevBeginTS)
	binary.BigEndian.PutUint16(result[10:12], prevValLen)
	pos := 12
	if prevValLen > 0 {
		copy(result[pos:], prevVal)
		pos += int(prevValLen)
	}
	binary.BigEndian.PutUint64(result[pos:pos+8], beginTS)
	pos += 8
	copy(result[pos:], realVal)
	return result, nil
}
