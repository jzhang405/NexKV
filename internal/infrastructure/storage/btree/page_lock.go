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
// Uses exponential backoff: spins locally for the first ~16 attempts,
// then yields the CPU via runtime.Gosched() to reduce contention.
func (l *SchedulerLock) Lock() {
	for i := 0; !l.state.CompareAndSwap(0, 1); i++ {
		if i > SpinLockBackoffThreshold {
			runtime.Gosched()
		}
	}
}

// TryLock attempts to acquire the lock without blocking.
// Returns true if the lock was acquired, false otherwise.
func (l *SchedulerLock) TryLock() bool {
	return l.state.CompareAndSwap(0, 1)
}

// Unlock releases the lock.
// Uses CAS to detect double-unlock — panics if the lock was already unlocked.
func (l *SchedulerLock) Unlock() {
	if !l.state.CompareAndSwap(1, 0) {
		panic("btree: unlock of unlocked SchedulerLock")
	}
}
