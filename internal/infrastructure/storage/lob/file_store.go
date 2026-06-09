// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package lob

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/infrastructure/storage/mvcc"
	"golang.org/x/sys/unix"
)

// LOB file header layout (40 bytes)
const lobFileHeaderSize = 40

const (
	lobMagic       = "NXLB"
	lobVersion1    = uint16(1)
	lobFlagDeleted = uint16(1) // bit 0: deleted (tombstone)
)

// LOB file naming (flat directory, same .ao suffix as BTree chunks).
//
//	data/
//	  btree_0_1.ao          ← BTree page chunk (append-only)
//	  btree_0_2.ao
//	  lob_0000000000000001.ao  ← LOB file (append-only, same suffix!)
//	  lob_tmp_0000000000000002.ao ← writing in progress (cleaned on crash)

const (
	lobFilePrefix    = "lob_"
	lobTmpFilePrefix = "lob_tmp_"
	lobFileExt       = ".ao"
)

func lobFilePath(dir string, lobID uint64) string {
	return fmt.Sprintf("%s/%s%020d%s", dir, lobFilePrefix, lobID, lobFileExt)
}

func lobTmpPath(dir string, lobID uint64) string {
	return fmt.Sprintf("%s/%s%020d%s", dir, lobTmpFilePrefix, lobID, lobFileExt)
}

// ---------------------------------------------------------------------------
// fd cache
// ---------------------------------------------------------------------------

// lobFDCacheEntry is a cached open file descriptor for a LOB file.
type lobFDCacheEntry struct {
	f        *os.File
	lobID    uint64
	refCount int // active borrowers — fd closed only when evicted with refCount == 0
	prev     *lobFDCacheEntry
	next     *lobFDCacheEntry
}

// FDCacheStats holds fd cache hit/miss counters.
type FDCacheStats struct {
	Hits   uint64
	Misses uint64
	Size   int
}

// HitRate returns the cache hit rate (0.0 ~ 1.0).
func (s FDCacheStats) HitRate() float64 {
	total := s.Hits + s.Misses
	if total == 0 {
		return 0
	}
	return float64(s.Hits) / float64(total)
}

// lobFDCache is a bounded LRU cache of open LOB file descriptors with reference counting.
//
// Protocol:
//   - get(id) → fd, refCount++; caller MUST call release(id) when done
//   - release(id) → refCount--; if entry pending removal and refCount==0, close fd
//   - add(id, fd) → insert new entry
//   - remove(id) → unlink from LRU; if refCount==0 close immediately, else defer to release
type lobFDCache struct {
	mu           sync.Mutex
	capacity     int
	entries      map[uint64]*lobFDCacheEntry
	pendingClose map[uint64]*lobFDCacheEntry // entries removed from LRU but still borrowed
	head         *lobFDCacheEntry            // most recently used
	tail         *lobFDCacheEntry            // least recently used
	hits         uint64
	misses       uint64
}

func newLobFDCache(capacity int) *lobFDCache {
	return &lobFDCache{
		capacity:     capacity,
		entries:      make(map[uint64]*lobFDCacheEntry, capacity),
		pendingClose: make(map[uint64]*lobFDCacheEntry),
	}
}

func (c *lobFDCache) get(lobID uint64) *os.File {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[lobID]
	if !ok {
		c.misses++
		return nil
	}
	c.hits++
	e.refCount++
	c.moveToHead(e)
	return e.f
}

func (c *lobFDCache) release(lobID uint64) {
	c.mu.Lock()
	e, ok := c.entries[lobID]
	if ok {
		e.refCount--
		c.mu.Unlock()
		return
	}
	e, ok = c.pendingClose[lobID]
	if !ok {
		c.mu.Unlock()
		return
	}
	e.refCount--
	if e.refCount <= 0 {
		delete(c.pendingClose, lobID)
		c.mu.Unlock()
		e.f.Close()
		return
	}
	c.mu.Unlock()
}

func (c *lobFDCache) stats() FDCacheStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return FDCacheStats{
		Hits:   c.hits,
		Misses: c.misses,
		Size:   len(c.entries),
	}
}

func (c *lobFDCache) add(lobID uint64, f *os.File) {
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
	e, ok := c.entries[lobID]
	if !ok {
		c.mu.Unlock()
		return
	}
	c.unlink(e)
	delete(c.entries, lobID)
	if e.refCount > 0 {
		c.pendingClose[lobID] = e
		c.mu.Unlock()
	} else {
		c.mu.Unlock()
		e.f.Close()
	}
}

func (c *lobFDCache) closeAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.entries {
		e.f.Close()
	}
	for _, e := range c.pendingClose {
		e.f.Close()
	}
	c.entries = make(map[uint64]*lobFDCacheEntry)
	c.pendingClose = make(map[uint64]*lobFDCacheEntry)
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
	delete(c.entries, e.lobID)
	if e.refCount > 0 {
		c.pendingClose[e.lobID] = e
	} else {
		e.f.Close()
	}
}

