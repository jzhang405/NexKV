// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"time"

	errpkg "github.com/jzhang405/NexKV/pkg/errors"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// leafMutation records the result of a leaf-level COW mutation.
type leafMutation struct {
	newPageID      model.PageID // the new leaf page ID after COW (or same pageID for in-place)
	delta          int64        // change in key count: +1 insert, -1 delete, 0 update
	tombstoneDelta int16        // Phase 6.5: change in tombstone count
	inPlace        bool         // CAS-first in-place update: value fits, no COW needed
	inPlaceIdx     int          // index in leaf for in-place overwrite
	inPlaceValue   []byte        // new value for in-place overwrite (applied after CAS claim)
}

// mutateFunc applies a COW mutation to a leaf page.
// Returns the mutation result or a non-retryable error.
type mutateFunc func(leaf LeafPage) (*leafMutation, error)

// handleParentCASWithSpin uses spin-waiting to handle parent CAS.
// Each spin iteration re-reads the parent, re-InsertsChild, and re-generates newPageInfo.
func (b *BTree) handleParentCASWithSpin(
	parentRef *PageRef,
	oldChildID model.PageID,
	leftChildID, rightChildID model.PageID,
	splitKey []byte,
	childIdx int,
	leftRef, rightRef *PageRef,
) (*PageInfo, NodePage, error) {
	for range MaxParentCASSpins {
		curInfo := parentRef.GetPageInfo()
		if curInfo == nil || curInfo.Redirect {
			return nil, nil, ErrCASConflict
		}

		parentRef.Retain()
		oldParent, err := b.storage.GetNodePage(curInfo.PageID)
		if err != nil {
			parentRef.Release()
			// Retry if parent page type changed concurrently (common in splits)
			if strings.Contains(err.Error(), "is not a node page") {
				continue
			}
			return nil, nil, fmt.Errorf("btree: handleParentCASWithSpin get parent: %w", err)
		}

		// ★ FIX: Re-derive childIdx from parent page — the original childIdx
		// from searchPath may be stale due to concurrent splits on the same parent.
		actualIdx := childIdx
		for ci := range oldParent.ChildCount() {
			if oldParent.GetChild(ci) == oldChildID {
				actualIdx = ci
				break
			}
		}
		if actualIdx >= oldParent.ChildCount() {
			parentRef.Release()
			return nil, nil, ErrCASConflict // oldChildID not found — parent changed drastically
		}

		newParent, err := oldParent.InsertChild(actualIdx, splitKey, leftChildID, rightChildID)
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
			// ★ FIX: Update children cache immediately after CAS.
			// handleParentCASWithSpin does InsertChild (n → n+1 children).
			// Without this, concurrent searchPath reads stale children cache
			// causing idx_out_of_bounds (idx=n but children cache only has n-1 entries).
			updateChildrenCache(parentRef, oldChildID, leftRef, rightRef, splitKey)
			return newInfo, newParent, nil
		}
		// CAS failed → continue loop
	}

	return nil, nil, ErrCASConflict
}

