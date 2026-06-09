// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package lob

import (
	"os"
	"testing"
)

func TestDefaultLOBFileManager_CreateAndRead(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewDefaultLOBFileManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	data := []byte("file manager test data")
	ref, err := mgr.Create(data)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if ref.TotalLen != uint64(len(data)) {
		t.Fatalf("expected TotalLen=%d, got %d", len(data), ref.TotalLen)
	}

	readBack, err := mgr.Read(ref)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if string(readBack) != string(data) {
		t.Fatalf("expected %q, got %q", data, readBack)
	}
}

func TestDefaultLOBFileManager_Delete(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewDefaultLOBFileManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	ref, _ := mgr.Create([]byte("to-delete"))
	if err := mgr.Delete(ref); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
}

func TestDefaultLOBFileManager_CleanupTmp(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewDefaultLOBFileManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	// Create an orphan lob_tmp_*.ao file manually in the flat directory
	tmpPath := lobTmpPath(dir, 99999)
	os.WriteFile(tmpPath, []byte("orphan"), 0640)

	// Also create a real lob_*.ao — should NOT be cleaned
	realPath := lobFilePath(dir, 100000)
	os.WriteFile(realPath, []byte("real"), 0640)

	if err := mgr.CleanupTmp(); err != nil {
		t.Fatalf("CleanupTmp failed: %v", err)
	}

	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("expected tmp file to be cleaned up")
	}
	if _, err := os.Stat(realPath); err != nil {
		t.Error("expected real lob file to remain")
	}
}

func TestDefaultLOBFileManager_FDCacheStats(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewDefaultLOBFileManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	ref, _ := mgr.Create([]byte("cache-test"))

	// First read — miss
	mgr.Read(ref)
	stats := mgr.FDCacheStats()
	if stats.Misses != 1 {
		t.Fatalf("expected 1 miss, got %d", stats.Misses)
	}

	// Second read — hit
	mgr.Read(ref)
	stats = mgr.FDCacheStats()
	if stats.Hits != 1 {
		t.Fatalf("expected 1 hit, got %d", stats.Hits)
	}
}

func TestDefaultLOBFileManager_LargeFile(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewDefaultLOBFileManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	data := make([]byte, 100_000)
	for i := range data {
		data[i] = byte(i % 256)
	}

	ref, err := mgr.Create(data)
	if err != nil {
		t.Fatal(err)
	}
	readBack, err := mgr.Read(ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(readBack) != len(data) {
		t.Fatalf("expected %d bytes, got %d", len(data), len(readBack))
	}
}
