// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package mvcc

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestKeyLock_Basic(t *testing.T) {
	kl := &KeyLock{}
	if err := kl.Lock(); err != nil {
		t.Fatalf("Lock failed: %v", err)
	}
	kl.Unlock()
	// Should be able to lock again
	if err := kl.Lock(); err != nil {
		t.Fatalf("second Lock failed: %v", err)
	}
	kl.Unlock()
}

func TestKeyLock_DoubleUnlock(t *testing.T) {
	kl := &KeyLock{}
	kl.Lock()
	kl.Unlock()
	// Double unlock should not panic
	kl.Unlock()
}

func TestKeyLock_Concurrent(t *testing.T) {
	kl := &KeyLock{}
	var counter atomic.Int64
	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			if err := kl.Lock(); err != nil {
				t.Errorf("Lock failed: %v", err)
				return
			}
			counter.Add(1)
			kl.Unlock()
		}()
	}
	wg.Wait()

	if counter.Load() != goroutines {
		t.Fatalf("expected counter=%d, got %d", goroutines, counter.Load())
	}
}
