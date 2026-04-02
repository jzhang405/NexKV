// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

// Package btree2 implements a concurrent B+Tree with COW semantics.
package btree

import "errors"

// Sentinel errors for btree2 operations.
// All errors are comparable via errors.Is.
var (
	// ErrCASConflict is returned when a CAS operation fails after max retries.
	ErrCASConflict = errors.New("btree2: cas conflict after max retries")

	// ErrPageFreed is returned when accessing a page that has been freed.
	ErrPageFreed = errors.New("btree2: page already freed")

	// ErrKeyNotFound is returned when the requested key does not exist.
	ErrKeyNotFound = errors.New("btree2: key not found")

	// ErrTreeClosed is returned when operating on a closed tree.
	ErrTreeClosed = errors.New("btree2: tree closed")

	// ErrInvalidPage is returned when encountering an invalid page.
	ErrInvalidPage = errors.New("btree2: invalid page")

	// ErrPageFull is returned when a page has no space for a new entry.
	ErrPageFull = errors.New("btree2: page full")

	// ErrPageEmpty is returned when operating on an empty page.
	ErrPageEmpty = errors.New("btree2: page empty")

	// ErrDuplicateKey is returned when inserting a key that already exists.
	ErrDuplicateKey = errors.New("btree2: duplicate key")

	// ErrNotImplemented is returned by methods that are not yet implemented.
	ErrNotImplemented = errors.New("btree2: not implemented")
)
