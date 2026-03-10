package btree

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBTree_GetDepth tests GetDepth method.
func TestBTree_GetDepth(t *testing.T) {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	depth := btree.GetDepth()
	assert.GreaterOrEqual(t, depth, 0)
}

// TestBTreeOperations_CompareBytes tests compareBytes helper function.
func TestBTreeOperations_CompareBytes(t *testing.T) {
	// Test equal bytes
	result := compareBytes([]byte("abc"), []byte("abc"))
	assert.Equal(t, 0, result)

	// Test less than
	result = compareBytes([]byte("abc"), []byte("def"))
	assert.Equal(t, -1, result)

	// Test greater than
	result = compareBytes([]byte("def"), []byte("abc"))
	assert.Equal(t, 1, result)

	// Test empty slices
	result = compareBytes([]byte{}, []byte{})
	assert.Equal(t, 0, result)
}

// TestBTreeOperations_CompareBytesInternal tests compareBytesInternal function.
func TestBTreeOperations_CompareBytesInternal(t *testing.T) {
	// Test equal length and content
	result := compareBytesInternal([]byte("abc"), []byte("abc"))
	assert.Equal(t, 0, result)

	// Test different content
	result = compareBytesInternal([]byte("abc"), []byte("abd"))
	assert.Less(t, result, 0)

	// Test different length
	result = compareBytesInternal([]byte("abc"), []byte("abcd"))
	assert.Less(t, result, 0)
}
