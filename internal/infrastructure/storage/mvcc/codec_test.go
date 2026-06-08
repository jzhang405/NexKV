// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package mvcc

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMVCCHeaderSize(t *testing.T) {
	assert.Equal(t, 12, MVCCHeaderSize, "MVCCHeaderSize = 1(Flag) + 1(prevFlag) + 8(prevBeginTS) + 2(prevValLen) = 12 bytes")
}

func TestMVCCValue_IsTombstone(t *testing.T) {
	v := &MVCCValue{Flag: FlagTombstone}
	assert.True(t, v.IsTombstone())

	v2 := &MVCCValue{Flag: FlagNormal}
	assert.False(t, v2.IsTombstone())
}

func TestParseMVCC_Normal(t *testing.T) {
	raw, err := BuildMVCC(FlagNormal, 42, []byte("hello"), 0, 0, nil)
	require.NoError(t, err)

	val, err := ParseMVCC(raw)
	require.NoError(t, err)
	assert.Equal(t, FlagNormal, val.Flag)
	assert.Equal(t, uint64(42), val.BeginTS)
	assert.Equal(t, []byte("hello"), val.RealVal)
}

func TestParseMVCC_Tombstone(t *testing.T) {
	raw, err := BuildMVCC(FlagTombstone, 100, nil, 0, 0, nil)
	require.NoError(t, err)

	val, err := ParseMVCC(raw)
	require.NoError(t, err)
	assert.Equal(t, FlagTombstone, val.Flag)
	assert.Equal(t, uint64(100), val.BeginTS)
	assert.Empty(t, val.RealVal)
}

func TestParseMVCC_EmptyRealVal(t *testing.T) {
	raw, err := BuildMVCC(FlagNormal, 1, nil, 0, 0, nil)
	require.NoError(t, err)

	val, err := ParseMVCC(raw)
	require.NoError(t, err)
	assert.Equal(t, FlagNormal, val.Flag)
	assert.Equal(t, uint64(1), val.BeginTS)
	assert.Empty(t, val.RealVal)
	assert.Equal(t, MVCCHeaderSize+8, len(raw)) // header(12) + beginTS(8) = 20 bytes minimum
}

func TestParseMVCC_ValueTooShort(t *testing.T) {
	shortVals := [][]byte{
		{},
		{0x00},
		{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07},
	}
	for _, v := range shortVals {
		_, err := ParseMVCC(v)
		assert.ErrorIs(t, err, ErrValueTooShort, "value shorter than MVCCHeaderSize should return ErrValueTooShort")
	}
}

func TestParseMVCC_InvalidFlag(t *testing.T) {
	raw, err := BuildMVCC(FlagNormal, 1, []byte("data"), 0, 0, nil)
	require.NoError(t, err)
	// Corrupt flag byte
	raw[0] = 0xFF

	_, err = ParseMVCC(raw)
	assert.ErrorIs(t, err, ErrInvalidFlag)
}

func TestBuildMVCC_InvalidFlag(t *testing.T) {
	_, err := BuildMVCC(0xFF, 1, []byte("data"), 0, 0, nil)
	assert.ErrorIs(t, err, ErrInvalidFlag)
}

func TestBuildMVCC_ZeroTimestamp(t *testing.T) {
	_, err := BuildMVCC(FlagNormal, 0, []byte("data"), 0, 0, nil)
	assert.ErrorIs(t, err, ErrZeroTimestamp)
}

func TestBuildMVCC_RoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		flag    byte
		beginTS uint64
		realVal []byte
	}{
		{"Normal with data", FlagNormal, 42, []byte("hello")},
		{"Normal with nil", FlagNormal, 1, nil},
		{"Tombstone", FlagTombstone, 100, nil},
		{"Large data", FlagNormal, 999, make([]byte, 4096)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			built, err := BuildMVCC(tc.flag, tc.beginTS, tc.realVal, 0, 0, nil)
			require.NoError(t, err)
			assert.Equal(t, MVCCHeaderSize+8+len(tc.realVal), len(built)) // header(12) + beginTS(8) + realVal

			val, err := ParseMVCC(built)
			require.NoError(t, err)
			assert.Equal(t, tc.flag, val.Flag)
			assert.Equal(t, tc.beginTS, val.BeginTS)
			assert.Equal(t, len(tc.realVal), len(val.RealVal))
			if len(tc.realVal) > 0 {
				assert.Equal(t, tc.realVal, val.RealVal)
			}
		})
	}
}

func TestSentinelErrors_AreDistinct(t *testing.T) {
	sentinels := []error{ErrValueTooShort, ErrInvalidFlag, ErrZeroTimestamp}
	for i := range len(sentinels) {
		for j := i + 1; j < len(sentinels); j++ {
			assert.NotEqual(t, sentinels[i], sentinels[j])
		}
		assert.True(t, errors.Is(sentinels[i], sentinels[i]))
	}
}
