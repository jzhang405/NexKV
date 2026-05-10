// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package mvcc

import "sync/atomic"

// TSGenerator generates monotonically increasing timestamps.
// Phase 2 uses local monotonic counter; distributed HLC is reserved for future phases.
type TSGenerator interface {
	// NextTS allocates the next monotonic timestamp.
	// Under CAS contention, timestamps may have gaps (e.g., 1,2,5 if retries
	// discard 3,4). This is intentional: MVCC correctness requires monotonicity,
	// not contiguity. Gaps have no semantic impact on snapshot isolation.
	// Panic on uint64 overflow (~585,000 years at 1M commits/sec).
	NextTS() uint64

	// CurrentTS returns the current timestamp without incrementing.
	// Used for GC watermark fallback when no active transactions exist.
	CurrentTS() uint64
}

// LocalTS is a local monotonic timestamp generator (Phase 2).
type LocalTS struct {
	counter atomic.Uint64
}

// NewLocalTS creates a new LocalTS. The first call to NextTS() returns 1.
func NewLocalTS() *LocalTS {
	return &LocalTS{}
}

// NextTS returns the next monotonic timestamp (starts at 1).
func (t *LocalTS) NextTS() uint64 {
	ts := t.counter.Add(1)
	if ts == 0 {
		panic("mvcc: timestamp overflow -- restart required")
	}
	return ts
}

// CurrentTS returns the current timestamp value without incrementing.
// Used by GC as watermark fallback when no active transactions exist.
func (t *LocalTS) CurrentTS() uint64 {
	return t.counter.Load()
}
