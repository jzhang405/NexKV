// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"bytes"
	"errors"
	"fmt"

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
	for range MaxCASRetries {
		// Step 1: Search path to leaf
		path, err := searchPath(b.storage, b.rootRef, key)
		if err != nil {
			if errors.Is(err, ErrRetry) {
				continue // Tombstone window, retry from root
			}
			return errpkg.BTreeWriteOpSearch(err)
		}

		// Step 2: Lock leaf
		leafEntry := path.Leaf()
		leafRef := leafEntry.Ref
		leafRef.Lock()

		// Step 3: Read current page info
		oldInfo := leafRef.GetPageInfo()
		if oldInfo == nil || oldInfo.Tombstone {
			// Page freed or split, retry
			leafRef.Unlock()
			path.ReleaseAll()
			continue
		}

		// Step 4: Get current leaf page handle
		oldLeaf, err := b.storage.GetLeafPage(oldInfo.PageID)
		if err != nil {
			leafRef.Unlock()
			path.ReleaseAll()
			return errpkg.BTreeWriteOpGetLeaf(err)
		}

		// Step 5: CR-08 — IsFull check BEFORE mutate
		// Extract key/value from mutate closure for IsFull check
		// Since mutate is a closure, we check by trying Insert
		isFull := oldLeaf.IsFull(len(key), 0)
		if isFull { // Approximate check; precise check needs value
			leafRef.Unlock()
			// ★ BUG FIX: Don't ReleaseAll() here — handleLeafSplit/handleRootSplit need the path.
			// The path will be released by the split handlers or after they return.

			// ★ CR-08: Root Split detection — path length < 2 means root is leaf
			if len(path) < 2 {
				splitErr := b.handleRootSplit(leafRef, oldInfo, path, key, mutate)
				path.ReleaseAll() // Release after split handling
				if splitErr == nil {
					return nil // Split + Insert completed atomically
				}
				if errors.Is(splitErr, ErrCASConflict) {
					continue
				}
				return splitErr
			}

			splitErr := b.handleLeafSplit(leafRef, oldInfo, path, key, mutate)
			path.ReleaseAll() // Release after split handling
			if splitErr == nil {
				return nil // ★ CR-08: Strong consistency — Split + Insert in one shot
			}
			if errors.Is(splitErr, ErrCASConflict) {
				continue
			}
			return splitErr // Other errors (ErrDuplicateKey, etc.)
		}

		// Step 6: Apply COW mutation (non-full path)
		result, err := mutate(oldLeaf)
		if err != nil {
			leafRef.Unlock()
			path.ReleaseAll()
			return err // non-retryable
		}

		// Step 7: CAS leaf PageInfo
		newInfo := &PageInfo{
			PageID:  result.newPageID,
			Version: oldInfo.Version + 1,
		}

		if !leafRef.CAS(oldInfo, newInfo) {
			// CAS conflict — free new page and retry
			_ = b.storage.FreePage(result.newPageID)
			leafRef.Unlock()
			path.ReleaseAll()

			// Track CAS retry
			if b.metrics != nil {
				b.metrics.IncrementCASRetry()
			}

			continue
		}

		// Step 8: Unlock leaf
		leafRef.Unlock()

		// Step 9: Propagate upward
		if parentPath := path.ParentPath(); len(parentPath) > 0 {
			propagateUpward(b, parentPath, result.newPageID, parentPath[len(parentPath)-1].Index)
		}

		// Step 10: Update size counter
		b.size.Add(result.delta)

		// Step 11: Release path
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
		if oldInfo == nil || oldInfo.Tombstone {
			// ★ B4 fix: Tombstone check — stop propagation if parent was split
			// Parent is no longer navigable, don't update it
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

// handleLeafSplit handles leaf page split with CR-08 immediate insert.
// Called when leaf is full; performs split, immediate insert, and parent update atomically.
//
// ★ CR-08 double-COW: target child is mutated immediately after split.
// Bug fixes: B1/B2/B6/B7/B18/B19
func (b *BTree) handleLeafSplit(leafRef *PageRef, leafInfo *PageInfo,
	path SearchPath, key []byte, mutate mutateFunc) error {

	// Step 1: Split leaf → left + right + splitKey
	leaf, err := b.storage.GetLeafPage(leafInfo.PageID)
	if err != nil {
		return fmt.Errorf("btree: handleLeafSplit get leaf: %w", err)
	}
	leftPage, rightPage, splitKey, err := leaf.Split()
	if err != nil {
		return err
	}

	// Step 2: CR-08 — determine target child and immediate insert
	var target LeafPage
	var sibling LeafPage
	if bytes.Compare(key, splitKey) < 0 {
		target, sibling = leftPage, rightPage
	} else {
		target, sibling = rightPage, leftPage
	}
	_ = sibling // sibling is kept in tree, not modified

	// Step 3: Mutate target (double-COW)
	mutation, err := mutate(target)
	if err != nil {
		// Cleanup split pages
		_ = b.storage.FreePage(leftPage.PageID())
		_ = b.storage.FreePage(rightPage.PageID())
		return err
	}

	// ★ B6 fix: Track orphan page (double-COW replaced original split page)
	var leftRef, rightRef *PageRef
	var orphanPageID model.PageID
	if bytes.Compare(key, splitKey) < 0 {
		// target = left → left was mutated
		leftRef = NewPageRef(mutation.newPageID, 0, nil, b.rootRef.freeFunc)
		rightRef = NewPageRef(rightPage.PageID(), 0, nil, b.rootRef.freeFunc)
		orphanPageID = leftPage.PageID() // leftPage replaced by double-COW
	} else {
		// target = right → right was mutated
		leftRef = NewPageRef(leftPage.PageID(), 0, nil, b.rootRef.freeFunc)
		rightRef = NewPageRef(mutation.newPageID, 0, nil, b.rootRef.freeFunc)
		orphanPageID = rightPage.PageID() // rightPage replaced by double-COW
	}
	leftRef.Retain()  // refCount: 0 → 1
	rightRef.Retain() // refCount: 0 → 1

	// Recycle orphan page (original split page not referenced by any PageRef)
	_ = b.storage.FreePage(orphanPageID)

	// Step 4: Parent InsertChild (COW)
	parentEntry := path[len(path)-2] // leaf's parent
	parentRef := parentEntry.Ref
	parentInfo := parentRef.GetPageInfo()
	if parentInfo == nil {
		// Parent freed, full retry
		leftRef.Release()
		rightRef.Release()
		return ErrCASConflict
	}

	oldParent, err := b.storage.GetNodePage(parentInfo.PageID)
	if err != nil {
		leftRef.Release()
		rightRef.Release()
		return err
	}

	// Determine child IDs for InsertChild
	var leftChildID, rightChildID model.PageID
	if bytes.Compare(key, splitKey) < 0 {
		leftChildID = mutation.newPageID  // target=left, left mutated
		rightChildID = rightPage.PageID() // right unchanged
	} else {
		leftChildID = leftPage.PageID()   // left unchanged
		rightChildID = mutation.newPageID // target=right, right mutated
	}

	newParent, err := oldParent.InsertChild(parentEntry.Index, splitKey, leftChildID, rightChildID)
	if err != nil {
		leftRef.Release()
		rightRef.Release()
		return err
	}

	newParentInfo := &PageInfo{
		PageID:    newParent.PageID(),
		Version:   parentInfo.Version + 1,
		Tombstone: false,
	}

	// Step 5: Parent CAS
	if !parentRef.CAS(parentInfo, newParentInfo) {
		// CAS failed → cleanup
		// ★ B7 fix: Only Release, no explicit FreePage (avoid double-free)
		// Release() calls freeFunc when refCount reaches 0
		leftRef.Release()
		rightRef.Release()
		_ = b.storage.FreePage(newParent.PageID()) // newParent has no PageRef, free explicitly
		return ErrCASConflict
	}

	// Step 6: ★ B2 fix — SetSplitMarker BEFORE Tombstone
	leafRef.SetSplitMarker(leftRef, rightRef, splitKey)
	// SplitMarker.Left → leftRef (pageID = mutation.newPageID or leftPage.PageID())
	// SplitMarker.Right → rightRef (pageID = rightPage.PageID() or mutation.newPageID)
	// Matches parent InsertChild childID exactly ✅

	// Step 7: Tombstone the old leaf (must be AFTER SplitMarker)
	// This CAS succeeds because pInfo hasn't been modified while leafRef is locked
	leafRef.CAS(leafInfo, &PageInfo{
		PageID:    leafInfo.PageID,
		Version:   leafInfo.Version + 1,
		Tombstone: true,
	})

	// Step 8: ★ B18+B19 fix — Direct children cache setting, reuse old child PageRefs
	//
	// B18 fix: GetOrCreateChildren creates new PageRef objects (refCount=0),
	//   independent from leftRef/rightRef. InvalidateChildren + Release →
	//   refCount→0 → freeFunc → page freed, but parent child pointer still
	//   points to that PageID → UAF.
	//
	// B19 fix: Instead of creating new PageRef objects, directly reuse existing
	//   PageRef objects from old children array. Prevents duplicate PageRef for
	//   same pageID → refCount confusion → UAF.
	oldChildren := parentRef.children.Load()
	// ★ BUG FIX: ChildCount = Count + 1, and InsertChild adds 1 more child
	// So new child count = (oldParent.Count() + 1) + 1 = oldParent.Count() + 2
	newChildCount := oldParent.Count() + 2 // After InsertChild: entries+1, children+1
	newChildren := make([]*PageRef, newChildCount)
	for i := range newChildCount {
		switch {
		case i == parentEntry.Index:
			newChildren[i] = leftRef // Direct insert, no extra Retain needed
		case i == parentEntry.Index+1:
			newChildren[i] = rightRef // Direct insert, no extra Retain needed
		default:
			// ★ B19 fix: Reuse existing PageRef from old children
			srcIdx := i
			if i > parentEntry.Index+1 {
				srcIdx = i - 1 // Skip replaced position
			}
			if oldChildren != nil && srcIdx < len(*oldChildren) && (*oldChildren)[srcIdx] != nil {
				newChildren[i] = (*oldChildren)[srcIdx] // Reuse: same PageRef object, refCount unchanged
			} else {
				// Defense: old children nil or OOB — build from newParent data (shouldn't happen)
				childID := newParent.GetChild(i)
				ref := NewPageRef(childID, 0, parentRef, parentRef.freeFunc)
				ref.Retain()
				newChildren[i] = ref
			}
		}
	}
	parentRef.children.Store(&newChildren) // Atomic replacement

	// ★ B18 fix: ClearSplitMarker now safe
	// leftRef/rightRef are held by newChildren (refCount ≥ 1),
	// ClearSplitMarker releases SplitMarker's Retain (refCount -1) but won't reach 0.
	leafRef.ClearSplitMarker()

	// Step 9: Update metrics + size
	b.size.Add(mutation.delta)
	// ★ RefCount tracking (B18 fix):
	//   leftRef:  Retain(Step3)=1 + Retain(SplitMarker)=1 - Release(ClearSplitMarker)=-1 = 1
	//             Held by newChildren[i] → refCount=1, safe
	//   rightRef: Same refCount=1
	//   Note: Don't Release leftRef/rightRef! Their lifecycle is managed by parent.children.
	//   Recycling: when parent.children is invalidated (split or tree close),
	//              each childRef in old children is Released (including leftRef/rightRef).
	return nil
}

// handleRootSplit handles root leaf split when tree has single leaf root.
// Creates new internal root and promotes children to first level.
//
// ★ CR-08 double-COW: target child mutated immediately after split.
// Bug fixes: B10/B11/B18/B20
func (b *BTree) handleRootSplit(leafRef *PageRef, rootInfo *PageInfo,
	path SearchPath, key []byte, mutate mutateFunc) error {

	// Step 1: Split root leaf → left + right + splitKey
	rootLeaf, err := b.storage.GetLeafPage(rootInfo.PageID)
	if err != nil {
		return fmt.Errorf("btree: handleRootSplit get leaf: %w", err)
	}
	leftPage, rightPage, splitKey, err := rootLeaf.Split()
	if err != nil {
		return err
	}

	// Step 2: CR-08 — determine target and immediate insert
	var target LeafPage
	if bytes.Compare(key, splitKey) < 0 {
		target = leftPage
	} else {
		target = rightPage
	}

	// Step 3: Mutate target (double-COW)
	mutation, err := mutate(target)
	if err != nil {
		_ = b.storage.FreePage(leftPage.PageID())
		_ = b.storage.FreePage(rightPage.PageID())
		return err
	}

	// ★ B10 fix: Track orphan page (double-COW replaced original split page)
	var orphanPageID model.PageID
	if bytes.Compare(key, splitKey) < 0 {
		orphanPageID = leftPage.PageID() // leftPage replaced by double-COW
	} else {
		orphanPageID = rightPage.PageID() // rightPage replaced by double-COW
	}

	// Step 4: Determine final left/right child IDs
	var leftChildID, rightChildID model.PageID
	if bytes.Compare(key, splitKey) < 0 {
		leftChildID = mutation.newPageID  // left mutated
		rightChildID = rightPage.PageID() // right unchanged
	} else {
		leftChildID = leftPage.PageID()   // left unchanged
		rightChildID = mutation.newPageID // right mutated
	}

	// Step 5: Create new root node page
	newRootID, err := b.storage.AllocNodePage()
	if err != nil {
		_ = b.storage.FreePage(leftPage.PageID())
		_ = b.storage.FreePage(rightPage.PageID())
		return err
	}
	newRootPage, err := b.storage.GetNodePage(newRootID)
	if err != nil {
		_ = b.storage.FreePage(newRootID)
		return err
	}

	// Insert left/right as children of new root
	// Index 0: splitKey separates leftChildID (< splitKey) and rightChildID (>= splitKey)
	newRootPage, err = newRootPage.InsertChild(0, splitKey, leftChildID, rightChildID)
	if err != nil {
		_ = b.storage.FreePage(newRootID)
		return err
	}

	// Step 6: Create PageRefs with parentRef = rootRef
	leftRef := NewPageRef(leftChildID, 0, &b.rootRef.PageRef, b.rootRef.freeFunc)
	rightRef := NewPageRef(rightChildID, 0, &b.rootRef.PageRef, b.rootRef.freeFunc)
	leftRef.Retain()
	rightRef.Retain()

	// Step 7: ReplaceRoot (D14: SetParentRef before CAS)
	newRootInfo := &PageInfo{
		PageID:    newRootPage.PageID(),
		Version:   rootInfo.Version + 1,
		Tombstone: false,
	}
	newChildren := []*PageRef{leftRef, rightRef}

	if !b.rootRef.ReplaceRoot(rootInfo, newRootInfo, newChildren) {
		// CAS failed → cleanup
		// ★ B10 fix: Same as B7 — only Release + orphan FreePage
		leftRef.Release()
		rightRef.Release()
		// Explicit recycle of orphan pages (not managed by any PageRef)
		_ = b.storage.FreePage(orphanPageID)         // Original split page replaced by double-COW
		_ = b.storage.FreePage(newRootID)            // InsertChild COW replaced blank NodePage
		_ = b.storage.FreePage(newRootPage.PageID()) // InsertChild COW output page
		return ErrCASConflict
	}

	// Step 8: ★ B20 fix — Set children BEFORE SetSplitMarker
	//
	// Safety argument (B20 fix core):
	// GetOrCreateChildren only triggers lazy-init when children == nil.
	// Before ReplaceRoot CAS, children stays nil (old leaf root has no children).
	// After ReplaceRoot CAS, children is immediately Store'd as non-nil (same goroutine, no schedule point).
	// So there is NO observable window where "pInfo already points to new internal root but children still nil":
	//   - Reader before ReplaceRoot CAS → sees old pInfo (leaf) → normal traversal
	//   - Reader after ReplaceRoot CAS + children.Store → sees new pInfo + non-nil children → uses directly
	// GetOrCreateChildren lazy-init only triggers when children == nil, which only holds before ReplaceRoot.
	//
	// Old strategy (children.Store after SetSplitMarker):
	//   ReplaceRoot CAS → SetSplitMarker → children.Store
	//   Between SetSplitMarker and children.Store, pInfo already points to new internal root but children still nil
	//   → GetOrCreateChildren triggers lazy-init → creates independent PageRef → UAF
	//
	// New strategy (B20 fix):
	//   ReplaceRoot CAS → children.Store → SetSplitMarker
	//   children.Store before SetSplitMarker, after Store children non-nil, subsequent GetOrCreateChildren doesn't trigger lazy-init
	rootChildren := []*PageRef{leftRef, rightRef}
	b.rootRef.children.Store(&rootChildren) // Atomic replacement (old children was nil)

	// Step 9: Set SplitMarker on root (for CAS window readers)
	// Readers with stale root pInfo can follow this to find correct child
	b.rootRef.SetSplitMarker(leftRef, rightRef, splitKey)

	// ★ B11 fix: Root Split does NOT need Tombstone CAS
	// Reason: ReplaceRoot already CAS'd rootRef.pInfo to newRootInfo (points to new internal root),
	// concurrent reader sees rootRef.pInfo already as new internal node, doesn't go through old leaf.
	// SplitMarker only for极少数在 ReplaceRoot CAS 窗口期读到旧 pInfo 的 reader.

	// Step 10: ClearSplitMarker — leftRef/rightRef held by rootChildren, safe to release
	b.rootRef.ClearSplitMarker()

	// Step 11: Update metrics + size
	b.size.Add(mutation.delta)
	if b.metrics != nil {
		b.metrics.IncrementSplit()
		// Note: Tree height increased — consider adding IncrementTreeHeight metric if needed
	}

	// Step 12: Cleanup orphan pages
	// ★ RefCount tracking (B18 fix):
	//   leftRef:  Retain(Step6)=1 + Retain(SplitMarker)=1 - Release(ClearSplitMarker)=-1 = 1
	//             Held by rootChildren[0] → refCount=1, safe
	//   rightRef: Same refCount=1
	//   Note: Don't Release leftRef/rightRef! Lifecycle managed by rootRef.children.
	_ = b.storage.FreePage(orphanPageID) // double-COW replaced original split page
	_ = b.storage.FreePage(newRootID)    // InsertChild COW replaced blank NodePage

	return nil
}
