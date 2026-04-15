// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license
// that can be found in the LICENSE file.

package btree

import (
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/mvcc"
)

// BTreeOption configures a BTree during construction.
type BTreeOption func(*btreeConfig)

// btreeConfig holds the resolved configuration for BTree construction.
type btreeConfig struct {
	metrics *BTreeMetrics
	tracer  Tracer
	tsGen   mvcc.TSGenerator
}

// newBTreeConfig applies all options and fills in defaults.
func newBTreeConfig(opts ...BTreeOption) *btreeConfig {
	cfg := &btreeConfig{
		tsGen: mvcc.NewLocalTS(), // default: local monotonic counter
	}
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.tracer == nil {
		cfg.tracer = DefaultTracer
	}
	return cfg
}

// WithMetrics enables metrics collection for the BTree.
func WithMetrics(metrics *BTreeMetrics) BTreeOption {
	return func(cfg *btreeConfig) {
		cfg.metrics = metrics
	}
}

// WithTracer sets a custom tracer for the BTree.
// If not set, DefaultTracer is used.
func WithTracer(tracer Tracer) BTreeOption {
	return func(cfg *btreeConfig) {
		cfg.tracer = tracer
	}
}

// WithTSGenerator sets a custom timestamp generator for MVCC.
// If not set, mvcc.NewLocalTS() is used.
// Useful for testing with deterministic timestamps.
func WithTSGenerator(tsGen mvcc.TSGenerator) BTreeOption {
	return func(cfg *btreeConfig) {
		if tsGen != nil {
			cfg.tsGen = tsGen
		}
	}
}
