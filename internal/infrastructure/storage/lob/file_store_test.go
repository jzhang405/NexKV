// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package lob

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jzhang405/NexKV/internal/infrastructure/storage/mvcc"
)

// --- fd cache tests ---

func TestLobFDCache_GetMiss(t *testing.T) {
	c := newLobFDCache(4)
	f := c.get(42)
	if f != nil {
		t.Fatal("expected nil on cache miss")
	}
	stats := c.stats()
	if stats.Misses != 1 {
		t.Fatalf("expected 1 miss, got %d", stats.Misses)
	}
	if stats.Hits != 0 {
		t.Fatalf("expected 0 hits, got %d", stats.Hits)
	}
}

func TestLobFDCache_AddAndGet(t *testing.T) {
	c := newLobFDCache(4)
	tmp, err := os.CreateTemp("", "lob-fd-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	c.add(42, tmp)

	f := c.get(42)
	if f == nil {
		t.Fatal("expected fd on cache hit")
	}
	c.release(42) // release the get reference

	stats := c.stats()
	if stats.Hits != 1 {
		t.Fatalf("expected 1 hit, got %d", stats.Hits)
	}
	if stats.Size != 1 {
		t.Fatalf("expected size=1, got %d", stats.Size)
	}
}

func TestLobFDCache_ReleaseDecrementsRef(t *testing.T) {
	c := newLobFDCache(4)
	tmp, err := os.CreateTemp("", "lob-fd-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())

	c.add(42, tmp)

	// Multiple get calls increment refCount
	f1 := c.get(42)
	f2 := c.get(42)
	if f1 == nil || f2 == nil {
		t.Fatal("expected both gets to succeed")
	}

	// Release both
	c.release(42)
	c.release(42)

	// Remove should now succeed immediately (refCount == 0)
	c.remove(42)
	stats := c.stats()
	if stats.Size != 0 {
		t.Fatalf("expected size=0 after remove, got %d", stats.Size)
	}
}

func TestLobFDCache_RemoveWithActiveBorrower(t *testing.T) {
	c := newLobFDCache(4)
	tmp, err := os.CreateTemp("", "lob-fd-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())

	c.add(42, tmp)

	// Borrow the fd
	f := c.get(42)
	if f == nil {
		t.Fatal("expected fd on get")
	}

	// Remove while borrowed — fd should NOT be closed yet (deferred to release)
	c.remove(42)

	// Entry should be gone from entries but in pendingClose
	stats := c.stats()
	if stats.Size != 0 {
		t.Fatalf("expected size=0 after remove, got %d", stats.Size)
	}

	// The fd should still be usable (not closed)
	if _, err := f.Stat(); err != nil {
		t.Fatalf("fd should still be usable after remove with active borrower: %v", err)
	}

	// Release the borrower — should trigger deferred close
	c.release(42)

	// After release, fd should be closed
	if _, err := f.Stat(); err == nil {
		t.Fatal("expected fd to be closed after release")
	}
}

func TestLobFDCache_EvictTailWithRef(t *testing.T) {
	c := newLobFDCache(2)

	// Fill cache to capacity
	f1, _ := os.CreateTemp("", "lob-evict-1-*")
	defer os.Remove(f1.Name())
	f2, _ := os.CreateTemp("", "lob-evict-2-*")
	defer os.Remove(f2.Name())

	c.add(1, f1)
	c.add(2, f2)

	// Borrow f1 (tail in LRU)
	borrowed := c.get(1)
	if borrowed == nil {
		t.Fatal("expected fd on get")
	}

	// Add a third entry — should evict tail (f1), but f1 has active ref
	f3, _ := os.CreateTemp("", "lob-evict-3-*")
	defer os.Remove(f3.Name())
	c.add(3, f3)

	// f1 should still be usable (deferred close)
	if _, err := borrowed.Stat(); err != nil {
		t.Fatalf("evicted fd with active ref should still be usable: %v", err)
	}

	// Release — should close the deferred fd
	c.release(1)
}

func TestLobFDCache_Stats(t *testing.T) {
	c := newLobFDCache(4)

	// Miss
	c.get(1)

	// Add + Hit
	tmp, _ := os.CreateTemp("", "lob-stats-*")
	defer os.Remove(tmp.Name())
	c.add(1, tmp)
	c.get(1)
	c.release(1)

	stats := c.stats()
	if stats.Hits != 1 {
		t.Fatalf("expected 1 hit, got %d", stats.Hits)
	}
	if stats.Misses != 1 {
		t.Fatalf("expected 1 miss, got %d", stats.Misses)
	}
	if stats.Size != 1 {
		t.Fatalf("expected size=1, got %d", stats.Size)
	}
}

// --- file store integration tests ---

func TestNewLOBFileStore_RandomInit(t *testing.T) {
	dir := t.TempDir()
	store, err := newLOBFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// nextLOBID should have random high 32 bits (non-zero)
	id := store.nextLOBID.Load()
	if id == 0 {
		t.Fatal("nextLOBID should be non-zero after random init")
	}
	// High 32 bits should be non-zero (random)
	if id>>32 == 0 {
		t.Fatal("nextLOBID high 32 bits should be non-zero (random init)")
	}
}

func TestLOBFileStore_CreateAndRead(t *testing.T) {
	dir := t.TempDir()
	store, err := newLOBFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	data := []byte("hello LOB world")
	ref, err := store.Create(data)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if ref.TotalLen != uint64(len(data)) {
		t.Fatalf("expected TotalLen=%d, got %d", len(data), ref.TotalLen)
	}

	readBack, err := store.Read(ref)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if string(readBack) != string(data) {
		t.Fatalf("expected %q, got %q", data, readBack)
	}
}

func TestLOBFileStore_LargeFile(t *testing.T) {
	dir := t.TempDir()
	store, err := newLOBFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// > 64KB to trigger mmap path
	data := make([]byte, 100_000)
	for i := range data {
		data[i] = byte(i % 256)
	}

	ref, err := store.Create(data)
	if err != nil {
		t.Fatalf("Create large file failed: %v", err)
	}

	readBack, err := store.Read(ref)
	if err != nil {
		t.Fatalf("Read large file failed: %v", err)
	}
	if len(readBack) != len(data) {
		t.Fatalf("expected %d bytes, got %d", len(data), len(readBack))
	}
	for i := range data {
		if readBack[i] != data[i] {
			t.Fatalf("mismatch at byte %d: expected %d, got %d", i, data[i], readBack[i])
		}
	}
}

func TestLOBFileStore_Delete(t *testing.T) {
	dir := t.TempDir()
	store, err := newLOBFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ref, err := store.Create([]byte("to-be-deleted"))
	if err != nil {
		t.Fatal(err)
	}

	if err := store.Delete(ref); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, err = store.Read(ref)
	if err == nil {
		t.Fatal("expected error reading deleted file")
	}
}

func TestLOBFileStore_CleanupTmp(t *testing.T) {
	dir := t.TempDir()
	store, err := newLOBFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Create a leftover .tmp- file manually
	tmpDir := filepath.Join(dir, "00000", "00000")
	if err := os.MkdirAll(tmpDir, 0750); err != nil {
		t.Fatal(err)
	}
	tmpFile := filepath.Join(tmpDir, ".tmp-00000000000000000001")
	if err := os.WriteFile(tmpFile, []byte("orphan"), 0640); err != nil {
		t.Fatal(err)
	}

	// Also create a normal .lob file — should NOT be cleaned
	lobFile := filepath.Join(tmpDir, "00000000000000000001.lob")
	if err := os.WriteFile(lobFile, []byte("real"), 0640); err != nil {
		t.Fatal(err)
	}

	if err := store.CleanupTmp(); err != nil {
		t.Fatalf("CleanupTmp failed: %v", err)
	}

	// .tmp- file should be gone
	if _, err := os.Stat(tmpFile); !os.IsNotExist(err) {
		t.Fatal("expected .tmp- file to be removed")
	}
	// .lob file should remain
	if _, err := os.Stat(lobFile); err != nil {
		t.Fatal("expected .lob file to remain")
	}
}

func TestLOBFileStore_CleanupTmp_ShortFilename(t *testing.T) {
	dir := t.TempDir()
	store, err := newLOBFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Create files with very short names — should NOT panic
	shortDir := filepath.Join(dir, "00000", "00000")
	if err := os.MkdirAll(shortDir, 0750); err != nil {
		t.Fatal(err)
	}

	shortNames := []string{"a", "ab", ".tm", ".t"}
	for _, name := range shortNames {
		p := filepath.Join(shortDir, name)
		if err := os.WriteFile(p, []byte("short"), 0640); err != nil {
			t.Fatal(err)
		}
	}

	// CleanupTmp should NOT panic on short filenames
	if err := store.CleanupTmp(); err != nil {
		t.Fatalf("CleanupTmp failed on short filenames: %v", err)
	}

	// Short files should still exist (they don't match ".tmp-" prefix)
	for _, name := range shortNames {
		p := filepath.Join(shortDir, name)
		if _, err := os.Stat(p); os.IsNotExist(err) {
			t.Fatalf("short file %q should not be removed", name)
		}
	}
}

func TestLOBFileStore_CreateIncrementsLOBID(t *testing.T) {
	dir := t.TempDir()
	store, err := newLOBFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ref1, _ := store.Create([]byte("first"))
	ref2, _ := store.Create([]byte("second"))

	if ref2.LOBID <= ref1.LOBID {
		t.Fatalf("LOBID should be monotonically increasing: %d -> %d", ref1.LOBID, ref2.LOBID)
	}
}

func TestLOBFileStore_ReadNonExistent(t *testing.T) {
	dir := t.TempDir()
	store, err := newLOBFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	_, err = store.Read(mvcc.LOBFileRef{LOBID: 99999, TotalLen: 10})
	if err == nil {
		t.Fatal("expected error reading non-existent file")
	}
}
