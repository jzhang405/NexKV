// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

// Package service provides domain service interfaces.
package service

import "github.com/jzhang405/NexKV/internal/domain/model"

// ChunkManager manages physical storage of BTree pages in .ao files.
// Pages are assigned unique positions (ChunkPosition), enabling on-demand lazy loading.
// The interface lives in the domain layer, matching the precedent of service.WAL.
//
// Implementations (e.g., DiskChunkManager) live in infrastructure/storage/chunk/.
type ChunkManager interface {
	// Allocate reserves space in an .ao file and returns the ChunkPosition.
	Allocate(size int, pageType uint8) (model.ChunkPosition, error)

	// WritePage writes serialized page data at the given position.
	WritePage(pos model.ChunkPosition, data []byte) error

	// ReadPage reads serialized page data from the given position (lazy load).
	ReadPage(pos model.ChunkPosition) ([]byte, error)

	// FreePage marks a page position as reusable.
	FreePage(pos model.ChunkPosition) error

	// Sync flushes all buffered writes to disk.
	Sync() error

	// Stats returns runtime statistics for the ChunkManager.
	Stats() ChunkManagerStats

	// Close closes all chunk files.
	Close() error
}

// ChunkManagerStats provides runtime statistics for the ChunkManager.
type ChunkManagerStats struct {
	TotalChunks  int   // total number of chunk files
	ActiveChunks int   // number of active chunk files
	TotalPages   int64 // total allocated pages
	FreePages    int64 // free page positions
	UsedBytes    int64 // bytes used
	FreeBytes    int64 // bytes free
	ReadOps      int64 // read operation count
	WriteOps     int64 // write operation count
}
