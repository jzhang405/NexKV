// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package mvcc

import (
	"testing"
)

// mockLOBManager implements LOBManager for testing.
type mockLOBManager struct {
	allocated []LOBRef
	freed     []LOBRef
	readData  map[uint32][]byte
}

func newMockLOBManager() *mockLOBManager {
	return &mockLOBManager{
		readData: make(map[uint32][]byte),
	}
}

func (m *mockLOBManager) Allocate(data []byte) (LOBRef, error) {
	id := uint32(len(m.allocated) + 1)
	ref := LOBRef{FirstPageID: id, TotalLen: uint32(len(data))}
	m.allocated = append(m.allocated, ref)
	m.readData[id] = data
	return ref, nil
}

func (m *mockLOBManager) Read(ref LOBRef) ([]byte, error) {
	data, ok := m.readData[ref.FirstPageID]
	if !ok {
		return nil, ErrKeyNotFound
	}
	return data, nil
}

func (m *mockLOBManager) Free(ref LOBRef) error {
	m.freed = append(m.freed, ref)
	return nil
}

// mockLOBFileManager implements LOBFileManager for testing.
type mockLOBFileManager struct {
	created  []LOBFileRef
	deleted  []LOBFileRef
	readData map[uint64][]byte
}

func newMockLOBFileManager() *mockLOBFileManager {
	return &mockLOBFileManager{
		readData: make(map[uint64][]byte),
	}
}

func (m *mockLOBFileManager) Create(data []byte) (LOBFileRef, error) {
	id := uint64(len(m.created) + 1)
	ref := LOBFileRef{LOBID: id, TotalLen: uint64(len(data))}
	m.created = append(m.created, ref)
	m.readData[id] = data
	return ref, nil
}

func (m *mockLOBFileManager) Read(ref LOBFileRef) ([]byte, error) {
	data, ok := m.readData[ref.LOBID]
	if !ok {
		return nil, ErrKeyNotFound
	}
	return data, nil
}

func (m *mockLOBFileManager) Delete(ref LOBFileRef) error {
	m.deleted = append(m.deleted, ref)
	return nil
}

// --- EncodeValue tests ---

