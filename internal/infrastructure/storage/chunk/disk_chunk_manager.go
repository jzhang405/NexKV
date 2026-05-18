// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package chunk

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
)

// DefaultChunkCapacity is the default maximum size of a single chunk file (256MB).
const DefaultChunkCapacity = 256 * 1024 * 1024

// DiskChunkManager implements service.ChunkManager using .ao chunk files.
// Aligns with Lealone ChunkManager.java.
type DiskChunkManager struct {
	dir       string // chunk file directory
	chunkSize int64  // per-chunk capacity

	mu        sync.RWMutex
	chunks    []*ChunkFile          // active chunks (sorted by seq)
	lastChunk *ChunkFile            // most recently written chunk
	maxSeq    uint64                // global max sequence number
	chunkIDs  *chunkIDBitSet        // chunk ID bit field (Lealone BitField)
	idToChunk map[uint32]*ChunkFile // chunkID → ChunkFile
	seqToID   map[uint64]uint32     // seq → chunkID

	closed   atomic.Bool
	readOps  atomic.Int64
	writeOps atomic.Int64
}

// NewDiskChunkManager creates a new DiskChunkManager with no existing chunk files.
func NewDiskChunkManager(dir string, chunkSize int64) (*DiskChunkManager, error) {
	if chunkSize <= 0 {
		chunkSize = DefaultChunkCapacity
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("chunk: create dir %s: %w", dir, err)
	}

	cm := &DiskChunkManager{
		dir:       dir,
		chunkSize: chunkSize,
		chunkIDs:  newChunkIDBitSet(),
		idToChunk: make(map[uint32]*ChunkFile),
		seqToID:   make(map[uint64]uint32),
	}
	return cm, nil
}

