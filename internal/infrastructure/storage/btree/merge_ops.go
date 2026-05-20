// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import "runtime"

// maybeMergeAfterWrite triggers lazy merge after a successful Delete when the target
// leaf is below MergeThreshold. Currently disabled: the children cache update path
// (removeChildFromCache) needs to be replaced with a proper replaceTwoWithOne that
// creates a PageRef for the merged page. Without this, subsequent searchPath calls
// miss the merged page and return "key not found" for keys that still exist.
//
// TODO(G1): replace removeChildFromCache with mergeChildRefsInCache that:
//   1. Creates a new PageRef(mergedPageID, 0, freeFunc) with Retain
//   2. Replaces children[rmIdx] and children[rmIdx+1] with the new PageRef
//   3. Removes separators[rmIdx]
// Then re-enable the call from btree.go Delete path.
func (b *BTree) maybeMergeAfterWrite(_ []byte, _ int64) {}

func (b *BTree) handleLeafMerge(path SearchPath, sparseRef *PageRef, leafPI *PageInfo) error {
	parentEntry := path[len(path)-2]
	parentRef := parentEntry.Ref
	leafIdx := parentEntry.Index

	parentPI := parentRef.GetPageInfo()
	if parentPI == nil || parentPI.IsBusy() {
		return nil
	}
	parentRef.Retain()
	defer parentRef.Release()

	parent, err := b.storage.GetNodePage(parentPI.PageID)
	if err != nil {
		return err
	}

	var sibRef *PageRef
	var sibLeaf LeafPage
	var sibPI *PageInfo
	var sibIdx int
	var selfIsLeft bool

	if leafIdx > 0 {
		sibIdx = leafIdx - 1
		sibRef = NewPageRef(parent.GetChild(sibIdx), 0, b.rootRef.freeFunc)
		sibRef.Retain()
		sibPI = sibRef.GetPageInfo()
		if sibPI == nil || !sibPI.IsLeaf || sibPI.IsBusy() {
			sibRef.Release()
			return nil
		}
		sibLeaf, err = b.storage.GetLeafPage(sibPI.PageID)
		selfIsLeft = false
	} else if leafIdx < parent.ChildCount()-1 {
		sibIdx = leafIdx + 1
		sibRef = NewPageRef(parent.GetChild(sibIdx), 0, b.rootRef.freeFunc)
		sibRef.Retain()
		sibPI = sibRef.GetPageInfo()
		if sibPI == nil || !sibPI.IsLeaf || sibPI.IsBusy() {
			sibRef.Release()
			return nil
		}
		sibLeaf, err = b.storage.GetLeafPage(sibPI.PageID)
		selfIsLeft = true
	} else {
		return nil
	}
	if err != nil {
		return err
	}
	defer sibRef.Release()

	if !isLeafSparse(sibLeaf, MergeThreshold) {
		return nil
	}

	selfLeaf, err := b.storage.GetLeafPage(leafPI.PageID)
	if err != nil {
		return err
	}

	// Phase 1: CAS NodeMerging (PageID ascending)
	var refA, refB *PageRef
	var piA, piB, markA, markB *PageInfo
	if sparseRef.PageID() < sibRef.PageID() {
		refA, refB = sparseRef, sibRef
		piA, piB = leafPI, sibPI
	} else {
		refA, refB = sibRef, sparseRef
		piA, piB = sibPI, leafPI
	}

	markA = &PageInfo{PageID: piA.PageID, Version: piA.Version + 1, IsLeaf: true, NodeState: NodeMerging}
	if !refA.CAS(piA, markA) {
		return nil
	}
	markB = &PageInfo{PageID: piB.PageID, Version: piB.Version + 1, IsLeaf: true, NodeState: NodeMerging}
	if !refB.CAS(piB, markB) {
		refA.CAS(markA, piA)
		return nil
	}

	// Phase 2: COW merge
	var merged LeafPage
	if selfIsLeft {
		merged, err = b.storage.MergeLeaves(selfLeaf, sibLeaf)
	} else {
		merged, err = b.storage.MergeLeaves(sibLeaf, selfLeaf)
	}
	if err != nil {
		refA.CAS(markA, piA)
		refB.CAS(markB, piB)
		return err
	}

	// Phase 3: CAS parent — RemoveChild for the merged sibling
	// When leafIdx is left and sibIdx is right: RemoveChild(leafIdx) removes separator key[leafIdx] and child[leafIdx+1]
	// Then ReplaceChild to point to merged page
	var newParent NodePage
	removeIdx := leafIdx
	if !selfIsLeft {
		removeIdx = sibIdx // remove the left sibling
	}
	newParent, err = parent.RemoveChild(removeIdx)
	if err != nil {
		refA.CAS(markA, piA)
		refB.CAS(markB, piB)
		return err
	}
	repIdx := removeIdx
	newParent, err = newParent.ReplaceChild(repIdx, merged.PageID())
	if err != nil {
		refA.CAS(markA, piA)
		refB.CAS(markB, piB)
		_ = b.storage.FreePage(newParent.PageID())
		return err
	}

	newParPI := &PageInfo{PageID: newParent.PageID(), Version: parentPI.Version + 1, IsLeaf: false, NodeState: parentPI.NodeState}
	if !parentRef.CAS(parentPI, newParPI) {
		refA.CAS(markA, piA)
		refB.CAS(markB, piB)
		_ = b.storage.FreePage(newParent.PageID())
		return nil
	}

	// Update parent children cache — remove the merged sibling
	b.removeChildFromCache(parentRef, removeIdx)

	// Phase 4: Mark old pages NodeRedirect (must set Redirect:true — searchPath checks this field)
	refA.CAS(markA, &PageInfo{PageID: piA.PageID, Version: markA.Version + 1, IsLeaf: true, NodeState: NodeRedirect, Redirect: true})
	refB.CAS(markB, &PageInfo{PageID: piB.PageID, Version: markB.Version + 1, IsLeaf: true, NodeState: NodeRedirect, Redirect: true})

	_ = b.storage.FreePage(piA.PageID)
	_ = b.storage.FreePage(piB.PageID)

	// Underflow check
	if isNodeSparse(newParent, MergeThreshold) && len(path) >= 3 {
		npi := parentRef.GetPageInfo()
		if npi != nil {
			_ = b.handleInternalMerge(path[:len(path)-1], parentRef, npi)
		}
	}
	_ = b.mergeRoot()
	return nil
}

