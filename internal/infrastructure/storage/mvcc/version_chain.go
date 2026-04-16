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
// rolledBack may only transition from false→true (monotonic) via atomic store.
type VersionNode struct {
	commitTS   uint64
	value      []byte       // deepCopy'd value (excludes Flag); nil for Tombstone
	flag       byte         // FlagNormal / FlagTombstone
	rolledBack atomic.Bool  // marks this node's transaction as rolled back; snapshot reads skip it
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
