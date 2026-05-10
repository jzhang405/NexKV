// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package wal

import "hash/crc32"

// crc32cTable is the Castagnoli polynomial table for CRC32C.
var crc32cTable = crc32.MakeTable(crc32.Castagnoli)

// CRC32C computes the Castagnoli CRC32 checksum.
// Castagnoli has hardware acceleration on x86 (crc32q) and ARM (crc32w).
func CRC32C(data []byte) uint32 {
	return crc32.Checksum(data, crc32cTable)
}

// trailerMagic is the WAL entry trailer: 0xDEADBEEF.
const trailerMagic uint32 = 0xDEADBEEF
