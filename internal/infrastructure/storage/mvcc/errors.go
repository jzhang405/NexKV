// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package mvcc

import "errors"

var (
	// ErrKeyNotFound is returned when a key does not exist or is tombstoned.
	ErrKeyNotFound = errors.New("mvcc: key not found")

	// ErrValueTooShort is returned when an MVCC value is shorter than MVCCHeaderSize.
	ErrValueTooShort = errors.New("mvcc: value too short")

	// ErrInvalidFlag is returned when the flag byte is not FlagNormal or FlagTombstone.
	ErrInvalidFlag = errors.New("mvcc: invalid flag")

	// ErrZeroTimestamp is returned when BuildMVCC is called with beginTS=0.
	ErrZeroTimestamp = errors.New("mvcc: beginTS must be non-zero")

	// ErrVersionChainConflict is returned when VersionChain Prepend CAS retries are exhausted.
	ErrVersionChainConflict = errors.New("mvcc: version chain conflict")

	// ErrConflict is returned when a transaction conflict is detected (PreCheck or Apply).
	ErrConflict = errors.New("mvcc: conflict detected")

	// ErrLockTimeout is returned when KeyLock acquisition exceeds maxRetries.
	ErrLockTimeout = errors.New("mvcc: key lock timeout")

	// ErrTxCommitted is returned when an operation is attempted on an already committed transaction.
	ErrTxCommitted = errors.New("mvcc: transaction already committed")

	// ErrTxRolledBack is returned when an operation is attempted on an already rolled back transaction.
	ErrTxRolledBack = errors.New("mvcc: transaction already rolled back")
)
