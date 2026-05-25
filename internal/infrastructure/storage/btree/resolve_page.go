// Package btree key→page resolution for PageDispatcher.
package btree

import (
	"context"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// ResolvePageID traverses the BTree to find the leaf PageID that would contain key.
// Used by PageDispatcher to group keys by target page before dispatching writes.
// Respects context cancellation: returns ctx.Err() if ctx is done.
func (b *BTree) ResolvePageID(ctx context.Context, key []byte) (model.PageID, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	path, err := searchPath(b.rootRef, key)
	if err != nil {
		return 0, err
	}
	pid := path.Leaf().Ref.GetPageInfo().PageID
	path.ReleaseAll()
	return pid, nil
}

// inSamePage is a fast check: is key likely still in the page identified by pageID?
// Pure read optimization: false positives/negatives only affect performance, not correctness.
//
// TODO(V2): implement key range check using leaf page metadata from KeyRangeIndex.
// Currently always returns false (conservative), so every adjacent sorted key pays
// a full ResolvePageID traversal. This is correct but suboptimal for large batches.
func (b *BTree) inSamePage(pageID model.PageID, key []byte) bool {
	_ = pageID
	_ = key
	return false
}
