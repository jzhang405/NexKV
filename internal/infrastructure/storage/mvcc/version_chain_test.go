package mvcc

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVersionChain_Prepend(t *testing.T) {
	vc := &VersionChain{}

	assert.NoError(t, vc.Prepend(100, []byte("v1"), FlagTombstone))
	assert.NoError(t, vc.Prepend(200, []byte("v2"), FlagNormal))
	assert.NoError(t, vc.Prepend(300, []byte("v3"), FlagNormal))

	head := vc.Load()
	if head.commitTS != 300 {
		t.Fatalf("expected head commitTS=300, got %d", head.commitTS)
	}
	n1 := head.next.Load()
	if n1.commitTS != 200 {
		t.Fatalf("expected second commitTS=200, got %d", n1.commitTS)
	}
	n2 := n1.next.Load()
	if n2.commitTS != 100 {
		t.Fatalf("expected third commitTS=100, got %d", n2.commitTS)
	}
	if n2.next.Load() != nil {
		t.Fatal("expected third node next=nil")
	}
}

func TestVersionChain_Prepend_Concurrent(t *testing.T) {
	vc := &VersionChain{}
	const goroutines = 64
	var wg sync.WaitGroup
	var errors atomic.Int64

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(ts uint64) {
			defer wg.Done()
			if err := vc.Prepend(ts, []byte("v"), FlagNormal); err != nil {
				errors.Add(1)
			}
		}(uint64(i + 1))
	}
	wg.Wait()

	if errors.Load() > 0 {
		t.Logf("CAS conflicts: %d (expected under high concurrency)", errors.Load())
	}
	assert.True(t, vc.Load() != nil, "head should be non-nil after concurrent Prepends")
}

func TestVersionChain_Load(t *testing.T) {
	vc := &VersionChain{}
	assert.Nil(t, vc.Load())
	vc.Prepend(100, []byte("v1"), FlagNormal)
	assert.NotNil(t, vc.Load())
}

func TestVersionChain_Generation(t *testing.T) {
	vc := &VersionChain{}
	assert.Equal(t, uint64(0), vc.Generation())
	vc.Prepend(100, []byte("v1"), FlagNormal)
	assert.Equal(t, uint64(1), vc.Generation())
	vc.Prepend(200, []byte("v2"), FlagNormal)
	assert.Equal(t, uint64(2), vc.Generation())
}

func TestVersionChain_PrependWithCleanup(t *testing.T) {
	vc := &VersionChain{}
	vc.Prepend(100, []byte("v1"), FlagNormal)
	vc.Prepend(200, []byte("v2"), FlagNormal)

	// Mark head as reclaimed to trigger cleanup path
	h := vc.Load()
	h.reclaimed.Store(true)

	cleaned, err := vc.PrependWithCleanup(300, []byte("v3"), FlagNormal)
	assert.NoError(t, err)
	assert.True(t, cleaned >= 1, "reclaimed head should be cleaned")
	assert.Equal(t, uint64(300), vc.Load().commitTS)
}

func TestVersionStore_LoadOrStore(t *testing.T) {
	vs := &VersionStore{}
	chain := vs.LoadOrStore("key1")
	assert.NotNil(t, chain)
	chain2 := vs.Load("key1")
	assert.Equal(t, chain, chain2)
}

func TestVersionStore_Prepend(t *testing.T) {
	vs := &VersionStore{}
	err := vs.Prepend("key1", 100, []byte("v1"), FlagNormal)
	assert.NoError(t, err)
	chain := vs.Load("key1")
	assert.NotNil(t, chain)
	assert.Equal(t, uint64(100), chain.Load().commitTS)
}
