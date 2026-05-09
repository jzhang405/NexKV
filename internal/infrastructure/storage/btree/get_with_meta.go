// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package btree

import (
	"context"

	"github.com/jzhang405/NexKV/internal/infrastructure/storage/mvcc"
)

// GetWithMeta returns the raw wire-format value and its beginTS (commitTS).
// Wire format: [Flag:1][beginTS:8][RealValue:N].
// Used by WAL recovery for the three-phase idempotency check.
func (b *BTree) GetWithMeta(ctx context.Context, key []byte) (raw []byte, beginTS uint64, err error) {
	mv, err := b.GetRaw(ctx, key)
	if err != nil {
		return nil, 0, err
	}

	// Rebuild raw wire format from MVCCValue fields.
	raw, buildErr := mvcc.BuildMVCC(mv.Flag, mv.BeginTS, mv.RealVal)
	if buildErr != nil {
		return nil, 0, buildErr
	}
	return raw, mv.BeginTS, nil
}
