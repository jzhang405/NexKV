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
	FlagNormal    byte = 0x00 // Normal data
	FlagTombstone byte = 0x01 // Logically deleted (tombstone marker)
)

// MVCCHeaderSize is the fixed header size for the new format: 1(Flag) + 1(prevFlag) + 8(prevBeginTS) + 2(prevValLen) = 12.
const MVCCHeaderSize = 12

// MVCCValue represents a decoded MVCC value with its metadata.
// The new format embeds the previous version inline, eliminating VersionChain traversal.
type MVCCValue struct {
	Flag    byte
	BeginTS uint64
	RealVal []byte // sub-slice of input; caller must copy if mutation needed

	// Embedded previous version (Phase 3: version-inline, eliminates VersionChain).
	// PrevBeginTS == 0 means no previous version (key was Insert'd).
	PrevFlag    byte
	PrevBeginTS uint64
	PrevVal     []byte // sub-slice of input; nil if PrevBeginTS == 0
}

// IsTombstone returns true if the value is a tombstone marker.
func (v *MVCCValue) IsTombstone() bool {
	return v.Flag == FlagTombstone
}

// ParseMVCC decodes the version-inline MVCC format:
//
//	[Flag:1][prevFlag:1][prevBeginTS:8][prevValLen:2][prevVal:N][beginTS:8][realVal:M]
//
// prevFlag is normalized to 0x00/0x01 (prevFlag & 0x01).
// prevBeginTS==0 means no previous version (Insert).
func ParseMVCC(val []byte) (*MVCCValue, error) {
	if len(val) < MVCCHeaderSize {
		return nil, errpkg.Wrap(ErrValueTooShort, fmt.Sprintf("got %d bytes, need %d", len(val), MVCCHeaderSize))
	}

	flag := val[0]
	if flag != FlagNormal && flag != FlagTombstone {
		return nil, errpkg.Wrap(ErrInvalidFlag, fmt.Sprintf("0x%02X", flag))
	}

	prevFlag := val[1] & 0x01 // normalize: only 0x00 or 0x01
	prevBeginTS := binary.BigEndian.Uint64(val[2:10])
	prevValLen := binary.BigEndian.Uint16(val[10:12])

	pos := uint16(12)
	var prevVal []byte
	if prevBeginTS != 0 && prevValLen > 0 {
		if int(pos+prevValLen) > len(val) {
			return nil, errpkg.Wrap(ErrValueTooShort, fmt.Sprintf("prevValLen=%d exceeds remaining bytes", prevValLen))
		}
		prevVal = val[pos : pos+prevValLen]
		pos += prevValLen
	}

	// Read current version: [beginTS:8][realVal]
	if int(pos+8) > len(val) {
		return nil, errpkg.Wrap(ErrValueTooShort, "missing beginTS for current version")
	}
	beginTS := binary.BigEndian.Uint64(val[pos : pos+8])
	pos += 8

	realVal := val[pos:]

	return &MVCCValue{
		Flag:        flag,
		BeginTS:     beginTS,
		RealVal:     realVal,
		PrevFlag:    prevFlag,
		PrevBeginTS: prevBeginTS,
		PrevVal:     prevVal,
	}, nil
}

// BuildMVCC encodes the version-inline MVCC format:
//
//	[Flag:1][prevFlag:1][prevBeginTS:8][prevValLen:2][prevVal:N][beginTS:8][realVal:M]
//
// prevFlag is normalized to 0x00/0x01 internally.
// For Insert (no previous version): pass prevFlag=0, prevBeginTS=0, prevVal=nil.
func BuildMVCC(flag byte, beginTS uint64, realVal []byte, prevFlag byte, prevBeginTS uint64, prevVal []byte) ([]byte, error) {
	if flag != FlagNormal && flag != FlagTombstone {
		return nil, errpkg.Wrap(ErrInvalidFlag, fmt.Sprintf("0x%02X", flag))
	}
	if beginTS == 0 {
		return nil, ErrZeroTimestamp
	}

	prevFlag = prevFlag & 0x01 // normalize
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