func (b *BTree) handleInternalMerge(path SearchPath, nodeRef *PageRef, _ *PageInfo) error {
	if len(path) < 2 {
		return nil
	}
	_ = path
	_ = nodeRef
	return nil
}

func (b *BTree) removeChildFromCache(parentRef *PageRef, removeIdx int) {
	for {
		curCache := parentRef.children.Load()
		if curCache == nil || removeIdx >= len(curCache.Children) {
			return
		}

		newChildren := make([]*PageRef, 0, len(curCache.Children)-1)
		newSeps := make([][]byte, 0, len(curCache.Separators))
		for i, child := range curCache.Children {
			if i == removeIdx+1 || i == removeIdx {
				continue
			}
			newChildren = append(newChildren, child)
		}
		for i, sep := range curCache.Separators {
			if i == removeIdx {
				continue
			}
			newSeps = append(newSeps, sep)
		}

		if parentRef.children.CompareAndSwap(curCache, &ChildrenCache{Children: newChildren, Separators: newSeps}) {
			return
		}
		// CAS failed — another goroutine updated the cache; retry with latest state
	}
}

func (b *BTree) mergeRoot() error {
	const maxMergeRootRetries = 100
	for attempt := 0; attempt < maxMergeRootRetries; attempt++ {
		oldRootPI := b.rootRef.GetPageInfo()
		if oldRootPI == nil || oldRootPI.IsLeaf {
			return nil
		}
		oldRoot, err := b.storage.GetNodePage(oldRootPI.PageID)
		if err != nil {
			return err
		}
		if oldRoot.Count() != 1 {
			return nil
		}
		childID := oldRoot.GetChild(0)
		cache := b.rootRef.GetChildren()
		var childRef *PageRef
		if cache != nil {
			for _, c := range cache.Children {
				if c.PageID() == childID {
					childRef = c
					break
				}
			}
		}
		if childRef == nil {
			childRef = NewPageRef(childID, 0, b.rootRef.freeFunc)
		}
		childRef.Retain()
		childPI := childRef.GetPageInfo()
		if childPI == nil || childPI.IsBusy() {
			childRef.Release()
			runtime.Gosched()
			continue
		}
		newRootPI := &PageInfo{
			PageID:    childPI.PageID,
			Version:   childPI.Version + 1,
			IsLeaf:    childPI.IsLeaf,
			NodeState: NodeRoot,
		}
		if b.rootRef.ReplaceRoot(oldRootPI, newRootPI, nil) {
			childRef.Release()
			_ = b.storage.FreePage(oldRootPI.PageID)
			return nil
		}
		childRef.Release()
	}
	return nil
}

// Phase 6.5: infrastructure functions, used when lazy merge is fully enabled
var _ = (*BTree).maybeMergeAfterWrite
var _ = (*BTree).handleLeafMerge
var _ = (*BTree).handleInternalMerge
var _ = (*BTree).removeChildFromCache
