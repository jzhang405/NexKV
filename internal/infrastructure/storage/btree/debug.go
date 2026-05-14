// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"fmt"
	"strings"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// PrintTree returns a multi-line string representation of the B+Tree.
func (b *BTree) PrintTree() string {
	var sb strings.Builder
	rootPI := b.RootPage().GetPageInfo()
	if rootPI == nil {
		return "<empty tree>"
	}
	b.printSubtree(rootPI.PageID, rootPI.IsLeaf, 0, &sb)
	return sb.String()
}

func (b *BTree) printSubtree(pageID model.PageID, isLeaf bool, depth int, sb *strings.Builder) {
	indent := strings.Repeat("  ", depth)
	if isLeaf {
		leaf, err := b.storage.GetLeafPage(pageID)
		if err != nil {
			sb.WriteString(fmt.Sprintf("%s[Leaf %d: ERROR %v]\n", indent, pageID, err))
			return
		}
		sb.WriteString(fmt.Sprintf("%s[Leaf %d: %d keys]", indent, pageID, leaf.Count()))
		if leaf.Count() > 0 {
			sb.WriteString(fmt.Sprintf(" [%s..%s]", truncate(string(leaf.GetKey(0)), 20),
				truncate(string(leaf.GetKey(leaf.Count()-1)), 20)))
		}
		sb.WriteString("\n")
		return
	}
	node, err := b.storage.GetNodePage(pageID)
	if err != nil {
		sb.WriteString(fmt.Sprintf("%s[Node %d: ERROR %v]\n", indent, pageID, err))
		return
	}
	count := node.Count()
	sb.WriteString(fmt.Sprintf("%s[Node %d: %d keys, %d children]\n", indent, pageID, count, node.ChildCount()))
	for i := 0; i < count; i++ {
		sb.WriteString(fmt.Sprintf("%s  key[%d] = %s\n", indent, i, truncate(string(node.GetKey(i)), 30)))
	}
	for i := 0; i < node.ChildCount(); i++ {
		childID := node.GetChild(i)
		// Determine if this child is leaf or node
		childRef := NewPageRef(childID, 0, b.rootRef.freeFunc)
		childPI := childRef.GetPageInfo()
		childIsLeaf := childPI != nil && childPI.IsLeaf
		b.printSubtree(childID, childIsLeaf, depth+1, sb)
	}
}

// AssertInvariants validates B+Tree structural invariants. Returns nil if all checks pass.
func (b *BTree) AssertInvariants() error {
	rootPI := b.RootPage().GetPageInfo()
	if rootPI == nil {
		return fmt.Errorf("btree: nil root")
	}
	return b.assertNodeInvariants(rootPI.PageID, rootPI.IsLeaf, nil, nil)
}

func (b *BTree) assertNodeInvariants(pageID model.PageID, isLeaf bool, minKey, maxKey []byte) error {
	if isLeaf {
		return b.assertLeafInvariants(pageID, minKey, maxKey)
	}
	return b.assertInternalInvariants(pageID, minKey, maxKey)
}

func (b *BTree) assertLeafInvariants(pageID model.PageID, minKey, maxKey []byte) error {
	leaf, err := b.storage.GetLeafPage(pageID)
	if err != nil {
		return fmt.Errorf("btree: get leaf %d: %w", pageID, err)
	}
	count := leaf.Count()
	for i := 1; i < count; i++ {
		prev := leaf.GetKey(i - 1)
		curr := leaf.GetKey(i)
		if string(prev) >= string(curr) {
			return fmt.Errorf("btree: leaf %d: key[%d]=%q >= key[%d]=%q",
				pageID, i-1, prev, i, curr)
		}
	}
	if minKey != nil && count > 0 && string(leaf.GetKey(0)) < string(minKey) {
		return fmt.Errorf("btree: leaf %d: first key %q < min key %q", pageID, leaf.GetKey(0), minKey)
	}
	if maxKey != nil && count > 0 && string(leaf.GetKey(count-1)) > string(maxKey) {
		return fmt.Errorf("btree: leaf %d: last key %q > max key %q", pageID, leaf.GetKey(count-1), maxKey)
	}
	return nil
}

func (b *BTree) assertInternalInvariants(pageID model.PageID, minKey, maxKey []byte) error {
	node, err := b.storage.GetNodePage(pageID)
	if err != nil {
		return fmt.Errorf("btree: get node %d: %w", pageID, err)
	}
	count := node.Count()
	for i := 1; i < count; i++ {
		prev := node.GetKey(i - 1)
		curr := node.GetKey(i)
		if string(prev) >= string(curr) {
			return fmt.Errorf("btree: node %d: key[%d]=%q >= key[%d]=%q",
				pageID, i-1, prev, i, curr)
		}
	}
	if node.ChildCount() != count+1 {
		return fmt.Errorf("btree: node %d: child count %d != key count %d + 1",
			pageID, node.ChildCount(), count)
	}
	// Recurse into children with key range constraints
	for i := 0; i < count; i++ {
		childID := node.GetChild(i)
		var childMin, childMax []byte
		if i > 0 {
			childMin = node.GetKey(i - 1)
		} else {
			childMin = minKey
		}
		if i < count-1 {
			childMax = node.GetKey(i)
		} else {
			childMax = maxKey
		}
		childRef := NewPageRef(childID, 0, b.rootRef.freeFunc)
		childPI := childRef.GetPageInfo()
		if childPI == nil {
			return fmt.Errorf("btree: node %d: child %d has nil PageInfo", pageID, i)
		}
		if err := b.assertNodeInvariants(childID, childPI.IsLeaf, childMin, childMax); err != nil {
			return err
		}
	}
	// Last child (extraChild)
	lastChildID := node.GetChild(count)
	lastRef := NewPageRef(lastChildID, 0, b.rootRef.freeFunc)
	lastPI := lastRef.GetPageInfo()
	if lastPI == nil {
		return fmt.Errorf("btree: node %d: last child %d has nil PageInfo", pageID, lastChildID)
	}
	lastMin := node.GetKey(count - 1)
	if err := b.assertNodeInvariants(lastChildID, lastPI.IsLeaf, lastMin, maxKey); err != nil {
		return err
	}
	return nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