// writeOperation is the CAS retry template for leaf mutations.
// Uses optimistic locking: lock-free read → mutate → CAS → retry on failure.
//
// Algorithm:
//  1. searchPath → find the leaf PageRef (lock-free)
//  2. Read current PageInfo (lock-free, atomic load)
//  3. Check Splitting state → backoff if another goroutine is splitting
//  4. Get current leaf page from storage (lock-free, I/O)
//  5. Double-check PageInfo not concurrently modified
//  6. If full: CAS Splitting marker → doSplitWithSplitting (defer rollback)
//     If not full: mutate → CAS
//  7. Non-transient errors (ErrDuplicateKey etc.) return immediately
//     Transient errors (ErrCASConflict, ErrRetry) retry from step 1
//  8. Update size counter
//
// After MaxCASRetries failures, returns ErrCASConflict.
func writeOperation(b *BTree, key []byte, mutate mutateFunc) error {
	var epochSlot int
	var retiredPages []model.PageID
	if b.epochMgr != nil {
		epochSlot = b.epochMgr.AllocSlot()
	}

	var searchRetryCount, splittingRetry, attempt int
	for attempt = range MaxCASRetries {
		// Step 1: Search path to leaf (lock-free)
		path, err := searchPath(b.rootRef, key)
		if err != nil {
			searchRetryCount++
			if errors.Is(err, ErrRetry) {
				continue // Tombstone window, retry from root
			}
			return errpkg.BTreeWriteOpSearch(err)
		}

		leafRef := path.Leaf().Ref

		// Step 2: Lock-free PageInfo read
		oldInfo := leafRef.GetPageInfo()
		if oldInfo == nil || oldInfo.NodeState == NodeRedirect || oldInfo.Redirect || !oldInfo.IsLeaf || oldInfo.NodeState == NodeMerging || oldInfo.NodeState == NodeCompacting || oldInfo.NodeState == NodeInplaceUpdate {
			path.ReleaseAll()
			continue
		}
		// Splitting backoff: spin → exponential sleep → give up.
		// Phase 3: replaces runtime.Gosched() (same-P only) with time.Sleep (cross-core).
		if oldInfo.NodeState == NodeSplitting {
			splittingRetry++
			path.ReleaseAll()
			if splittingRetry > SplitBackoffMaxRetries {
				return ErrCASConflict
			}
			if splittingRetry > SpinLockBackoffThreshold {
				backoff := time.Duration(1<<min(splittingRetry-SpinLockBackoffThreshold, 20)) * time.Microsecond
				if backoff > time.Millisecond {
					backoff = time.Millisecond
				}
				time.Sleep(backoff)
			}
			continue
		}

		// Step 3: GetLeafPage (lock-free, I/O operation)
		// TOCTOU window: pInfo may be CAS'd during GetLeafPage.
		// Step 4 double-check catches stale reads; CAS provides final correctness.
		oldLeaf, err := b.storage.GetLeafPage(oldInfo.PageID)
		if err != nil {
			path.ReleaseAll()
			if strings.Contains(err.Error(), "is not a leaf page") ||
				strings.Contains(err.Error(), "is not a node page") ||
				isLeafPageError(err) {
				continue
			}
			return errpkg.BTreeWriteOpGetLeaf(err)
		}

		// Step 4: Double-check pInfo not concurrently modified
		// Pointer comparison: if pInfo changed since Step 2, retry (CAS would fail anyway).
		// This handles all states: Redirect, Splitting, nil, or any concurrent CAS.
		// Note: NodeRoot and NodeNormal are both valid for leaf writes.
		curInfo := leafRef.GetPageInfo()
		if curInfo != oldInfo {
			path.ReleaseAll()
			continue
		}

		if oldLeaf.IsFull(len(key), 0) {
			// ---- Split path ----
			// CAS mark Splitting to prevent concurrent split on same leaf
			splittingInfo := &PageInfo{
				PageID:    oldInfo.PageID,
				Version:   oldInfo.Version + 1,
				IsLeaf:    true,
				NodeState: NodeSplitting,
			}
			if !leafRef.CAS(oldInfo, splittingInfo) {
				path.ReleaseAll()
				continue // another goroutine is operating on this leaf
			}

			// Delegate to helper: defer triggers on helper return, not on loop continue
			splitErr := b.doSplitWithSplitting(leafRef, splittingInfo, oldInfo, path, key, mutate)

			if splitErr == nil {
				return nil // Split + Insert success
			}
			// Non-transient errors: return directly (defer in helper rolled back Splitting)
			if !errors.Is(splitErr, ErrCASConflict) && !errors.Is(splitErr, ErrRetry) {
				return splitErr
			}
			// Transient errors: retry (defer in helper already rolled back Splitting)
			continue
		}

		// ---- Non-split path ----
		result, err := mutate(oldLeaf)
		if err != nil {
			path.ReleaseAll()
			return fmt.Errorf("btree: write operation mutate key %q: %w", key, err)
		}

		// CAS-first in-place update: claim → overwrite → finalize
		if result.inPlace {
			rawID := uint32(oldInfo.PageID)
			claimInfo := &PageInfo{
				PageID:    oldInfo.PageID,
				Version:   oldInfo.Version + 1,
				IsLeaf:    true,
				NodeState: NodeInplaceUpdate,
			}
			if leafRef.CAS(oldInfo, claimInfo) {
				b.storage.pa.OverwriteLeafValue(rawID, result.inPlaceIdx, result.inPlaceValue)
				finalInfo := &PageInfo{
					PageID:    oldInfo.PageID,
					Version:   oldInfo.Version + 2,
					IsLeaf:    true,
					NodeState: NodeNormal,
				}
				leafRef.CAS(claimInfo, finalInfo) // best-effort
				path.ReleaseAll()
				b.size.Add(result.delta)
				return nil
			}
			continue // CAS failed → retry
		}

		// Phase 6.5: apply tombstoneDelta to COW page header before CAS publish
		if result.tombstoneDelta != 0 {
			rawID := uint32(result.newPageID)
			tc := b.storage.pa.GetTombstoneCount(rawID)
			newTC := int16(tc) + result.tombstoneDelta
			if newTC < 0 {
				newTC = 0
			}
			b.storage.pa.SetTombstoneCount(rawID, uint16(newTC))
		}

		newInfo := &PageInfo{
			PageID:  result.newPageID,
			Version: oldInfo.Version + 1,
			IsLeaf:  true,
		}

		if !leafRef.CAS(oldInfo, newInfo) {
			// CAS conflict — free new page and retry
			_ = b.storage.FreePage(result.newPageID)
			path.ReleaseAll()

			if b.metrics != nil {
				b.metrics.IncrementCASRetry()
			}

			continue
		}

		// CAS success — mark for batch retirement (flushed via defer)
		if b.epochMgr != nil {
			retiredPages = append(retiredPages, oldInfo.PageID)
		}

		// Phase 6.5: lazy merge after Delete (G1/G2/G3)
		b.maybeMergeAfterWrite(path, result.delta)

		path.ReleaseAll()

		// Flush batched retired pages
		if b.epochMgr != nil && len(retiredPages) > 0 {
			b.epochMgr.RetireBatch(epochSlot, retiredPages...)
		}

		b.size.Add(result.delta)
		return nil
	}

	GlobalTracer.LogOp("writeOp.EXHAUSTED", "key", string(key), "attempt", attempt,
		"searchRetry", searchRetryCount, "splittingRetry", splittingRetry)
	return ErrCASConflict
}

