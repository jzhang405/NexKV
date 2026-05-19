// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package chunk

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/offheap"
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
	seqToID   map[uint64]uint32     // seq → chunkID (recovery: seq→chunkID reverse lookup)

	closed   atomic.Bool
	readOps  atomic.Int64
	writeOps atomic.Int64
}

// NewDiskChunkManager creates a new DiskChunkManager with no existing chunk files.
func NewDiskChunkManager(dir string, chunkSize int64) (*DiskChunkManager, error) {
	if chunkSize <= 0 {
		chunkSize = DefaultChunkCapacity
	}
	if chunkSize > (1<<32)-1 {
		return nil, fmt.Errorf("chunk: chunkSize %d exceeds maximum 4GB (FileOffset is uint32)", chunkSize)
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
	if pageType != offheap.PageTypeIndex && pageType != offheap.PageTypeLeaf {
		return 0, fmt.Errorf("chunk: invalid pageType %d (expected 0 or 1)", pageType)
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Determine the chunk and offset; create a new chunk if the last is full or absent
	var c *ChunkFile
	var offset uint32
	if cm.lastChunk != nil && cm.lastChunk.nextOffset+int64(size) <= cm.lastChunk.capacity {
		c = cm.lastChunk
		offset = uint32(c.nextOffset)
	} else {
		var err error
		c, err = cm.createChunk()
		if err != nil {
			return 0, err
		}
		offset = ChunkHeaderSize
	}

	pos, err := model.EncodeChunkPosition(c.id, offset, pageType)
	if err != nil {
		return 0, err
	}
	c.nextOffset = int64(offset) + int64(size)
	return pos, nil
}

// lookupChunk returns the chunk for chunkID. Caller must hold cm.mu (at least RLock).
func (cm *DiskChunkManager) lookupChunk(chunkID uint32) (*ChunkFile, error) {
	if cm.closed.Load() {
		return nil, ErrChunkClosed
	}
	c, ok := cm.idToChunk[chunkID]
	if !ok {
		return nil, fmt.Errorf("chunk: chunk %d not found", chunkID)
	}
	return c, nil
}

// WritePage writes serialized page data at the given position.
func (cm *DiskChunkManager) WritePage(pos model.ChunkPosition, data []byte) error {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	c, err := cm.lookupChunk(pos.ChunkID())
	if err != nil {
		return err
	}

	// Per-chunk lock protects pagePosToLen map and serializes I/O
	c.mu.Lock()
	defer c.mu.Unlock()

	offset := int64(pos.FileOffset())
	if _, err := c.file.WriteAt(data, offset); err != nil {
		return fmt.Errorf("chunk: WritePage: %w", err)
	}
	cm.writeOps.Add(1)
	c.pagePosToLen[pos] = int32(len(data))
	return nil
}

// ReadPage reads serialized page data from the given position.
func (cm *DiskChunkManager) ReadPage(pos model.ChunkPosition) ([]byte, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	c, err := cm.lookupChunk(pos.ChunkID())
	if err != nil {
		return nil, err
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
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	c, err := cm.lookupChunk(pos.ChunkID())
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.pagePosToLen[pos]; !exists {
		return fmt.Errorf("chunk: FreePage: position %s not allocated", pos)
	}
	c.removedPages[pos] = struct{}{}
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
		freeBytes += (c.capacity - c.nextOffset)
		c.mu.Unlock()
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
		// Write final header before closing.
		// TODO(phase4.3): Fill RootPagePos, SumOfPageLength, SumOfLivePageLength,
		//   PagePositionAndLengthOffset, RemovedPageOffset, LastTransactionID, MapSize
		//   from the BTree state at checkpoint time.
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
	chunkID, err := cm.chunkIDs.nextClearBit(0)
	if err != nil {
		return nil, err
	}
	cm.chunkIDs.set(chunkID)

	cm.maxSeq++
	path := fmt.Sprintf("%s/btree_%d_%d.ao", cm.dir, chunkID, cm.maxSeq)
	f, err := openChunkFile(path, cm.chunkSize)
	if err != nil {
		cm.chunkIDs.clear(chunkID)
		cm.maxSeq--
		return nil, err
	}

	c := &ChunkFile{
		id:           chunkID,
		seq:          cm.maxSeq,
		file:         f,
		capacity:     cm.chunkSize,
		nextOffset:   ChunkHeaderSize,
		pagePosToLen: make(map[model.ChunkPosition]int32),
		removedPages: make(map[model.ChunkPosition]struct{}),
	}

	cm.chunks = append(cm.chunks, c)
	cm.idToChunk[chunkID] = c
	cm.seqToID[cm.maxSeq] = chunkID
	cm.lastChunk = c
	return c, nil
}

// chunkFilenameRe is the expected format: btree_[chunkId]_[seq].ao

// parseChunkFilename parses a chunk filename into chunkID and seq.
// Expected format: "btree_0_1.ao" → (chunkID=0, seq=1).
// Rejects files with trailing content (e.g., "btree_0_1.ao.tmp", "btree_0_1.ao.backup").
func parseChunkFilename(name string) (chunkID uint32, seq uint64, err error) {
	var cid, s uint64
	n, err := fmt.Sscanf(name, "btree_%d_%d.ao", &cid, &s)
	if err != nil || n != 2 {
		return 0, 0, fmt.Errorf("chunk: invalid chunk filename %q", name)
	}
	// Reject files with trailing content (e.g., .tmp, .backup)
	if name != fmt.Sprintf("btree_%d_%d.ao", cid, s) {
		return 0, 0, fmt.Errorf("chunk: invalid chunk filename %q", name)
	}
	return uint32(cid), s, nil
}

// RestoreDiskChunkManager recovers a DiskChunkManager from existing .ao chunk files.
// Aligns with Lealone ChunkManager.init() recovery protocol:
//
//  1. Scan directory: list all btree_*_*.ao files
//  2. Delete zero-length files (created by crash during open)
//  3. Parse filenames: btree_[chunkId]_[seq].ao → chunkID + seq
//     Handle duplicate chunkIDs: keep highest seq (backup recovery)
//  4. Sort chunks by seq ascending
//  5. Open highest-seq chunk as lastChunk
//  6. Validate dual-block header for each chunk:
//     - Block 0 valid → parse header
//     - Block 0 corrupted, block 1 valid → recover from duplicate
//     - Both corrupted → mark as corrupted, skip
//  7. Recover metadata from headers
//  8. Rebuild in-memory structures: chunkIDs BitSet, idToChunk, seqToID, chunks
//
// If no valid chunk files exist, delegates to NewDiskChunkManager (first-start scenario).
// chunkSize is used when creating new chunks after recovery (0 = use DefaultChunkCapacity).
func RestoreDiskChunkManager(dir string, chunkSize int64) (*DiskChunkManager, error) {
	// Step 1: Scan directory
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return NewDiskChunkManager(dir, chunkSize)
		}
		return nil, fmt.Errorf("chunk: restore: read dir %s: %w", dir, err)
	}

	// Step 2-3: Parse filenames, delete zero-length files, dedup by chunkID
	type fileEntry struct {
		name    string
		chunkID uint32
		seq     uint64
	}
	bestByID := make(map[uint32]fileEntry)

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, infoErr := e.Info()
		if infoErr != nil {
			continue
		}
		// Step 2: Delete zero-length files (crash residue, best-effort)
		if info.Size() == 0 {
			_ = os.Remove(filepath.Join(dir, e.Name()))
			continue
		}
		// Step 3a: Parse filename
		cid, s, parseErr := parseChunkFilename(e.Name())
		if parseErr != nil {
			continue // skip non-chunk files
		}
		fe := fileEntry{name: e.Name(), chunkID: cid, seq: s}
		// Step 3c: Handle duplicate chunkID — keep highest seq (best-effort removal)
		if existing, ok := bestByID[cid]; ok {
			if s > existing.seq {
				_ = os.Remove(filepath.Join(dir, existing.name))
				bestByID[cid] = fe
			} else {
				_ = os.Remove(filepath.Join(dir, fe.name))
			}
		} else {
			bestByID[cid] = fe
		}
	}

	// No valid chunk files → first start
	if len(bestByID) == 0 {
		return NewDiskChunkManager(dir, chunkSize)
	}

	// Step 4: Sort by seq ascending
	sorted := make([]fileEntry, 0, len(bestByID))
	for _, fe := range bestByID {
		sorted = append(sorted, fe)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].seq < sorted[j].seq })

	// Determine chunkSize from the first valid file we open
	if chunkSize <= 0 {
		chunkSize = DefaultChunkCapacity
	}

	cm := &DiskChunkManager{
		dir:       dir,
		chunkSize: chunkSize,
		maxSeq:    sorted[len(sorted)-1].seq,
		chunkIDs:  newChunkIDBitSet(),
		idToChunk: make(map[uint32]*ChunkFile),
		seqToID:   make(map[uint64]uint32),
	}

	// Step 5-7: Open each chunk, validate header, recover metadata
	for _, fe := range sorted {
		path := filepath.Join(dir, fe.name)
		f, openErr := os.OpenFile(path, os.O_RDWR, 0600)
		if openErr != nil {
			continue
		}

		st, statErr := f.Stat()
		if statErr != nil {
			f.Close()
			continue
		}

		c := &ChunkFile{
			id:           fe.chunkID,
			seq:          fe.seq,
			file:         f,
			capacity:     st.Size(),
			nextOffset:   ChunkHeaderSize,
			pagePosToLen: make(map[model.ChunkPosition]int32),
			removedPages: make(map[model.ChunkPosition]struct{}),
		}

		// Step 6: Validate dual-block header
		// TODO(phase4.3): Recover pagePosToLen from PagePositionAndLengthOffset,
		//   removedPages from RemovedPageOffset, and RootPagePos from header.
		if _, err := c.readHeader(); err != nil {
			f.Close()
			continue
		}

		// Step 7: Adopt chunk into manager
		cm.chunks = append(cm.chunks, c)
		cm.idToChunk[fe.chunkID] = c
		cm.seqToID[fe.seq] = fe.chunkID
		cm.chunkIDs.set(fe.chunkID)
		cm.lastChunk = c
	}

	// No valid chunks after validation → first start
	if len(cm.chunks) == 0 {
		return NewDiskChunkManager(dir, chunkSize)
	}

	return cm, nil
}

