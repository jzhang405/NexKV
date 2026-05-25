// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package chunk

import errpkg "github.com/jzhang405/NexKV/pkg/errors"

var (
	ErrInvalidChunkHeader = errpkg.ErrChunkInvalidHeader
	ErrCRCMismatch        = errpkg.ErrChunkCRCMismatch
	ErrPageNotFound       = errpkg.ErrChunkPageNotFound
	ErrChunkClosed        = errpkg.ErrChunkClosed
	ErrNilDestination     = errpkg.ErrChunkNilDestination
	ErrInvalidPageLength  = errpkg.ErrChunkInvalidLength
	ErrChunkIDExhausted   = errpkg.ErrChunkIDExhausted
	ErrChunkHeaderError   = errpkg.ErrChunkHeaderError
	ErrChunkIOError       = errpkg.ErrChunkIOError
	ErrChunkFormatError   = errpkg.ErrChunkFormatError
	ErrChunkNotFound      = errpkg.ErrChunkNotFound
)
