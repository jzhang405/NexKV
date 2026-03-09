// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
)

var (
	// ErrWALClosed is returned when operating on a closed WAL.
	ErrWALClosed = errors.New("WAL closed")

	// ErrWALCorrupted is returned when WAL data is corrupted.
	ErrWALCorrupted = errors.New("WAL corrupted")
)

// WAL (Write-Ahead Log) provides durable logging for crash recovery.
//
// Design principles:
// - Append-only log for sequential writes
// - Fixed-size entries for easy parsing
// - Checksum for data integrity
// - Support for truncation and cleanup
type WAL struct {
	// file is the underlying WAL file.
	file *os.File

	// path is the file path.
	path string

	// closed indicates whether the WAL is closed.
	closed atomic.Bool

	// mu protects file operations.
	mu sync.Mutex
}

// WALEntryType represents the type of WAL entry.
type WALEntryType uint8

const (
	// WALEntryTypeInsert represents an insert operation.
	WALEntryTypeInsert WALEntryType = 1

	// WALEntryTypeDelete represents a delete operation.
	WALEntryTypeDelete WALEntryType = 2

	// WALEntryTypeSplit represents a node split operation.
	WALEntryTypeSplit WALEntryType = 3

	// WALEntryTypeCheckpoint represents a checkpoint marker.
	WALEntryTypeCheckpoint WALEntryType = 4
)

// WALEntry represents a single WAL entry.
//
// Layout (fixed header + variable data):
//
//	Offset 0:     Type (1 byte)
//	Offset 1:     KeyLen (2 bytes)
//	Offset 3:     ValueLen (2 bytes)
//	Offset 5:     Key (variable)
//	Offset 5+KeyLen: Value (variable)
//	Offset 5+KeyLen+ValueLen: Checksum (4 bytes)
type WALEntry struct {
	Type     WALEntryType
	Key      []byte
	Value    []byte
	Checksum uint32
}

// NewWAL creates or opens a WAL file.
func NewWAL(path string) (*WAL, error) {
	// Open file in append mode
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("open WAL file: %w", err)
	}

	return &WAL{
		file: file,
		path: path,
	}, nil
}

// Write writes an entry to the WAL.
func (wal *WAL) Write(entry *WALEntry) error {
	if wal.closed.Load() {
		return ErrWALClosed
	}

	if entry == nil {
		return errors.New("Write: entry is nil")
	}

	// Calculate checksum
	entry.Checksum = wal.calculateChecksum(entry)

	// Serialize entry
	data, err := wal.serializeEntry(entry)
	if err != nil {
		return fmt.Errorf("serialize entry: %w", err)
	}

	// Write to file
	wal.mu.Lock()
	defer wal.mu.Unlock()

	if _, err := wal.file.Write(data); err != nil {
		return fmt.Errorf("write entry: %w", err)
	}

	// Sync to disk for durability
	if err := wal.file.Sync(); err != nil {
		return fmt.Errorf("sync WAL: %w", err)
	}

	return nil
}

// Sync flushes the WAL to disk.
func (wal *WAL) Sync() error {
	if wal.closed.Load() {
		return ErrWALClosed
	}

	wal.mu.Lock()
	defer wal.mu.Unlock()

	if err := wal.file.Sync(); err != nil {
		return fmt.Errorf("sync WAL: %w", err)
	}

	return nil
}

// Truncate truncates the WAL file to zero length.
// This should be called after a successful checkpoint.
func (wal *WAL) Truncate() error {
	if wal.closed.Load() {
		return ErrWALClosed
	}

	wal.mu.Lock()
	defer wal.mu.Unlock()

	// Close file
	if err := wal.file.Close(); err != nil {
		return fmt.Errorf("close file: %w", err)
	}

	// Truncate file
	if err := os.Truncate(wal.path, 0); err != nil {
		return fmt.Errorf("truncate file: %w", err)
	}

	// Reopen file
	file, err := os.OpenFile(wal.path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("reopen file: %w", err)
	}

	wal.file = file

	return nil
}

