// Package wal provides Write-Ahead Logging (WAL) for crash recovery.
//
// The WAL interface is defined in domain/service/wal.go. This package provides
// the DiskWAL implementation and supporting types.
//
// Sync mode: call Append/Sync/Truncate directly.
// Async mode: use WALAppendItem via TaskScheduler for Group Commit batching.
package wal

// WALStats holds WAL statistics.
type WALStats struct {
	CurrentLSN   LSN
	TotalEntries int64
	TotalBytes   int64
	SegmentCount int
	SyncCount    int64
}
