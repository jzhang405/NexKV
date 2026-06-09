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
	metrics        *BTreeMetrics
	tracer         Tracer
	tsGen          mvcc.TSGenerator
	txMgr          mvcc.TxManager
	latencyMetrics *BTreeMetricsWithLatency
	enableEpoch    bool
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

// buildTxManager creates a TxManager from config, using the provided StorageBackend.
// If WithTxManager was called, uses that; otherwise creates a default one.
// If lobMgr is non-nil, enables LOB large object support in transactions.
func (cfg *btreeConfig) buildTxManager(storage mvcc.StorageBackend, lobMgr mvcc.LOBManager) mvcc.TxManager {
	if cfg.txMgr != nil {
		return cfg.txMgr
	}
	if lobMgr != nil {
		return mvcc.NewTxManagerWithLOB(storage, cfg.tsGen, lobMgr)
	}
	return mvcc.NewTxManager(storage, cfg.tsGen)
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

// WithTxManager sets a custom transaction manager.
// If not set, a default TxManager is created from the BTree's TSGenerator.
func WithTxManager(txMgr mvcc.TxManager) BTreeOption {
	return func(cfg *btreeConfig) {
		if txMgr != nil {
			cfg.txMgr = txMgr
		}
	}
}

// WithEpoch enables epoch-based COW old-page reclamation.
// When enabled, CAS-replaced pages are deferred and safely freed after all
// concurrent readers have exited. Without this, every CAS leaks one page (4KB).
func WithEpoch() BTreeOption {
	return func(cfg *btreeConfig) {
		cfg.enableEpoch = true
	}
}

// WithLatencyMetrics enables latency histogram collection.
// When enabled, every 1/64th operation records its latency in P50/P95/P99 histograms.
// Default is disabled (nil) to avoid hot-path overhead.
func WithLatencyMetrics() BTreeOption {
	return func(cfg *btreeConfig) {
		cfg.latencyMetrics = NewBTreeMetricsWithLatency()
	}
}
