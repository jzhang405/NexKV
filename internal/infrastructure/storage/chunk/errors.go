// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

// Package chunk provides Append-Only (AO) chunk file management for BTree page persistence.
package chunk

import "errors"

var (
	// ErrChunkFull indicates the chunk has no remaining space.
	ErrChunkFull = errors.New("chunk: chunk full")

	// ErrInvalidChunkHeader indicates the chunk header is corrupted or unreadable.
	ErrInvalidChunkHeader = errors.New("chunk: invalid chunk header")

	// ErrCRCMismatch indicates CRC verification failed on deserialized data.
	ErrCRCMismatch = errors.New("chunk: crc mismatch")

	// ErrPageNotFound indicates the requested page position was not found.
	ErrPageNotFound = errors.New("chunk: page not found")

	// ErrChunkClosed indicates an operation was attempted on a closed chunk manager.
	ErrChunkClosed = errors.New("chunk: chunk manager closed")

	// ErrNilDestination indicates a nil pointer was passed to Deserialize.
	ErrNilDestination = errors.New("chunk: nil destination pointer")

	// ErrInvalidPageLength indicates pageLength is outside the valid range.
	ErrInvalidPageLength = errors.New("chunk: invalid page length")
)
