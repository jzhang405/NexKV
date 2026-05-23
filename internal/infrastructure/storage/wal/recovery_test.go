package wal

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecovery_2PC_TxPrepareRollback(t *testing.T) {
	dw := setupTestWAL(t)
	defer dw.Close()

	txID := uint64(42)
	prepareKey := make([]byte, 8)
	binary.BigEndian.PutUint64(prepareKey, txID)
	prepareVal := make([]byte, 4)
	binary.BigEndian.PutUint32(prepareVal, 0) // 0 keys

	_, err := dw.Append(&WALEntry{
		Type:  WALTypeTxPrepare,
		Key:   prepareKey,
		Value: prepareVal,
	})
	require.NoError(t, err)
	require.NoError(t, dw.Sync())

	entries, err := dw.Recover()
	require.NoError(t, err)

	found := false
	for _, e := range entries {
		if e.Type == WALTypeTxPrepare {
			found = true
			break
		}
	}
	assert.True(t, found, "TxPrepare should be recovered")
}

func TestRecovery_2PC_TxPrepareWithCommit(t *testing.T) {
	dw := setupTestWAL(t)
	defer dw.Close()

	txID := uint64(42)
	prepareKey := make([]byte, 8)
	binary.BigEndian.PutUint64(prepareKey, txID)

	_, err := dw.Append(&WALEntry{
		Type:  WALTypeTxPrepare,
		TxID:  txID,
		Key:   prepareKey,
		Value: make([]byte, 4),
	})
	require.NoError(t, err)

	commitKey := make([]byte, 16)
	binary.BigEndian.PutUint64(commitKey[0:8], txID)
	binary.BigEndian.PutUint64(commitKey[8:16], 100)
	_, err = dw.Append(&WALEntry{
		Type: WALTypeTxCommit,
		TxID: txID,
		Key:  commitKey,
	})
	require.NoError(t, err)
	require.NoError(t, dw.Sync())

	entries, err := dw.Recover()
	require.NoError(t, err)

	var hasPrepare, hasCommit bool
	for _, e := range entries {
		switch e.Type {
		case WALTypeTxPrepare:
			hasPrepare = true
		case WALTypeTxCommit:
			hasCommit = true
		}
	}
	assert.True(t, hasPrepare)
	assert.True(t, hasCommit)
}

func TestWALTypeString_TxTypes(t *testing.T) {
	assert.Equal(t, "TxBegin", WALTypeString(WALTypeTxBegin))
	assert.Equal(t, "TxWrite", WALTypeString(WALTypeTxWrite))
	assert.Equal(t, "TxCommit", WALTypeString(WALTypeTxCommit))
	assert.Equal(t, "TxRollback", WALTypeString(WALTypeTxRollback))
	assert.Equal(t, "TxPrepare", WALTypeString(WALTypeTxPrepare))
}
