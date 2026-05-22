// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"runtime"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// maybeMergeAfterWrite triggers lazy leaf merge after a Delete when the target leaf
// becomes sparse. Called from writeOperation CAS success path before path.ReleaseAll().
//
// Prerequisites (satisfied by Epoch-based Page Reclamation):
// handleLeafMerge retires old pages via epochMgr.Retire() — no direct FreePage race.
func (b *BTree) maybeMergeAfterWrite(path SearchPath, delta int64) {
	if b.epochMgr == nil || delta >= 0 {
		return
	}
	leafEntry := path.Leaf()
	leafRef := leafEntry.Ref
	leafPI := leafRef.GetPageInfo()
	if leafPI == nil || leafPI.IsBusy() {
		return
	}
	leaf, err := b.storage.GetLeafPage(leafPI.PageID)
	if err != nil {
		return
	}
	if !isLeafSparse(leaf, MergeThreshold) {
		return
	}
	_ = b.handleLeafMerge(path, leafRef, leafPI)
}

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
		selfIsLeft = false
	} else if leafIdx < parent.ChildCount()-1 {
		sibIdx = leafIdx + 1
		selfIsLeft = true
	} else {
		return nil
	}

	// Get sibling from parent's children cache (not NewPageRef — must CAS the real PageRef)
	cache := parentRef.GetChildren()
	if cache == nil || sibIdx >= len(cache.Children) {
		return nil
	}
	sibRef = cache.Children[sibIdx]
	sibRef.Retain()
	sibPI = sibRef.GetPageInfo()
	if sibPI == nil || !sibPI.IsLeaf || sibPI.IsBusy() {
		sibRef.Release()
		return nil
	}
	sibLeaf, err = b.storage.GetLeafPage(sibPI.PageID)
	if err != nil {
		sibRef.Release()
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

	// Phase 6.5: update parent children cache with merged page ref
	b.mergeChildRefsInCache(parentRef, removeIdx, merged.PageID())

	// Phase 4: Mark old pages NodeRedirect (must set Redirect:true — searchPath checks this field)
	refA.CAS(markA, &PageInfo{PageID: piA.PageID, Version: markA.Version + 1, IsLeaf: true, NodeState: NodeRedirect, Redirect: true})
	refB.CAS(markB, &PageInfo{PageID: piB.PageID, Version: markB.Version + 1, IsLeaf: true, NodeState: NodeRedirect, Redirect: true})

	if b.epochMgr != nil {
		slot := b.epochMgr.AllocSlot()
		b.epochMgr.Retire(slot, piA.PageID)
		b.epochMgr.Retire(slot, piB.PageID)
	} else {
		_ = b.storage.FreePage(piA.PageID)
		_ = b.storage.FreePage(piB.PageID)
	}

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

func (b *BTree) handleInternalMerge(path SearchPath, nodeRef *PageRef, nodePI *PageInfo) error {
	if len(path) < 2 {
		return nil
	}
	parentEntry := path[len(path)-2]
	parentRef := parentEntry.Ref
	nodeIdx := parentEntry.Index

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
	var sibNode NodePage
	var sibPI *PageInfo
	var sibIdx int
	var selfIsLeft bool
	var separator []byte

	if nodeIdx > 0 {
		sibIdx = nodeIdx - 1
		selfIsLeft = false
		separator = parent.GetKey(sibIdx)
	} else if nodeIdx < parent.ChildCount()-1 {
		sibIdx = nodeIdx + 1
		selfIsLeft = true
		separator = parent.GetKey(nodeIdx)
	} else {
		return nil
	}

	// Get sibling from parent's children cache (not NewPageRef — must CAS the real PageRef)
	cache := parentRef.GetChildren()
	if cache == nil || sibIdx >= len(cache.Children) {
		return nil
	}
	sibRef = cache.Children[sibIdx]
	sibRef.Retain()
	sibPI = sibRef.GetPageInfo()
	if sibPI == nil || sibPI.IsLeaf || sibPI.IsBusy() {
		sibRef.Release()
		return nil
	}
	sibNode, err = b.storage.GetNodePage(sibPI.PageID)
	if err != nil {
		sibRef.Release()
		return err
	}
	defer sibRef.Release()

	if !isNodeSparse(sibNode, MergeThreshold) {
		return nil
	}

	selfNode, err := b.storage.GetNodePage(nodePI.PageID)
	if err != nil {
		return err
	}

	// Phase 1: CAS NodeMerging (PageID ascending)
	var refA, refB *PageRef
	var piA, piB, markA, markB *PageInfo
	if nodeRef.PageID() < sibRef.PageID() {
		refA, refB = nodeRef, sibRef
		piA, piB = nodePI, sibPI
	} else {
		refA, refB = sibRef, nodeRef
		piA, piB = sibPI, nodePI
	}

	markA = &PageInfo{PageID: piA.PageID, Version: piA.Version + 1, IsLeaf: false, NodeState: NodeMerging}
	if !refA.CAS(piA, markA) {
		return nil
	}
	markB = &PageInfo{PageID: piB.PageID, Version: piB.Version + 1, IsLeaf: false, NodeState: NodeMerging}
	if !refB.CAS(piB, markB) {
		refA.CAS(markA, piA)
		return nil
	}

	// Phase 2: MergeNodes (merge-only — two sparse internal nodes always fit in one page:
	// Count <= 61+61+1 = 123 <= MaxInternalKeys=126)
	var merged NodePage
	if selfIsLeft {
		merged, err = b.storage.MergeNodes(selfNode, sibNode, separator)
	} else {
		merged, err = b.storage.MergeNodes(sibNode, selfNode, separator)
	}
	if err != nil {
		refA.CAS(markA, piA)
		refB.CAS(markB, piB)
		return err
	}

	// Phase 3: COW parent — RemoveChild + ReplaceChild
	var newParent NodePage
	removeIdx := nodeIdx
	if !selfIsLeft {
		removeIdx = sibIdx
	}
	newParent, err = parent.RemoveChild(removeIdx)
	if err != nil {
		refA.CAS(markA, piA)
		refB.CAS(markB, piB)
		return err
	}
	newParent, err = newParent.ReplaceChild(removeIdx, merged.PageID())
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

	// Update children cache
	b.mergeChildRefsInCache(parentRef, removeIdx, merged.PageID())

	// Phase 4: Mark old pages NodeRedirect
	refA.CAS(markA, &PageInfo{PageID: piA.PageID, Version: markA.Version + 1, IsLeaf: false, NodeState: NodeRedirect, Redirect: true})
	refB.CAS(markB, &PageInfo{PageID: piB.PageID, Version: markB.Version + 1, IsLeaf: false, NodeState: NodeRedirect, Redirect: true})

	if b.epochMgr != nil {
		slot := b.epochMgr.AllocSlot()
		b.epochMgr.Retire(slot, piA.PageID)
		b.epochMgr.Retire(slot, piB.PageID)
	} else {
		_ = b.storage.FreePage(piA.PageID)
		_ = b.storage.FreePage(piB.PageID)
	}

	// Underflow: recurse upward if parent is now sparse
	if isNodeSparse(newParent, MergeThreshold) && len(path) >= 3 {
		npi := parentRef.GetPageInfo()
		if npi != nil {
			_ = b.handleInternalMerge(path[:len(path)-1], parentRef, npi)
		}
	}

	_ = b.mergeRoot()
	return nil
}

// mergeChildRefsInCache replaces two children at [rmIdx, rmIdx+1] with one PageRef
// for the merged page, and removes the separator at rmIdx.
// Unlike removeChildFromCache (which just drops refs), this inserts the merged
// page's PageRef so searchPath can navigate through the post-merge tree correctly.
func (b *BTree) mergeChildRefsInCache(parentRef *PageRef, rmIdx int, mergedPageID model.PageID) {
	for {
		curCache := parentRef.children.Load()
		if curCache == nil || rmIdx+1 >= len(curCache.Children) {
			return
		}
		if rmIdx >= len(curCache.Separators) {
			return
		}

		// Create new merged PageRef
		mergedRef := NewPageRef(mergedPageID, 0, nil) // nil freeFunc: page lifecycle managed by tree, not cache
		mergedRef.Retain()

		// Build new children: [0..rmIdx) + mergedRef + (rmIdx+2..]
		newLen := len(curCache.Children) - 1
		newChildren := make([]*PageRef, newLen)
		copy(newChildren, curCache.Children[:rmIdx])
		newChildren[rmIdx] = mergedRef
		copy(newChildren[rmIdx+1:], curCache.Children[rmIdx+2:])

		// Build new separators: all except rmIdx
		newSeps := make([][]byte, 0, len(curCache.Separators)-1)
		for i, sep := range curCache.Separators {
			if i == rmIdx {
				continue
			}
			newSeps = append(newSeps, sep)
		}

		if parentRef.children.CompareAndSwap(curCache, &ChildrenCache{Children: newChildren, Separators: newSeps}) {
			return
		}
		// CAS failed — another goroutine updated the cache; release mergedRef and retry
		mergedRef.Release()
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
			childRef = NewPageRef(childID, 0, nil)
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
		childCache := childRef.GetChildren()
		if b.rootRef.ReplaceRoot(oldRootPI, newRootPI, childCache) {
			childRef.Release()
			_ = b.storage.FreePage(oldRootPI.PageID)
			return nil
		}
		childRef.Release()
	}
	return nil
}

// Phase 6.5: infrastructure functions, used when lazy merge is fully enabled