// chunkIDBitSet is a simple bitset for tracking used chunk IDs.
// All methods must be called with cm.mu held (external synchronization).
type chunkIDBitSet struct {
	words []uint64
}

func newChunkIDBitSet() *chunkIDBitSet {
	return &chunkIDBitSet{words: make([]uint64, (model.MaxChunkID+63)/64)}
}

func (b *chunkIDBitSet) ensureWord(word uint32) {
	if word >= uint32(len(b.words)) {
		newWords := make([]uint64, word+1)
		copy(newWords, b.words)
		b.words = newWords
	}
}

func (b *chunkIDBitSet) nextClearBit(start uint32) (uint32, error) {
	for i := uint32(start); i <= model.MaxChunkID; i++ {
		word := i / 64
		b.ensureWord(word)
		if b.words[word]&(1<<(i%64)) == 0 {
			return i, nil
		}
	}
	return 0, fmt.Errorf("chunk: %d IDs exhausted: %w", model.MaxChunkID+1, ErrChunkIDExhausted)
}

func (b *chunkIDBitSet) set(id uint32) {
	word := id / 64
	b.ensureWord(word)
	b.words[word] |= 1 << (id % 64)
}

func (b *chunkIDBitSet) clear(id uint32) {
	word := id / 64
	if word < uint32(len(b.words)) {
		b.words[word] &^= 1 << (id % 64)
	}
}