// ---------------------------------------------------------------------------
// fsync group-commit
// ---------------------------------------------------------------------------

type fsyncGroup struct {
	enabled  bool // skip fsync when false (benchmark only)
	entries  chan fsyncEntry
	interval time.Duration
	maxBatch int
	ctx      context.Context
	cancel   context.CancelFunc
	closed   atomic.Bool
}

type fsyncEntry struct {
	fd     *os.File
	doneCh chan error
}

func newFsyncGroup(ctx context.Context, cfg Config) *fsyncGroup {
	ctx, cancel := context.WithCancel(ctx)
	g := &fsyncGroup{
		enabled:  cfg.FsyncEnabled,
			entries:  make(chan fsyncEntry, cfg.FsyncQueueSize),
		interval: cfg.FsyncInterval,
		maxBatch: cfg.FsyncMaxBatch,
		ctx:      ctx,
		cancel:   cancel,
	}
	go g.loop()
	return g
}

func (g *fsyncGroup) loop() {
	var batch []fsyncEntry
	ticker := time.NewTicker(g.interval)
	defer ticker.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		for _, e := range batch {
			e.doneCh <- e.fd.Sync()
		}
		batch = batch[:0]
	}

	for {
		select {
		case e := <-g.entries:
			batch = append(batch, e)
			if len(batch) >= g.maxBatch {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-g.ctx.Done():
			for {
				select {
				case e := <-g.entries:
					batch = append(batch, e)
				default:
					flush()
					return
				}
			}
		}
	}
}

func (g *fsyncGroup) Sync(fd *os.File) error {
	if !g.enabled {
		return nil // fsync disabled (benchmark mode)
	}
	if g.closed.Load() {
		return fd.Sync()
	}
	if len(g.entries) == 0 {
		return fd.Sync() // fast path: direct fdatasync
	}
	doneCh := make(chan error, 1)
	select {
	case g.entries <- fsyncEntry{fd: fd, doneCh: doneCh}:
	case <-g.ctx.Done():
		return fd.Sync()
	}
	select {
	case err := <-doneCh:
		return err
	case <-g.ctx.Done():
		return fd.Sync()
	}
}

func (g *fsyncGroup) close() {
	g.closed.Store(true)
	g.cancel()
}

// ---------------------------------------------------------------------------
// lobFileStore — flat .ao directory, no subdirectory sharding
// ---------------------------------------------------------------------------

type lobFileStore struct {
	cfg       Config
	rootDir   string
	nextLOBID atomic.Uint64
	fdCache   *lobFDCache
	fsync     *fsyncGroup
}

func newLOBFileStore(rootDir string, cfg Config) (*lobFileStore, error) {
	if err := os.MkdirAll(rootDir, 0750); err != nil {
		return nil, fmt.Errorf("lob: create root dir %s: %w", rootDir, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	_ = cancel // fsyncGroup owns ctx, cancel on Close via fsync.close()
	s := &lobFileStore{
		cfg:     cfg,
		rootDir: rootDir,
		fdCache: newLobFDCache(cfg.FDCacheCapacity),
		fsync:   newFsyncGroup(ctx, cfg),
	}
	// Random high 32 bits to avoid ID collisions after restart.
	var randBuf [4]byte
	if _, err := rand.Read(randBuf[:]); err == nil {
		s.nextLOBID.Store(uint64(binary.BigEndian.Uint32(randBuf[:]))<<32 | 1)
	}
	return s, nil
}

// Close releases all resources held by the store.
func (s *lobFileStore) Close() {
	s.fsync.close()
	s.fdCache.closeAll()
}

// ---------------------------------------------------------------------------
// CRUD
// ---------------------------------------------------------------------------

// Create writes data to a new LOB file atomically.
// Steps: allocate lobID → write to tmp → fsync → rename to lob_{id}.ao.
func (s *lobFileStore) Create(data []byte) (mvcc.LOBFileRef, error) {
	lobID := s.nextLOBID.Add(1)

	tmpPath := lobTmpPath(s.rootDir, lobID)
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0640)
	if err != nil {
		return mvcc.LOBFileRef{}, fmt.Errorf("lob: create tmp %s: %w", tmpPath, err)
	}

	header := make([]byte, lobFileHeaderSize)
	copy(header[0:4], lobMagic)
	binary.BigEndian.PutUint16(header[4:6], lobVersion1)
	binary.BigEndian.PutUint64(header[8:16], lobID)
	binary.BigEndian.PutUint64(header[16:24], uint64(len(data)))

	if _, err := f.Write(header); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return mvcc.LOBFileRef{}, fmt.Errorf("lob: write header: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return mvcc.LOBFileRef{}, fmt.Errorf("lob: write data: %w", err)
	}
	if err := s.fsync.Sync(f); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return mvcc.LOBFileRef{}, fmt.Errorf("lob: fsync tmp: %w", err)
	}
	f.Close()

	finalPath := lobFilePath(s.rootDir, lobID)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		os.Remove(tmpPath)
		return mvcc.LOBFileRef{}, fmt.Errorf("lob: rename %s → %s: %w", tmpPath, finalPath, err)
	}

	return mvcc.LOBFileRef{LOBID: lobID, TotalLen: uint64(len(data))}, nil
}

