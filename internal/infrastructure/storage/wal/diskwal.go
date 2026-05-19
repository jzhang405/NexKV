// Package wal provides the DiskWAL implementation.
package wal

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// DiskWAL is the on-disk WAL implementation.
type DiskWAL struct {
	mu           sync.RWMutex
	config       *WALConfig
	gcCfg        *WALGroupCommitConfig
	currentLSN   atomic.Uint64
	closed       atomic.Bool
	file         *os.File
	filePath     string
	dir          string
	stats        WALStats
	syncCount    atomic.Int64
	writtenBytes atomic.Int64 // bytes written to current segment
}

// NewDiskWAL creates a new DiskWAL.
func NewDiskWAL(config *WALConfig) (*DiskWAL, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(config.Dir, 0755); err != nil {
		return nil, fmt.Errorf("wal: create dir: %w", err)
	}

	dw := &DiskWAL{
		config: config,
		dir:    config.Dir,
		gcCfg:  DefaultGroupCommitConfig(),
	}
	if err := dw.openSegment(); err != nil {
		return nil, err
	}
	return dw, nil
}

// SetGroupCommitConfig configures Group Commit behavior.
func (w *DiskWAL) SetGroupCommitConfig(cfg *WALGroupCommitConfig) {
	w.mu.Lock()
	w.gcCfg = cfg
	w.mu.Unlock()
}

// --- Segment management ---

func (w *DiskWAL) openSegment() error {
	fileName := fmt.Sprintf("%020d.wal", w.currentLSN.Load()+1)
	w.filePath = filepath.Join(w.dir, fileName)
	f, err := os.OpenFile(w.filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("wal: open segment %s: %w", fileName, err)
	}
	w.file = f
	w.writtenBytes.Store(0)
	return nil
}

func (w *DiskWAL) rotateSegment() error {
	if w.file != nil {
		if err := w.file.Sync(); err != nil {
			return err
		}
		w.file.Close()
	}
	return w.openSegment()
}

func (w *DiskWAL) checkRotate(size int) error {
	newSize := w.writtenBytes.Add(int64(size))
	if newSize >= w.config.SegmentSize {
		return w.rotateSegment()
	}
	return nil
}

// --- Sync ---

func (w *DiskWAL) Sync() error {
	if w.closed.Load() {
		return ErrWALClosed
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.syncLocked()
}

func (w *DiskWAL) syncLocked() error {
	if err := w.file.Sync(); err != nil {
		return fmt.Errorf("wal: sync: %w", err)
	}
	w.syncCount.Add(1)
	return nil
}

// --- Append ---

func (w *DiskWAL) Append(entry *WALEntry) (LSN, error) {
	entries, err := w.AppendBatch([]*WALEntry{entry})
	if err != nil {
		return LSNInvalid, err
	}
	return entries[0], nil
}

func (w *DiskWAL) AppendBatch(entries []*WALEntry) ([]LSN, error) {
	if w.closed.Load() {
		return nil, ErrWALClosed
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	lsns := make([]LSN, len(entries))
	for i, entry := range entries {
		lsn := LSN(w.currentLSN.Add(1))
		entry.LSN = lsn
		lsns[i] = lsn

		data, err := MarshalWALEntry(entry)
		if err != nil {
			return nil, fmt.Errorf("wal: marshal: %w", err)
		}

		// Segment rotation check
		if err := w.checkRotate(len(data)); err != nil {
			return nil, err
		}

		if _, err := w.file.Write(data); err != nil {
			return nil, fmt.Errorf("wal: write: %w", err)
		}

		w.stats.TotalEntries++
		w.stats.TotalBytes += int64(len(data))
	}

	// Sync based on policy
	if w.config.SyncPolicy == SyncPolicyEveryWrite {
		if err := w.syncLocked(); err != nil {
			return nil, err
		}
	}

	return lsns, nil
}

// writeEntries writes entries to the OS buffer without syncing.
// Used by WALAppendItem for Group Commit; the caller handles fsync via PostBatchHook.
func (w *DiskWAL) writeEntries(entries []*WALEntry) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	for _, entry := range entries {
		data, err := MarshalWALEntry(entry)
		if err != nil {
			return fmt.Errorf("wal: marshal: %w", err)
		}
		if err := w.checkRotate(len(data)); err != nil {
			return err
		}
		if _, err := w.file.Write(data); err != nil {
			return fmt.Errorf("wal: write: %w", err)
		}
		w.stats.TotalEntries++
		w.stats.TotalBytes += int64(len(data))
	}
	return nil
}

// FlushBatch syncs the current file. Called by PostBatchHook after batch completes.
func (w *DiskWAL) FlushBatch(items []*WALAppendItem) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.syncLocked(); err != nil {
		for _, item := range items {
			item.Cancel(err)
		}
		return err
	}
	for _, item := range items {
		item.SignalSuccess()
	}
	return nil
}

