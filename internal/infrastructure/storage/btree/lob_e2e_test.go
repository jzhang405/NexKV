package btree_test

import (
	"bytes"
	"context"
	"encoding/binary"
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

	// Step 1: Create large LOB data (5000 bytes > 2KB threshold)
	largeValue := make([]byte, 5000)
	for i := range largeValue {
		largeValue[i] = byte(i % 256)
	}

	// Step 2: Allocate overflow pages via LOBManager
	lobRef, err := lobMgr.Allocate(largeValue)
	if err != nil {
		t.Fatal(err)
	}

	// Step 3: Build LOB ref bytes [lobRefLen:2][FirstPageID:4][TotalLen:4]
	lobRefBytes := make([]byte, 10)
	binary.BigEndian.PutUint16(lobRefBytes[0:2], 8) // lobRefLen=8
	binary.BigEndian.PutUint32(lobRefBytes[2:6], lobRef.FirstPageID)
	binary.BigEndian.PutUint32(lobRefBytes[6:10], lobRef.TotalLen)

	// Step 4: Encode via BuildMVCC
	encoded, err := mvcc.BuildMVCC(mvcc.FlagLOBNormal, 42, lobRefBytes, 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Step 5: Store in BTree (~31 bytes, fits in a single leaf entry)
	tree.Set(ctx, []byte("lob-key"), encoded)

	// Step 6: Read + ParseMVCC
	raw, err := tree.Get(ctx, []byte("lob-key"))
	if err != nil {
		t.Fatal(err)
	}
	mv, err := mvcc.ParseMVCC(raw)
	if err != nil {
		t.Fatal(err)
	}

	if mv.LOB == nil {
		t.Fatal("Expected LOB ref in parsed MVCC value")
	}
	if mv.LOB.TotalLen != 5000 {
		t.Fatalf("TotalLen: expected 5000, got %d", mv.LOB.TotalLen)
	}

	// Step 7: Expand LOB via LOBManager.Read
	expanded, err := lobMgr.Read(*mv.LOB)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(expanded, largeValue) {
		t.Fatalf("LOB roundtrip data mismatch: len(expected)=%d, len(got)=%d",
			len(largeValue), len(expanded))
	}
	t.Logf("LOB end-to-end OK: %d bytes via BTree + LOBManager", len(expanded))
}
