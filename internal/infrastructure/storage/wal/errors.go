// Package wal 定义 WAL 相关错误
package wal

import "errors"

// WAL 错误定义
var (
	// ErrWALClosed WAL 已关闭
	ErrWALClosed = errors.New("wal: closed")

	// ErrWALCorrupted WAL 文件损坏
	ErrWALCorrupted = errors.New("wal: corrupted")

	// ErrWALEntryCorrupted WAL 日志条目损坏
	ErrWALEntryCorrupted = errors.New("wal: entry corrupted")

	// ErrWALChecksumMismatch CRC32 校验和不匹配
	ErrWALChecksumMismatch = errors.New("wal: checksum mismatch")

	// ErrWALLSNGap LSN 间隙检测
	ErrWALLSNGap = errors.New("wal: lsn gap detected")

	// ErrInvalidWALConfig 无效的 WAL 配置
	ErrInvalidWALConfig = errors.New("wal: invalid config")

	// ErrWALSegmentFull WAL 分段已满
	ErrWALSegmentFull = errors.New("wal: segment full")
)

// IsWALClosed 判断是否为 WAL 已关闭错误
func IsWALClosed(err error) bool {
	return errors.Is(err, ErrWALClosed)
}

// IsWALCorrupted 判断是否为 WAL 损坏错误
func IsWALCorrupted(err error) bool {
	return errors.Is(err, ErrWALCorrupted) ||
		errors.Is(err, ErrWALEntryCorrupted) ||
		errors.Is(err, ErrWALChecksumMismatch)
}
