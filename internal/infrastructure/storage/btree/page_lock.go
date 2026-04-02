// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"runtime"
	"sync/atomic"
)

// SchedulerLock is a lightweight spin lock for short-held critical sections.
// Suitable for BTree write operations that hold the lock for microseconds.
type SchedulerLock struct {
	state atomic.Int32 // 0 = unlocked, 1 = locked
}

// Lock spins until acquiring the lock.
func (l *SchedulerLock) Lock() {
	for !l.state.CompareAndSwap(0, 1) {
		runtime.Gosched()
	}
}

// Unlock releases the lock.
// Uses CAS to detect double-unlock — panics if the lock was already unlocked
// (I2 fix: catches defer Unlock + manual Unlock bugs).
func (l *SchedulerLock) Unlock() {
	if !l.state.CompareAndSwap(1, 0) {
		panic("btree2: unlock of unlocked SchedulerLock")
	}
}
