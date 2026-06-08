// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package btree

import (
	"context"
	"encoding/binary"
	"fmt"
	errpkg "github.com/jzhang405/NexKV/pkg/errors"

	"github.com/jzhang405/NexKV/internal/infrastructure/storage/mvcc"
)

// GetWithMeta returns the raw wire-format value and its beginTS (commitTS).
// Wire format (Phase 3 version-inline):
//
//	[Flag:1][prevFlag:1][prevBeginTS:8][prevValLen:2][prevVal:N][beginTS:8][realVal:M]
//
// Used by WAL recovery for idempotency check.
func (b *BTree) GetWithMeta(ctx context.Context, key []byte) (raw []byte, beginTS uint64, err error) {
	raw, err = b.getRawBytes(key)
	if err != nil {
		return nil, 0, err
	}
	if len(raw) < mvcc.MVCCHeaderSize+8 {
		return nil, 0, errpkg.Wrap(ErrBTreeValidationError, fmt.Sprintf("btree: GetWithMeta: value too short: %d bytes", len(raw)))
	}
	// beginTS is after prevVal: offset = 12 + prevValLen
	prevValLen := binary.BigEndian.Uint16(raw[10:12])
	beginTS = binary.BigEndian.Uint64(raw[12+prevValLen : 12+prevValLen+8])
	return raw, beginTS, nil
}
