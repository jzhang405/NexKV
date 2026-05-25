// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package mvcc

import errpkg "github.com/jzhang405/NexKV/pkg/errors"

var (
	ErrKeyNotFound          = errpkg.ErrMVCCKeyNotFound
	ErrValueTooShort        = errpkg.ErrMVCCValueTooShort
	ErrInvalidFlag          = errpkg.ErrMVCCInvalidFlag
	ErrZeroTimestamp        = errpkg.ErrMVCCZeroTimestamp
	ErrVersionChainConflict = errpkg.ErrMVCCVersionChainConflict
	ErrConflict             = errpkg.ErrMVCCConflict
	ErrLockTimeout          = errpkg.ErrMVCCLockTimeout
	ErrTxCommitted          = errpkg.ErrMVCCTxCommitted
	ErrTxRolledBack         = errpkg.ErrMVCCTxRolledBack
)
