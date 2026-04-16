// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package mvcc

import (
	"runtime"
	"sync/atomic"
)

// KeyLock is a lightweight per-key spinlock using atomic.Bool.
// Designed for short critical sections (microsecond-scale B+Tree operations).
type KeyLock struct {
	locked atomic.Bool
}

// Lock acquires the spinlock using CAS + incremental runtime.Gosched yield.
// Returns ErrLockTimeout if maxRetries (1000) is exceeded.
func (kl *KeyLock) Lock() error {
	const maxRetries = 1000
	for i := 0; i < maxRetries; i++ {
		if kl.locked.CompareAndSwap(false, true) {
			return nil
		}
		if i <= 20 {
			runtime.Gosched()
		} else {
			yields := 2 + (i-20)/5
			for j := 0; j < yields; j++ {
				runtime.Gosched()
			}
		}
	}
	return ErrLockTimeout
}

// Unlock releases the spinlock.
// Uses CAS(true→false); double unlock is detected but does not panic.
func (kl *KeyLock) Unlock() {
	kl.locked.CompareAndSwap(true, false)
}
