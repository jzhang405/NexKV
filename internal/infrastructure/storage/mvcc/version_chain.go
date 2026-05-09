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
// After creation, commitTS/value/flag/next are never modified.
// rolledBack and reclaimed may only transition from false→true (monotonic) via atomic store.
type VersionNode struct {
	commitTS   uint64
	value      []byte       // deepCopy'd value (excludes Flag); nil for Tombstone
	flag       byte         // FlagNormal / FlagTombstone
	rolledBack atomic.Bool  // marks this node's transaction as rolled back; snapshot reads skip it
	reclaimed  atomic.Bool  // Phase 3 GC: marked by Prune, skipped by snapshotGet
	next       *VersionNode // read-only pointer to older version
}

// VersionChain is a lock-free append-only linked list using atomic.Pointer.
// Prepend uses CAS + retry to atomically insert at the head.
type VersionChain struct {
	head       atomic.Pointer[VersionNode]
	generation atomic.Uint64 // ABA protection for Phase 3 GC; Phase 2 only increments
}

// Prepend atomically inserts a new version at the head of the chain.
// Uses CAS + retry (maxRetries=16). Returns ErrVersionChainConflict on exhaustion.
// The value parameter must already be an independent copy (deepCopy'd by caller).
func (vc *VersionChain) Prepend(commitTS uint64, value []byte, flag byte) error {
	const maxRetries = 16
	for i := 0; i < maxRetries; i++ {
		oldHead := vc.head.Load()
		newNode := &VersionNode{
			commitTS: commitTS,
			value:    value,
			flag:     flag,
			next:     oldHead,
		}
		if vc.head.CompareAndSwap(oldHead, newNode) {
			vc.generation.Add(1)
			return nil
		}
		runtime.Gosched()
	}
	return ErrVersionChainConflict
}

// Load returns the current head of the chain (may be nil).
func (vc *VersionChain) Load() *VersionNode {
	return vc.head.Load()
}

// Generation returns the current generation counter value.
func (vc *VersionChain) Generation() uint64 {
	return vc.generation.Load()
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
// Must be followed by vc.generation.Add(1) to ensure snapshotGet detects the change.
func (vc *VersionChain) Prune(watermark uint64) int {
	head := vc.head.Load()
	if head == nil {
		return 0
	}

	// Pass 1: find the minimum commitTS that must be retained.
	var (
		lastBeforeWatermark        *VersionNode
		firstNonTombstoneBeforeWM *VersionNode
	)
	for node := head; node != nil; node = node.next {
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
	// All nodes with commitTS < minRetainedCommitTS can be reclaimed.
	minRetainedCommitTS := uint64(0)
	if lastBeforeWatermark != nil {
		minRetainedCommitTS = lastBeforeWatermark.commitTS
		if lastBeforeWatermark.flag == FlagTombstone && firstNonTombstoneBeforeWM != nil {
			// Also retain the first non-Tombstone covered by this Tombstone (rule 3).
			if firstNonTombstoneBeforeWM.commitTS < minRetainedCommitTS {
				minRetainedCommitTS = firstNonTombstoneBeforeWM.commitTS
			}
		}
	}

	// Pass 2: mark all nodes (except head) with commitTS < minRetainedCommitTS.
	var marked int
	for node := head; node != nil; node = node.next {
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
	cleaned := 0
	for i := 0; i < maxRetries; i++ {
		oldHead := vc.head.Load()

		// Clean consecutive reclaimed nodes from the head
		newOldHead := oldHead
		for newOldHead != nil && newOldHead.reclaimed.Load() {
			newOldHead = newOldHead.next
			cleaned++
		}

		newNode := &VersionNode{
			commitTS: commitTS,
			value:    value,
			flag:     flag,
			next:     newOldHead,
		}
		if vc.head.CompareAndSwap(oldHead, newNode) {
			vc.generation.Add(1)
			return cleaned, nil
		}
		runtime.Gosched()
	}
	return cleaned, ErrVersionChainConflict
}

// Range calls fn for each chain in the store. If fn returns false, iteration stops.
func (vs *VersionStore) Range(fn func(key string, chain *VersionChain) bool) {
	vs.chains.Range(func(k, v any) bool {
		return fn(k.(string), v.(*VersionChain))
	})
}
