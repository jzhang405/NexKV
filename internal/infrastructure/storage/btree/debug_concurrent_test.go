package btree

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConcurrentSplit_Debug(t *testing.T) {
	storage, err := NewOffheapBTreeStorage(1024 * 1024 * 1024)
	require.NoError(t, err)

	metrics := NewBTreeMetrics()
	tree, err := NewBTreeWithMetrics(storage, metrics)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// No prefill - start from empty tree, concurrent writes from scratch
	const prefill = 0

	// Concurrent phase: 2 goroutines, 50 keys each
	// These keys should trigger root split
	const goroutines = 2
	const keysPerG = 500
	totalKeys := prefill + goroutines*keysPerG

	var wg sync.WaitGroup
	var writeErrors []string
	var writeErrMu sync.Mutex

	for g := range goroutines {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for j := range keysPerG {
				key := fmt.Appendf(nil, "g%d-k%04d", gid, j)
				value := fmt.Appendf(nil, "g%d-v%04d", gid, j)

				// Retry up to 100 times (same as TestConcurrentSplit)
				for retry := range 100 {
					err := tree.Set(ctx, key, value)
					if err == nil {
						break
					}
					if retry == 99 {
						writeErrMu.Lock()
						writeErrors = append(writeErrors, fmt.Sprintf("g%d-k%04d: %v", gid, j, err))
						writeErrMu.Unlock()
					}
				}
			}
		}(g)
	}
	wg.Wait()

	t.Logf("After concurrent writes: tree size=%d, write errors=%d", tree.Size(), len(writeErrors))
	for _, e := range writeErrors {
		t.Logf("  Write error: %s", e)
	}

	// Verify all keys
	var missing []string
	var wrongValue []string

	// Check prefill keys
	for i := range prefill {
		key := fmt.Appendf(nil, "s-k%04d", i)
		val, err := tree.Get(ctx, key)
		if err != nil {
			missing = append(missing, string(key))
		} else {
			expected := fmt.Sprintf("s-v%04d", i)
			if string(val) != expected {
				wrongValue = append(wrongValue, fmt.Sprintf("%s: got %s want %s", string(key), string(val), expected))
			}
		}
	}

	// Check concurrent keys
	for g := range goroutines {
		for j := range keysPerG {
			key := fmt.Appendf(nil, "g%d-k%04d", g, j)
			val, err := tree.Get(ctx, key)
			if err != nil {
				missing = append(missing, string(key))
			} else {
				expected := fmt.Sprintf("g%d-v%04d", g, j)
				if string(val) != expected {
					wrongValue = append(wrongValue, fmt.Sprintf("%s: got %s want %s", string(key), string(val), expected))
				}
			}
		}
	}

	t.Logf("Result: total=%d, missing=%d, wrongValue=%d, treeSize=%d",
		totalKeys, len(missing), len(wrongValue), tree.Size())

	if len(missing) > 0 {
		t.Logf("First 20 missing keys:")
		for i, k := range missing {
			if i >= 20 {
				break
			}
			t.Logf("  %s", k)
		}
	}

	if len(wrongValue) > 0 {
		t.Logf("First 10 wrong values:")
		for i, w := range wrongValue {
			if i >= 10 {
				break
			}
			t.Logf("  %s", w)
		}
	}

	snap := tree.GetMetrics()
	t.Logf("Metrics: %s", snap.String())

	// Dump root state
	rootInfo := tree.rootRef.GetPageInfo()
	t.Logf("Root: pageID=%d, version=%d, isLeaf=%v, nodeState=%v",
		rootInfo.PageID, rootInfo.Version, rootInfo.IsLeaf, rootInfo.NodeState)

	if !rootInfo.IsLeaf {
		cache := tree.rootRef.children.Load()
		if cache != nil {
			t.Logf("Root children count=%d, separators=%d", len(cache.Children), len(cache.Separators))
			for i, child := range cache.Children {
				if child != nil {
					ci := child.GetPageInfo()
					if ci != nil {
						t.Logf("  child[%d]: pageID=%d, isLeaf=%v, redirect=%v, version=%d",
							i, ci.PageID, ci.IsLeaf, ci.Redirect, ci.Version)
					} else {
						t.Logf("  child[%d]: nil pInfo", i)
					}
				}
			}
			for i, sep := range cache.Separators {
				t.Logf("  sep[%d]: %s", i, string(sep))
			}
		} else {
			t.Logf("Root children cache: nil")
		}
	}

	assert.Equal(t, 0, len(missing), "no keys should be missing, got %d", len(missing))
	assert.Equal(t, 0, len(wrongValue), "no wrong values")
	assert.Equal(t, int64(totalKeys), tree.Size(), "tree size should match total keys")
}
