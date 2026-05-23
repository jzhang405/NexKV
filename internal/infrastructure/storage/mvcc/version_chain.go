// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package mvcc

import (
	"runtime"
	"sync"
	"sync/atomic"
)

// VersionNode is an immutable linked list node in the version chain.
// After creation, commitTS/value/flag are never modified.
// rolledBack and reclaimed may only transition from false→true (monotonic) via atomic store.
// next is an atomic pointer to support safe concurrent unlink during Prune.
type VersionNode struct {
	commitTS   uint64
	value      []byte                      // deepCopy'd value (excludes Flag); nil for Tombstone
	flag       byte                        // FlagNormal / FlagTombstone
	rolledBack atomic.Bool                 // marks this node's transaction as rolled back; snapshot reads skip it
	reclaimed  atomic.Bool                 // Phase 3 GC: marked by Prune, skipped by snapshotGet
	next       atomic.Pointer[VersionNode] // pointer to older version; atomic for CAS unlink in Prune
}

// chainHead wraps a VersionNode pointer with a generation counter for ABA protection.
// The pair is stored in a single atomic.Pointer so that CAS atomically compares both.
// This prevents ABA: even if Go reuses a *VersionNode address, the generation differs.
type chainHead struct {
	node       *VersionNode
	generation uint64
}

// VersionChain is a lock-free append-only linked list using atomic.Pointer[chainHead].
// Prepend uses CAS + retry to atomically insert at the head.
// generation is embedded in chainHead for ABA protection.
type VersionChain struct {
	head atomic.Pointer[chainHead]
}

// Prepend atomically inserts a new version at the head of the chain.
// Uses CAS + retry (maxRetries=16). Returns ErrVersionChainConflict on exhaustion.
// The value parameter must already be an independent copy (deepCopy'd by caller).
func (vc *VersionChain) Prepend(commitTS uint64, value []byte, flag byte) error {
	const maxRetries = 16
	for i := 0; i < maxRetries; i++ {
		old := vc.head.Load()
		var oldNode *VersionNode
		var oldGen uint64
		if old != nil {
			oldNode = old.node
			oldGen = old.generation
		}
		newNode := &VersionNode{
			commitTS: commitTS,
			value:    value,
			flag:     flag,
		}
		newNode.next.Store(oldNode)
		newHead := &chainHead{
			node:       newNode,
			generation: oldGen + 1,
		}
		if vc.head.CompareAndSwap(old, newHead) {
			return nil
		}
		runtime.Gosched()
	}
	return ErrVersionChainConflict
}

// Load returns the current head VersionNode (may be nil).
func (vc *VersionChain) Load() *VersionNode {
	h := vc.head.Load()
	if h == nil {
		return nil
	}
	return h.node
}

// Generation returns the current generation counter value.
func (vc *VersionChain) Generation() uint64 {
	h := vc.head.Load()
	if h == nil {
		return 0
	}
	return h.generation
}

// VersionStore is a global version chain store mapping keys to their version chains.
type VersionStore struct {
	chains sync.Map // string → *VersionChain
}

// Prepend appends an old version to the specified key's version chain.
// Automatically creates the chain if it does not exist.
func (vs *VersionStore) Prepend(key string, commitTS uint64, value []byte, flag byte) error {
	val, _ := vs.chains.LoadOrStore(key, &VersionChain{})
	chain := val.(*VersionChain)
	return chain.Prepend(commitTS, value, flag)
}

// Load returns the version chain for the given key, or nil if not found.
func (vs *VersionStore) Load(key string) *VersionChain {
	val, ok := vs.chains.Load(key)
	if !ok {
		return nil
	}
	return val.(*VersionChain)
}

// LoadOrStore returns the existing chain for key or creates and stores a new one.
func (vs *VersionStore) LoadOrStore(key string) *VersionChain {
	val, _ := vs.chains.LoadOrStore(key, &VersionChain{})
	return val.(*VersionChain)
}

