// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package chunk

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseHeader_Success(t *testing.T) {
	text := "id:42\nrootPagePos:deadbeef\npageCount:100\nformat:1\n"
	m, err := parseHeader(text)
	require.NoError(t, err)
	assert.Equal(t, "42", m["id"])
	assert.Equal(t, "deadbeef", m["rootPagePos"])
	assert.Equal(t, "100", m["pageCount"])
	assert.Equal(t, "1", m["format"])
}

func TestParseHeader_Empty(t *testing.T) {
	m, err := parseHeader("")
	require.NoError(t, err)
	assert.Empty(t, m)
}

func TestParseHeader_MalformedLine(t *testing.T) {
	_, err := parseHeader("id:1\nmalformed\npageCount:3")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "malformed header line")
}

func TestParseHeader_TrailingNewline(t *testing.T) {
	m, err := parseHeader("id:1\npageCount:2\n\n")
	require.NoError(t, err)
	assert.Equal(t, "1", m["id"])
	assert.Equal(t, "2", m["pageCount"])
}

func TestDecodeHeader_Success(t *testing.T) {
	h := &ChunkHeader{
		ID:            5,
		RootPagePos:   0xABCD,
		PageCount:     42,
		BlockSize:     ChunkBlockSize,
		FormatVersion: 1,
	}
	data := h.encode()
	decoded, err := decodeHeader(data)
	require.NoError(t, err)
	assert.Equal(t, h.ID, decoded.ID)
	assert.Equal(t, h.RootPagePos, decoded.RootPagePos)
	assert.Equal(t, h.PageCount, decoded.PageCount)
	assert.Equal(t, h.BlockSize, decoded.BlockSize)
	assert.Equal(t, h.FormatVersion, decoded.FormatVersion)
}

func TestDecodeHeader_BadBlockSize(t *testing.T) {
	data := []byte("id:1\nrootPagePos:0\nblockSize:1234\nformat:1\npageCount:0\nsumOfPageLength:0\nsumOfLivePageLength:0\npagePositionAndLengthOffset:0\nremovedPageOffset:0\nremovedPageCount:0\nlastTransactionId:0\nmapSize:0\n")
	_, err := decodeHeader(data)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported block size")
}

func TestDecodeHeader_BadFormat(t *testing.T) {
	_, err := decodeHeader([]byte("malformed"))
	require.Error(t, err)
}

func TestDecodeHeader_ParseError(t *testing.T) {
	data := []byte("id:notanumber\nblockSize:4096\nformat:1\n")
	_, err := decodeHeader(data)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse")
}

func TestParseUint32_Valid(t *testing.T) {
	v, err := parseUint32("42")
	require.NoError(t, err)
	assert.Equal(t, uint32(42), v)

	v, err = parseUint32("0")
	require.NoError(t, err)
	assert.Equal(t, uint32(0), v)
}

func TestParseUint32_Invalid(t *testing.T) {
	_, err := parseUint32("abc")
	require.Error(t, err)
	_, err = parseUint32("")
	require.Error(t, err)
}

func TestParseInt32_Valid(t *testing.T) {
	v, err := parseInt32("-1")
	require.NoError(t, err)
	assert.Equal(t, int32(-1), v)

	v, err = parseInt32("100")
	require.NoError(t, err)
	assert.Equal(t, int32(100), v)
}

func TestParseInt32_Invalid(t *testing.T) {
	_, err := parseInt32("")
	require.Error(t, err)
}

func TestParseInt64_Valid(t *testing.T) {
	v, err := parseInt64("1234567890123")
	require.NoError(t, err)
	assert.Equal(t, int64(1234567890123), v)

	v, err = parseInt64("-1")
	require.NoError(t, err)
	assert.Equal(t, int64(-1), v)
}

func TestParseInt64_Invalid(t *testing.T) {
	_, err := parseInt64("")
	require.Error(t, err)
}

func TestParseHexUint64_Valid(t *testing.T) {
	v, err := parseHexUint64("deadbeef")
	require.NoError(t, err)
	assert.Equal(t, uint64(0xDEADBEEF), v)

	v, err = parseHexUint64("0")
	require.NoError(t, err)
	assert.Equal(t, uint64(0), v)
}

func TestParseHexUint64_Invalid(t *testing.T) {
	_, err := parseHexUint64("")
	require.Error(t, err)
}

func TestEncode_Roundtrip(t *testing.T) {
	h := &ChunkHeader{
		ID:                          1,
		RootPagePos:                 0xCAFE,
		PageCount:                   256,
		SumOfPageLength:             1048576,
		SumOfLivePageLength:         524288,
		PagePositionAndLengthOffset: 8192,
		BlockSize:                   ChunkBlockSize,
		FormatVersion:               1,
		RemovedPageOffset:           1000000,
		RemovedPageCount:            12,
		LastTransactionID:           999,
		MapSize:                     5000,
	}
	decoded, err := decodeHeader(h.encode())
	require.NoError(t, err)
	assert.Equal(t, h.ID, decoded.ID)
	assert.Equal(t, h.RootPagePos, decoded.RootPagePos)
	assert.Equal(t, h.PageCount, decoded.PageCount)
	assert.Equal(t, h.SumOfPageLength, decoded.SumOfPageLength)
	assert.Equal(t, h.SumOfLivePageLength, decoded.SumOfLivePageLength)
	assert.Equal(t, h.PagePositionAndLengthOffset, decoded.PagePositionAndLengthOffset)
	assert.Equal(t, h.BlockSize, decoded.BlockSize)
	assert.Equal(t, h.FormatVersion, decoded.FormatVersion)
	assert.Equal(t, h.RemovedPageOffset, decoded.RemovedPageOffset)
	assert.Equal(t, h.RemovedPageCount, decoded.RemovedPageCount)
	assert.Equal(t, h.LastTransactionID, decoded.LastTransactionID)
	assert.Equal(t, h.MapSize, decoded.MapSize)
}
