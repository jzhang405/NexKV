// Package btree key→page resolution for PageDispatcher.
package btree

import (
	"context"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// ResolvePageID traverses the BTree to find the leaf PageID that would contain key.
// Used by PageDispatcher to group keys by target page before dispatching writes.
func (b *BTree) ResolvePageID(_ context.Context, key []byte) (model.PageID, error) {
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
// Used by resolveShardPageIDs to avoid redundant ResolvePageID calls for adjacent sorted keys.
func (b *BTree) inSamePage(pageID model.PageID, key []byte) bool {
	if pageID == 0 {
		return false
	}
	// Quick estimate: ResolvePageID is the definitive answer.
	// We avoid the double traversal cost here; callers batch-check.
	return false
}
