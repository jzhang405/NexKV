// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package chunk

import (
	errpkg "github.com/jzhang405/NexKV/pkg/errors"
	"fmt"
	"strconv"
	"strings"
)

// ChunkHeader is the text-encoded chunk metadata (Lealone Chunk header equivalent).
// Stored as key-value pairs in the dual-block header area.
type ChunkHeader struct {
	ID                          uint32 // chunk id
	RootPagePos                 uint64 // root page position (64-bit ChunkPosition)
	PageCount                   int32  // total page count
	SumOfPageLength             int64  // sum of all page lengths
	SumOfLivePageLength         int64  // sum of live (non-removed) page lengths
	PagePositionAndLengthOffset int64  // offset of pagePosToLen map
	BlockSize                   int32  // always 4096
	FormatVersion               int32  // format version
	RemovedPageOffset           int64  // offset of removedPages table
	RemovedPageCount            int32  // removed page count
	LastTransactionID           int64  // last transaction ID (WAL GC boundary)
	MapSize                     int64  // BTreeMap size
}

// encode serializes the header as newline-separated key:value text.
func (h *ChunkHeader) encode() []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "id:%d\n", h.ID)
	fmt.Fprintf(&b, "rootPagePos:%x\n", h.RootPagePos)
	fmt.Fprintf(&b, "pageCount:%d\n", h.PageCount)
	fmt.Fprintf(&b, "sumOfPageLength:%d\n", h.SumOfPageLength)
	fmt.Fprintf(&b, "sumOfLivePageLength:%d\n", h.SumOfLivePageLength)
	fmt.Fprintf(&b, "pagePositionAndLengthOffset:%d\n", h.PagePositionAndLengthOffset)
	fmt.Fprintf(&b, "blockSize:%d\n", h.BlockSize)
	fmt.Fprintf(&b, "format:%d\n", h.FormatVersion)
	fmt.Fprintf(&b, "removedPageOffset:%d\n", h.RemovedPageOffset)
	fmt.Fprintf(&b, "removedPageCount:%d\n", h.RemovedPageCount)
	fmt.Fprintf(&b, "lastTransactionId:%d\n", h.LastTransactionID)
	fmt.Fprintf(&b, "mapSize:%d\n", h.MapSize)
	return []byte(b.String())
}

func parseHeader(text string) (map[string]string, error) {
	text = strings.TrimRight(text, "\x00")
	result := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			return nil, errpkg.Wrapf(ErrChunkHeaderError, "chunk: malformed header line: %q", line)
		}
		result[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	return result, nil
}

func decodeHeader(data []byte) (*ChunkHeader, error) {
	m, err := parseHeader(string(data))
	if err != nil {
		return nil, err
	}
	h := &ChunkHeader{}

	if e := parseField(m, "id", &h.ID, parseUint32); e != nil {
		return nil, e
	}
	if e := parseField(m, "rootPagePos", &h.RootPagePos, parseHexUint64); e != nil {
		return nil, e
	}
	if e := parseField(m, "pageCount", &h.PageCount, parseInt32); e != nil {
		return nil, e
	}
	if e := parseField(m, "sumOfPageLength", &h.SumOfPageLength, parseInt64); e != nil {
		return nil, e
	}
	if e := parseField(m, "sumOfLivePageLength", &h.SumOfLivePageLength, parseInt64); e != nil {
		return nil, e
	}
	if e := parseField(m, "pagePositionAndLengthOffset", &h.PagePositionAndLengthOffset, parseInt64); e != nil {
		return nil, e
	}
	if e := parseField(m, "blockSize", &h.BlockSize, parseInt32); e != nil {
		return nil, e
	}
	if e := parseField(m, "format", &h.FormatVersion, parseInt32); e != nil {
		return nil, e
	}
	if e := parseField(m, "removedPageOffset", &h.RemovedPageOffset, parseInt64); e != nil {
		return nil, e
	}
	if e := parseField(m, "removedPageCount", &h.RemovedPageCount, parseInt32); e != nil {
		return nil, e
	}
	if e := parseField(m, "lastTransactionId", &h.LastTransactionID, parseInt64); e != nil {
		return nil, e
	}
	if e := parseField(m, "mapSize", &h.MapSize, parseInt64); e != nil {
		return nil, e
	}

	if h.BlockSize != ChunkBlockSize {
		return nil, errpkg.Wrapf(ErrChunkHeaderError, "chunk: unsupported block size %d", h.BlockSize)
	}
	return h, nil
}

// parseField is a generic parse-and-set helper that reduces repetitive error handling in decodeHeader.
func parseField[T any](m map[string]string, key string, dst *T, fn func(string) (T, error)) error {
	v, err := fn(m[key])
	if err != nil {
		return err
	}
	*dst = v
	return nil
}

func parseUint32(s string) (uint32, error) {
	v, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, errpkg.Wrapf(err, "chunk: parse uint32 %q", s)
	}
	return uint32(v), nil
}

func parseInt32(s string) (int32, error) {
	v, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return 0, errpkg.Wrapf(err, "chunk: parse int32 %q", s)
	}
	return int32(v), nil
}

func parseInt64(s string) (int64, error) {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, errpkg.Wrapf(err, "chunk: parse int64 %q", s)
	}
	return v, nil
}

func parseHexUint64(s string) (uint64, error) {
	v, err := strconv.ParseUint(s, 16, 64)
	if err != nil {
		return 0, errpkg.Wrapf(err, "chunk: parse hex uint64 %q", s)
	}
	return v, nil
}