// Read reads the full data of a LOB file.
func (s *lobFileStore) Read(ref mvcc.LOBFileRef) ([]byte, error) {
	f := s.fdCache.get(ref.LOBID)
	cached := f != nil
	if !cached {
		var err error
		f, err = os.Open(lobFilePath(s.rootDir, ref.LOBID))
		if err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("lob: file not found: %d", ref.LOBID)
			}
			return nil, fmt.Errorf("lob: open %d: %w", ref.LOBID, err)
		}
	}

	header := make([]byte, lobFileHeaderSize)
	if _, err := f.ReadAt(header, 0); err != nil {
		if !cached {
			f.Close()
		} else {
			s.fdCache.release(ref.LOBID)
		}
		return nil, fmt.Errorf("lob: read header %d: %w", ref.LOBID, err)
	}
	if string(header[0:4]) != lobMagic {
		if !cached {
			f.Close()
		} else {
			s.fdCache.release(ref.LOBID)
		}
		return nil, fmt.Errorf("lob: bad magic in %d", ref.LOBID)
	}
	storedID := binary.BigEndian.Uint64(header[8:16])
	if storedID != ref.LOBID {
		if !cached {
			f.Close()
		} else {
			s.fdCache.release(ref.LOBID)
		}
		return nil, fmt.Errorf("lob: LOBID mismatch: expected %d, got %d", ref.LOBID, storedID)
	}
	flags := binary.BigEndian.Uint16(header[6:8])
	if flags&lobFlagDeleted != 0 {
		if !cached {
			f.Close()
		} else {
			s.fdCache.release(ref.LOBID)
		}
		return nil, fmt.Errorf("lob: %d is deleted (tombstone)", ref.LOBID)
	}

	dataLen := binary.BigEndian.Uint64(header[16:24])
	if dataLen == 0 {
		if cached {
			s.fdCache.release(ref.LOBID)
		}
		return nil, nil
	}

	var (
		data  []byte
		rdErr error
	)
	if dataLen > uint64(s.cfg.FileMMapThreshold) {
		data, rdErr = s.mmapRead(f, int64(lobFileHeaderSize), int64(dataLen))
	} else {
		data = make([]byte, dataLen)
		_, rdErr = f.ReadAt(data, lobFileHeaderSize)
	}

	if rdErr != nil {
		if !cached {
			f.Close()
		} else {
			s.fdCache.release(ref.LOBID)
		}
		return nil, fmt.Errorf("lob: read data: %w", rdErr)
	}

	if cached {
		s.fdCache.release(ref.LOBID)
	} else {
		s.fdCache.add(ref.LOBID, f)
	}
	return data, nil
}

func (s *lobFileStore) mmapRead(f *os.File, offset, length int64) ([]byte, error) {
	pageSize := int64(os.Getpagesize())
	mmapLen := offset + length
	if remainder := mmapLen % pageSize; remainder != 0 {
		mmapLen += pageSize - remainder
	}

	data, err := unix.Mmap(int(f.Fd()), 0, int(mmapLen), unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		buf := make([]byte, length)
		_, readErr := f.ReadAt(buf, offset)
		return buf, readErr
	}
	result := make([]byte, length)
	copy(result, data[offset:offset+length])
	unix.Munmap(data)
	return result, nil
}

// Delete unlinks a LOB file.
func (s *lobFileStore) Delete(ref mvcc.LOBFileRef) error {
	s.fdCache.remove(ref.LOBID)
	path := lobFilePath(s.rootDir, ref.LOBID)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("lob: unlink %s: %w", path, err)
	}
	return nil
}

// CleanupTmp removes leftover lob_tmp_*.ao files from crashes.
// Call at startup.
func (s *lobFileStore) CleanupTmp() error {
	entries, err := os.ReadDir(s.rootDir)
	if err != nil {
		return err
	}
	var count int
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) > len(lobTmpFilePrefix) && name[:len(lobTmpFilePrefix)] == lobTmpFilePrefix {
			path := s.rootDir + "/" + name
			if rmErr := os.Remove(path); rmErr == nil {
				count++
			}
		}
	}
	return nil
}
