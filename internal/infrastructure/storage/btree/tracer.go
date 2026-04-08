// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// Tracer defines the interface for tracing BTree operations.
// All methods are safe for concurrent use.
type Tracer interface {
	// LogOp records a general operation.
	LogOp(op string, args ...any)

	// LogPageRefOp records a PageRef operation.
	LogPageRefOp(ref *PageRef, op string, args ...any)

	// LogPageOp records a Page operation.
	LogPageOp(pageID model.PageID, op string, args ...any)

	// LogPageData records a snapshot of page data for debugging.
	LogPageData(pageID model.PageID, desc string, data any)

	// WithContext returns a new Tracer with the given context.
	WithContext(ctx context.Context) Tracer
}

// nilTracer is a no-op implementation of Tracer.
type nilTracer struct{}

func (t *nilTracer) LogOp(op string, args ...any)                           {}
func (t *nilTracer) LogPageRefOp(ref *PageRef, op string, args ...any)      {}
func (t *nilTracer) LogPageOp(pageID model.PageID, op string, args ...any)  {}
func (t *nilTracer) LogPageData(pageID model.PageID, desc string, data any) {}
func (t *nilTracer) WithContext(ctx context.Context) Tracer                 { return t }

// DefaultTracer is the default tracer that does nothing.
var DefaultTracer Tracer = &nilTracer{}

// GlobalTracer is an optional global tracer for debugging.
// If set, it will be used by all tracing points.
var GlobalTracer Tracer = &nilTracer{}
