// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package btree

import (
	"context"

	"github.com/jzhang405/NexKV/internal/infrastructure/storage/mvcc"
)

// btreeStorageAdapter adapts a BTree to implement mvcc.StorageBackend.
// It bypasses the BTree's built-in MVCC encoding (BuildMVCC/ParseMVCC)
// and directly writes/reads raw encoded bytes from leaf pages.
// The transaction layer handles MVCC encoding/decoding.
type btreeStorageAdapter struct {
	tree *BTree
}

// newStorageAdapter creates a StorageBackend adapter for the given BTree.
func newStorageAdapter(tree *BTree) mvcc.StorageBackend {
	return &btreeStorageAdapter{tree: tree}
}

// GetRaw returns the full MVCC-encoded value as a Go-heap copy.
// Bypasses tombstone filtering — returns raw bytes from the leaf page.
func (a *btreeStorageAdapter) GetRaw(_ context.Context, key []byte) ([]byte, error) {
	if err := a.tree.checkOpen(); err != nil {
		return nil, err
	}

	path, err := searchPath(a.tree.rootRef, key)
	if err != nil {
		return nil, err
	}
	defer path.ReleaseAll()

	leafEntry := path.Leaf()
	pInfo := leafEntry.Ref.GetPageInfo()
	if pInfo == nil {
		return nil, ErrPageFreed
	}

	leaf, err := a.tree.storage.GetLeafPage(pInfo.PageID)
	if err != nil {
		return nil, err
	}

	idx, found := leaf.Search(key)
	if !found {
		return nil, mvcc.ErrKeyNotFound
	}

	// leaf.GetValue returns mmap sub-slice. Copy for safety — caller may retain copy.
	raw := leaf.GetValue(idx)
	cp := make([]byte, len(raw))
	copy(cp, raw)
	return cp, nil
}

// GetBatchRaw returns raw MVCC-encoded values for multiple keys in one
// searchPath+epoch window. Used by SnapshotTx.GetBatch for batch snapshot reads.
func (a *btreeStorageAdapter) GetBatchRaw(ctx context.Context, keys [][]byte) ([][]byte, error) {
	return a.tree.getBatchRawBytes(ctx, keys)
}

// Set writes a pre-encoded MVCC value to the B+Tree.
// Unlike BTree.Set, this does NOT call BuildMVCC — the caller provides
// the fully encoded value (flag + beginTS + realVal).
func (a *btreeStorageAdapter) Set(_ context.Context, key, value []byte) error {
	if err := a.tree.checkOpen(); err != nil {
		return err
	}

	err := writeOperation(a.tree, key, func(leaf LeafPage) (*leafMutation, error) {
		idx, found := leaf.Search(key)
		if found {
			// Detect Tombstone→Normal transition for size accounting
			var delta int64
			oldVal := leaf.GetValue(idx)
			oldMVCC, parseErr := mvcc.ParseMVCC(oldVal)
			if parseErr == nil && oldMVCC.IsTombstone() {
				newMVCC, newParseErr := mvcc.ParseMVCC(value)
				if newParseErr == nil && !newMVCC.IsTombstone() {
					delta = 1 // Tombstone→Normal: key becomes visible
				}
			}

			newLeaf, updateErr := leaf.Update(idx, value)
			if updateErr != nil {
				return nil, updateErr
			}
			return &leafMutation{
				newPageID: newLeaf.PageID(),
				delta:     delta,
			}, nil
		}

		// Insert new key with raw encoded value
		newLeaf, insertErr := leaf.Insert(key, value)
		if insertErr != nil {
			return nil, insertErr
		}
		return &leafMutation{
			newPageID: newLeaf.PageID(),
			delta:     1,
		}, nil
	}, MaxCASRetries)

	if err == nil && a.tree.metrics != nil {
		a.tree.metrics.WriteCount.Add(1)
	}

	return err
}

// SetBatch applies multiple key-value pairs with segmented COW batching.
// Pairs are processed in segments that fit in a single page (~100 keys/page).
// Each segment uses one writeOperation → one COW + one CAS.
// Total: O(N/pageSize) COWs instead of O(N) with single-key Set.
//
// Keys must be sorted before calling — the caller (applyWriteBuffer) already sorts.
func (a *btreeStorageAdapter) SetBatch(_ context.Context, pairs []mvcc.KVPair) (int, error) {
	if err := a.tree.checkOpen(); err != nil {
		return 0, err
	}
	if len(pairs) == 0 {
		return 0, nil
	}

	count := 0
	for offset := 0; offset < len(pairs); {
		// Each segment: as many keys as fit in current page (~100),
		// start from pairs[offset]. CAS retries restart from same offset.
		segStart := offset
		err := writeOperation(a.tree, pairs[segStart].Key, func(leaf LeafPage) (*leafMutation, error) {
			var totalDelta int64
			current := leaf
			n := 0

			for i := segStart; i < len(pairs); i++ {
				p := pairs[i]
				idx, found := current.Search(p.Key)
				if found {
					oldVal := current.GetValue(idx)
					if oldMVCC, parseErr := mvcc.ParseMVCC(oldVal); parseErr == nil && oldMVCC.IsTombstone() {
						if newMVCC, newParseErr := mvcc.ParseMVCC(p.Value); newParseErr == nil && !newMVCC.IsTombstone() {
							totalDelta++
						}
					}
					newLeaf, updateErr := current.Update(idx, p.Value)
					if updateErr != nil {
						return nil, updateErr
					}
					current = newLeaf
				} else {
					// Insert: check page capacity first
					if current.IsFull(len(p.Key), len(p.Value)) {
						break // segment full, return what we have
					}
					newLeaf, insertErr := current.Insert(p.Key, p.Value)
					if insertErr != nil {
						return nil, insertErr
					}
					current = newLeaf
					totalDelta++
				}
				n++
			}

			// Advance offset after this segment (re-applied on CAS retry)
			offset = segStart + n

			return &leafMutation{
				newPageID: current.PageID(),
				delta:     totalDelta,
			}, nil
		}, MaxCASRetries)

		if err != nil {
			return count, err
		}
		advanced := offset - segStart
		count += advanced
		if advanced == 0 {
			offset++ // safety: force-advance to avoid infinite loop
			count++
		}
	}

	if a.tree.metrics != nil {
		a.tree.metrics.WriteCount.Add(int64(count))
	}

	return count, nil
}

// Delete physically removes the key from the B+Tree.
// For MVCC transactions, tombstone encoding is handled by the transaction layer;
// this adapter only handles the case where the transaction needs to physically
// restore a previous value (rollback) or write a tombstone.
// Note: StorageBackend.Delete is for physical removal; MVCC uses Set with tombstone flag.
func (a *btreeStorageAdapter) Delete(_ context.Context, key []byte) error {
	if err := a.tree.checkOpen(); err != nil {
		return err
	}

	err := writeOperation(a.tree, key, func(leaf LeafPage) (*leafMutation, error) {
		idx, found := leaf.Search(key)
		if !found {
			return nil, mvcc.ErrKeyNotFound
		}

		newLeaf, deleteErr := leaf.Delete(idx)
		if deleteErr != nil {
			return nil, deleteErr
		}
		return &leafMutation{
			newPageID: newLeaf.PageID(),
			delta:     -1,
		}, nil
	}, MaxCASRetries)

	if err == nil && a.tree.metrics != nil {
		a.tree.metrics.DeleteCount.Add(1)
	}

	return err
}
