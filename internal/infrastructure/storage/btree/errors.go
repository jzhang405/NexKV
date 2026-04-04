// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

// Package btree implements a concurrent B+Tree with COW semantics.
package btree

import (
	"errors"

	errpkg "github.com/jzhang405/NexKV/pkg/errors"
)

// Sentinel errors for btree operations.
// All errors are comparable via errors.Is.
var (
	// ErrCASConflict is returned when a CAS operation fails after max retries.
	ErrCASConflict = errors.New("btree: cas conflict after max retries")

	// ErrRetry is returned when a transient condition requires retry.
	// Used for SplitMarker windows where the split is not yet visible.
	ErrRetry = errors.New("btree: retry operation")

	// ErrPageFreed is returned when accessing a page that has been freed.
	ErrPageFreed = errors.New("btree: page already freed")

	// ErrKeyNotFound is returned when the requested key does not exist.
	ErrKeyNotFound = errors.New("btree: key not found")

	// ErrTreeClosed is returned when operating on a closed tree.
	ErrTreeClosed = errors.New("btree: tree closed")

	// ErrInvalidPage is returned when encountering an invalid page.
	// 使用 pkg/errors 中的统一定义，确保错误链正确传播
	ErrInvalidPage = errpkg.ErrBTreeInvalidPage

	// ErrPageFull is returned when a page has no space for a new entry.
	ErrPageFull = errors.New("btree: page full")

	// ErrPageEmpty is returned when operating on an empty page.
	ErrPageEmpty = errors.New("btree: page empty")

	// ErrDuplicateKey is returned when inserting a key that already exists.
	ErrDuplicateKey = errors.New("btree: duplicate key")

	// ErrNotImplemented is returned by methods that are not yet implemented.
	ErrNotImplemented = errors.New("btree: not implemented")
)
