// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package mvcc

import (
	"sync"
	"testing"
)

func TestVersionChain_Prepend_Single(t *testing.T) {
	vc := &VersionChain{}
	if err := vc.Prepend(100, []byte("val1"), FlagNormal); err != nil {
		t.Fatalf("Prepend failed: %v", err)
	}
	head := vc.Load()
	if head == nil || head.commitTS != 100 {
		t.Fatalf("expected non-nil head with commitTS=100, got head=%v", head)
	}
	if string(head.value) != "val1" {
		t.Fatalf("expected value=val1, got %s", head.value)
	}
	if head.flag != FlagNormal {
		t.Fatalf("expected FlagNormal, got 0x%02X", head.flag)
	}
}

func TestVersionChain_Prepend_Multiple(t *testing.T) {
	vc := &VersionChain{}
	_ = vc.Prepend(100, []byte("v1"), FlagNormal)
	_ = vc.Prepend(200, []byte("v2"), FlagNormal)
	_ = vc.Prepend(300, []byte("v3"), FlagNormal)

	// LIFO order: head is the last Prepend'd
	head := vc.Load()
	if head.commitTS != 300 {
		t.Fatalf("expected head commitTS=300, got %d", head.commitTS)
	}
	if head.next.commitTS != 200 {
		t.Fatalf("expected second commitTS=200, got %d", head.next.commitTS)
	}
	if head.next.next.commitTS != 100 {
		t.Fatalf("expected third commitTS=100, got %d", head.next.next.commitTS)
	}
	if head.next.next.next != nil {
		t.Fatal("expected third node next=nil")
	}
}

func TestVersionChain_Prepend_Concurrent(t *testing.T) {
	vc := &VersionChain{}
	const goroutines = 64
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(ts uint64) {
			defer wg.Done()
			val := []byte{byte(ts)}
			if err := vc.Prepend(ts, val, FlagNormal); err != nil {
				t.Errorf("Prepend(%d) failed: %v", ts, err)
			}
		}(uint64(i + 1))
	}
	wg.Wait()

	// Count nodes: must have exactly 64
	count := 0
	node := vc.Load()
	for node != nil {
		count++
		node = node.next
	}
	if count != goroutines {
		t.Fatalf("expected %d nodes, got %d", goroutines, count)
	}
}

func TestVersionChain_Generation_Increment(t *testing.T) {
	vc := &VersionChain{}
	if vc.Generation() != 0 {
		t.Fatalf("expected initial generation=0, got %d", vc.Generation())
	}
	_ = vc.Prepend(100, []byte("v1"), FlagNormal)
	if vc.Generation() != 1 {
		t.Fatalf("expected generation=1 after 1 Prepend, got %d", vc.Generation())
	}
	_ = vc.Prepend(200, []byte("v2"), FlagNormal)
	if vc.Generation() != 2 {
		t.Fatalf("expected generation=2 after 2 Prepend, got %d", vc.Generation())
	}
}

func TestVersionStore_Prepend_AutoCreate(t *testing.T) {
	vs := &VersionStore{}
	err := vs.Prepend("key1", 100, []byte("val1"), FlagNormal)
	if err != nil {
		t.Fatalf("Prepend failed: %v", err)
	}
	chain := vs.Load("key1")
	if chain == nil {
		t.Fatal("expected chain to be auto-created")
	}
	head := chain.Load()
	if head == nil || head.commitTS != 100 {
		t.Fatalf("expected head commitTS=100, got %v", head)
	}
}

func TestVersionStore_Load_NotFound(t *testing.T) {
	vs := &VersionStore{}
	chain := vs.Load("nonexistent")
	if chain != nil {
		t.Fatal("expected nil for nonexistent key")
	}
}
