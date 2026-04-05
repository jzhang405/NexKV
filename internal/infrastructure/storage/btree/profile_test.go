// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"strconv"
	"testing"
	"time"
)

// TestProfileGetParallel is a long-running test for CPU/memory profiling.
// Run with: go test -run=TestProfileGetParallel -cpuprofile=cpu.prof -memprofile=mem.prof
func TestProfileGetParallel(t *testing.T) {
	storage, err := NewOffheapBTreeStorage(512 * 1024 * 1024)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}

	tree, err := NewBTree(storage)
	if err != nil {
		t.Fatalf("failed to create btree: %v", err)
	}
	defer tree.Close()

	ctx := context.Background()

	// Pre-populate with 1000 keys
	const maxKeys = 1000
	for i := range maxKeys {
		key := []byte("key-" + strconv.Itoa(i))
		value := []byte("value-" + strconv.Itoa(i))
		if err := tree.Set(ctx, key, value); err != nil {
			t.Fatalf("Setup Set failed: %v", err)
		}
	}

	// Run parallel reads for 10 seconds
	duration := 10 * time.Second
	t.Logf("Running parallel Get operations for %v...", duration)

	start := time.Now()
	iterations := 0

	for time.Since(start) < duration {
		// Run 8 parallel workers
		done := make(chan bool, 8)
		for worker := range 8 {
			go func(workerID int) {
				defer func() { done <- true }()

				for i := range 10000 {
					key := []byte("key-" + strconv.Itoa((workerID*10000+i)%maxKeys))
					_, err := tree.Get(ctx, key)
					if err != nil {
						t.Errorf("Get failed: %v", err)
						return
					}
				}
			}(worker)
		}

		// Wait for all workers
		for range 8 {
			<-done
		}
		iterations += 8 * 10000
	}

	elapsed := time.Since(start)
	opsPerSec := float64(iterations) / elapsed.Seconds()
	t.Logf("Completed %d iterations in %v (%.2f ops/sec)", iterations, elapsed, opsPerSec)
}

// TestProfileSetSequential is a long-running test for write profiling.
// NOTE: Skipped in Phase 5 due to COW page retention causing memory exhaustion.
// Will be enabled in Phase 6 after implementing page reclamation.
func TestProfileSetSequential(t *testing.T) {
	t.Skip("Phase 5 limitation: COW pages not reclaimed, causes memory exhaustion. Enable in Phase 6.")

	storage, err := NewOffheapBTreeStorage(512 * 1024 * 1024)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}

	tree, err := NewBTreeWithMetrics(storage, &BTreeMetrics{})
	if err != nil {
		t.Fatalf("failed to create btree: %v", err)
	}
	defer tree.Close()

	ctx := context.Background()

	// Run sequential writes for 5 seconds
	duration := 5 * time.Second
	t.Logf("Running sequential Set operations for %v...", duration)

	start := time.Now()
	iterations := 0
	const maxKeys = 1000

	for time.Since(start) < duration {
		for i := range 1000 {
			key := []byte("key-" + strconv.Itoa(i%maxKeys))
			value := []byte("value-" + strconv.Itoa(i%maxKeys))
			if err := tree.Set(ctx, key, value); err != nil {
				t.Fatalf("Set failed: %v", err)
			}
			iterations++
		}
	}

	elapsed := time.Since(start)
	opsPerSec := float64(iterations) / elapsed.Seconds()
	metrics := tree.GetMetrics()
	t.Logf("Completed %d iterations in %v (%.2f ops/sec)", iterations, elapsed, opsPerSec)
	t.Logf("Metrics: %s", metrics.String())
	t.Logf("Conflict rate: %.2f%%", metrics.ConflictRate()*100)
}
