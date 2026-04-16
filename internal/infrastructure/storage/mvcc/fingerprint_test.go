// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package mvcc

import (
	"testing"
)

func TestReadFingerprint_SameValue(t *testing.T) {
	val := []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x64, 0x01, 0x02}
	fp1 := NewReadFingerprint(val)
	fp2 := NewReadFingerprint(val)
	if fp1.ValueHash != fp2.ValueHash {
		t.Fatalf("same value should produce same hash: %d != %d", fp1.ValueHash, fp2.ValueHash)
	}
}

func TestReadFingerprint_DifferentValue(t *testing.T) {
	val1 := []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x64, 0x01}
	val2 := []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x64, 0x02}
	fp1 := NewReadFingerprint(val1)
	fp2 := NewReadFingerprint(val2)
	if fp1.ValueHash == fp2.ValueHash {
		t.Fatal("different values should produce different hashes")
	}
}

func TestReadFingerprint_EmptyValue(t *testing.T) {
	fp := NewReadFingerprint([]byte{})
	if fp.ValueHash == 0 {
		// FNV-1a of empty input is the offset basis (2166136261), not 0
		t.Fatal("empty value should produce non-zero hash")
	}
}
