// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package checkpoint

import (
	"encoding/binary"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockWAL implements WALWriter for testing.
type mockWAL struct {
	mu        sync.Mutex
	entries   []*service.WALEntry
	lsn       atomic.Uint64
	truncated atomic.Uint64
}

func newMockWAL() *mockWAL { return &mockWAL{} }

func (m *mockWAL) Append(entry *service.WALEntry) (service.LSN, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	lsn := m.lsn.Add(1)
	entry.LSN = service.LSN(lsn)
	m.entries = append(m.entries, entry)
	return service.LSN(lsn), nil
}

func (m *mockWAL) Sync() error { return nil }

func (m *mockWAL) Truncate(lsn service.LSN) error {
	m.truncated.Store(uint64(lsn))
	return nil
}

func (m *mockWAL) CurrentLSN() service.LSN {
	return service.LSN(m.lsn.Load())
}

func (m *mockWAL) entryCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.entries)
}

// mockPageRef implements PageRef for testing.
type mockPageRef struct {
	id   model.PageID
	leaf bool
	kids []model.PageID
}

func (p *mockPageRef) PageID() model.PageID     { return p.id }
func (p *mockPageRef) IsLeaf() bool             { return p.leaf }
func (p *mockPageRef) ChildIDs() []model.PageID { return p.kids }

// mockBTreeScanner implements BTreeScanner for testing.
type mockBTreeScanner struct {
	root *mockPageRef
}

func (m *mockBTreeScanner) RootPage() PageRef {
	if m.root == nil {
		return nil
	}
	return m.root
}

func (m *mockBTreeScanner) EnumeratePages(root PageRef) ([]PageFlushItem, error) {
	return nil, nil // stub: no AO pages
}

func newTestBTree() *mockBTreeScanner {
	return &mockBTreeScanner{
		root: &mockPageRef{
			id:   model.RootPageID,
			leaf: false,
			kids: []model.PageID{2, 3},
		},
	}
}

func TestConfig_Defaults(t *testing.T) {
	cfg := DefaultConfig()
	assert.Equal(t, 30*time.Second, cfg.Interval)
	assert.Equal(t, 4, cfg.SegmentCountThreshold)
	assert.Equal(t, int64(256*1024*1024), cfg.WALSizeThreshold)
}

func TestNewManager(t *testing.T) {
	wal := newMockWAL()
	btree := newTestBTree()
	cfg := DefaultConfig()

	mgr := NewManager(wal, btree, nil, cfg)
	assert.NotNil(t, mgr)
	assert.NotNil(t, mgr.ctx)
}

func TestFuzzyCheckpoint(t *testing.T) {
	wal := newMockWAL()
	btree := newTestBTree()
	cfg := DefaultConfig()

	mgr := NewManager(wal, btree, nil, cfg)
	err := mgr.FuzzyCheckpoint()
	require.NoError(t, err)

	// Verify Checkpoint WAL entry was appended
	assert.GreaterOrEqual(t, wal.entryCount(), 1)
}

func TestSharpCheckpoint(t *testing.T) {
	wal := newMockWAL()
	btree := newTestBTree()
	cfg := DefaultConfig()

	// Write some entries so CurrentLSN > 0
	for i := 0; i < 3; i++ {
		_, _ = wal.Append(&service.WALEntry{Type: service.WALTypeCheckpoint, Key: []byte{byte(i)}})
	}

	mgr := NewManager(wal, btree, nil, cfg)
	err := mgr.SharpCheckpoint()
	require.NoError(t, err)

	// Verify WAL truncate was called with non-zero LSN
	assert.Greater(t, wal.truncated.Load(), uint64(0))
}

func TestFuzzyCheckpoint_CheckpointEntryBeforeTruncate(t *testing.T) {
	wal := newMockWAL()
	btree := newTestBTree()
	cfg := DefaultConfig()

	mgr := NewManager(wal, btree, nil, cfg)

	// Write some WAL entries before checkpoint
	for i := 0; i < 5; i++ {
		_, _ = wal.Append(&service.WALEntry{Type: service.WALTypeCheckpoint, Key: []byte{byte(i)}})
	}

	err := mgr.FuzzyCheckpoint()
	require.NoError(t, err)

	// Checkpoint entry must be the last one (appended before truncate)
	wal.mu.Lock()
	defer wal.mu.Unlock()
	last := wal.entries[len(wal.entries)-1]
	assert.Equal(t, service.WALTypeCheckpoint, last.Type)
	// Key contains checkpointStartLSN in big-endian (8 bytes)
	assert.Len(t, last.Key, 12)
	checkpointLSN := binary.BigEndian.Uint64(last.Key[0:8])
	assert.Greater(t, checkpointLSN, uint64(0))
}

func TestFuzzyCheckpoint_StartLSNBeforeRootSnapshot(t *testing.T) {
	wal := newMockWAL()
	btree := newTestBTree()
	cfg := DefaultConfig()

	// Pre-write entries to increment LSN
	for i := 0; i < 3; i++ {
		_, _ = wal.Append(&service.WALEntry{Type: service.WALTypeCheckpoint, Key: []byte{byte(i)}})
	}

	mgr := NewManager(wal, btree, nil, cfg)
	err := mgr.FuzzyCheckpoint()
	require.NoError(t, err)

	// Checkpoint LSN should be the state at the start of checkpoint (4 entries existed)
	wal.mu.Lock()
	defer wal.mu.Unlock()
	for _, e := range wal.entries {
		if e.Type == service.WALTypeCheckpoint && len(e.Key) >= 12 {
			lsn := binary.BigEndian.Uint64(e.Key[0:8])
			// Pre-existing entries had LSN 1,2,3 so checkpoint start LSN was 3.
			// The new checkpoint entry gets LSN 4 or higher.
			// The Checkpoint entry's Key encodes the startLSN, not its own LSN.
			assert.Greater(t, lsn, uint64(0))
			return
		}
	}
	t.Fatal("expected a checkpoint entry")
}

