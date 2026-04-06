// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

//go:build !enable_tracer

package btree

import (
	"context"
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// TestTracer is a no-op stub when tracer is disabled via build tags.
// Provides compile-time compatibility for tests without enable_tracer tag.
type TestTracer struct{}

// NewTestTracer returns a no-op TestTracer when tracer is disabled.
func NewTestTracer(t testing.TB, maxBufferLen, logChanSize int) *TestTracer {
	return &TestTracer{}
}

func (t *TestTracer) LogOp(op string, args ...any)                           {}
func (t *TestTracer) LogPageRefOp(ref *PageRef, op string, args ...any)      {}
func (t *TestTracer) LogPageOp(pageID model.PageID, op string, args ...any)  {}
func (t *TestTracer) LogPageData(pageID model.PageID, desc string, data any) {}
func (t *TestTracer) WithContext(ctx context.Context) Tracer                 { return t }
func (t *TestTracer) DumpLogs() []string                                     { return nil }
func (t *TestTracer) DumpToFile(path string) error                           { return nil }
func (t *TestTracer) GetRefCount(refPtr uintptr) int                         { return 0 }
func (t *TestTracer) Close()                                                 {}
