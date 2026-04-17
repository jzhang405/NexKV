// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package mvcc

import (
	"testing"
)

func TestWriteBuffer_Put_Insert(t *testing.T) {
	wb := NewWriteBuffer()
	wb.Put("k1", []byte("v1"), nil, 0, 0)

	entry, ok := wb.Get("k1")
	if !ok {
		t.Fatal("expected entry for k1")
	}
	if entry.Op != OpInsert {
		t.Fatalf("expected OpInsert, got %d", entry.Op)
	}
	if string(entry.Value) != "v1" {
		t.Fatalf("expected value=v1, got %s", entry.Value)
	}
	if entry.OldValue != nil {
		t.Fatalf("expected nil OldValue for OpInsert, got %v", entry.OldValue)
	}
}

func TestWriteBuffer_Put_Update(t *testing.T) {
	wb := NewWriteBuffer()
	wb.Put("k1", []byte("new"), []byte("old"), FlagNormal, 100)

	entry, ok := wb.Get("k1")
	if !ok {
		t.Fatal("expected entry for k1")
	}
	if entry.Op != OpUpdate {
		t.Fatalf("expected OpUpdate, got %d", entry.Op)
	}
	if string(entry.Value) != "new" {
		t.Fatalf("expected value=new, got %s", entry.Value)
	}
	if string(entry.OldValue) != "old" {
		t.Fatalf("expected OldValue=old, got %s", entry.OldValue)
	}
	if entry.OldBeginTS != 100 {
		t.Fatalf("expected OldBeginTS=100, got %d", entry.OldBeginTS)
	}
}

func TestWriteBuffer_Put_MultipleMerge(t *testing.T) {
	wb := NewWriteBuffer()
	wb.Put("k1", []byte("v1"), []byte("old"), FlagNormal, 100)
	wb.Put("k1", []byte("v2"), nil, 0, 0) // OldValue should not change
	wb.Put("k1", []byte("v3"), nil, 0, 0)

	entry, _ := wb.Get("k1")
	if string(entry.Value) != "v3" {
		t.Fatalf("expected value=v3, got %s", entry.Value)
	}
	if string(entry.OldValue) != "old" {
		t.Fatalf("OldValue should be preserved as 'old', got %s", entry.OldValue)
	}
	if entry.OldBeginTS != 100 {
		t.Fatalf("OldBeginTS should be preserved as 100, got %d", entry.OldBeginTS)
	}
}

func TestWriteBuffer_Delete_FromEmpty(t *testing.T) {
	wb := NewWriteBuffer()
	err := wb.Delete("k1", nil, 0, 0)
	if err != ErrKeyNotFound {
		t.Fatalf("expected ErrKeyNotFound, got %v", err)
	}
}

func TestWriteBuffer_Delete_InsertCancel(t *testing.T) {
	wb := NewWriteBuffer()
	wb.Put("k1", []byte("v1"), nil, 0, 0) // OpInsert
	err := wb.Delete("k1", nil, 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, ok := wb.Get("k1")
	if ok {
		t.Fatal("expected k1 to be removed after Insert→Delete")
	}
}

func TestWriteBuffer_Delete_UpdateMark(t *testing.T) {
	wb := NewWriteBuffer()
	wb.Put("k1", []byte("v1"), []byte("old"), FlagNormal, 100) // OpUpdate
	err := wb.Delete("k1", nil, 0, 0)                          // uses WB OldValue, not btreeOldValue
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	entry, ok := wb.Get("k1")
	if !ok {
		t.Fatal("expected entry for k1 after Update→Delete")
	}
	if entry.Op != OpDelete {
		t.Fatalf("expected OpDelete, got %d", entry.Op)
	}
	if string(entry.OldValue) != "old" {
		t.Fatalf("OldValue should be preserved as 'old', got %s", entry.OldValue)
	}
}

func TestWriteBuffer_Delete_Idempotent(t *testing.T) {
	wb := NewWriteBuffer()
	// Delete from B+Tree state (not in WB)
	err := wb.Delete("k1", []byte("old"), FlagNormal, 100)
	if err != nil {
		t.Fatalf("first delete failed: %v", err)
	}
	// Second delete: entry already OpDelete in WB
	err = wb.Delete("k1", nil, 0, 0) // WB has the entry, OpDelete stays OpDelete
	if err != nil {
		t.Fatalf("second delete failed: %v", err)
	}
	entry, _ := wb.Get("k1")
	if entry.Op != OpDelete {
		t.Fatalf("expected OpDelete after idempotent delete, got %d", entry.Op)
	}
}

func TestWriteBuffer_PutAfterDelete(t *testing.T) {
	wb := NewWriteBuffer()
	wb.Put("k1", []byte("v1"), []byte("old"), FlagNormal, 100) // OpUpdate
	wb.Delete("k1", nil, 0, 0)                                 // → OpDelete
	wb.Put("k1", []byte("v2"), nil, 0, 0)                      // → restore to OpUpdate (has OldValue)

	entry, _ := wb.Get("k1")
	if entry.Op != OpUpdate {
		t.Fatalf("expected OpUpdate after Delete→Put, got %d", entry.Op)
	}
	if string(entry.Value) != "v2" {
		t.Fatalf("expected value=v2, got %s", entry.Value)
	}
	if string(entry.OldValue) != "old" {
		t.Fatalf("OldValue should still be 'old', got %s", entry.OldValue)
	}
}

func TestWriteBuffer_OrderedKeys(t *testing.T) {
	wb := NewWriteBuffer()
	wb.Put("c", []byte("1"), nil, 0, 0)
	wb.Put("a", []byte("2"), nil, 0, 0)
	wb.Put("b", []byte("3"), nil, 0, 0)

	keys := wb.OrderedKeys()
	expected := []string{"c", "a", "b"}
	for i, k := range expected {
		if keys[i] != k {
			t.Fatalf("expected keys[%d]=%s, got %s", i, k, keys[i])
		}
	}
}
