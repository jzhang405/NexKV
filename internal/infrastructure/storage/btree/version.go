// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// SnapshotID uniquely identifies a snapshot.
// Re-exported from model package for convenience.
type SnapshotID = model.SnapshotID

// RootInfo holds root node information.
type RootInfo struct {
	// Root is the root node pointer (pure memory BTree).
	Root *Node

	// RootID is the ID of the root page (kept for compatibility).
	RootID model.PageID

	// Version is the monotonically increasing version number.
	Version uint64

	// Created is the timestamp when this root was created.
	Created time.Time

	// WALSeqNum is the WAL sequence number for this root.
	WALSeqNum uint64

	// RefCount is the reference count for snapshot tracking.
	RefCount atomic.Int32
}

// Acquire increments the reference count.
func (r *RootInfo) Acquire() {
	r.RefCount.Add(1)
}

// Release decrements the reference count.
func (r *RootInfo) Release() int32 {
	return r.RefCount.Add(-1)
}

// GetRefCount returns the current reference count.
func (r *RootInfo) GetRefCount() int32 {
	return r.RefCount.Load()
}

// VersionedRoot manages versioned root pointers with CCOW support.
type VersionedRoot struct {
	// current holds the current root info (atomic for lock-free reads).
	current atomic.Value // *RootInfo

	// versions holds historical root infos for snapshots.
	// Key: version number, Value: *RootInfo
	versions sync.Map

	// mu protects critical sections (minimal use).
	mu sync.RWMutex

	// nextVersion is the next version number.
	nextVersion uint64

	// maxVersions is the maximum number of versions to keep.
	maxVersions int

	// closedAtomic indicates whether the VersionedRoot is closed (atomic for lock-free reads).
	closedAtomic int32
}

// NewVersionedRoot creates a new VersionedRoot with the given initial root node.
func NewVersionedRoot(initialRoot *Node) *VersionedRoot {
	now := time.Now()
	root := &RootInfo{
		Root:      initialRoot,
		RootID:    1, // Placeholder for compatibility
		Version:   0,
		Created:   now,
		WALSeqNum: 0,
	}
	root.RefCount.Store(1) // Initial reference

	vr := &VersionedRoot{
		maxVersions: 10, // Default: keep last 10 versions
		nextVersion: 1,
	}

	vr.current.Store(root)
	vr.versions.Store(uint64(0), root)

	return vr
}

// Get returns the current root info (lock-free read).
// This is safe for concurrent reads and will never block.
func (v *VersionedRoot) Get() *RootInfo {
	root := v.current.Load().(*RootInfo)
	root.Acquire()
	return root
}

// Update updates the root to a new node using lock-free CAS.
// This method uses Compare-And-Swap for concurrent writes without global lock.
// Multiple writers will compete via CAS, and losers will retry.
func (v *VersionedRoot) Update(ctx context.Context, newRoot *Node, walSeqNum uint64) error {
	const maxRetries = 1000 // Maximum retry attempts to avoid infinite loops

	for range maxRetries {
		// Check if closed (no lock needed, atomic read)
		if atomic.LoadInt32(&v.closedAtomic) != 0 {
			return ErrClosed
		}

		// Read current root (lock-free)
		oldRootInfo := v.current.Load().(*RootInfo)

		// Calculate new version number based on current version
		newVersion := oldRootInfo.Version + 1

		// Create new root info
		newRootInfo := &RootInfo{
			Root:      newRoot,
			RootID:    model.PageID(newVersion),
			Version:   newVersion,
			Created:   time.Now(),
			WALSeqNum: walSeqNum,
		}
		newRootInfo.RefCount.Store(1)

		// Try CAS (Compare-And-Swap) to atomically update root pointer
		// If successful, we win the race; if not, another goroutine updated first
		if v.current.CompareAndSwap(oldRootInfo, newRootInfo) {
			// CAS success: atomically update nextVersion and store in version map
			atomic.StoreUint64(&v.nextVersion, newVersion+1)
			v.versions.Store(newVersion, newRootInfo)
			oldRootInfo.Release()
			return nil
		}

		// CAS failed: another goroutine updated the root first
		// Retry with the new current root. Note: newRoot is now stale
		// and will be garbage collected since we didn't publish it.
	}

	return ErrRetry
}

// CreateSnapshot creates a snapshot of the current root.
// Returns a unique snapshot ID that can be used to read consistent state.
func (v *VersionedRoot) CreateSnapshot(ctx context.Context) (SnapshotID, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if atomic.LoadInt32(&v.closedAtomic) != 0 {
		return 0, ErrClosed
	}

	// Get current root
	root := v.current.Load().(*RootInfo)

	// Increment reference count (snapshot holds a reference)
	root.Acquire()

	// Generate snapshot ID using timestamp (to avoid collision with version 0)
	// Format: version (48 bits) + timestamp (16 bits)
	timestamp := uint64(time.Now().UnixMilli() & 0xFFFF)
	snapshotID := SnapshotID((root.Version << 16) | timestamp)

	return snapshotID, nil
}

// ReleaseSnapshot releases a snapshot.
// When all snapshots for a version are released, that version can be garbage collected.
func (v *VersionedRoot) ReleaseSnapshot(ctx context.Context, snapshotID SnapshotID) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if atomic.LoadInt32(&v.closedAtomic) != 0 {
		return ErrClosed
	}

	// Extract version from snapshot ID
	version := uint64(snapshotID) >> 16

	// Find the root info
	value, ok := v.versions.Load(version)
	if !ok {
		return nil // Already released or never existed
	}

	root := value.(*RootInfo)
	refCount := root.Release()

	// If refCount reaches 0, we can garbage collect this version
	if refCount == 0 {
		v.versions.Delete(version)
	}

	return nil
}

// GetVersion retrieves a specific version by snapshot ID.
// Returns nil if the version has been garbage collected.
func (v *VersionedRoot) GetVersion(snapshotID SnapshotID) *RootInfo {
	// Extract version from snapshot ID
	version := uint64(snapshotID) >> 16

	value, ok := v.versions.Load(version)
	if !ok {
		return nil
	}

	root := value.(*RootInfo)
	root.Acquire()
	return root
}

// Close closes the VersionedRoot and prevents further updates.
func (v *VersionedRoot) Close() error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if atomic.LoadInt32(&v.closedAtomic) != 0 {
		return ErrClosed
	}

	atomic.StoreInt32(&v.closedAtomic, 1)

	// Release all versions
	v.versions.Range(func(key, value any) bool {
		root := value.(*RootInfo)
		root.Release()
		return true
	})

	// Clear all versions
	v.versions = sync.Map{}

	return nil
}

// GetCurrentVersion returns the current version number.
func (v *VersionedRoot) GetCurrentVersion() uint64 {
	v.mu.RLock()
	defer v.mu.RUnlock()

	root := v.current.Load().(*RootInfo)
	return root.Version
}

// GetVersionCount returns the number of active versions.
func (v *VersionedRoot) GetVersionCount() int {
	count := 0
	v.versions.Range(func(key, value any) bool {
		_ = key
		_ = value
		count++
		return true
	})
	return count
}

// SetMaxVersions sets the maximum number of versions to keep.
func (v *VersionedRoot) SetMaxVersions(maxVersions int) {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.maxVersions = maxVersions
}

// GetMaxVersions returns the maximum number of versions to keep.
func (v *VersionedRoot) GetMaxVersions() int {
	v.mu.RLock()
	defer v.mu.RUnlock()

	return v.maxVersions
}
