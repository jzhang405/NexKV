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
	"path/filepath"
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
// Avoids repeated open/close for frequently accessed LOBs.
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

// get returns a cached fd and increments its reference count.
// Caller MUST call release(lobID) when done with the fd.
// Returns nil if not cached.
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

// release decrements the reference count for a previously get'd fd.
// If the entry was removed from LRU (pending close), closes the fd when refCount reaches 0.
func (c *lobFDCache) release(lobID uint64) {
	c.mu.Lock()
	e, ok := c.entries[lobID]
	if ok {
		e.refCount--
		c.mu.Unlock()
		return
	}
	// Not in entries — check pendingClose
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

// Stats returns cache hit/miss counters and current size.
func (c *lobFDCache) stats() FDCacheStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return FDCacheStats{
		Hits:   c.hits,
		Misses: c.misses,
		Size:   len(c.entries),
	}
}

// add inserts a new fd into the cache for the given lobID.
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

// remove unlinks a fd from the cache.
// If refCount > 0 (active borrowers), defers close to release(); otherwise closes immediately.
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
		// Active borrowers — defer close until last release()
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

// fsyncGroup batches f.Sync() calls to amortize fsync latency.
// Writers submit fd → background goroutine calls f.Sync() in batches →
// writers get notified via channel → rename.
type fsyncGroup struct {
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
			// Drain remaining entries before shutdown
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
	if g.closed.Load() {
		return fd.Sync() // group closed, direct fsync
	}
	// Fast path: if no pending entries in the batch channel, do direct fsync.
	// This avoids the 1ms ticker latency for single-threaded workloads while
	// still allowing concurrent writers to batch via the channel slow path.
	if len(g.entries) == 0 {
		return fd.Sync()
	}
	doneCh := make(chan error, 1)
	select {
	case g.entries <- fsyncEntry{fd: fd, doneCh: doneCh}:
	case <-g.ctx.Done():
		return fd.Sync() // fallback: direct fsync
	}
	select {
	case err := <-doneCh:
		return err
	case <-g.ctx.Done():
		return fd.Sync() // fallback: direct fsync if group shutting down
	}
}

func (g *fsyncGroup) close() {
	g.closed.Store(true)
	g.cancel()
}

// lobFileStore manages the filesystem storage of LOB files.
// Thread-safe: Create and Delete are serialized via atomic counter + OS-level
// atomic rename/unlink.
type lobFileStore struct {
	cfg       Config
	rootDir   string
	nextLOBID atomic.Uint64
	fdCache   *lobFDCache
	fsync     *fsyncGroup

	cleanupCtx    context.Context
	cleanupCancel context.CancelFunc
}

// newLOBFileStore creates a new LOB file store rooted at rootDir with the given config.
func newLOBFileStore(rootDir string, cfg Config) (*lobFileStore, error) {
	if err := os.MkdirAll(rootDir, 0750); err != nil {
		return nil, fmt.Errorf("lob: create root dir %s: %w", rootDir, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &lobFileStore{
		cfg:           cfg,
		rootDir:       rootDir,
		fdCache:       newLobFDCache(cfg.FDCacheCapacity),
		cleanupCtx:    ctx,
		cleanupCancel: cancel,
		fsync:         newFsyncGroup(ctx, cfg),
	}
	// Initialize nextLOBID with random high 32 bits to avoid ID collisions after restart.
	var randBuf [4]byte
	if _, err := rand.Read(randBuf[:]); err == nil {
		s.nextLOBID.Store(uint64(binary.BigEndian.Uint32(randBuf[:]))<<32 | 1)
	}
	if cfg.CleanerInterval > 0 {
		s.startEmptyDirCleaner(cfg.CleanerInterval)
	}
	return s, nil
}

// startEmptyDirCleaner runs a background goroutine that periodically removes
// empty leaf directories. Interval must be > 0. Call stopEmptyDirCleaner to shut down.
func (s *lobFileStore) startEmptyDirCleaner(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.cleanEmptyDirs()
			case <-s.cleanupCtx.Done():
				return
			}
		}
	}()
}

// stopEmptyDirCleaner stops the background cleaner goroutine.
func (s *lobFileStore) stopEmptyDirCleaner() {
	s.cleanupCancel()
}

