// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package mvcc

import (
	"context"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestGCGoroutineCleanup(t *testing.T) {
	defer goleak.VerifyNone(t)

	tsGen := NewLocalTS()
	tm := NewTxManagerWithGC(nil, tsGen, DefaultGCConfig()).(*txManager)

	ctx, cancel := context.WithCancel(context.Background())
	go tm.runGC(ctx)

	// Let it run at least one cycle
	time.Sleep(50 * time.Millisecond)

	cancel()
	// Allow goroutine to exit
	time.Sleep(50 * time.Millisecond)
}

func TestGCGoroutineCancelBeforeStart(t *testing.T) {
	defer goleak.VerifyNone(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before start

	tsGen := NewLocalTS()
	tm := NewTxManagerWithGC(nil, tsGen, DefaultGCConfig()).(*txManager)

	go tm.runGC(ctx)
	time.Sleep(50 * time.Millisecond)
}
