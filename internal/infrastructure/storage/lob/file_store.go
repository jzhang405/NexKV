// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package lob

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/jzhang405/NexKV/internal/infrastructure/storage/mvcc"
	"golang.org/x/sys/unix"
)

// LOB file header layout (40 bytes)
const lobFileHeaderSize = 40

const (
	lobMagic     = "NXLB"
	lobVersion1  = uint16(1)
	lobFlagDeleted = uint16(1) // bit 0: deleted (tombstone)
)

// lobFDCacheEntry is a cached open file descriptor for a LOB file.
type lobFDCacheEntry struct {
	f    *os.File
	lobID uint64
	prev *lobFDCacheEntry
	next *lobFDCacheEntry
}

// lobFDCache is a bounded LRU cache of open LOB file descriptors.
// Avoids repeated open/close for frequently accessed LOBs.
type lobFDCache struct {
	mu       sync.Mutex
	capacity int
	entries  map[uint64]*lobFDCacheEntry
	head     *lobFDCacheEntry // most recently used
	tail     *lobFDCacheEntry // least recently used
}

func newLobFDCache(capacity int) *lobFDCache {
	return &lobFDCache{
		capacity: capacity,
		entries:  make(map[uint64]*lobFDCacheEntry, capacity),
	}
}

func (c *lobFDCache) get(lobID uint64) *os.File {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[lobID]
	if !ok {
		return nil
	}
	c.moveToHead(e)
	return e.f
}

func (c *lobFDCache) put(lobID uint64, f *os.File) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[lobID]; ok {
		e.f = f
		c.moveToHead(e)
		return
	}
	e := &lobFDCacheEntry{f: f, lobID: lobID}
	c.entries[lobID] = e
	c.addToHead(e)
	if len(c.entries) > c.capacity {
		c.evictTail()
	}
}

func (c *lobFDCache) remove(lobID uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[lobID]; ok {
		c.unlink(e)
		e.f.Close()
		delete(c.entries, lobID)
	}
}

func (c *lobFDCache) closeAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.entries {
		e.f.Close()
	}
	c.entries = make(map[uint64]*lobFDCacheEntry)
	c.head = nil
	c.tail = nil
}

func (c *lobFDCache) addToHead(e *lobFDCacheEntry) {
	e.next = c.head
	e.prev = nil
	if c.head != nil {
		c.head.prev = e
	}
	c.head = e
	if c.tail == nil {
		c.tail = e
	}
}

func (c *lobFDCache) moveToHead(e *lobFDCacheEntry) {
	if c.head == e {
		return
	}
	c.unlink(e)
	c.addToHead(e)
}

func (c *lobFDCache) unlink(e *lobFDCacheEntry) {
	if e.prev != nil {
		e.prev.next = e.next
	}
	if e.next != nil {
		e.next.prev = e.prev
	}
	if c.head == e {
		c.head = e.next
	}
	if c.tail == e {
		c.tail = e.prev
	}
	e.prev = nil
	e.next = nil
}

func (c *lobFDCache) evictTail() {
	if c.tail == nil {
		return
	}
	e := c.tail
	c.unlink(e)
	e.f.Close()
	delete(c.entries, e.lobID)
}

const lobFDCacheCapacity = 64

// lobFileStore manages the filesystem storage of LOB files.
// Thread-safe: Create and Delete are serialized via atomic counter + OS-level
// atomic rename/unlink.
type lobFileStore struct {
	rootDir   string
	nextLOBID atomic.Uint64
	fdCache   *lobFDCache
}

// newLOBFileStore creates a new LOB file store rooted at rootDir.
// Creates the directory if it does not exist.
func newLOBFileStore(rootDir string) (*lobFileStore, error) {
	if err := os.MkdirAll(rootDir, 0750); err != nil {
		return nil, fmt.Errorf("lob: create root dir %s: %w", rootDir, err)
	}
	return &lobFileStore{
		rootDir: rootDir,
		fdCache: newLobFDCache(lobFDCacheCapacity),
	}, nil
}

// shardDir returns the sharded directory path for a LOB ID.
// Uses the high 4 bytes: first 2 bytes = top-level, next 2 bytes = sub-level.
// 65536 × 65536 = 4G potential leaf directories (created on demand).
func (s *lobFileStore) shardDir(lobID uint64) string {
	hi := uint32(lobID >> 16) // high 2 bytes
	lo := uint32(lobID)       // low 2 bytes
	return filepath.Join(s.rootDir, fmt.Sprintf("%05d", hi), fmt.Sprintf("%05d", lo))
}

// lobPath returns the full path to a LOB file.
func (s *lobFileStore) lobPath(lobID uint64) string {
	return filepath.Join(s.shardDir(lobID), fmt.Sprintf("%020d.lob", lobID))
}

// tmpPath returns the temporary path for atomic write.
func (s *lobFileStore) tmpPath(lobID uint64) string {
	return filepath.Join(s.shardDir(lobID), fmt.Sprintf(".tmp-%020d", lobID))
}

