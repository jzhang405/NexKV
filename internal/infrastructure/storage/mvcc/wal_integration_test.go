package mvcc

import (
	"testing"
	"github.com/jzhang405/NexKV/internal/domain/service"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteEntries(t *testing.T) {
	wb := NewWriteBuffer()
	wb.Put("key1", []byte("val1"), []byte("old1"), FlagNormal, 100)
	wb.Put("key2", []byte("val2"), nil, 0, 0) // Insert
	wb.Delete("key3", []byte("old3"), FlagNormal, 300)

	entries := wb.WriteEntries()
	require.Len(t, entries, 3)

	// Verify entry types
	assert.Equal(t, service.WALTypeUpdate, entries[0].Type) // key1: Update (had oldValue)
	assert.Equal(t, "key1", string(entries[0].Key))
	assert.Equal(t, []byte("val1"), entries[0].Value)

	assert.Equal(t, service.WALTypeInsert, entries[1].Type) // key2: Insert (no oldValue)
	assert.Equal(t, "key2", string(entries[1].Key))

	assert.Equal(t, service.WALTypeDelete, entries[2].Type) // key3: Delete
	assert.Equal(t, "key3", string(entries[2].Key))
}

func TestWriteEntries_NoCommitMarker(t *testing.T) {
	wb := NewWriteBuffer()
	wb.Put("key1", []byte("val1"), []byte("old1"), FlagNormal, 100)

	entries := wb.WriteEntries()
	require.Len(t, entries, 1)

	// Verify NO Commit marker in WriteEntries
	for _, e := range entries {
		assert.NotEqual(t, service.WALTypeCommit, e.Type, "WriteEntries must not include Commit marker")
	}
}

func TestCommitEntry(t *testing.T) {
	e := CommitEntry(12345)
	assert.Equal(t, service.WALTypeCommit, e.Type)
	require.Len(t, e.Key, 8)

	// Verify commitTS in Key (big-endian)
	var commitTS uint64
	for i := 0; i < 8; i++ {
		commitTS = (commitTS << 8) | uint64(e.Key[i])
	}
	assert.Equal(t, uint64(12345), commitTS)
}

func TestTxPrepareEntry_RoundTrip(t *testing.T) {
	wb := NewWriteBuffer()
	wb.Put("alpha", []byte("new-alpha"), []byte("old-alpha"), FlagNormal, 100)
	wb.Put("beta", []byte("new-beta"), nil, 0, 0)                         // Insert
	wb.Delete("gamma", []byte("old-gamma"), FlagTombstone, 300)           // Delete
	wb.Put("delta", []byte("new-delta"), []byte("old-delta"), FlagNormal, 400)

	txID := uint64(42)
	e := TxPrepareEntry(txID, wb)
	assert.Equal(t, service.WALTypeTxPrepare, e.Type)

	// Verify Key = txID
	require.Len(t, e.Key, 8)
	parsedTxID := uint64(0)
	for i := 0; i < 8; i++ {
		parsedTxID = (parsedTxID << 8) | uint64(e.Key[i])
	}
	assert.Equal(t, txID, parsedTxID)

	// Parse back
	gotTxID, entries, err := ParseTxPrepareEntry(e)
	require.NoError(t, err)
	assert.Equal(t, txID, gotTxID)
	require.Len(t, entries, 4)

	// Verify parsed entries
	byKey := make(map[string]TxPrepareParsedEntry)
	for _, pe := range entries {
		byKey[pe.Key] = pe
	}

	// alpha: Update
	alpha := byKey["alpha"]
	assert.Equal(t, OpUpdate, alpha.Op)
	assert.Equal(t, byte(FlagNormal), alpha.OldFlag)
	assert.Equal(t, uint64(100), alpha.OldBeginTS)
	assert.Equal(t, []byte("old-alpha"), alpha.OldValue)
	assert.Equal(t, byte(FlagNormal), alpha.NewFlag)
	assert.Equal(t, []byte("new-alpha"), alpha.NewValue)

	// beta: Insert
	beta := byKey["beta"]
	assert.Equal(t, OpInsert, beta.Op)
	assert.Equal(t, byte(0), beta.OldFlag)
	assert.Equal(t, uint64(0), beta.OldBeginTS)
	assert.Equal(t, 0, len(beta.OldValue), "Insert should have empty oldValue")
	assert.Equal(t, byte(FlagNormal), beta.NewFlag)
	assert.Equal(t, []byte("new-beta"), beta.NewValue)

	// gamma: Delete
	gamma := byKey["gamma"]
	assert.Equal(t, OpDelete, gamma.Op)
	assert.Equal(t, byte(FlagTombstone), gamma.OldFlag)
	assert.Equal(t, uint64(300), gamma.OldBeginTS)
	assert.Equal(t, []byte("old-gamma"), gamma.OldValue)
	assert.Equal(t, byte(FlagTombstone), gamma.NewFlag)

	// delta: Update
	delta := byKey["delta"]
	assert.Equal(t, OpUpdate, delta.Op)
	assert.Equal(t, []byte("old-delta"), delta.OldValue)
	assert.Equal(t, []byte("new-delta"), delta.NewValue)
}

func TestTxPrepareEntry_Empty(t *testing.T) {
	wb := NewWriteBuffer()
	e := TxPrepareEntry(1, wb)

	_, entries, err := ParseTxPrepareEntry(e)
	require.NoError(t, err)
	assert.Len(t, entries, 0)
}

func TestTxPrepareEntry_OrderDeterministic(t *testing.T) {
	// Verify TxPrepare produces deterministic output (sorted keys)
	wb1 := NewWriteBuffer()
	wb1.Put("z", []byte("v"), []byte("old"), FlagNormal, 1)
	wb1.Put("a", []byte("v"), []byte("old"), FlagNormal, 1)
	wb1.Put("m", []byte("v"), []byte("old"), FlagNormal, 1)

	wb2 := NewWriteBuffer()
	wb2.Put("m", []byte("v"), []byte("old"), FlagNormal, 1)
	wb2.Put("a", []byte("v"), []byte("old"), FlagNormal, 1)
	wb2.Put("z", []byte("v"), []byte("old"), FlagNormal, 1)

	_, entries1, _ := ParseTxPrepareEntry(TxPrepareEntry(1, wb1))
	_, entries2, _ := ParseTxPrepareEntry(TxPrepareEntry(1, wb2))

	require.Len(t, entries1, 3)
	require.Len(t, entries2, 3)

	// Both should be sorted: a, m, z
	assert.Equal(t, "a", entries1[0].Key)
	assert.Equal(t, "m", entries1[1].Key)
	assert.Equal(t, "z", entries1[2].Key)
	assert.Equal(t, entries1[0].Key, entries2[0].Key)
	assert.Equal(t, entries1[1].Key, entries2[1].Key)
	assert.Equal(t, entries1[2].Key, entries2[2].Key)
}

func TestParseTxPrepareEntry_Invalid(t *testing.T) {
	// Wrong type
	e := &WALEntry{Type: service.WALTypeInsert}
	_, _, err := ParseTxPrepareEntry(e)
	assert.Error(t, err)

	// Too-short key
	e2 := &WALEntry{Type: service.WALTypeTxPrepare, Key: []byte{1, 2, 3}}
	_, _, err = ParseTxPrepareEntry(e2)
	assert.Error(t, err)

	// Too-short value (no keyCount)
	e3 := &WALEntry{Type: service.WALTypeTxPrepare, Key: make([]byte, 8), Value: []byte{1, 2}}
	_, _, err = ParseTxPrepareEntry(e3)
	assert.Error(t, err)
}
