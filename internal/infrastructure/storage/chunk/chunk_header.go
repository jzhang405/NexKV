// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package chunk

import (
	"fmt"
	"strconv"
	"strings"
)

// ChunkHeader is the text-encoded chunk metadata (Lealone Chunk header equivalent).
// Stored as key-value pairs in the dual-block header area.
type ChunkHeader struct {
	ID                            uint32 // chunk id
	RootPagePos                   uint64 // root page position (64-bit ChunkPosition)
	PageCount                     int32  // total page count
	SumOfPageLength               int64  // sum of all page lengths
	SumOfLivePageLength           int64  // sum of live (non-removed) page lengths
	PagePositionAndLengthOffset   int64  // offset of pagePosToLen map
	BlockSize                     int32  // always 4096
	FormatVersion                 int32  // format version
	RemovedPageOffset             int64  // offset of removedPages table
	RemovedPageCount              int32  // removed page count
	LastTransactionID             int64  // last transaction ID (WAL GC boundary)
	MapSize                       int64  // BTreeMap size
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

// parseHeader parses a key:value text into a map.
func parseHeader(text string) (map[string]string, error) {
	result := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("chunk: malformed header line: %q", line)
		}
		result[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	return result, nil
}

// decodeHeader parses the text header into a ChunkHeader.
func decodeHeader(data []byte) (*ChunkHeader, error) {
	m, err := parseHeader(string(data))
	if err != nil {
		return nil, err
	}
	h := &ChunkHeader{}
	h.ID = parseUint32(m["id"])
	h.RootPagePos = parseHexUint64(m["rootPagePos"])
	h.PageCount = parseInt32(m["pageCount"])
	h.SumOfPageLength = parseInt64(m["sumOfPageLength"])
	h.SumOfLivePageLength = parseInt64(m["sumOfLivePageLength"])
	h.PagePositionAndLengthOffset = parseInt64(m["pagePositionAndLengthOffset"])
	h.BlockSize = parseInt32(m["blockSize"])
	h.FormatVersion = parseInt32(m["format"])
	h.RemovedPageOffset = parseInt64(m["removedPageOffset"])
	h.RemovedPageCount = parseInt32(m["removedPageCount"])
	h.LastTransactionID = parseInt64(m["lastTransactionId"])
	h.MapSize = parseInt64(m["mapSize"])

	if h.BlockSize != ChunkBlockSize {
		return nil, fmt.Errorf("chunk: unsupported block size %d", h.BlockSize)
	}
	return h, nil
}

func parseUint32(s string) uint32 {
	v, _ := strconv.ParseUint(s, 10, 32)
	return uint32(v)
}

func parseInt32(s string) int32 {
	v, _ := strconv.ParseInt(s, 10, 32)
	return int32(v)
}

func parseInt64(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

func parseHexUint64(s string) uint64 {
	v, _ := strconv.ParseUint(s, 16, 64)
	return v
}
