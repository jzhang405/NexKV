// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package mvcc

import (
	"sync"
)

// ActiveTxRegistry tracks active transactions for GC watermark computation.
// All methods are safe for concurrent use.
type ActiveTxRegistry struct {
	mu  sync.Mutex
	txs map[uint64]uint64 // txID → snapshotTS

	// Phase 4: remote watermarks from other nodes (Gossip protocol)
	remoteWatermarks map[string]uint64 // nodeID → watermark
}

// NewActiveTxRegistry creates a new registry.
func NewActiveTxRegistry() *ActiveTxRegistry {
	return &ActiveTxRegistry{
		txs:              make(map[uint64]uint64),
		remoteWatermarks: make(map[string]uint64),
	}
}

// Register adds a transaction to the registry.
// Called by BeginTx under the same Mutex that allocates snapshotTS.
func (r *ActiveTxRegistry) Register(txID uint64, snapshotTS uint64) {
	r.mu.Lock()
	r.txs[txID] = snapshotTS
	r.mu.Unlock()
}

// Unregister removes a transaction from the registry.
// Called by Commit/Rollback via defer. Safe to call multiple times.
func (r *ActiveTxRegistry) Unregister(txID uint64) {
	r.mu.Lock()
	delete(r.txs, txID)
	r.mu.Unlock()
}

// Watermark returns the minimum snapshotTS among all active transactions.
// Returns 0 if no transactions are active (caller should fall back to CurrentTS).
func (r *ActiveTxRegistry) Watermark() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.txs) == 0 {
		return 0
	}
	var min uint64 = ^uint64(0)
	for _, ts := range r.txs {
		if ts < min {
			min = ts
		}
	}
	return min
}

// SetRemoteWatermark records the watermark from a remote node.
// Phase 4: used by Gossip-based Global Watermark protocol.
// watermark is monotonic per node; this method applies max(existing, incoming).
func (r *ActiveTxRegistry) SetRemoteWatermark(nodeID string, watermark uint64) {
	r.mu.Lock()
	existing := r.remoteWatermarks[nodeID]
	if watermark > existing {
		r.remoteWatermarks[nodeID] = watermark
	}
	r.mu.Unlock()
}

// RemoveRemoteWatermark removes a node's watermark (e.g., node marked DEAD).
func (r *ActiveTxRegistry) RemoveRemoteWatermark(nodeID string) {
	r.mu.Lock()
	delete(r.remoteWatermarks, nodeID)
	r.mu.Unlock()
}

// ActiveCount returns the number of currently registered transactions.
func (r *ActiveTxRegistry) ActiveCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.txs)
}
