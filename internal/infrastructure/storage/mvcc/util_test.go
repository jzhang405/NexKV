// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package mvcc

import (
	"testing"
)

func TestDeepCopy_Nil(t *testing.T) {
	result := deepCopy(nil)
	if result != nil {
		t.Fatalf("expected nil, got %v", result)
	}
}

func TestDeepCopy_EmptySlice(t *testing.T) {
	result := deepCopy([]byte{})
	if result == nil {
		t.Fatal("expected non-nil empty slice")
	}
	if len(result) != 0 {
		t.Fatalf("expected empty slice, got len=%d", len(result))
	}
}

func TestDeepCopy_Normal(t *testing.T) {
	original := []byte{0x01, 0x02, 0x03}
	result := deepCopy(original)

	// Same contents
	if string(result) != string(original) {
		t.Fatalf("expected %v, got %v", original, result)
	}

	// Independent copy: modifying result does not affect original
	result[0] = 0xFF
	if original[0] == 0xFF {
		t.Fatal("deepCopy returned alias, not independent copy")
	}
}