// cleanEmptyDirs walks the LOB directory tree bottom-up and removes empty
// leaf directories (the shard level 2 directories that contain individual .lob files).
// Does NOT remove shard level 1 directories or the root.
func (s *lobFileStore) cleanEmptyDirs() {
	var emptyDirs []string
	filepath.Walk(s.rootDir, func(p string, info os.FileInfo, err error) error {
		if err != nil || !info.IsDir() || p == s.rootDir {
			return nil
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			return nil
		}
		hasSubdirs := false
		for _, e := range entries {
			if e.IsDir() {
				hasSubdirs = true
				break
			}
		}
		if !hasSubdirs && len(entries) == 0 {
			emptyDirs = append(emptyDirs, p)
		}
		return nil
	})

	for i := len(emptyDirs) - 1; i >= 0; i-- {
		_ = os.Remove(emptyDirs[i])
	}
	for i := len(emptyDirs) - 1; i >= 0; i-- {
		parent := filepath.Dir(emptyDirs[i])
		if parent == s.rootDir {
			continue
		}
		entries, _ := os.ReadDir(parent)
		if len(entries) == 0 {
			_ = os.Remove(parent)
		}
	}
}

// Close releases all resources held by the store.
func (s *lobFileStore) Close() {
	s.stopEmptyDirCleaner()
	s.fsync.close()
	s.fdCache.closeAll()
}

// shardDir returns the sharded directory path for a LOB ID.
func (s *lobFileStore) shardDir(lobID uint64) string {
	hi := uint32(lobID >> 16)
	lo := uint32(lobID)
	return filepath.Join(s.rootDir, fmt.Sprintf("%05d", hi), fmt.Sprintf("%05d", lo))
}

func (s *lobFileStore) lobPath(lobID uint64) string {
	return filepath.Join(s.shardDir(lobID), fmt.Sprintf("%020d.lob", lobID))
}

func (s *lobFileStore) tmpPath(lobID uint64) string {
	return filepath.Join(s.shardDir(lobID), fmt.Sprintf(".tmp-%020d", lobID))
}

// Create writes data to a new LOB file atomically.
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

	finalPath := s.lobPath(lobID)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		os.Remove(tmpPath)
		return mvcc.LOBFileRef{}, fmt.Errorf("lob: rename %s → %s: %w", tmpPath, finalPath, err)
	}

	return mvcc.LOBFileRef{LOBID: lobID, TotalLen: uint64(len(data))}, nil
}

// Read reads the full data of a LOB file.
// Uses fd cache for hot LOBs, mmap for files > 64KB, ReadAt for smaller.
func (s *lobFileStore) Read(ref mvcc.LOBFileRef) ([]byte, error) {
	// Try fd cache first (increments refCount on hit)
	f := s.fdCache.get(ref.LOBID)
	cached := f != nil
	if !cached {
		var err error
		f, err = os.Open(s.lobPath(ref.LOBID))
		if err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("lob: file not found: %d", ref.LOBID)
			}
			return nil, fmt.Errorf("lob: open %d: %w", ref.LOBID, err)
		}
	}

	// Always use ReadAt — stateless, no Seek position, safe on shared cached fds
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
		s.fdCache.add(ref.LOBID, f) // cache for next read
	}
	return data, nil
}

// mmapRead reads the data region of a LOB file using mmap (with page-aligned offset).
// Falls back to ReadAt if mmap fails.
func (s *lobFileStore) mmapRead(f *os.File, offset, length int64) ([]byte, error) {
	pageSize := int64(os.Getpagesize())
	mmapLen := offset + length
	if remainder := mmapLen % pageSize; remainder != 0 {
		mmapLen += pageSize - remainder
	}

	data, err := unix.Mmap(int(f.Fd()), 0, int(mmapLen), unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		// Fallback to ReadAt — safe on shared fds (no Seek needed)
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
	s.fdCache.remove(ref.LOBID) // evict from cache before unlink
	path := s.lobPath(ref.LOBID)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("lob: unlink %s: %w", path, err)
	}
	return nil
}

// CleanupTmp removes leftover .tmp-* files from crashes.
// Call at startup to clean up any abandoned tmp files.
func (s *lobFileStore) CleanupTmp() error {
	var count int
	err := filepath.Walk(s.rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if len(base) >= 5 && base[:5] == ".tmp-" {
			if rmErr := os.Remove(path); rmErr == nil {
				count++
			}
		}
		return nil
	})
	_ = count // used for logging if needed
	return err
}
