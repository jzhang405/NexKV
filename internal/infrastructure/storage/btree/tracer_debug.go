// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

//go:build enable_tracer

package btree

import (
	"context"
	"fmt"
	"os"
	"path"
	"runtime"
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

// callerStack returns the caller function name and two parent callers.
// skip=0 returns the direct caller; increase skip to skip more frames.
func callerStack(skip int) (caller, c1, c2 string) {
	pc := make([]uintptr, 4)
	n := runtime.Callers(skip+2, pc) // +2 for runtime.Callers + callerStack itself
	if n >= 1 {
		caller = path.Base(runtime.FuncForPC(pc[0]).Name())
	}
	if n >= 2 {
		c1 = path.Base(runtime.FuncForPC(pc[1]).Name())
	}
	if n >= 3 {
		c2 = path.Base(runtime.FuncForPC(pc[2]).Name())
	}
	return
}

// formatArgs formats alternating key-value pairs as " key=val" strings.
func formatArgs(args []any) string {
	if len(args) == 0 {
		return ""
	}
	pairs := make([]byte, 0, 128)
	for i := 0; i+1 < len(args); i += 2 {
		pairs = append(pairs, fmt.Sprintf(" %v=%v", args[i], args[i+1])...)
	}
	if len(args)%2 == 1 {
		pairs = append(pairs, fmt.Sprintf(" %v", args[len(args)-1])...)
	}
	return string(pairs)
}

func (t *TestTracer) LogOp(op string, args ...any) {
	caller, c1, c2 := callerStack(0)
	ts := time.Now().Format("15:04:05.000")
	msg := fmt.Sprintf("[%s] %-40s | %s ← %s ← %s%s",
		ts, op, caller, c1, c2, formatArgs(args))
	t.sendAsync(msg)
}

func (t *TestTracer) LogPageRefOp(ref *PageRef, op string, args ...any) {
	caller, c1, c2 := callerStack(0)
	ts := time.Now().Format("15:04:05.000")
	msg := fmt.Sprintf("[%s] PageRef:%p %-30s pageID=%d | %s ← %s ← %s%s",
		ts, ref, op, ref.pageID, caller, c1, c2, formatArgs(args))
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
	caller, c1, c2 := callerStack(0)
	ts := time.Now().Format("15:04:05.000")
	msg := fmt.Sprintf("[%s] Page:%-4d %-30s | %s ← %s ← %s%s",
		ts, pageID, op, caller, c1, c2, formatArgs(args))
	t.sendAsync(msg)

	t.pageMu.Lock()
	t.pageMap[pageID] = op
	t.pageMu.Unlock()
}

func (t *TestTracer) LogPageData(pageID model.PageID, desc string, data any) {
	caller, c1, c2 := callerStack(0)
	ts := time.Now().Format("15:04:05.000")
	msg := fmt.Sprintf("[%s] PageData:%-4d %-30s | %s ← %s ← %s %s",
		ts, pageID, desc, caller, c1, c2, fmt.Sprintf("%#v", data))
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