// doSplitWithSplitting executes the Split operation after Splitting CAS succeeds.
// Extracted as a separate function so that defer triggers on each call return
// (not delayed until writeOperation's for-loop iteration ends).
//
// On return:
//   - path is ReleaseAll'd (defer guaranteed, including panic paths)
//   - if pInfo still points to splittingInfo, it is rolled back to oldInfo (defer guaranteed)
func (b *BTree) doSplitWithSplitting(leafRef *PageRef, splittingInfo, oldInfo *PageInfo,
	path SearchPath, key []byte, mutate mutateFunc) error {

	// LIFO defer order: ReleaseAll first, then Splitting rollback.
	// ReleaseAll releasing path refs does not affect leafRef's pInfo CAS.
	defer func() {
		// If pInfo still points to splittingInfo, the split did not complete successfully.
		// handleRootSplit success: ReplaceRoot replaced pInfo → GetPageInfo() != splittingInfo → skip rollback.
		if leafRef.GetPageInfo() == splittingInfo {
			leafRef.CAS(splittingInfo, oldInfo) // Rollback to Normal
		}
	}()
	defer path.ReleaseAll() // Guarantee cleanup even on panic

	if len(path) < 2 {
		// Root split: handleRootSplit success → ReplaceRoot CAS replaces pInfo → defer skips rollback
		return b.handleRootSplit(leafRef, splittingInfo, path, key, mutate)
	}
	return b.handleLeafSplit(leafRef, splittingInfo, path, key, mutate)
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
		// Use path array instead of parentRef chain.
		// path[0]=root, ..., path[currentLevel]=currentRef.
		// grandparent is at path[currentLevel-1].
		if currentLevel < 1 {
			// Root internal split — currentRef IS the root (at path[0])
			return b.handleRootInternalSplit(
				currentRef, currentInfo,
				currentLeft, currentRight, splitKey,
				&toFree, &toRelease,
			)
		}
		grandparentRef := path[currentLevel-1].Ref

		// Step 3: Get child index from path (§10.10 Option A: O(1) lookup)
		// path[i].Index = child index chosen at level i
		// currentRef is at path[currentLevel], so its index in grandparent is path[currentLevel-1].Index
		idx := path[currentLevel-1].Index

		// Step 3a: Create PageRefs for split children
		currentLeftRef := NewPageRef(currentLeft.PageID(), 0, nil)
		currentRightRef := NewPageRef(currentRight.PageID(), 0, nil)
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

		// ★ FIX: Re-derive idx from physical page — path[currentLevel-1].Index
		// may be stale due to concurrent splits inserting children before our position.
		actualIdx := idx
		for ci := range oldGrandparent.ChildCount() {
			if oldGrandparent.GetChild(ci) == currentRef.pageID {
				actualIdx = ci
				break
			}
		}
		if actualIdx >= oldGrandparent.ChildCount() {
			// currentRef.pageID not in grandparent — already replaced by concurrent split
			return ErrCASConflict
		}

		newGrandparent, err := oldGrandparent.InsertChild(actualIdx, splitKey, currentLeft.PageID(), currentRight.PageID())
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

		// Retire old grandparent page (P-page)
		if b.epochMgr != nil {
			b.epochMgr.Retire(b.epochMgr.AllocSlot(), grandparentInfo.PageID)
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
		updateChildrenCache(grandparentRef, currentRef.pageID, currentLeftRef, currentRightRef, splitKey)

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
	leftRef := NewPageRef(leftPage.PageID(), 0, nil)
	rightRef := NewPageRef(rightPage.PageID(), 0, nil)
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
	newChildren := &ChildrenCache{
		Children:   []*PageRef{leftRef, rightRef},
		Separators: [][]byte{copyKey(splitKey)},
	}

	// Step 4: ReplaceRoot CAS
	if !b.rootRef.ReplaceRoot(rootInfo, newRootInfo, newChildren) {
		// CAS failed — cleanup handled by defer in handleInternalSplit
		// Explicitly free newRootPage since it has no PageRef managing it
		return ErrCASConflict
	}

	// Retire old root page (P-page)
	if b.epochMgr != nil {
		b.epochMgr.Retire(b.epochMgr.AllocSlot(), rootInfo.PageID)
	}

	// CAS succeeded — remove integrated entries from cleanup tracking
	// Remove leftID, rightID, newRootID, newRootPageID from toFree
	*toFree = (*toFree)[:len(*toFree)-4] // 2 from Split + 2 from root creation
	*toRelease = (*toRelease)[:len(*toRelease)-2]

	// Step 5: children cache already set atomically by ReplaceRoot (race condition fix)
	// No separate children.Store needed — ReplaceRoot does it immediately after CAS

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
	oldCache := oldRef.children.Load()
	if oldCache == nil {
		return // No children cache to distribute (shouldn't happen for internal nodes)
	}

	leftCount := leftPage.ChildCount() // mid+1 children
	allChildren := oldCache.Children

	if leftCount > 0 && leftCount <= len(allChildren) {
		// Distribute children: first leftCount to leftRef, rest to rightRef
		leftChildren := make([]*PageRef, leftCount)
		copy(leftChildren, allChildren[:leftCount])
		leftSeps := make([][]byte, leftCount-1)
		for i := range leftSeps {
			leftSeps[i] = copyKey(oldCache.Separators[i])
		}
		leftRef.children.Store(&ChildrenCache{
			Children:   leftChildren,
			Separators: leftSeps,
		})

		rightCount := len(allChildren) - leftCount
		if rightCount > 0 {
			rightChildren := make([]*PageRef, rightCount)
			copy(rightChildren, allChildren[leftCount:])
			// Skip separator at index leftCount-1 (the split key moved to parent)
			rightSeps := make([][]byte, rightCount-1)
			for i := range rightSeps {
				rightSeps[i] = copyKey(oldCache.Separators[leftCount+i])
			}
			rightRef.children.Store(&ChildrenCache{
				Children:   rightChildren,
				Separators: rightSeps,
			})
		}

	}
}

// updateChildrenCache atomically replaces oldChildID in parent's children cache
// with [leftRef, rightRef] and inserts splitKey as separator, using CAS loop
// for lock-free concurrency safety.
//
// ★ Immutable replacement: creates entirely new ChildrenCache on every attempt.
// The old cache is never modified — readers holding stale references continue
// to see consistent (though possibly outdated) data.
//
// ★ CAS loop guarantees linearizability: if two goroutines split different
// children of the same parent concurrently, each retry sees the latest cache
// including the other's update, so no updates are lost.
//
// Parameters:
//   - parentRef:   the parent PageRef whose children cache is being updated
//   - oldChildID:  the pageID of the child that was split
//   - leftRef:     the left child PageRef (already Retained by caller)
//   - rightRef:    the right child PageRef (already Retained by caller)
//   - splitKey:    the separator key between leftRef and rightRef
func updateChildrenCache(
	parentRef *PageRef,
	oldChildID model.PageID,
	leftRef, rightRef *PageRef,
	splitKey []byte,
) {
	for {
		curCache := parentRef.children.Load()
		if curCache == nil {
			// No cache — shouldn't happen for internal nodes after ReplaceRoot
			return
		}

		// Find oldChildID in current cache by page ID
		idx := -1
		for i, child := range curCache.Children {
			if child != nil && child.pageID == oldChildID {
				idx = i
				break
			}
		}

		if idx == -1 {
			// oldChildID not in cache — already replaced by a concurrent updateChildrenCache.
			// Check if our refs are already there.
			for _, child := range curCache.Children {
				if child != nil && child.pageID == leftRef.pageID {
					GlobalTracer.LogOp("updateChildrenCache.alreadyPresent", "oldChildID", oldChildID, "leftRefID", leftRef.pageID)
					return
				}
			}
			GlobalTracer.LogOp("updateChildrenCache.NOT_FOUND", "oldChildID", oldChildID, "cacheChildren", len(curCache.Children), "leftRefID", leftRef.pageID)
			return
		}

		GlobalTracer.LogOp("updateChildrenCache.cas", "oldChildID", oldChildID, "idx", idx, "splitKey", string(splitKey), "leftRefID", leftRef.pageID, "rightRefID", rightRef.pageID, "cacheChildren", len(curCache.Children))

		// Build entirely new cache (immutable — no mutation of curCache)
		newChildren := make([]*PageRef, len(curCache.Children)+1)
		copy(newChildren[:idx], curCache.Children[:idx])
		newChildren[idx] = leftRef
		newChildren[idx+1] = rightRef
		copy(newChildren[idx+2:], curCache.Children[idx+1:])

		newSeps := make([][]byte, len(curCache.Separators)+1)
		copy(newSeps[:idx], curCache.Separators[:idx])
		newSeps[idx] = copyKey(splitKey)
		copy(newSeps[idx+1:], curCache.Separators[idx:])

		newCache := &ChildrenCache{
			Children:   newChildren,
			Separators: newSeps,
		}

		// CAS: only succeeds if no other goroutine modified the cache since our Load()
		if parentRef.children.CompareAndSwap(curCache, newCache) {
			return // success
		}
		// CAS failed — another goroutine updated the cache. Retry with latest state.
		GlobalTracer.LogOp("updateChildrenCache.casRetry", "oldChildID", oldChildID)
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
	GlobalTracer.LogOp("handleLeafSplit.SplitStart", "pageID", leafInfo.PageID, "leftID", leftPage.PageID(), "rightID", rightPage.PageID(), "splitKey", string(splitKey))

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

	// Phase 6.5 (G4): apply tombstoneDelta to COW target page (double-COW in split path)
	if mutation.tombstoneDelta != 0 {
		rawID := uint32(mutation.newPageID)
		tc := b.storage.pa.GetTombstoneCount(rawID)
		newTC := int16(tc) + mutation.tombstoneDelta
		if newTC < 0 {
			newTC = 0
		}
		b.storage.pa.SetTombstoneCount(rawID, uint16(newTC))
	}

	// ★ B6 fix: Track orphan page (double-COW replaced original split page)
	var leftRef, rightRef *PageRef
	var orphanPageID model.PageID
	if bytes.Compare(key, splitKey) < 0 {
		// target = left → left was mutated
		leftRef = NewPageRef(mutation.newPageID, 0, nil)
		rightRef = NewPageRef(rightPage.PageID(), 0, nil)
		orphanPageID = leftPage.PageID() // leftPage replaced by double-COW
	} else {
		// target = right → right was mutated
		leftRef = NewPageRef(leftPage.PageID(), 0, nil)
		rightRef = NewPageRef(mutation.newPageID, 0, nil)
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
		leafRef.pageID, // oldChildID: use PageRef's immutable pageID (stable across COW mutations)
		leftChildID, rightChildID, splitKey, parentEntry.Index, leftRef, rightRef)
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
		// CAS failed on leaf — another goroutine set a newer pInfo on leafRef.
		// The split data (left/right pages) is already committed in parent's children
		// cache via handleParentCASWithSpin.
		// ★ FIX: Do NOT Release leftRef/rightRef! Their refCount=1 is owned by
		// parent.children[splitIdx/splitIdx+1]. Releasing would drop refCount to 0,
		// triggering freeFunc → page recycled → data loss for all subsequent operations.
		_ = b.storage.FreePage(newParent.PageID())
		return ErrCASConflict
	}

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
func (b *BTree) handleRootSplit(_ *PageRef, rootInfo *PageInfo,
	_ SearchPath, key []byte, mutate mutateFunc) error {

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

	// Phase 6.5 (G4): apply tombstoneDelta to COW target page (double-COW in split path)
	if mutation.tombstoneDelta != 0 {
		rawID := uint32(mutation.newPageID)
		tc := b.storage.pa.GetTombstoneCount(rawID)
		newTC := int16(tc) + mutation.tombstoneDelta
		if newTC < 0 {
			newTC = 0
		}
		b.storage.pa.SetTombstoneCount(rawID, uint16(newTC))
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

	// Step 6: Create PageRefs as children of root
	leftRef := NewPageRef(leftChildID, 0, nil)
	rightRef := NewPageRef(rightChildID, 0, nil)
	leftRef.Retain()
	rightRef.Retain()

	// Step 7: ReplaceRoot CAS
	newRootInfo := &PageInfo{
		PageID:    newRootPage.PageID(),
		Version:   rootInfo.Version + 1,
		IsLeaf:    false,
		NodeState: NodeRoot,
	}
	newChildren := &ChildrenCache{
		Children:   []*PageRef{leftRef, rightRef},
		Separators: [][]byte{copyKey(splitKey)},
	}

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

	// Retire old root page (P-page)
	if b.epochMgr != nil {
		b.epochMgr.Retire(b.epochMgr.AllocSlot(), rootInfo.PageID)
	}

	// Step 8: children cache already set atomically by ReplaceRoot (race condition fix)
	// No separate children.Store needed — ReplaceRoot does it immediately after CAS

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

// --- Phase 6.5: Sparse detection helpers ---

// isLeafSparse checks if a leaf page's utilization is below the given threshold.
func isLeafSparse(leaf LeafPage, threshold float64) bool {
	return leaf.Capacity() < threshold
}

// isNodeSparse checks if an internal node page's child utilization is below the given threshold.
func isNodeSparse(node NodePage, threshold float64) bool {
	return float64(node.ChildCount())/float64(MaxInternalKeys) < threshold
}

// isNodeSparse is used by handleLeafMerge and handleInternalMerge (Phase 6.5)