// Create writes data to a new LOB file atomically.
// Steps: allocate lobID → mkdir shard dir → write to tmp → fsync → rename → return ref.
func (s *lobFileStore) Create(data []byte) (mvcc.LOBFileRef, error) {
	lobID := s.nextLOBID.Add(1)

	dir := s.shardDir(lobID)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return mvcc.LOBFileRef{}, fmt.Errorf("lob: mkdir %s: %w", dir, err)
	}

	tmpPath := s.tmpPath(lobID)
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0640)
	if err != nil {
		return mvcc.LOBFileRef{}, fmt.Errorf("lob: create tmp %s: %w", tmpPath, err)
	}

	// Write header
	header := make([]byte, lobFileHeaderSize)
	copy(header[0:4], lobMagic)
	binary.BigEndian.PutUint16(header[4:6], lobVersion1)
	// Flags: 0 (not deleted)
	binary.BigEndian.PutUint64(header[8:16], lobID)
	binary.BigEndian.PutUint64(header[16:24], uint64(len(data)))
	// DataCRC at offset 24:4 — zero for now, computed on read

	if _, err := f.Write(header); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return mvcc.LOBFileRef{}, fmt.Errorf("lob: write header: %w", err)
	}

	// Write data
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return mvcc.LOBFileRef{}, fmt.Errorf("lob: write data: %w", err)
	}

	// fsync before rename — crash-safe
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return mvcc.LOBFileRef{}, fmt.Errorf("lob: fsync tmp: %w", err)
	}
	f.Close()

	// Atomic rename
	finalPath := s.lobPath(lobID)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		os.Remove(tmpPath)
		return mvcc.LOBFileRef{}, fmt.Errorf("lob: rename %s → %s: %w", tmpPath, finalPath, err)
	}

	return mvcc.LOBFileRef{LOBID: lobID, TotalLen: uint64(len(data))}, nil
}

// Read reads the full data of a LOB file.
// Uses fd cache for hot LOBs, mmap for files > 64KB, pread for smaller.
func (s *lobFileStore) Read(ref mvcc.LOBFileRef) ([]byte, error) {
	// Try fd cache first
	f := s.fdCache.get(ref.LOBID)
	fresh := f == nil
	if fresh {
		var err error
		f, err = os.Open(s.lobPath(ref.LOBID))
		if err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("lob: file not found: %d", ref.LOBID)
			}
			return nil, fmt.Errorf("lob: open %d: %w", ref.LOBID, err)
		}
	} else {
		// Cached fd — seek to 0 (previous read may have advanced pos)
		f.Seek(0, 0)
	}

	// Validate header
	header := make([]byte, lobFileHeaderSize)
	if _, err := f.Read(header); err != nil {
		return nil, fmt.Errorf("lob: read header %d: %w", ref.LOBID, err)
	}
	if string(header[0:4]) != lobMagic {
		return nil, fmt.Errorf("lob: bad magic in %d", ref.LOBID)
	}
	storedID := binary.BigEndian.Uint64(header[8:16])
	if storedID != ref.LOBID {
		return nil, fmt.Errorf("lob: LOBID mismatch: expected %d, got %d", ref.LOBID, storedID)
	}
	flags := binary.BigEndian.Uint16(header[6:8])
	if flags&lobFlagDeleted != 0 {
		return nil, fmt.Errorf("lob: %d is deleted (tombstone)", ref.LOBID)
	}

	dataLen := binary.BigEndian.Uint64(header[16:24])
	if dataLen == 0 {
		return nil, nil
	}

	// Read data region
	var (
		data []byte
		rdErr error
	)
	if dataLen > LOBFileMMapThreshold {
		data, rdErr = s.mmapRead(f, int64(lobFileHeaderSize), int64(dataLen))
	} else {
		data = make([]byte, dataLen)
		_, rdErr = f.ReadAt(data, lobFileHeaderSize)
	}
	if rdErr != nil {
		if fresh {
			f.Close()
		}
		return nil, fmt.Errorf("lob: read data: %w", rdErr)
	}
	if fresh {
		s.fdCache.put(ref.LOBID, f) // cache for next read
	}
	return data, nil
}

// LOBFileMMapThreshold is the file size above which mmap is used for reading.
const LOBFileMMapThreshold = 65536

// mmapRead reads the data region of a LOB file using mmap (with page-aligned offset).
// Falls back to pread if mmap fails (e.g., offset not page-aligned on some platforms).
func (s *lobFileStore) mmapRead(f *os.File, offset, length int64) ([]byte, error) {
	// mmap requires page-aligned offset — map from 0 and slice
	pageSize := int64(os.Getpagesize())
	mmapLen := offset + length
	// Round up to page boundary
	if remainder := mmapLen % pageSize; remainder != 0 {
		mmapLen += pageSize - remainder
	}

	data, err := unix.Mmap(int(f.Fd()), 0, int(mmapLen), unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		// Fallback to pread
		f.Seek(offset, 0)
		buf := make([]byte, length)
		_, readErr := f.Read(buf)
		return buf, readErr
	}
	// Copy data region to Go heap and munmap
	result := make([]byte, length)
	copy(result, data[offset:offset+length])
	unix.Munmap(data)
	return result, nil
}

// Delete unlinks a LOB file.
func (s *lobFileStore) Delete(ref mvcc.LOBFileRef) error {
	s.fdCache.remove(ref.LOBID) // evict from cache before unlink
	path := s.lobPath(ref.LOBID)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("lob: unlink %s: %w", path, err)
	}
	return nil
}

// Retire unlinks multiple LOB files in batch.
func (s *lobFileStore) Retire(lobIDs []uint64) error {
	var firstErr error
	for _, id := range lobIDs {
		path := s.lobPath(id)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// CleanupTmp removes leftover .tmp-* files from crashes.
// Call at startup to clean up any abandoned tmp files.
func (s *lobFileStore) CleanupTmp() error {
	var count int
	err := filepath.Walk(s.rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip inaccessible
		}
		if !info.IsDir() && filepath.Base(path)[:5] == ".tmp-" {
			if err := os.Remove(path); err == nil {
				count++
			}
		}
		return nil
	})
	if count > 0 {
		// log or just silently clean up
	}
	return err
}
