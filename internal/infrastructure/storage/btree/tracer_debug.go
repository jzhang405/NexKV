// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

//go:build enable_tracer

package btree

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// TestTracer is an optimized tracer for testing.
// Uses async channel-based logging to minimize lock contention,
// fine-grained RWMutex for ref/page tracking, and capped buffer to prevent OOM.
type TestTracer struct {
	t  testing.TB
	wg sync.WaitGroup

	// Async log pipeline
	logCh  chan string
	done   chan struct{}
	closed atomic.Bool

	// Log buffer — written by consumer goroutine only after Close drains the channel
	bufferMu   sync.Mutex
	logBuffer  []string
	bufferSize atomic.Int32
	maxBufLen  int

	// Fine-grained tracking
	refMu   sync.RWMutex
	refMap  map[uintptr]int
	pageMu  sync.RWMutex
	pageMap map[model.PageID]string
}

// NewTestTracer creates an optimized TestTracer.
// maxBufferLen: log buffer cap (auto-clears when exceeded); recommend 10000.
// logChanSize: async channel size; recommend 100000 for high-frequency tests.
func NewTestTracer(t testing.TB, maxBufferLen, logChanSize int) *TestTracer {
	if maxBufferLen <= 0 {
		maxBufferLen = 10000
	}
	if logChanSize <= 0 {
		logChanSize = 100000
	}
	tt := &TestTracer{
		t:         t,
		logCh:     make(chan string, logChanSize),
		done:      make(chan struct{}),
		logBuffer: make([]string, 0, maxBufferLen),
		maxBufLen: maxBufferLen,
		refMap:    make(map[uintptr]int),
		pageMap:   make(map[model.PageID]string),
	}
	tt.wg.Add(1)
	go tt.consumeLogs()
	return tt
}

// consumeLogs drains the log channel into the buffer.
// Runs in a single background goroutine — no lock needed for logBuffer writes.
func (t *TestTracer) consumeLogs() {
	defer t.wg.Done()
	for {
		select {
		case msg, ok := <-t.logCh:
			if !ok {
				return
			}
			t.logBuffer = append(t.logBuffer, msg)
			t.bufferSize.Add(1)
			// Auto-clear when buffer exceeds cap to prevent OOM
			if len(t.logBuffer) >= t.maxBufLen {
				t.logBuffer = t.logBuffer[:0]
				t.bufferSize.Store(0)
			}
		case <-t.done:
			return
		}
	}
}

func (t *TestTracer) LogOp(op string, args ...any) {
	msg := fmt.Sprintf("[%s] %v", time.Now().Format(time.RFC3339Nano), op)
	if len(args) > 0 {
		msg += fmt.Sprintf(" %v", args)
	}
	t.sendAsync(msg)
}

func (t *TestTracer) LogPageRefOp(ref *PageRef, op string, args ...any) {
	msg := fmt.Sprintf("[%s][PageRef:%p][%s] pageID=%d",
		time.Now().Format(time.RFC3339Nano), ref, op, ref.pageID)
	if len(args) > 0 {
		msg += fmt.Sprintf(", args=%v", args)
	}
	t.sendAsync(msg)

	// Fine-grained ref tracking
	t.refMu.Lock()
	refPtr := uintptr(unsafe.Pointer(ref))
	if op == "Retain" {
		t.refMap[refPtr]++
	} else if op == "Release" {
		t.refMap[refPtr]--
	}
	t.refMu.Unlock()
}

func (t *TestTracer) LogPageOp(pageID model.PageID, op string, args ...any) {
	msg := fmt.Sprintf("[%s][Page:%d][%s] %v",
		time.Now().Format(time.RFC3339Nano), pageID, op, args)
	t.sendAsync(msg)

	t.pageMu.Lock()
	t.pageMap[pageID] = op
	t.pageMu.Unlock()
}

func (t *TestTracer) LogPageData(pageID model.PageID, desc string, data any) {
	msg := fmt.Sprintf("[%s][PageData:%d][%s] %#v",
		time.Now().Format(time.RFC3339Nano), pageID, desc, data)
	t.sendAsync(msg)
}

func (t *TestTracer) WithContext(ctx context.Context) Tracer {
	return t
}

// sendAsync writes msg to the channel, with fallback to sync buffer write if channel is full.
func (t *TestTracer) sendAsync(msg string) {
	if t.closed.Load() {
		return
	}
	select {
	case t.logCh <- msg:
	default:
		// Channel full — fallback to sync write (avoid blocking caller)
		t.bufferMu.Lock()
		t.logBuffer = append(t.logBuffer, msg)
		t.bufferSize.Add(1)
		if len(t.logBuffer) >= t.maxBufLen {
			t.logBuffer = t.logBuffer[:0]
			t.bufferSize.Store(0)
		}
		t.bufferMu.Unlock()
	}
}

// DumpLogs returns a copy of the current log buffer.
func (t *TestTracer) DumpLogs() []string {
	t.bufferMu.Lock()
	defer t.bufferMu.Unlock()
	out := make([]string, len(t.logBuffer))
	copy(out, t.logBuffer)
	return out
}

// DumpToFile drains the channel, then writes all buffered logs to file.
func (t *TestTracer) DumpToFile(path string) error {
	t.drainChannel()

	t.bufferMu.Lock()
	defer t.bufferMu.Unlock()

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create tracer dump file: %w", err)
	}
	defer f.Close()

	buf := make([]byte, 0, 4096)
	for i, log := range t.logBuffer {
		buf = append(buf, fmt.Sprintf("%d\t%s\n", i, log)...)
		if len(buf) >= 4096 {
			if _, err := f.Write(buf); err != nil {
				return fmt.Errorf("write log entries: %w", err)
			}
			buf = buf[:0]
		}
	}
	if len(buf) > 0 {
		if _, err := f.Write(buf); err != nil {
			return fmt.Errorf("write remaining log entries: %w", err)
		}
	}
	return nil
}

// drainChannel drains remaining messages from the async channel into the buffer.
func (t *TestTracer) drainChannel() {
	for {
		select {
		case msg, ok := <-t.logCh:
			if !ok {
				return
			}
			t.bufferMu.Lock()
			t.logBuffer = append(t.logBuffer, msg)
			t.bufferSize.Add(1)
			if len(t.logBuffer) >= t.maxBufLen {
				t.logBuffer = t.logBuffer[:0]
				t.bufferSize.Store(0)
			}
			t.bufferMu.Unlock()
		default:
			return
		}
	}
}

// GetRefCount returns the tracked ref count for a PageRef pointer.
func (t *TestTracer) GetRefCount(refPtr uintptr) int {
	t.refMu.RLock()
	defer t.refMu.RUnlock()
	return t.refMap[refPtr]
}

// Close stops the background consumer and drains remaining logs.
func (t *TestTracer) Close() {
	if t.closed.Swap(true) {
		return // already closed
	}
	close(t.done)
	t.wg.Wait()
	t.drainChannel()
}