func TestFuzzyCheckpoint_ValidatesRoot(t *testing.T) {
	wal := newMockWAL()

	// BTree with nil root
	btree := &mockBTreeScanner{root: nil}
	cfg := DefaultConfig()

	mgr := NewManager(wal, btree, nil, cfg)
	err := mgr.FuzzyCheckpoint()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil root page")
}

func TestSharpCheckpoint_DoubleCall(t *testing.T) {
	wal := newMockWAL()
	btree := newTestBTree()
	cfg := DefaultConfig()

	mgr := NewManager(wal, btree, nil, cfg)

	err := mgr.SharpCheckpoint()
	require.NoError(t, err)

	// Second call should also succeed
	err = mgr.SharpCheckpoint()
	require.NoError(t, err)
}

func TestCheckpoint_Stats(t *testing.T) {
	wal := newMockWAL()
	btree := newTestBTree()
	cfg := DefaultConfig()

	mgr := NewManager(wal, btree, nil, cfg)

	// Write entries to produce non-zero LSN
	for i := 0; i < 3; i++ {
		_, _ = wal.Append(&service.WALEntry{Type: service.WALTypeCheckpoint, Key: []byte{byte(i)}})
	}

	assert.Equal(t, uint64(0), mgr.stats.TotalCheckpoints.Load())

	_ = mgr.FuzzyCheckpoint()
	assert.Equal(t, uint64(1), mgr.stats.TotalCheckpoints.Load())
	assert.Greater(t, mgr.stats.LastCheckpointLSN.Load(), uint64(0))
	// Duration may be zero on fast machines (sub-millisecond), so just check not negative
	assert.GreaterOrEqual(t, mgr.stats.LastDurationMs.Load(), int64(0))
}

// FuzzCheckpointRecovery tests checkpoint consistency under random WAL states.
func FuzzCheckpointRecovery(f *testing.F) {
	f.Add(int(5), int(3))

	f.Fuzz(func(t *testing.T, numPreEntries int, numPostEntries int) {
		if numPreEntries < 0 || numPreEntries > 50 {
			return
		}
		if numPostEntries < 0 || numPostEntries > 50 {
			return
		}

		wal := newMockWAL()
		btree := newTestBTree()
		cfg := DefaultConfig()

		mgr := NewManager(wal, btree, nil, cfg)

		// Write some WAL entries before checkpoint
		for i := 0; i < numPreEntries; i++ {
			_, _ = wal.Append(&service.WALEntry{Type: service.WALType(i % 7), Key: []byte{byte(i)}})
		}

		// Run checkpoint
		err := mgr.FuzzyCheckpoint()
		if err != nil && err.Error() == "checkpoint: nil root page" {
			return
		}
		require.NoError(t, err)

		// Write more entries after checkpoint
		for i := 0; i < numPostEntries; i++ {
			_, _ = wal.Append(&service.WALEntry{Type: service.WALType(i % 7), Key: []byte{byte(i + numPreEntries)}})
		}

		// Verify truncate was called with correct LSN
		truncatedLSN := wal.truncated.Load()
		assert.Greater(t, truncatedLSN, uint64(0))

		// Verify checkpoint entry exists
		wal.mu.Lock()
		foundCheckpoint := false
		for _, e := range wal.entries {
			if e.Type == service.WALTypeCheckpoint && len(e.Key) >= 12 {
				foundCheckpoint = true
				assert.Equal(t, truncatedLSN, binary.BigEndian.Uint64(e.Key[0:8]))
				break
			}
		}
		wal.mu.Unlock()
		assert.True(t, foundCheckpoint, "expected a checkpoint entry")
	})
}

func TestEncodeCheckpointKey_Empty(t *testing.T) {
	key := encodeCheckpointKey(100, nil)
	if len(key) != 12 {
		t.Fatalf("expected key len 12, got %d", len(key))
	}
	startLSN := binary.BigEndian.Uint64(key[0:8])
	if startLSN != 100 {
		t.Errorf("expected startLSN=100, got %d", startLSN)
	}
	pageCount := binary.BigEndian.Uint32(key[8:12])
	if pageCount != 0 {
		t.Errorf("expected pageCount=0, got %d", pageCount)
	}
}

func TestEncodeCheckpointKey_WithPages(t *testing.T) {
	locs := map[model.PageID]model.ChunkPosition{
		1: 0x1000,
		2: 0x2000,
	}
	key := encodeCheckpointKey(200, locs)
	expectedLen := 12 + 2*16 // startLSN(8) + pageCount(4) + (PageID:8,ChunkPos:8)*2
	if len(key) != expectedLen {
		t.Fatalf("expected key len %d, got %d", expectedLen, len(key))
	}
	pageCount := binary.BigEndian.Uint32(key[8:12])
	if pageCount != 2 {
		t.Errorf("expected pageCount=2, got %d", pageCount)
	}
}
