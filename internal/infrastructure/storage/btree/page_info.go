// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import "github.com/jzhang405/NexKV/internal/domain/model"

// NodeState describes the logical state of a node in the B+Tree.
type NodeState int8

const (
	NodeNormal    NodeState = 0 // 普通节点（内部节点或叶子）
	NodeRoot      NodeState = 1 // 根节点
	NodeRedirect  NodeState = 2 // 已分裂重定向（旧节点）
	NodeSplitting NodeState = 3 // 正在分裂（乐观锁标记，防止并发 split）
)

// PageInfo is an immutable snapshot of a page's metadata.
// Each COW mutation produces a new instance with incremented Version.
// Replaced atomically via PageRef.CAS.
type PageInfo struct {
	PageID    model.PageID
	Version   uint64
	Redirect  bool      // data structure changed (split), reader should re-navigate via NewRef
	NewRef    *PageRef  // when Redirect=true, points to the left child of the split
	IsLeaf    bool      // whether this page is a leaf (stored to avoid TOCTOU race with page reuse)
	NodeState NodeState // logical node state: Normal, Root, or Redirect
}
