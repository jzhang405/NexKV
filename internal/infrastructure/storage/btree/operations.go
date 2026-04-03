// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	errpkg "github.com/jzhang405/NexKV/pkg/errors"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// leafMutation records the result of a leaf-level COW mutation.
type leafMutation struct {
	newPageID model.PageID // the new leaf page ID after COW
	delta     int64        // change in key count: +1 insert, -1 delete, 0 update
}

// mutateFunc applies a COW mutation to a leaf page.
// Returns the mutation result or a non-retryable error
// (ErrKeyNotFound, ErrDuplicateKey, ErrPageFull, etc.).
type mutateFunc func(leaf LeafPage) (*leafMutation, error)

// writeOperation is the CAS retry template for leaf mutations.
//
// Algorithm (D-ops-1):
//  1. searchPath → find the leaf PageRef
//  2. Lock the leaf (SchedulerLock)
//  3. Read current PageInfo
//  4. Get current leaf page from storage
//  5. Apply COW mutation via mutateFunc (insert/update/delete)
//  6. CAS the leaf's PageInfo (old → new)
//  7. Unlock the leaf
//  8. Propagate changes upward (best-effort in Phase 5)
//  9. Update size counter
//  10. Release all path references
//
// On CAS conflict: free the new page, release path, retry from step 1.
// After MaxCASRetries failures, returns ErrCASConflict.
func writeOperation(b *BTree, key []byte, mutate mutateFunc) error {
	for attempt := 0; attempt < MaxCASRetries; attempt++ {
		// Step 1: Search path to leaf
		path, err := searchPath(b.storage, b.rootRef, key)
		if err != nil {
			return errpkg.BTreeWriteOpSearch(err)
		}

		// Step 2: Lock leaf
		leafEntry := path.Leaf()
		leafRef := leafEntry.Ref
		leafRef.Lock()

		// Step 3: Read current page info
		oldInfo := leafRef.GetPageInfo()
		if oldInfo == nil {
			leafRef.Unlock()
			path.ReleaseAll()
			return ErrPageFreed
		}

		// Step 4: Get current leaf page handle
		oldLeaf, err := b.storage.GetLeafPage(oldInfo.PageID)
		if err != nil {
			leafRef.Unlock()
			path.ReleaseAll()
			return errpkg.BTreeWriteOpGetLeaf(err)
		}

		// Step 5: Apply COW mutation
		result, err := mutate(oldLeaf)
		if err != nil {
			leafRef.Unlock()
			path.ReleaseAll()
			return err // non-retryable
		}

		// Step 6: CAS leaf PageInfo
		newInfo := &PageInfo{
			PageID:  result.newPageID,
			Version: oldInfo.Version + 1,
		}

		if !leafRef.CAS(oldInfo, newInfo) {
			// CAS conflict — free new page and retry
			_ = b.storage.FreePage(result.newPageID)
			leafRef.Unlock()
			path.ReleaseAll()
			continue
		}

		// Step 7: Unlock leaf
		leafRef.Unlock()

		// Step 8: Propagate upward (best-effort for Phase 5)
		if parentPath := path.ParentPath(); len(parentPath) > 0 {
			propagateUpward(b, parentPath, result.newPageID, parentPath[len(parentPath)-1].Index)
		}

		// Step 9: Update size counter
		b.size.Add(result.delta)

		// Step 10: Release path
		path.ReleaseAll()
		return nil
	}

	return ErrCASConflict
}

// propagateUpward updates parent PageRefs along the search path after a leaf mutation.
//
// Walks from leaf's direct parent up to root, doing COW ReplaceChild + CAS at each level.
//
// Phase 5 behavior (D-ops-2): best-effort propagation.
// CAS failure at any level stops propagation without error.
// This is safe because the leaf's pInfo is already updated — readers who reach
// the leaf via the PageRef chain will see the correct pInfo regardless of parent state.
//
// Page lifecycle (D-ops-3): old parent pages are not freed in Phase 5.
// Page reclamation is deferred to BTree.Close() which releases the entire mmap region.
func propagateUpward(b *BTree, parentPath []PathEntry, newChildID model.PageID, childIdx int) {
	// Walk from leaf's parent up to root
	for i := len(parentPath) - 1; i >= 0; i-- {
		entry := parentPath[i]
		parentRef := entry.Ref

		oldInfo := parentRef.GetPageInfo()
		if oldInfo == nil {
			return
		}

		// COW: copy parent, replace child
		oldNode, err := b.storage.GetNodePage(oldInfo.PageID)
		if err != nil {
			return
		}

		newNode, err := oldNode.ReplaceChild(childIdx, newChildID)
		if err != nil {
			return
		}

		newInfo := &PageInfo{
			PageID:  newNode.PageID(),
			Version: oldInfo.Version + 1,
		}

		if !parentRef.CAS(oldInfo, newInfo) {
			// CAS conflict — free new page and stop propagation
			_ = b.storage.FreePage(newNode.PageID())
			return
		}

		// Update tracking for next level up
		newChildID = newNode.PageID()
		if i > 0 {
			childIdx = parentPath[i-1].Index
		}
	}
}