// Replay replays WAL entries from the beginning.
// Returns the number of entries replayed and any error encountered.
func (wal *WAL) Replay(fn func(entry *WALEntry) error) (int, error) {
	if wal.closed.Load() {
		return 0, ErrWALClosed
	}

	// Close current file
	wal.mu.Lock()
	if err := wal.file.Close(); err != nil {
		wal.mu.Unlock()
		return 0, fmt.Errorf("close file: %w", err)
	}
	wal.mu.Unlock()

	// Open file for reading
	file, err := os.Open(wal.path)
	if err != nil {
		return 0, fmt.Errorf("open file for reading: %w", err)
	}
	defer file.Close()

	// Replay entries
	count := 0
	buffer := make([]byte, PageSize)

	for {
		// Read entry header (5 bytes: Type(1) + KeyLen(2) + ValueLen(2))
		header := make([]byte, 5)
		n, err := file.Read(header)
		if err != nil {
			if n == 0 {
				break // EOF
			}
			return count, fmt.Errorf("read header: %w", err)
		}
		if n < 5 {
			return count, ErrWALCorrupted
		}

		// Parse header
		entryType := WALEntryType(header[0])
		keyLen := int(binary.LittleEndian.Uint16(header[1:3]))
		valueLen := int(binary.LittleEndian.Uint16(header[3:5]))

		// Read key and value
		dataSize := keyLen + valueLen + 4 // +4 for checksum
		if dataSize > len(buffer) {
			buffer = make([]byte, dataSize)
		}
		data := buffer[:dataSize]

		n, err = file.Read(data)
		if err != nil {
			return count, fmt.Errorf("read data: %w", err)
		}
		if n < dataSize {
			return count, ErrWALCorrupted
		}

		// Extract key, value, checksum
		key := make([]byte, keyLen)
		copy(key, data[:keyLen])

		value := make([]byte, valueLen)
		copy(value, data[keyLen:keyLen+valueLen])

		checksum := binary.LittleEndian.Uint32(data[keyLen+valueLen : keyLen+valueLen+4])

		entry := &WALEntry{
			Type:     entryType,
			Key:      key,
			Value:    value,
			Checksum: checksum,
		}

		// Verify checksum
		if !wal.verifyChecksum(entry) {
			return count, fmt.Errorf("checksum mismatch for entry %d", count)
		}

		// Call replay function
		if err := fn(entry); err != nil {
			return count, fmt.Errorf("replay function: %w", err)
		}

		count++
	}

	// Reopen file for appending
	wal.mu.Lock()
	defer wal.mu.Unlock()

	file, err = os.OpenFile(wal.path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return count, fmt.Errorf("reopen file for appending: %w", err)
	}

	wal.file = file

	return count, nil
}

// Close closes the WAL.
func (wal *WAL) Close() error {
	if wal.closed.Load() {
		return nil // Already closed
	}

	wal.mu.Lock()
	defer wal.mu.Unlock()

	if err := wal.file.Close(); err != nil {
		return fmt.Errorf("close file: %w", err)
	}

	wal.closed.Store(true)

	return nil
}

// serializeEntry serializes a WAL entry to bytes.
func (wal *WAL) serializeEntry(entry *WALEntry) ([]byte, error) {
	keyLen := len(entry.Key)
	valueLen := len(entry.Value)

	// Total size: 5 (header) + keyLen + valueLen + 4 (checksum)
	totalSize := 5 + keyLen + valueLen + 4

	buf := make([]byte, totalSize)

	// Write header
	buf[0] = uint8(entry.Type)
	binary.LittleEndian.PutUint16(buf[1:3], uint16(keyLen))
	binary.LittleEndian.PutUint16(buf[3:5], uint16(valueLen))

	// Write key
	copy(buf[5:5+keyLen], entry.Key)

	// Write value
	copy(buf[5+keyLen:5+keyLen+valueLen], entry.Value)

	// Write checksum
	offset := 5 + keyLen + valueLen
	binary.LittleEndian.PutUint32(buf[offset:offset+4], entry.Checksum)

	return buf, nil
}

// calculateChecksum calculates a simple checksum for the entry.
func (wal *WAL) calculateChecksum(entry *WALEntry) uint32 {
	var checksum uint32

	// Add type
	checksum += uint32(entry.Type)

	// Add key bytes
	for _, b := range entry.Key {
		checksum += uint32(b)
	}

	// Add value bytes
	for _, b := range entry.Value {
		checksum += uint32(b)
	}

	return checksum
}

// verifyChecksum verifies the checksum of an entry.
func (wal *WAL) verifyChecksum(entry *WALEntry) bool {
	return entry.Checksum == wal.calculateChecksum(entry)
}

// NewInsertEntry creates a new insert entry.
func NewInsertEntry(key, value []byte) *WALEntry {
	return &WALEntry{
		Type:  WALEntryTypeInsert,
		Key:   key,
		Value: value,
	}
}

// NewDeleteEntry creates a new delete entry.
func NewDeleteEntry(key []byte) *WALEntry {
	return &WALEntry{
		Type:  WALEntryTypeDelete,
		Key:   key,
		Value: nil,
	}
}

// NewCheckpointEntry creates a new checkpoint entry.
func NewCheckpointEntry() *WALEntry {
	return &WALEntry{
		Type:  WALEntryTypeCheckpoint,
		Key:   nil,
		Value: nil,
	}
}