// --- Recovery ---

func (w *DiskWAL) Recover() ([]*WALEntry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	entries, err := w.scanSegments()
	if err != nil {
		return nil, err
	}

	// Restore currentLSN from recovered entries
	for _, e := range entries {
		if uint64(e.LSN) > w.currentLSN.Load() {
			w.currentLSN.Store(uint64(e.LSN))
		}
	}

	return entries, nil
}

func (w *DiskWAL) CurrentLSN() LSN {
	return LSN(w.currentLSN.Load())
}

func (w *DiskWAL) scanSegments() ([]*WALEntry, error) {
	files, err := os.ReadDir(w.dir)
	if err != nil {
		return nil, fmt.Errorf("wal: read dir: %w", err)
	}

	// Sort by filename (LSN prefix) for deterministic recovery order
	var walFiles []string
	for _, f := range files {
		if f.IsDir() || filepath.Ext(f.Name()) != ".wal" {
			continue
		}
		walFiles = append(walFiles, f.Name())
	}
	sort.Strings(walFiles)

	var allEntries []*WALEntry
	for _, name := range walFiles {
		entries, err := w.recoverFile(filepath.Join(w.dir, name))
		if err != nil {
			// Conservative: skip corrupted files but log
			continue
		}
		allEntries = append(allEntries, entries...)
	}

	// Clean up .wal.deleting residue
	w.cleanDeleting()

	if allEntries == nil {
		return []*WALEntry{}, nil
	}
	return allEntries, nil
}

func (w *DiskWAL) cleanDeleting() {
	files, _ := os.ReadDir(w.dir)
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".wal.deleting") {
			os.Remove(filepath.Join(w.dir, f.Name()))
		}
	}
}

func (w *DiskWAL) recoverFile(path string) ([]*WALEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var entries []*WALEntry
	offset := 0
	for offset+4 <= len(data) {
		// Read Length field (offset 4-7 within entry)
		if offset+8 > len(data) {
			break
		}
		length := int(binary.BigEndian.Uint32(data[offset+4:]))
		entryEnd := offset + 4 + 4 + length + 4 // CRC + Length + paddedPayload + Trailer
		if entryEnd > len(data) {
			break // truncated entry
		}

		entry := &WALEntry{}
		if err := UnmarshalWALEntry(entry, data[offset:entryEnd]); err != nil {
			// Jump scan: try to find the next valid entry by scanning forward
			// aligned positions. This implements the "optimistic guess + CRC verify"
			// pattern from §3.3 (H4-6, C1).
			nextOffset := w.jumpScan(data, offset+8)
			if nextOffset < 0 {
				break // no valid entry found, stop recovery for this file
			}
			offset = nextOffset
			continue
		}
		entries = append(entries, entry)
		offset = entryEnd
	}
	return entries, nil
}

// --- Truncate ---

