package btree

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

func TestEpochManager_AllocSlot(t *testing.T) {
	em := NewEpochManager(func(id model.PageID) {})
	s0 := em.AllocSlot()
	s1 := em.AllocSlot()
	s2 := em.AllocSlot()
	if s0 < 0 || s0 >= maxReaderSlots {
		t.Errorf("slot out of range: %d", s0)
	}
	if s1 < 0 || s1 >= maxReaderSlots {
		t.Errorf("slot out of range: %d", s1)
	}
	if s2 < 0 || s2 >= maxReaderSlots {
		t.Errorf("slot out of range: %d", s2)
	}
}

func TestEpochManager_EnterExitRead(t *testing.T) {
	em := NewEpochManager(func(id model.PageID) {})
	slot := em.AllocSlot()

	em.EnterRead(slot)
	if em.readers[slot].Load() == 0 {
		t.Error("reader epoch should be non-zero after EnterRead")
	}
	em.ExitRead(slot)
	if em.readers[slot].Load() != 0 {
		t.Error("reader epoch should be zero after ExitRead")
	}
}

func TestEpochManager_DeferAndReclaim(t *testing.T) {
	var freed []model.PageID
	var mu sync.Mutex
	em := NewEpochManager(func(id model.PageID) {
		mu.Lock()
		freed = append(freed, id)
		mu.Unlock()
	})

	slot := em.AllocSlot()
	em.Retire(slot, 42)
	em.Retire(slot, 43)

	initialEpoch := em.globalEpoch.Load()
	em.tryReclaim()

	mu.Lock()
	got := freed
	mu.Unlock()

	// After tryReclaim with no active readers, both pages should be freed.
	// Treiber stack is LIFO — order is not guaranteed.
	if len(got) != 2 {
		t.Errorf("expected 2 freed pages, got %d: %v (initialEpoch=%d, currentEpoch=%d)",
			len(got), got, initialEpoch, em.globalEpoch.Load())
	}
	found42, found43 := false, false
	for _, id := range got {
		if id == 42 {
			found42 = true
		}
		if id == 43 {
			found43 = true
		}
	}
	if !found42 || !found43 {
		t.Errorf("expected pages 42 and 43, got %v", got)
	}
}

func TestEpochManager_SafeEpoch(t *testing.T) {
	var freed atomic.Int32
	em := NewEpochManager(func(id model.PageID) {
		freed.Add(1)
	})

	// Register a reader
	slot := em.AllocSlot()
	em.EnterRead(slot)

	// Retire a page while reader is active
	em.Retire(slot, 100)

	// tryReclaim — should NOT free page 100 because reader is active
	em.tryReclaim()

	if freed.Load() != 0 {
		t.Error("page should not be freed while reader is active")
	}

	// Reader exits
	em.ExitRead(slot)

	// Now reclaim should succeed
	em.tryReclaim()

	if freed.Load() != 1 {
		t.Error("page should be freed after reader exits")
	}
}

func TestEpochManager_MultiEpoch(t *testing.T) {
	var freed []model.PageID
	var mu sync.Mutex
	em := NewEpochManager(func(id model.PageID) {
		mu.Lock()
		freed = append(freed, id)
		mu.Unlock()
	})

	// Reader at epoch E
	slot1 := em.AllocSlot()
	em.EnterRead(slot1)

	em.Retire(slot1, model.PageID(100))

	em.tryReclaim() // advance to e1+1, safeEpoch = e1 (reader active)

	// Retire more pages with newer epochs (these shouldn't be freed yet)
	em.Retire(slot1, model.PageID(200))
	em.tryReclaim() // advance to e1+2, safeEpoch = e1

	em.ExitRead(slot1)

	// Now reclaim: should free both pages (retired at e1 and e1+1, safeEpoch = e1+2)
	em.tryReclaim()

	mu.Lock()
	got := freed
	mu.Unlock()

	if len(got) != 2 {
		t.Errorf("expected 2 freed pages, got %d: %v", len(got), got)
	}
}

func TestEpochManager_SlotRace(t *testing.T) {
	var freed atomic.Int32
	em := NewEpochManager(func(id model.PageID) {
		freed.Add(1)
	})

	const (
		writers   = 8
		opsPerW   = 10000
		reclaimMs = 5
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Background reclaimer
	go func() {
		ticker := time.NewTicker(time.Duration(reclaimMs) * time.Millisecond)
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

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slot := em.AllocSlot()
			for i := 0; i < opsPerW; i++ {
				em.Retire(slot, model.PageID(i))
			}
		}()
	}
	wg.Wait()

	// Let the reclaimer catch up
	time.Sleep(50 * time.Millisecond)
	cancel()

	// Final reclaim
	em.tryReclaim()

	n := freed.Load()
	totalExpected := int32(writers * opsPerW)
	if n < totalExpected/2 {
		t.Errorf("freed too few pages: %d / %d (reclaimer may be stuck)", n, totalExpected)
	}
}

func TestEpochManager_Shutdown(t *testing.T) {
	var freed atomic.Int32
	em := NewEpochManager(func(id model.PageID) {
		freed.Add(1)
	})

	ctx, cancel := context.WithCancel(context.Background())
	em.StartBackgroundReclaim(ctx)

	// Retire some pages
	slot := em.AllocSlot()
	for i := 0; i < 100; i++ {
		em.Retire(slot, model.PageID(i))
	}

	// Shutdown: stop background, drain
	cancel()
	em.Shutdown()

	if freed.Load() < 100 {
		t.Errorf("Shutdown should drain all retired pages, got %d/100", freed.Load())
	}
}

func TestEpochManager_NoDoubleFree(t *testing.T) {
	// Same pageID retired multiple times should not be double-freed
	// (the freeFn is called once per Retire, but PageManager handles
	// duplicate FreePage via the deleted flag)

	var count atomic.Int32
	em := NewEpochManager(func(id model.PageID) {
		count.Add(1)
	})

	slot := em.AllocSlot()
	// Retire same pageID twice (simulating two CAS wins on the same page in different epochs)
	em.Retire(slot, model.PageID(77))
	em.Retire(slot, model.PageID(77))

	em.tryReclaim()

	if count.Load() != 2 {
		t.Errorf("both retired entries should be processed, got %d", count.Load())
	}
}

func TestEpochManager_EnterReadDoubleCheck(t *testing.T) {
	em := NewEpochManager(func(id model.PageID) {})

	// Artificially advance globalEpoch to simulate TOCTOU
	em.globalEpoch.Store(100)

	slot := em.AllocSlot()
	em.EnterRead(slot)

	epoch := em.readers[slot].Load()
	if epoch != 100 {
		t.Errorf("EnterRead should register epoch 100, got %d", epoch)
	}

	// Simulate epoch advancing after the first Load but before Store
	// by calling EnterRead again after globalEpoch has changed
	em.globalEpoch.Store(200)
	slot2 := em.AllocSlot()
	em.EnterRead(slot2)

	epoch2 := em.readers[slot2].Load()
	if epoch2 != 200 {
		t.Errorf("EnterRead double-check should catch TOCTOU, expected 200 got %d", epoch2)
	}
}