func TestEncodeValue_Inline(t *testing.T) {
	lobMgr := newMockLOBManager()
	fileMgr := newMockLOBFileManager()

	// Small value → inline (FlagNormal)
	encoded, err := EncodeValue([]byte("hi"), 100, 0, 0, nil, lobMgr, fileMgr)
	if err != nil {
		t.Fatal(err)
	}
	mv, err := ParseMVCC(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if mv.Flag != FlagNormal {
		t.Fatalf("expected FlagNormal, got 0x%02X", mv.Flag)
	}
	if string(mv.RealVal) != "hi" {
		t.Fatalf("expected 'hi', got %q", mv.RealVal)
	}
}

func TestEncodeValue_Tier1Overflow(t *testing.T) {
	lobMgr := newMockLOBManager()
	fileMgr := newMockLOBFileManager()

	// 3KB → Tier 1 overflow page
	data := make([]byte, 3000)
	for i := range data {
		data[i] = byte(i % 256)
	}

	encoded, err := EncodeValue(data, 100, 0, 0, nil, lobMgr, fileMgr)
	if err != nil {
		t.Fatal(err)
	}
	mv, err := ParseMVCC(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if mv.Flag != FlagLOBNormal {
		t.Fatalf("expected FlagLOBNormal, got 0x%02X", mv.Flag)
	}
	if len(lobMgr.allocated) != 1 {
		t.Fatalf("expected 1 allocation, got %d", len(lobMgr.allocated))
	}
	if mv.LOB == nil {
		t.Fatal("expected LOB ref to be parsed")
	}
	if mv.LOB.FirstPageID != lobMgr.allocated[0].FirstPageID {
		t.Fatalf("LOB ref mismatch: expected pageID=%d, got %d",
			lobMgr.allocated[0].FirstPageID, mv.LOB.FirstPageID)
	}
}

func TestEncodeValue_Tier2File(t *testing.T) {
	lobMgr := newMockLOBManager()
	fileMgr := newMockLOBFileManager()

	// 100KB → Tier 2 LOB file
	data := make([]byte, 100_000)
	for i := range data {
		data[i] = byte(i % 256)
	}

	encoded, err := EncodeValue(data, 100, 0, 0, nil, lobMgr, fileMgr)
	if err != nil {
		t.Fatal(err)
	}
	mv, err := ParseMVCC(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if mv.Flag != FlagLOBFile {
		t.Fatalf("expected FlagLOBFile, got 0x%02X", mv.Flag)
	}
	if len(fileMgr.created) != 1 {
		t.Fatalf("expected 1 file creation, got %d", len(fileMgr.created))
	}
	if mv.LOBFile == nil {
		t.Fatal("expected LOBFile ref to be parsed")
	}
	if mv.LOBFile.LOBID != fileMgr.created[0].LOBID {
		t.Fatalf("LOBFile ref mismatch: expected lobID=%d, got %d",
			fileMgr.created[0].LOBID, mv.LOBFile.LOBID)
	}
}

func TestEncodeValue_DisabledTiers(t *testing.T) {
	// No LOB managers → everything inline regardless of size
	data := make([]byte, 100_000)
	encoded, err := EncodeValue(data, 100, 0, 0, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	mv, err := ParseMVCC(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if mv.Flag != FlagNormal {
		t.Fatalf("expected FlagNormal when LOB disabled, got 0x%02X", mv.Flag)
	}
}

// --- EncodeDeleteValue tests ---

func TestEncodeDeleteValue_Normal(t *testing.T) {
	encoded, err := EncodeDeleteValue(200, FlagNormal, 100, []byte("old"), 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	mv, err := ParseMVCC(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if mv.Flag != FlagTombstone {
		t.Fatalf("expected FlagTombstone, got 0x%02X", mv.Flag)
	}
}

func TestEncodeDeleteValue_LOBPreserved(t *testing.T) {
	lobRef := make([]byte, 10)
	lobRef[0] = 0x00
	lobRef[1] = 0x08
	lobRef[2] = 0x00
	lobRef[3] = 0x00
	lobRef[4] = 0x00
	lobRef[5] = 0x2A // pageID = 42

	encoded, err := EncodeDeleteValue(200, FlagLOBNormal, 100, lobRef, 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	mv, err := ParseMVCC(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if mv.Flag != FlagLOBTombstone {
		t.Fatalf("expected FlagLOBTombstone, got 0x%02X", mv.Flag)
	}
	// LOB ref should be preserved for GC
	if string(mv.RealVal) != string(lobRef) {
		t.Fatal("LOB ref should be preserved in tombstone for GC")
	}
}

func TestEncodeDeleteValue_LOBFilePreserved(t *testing.T) {
	lobFileRef := make([]byte, 18)
	lobFileRef[0] = 0x00
	lobFileRef[1] = 0x10
	// LOBID = 123
	lobFileRef[2] = 0x00
	lobFileRef[3] = 0x00
	lobFileRef[4] = 0x00
	lobFileRef[5] = 0x00
	lobFileRef[6] = 0x00
	lobFileRef[7] = 0x00
	lobFileRef[8] = 0x00
	lobFileRef[9] = 0x7B

	encoded, err := EncodeDeleteValue(200, FlagLOBFile, 100, lobFileRef, 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	mv, err := ParseMVCC(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if mv.Flag != FlagLOBFileTombstone {
		t.Fatalf("expected FlagLOBFileTombstone, got 0x%02X", mv.Flag)
	}
	if string(mv.RealVal) != string(lobFileRef) {
		t.Fatal("LOB file ref should be preserved in tombstone for GC")
	}
}

// --- DecodeValue tests ---

func TestDecodeValue_InlineExpansion(t *testing.T) {
	lobMgr := newMockLOBManager()
	fileMgr := newMockLOBFileManager()

	raw, _ := BuildMVCC(FlagNormal, 100, []byte("hello"), 0, 0, nil)
	mv, err := DecodeValue(raw, lobMgr, fileMgr)
	if err != nil {
		t.Fatal(err)
	}
	if string(mv.RealVal) != "hello" {
		t.Fatalf("inline value should pass through, got %q", mv.RealVal)
	}
}

func TestDecodeValue_Tier1Expansion(t *testing.T) {
	lobMgr := newMockLOBManager()
	fileMgr := newMockLOBFileManager()

	// Allocate via mock
	lobRef, _ := lobMgr.Allocate([]byte("expanded-lob-data"))

	// Build LOB ref bytes
	refBytes := make([]byte, 10)
	refBytes[0] = 0x00
	refBytes[1] = 0x08
	refBytes[2] = byte(lobRef.FirstPageID >> 24)
	refBytes[3] = byte(lobRef.FirstPageID >> 16)
	refBytes[4] = byte(lobRef.FirstPageID >> 8)
	refBytes[5] = byte(lobRef.FirstPageID)
	refBytes[6] = byte(lobRef.TotalLen >> 24)
	refBytes[7] = byte(lobRef.TotalLen >> 16)
	refBytes[8] = byte(lobRef.TotalLen >> 8)
	refBytes[9] = byte(lobRef.TotalLen)

	raw, _ := BuildMVCC(FlagLOBNormal, 100, refBytes, 0, 0, nil)
	mv, err := DecodeValue(raw, lobMgr, fileMgr)
	if err != nil {
		t.Fatal(err)
	}
	if string(mv.RealVal) != "expanded-lob-data" {
		t.Fatalf("expected expanded data, got %q", mv.RealVal)
	}
}

func TestDecodeValue_Tier2Expansion(t *testing.T) {
	lobMgr := newMockLOBManager()
	fileMgr := newMockLOBFileManager()

	lobFileRef, _ := fileMgr.Create([]byte("expanded-file-data"))

	refBytes := make([]byte, 18)
	refBytes[0] = 0x00
	refBytes[1] = 0x10
	refBytes[2] = byte(lobFileRef.LOBID >> 56)
	refBytes[3] = byte(lobFileRef.LOBID >> 48)
	refBytes[4] = byte(lobFileRef.LOBID >> 40)
	refBytes[5] = byte(lobFileRef.LOBID >> 32)
	refBytes[6] = byte(lobFileRef.LOBID >> 24)
	refBytes[7] = byte(lobFileRef.LOBID >> 16)
	refBytes[8] = byte(lobFileRef.LOBID >> 8)
	refBytes[9] = byte(lobFileRef.LOBID)
	refBytes[10] = byte(lobFileRef.TotalLen >> 56)
	refBytes[11] = byte(lobFileRef.TotalLen >> 48)
	refBytes[12] = byte(lobFileRef.TotalLen >> 40)
	refBytes[13] = byte(lobFileRef.TotalLen >> 32)
	refBytes[14] = byte(lobFileRef.TotalLen >> 24)
	refBytes[15] = byte(lobFileRef.TotalLen >> 16)
	refBytes[16] = byte(lobFileRef.TotalLen >> 8)
	refBytes[17] = byte(lobFileRef.TotalLen)

	raw, _ := BuildMVCC(FlagLOBFile, 100, refBytes, 0, 0, nil)
	mv, err := DecodeValue(raw, lobMgr, fileMgr)
	if err != nil {
		t.Fatal(err)
	}
	if string(mv.RealVal) != "expanded-file-data" {
		t.Fatalf("expected expanded file data, got %q", mv.RealVal)
	}
}

func TestDecodeValue_NilManagers(t *testing.T) {
	// LOB flag but nil managers — should NOT expand, keep raw ref bytes
	refBytes := make([]byte, 10)
	raw, _ := BuildMVCC(FlagLOBNormal, 100, refBytes, 0, 0, nil)

	mv, err := DecodeValue(raw, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// RealVal should be the raw ref bytes (not expanded)
	if string(mv.RealVal) != string(refBytes) {
		t.Fatal("with nil managers, LOB ref should pass through unexpanded")
	}
}
