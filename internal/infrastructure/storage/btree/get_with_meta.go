// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package btree

import (
	"context"
	"encoding/binary"
	errpkg "github.com/jzhang405/NexKV/pkg/errors"

	"github.com/jzhang405/NexKV/internal/infrastructure/storage/mvcc"
)

// GetWithMeta returns the raw wire-format value and its beginTS (commitTS).
// Wire format: [Flag:1][beginTS:8][RealValue:N].
// Used by WAL recovery for the three-phase idempotency check.
//
// Extracts beginTS directly from the raw MVCC header to avoid the
// ParseMVCC → BuildMVCC round-trip present in the old implementation.
func (b *BTree) GetWithMeta(ctx context.Context, key []byte) (raw []byte, beginTS uint64, err error) {
	raw, err = b.getRawBytes(key)
	if err != nil {
		return nil, 0, err
	}
	if len(raw) < mvcc.MVCCHeaderSize {
		return nil, 0, errpkg.Wrapf(ErrBTreeValidationError, "btree: GetWithMeta: value too short: %d bytes", len(raw))
	}
	beginTS = binary.BigEndian.Uint64(raw[1:9])
	return raw, beginTS, nil
}
