// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package mvcc

import "errors"

var (
	// ErrValueTooShort is returned when an MVCC value is shorter than MVCCHeaderSize.
	ErrValueTooShort = errors.New("mvcc: value too short")

	// ErrInvalidFlag is returned when the flag byte is not FlagNormal or FlagTombstone.
	ErrInvalidFlag = errors.New("mvcc: invalid flag")

	// ErrZeroTimestamp is returned when BuildMVCC is called with beginTS=0.
	ErrZeroTimestamp = errors.New("mvcc: beginTS must be non-zero")
)
