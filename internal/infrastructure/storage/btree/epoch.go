// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

const (
	epochInit      = 1
	maxReaderSlots = 64
)

// EpochManager provides epoch-based safe page reclamation for COW old pages.
//
// Readers register their epoch before reading pages; writers retire old pageIDs
// after CAS via lock-free Treiber stacks. A background goroutine periodically
// advances the global epoch, snapshots active reader epochs, and frees retired
// pages whose epoch is strictly less than the safe epoch.
type EpochManager struct {
	globalEpoch atomic.Uint64
	readers     [maxReaderSlots]atomic.Uint64
	slots       [maxReaderSlots]epochSlot
	freeFn      func(model.PageID)

	nodePool sync.Pool // *retiredNode

	nextSlot atomic.Uint64
	wg       sync.WaitGroup
}

// epochSlot uses a Treiber stack (lock-free CAS-linked list) for retired pages.
// Writers push via CAS on head; the reclaimer atomically swaps head to nil.
type epochSlot struct {
	head atomic.Pointer[retiredNode]
}

type retiredNode struct {
	pageID model.PageID
	epoch  uint64
	next   atomic.Pointer[retiredNode]
}

// NewEpochManager creates an EpochManager. freeFn is called to release retired pages.
func NewEpochManager(freeFn func(model.PageID)) *EpochManager {
	em := &EpochManager{
		freeFn: freeFn,
		nodePool: sync.Pool{New: func() any { return new(retiredNode) }},
	}
	em.globalEpoch.Store(epochInit)
	return em
}

// AllocSlot allocates a reader/writer slot via atomic round-robin.
func (em *EpochManager) AllocSlot() int {
	return int(em.nextSlot.Add(1) % maxReaderSlots)
}

// EnterRead registers this reader with the current global epoch.
func (em *EpochManager) EnterRead(slot int) {
	epoch := em.globalEpoch.Load()
	em.readers[slot].Store(epoch)
	if em.globalEpoch.Load() != epoch {
		em.readers[slot].Store(em.globalEpoch.Load())
	}
}

// ExitRead unregisters this reader.
func (em *EpochManager) ExitRead(slot int) {
	em.readers[slot].Store(0)
}

// Retire pushes a COW-replaced page onto the slot's Treiber stack.
// Lock-free: CAS loop on head. Node from sync.Pool to avoid per-op allocation.
func (em *EpochManager) Retire(slot int, pageID model.PageID) {
	node := em.nodePool.Get().(*retiredNode)
	node.pageID = pageID
	node.epoch = em.globalEpoch.Load()
	node.next.Store(nil)
	s := &em.slots[slot]
	for {
		old := s.head.Load()
		node.next.Store(old)
		if s.head.CompareAndSwap(old, node) {
			return
		}
	}
}

// tryReclaim advances the global epoch, snapshots reader slots, and frees
// pages whose epoch < safeEpoch.
func (em *EpochManager) tryReclaim() {
	newEpoch := em.globalEpoch.Add(1)

	minActive := uint64(math.MaxUint64)
	for i := range em.readers {
		if e := em.readers[i].Load(); e != 0 && e < minActive {
			minActive = e
		}
	}

	var safeEpoch uint64
	if minActive == math.MaxUint64 {
		safeEpoch = newEpoch
	} else {
		safeEpoch = minActive
	}

	var toFree []model.PageID
	for i := range em.slots {
		s := &em.slots[i]
		// Atomically detach all nodes from the Treiber stack.
		var head *retiredNode
		for {
			head = s.head.Load()
			if s.head.CompareAndSwap(head, nil) {
				break
			}
		}

		// Walk the detached list; collect unsafe nodes for re-push.
		var unsafeHead, unsafeTail *retiredNode
		for n := head; n != nil; {
			next := n.next.Load() // save before clearing
			if n.epoch < safeEpoch {
				toFree = append(toFree, n.pageID)
				em.nodePool.Put(n) // recycle node
			} else {
				n.next.Store(nil)
				if unsafeHead == nil {
					unsafeHead = n
				} else {
					unsafeTail.next.Store(n)
				}
				unsafeTail = n
			}
			n = next
		}

		// Push back unsafe nodes (batch push: link tail to current head, CAS).
		if unsafeHead != nil {
			for {
				old := s.head.Load()
				unsafeTail.next.Store(old)
				if s.head.CompareAndSwap(old, unsafeHead) {
					break
				}
			}
		}
	}

	for _, pageID := range toFree {
		em.freeFn(pageID)
	}
}

// StartBackgroundReclaim launches a background goroutine that periodically
// calls tryReclaim.
func (em *EpochManager) StartBackgroundReclaim(ctx context.Context) {
	em.wg.Add(1)
	go func() {
		defer em.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				_ = r
			}
		}()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				em.tryReclaim()
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Shutdown waits for the background goroutine to exit, then drains all slots.
func (em *EpochManager) Shutdown() {
	em.wg.Wait()
	em.tryReclaim()
}
