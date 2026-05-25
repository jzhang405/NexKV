// Package btree writeOperationWithRetry — variant with configurable CAS retry limit.
package btree

import (
	"errors"
	"strings"
	"time"

	errpkg "github.com/jzhang405/NexKV/pkg/errors"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// writeOperationWithRetry is the CAS retry template with configurable maxRetries.
// Same algorithm as writeOperation, but returns ErrCASRetryExhausted instead of
// ErrCASConflict when maxRetries is reached. Used by PageDispatcher to enable
// Lealone-style re-queue (3 fast attempts → re-enqueue → yield).
func writeOperationWithRetry(b *BTree, key []byte, mutate mutateFunc, maxRetries int) error {
	var epochSlot int
	var retiredPages []model.PageID
	if b.epochMgr != nil {
		epochSlot = b.epochMgr.AllocSlot()
	}

	var searchRetryCount, splittingRetry, attempt int
	for attempt = range maxRetries {
		// Step 1: Search path to leaf (lock-free)
		path, err := searchPath(b.rootRef, key)
		if err != nil {
			searchRetryCount++
			if errors.Is(err, ErrRetry) {
				continue
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

		// Step 3: GetLeafPage
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
		curInfo := leafRef.GetPageInfo()
		if curInfo != oldInfo {
			path.ReleaseAll()
			continue
		}

		if oldLeaf.IsFull(len(key), 0) {
			splittingInfo := &PageInfo{
				PageID:    oldInfo.PageID,
				Version:   oldInfo.Version + 1,
				IsLeaf:    true,
				NodeState: NodeSplitting,
			}
			if !leafRef.CAS(oldInfo, splittingInfo) {
				path.ReleaseAll()
				continue
			}

			splitErr := b.doSplitWithSplitting(leafRef, splittingInfo, oldInfo, path, key, mutate)

			if splitErr == nil {
				return nil
			}
			if !errors.Is(splitErr, ErrCASConflict) && !errors.Is(splitErr, ErrRetry) {
				return splitErr
			}
			continue
		}

		// ---- Non-split path ----
		result, err := mutate(oldLeaf)
		if err != nil {
			path.ReleaseAll()
			return err
		}

		// CAS-first in-place update
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
				leafRef.CAS(claimInfo, finalInfo)
				path.ReleaseAll()
				b.size.Add(result.delta)
				return nil
			}
			continue
		}

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
			_ = b.storage.FreePage(result.newPageID)
			path.ReleaseAll()
			if b.metrics != nil {
				b.metrics.IncrementCASRetry()
			}
			continue
		}

		if b.epochMgr != nil {
			retiredPages = append(retiredPages, oldInfo.PageID)
		}

		b.maybeMergeAfterWrite(path, result.delta)

		path.ReleaseAll()

		if b.epochMgr != nil && len(retiredPages) > 0 {
			b.epochMgr.RetireBatch(epochSlot, retiredPages...)
		}

		b.size.Add(result.delta)
		return nil
	}

	GlobalTracer.LogOp("writeOpWithRetry.EXHAUSTED", "key", string(key), "attempt", attempt,
		"searchRetry", searchRetryCount, "splittingRetry", splittingRetry, "maxRetries", maxRetries)
	return ErrCASRetryExhausted
}
