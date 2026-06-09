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
	ringSize       = 16384 // per-slot ring buffer capacity (~256KB)
)

// EpochManager provides epoch-based safe page reclamation for COW old pages.
// Phase 6: also manages LOB resource retirement (overflow page chains + LOB files).
type EpochManager struct {
	globalEpoch atomic.Uint64
	readers     [maxReaderSlots]atomic.Uint64
	slots       [maxReaderSlots]epochSlot
	freeFn      func(model.PageID)

	// Phase 6: LOB resource retirement
	lobFreeFn     func(firstPageID uint32) // free overflow page chain
	lobFileFreeFn func(lobID uint64)       // unlink LOB file
	lobMu         sync.Mutex
	lobRetired    []lobRetiredEntry

	nextSlot atomic.Uint64
	wg       sync.WaitGroup
}

// lobRetiredEntry tracks a retired LOB resource with its retirement epoch.
// firstPageID != 0 → overflow page chain; lobID != 0 → LOB file.
type lobRetiredEntry struct {
	firstPageID uint32
	lobID       uint64
	epoch       uint64
}

// epochSlot uses a bounded ring buffer for retired pages.
//
// Writer protocol (Retire):
//  1. CAS on tail to claim a position (multi-producer safe)
//  2. Write pageID (plain store)
//  3. epoch.Store(epoch) — atomic release, publishes the entry
//  4. If buffer full (tail-head >= ringSize), spin-wait for reader to advance head
//
// Reader protocol (tryReclaim):
//  1. Load tail, read head
//  2. For each position in [head, tail):
//     a. epoch.Load() — atomic acquire, 0 = not yet committed
//     b. If epoch >= safeEpoch → stop (can't advance past unsafe entry)
//     c. Read pageID (plain, visible after acquire)
//     d. Free if epoch < safeEpoch
//  3. Advance head to maxHead
//
// Release-acquire ordering (epoch.Store → epoch.Load) guarantees pageID is visible.
type epochSlot struct {
	buf  [ringSize]retiredEntry
	head atomic.Uint64 // reader position, written by tryReclaim, read by Retire
	tail atomic.Uint64 // writer position, next write index (CAS-claimed)
}

type retiredEntry struct {
	pageID model.PageID
	epoch  atomic.Uint64 // 0 = not committed; committed = epoch value (≥1)
}

func NewEpochManager(freeFn func(model.PageID)) *EpochManager {
	em := &EpochManager{freeFn: freeFn}
	em.globalEpoch.Store(epochInit)
	return em
}

func (em *EpochManager) AllocSlot() int {
	return int(em.nextSlot.Add(1) % maxReaderSlots)
}

func (em *EpochManager) EnterRead(slot int) {
	epoch := em.globalEpoch.Load()
	em.readers[slot].Store(epoch)
	if em.globalEpoch.Load() != epoch {
		em.readers[slot].Store(em.globalEpoch.Load())
	}
}

func (em *EpochManager) ExitRead(slot int) {
	em.readers[slot].Store(0)
}

// Retire writes a COW-replaced page into the slot's ring buffer.
// Hot-path: 1 CAS (claim) + 2 plain stores + 1 atomic store (commit). Zero allocs.
func (em *EpochManager) Retire(slot int, pageID model.PageID) {
	s := &em.slots[slot]
	epoch := em.globalEpoch.Load()

	for {
		tail := s.tail.Load()
		head := s.head.Load()
		if tail-head >= ringSize {
			// Buffer full — reader hasn't kept up. Brief spin.
			continue
		}
		if s.tail.CompareAndSwap(tail, tail+1) {
			idx := tail % ringSize
			s.buf[idx].pageID = pageID    // plain store
			s.buf[idx].epoch.Store(epoch) // atomic release — publish
			return
		}
	}
}

// RetireBatch retires multiple pages sharing one epoch snapshot.
func (em *EpochManager) RetireBatch(slot int, pageIDs ...model.PageID) {
	if len(pageIDs) == 0 {
		return
	}
	epoch := em.globalEpoch.Load()
	s := &em.slots[slot]
	for _, pid := range pageIDs {
		for {
			tail := s.tail.Load()
			if tail-s.head.Load() >= ringSize {
				continue
			}
			if s.tail.CompareAndSwap(tail, tail+1) {
				idx := tail % ringSize
				s.buf[idx].pageID = pid
				s.buf[idx].epoch.Store(epoch)
				break
			}
		}
	}
}

