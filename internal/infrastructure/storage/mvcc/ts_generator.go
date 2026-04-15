// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package mvcc

import "sync/atomic"

// TSGenerator generates monotonically increasing timestamps.
// Phase 2 uses local monotonic counter; distributed HLC is reserved for future phases.
type TSGenerator interface {
	// NextTS allocates the next monotonic timestamp.
	// Panic on uint64 overflow (~585,000 years at 1M commits/sec).
	NextTS() uint64
}

// LocalTS is a local monotonic timestamp generator (Phase 2).
type LocalTS struct {
	counter atomic.Uint64
}

// NewLocalTS creates a new LocalTS starting at 0.
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
