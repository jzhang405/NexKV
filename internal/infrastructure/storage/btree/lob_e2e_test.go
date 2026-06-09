package btree_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"strings"
	"testing"

	"github.com/jzhang405/NexKV/internal/infrastructure/storage/btree"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/lob"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/mvcc"
)

func TestLOBEndToEnd(t *testing.T) {
	storage, err := btree.NewOffheapBTreeStorage(64 * 1024 * 1024)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := btree.NewBTree(storage, btree.WithTSGenerator(mvcc.NewLocalTS()))
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	ctx := context.Background()

	lobMgr := lob.NewDefaultLOBManager(storage.GetPageManager())

	largeValue := make([]byte, 5000)
	for i := range largeValue {
		largeValue[i] = byte(i % 256)
	}

	lobRef, err := lobMgr.Allocate(largeValue)
	if err != nil {
		t.Fatal(err)
	}

	lobRefBytes := make([]byte, 10)
	binary.BigEndian.PutUint16(lobRefBytes[0:2], 8)
	binary.BigEndian.PutUint32(lobRefBytes[2:6], lobRef.FirstPageID)
	binary.BigEndian.PutUint32(lobRefBytes[6:10], lobRef.TotalLen)

	encoded, err := mvcc.BuildMVCC(mvcc.FlagLOBNormal, 42, lobRefBytes, 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}

	tree.Set(ctx, []byte("lob-key"), encoded)

	raw, err := tree.Get(ctx, []byte("lob-key"))
	if err != nil {
		t.Fatal(err)
	}
	mv, err := mvcc.ParseMVCC(raw)
	if err != nil {
		t.Fatal(err)
	}

	if mv.LOB == nil {
		t.Fatal("Expected LOB ref in MVCC value")
	}
	if mv.LOB.TotalLen != 5000 {
		t.Fatalf("TotalLen: expected 5000, got %d", mv.LOB.TotalLen)
	}

	expanded, err := lobMgr.Read(*mv.LOB)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(expanded, largeValue) {
		t.Fatalf("LOB roundtrip mismatch: %d vs %d", len(largeValue), len(expanded))
	}
	t.Logf("LOB overflow OK: %d bytes", len(expanded))
}

func TestLOBFileEndToEnd(t *testing.T) {
	lobDir := t.TempDir()

	storage, err := btree.NewOffheapBTreeStorage(64 * 1024 * 1024)
	if err != nil {
		t.Fatal(err)
	}

	tree, err := btree.NewBTree(storage,
		btree.WithTSGenerator(mvcc.NewLocalTS()),
		btree.WithLOBDir(lobDir),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	ctx := context.Background()

	lobFileMgr, err := lob.NewDefaultLOBFileManager(lobDir)
	if err != nil {
		t.Fatal(err)
	}

	dataSize := 300 * 1024 // >256KB to trigger Tier 2
	largeValue := make([]byte, dataSize)
	for i := range largeValue {
		largeValue[i] = byte(i % 256)
	}

	ref, err := lobFileMgr.Create(largeValue)
	if err != nil {
		t.Fatal(err)
	}
	if ref.LOBID == 0 {
		t.Fatal("Expected non-zero LOBID")
	}
	if ref.TotalLen != uint64(dataSize) {
		t.Fatalf("TotalLen: expected %d, got %d", dataSize, ref.TotalLen)
	}

	refBytes := make([]byte, 18)
	binary.BigEndian.PutUint16(refBytes[0:2], 16)
	binary.BigEndian.PutUint64(refBytes[2:10], ref.LOBID)
	binary.BigEndian.PutUint64(refBytes[10:18], ref.TotalLen)

	encoded, err := mvcc.BuildMVCC(mvcc.FlagLOBFile, 42, refBytes, 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}

	err = tree.Set(ctx, []byte("lob-file-key"), encoded)
	if err != nil {
		t.Fatal(err)
	}

	raw, err := tree.Get(ctx, []byte("lob-file-key"))
	if err != nil {
		t.Fatal(err)
	}
	mv, err := mvcc.ParseMVCC(raw)
	if err != nil {
		t.Fatal(err)
	}

	if mv.LOBFile == nil {
		t.Fatal("Expected LOBFile ref in MVCC value")
	}
	if mv.LOBFile.LOBID != ref.LOBID {
		t.Fatalf("LOBID mismatch: %d vs %d", ref.LOBID, mv.LOBFile.LOBID)
	}

	decoded, err := mvcc.DecodeValue(raw, nil, lobFileMgr)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(decoded.RealVal, largeValue) {
		t.Fatalf("LOB file roundtrip mismatch")
	}

	// Verify file on disk (flat directory, lob_*.ao suffix)
	found := false
	entries, _ := os.ReadDir(lobDir)
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "lob_") && strings.HasSuffix(e.Name(), ".ao") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("LOB file not found on disk")
	}

	// Delete + verify
	err = lobFileMgr.Delete(ref)
	if err != nil {
		t.Fatal(err)
	}
	found = false
	entries, _ = os.ReadDir(lobDir)
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "lob_") && strings.HasSuffix(e.Name(), ".ao") {
			found = true
			break
		}
	}
	if found {
		t.Fatal("LOB file should be deleted")
	}

	t.Logf("LOB File OK: %d bytes via BTree + LOBFileManager", len(decoded.RealVal))
}
