// Package wal 定义 WAL 相关错误
package wal

import (
	"errors"

	errpkg "github.com/jzhang405/NexKV/pkg/errors"
)

var (
	ErrWALClosed                = errpkg.ErrWALClosed
	ErrWALCorrupted             = errpkg.ErrWALCorrupted
	ErrWALEntryCorrupted        = errpkg.ErrWALEntryCorrupted
	ErrWALChecksumMismatch      = errpkg.ErrWALChecksumMismatch
	ErrWALLSNGap                = errpkg.ErrWALLSNGap
	ErrInvalidWALConfig         = errpkg.ErrWALInvalidConfig
	ErrWALSegmentFull           = errpkg.ErrWALSegmentFull
	ErrWALCorruptedTruncatedEntry = errpkg.ErrWALTruncatedEntry
)

func IsWALClosed(err error) bool {
	return errors.Is(err, ErrWALClosed)
}

func IsWALCorrupted(err error) bool {
	return errors.Is(err, ErrWALCorrupted) ||
		errors.Is(err, ErrWALEntryCorrupted) ||
		errors.Is(err, ErrWALChecksumMismatch)
}
