// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package wal

import (
	"context"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestBatchFlusherGoroutineCleanup(t *testing.T) {
	defer goleak.VerifyNone(t)

	dir := t.TempDir()
	config := &WALConfig{
		Dir:         dir,
		SegmentSize: 64 * 1024 * 1024,
		SyncPolicy:  SyncPolicyGroupCommit,
	}
	wal, err := NewDiskWAL(config)
	if err != nil {
		t.Fatal(err)
	}
	defer wal.Close()

	ctx, cancel := context.WithCancel(context.Background())
	wal.StartBatchFlusher(ctx)

	// Let it run at least one tick
	time.Sleep(50 * time.Millisecond)

	cancel()
	time.Sleep(50 * time.Millisecond)
}

func TestBatchFlusherCancelBeforeStart(t *testing.T) {
	defer goleak.VerifyNone(t)

	dir := t.TempDir()
	config := &WALConfig{
		Dir:         dir,
		SegmentSize: 64 * 1024 * 1024,
	}
	wal, err := NewDiskWAL(config)
	if err != nil {
		t.Fatal(err)
	}
	defer wal.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before start
	wal.StartBatchFlusher(ctx)
	time.Sleep(50 * time.Millisecond)
}
