// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package mvcc

import (
	errpkg "github.com/jzhang405/NexKV/pkg/errors"
	"encoding/binary"
)

// Value flag constants for MVCC-encoded values.
const (
	FlagNormal    byte = 0x00 // Normal data
	FlagTombstone byte = 0x01 // Logically deleted (tombstone marker)
)

// MVCCHeaderSize is the fixed header size for MVCC values: 1(Flag) + 8(beginTS) = 9 bytes.
const MVCCHeaderSize = 9

// MVCCValue represents a decoded MVCC value with its metadata.
type MVCCValue struct {
	Flag    byte
	BeginTS uint64
	// RealVal is a sub-slice of the byte array passed to ParseMVCC.
	// Callers must not modify it; make a copy if mutation is needed.
	RealVal []byte
}

// IsTombstone returns true if the value is a tombstone marker.
func (v *MVCCValue) IsTombstone() bool {
	return v.Flag == FlagTombstone
}

// ParseMVCC decodes a Phase 2 MVCC value: [1B Flag][8B beginTS][realVal].
// Returns ErrValueTooShort if the value is shorter than MVCCHeaderSize.
// Returns ErrInvalidFlag if the flag is not FlagNormal or FlagTombstone.
func ParseMVCC(val []byte) (*MVCCValue, error) {
	if len(val) < MVCCHeaderSize {
		return nil, errpkg.Wrapf(ErrValueTooShort, "got %d bytes, need %d", len(val), MVCCHeaderSize)
	}

	flag := val[0]
	if flag != FlagNormal && flag != FlagTombstone {
		return nil, errpkg.Wrapf(ErrInvalidFlag, "0x%02X", flag)
	}

	beginTS := binary.BigEndian.Uint64(val[1:9])

	return &MVCCValue{
		Flag:    flag,
		BeginTS: beginTS,
		RealVal: val[9:],
	}, nil
}

// BuildMVCC encodes a Phase 2 MVCC value: [1B Flag][8B beginTS][realVal].
// Returns ErrInvalidFlag if the flag is not FlagNormal or FlagTombstone.
// Returns ErrZeroTimestamp if beginTS is 0.
func BuildMVCC(flag byte, beginTS uint64, realVal []byte) ([]byte, error) {
	if flag != FlagNormal && flag != FlagTombstone {
		return nil, errpkg.Wrapf(ErrInvalidFlag, "0x%02X", flag)
	}
	if beginTS == 0 {
		return nil, ErrZeroTimestamp
	}

	result := make([]byte, MVCCHeaderSize+len(realVal))
	result[0] = flag
	binary.BigEndian.PutUint64(result[1:9], beginTS)
	copy(result[9:], realVal)
	return result, nil
}