// Allocate reserves space for a page in a chunk file.
func (cm *DiskChunkManager) Allocate(size int, pageType uint8) (model.ChunkPosition, error) {
	if cm.closed.Load() {
		return 0, ErrChunkClosed
	}
	if size < MinPagePayload || size > MaxPagePayload {
		return 0, fmt.Errorf("chunk: invalid page size %d (range [%d,%d]): %w",
			size, MinPagePayload, MaxPagePayload, ErrInvalidPageLength)
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Try to append to the last chunk
	if cm.lastChunk != nil {
		nextOff := cm.lastChunk.nextOffset
		if nextOff+int64(size) <= cm.lastChunk.capacity {
			pos, err := model.EncodeChunkPosition(cm.lastChunk.id, uint32(nextOff), pageType)
			if err != nil {
				return 0, err
			}
			cm.lastChunk.nextOffset = nextOff + int64(size)
			cm.writeOps.Add(1)
			return pos, nil
		}
	}

	// Create a new chunk
	c, err := cm.createChunk()
	if err != nil {
		return 0, err
	}

	offset := uint32(ChunkHeaderSize)
	c.nextOffset = int64(offset) + int64(size)
	pos, err := model.EncodeChunkPosition(c.id, offset, pageType)
	if err != nil {
		return 0, err
	}
	cm.writeOps.Add(1)
	return pos, nil
}

// WritePage writes serialized page data at the given position.
func (cm *DiskChunkManager) WritePage(pos model.ChunkPosition, data []byte) error {
	if cm.closed.Load() {
		return ErrChunkClosed
	}

	chunkID := pos.ChunkID()
	cm.mu.RLock()
	c, ok := cm.idToChunk[chunkID]
	cm.mu.RUnlock()
	if !ok {
		return fmt.Errorf("chunk: WritePage: chunk %d not found", chunkID)
	}

	// Per-chunk lock protects pagePosToLen map and serializes I/O
	c.mu.Lock()
	defer c.mu.Unlock()

	offset := int64(pos.FileOffset())
	if _, err := c.file.WriteAt(data, offset); err != nil {
		return fmt.Errorf("chunk: WritePage: %w", err)
	}
	c.pagePosToLen[pos] = int32(len(data))
	return nil
}

// ReadPage reads serialized page data from the given position.
func (cm *DiskChunkManager) ReadPage(pos model.ChunkPosition) ([]byte, error) {
	if cm.closed.Load() {
		return nil, ErrChunkClosed
	}

	chunkID := pos.ChunkID()
	cm.mu.RLock()
	c, ok := cm.idToChunk[chunkID]
	cm.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("chunk: ReadPage: chunk %d not found", chunkID)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	offset := int64(pos.FileOffset())
	length, ok := c.pagePosToLen[pos]
	if !ok {
		return nil, ErrPageNotFound
	}

	buf := make([]byte, length)
	if _, err := c.file.ReadAt(buf, offset); err != nil {
		return nil, fmt.Errorf("chunk: ReadPage: %w", err)
	}
	cm.readOps.Add(1)
	return buf, nil
}

// FreePage marks a page position as removed.
func (cm *DiskChunkManager) FreePage(pos model.ChunkPosition) error {
	if cm.closed.Load() {
		return ErrChunkClosed
	}

	chunkID := pos.ChunkID()
	cm.mu.RLock()
	c, ok := cm.idToChunk[chunkID]
	cm.mu.RUnlock()
	if !ok {
		return fmt.Errorf("chunk: FreePage: chunk %d not found", chunkID)
	}

	c.mu.Lock()
	c.removedPages[pos] = struct{}{}
	c.mu.Unlock()
	return nil
}

// Sync flushes all chunk files to disk.
func (cm *DiskChunkManager) Sync() error {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	for _, c := range cm.chunks {
		if err := c.file.Sync(); err != nil {
			return fmt.Errorf("chunk: sync chunk %d: %w", c.id, err)
		}
	}
	return nil
}

// Stats returns runtime statistics.
func (cm *DiskChunkManager) Stats() service.ChunkManagerStats {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	var totalPages, freePages int64
	var usedBytes, freeBytes int64
	for _, c := range cm.chunks {
		c.mu.Lock()
		totalPages += int64(len(c.pagePosToLen))
		freePages += int64(len(c.removedPages))
		for _, l := range c.pagePosToLen {
			usedBytes += int64(l)
		}
		c.mu.Unlock()
		// Estimate free space (capacity minus used)
		freeBytes += (c.capacity - c.nextOffset)
	}

	return service.ChunkManagerStats{
		TotalChunks:  len(cm.idToChunk),
		ActiveChunks: len(cm.chunks),
		TotalPages:   totalPages,
		FreePages:    freePages,
		UsedBytes:    usedBytes,
		FreeBytes:    freeBytes,
		ReadOps:      cm.readOps.Load(),
		WriteOps:     cm.writeOps.Load(),
	}
}

// Close closes all chunk files.
func (cm *DiskChunkManager) Close() error {
	if !cm.closed.CompareAndSwap(false, true) {
		return nil
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()

	var firstErr error
	for _, c := range cm.chunks {
		c.mu.Lock()
		// Write final header before closing (Lealone: header written at finalization)
		h := &ChunkHeader{
			ID:               c.id,
			PageCount:        int32(len(c.pagePosToLen)),
			BlockSize:        ChunkBlockSize,
			FormatVersion:    1,
			RemovedPageCount: int32(len(c.removedPages)),
		}
		if err := c.writeHeader(h); err != nil && firstErr == nil {
			firstErr = err
		}
		if err := c.file.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		c.mu.Unlock()
	}
	return firstErr
}

// createChunk allocates a new chunk file. Must be called with cm.mu held.
func (cm *DiskChunkManager) createChunk() (*ChunkFile, error) {
	chunkID := cm.chunkIDs.nextClearBit(0)
	cm.chunkIDs.set(chunkID)

	cm.maxSeq++
	seq := cm.maxSeq

	path := fmt.Sprintf("%s/btree_%d_%d.ao", cm.dir, chunkID, seq)
	f, err := openChunkFile(path, cm.chunkSize)
	if err != nil {
		cm.chunkIDs.clear(chunkID)
		cm.maxSeq--
		return nil, err
	}

	c := &ChunkFile{
		id:           chunkID,
		seq:          seq,
		file:         f,
		path:         path,
		capacity:     cm.chunkSize,
		nextOffset:   ChunkHeaderSize,
		pagePosToLen: make(map[model.ChunkPosition]int32),
		removedPages: make(map[model.ChunkPosition]struct{}),
	}

	cm.chunks = append(cm.chunks, c)
	cm.idToChunk[chunkID] = c
	cm.seqToID[seq] = chunkID
	cm.lastChunk = c
	return c, nil
}

// chunkIDBitSet is a simple bitset for tracking used chunk IDs.
type chunkIDBitSet struct {
	words []uint64
}

func newChunkIDBitSet() *chunkIDBitSet {
	return &chunkIDBitSet{words: make([]uint64, (model.MaxChunkID+63)/64)}
}

func (b *chunkIDBitSet) nextClearBit(start uint32) uint32 {
	for i := uint32(start); i <= model.MaxChunkID; i++ {
		word := i / 64
		bit := i % 64
		if word >= uint32(len(b.words)) {
			// Extend
			newWords := make([]uint64, word+1)
			copy(newWords, b.words)
			b.words = newWords
		}
		if b.words[word]&(1<<bit) == 0 {
			return i
		}
	}
	return 1 // fallback
}

func (b *chunkIDBitSet) set(id uint32) {
	word := id / 64
	if word >= uint32(len(b.words)) {
		newWords := make([]uint64, word+1)
		copy(newWords, b.words)
		b.words = newWords
	}
	b.words[word] |= 1 << (id % 64)
}

func (b *chunkIDBitSet) clear(id uint32) {
	word := id / 64
	if word < uint32(len(b.words)) {
		b.words[word] &^= 1 << (id % 64)
	}
}
