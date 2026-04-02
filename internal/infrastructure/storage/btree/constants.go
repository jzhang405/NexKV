// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import "github.com/jzhang405/NexKV/internal/domain/model"

const (
	// HeaderSize is the size of the PageHeader struct in bytes.
	// Go compiler inserts padding between deleted(uint8) and deleteEpoch(uint64)
	// for alignment, resulting in 56 bytes total.
	HeaderSize = 56

	// MaxInternalKeys is the maximum number of keys in an internal node.
	// Calculated as: (PageSize - HeaderSize) / (IndexEntrySize + avgKeySize)
	// = (4096 - 56) / (16 + 16) = 126 (based on avgKeySize=16B).
	MaxInternalKeys = 126

	// IndexEntrySize is the size of an IndexEntry (keyOff + keyLen + child) in bytes.
	IndexEntrySize = 16

	// LeafEntrySize is the size of a LeafEntry (keyOff + keyLen + valOff + valLen) in bytes.
	LeafEntrySize = 16

	// PageSize is the page size, referencing the domain model default.
	PageSize = model.DefaultPageSize

	// UsableSize is the number of usable bytes per page (excluding header).
	UsableSize = PageSize - HeaderSize

	// MergeThreshold is the capacity threshold below which a merge is triggered.
	// A page with Capacity() < MergeThreshold should be considered for merge.
	MergeThreshold = 0.5

	// MaxCASRetries is the maximum number of CAS retry attempts in writeOperation.
	MaxCASRetries = 100

	// SpinLockBackoffThreshold is the number of CAS spin attempts before yielding
	// the CPU via runtime.Gosched(). Below this threshold, the lock spins on the
	// cache line to minimize latency; above it, yields to reduce contention.
	SpinLockBackoffThreshold = 16
)
