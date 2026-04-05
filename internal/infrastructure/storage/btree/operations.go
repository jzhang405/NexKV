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

// handleParentCASWithSpin uses spin-waiting to handle parent CAS.
// Each spin iteration re-reads the parent, re-InsertsChild, and re-generates newPageInfo.
func (b *BTree) handleParentCASWithSpin(
	parentRef *PageRef,
	leftChildID, rightChildID model.PageID,
	splitKey []byte,
	childIdx int,
) (*PageInfo, NodePage, error) {
	for i := 0; i < MaxParentCASSpins; i++ {
		curInfo := parentRef.GetPageInfo()
		if curInfo == nil || curInfo.Redirect {
			return nil, nil, ErrCASConflict
		}

		parentRef.Retain()
		oldParent, err := b.storage.GetNodePage(curInfo.PageID)
		if err != nil {
			parentRef.Release()
			return nil, nil, err
		}

		newParent, err := oldParent.InsertChild(childIdx, splitKey, leftChildID, rightChildID)
		if err != nil {
			parentRef.Release()
			return nil, nil, err
		}

		newInfo := &PageInfo{
			PageID:    newParent.PageID(),
			Version:   curInfo.Version + 1,
			IsLeaf:    false,
			NodeState: curInfo.NodeState, // preserve root/normal
		}

		success := parentRef.CAS(curInfo, newInfo)
		parentRef.Release()

		if success {
			return newInfo, newParent, nil
		}
		// CAS failed → continue loop
	}

	return nil, nil, ErrCASConflict
}

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
		if oldInfo == nil || oldInfo.NodeState == NodeRedirect {
			// Page freed or already split — retry
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
			IsLeaf:  true,
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

// handleInternalSplit performs cascading internal node splits upward.
// Called when a parent internal node becomes full after InsertChild from a child split.
//
// Algorithm (§10.5 iterative per-level CAS loop):
//
//	Loop from currentLevel upward:
//	  1. Split current node (move-up semantics)
//	  2. InsertChild into grandparent
//	  3. Grandparent CAS
//	  4. If CAS fails → cleanup → return ErrCASConflict
//	  5. If grandparent is nil → handleRootInternalSplit
//	  6. If grandparent not full → done
//	  7. Move up one level, continue
//
// Cleanup strategy (§10.9): track allocated pages and retained refs,
// cleanup on error via defer.
func (b *BTree) handleInternalSplit(
	currentRef *PageRef,
	currentInfo *PageInfo,
	path SearchPath,
	currentLevel int,
) (retErr error) {
	// Cleanup tracking (§10.9)
	var toFree []model.PageID
	var toRelease []*PageRef

	defer func() {
		if retErr != nil {
			for _, id := range toFree {
				_ = b.storage.FreePage(id)
			}
			for _, ref := range toRelease {
				ref.Release()
			}
		}
	}()

	// Step 0: Acquire split latch — only one goroutine can split this node at a time
	if !currentRef.TryAcquireSplitLatch() {
		return ErrCASConflict
	}
	defer currentRef.ReleaseSplitLatch()

	for {
		// Step 1: Split current internal node (move-up semantics)
		currentNode, err := b.storage.GetNodePage(currentInfo.PageID)
		if err != nil {
			return fmt.Errorf("btree: handleInternalSplit get node: %w", err)
		}
		currentLeft, currentRight, splitKey, err := currentNode.Split()
		if err != nil {
			return fmt.Errorf("btree: handleInternalSplit split node: %w", err)
		}
		toFree = append(toFree, currentLeft.PageID(), currentRight.PageID())

		// Step 2: Check if we reached root (no grandparent)
		grandparentRef := currentRef.GetParentRef()
		if grandparentRef == nil {
			// Root internal split — currentRef IS the root
			return b.handleRootInternalSplit(
				currentRef, currentInfo,
				currentLeft, currentRight, splitKey,
				&toFree, &toRelease,
			)
		}

		// Step 3: Get child index from path (§10.10 Option A: O(1) lookup)
		// path[i].Index = child index chosen at level i
		// currentRef is at path[currentLevel], so its index in grandparent is path[currentLevel-1].Index
		if currentLevel < 1 {
			// Shouldn't happen — level 0 is root, handled above
			return fmt.Errorf("btree: handleInternalSplit: unexpected level %d with non-nil parent", currentLevel)
		}
		idx := path[currentLevel-1].Index

		// Step 3a: Create PageRefs for split children
		currentLeftRef := NewPageRef(currentLeft.PageID(), 0, grandparentRef, b.rootRef.freeFunc)
		currentRightRef := NewPageRef(currentRight.PageID(), 0, grandparentRef, b.rootRef.freeFunc)
		// internal split 子节点 → IsLeaf=false（刚创建无竞争，直接 Store）
		currentLeftRef.pInfo.Store(&PageInfo{
			PageID:    currentLeft.PageID(),
			Version:   1,
			IsLeaf:    false,
			NodeState: NodeNormal,
		})
		currentRightRef.pInfo.Store(&PageInfo{
			PageID:    currentRight.PageID(),
			Version:   1,
			IsLeaf:    false,
			NodeState: NodeNormal,
		})
		currentLeftRef.Retain()  // refCount: 0 → 1
		currentRightRef.Retain() // refCount: 0 → 1
		toRelease = append(toRelease, currentLeftRef, currentRightRef)

		// ★ B18/B19 fix: Distribute old children cache to left/right
		distributeChildrenAfterSplit(currentRef, currentLeftRef, currentRightRef, currentLeft)

		// Step 4: Get grandparent info
		grandparentInfo := grandparentRef.GetPageInfo()
		if grandparentInfo == nil {
			// Grandparent changed concurrently, signal retry
			return ErrCASConflict
		}

		// Step 5: Grandparent InsertChild (COW)
		oldGrandparent, err := b.storage.GetNodePage(grandparentInfo.PageID)
		if err != nil {
			return fmt.Errorf("btree: handleInternalSplit get grandparent: %w", err)
		}

		newGrandparent, err := oldGrandparent.InsertChild(idx, splitKey, currentLeft.PageID(), currentRight.PageID())
		if err != nil {
			return fmt.Errorf("btree: handleInternalSplit insert child: %w", err)
		}
		toFree = append(toFree, newGrandparent.PageID())

		newGrandparentInfo := &PageInfo{
			PageID:    newGrandparent.PageID(),
			Version:   grandparentInfo.Version + 1,
			IsLeaf:    false,
			NodeState: grandparentInfo.NodeState, // preserve root/normal
		}

		// Step 6: Grandparent CAS
		if !grandparentRef.CAS(grandparentInfo, newGrandparentInfo) {
			// CAS failed — cleanup and signal retry
			// Remove from tracking since we're about to clean up
			_ = b.storage.FreePage(newGrandparent.PageID())
			// Remove last 3 entries from toFree (newGrandparent already freed, remove left/right)
			toFree = toFree[:len(toFree)-3]
			// Release retained refs
			currentLeftRef.Release()
			currentRightRef.Release()
			// Remove last 2 entries from toRelease
			toRelease = toRelease[:len(toRelease)-2]
			return ErrCASConflict
		}

		// CAS succeeded — remove integrated entries from cleanup tracking
		// left/right pages are now owned by grandparent's children
		// newGrandparent page is now the live parent page
		toFree = toFree[:len(toFree)-3]          // Remove leftID, rightID, newGrandparentID
		toRelease = toRelease[:len(toRelease)-2] // Remove leftRef, rightRef

		// Step 7: Redirect CAS on currentRef (best-effort, CAS-R1)
		redirectInfo := &PageInfo{
			PageID:    currentInfo.PageID,
			Version:   currentInfo.Version + 1,
			IsLeaf:    currentInfo.IsLeaf,
			NodeState: NodeRedirect,
			Redirect:  true,
			NewRef:    currentLeftRef,
		}
		_ = currentRef.CAS(currentInfo, redirectInfo) // best-effort, ignore result

		// Step 8: Update grandparent children cache (B18/B19 pattern)
		updateChildrenCache(grandparentRef, idx, currentLeftRef, currentRightRef, newGrandparent)

		// Step 9: Update metrics
		if b.metrics != nil {
			b.metrics.IncrementSplit()
		}

		// Step 10: Check if grandparent is now full
		if !newGrandparent.IsFull(0, 0) {
			return nil // Done — no more cascading needed
		}

		// Step 11: Move up one level
		currentRef = grandparentRef
		currentInfo = newGrandparentInfo
		currentLevel--
		// Continue loop
	}
}

// handleRootInternalSplit handles cascading split reaching the root
// when the root is an internal node (not a leaf).
// Creates a new root one level higher, promoting the splitKey.
func (b *BTree) handleRootInternalSplit(
	rootRef *PageRef,
	rootInfo *PageInfo,
	leftPage, rightPage NodePage,
	splitKey []byte,
	toFree *[]model.PageID,
	toRelease *[]*PageRef,
) error {
	// Step 1: Create PageRefs for left/right children of new root
	leftRef := NewPageRef(leftPage.PageID(), 0, &b.rootRef.PageRef, b.rootRef.freeFunc)
	rightRef := NewPageRef(rightPage.PageID(), 0, &b.rootRef.PageRef, b.rootRef.freeFunc)
	// internal split 子节点 → IsLeaf=false（刚创建无竞争，直接 Store）
	leftRef.pInfo.Store(&PageInfo{
		PageID:    leftPage.PageID(),
		Version:   1,
		IsLeaf:    false,
		NodeState: NodeNormal,
	})
	rightRef.pInfo.Store(&PageInfo{
		PageID:    rightPage.PageID(),
		Version:   1,
		IsLeaf:    false,
		NodeState: NodeNormal,
	})
	leftRef.Retain()  // refCount: 0 → 1
	rightRef.Retain() // refCount: 0 → 1
	*toRelease = append(*toRelease, leftRef, rightRef)

	// ★ B18/B19 fix: Distribute old children cache to leftRef/rightRef
	// Without this, GetOrCreateChildren creates brand new PageRef objects (version=0)
	// that diverge from the original leaf PageRefs → data corruption.
	distributeChildrenAfterSplit(rootRef, leftRef, rightRef, leftPage)

	// Step 2: Create new root node page
	newRootID, err := b.storage.AllocNodePage()
	if err != nil {
		return fmt.Errorf("btree: handleRootInternalSplit alloc root: %w", err)
	}
	*toFree = append(*toFree, newRootID)

	newRootPage, err := b.storage.GetNodePage(newRootID)
	if err != nil {
		return fmt.Errorf("btree: handleRootInternalSplit get new root: %w", err)
	}

	// Insert left/right as children of new root
	newRootPage, err = newRootPage.InsertChild(0, splitKey, leftPage.PageID(), rightPage.PageID())
	if err != nil {
		return fmt.Errorf("btree: handleRootInternalSplit insert child: %w", err)
	}
	*toFree = append(*toFree, newRootPage.PageID())

	// Step 3: Prepare ReplaceRoot
	newRootInfo := &PageInfo{
		PageID:    newRootPage.PageID(),
		Version:   rootInfo.Version + 1,
		IsLeaf:    false,
		NodeState: NodeRoot,
	}
	newChildren := []*PageRef{leftRef, rightRef}

	// Step 4: ReplaceRoot CAS (D14: SetParentRef before CAS)
	if !b.rootRef.ReplaceRoot(rootInfo, newRootInfo, newChildren) {
		// CAS failed — cleanup handled by defer in handleInternalSplit
		// Explicitly free newRootPage since it has no PageRef managing it
		return ErrCASConflict
	}

	// CAS succeeded — remove integrated entries from cleanup tracking
	// Remove leftID, rightID, newRootID, newRootPageID from toFree
	*toFree = (*toFree)[:len(*toFree)-4] // 2 from Split + 2 from root creation
	*toRelease = (*toRelease)[:len(*toRelease)-2]

	// Step 5: Set children cache (B20 fix: immediately after ReplaceRoot)
	rootChildren := []*PageRef{leftRef, rightRef}
	b.rootRef.children.Store(&rootChildren)

	// Step 6: Redirect CAS on old root (best-effort)
	redirectInfo := &PageInfo{
		PageID:    rootInfo.PageID,
		Version:   rootInfo.Version + 1,
		IsLeaf:    rootInfo.IsLeaf,
		NodeState: NodeRedirect,
		Redirect:  true,
		NewRef:    leftRef,
	}
	_ = rootRef.CAS(rootInfo, redirectInfo) // best-effort

	// Step 7: Update metrics
	if b.metrics != nil {
		b.metrics.IncrementSplit()
		b.metrics.IncrementTreeHeight()
	}

	// Step 8: Cleanup orphan pages (COW-replaced originals)
	// newRootID was COW-replaced by InsertChild → orphan
	_ = b.storage.FreePage(newRootID)

	return nil
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
			PageID:    newNode.PageID(),
			Version:   oldInfo.Version + 1,
			IsLeaf:    false,
			NodeState: oldInfo.NodeState,
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

// distributeChildrenAfterSplit distributes the old node's children cache to the
// split halves (leftRef and rightRef). This is the B18/B19 fix for internal node splits.
//
// Without this, GetOrCreateChildren creates brand new PageRef objects (version=0)
// that diverge from the original leaf PageRefs → concurrent writes via different
// PageRef objects cause data corruption.
//
// Split semantics: if old node had count entries and count+1 children,
// with mid = count/2:
//   - Left:  entries[0..mid),  children[0..mid+1]   → mid+1 children
//   - Right: entries[mid+1..count), children[mid+1..count+1] → count-mid children
//
// Parameters:
//   - oldRef:     the node that was split (has the old children cache)
//   - leftRef:   the left half PageRef (receives first mid+1 children)
//   - rightRef:  the right half PageRef (receives remaining children)
//   - leftPage:  the left split page (used for child count)
func distributeChildrenAfterSplit(oldRef, leftRef, rightRef *PageRef, leftPage NodePage) {
	oldChildren := oldRef.children.Load()
	if oldChildren == nil {
		return // No children cache to distribute (shouldn't happen for internal nodes)
	}

	leftCount := leftPage.ChildCount() // mid+1 children
	allChildren := *oldChildren

	if leftCount <= len(allChildren) {
		// Distribute children: first leftCount to leftRef, rest to rightRef
		leftChildren := make([]*PageRef, leftCount)
		copy(leftChildren, allChildren[:leftCount])
		leftRef.children.Store(&leftChildren)

		rightCount := len(allChildren) - leftCount
		if rightCount > 0 {
			rightChildren := make([]*PageRef, rightCount)
			copy(rightChildren, allChildren[leftCount:])
			rightRef.children.Store(&rightChildren)
		}

		// Update parentRef for all children to point to their new parent
		for _, child := range allChildren[:leftCount] {
			if child != nil {
				child.SetParentRef(leftRef)
			}
		}
		for _, child := range allChildren[leftCount:] {
			if child != nil {
				child.SetParentRef(rightRef)
			}
		}
	}
}

// updateChildrenCache atomically replaces parent's children cache with a new array
// that incorporates the two new child refs at the specified split index.
// Reuses existing PageRef objects from old children (B19 fix).
//
// Parameters:
//   - parentRef:    the parent whose children cache is being updated
//   - splitIdx:     the index where oldChild was replaced by leftRef + rightRef
//   - leftRef:      the left child PageRef (already Retained by caller)
//   - rightRef:     the right child PageRef (already Retained by caller)
//   - newNodePage:  the new parent NodePage (for fallback child ID lookup)
func updateChildrenCache(
	parentRef *PageRef,
	splitIdx int,
	leftRef, rightRef *PageRef,
	newNodePage NodePage,
) {
	oldChildren := parentRef.children.Load()
	newChildCount := newNodePage.ChildCount()
	newChildren := make([]*PageRef, newChildCount)
	for i := range newChildCount {
		switch i {
		case splitIdx:
			newChildren[i] = leftRef
		case splitIdx + 1:
			newChildren[i] = rightRef
		default:
			// ★ B19 fix: Reuse existing PageRef from old children
			srcIdx := i
			if i > splitIdx+1 {
				srcIdx = i - 1 // Skip replaced position
			}
			if oldChildren != nil && srcIdx < len(*oldChildren) && (*oldChildren)[srcIdx] != nil {
				newChildren[i] = (*oldChildren)[srcIdx] // Reuse: same PageRef object, refCount unchanged
			} else {
				// Defense: old children nil or OOB — build from newNodePage data (shouldn't happen)
				childID := newNodePage.GetChild(i)
				ref := NewPageRef(childID, 0, parentRef, parentRef.freeFunc)
				ref.Retain()
				newChildren[i] = ref
			}
		}
	}
	parentRef.children.Store(&newChildren) // Atomic replacement
}

// handleLeafSplit handles leaf page split with CR-08 immediate insert.
// Called when leaf is full; performs split, immediate insert, and parent update atomically.
//
// ★ CR-08 double-COW: target child is mutated immediately after split.
// Bug fixes: B1/B2/B6/B7/B18/B19
func (b *BTree) handleLeafSplit(leafRef *PageRef, leafInfo *PageInfo,
	path SearchPath, key []byte, mutate mutateFunc) error {

	// Step 0: Acquire split latch — only one goroutine can split this leaf at a time
	if !leafRef.TryAcquireSplitLatch() {
		return ErrCASConflict
	}
	defer leafRef.ReleaseSplitLatch()

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

	// Determine child IDs for InsertChild
	var leftChildID, rightChildID model.PageID
	if bytes.Compare(key, splitKey) < 0 {
		leftChildID = mutation.newPageID  // target=left, left mutated
		rightChildID = rightPage.PageID() // right unchanged
	} else {
		leftChildID = leftPage.PageID()   // left unchanged
		rightChildID = mutation.newPageID // target=right, right mutated
	}

	// Step 5: Parent CAS with spin-waiting
	newParentInfo, newParent, err := b.handleParentCASWithSpin(parentRef,
		leftChildID, rightChildID, splitKey, parentEntry.Index)
	if err != nil {
		leftRef.Release()
		rightRef.Release()
		return err
	}

	// Step 6: Atomically set Tombstone + Redirect + NewRef (replaces SetSplitMarker + Tombstone CAS)
	// Redirect+NewRef is CAS-atomic with Tombstone — no window gap between split and redirect visibility.
	// NewRef points to leftRef so searchPath can re-navigate via parent's updated children cache.
	redirectInfo := &PageInfo{
		PageID:    leafInfo.PageID,
		Version:   leafInfo.Version + 1,
		IsLeaf:    true,
		NodeState: NodeRedirect,
		Redirect:  true,
		NewRef:    leftRef,
	}
	if !leafRef.CAS(leafInfo, redirectInfo) {
		// CAS failed: concurrent modification — release resources and retry
		leftRef.Release()
		rightRef.Release()
		_ = b.storage.FreePage(newParent.PageID())
		return ErrCASConflict
	}

	// Step 8: ★ B18+B19 fix — Direct children cache setting, reuse old child PageRefs
	updateChildrenCache(parentRef, parentEntry.Index, leftRef, rightRef, newParent)

	// ★ Cascading split: parent full after InsertChild → propagate upward
	// path structure: path[0]=root, ..., path[len-2]=parent, path[len-1]=leaf
	// parentRef is at path[len(path)-2], cascading starts from parent level.
	if newParent.IsFull(0, 0) {
		_ = b.handleInternalSplit(parentRef, newParentInfo, path, len(path)-2)
		// Best-effort: don't propagate error — CR-08 data is already committed
		// (Redirect CAS + children cache updated). Parent overflow is transient;
		// next write to this subtree will trigger another split attempt.
	}

	// Step 9: Update metrics + size
	b.size.Add(mutation.delta)
	// ★ RefCount tracking (updated for Redirect):
	//   leftRef:  Retain(Step3)=1 → Held by newChildren[i] → refCount=1, safe
	//             redirectInfo.NewRef points to leftRef but does NOT Retain (CAS-publish only).
	//   rightRef: Retain(Step3)=1 → Held by newChildren[i] → refCount=1, safe
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
		IsLeaf:    false,
		NodeState: NodeRoot,
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

	// Step 8: Set children (B20 fix: children.Store before readers can observe new pInfo)
	rootChildren := []*PageRef{leftRef, rightRef}
	b.rootRef.children.Store(&rootChildren) // Atomic replacement (old children was nil)

	// No SplitMarker needed for root split — ReplaceRoot CAS atomically replaces pInfo.
	// No Redirect needed — old root leaf is replaced by new internal root in one CAS.
	// Readers who see old pInfo still see a leaf (pre-split state), which is correct.
	// Readers who see new pInfo see an internal node with children already set.

	// Step 9: Update metrics + size
	b.size.Add(mutation.delta)
	if b.metrics != nil {
		b.metrics.IncrementSplit()
		// Note: Tree height increased — consider adding IncrementTreeHeight metric if needed
	}

	// Step 10: Cleanup orphan pages
	// ★ RefCount tracking (updated for Redirect):
	//   leftRef:  Retain(Step6)=1 → Held by rootChildren[0] → refCount=1, safe
	//   rightRef: Retain(Step6)=1 → Held by rootChildren[1] → refCount=1, safe
	//   Note: Don't Release leftRef/rightRef! Lifecycle managed by rootRef.children.
	_ = b.storage.FreePage(orphanPageID) // double-COW replaced original split page
	_ = b.storage.FreePage(newRootID)    // InsertChild COW replaced blank NodePage

	return nil
}