// Prune marks nodes eligible for GC as reclaimed based on the watermark.
// GC retention rules:
//  1. Chain head is always retained
//  2. The latest visible version before watermark is retained (including Tombstone)
//  3. If the latest visible version is a Tombstone, the first non-Tombstone visible version
//     before it must also be retained (prevents key resurrection for old snapshots)
//  4. Older versions (commitTS < minRetainedCommitTS) are marked reclaimed
//
// Returns the number of nodes marked reclaimed.
func (vc *VersionChain) Prune(watermark uint64) int {
	head := vc.Load()
	if head == nil {
		return 0
	}

	// Pass 1: find the minimum commitTS that must be retained.
	var (
		lastBeforeWatermark       *VersionNode
		firstNonTombstoneBeforeWM *VersionNode
	)
	for node := head; node != nil; node = node.next.Load() {
		if node.commitTS < watermark {
			if lastBeforeWatermark == nil {
				lastBeforeWatermark = node
			}
			if node.flag != FlagTombstone && firstNonTombstoneBeforeWM == nil {
				firstNonTombstoneBeforeWM = node
			}
		}
	}

	// Compute minRetainedCommitTS: the minimum commitTS that must be kept.
	minRetainedCommitTS := uint64(0)
	if lastBeforeWatermark != nil {
		minRetainedCommitTS = lastBeforeWatermark.commitTS
		if lastBeforeWatermark.flag == FlagTombstone && firstNonTombstoneBeforeWM != nil {
			if firstNonTombstoneBeforeWM.commitTS < minRetainedCommitTS {
				minRetainedCommitTS = firstNonTombstoneBeforeWM.commitTS
			}
		}
	}

	// Pass 2: mark all nodes (except head) with commitTS < minRetainedCommitTS.
	var marked int
	for node := head; node != nil; node = node.next.Load() {
		if node == head {
			continue
		}
		if node.commitTS < minRetainedCommitTS {
			node.reclaimed.Store(true)
			marked++
		}
	}

	return marked
}

// PrependWithCleanup atomically inserts a new version and cleans up consecutive
// reclaimed nodes from the chain head. Used in the Prepend hot path during commit.
// Returns the number of reclaimed nodes removed.
func (vc *VersionChain) PrependWithCleanup(commitTS uint64, value []byte, flag byte) (int, error) {
	const maxRetries = 16
	var cleaned int
	for i := 0; i < maxRetries; i++ {
		cleaned = 0 // reset per attempt — CAS failure means cleanup was not applied
		old := vc.head.Load()
		var oldNode *VersionNode
		var oldGen uint64
		if old != nil {
			oldNode = old.node
			oldGen = old.generation
		}

		// Clean consecutive reclaimed nodes from the head
		newOldHead := oldNode
		for newOldHead != nil && newOldHead.reclaimed.Load() {
			newOldHead = newOldHead.next.Load()
			cleaned++
		}

		newNode := &VersionNode{
			commitTS: commitTS,
			value:    value,
			flag:     flag,
		}
		newNode.next.Store(newOldHead)
		newHead := &chainHead{
			node:       newNode,
			generation: oldGen + 1,
		}
		if vc.head.CompareAndSwap(old, newHead) {
			return cleaned, nil
		}
		runtime.Gosched()
	}
	return 0, ErrVersionChainConflict
}

// bumpGeneration atomically increments the generation counter for ABA protection.
// This signals to concurrent snapshotGet readers that the chain has logically changed.
// Safe to call on empty chains (head==nil): no-op.
func (vc *VersionChain) bumpGeneration() {
	for {
		cur := vc.head.Load()
		if cur == nil {
			return
		}
		newHead := &chainHead{node: cur.node, generation: cur.generation + 1}
		if vc.head.CompareAndSwap(cur, newHead) {
			return
		}
	}
}

// Range calls fn for each chain in the store. If fn returns false, iteration stops.
func (vs *VersionStore) Range(fn func(key string, chain *VersionChain) bool) {
	vs.chains.Range(func(k, v any) bool {
		return fn(k.(string), v.(*VersionChain))
	})
}
