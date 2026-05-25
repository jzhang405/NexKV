// Package btree SetWithRetry — configurable CAS retry limit for PageDispatcher.
package btree

import (
	"context"
	"errors"

	"github.com/jzhang405/NexKV/internal/infrastructure/storage/mvcc"
)

// SetWithRetry 类似 Set，但 CAS 最大重试次数可配置。
// maxRetries 控制 writeOperation 内部 CAS 重试上限。
// 超过 maxRetries 返回 ErrCASRetryExhausted；原 Set 默认 10 次。
func (b *BTree) SetWithRetry(ctx context.Context, key, value []byte, maxRetries int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.checkOpen(); err != nil {
		return err
	}

	err := writeOperationWithRetry(b, key, func(leaf LeafPage) (*leafMutation, error) {
		idx, found := leaf.Search(key)
		if found {
			return b.mutateUpdate(leaf, idx, value)
		}
		return b.mutateInsert(leaf, idx, key, value)
	}, maxRetries)

	return err
}

// mutateUpdate handles updating an existing key.
func (b *BTree) mutateUpdate(leaf LeafPage, idx int, value []byte) (*leafMutation, error) {
	raw := leaf.GetValue(idx)
	mvccVal, parseErr := mvcc.ParseMVCC(raw)
	if parseErr != nil {
		return nil, parseErr
	}
	encoded, buildErr := mvcc.BuildMVCC(mvcc.FlagNormal, b.tsGen.NextTS(), value)
	if buildErr != nil {
		return nil, buildErr
	}
	if lh, ok := leaf.(*leafPageHandle); ok && lh.TryInPlace(idx, encoded) {
		delta := int64(0)
		tombstoneDelta := int16(0)
		if mvccVal.IsTombstone() {
			delta = +1
			tombstoneDelta = -1
		}
		return &leafMutation{
			inPlace:        true,
			inPlaceIdx:     idx,
			inPlaceValue:   encoded,
			delta:          delta,
			tombstoneDelta: tombstoneDelta,
		}, nil
	}
	newLeaf, err := leaf.Update(idx, encoded)
	if err != nil {
		return nil, err
	}
	return &leafMutation{newPageID: newLeaf.PageID(), delta: 0}, nil
}

// mutateInsert handles inserting a new key-value pair.
func (b *BTree) mutateInsert(leaf LeafPage, idx int, key, value []byte) (*leafMutation, error) {
	encoded, buildErr := mvcc.BuildMVCC(mvcc.FlagNormal, b.tsGen.NextTS(), value)
	if buildErr != nil {
		return nil, buildErr
	}
	newLeaf, err := leaf.Insert(key, encoded)
	if err != nil {
		if errors.Is(err, ErrDuplicateKey) {
			return nil, err
		}
		return nil, err
	}
	return &leafMutation{newPageID: newLeaf.PageID(), delta: +1}, nil
}
