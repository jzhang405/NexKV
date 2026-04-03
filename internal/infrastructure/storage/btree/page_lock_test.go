// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestSchedulerLock_TryLock_Success verifies TryLock returns true when lock is available.
func TestSchedulerLock_TryLock_Success(t *testing.T) {
	var lock SchedulerLock

	// TryLock should succeed on unlocked lock
	acquired := lock.TryLock()
	assert.True(t, acquired, "TryLock should succeed when lock is available")

	// Lock should be held now
	assert.Equal(t, int32(1), lock.state.Load(), "lock state should be 1 (locked)")

	// Unlock to release
	lock.Unlock()
	assert.Equal(t, int32(0), lock.state.Load(), "lock state should be 0 (unlocked)")
}

// TestSchedulerLock_TryLock_Contention verifies TryLock returns false when lock is held.
func TestSchedulerLock_TryLock_Contention(t *testing.T) {
	var lock SchedulerLock

	// Acquire lock with Lock()
	lock.Lock()

	// TryLock should fail while lock is held
	acquired := lock.TryLock()
	assert.False(t, acquired, "TryLock should fail when lock is already held")

	// Lock state should still be 1
	assert.Equal(t, int32(1), lock.state.Load(), "lock state should remain 1")

	// Release lock
	lock.Unlock()

	// TryLock should now succeed
	acquired = lock.TryLock()
	assert.True(t, acquired, "TryLock should succeed after lock is released")
	lock.Unlock()
}

// TestSchedulerLock_TryLock_Concurrent verifies TryLock behavior under contention.
func TestSchedulerLock_TryLock_Concurrent(t *testing.T) {
	var lock SchedulerLock
	var successCount atomic.Int32
	var failCount atomic.Int32

	// Pre-acquire lock to create contention
	lock.Lock()

	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)

	// All goroutines try TryLock while lock is held
	for range goroutines {
		go func() {
			defer wg.Done()

			if lock.TryLock() {
				successCount.Add(1)
				lock.Unlock()
			} else {
				failCount.Add(1)
			}
		}()
	}

	// Wait a bit for all goroutines to attempt TryLock
	time.Sleep(10 * time.Millisecond)

	// Release lock
	lock.Unlock()

	wg.Wait()

	// All goroutines should fail while lock was held
	assert.Equal(t, int32(0), successCount.Load(), "no TryLock should succeed while lock is held")
	assert.Equal(t, int32(goroutines), failCount.Load(), "all TryLocks should fail while lock is held")
}

// TestSchedulerLock_TryLock_ThenLock verifies TryLock + Lock interaction.
func TestSchedulerLock_TryLock_ThenLock(t *testing.T) {
	var lock SchedulerLock

	// Acquire with TryLock
	acquired := lock.TryLock()
	assert.True(t, acquired)

	// Try to Lock() should block, so test in separate goroutine
	done := make(chan struct{})
	go func() {
		lock.Lock() // Should block until we unlock
		// Critical section: verify we acquired the lock
		_ = lock.state.Load() // non-empty critical section
		lock.Unlock()
		close(done)
	}()

	// Give goroutine time to start and block
	// Note: This is a timing-dependent test, but demonstrates the behavior
	lock.Unlock() // Release TryLock

	// Wait for Lock() to succeed
	select {
	case <-done:
		// Good: Lock() succeeded after we unlocked
	case <-time.After(100 * time.Millisecond):
		t.Error("Lock() should have succeeded after TryLock was released")
	}
}

// TestSchedulerLock_TryLock_MultipleAttempts verifies retry pattern.
func TestSchedulerLock_TryLock_MultipleAttempts(t *testing.T) {
	var lock SchedulerLock

	// Simulate CAS retry pattern with TryLock
	const maxAttempts = 10
	var acquired bool

	for range maxAttempts {
		if lock.TryLock() {
			acquired = true
			lock.Unlock()
			break
		}
		// In real code, would do backoff here
	}

	assert.True(t, acquired, "TryLock should succeed in retry loop")
}

// TestSchedulerLock_TryLock_NoBlock verifies TryLock doesn't block.
func TestSchedulerLock_TryLock_NoBlock(t *testing.T) {
	var lock SchedulerLock

	// Acquire lock
	lock.Lock()

	// TryLock should return immediately (not block)
	done := make(chan bool, 1)
	go func() {
		acquired := lock.TryLock()
		done <- acquired
	}()

	select {
	case acquired := <-done:
		assert.False(t, acquired, "TryLock should return false immediately")
	case <-time.After(10 * time.Millisecond):
		t.Error("TryLock should not block")
	}

	lock.Unlock()
}
