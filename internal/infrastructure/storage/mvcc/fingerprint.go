// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package mvcc

import (
	"hash/fnv"
)

// ReadFingerprint is a per-key value hash recorded at read time.
// Used by PreCheck to detect if a key was modified between read and commit.
type ReadFingerprint struct {
	ValueHash uint32 // FNV-1a 32-bit hash of the complete MVCC Value
}

// NewReadFingerprint computes a fingerprint from a complete MVCC Value
// ([Flag][beginTS][RealValue]). Must use the full encoded value, not just RealVal,
// so that beginTS changes are detected.
func NewReadFingerprint(value []byte) ReadFingerprint {
	h := fnv.New32a()
	h.Write(value)
	return ReadFingerprint{ValueHash: h.Sum32()}
}