func (w *DiskWAL) Truncate(lsn LSN) error {
	if w.closed.Load() {
		return ErrWALClosed
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	return w.truncateBefore(uint64(lsn))
}

// truncateBefore removes segments with max-LSN < lsn using rename-then-delete.
func (w *DiskWAL) truncateBefore(lsn uint64) error {
	files, err := os.ReadDir(w.dir)
	if err != nil {
		return err
	}

	for _, f := range files {
		if f.IsDir() || filepath.Ext(f.Name()) != ".wal" {
			continue
		}
		fileLSN, parseErr := strconv.ParseUint(strings.TrimSuffix(f.Name(), ".wal"), 10, 64)
		if parseErr != nil {
			continue
		}
		if fileLSN >= lsn {
			continue // keep this segment
		}

		fPath := filepath.Join(w.dir, f.Name())
		deletingPath := fPath + ".deleting"

		// Step 1: Rename to .deleting
		if err := os.Rename(fPath, deletingPath); err != nil {
			continue
		}
		// Step 2: fsync parent directory
		if dir, e := os.Open(w.dir); e == nil {
			dir.Sync()
			dir.Close()
		}
		// Step 3: Delete
		os.Remove(deletingPath)
		// Step 4: fsync parent directory
		if dir, e := os.Open(w.dir); e == nil {
			dir.Sync()
			dir.Close()
		}
	}

	return nil
}

// --- Async ---

func (w *DiskWAL) AppendAsync(ctx context.Context, entry *WALEntry) model.Task[LSN] {
	return NewCompletedWALTask(func() (LSN, error) {
		return w.Append(entry)
	})
}

func (w *DiskWAL) TruncateAsync(ctx context.Context, lsn LSN) model.Task[struct{}] {
	return NewCompletedTruncateTask(func() (struct{}, error) {
		return struct{}{}, w.Truncate(lsn)
	})
}

// --- Lifecycle ---

func (w *DiskWAL) Close() error {
	if !w.closed.CompareAndSwap(false, true) {
		return ErrWALClosed
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.syncLocked(); err != nil {
		return err
	}
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			return fmt.Errorf("wal: close: %w", err)
		}
	}
	return nil
}

// StartBatchFlusher starts a background goroutine that periodically flushes
// pending WAL batches. Implements the Group Commit time-window trigger (C6).
func (w *DiskWAL) StartBatchFlusher(ctx context.Context) {
	go func() {
		interval := time.Duration(w.gcCfg.BatchTimeoutMs) * time.Millisecond
		if interval <= 0 {
			interval = time.Millisecond
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				w.flushPending()
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (w *DiskWAL) flushPending() {
	w.mu.Lock()
	defer w.mu.Unlock()
	// No-op if nothing pending; sync is cheap when file is already synced.
	_ = w.syncLocked()
}

// jumpScan attempts to find the next valid WAL entry after a corrupted one.
// Scans forward in 8-byte aligned steps, trying to find a valid entry by
// verifying CRC + Trailer. Returns the offset of the next valid entry or -1.
func (w *DiskWAL) jumpScan(data []byte, startOffset int) int {
	maxOffset := len(data) - (4 + 4 + 4) // at least CRC + Length + Trailer
	for off := startOffset; off < maxOffset; off += 8 {
		// Try to read Length at off+4
		if off+8 > len(data) {
			return -1
		}
		length := int(binary.BigEndian.Uint32(data[off+4:]))
		paddedEnd := off + 4 + 4 + length + 4
		if paddedEnd > len(data) {
			continue
		}
		// Verify CRC
		crc := binary.BigEndian.Uint32(data[off:])
		if CRC32C(data[off+4:paddedEnd]) != crc {
			continue
		}
		// Verify Trailer
		trailer := binary.BigEndian.Uint32(data[paddedEnd-4:])
		if trailer != trailerMagic {
			continue
		}
		return off
	}
	return -1
}

// GetStats returns current WAL statistics.
func (w *DiskWAL) GetStats() WALStats {
	w.mu.RLock()
	defer w.mu.RUnlock()
	stats := w.stats
	stats.CurrentLSN = LSN(w.currentLSN.Load())
	stats.SyncCount = w.syncCount.Load()
	return stats
}
