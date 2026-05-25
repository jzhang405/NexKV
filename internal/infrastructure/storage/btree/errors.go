// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

// Package btree implements a concurrent B+Tree with COW semantics.
package btree

import (
	errpkg "github.com/jzhang405/NexKV/pkg/errors"
)

// Sentinel errors for btree operations.
// All errors are comparable via errors.Is.
// 这些错误统一从 pkg/errors 导入，确保跨包一致性。
var (
	// ErrCASConflict is returned when a CAS operation fails after max retries.
	ErrCASConflict = errpkg.ErrBTreeCASConflict

	// ErrRetry is returned when a transient condition requires retry.
	// Used for SplitMarker windows where the split is not yet visible.
	ErrRetry = errpkg.ErrBTreeRetry

	// ErrPageFreed is returned when accessing a page that has been freed.
	ErrPageFreed = errpkg.ErrBTreePageFreed

	// ErrKeyNotFound is returned when the requested key does not exist.
	ErrKeyNotFound = errpkg.ErrBTreeKeyNotFound

	// ErrTreeClosed is returned when operating on a closed tree.
	ErrTreeClosed = errpkg.ErrBTreeClosed

	// ErrInvalidPage is returned when encountering an invalid page.
	ErrInvalidPage = errpkg.ErrBTreeInvalidPage

	// ErrPageFull is returned when a page has no space for a new entry.
	ErrPageFull = errpkg.ErrBTreePageFull

	// ErrPageEmpty is returned when operating on an empty page.
	ErrPageEmpty = errpkg.ErrBTreePageEmpty

	// ErrDuplicateKey is returned when inserting a key that already exists.
	ErrDuplicateKey = errpkg.ErrBTreeDuplicateKey

	// ErrNotImplemented is returned by methods that are not yet implemented.
	ErrNotImplemented = errpkg.ErrBTreeNotImplemented

	// Phase 6.5: Merge/Borrow errors
	ErrBorrowSourceEmpty    = errpkg.ErrBTreeBorrowSourceEmpty
	ErrMergeNoSibling       = errpkg.ErrBTreeMergeNoSibling
	ErrBTreeDebugError      = errpkg.ErrBTreeDebugError
	ErrBTreeSearchError     = errpkg.ErrBTreeSearchError
	ErrBTreeValidationError   = errpkg.ErrBTreeValidationError
	ErrCASRetryExhausted      = errpkg.ErrBTreeCASRetryExhausted
)