// tryReclaim advances the global epoch, snapshots readers, and frees safe pages.
func (em *EpochManager) tryReclaim() {
	newEpoch := em.globalEpoch.Add(1)

	minActive := uint64(math.MaxUint64)
	for i := range em.readers {
		if e := em.readers[i].Load(); e != 0 && e < minActive {
			minActive = e
		}
	}
	safeEpoch := newEpoch
	if minActive != math.MaxUint64 {
		safeEpoch = minActive
	}

	var toFree []model.PageID
	for i := range em.slots {
		s := &em.slots[i]
		tail := s.tail.Load()
		head := s.head.Load()
		if head >= tail {
			continue
		}
		maxHead := head
		for j := head; j < tail; j++ {
			idx := j % ringSize
			epoch := s.buf[idx].epoch.Load() // atomic acquire
			if epoch == 0 {
				break // hole: writer claimed slot but not yet committed
			}
			if epoch >= safeEpoch {
				break // unsafe: can't advance past entries readers still reference
			}
			toFree = append(toFree, s.buf[idx].pageID)
			maxHead = j + 1
		}
		s.head.Store(maxHead)
	}

	for _, pageID := range toFree {
		em.freeFn(pageID)
	}

	// Phase 6: drain retired LOB resources
	em.drainLOBRetired(safeEpoch)
}

func (em *EpochManager) StartBackgroundReclaim(ctx context.Context) {
	em.wg.Add(1)
	go func() {
		defer em.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				_ = r
			}
		}()
		ticker := time.NewTicker(500 * time.Millisecond)
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

func (em *EpochManager) Shutdown() {
	em.wg.Wait()
	em.tryReclaim()
}

// ---------------------------------------------------------------------------
// Phase 6: LOB resource epoch retirement
// ---------------------------------------------------------------------------

// SetLOBFreeFns configures LOB resource free functions for epoch-based GC.
// lobFreeFn is called for overflow page chains (firstPageID → FreeOverflow).
// lobFileFreeFn is called for LOB files (lobID → os.Remove).
func (em *EpochManager) SetLOBFreeFns(overflowFn func(uint32), lobFileFn func(uint64)) {
	em.lobFreeFn = overflowFn
	em.lobFileFreeFn = lobFileFn
}

// RetireLobChain pushes an overflow page chain into the epoch retirement queue.
// The chain is walked and freed only after the current epoch advances past all readers.
func (em *EpochManager) RetireLobChain(firstPageID uint32) {
	if firstPageID == 0 || em.lobFreeFn == nil {
		return
	}
	epoch := em.globalEpoch.Load()
	em.lobMu.Lock()
	em.lobRetired = append(em.lobRetired, lobRetiredEntry{
		firstPageID: firstPageID,
		epoch:       epoch,
	})
	em.lobMu.Unlock()
}

// RetireLobFile pushes a LOB file ID into the epoch retirement queue.
// The file is unlinked only after the current epoch advances past all readers.
func (em *EpochManager) RetireLobFile(lobID uint64) {
	if lobID == 0 || em.lobFileFreeFn == nil {
		return
	}
	epoch := em.globalEpoch.Load()
	em.lobMu.Lock()
	em.lobRetired = append(em.lobRetired, lobRetiredEntry{
		lobID: lobID,
		epoch: epoch,
	})
	em.lobMu.Unlock()
}

// drainLOBRetired drains retired LOB resources whose epoch is safe.
func (em *EpochManager) drainLOBRetired(safeEpoch uint64) {
	em.lobMu.Lock()
	var keep []lobRetiredEntry
	for _, e := range em.lobRetired {
		if e.epoch < safeEpoch {
			if e.firstPageID != 0 && em.lobFreeFn != nil {
				em.lobFreeFn(e.firstPageID)
			}
			if e.lobID != 0 && em.lobFileFreeFn != nil {
				em.lobFileFreeFn(e.lobID)
			}
		} else {
			keep = append(keep, e)
		}
	}
	em.lobRetired = keep
	em.lobMu.Unlock()
}
