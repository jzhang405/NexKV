// Package wal 提供 WAL 的磁盘实现
package wal

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// DiskWAL WAL 的磁盘实现
type DiskWAL struct {
	mu         sync.RWMutex
	config     *WALConfig
	currentLSN atomic.Uint64
	closed     atomic.Bool
	file       *os.File
	filePath   string
	stats      WALStats
	syncCount  atomic.Int64
}

// NewDiskWAL 创建新的磁盘 WAL
func NewDiskWAL(config *WALConfig) (*DiskWAL, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	// 创建 WAL 目录
	if err := os.MkdirAll(config.Dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create wal directory: %w", err)
	}

	// 初始化 WAL
	dwal := &DiskWAL{
		config: config,
	}

	// 打开或创建 WAL 文件
	if err := dwal.openCurrentSegment(); err != nil {
		return nil, err
	}

	return dwal, nil
}

// openCurrentSegment 打开当前分段
func (w *DiskWAL) openCurrentSegment() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// 使用 LSN 作为文件名
	fileName := fmt.Sprintf("%020d.wal", w.currentLSN.Load()+1)
	w.filePath = filepath.Join(w.config.Dir, fileName)

	file, err := os.OpenFile(w.filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open wal file: %w", err)
	}

	w.file = file
	return nil
}

// Append 追加一条日志记录（同步）
func (w *DiskWAL) Append(entry *WALEntry) (LSN, error) {
	if w.closed.Load() {
		return LSNInvalid, ErrWALClosed
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// 预分配 LSN（不提交）
	preAllocatedLSN := w.currentLSN.Load() + 1
	lsn := LSN(preAllocatedLSN)
	entry.LSN = lsn

	// 序列化
	data, err := entry.Marshal()
	if err != nil {
		return LSNInvalid, fmt.Errorf("failed to marshal entry: %w", err)
	}

	// 写入文件
	if _, err := w.file.Write(data); err != nil {
		return LSNInvalid, fmt.Errorf("failed to write entry: %w", err)
	}

	// 更新统计
	w.stats.TotalEntries++
	w.stats.TotalBytes += int64(len(data))

	// 根据同步策略决定是否同步
	if w.config.SyncPolicy == SyncPolicyEveryWrite {
		if err := w.syncLocked(); err != nil {
			return LSNInvalid, err
		}
	}

	// 只有所有操作成功后才提交 LSN
	w.currentLSN.Store(preAllocatedLSN)

	return lsn, nil
}

// Sync 刷盘
func (w *DiskWAL) Sync() error {
	if w.closed.Load() {
		return ErrWALClosed
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	return w.syncLocked()
}

// syncLocked 刷盘（已持有锁）
func (w *DiskWAL) syncLocked() error {
	if err := w.file.Sync(); err != nil {
		return fmt.Errorf("failed to sync wal: %w", err)
	}
	w.syncCount.Add(1)
	return nil
}

// Recover 崩溃恢复
func (w *DiskWAL) Recover() ([]*WALEntry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// 扫描 WAL 目录
	entries, err := w.scanWALDirectory()
	if err != nil {
		return nil, err
	}

	// 确保 entries 不是 nil
	if entries == nil {
		entries = []*WALEntry{}
	}

	// 重放日志，更新当前 LSN
	for _, entry := range entries {
		currentLSN := uint64(entry.LSN)
		maxLSN := w.currentLSN.Load()
		if currentLSN > maxLSN {
			w.currentLSN.Store(currentLSN)
		}
	}

	return entries, nil
}

// scanWALDirectory 扫描 WAL 目录
func (w *DiskWAL) scanWALDirectory() ([]*WALEntry, error) {
	var allEntries []*WALEntry

	files, err := os.ReadDir(w.config.Dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read wal directory: %w", err)
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		// 跳过非 .wal 文件
		if filepath.Ext(file.Name()) != ".wal" {
			continue
		}

		filePath := filepath.Join(w.config.Dir, file.Name())
		entries, err := w.recoverFile(filePath)
		if err != nil {
			if IsWALCorrupted(err) {
				// 跳过损坏的文件
				continue
			}
			return nil, err
		}

		allEntries = append(allEntries, entries...)
	}

	// 确保返回空切片而不是 nil
	if allEntries == nil {
		allEntries = []*WALEntry{}
	}

	return allEntries, nil
}

// Truncate 截断日志
// recoverFile 恢复单个 WAL 文件
func (w *DiskWAL) recoverFile(filePath string) ([]*WALEntry, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open wal file: %w", err)
	}
	defer file.Close()

	var entries []*WALEntry
	buf := make([]byte, 32*1024) // 32KB 缓冲区

	for {
		// 1. 读取条目头部（至少包含长度字段）
		// 格式：[CRC:4][LSN:8][Type:1][TxID:8][Timestamp:8][PrevLSN:8][KeyLen:4][ValueLen:4]
		header := make([]byte, 4+8+1+8+8+8+4+4)
		_, err := io.ReadFull(file, header)
		if err != nil {
			if err == io.EOF {
				break // 文件结束
			}
			if IsWALCorrupted(err) {
				continue // 跳过损坏数据
			}
			return nil, fmt.Errorf("failed to read entry header: %w", err)
		}

		// 2. 解析 KeyLen 和 ValueLen
		keyLen := binary.BigEndian.Uint32(header[37:41])
		valueLen := binary.BigEndian.Uint32(header[41:45])

		// 3. 读取 Key 和 Value
		entrySize := int(keyLen) + int(valueLen)
		if entrySize > cap(buf) {
			buf = make([]byte, entrySize)
		}
		keyAndValue := buf[:entrySize]
		_, err = io.ReadFull(file, keyAndValue)
		if err != nil {
			if IsWALCorrupted(err) {
				continue // 跳过损坏数据
			}
			return nil, fmt.Errorf("failed to read entry data: %w", err)
		}

		// 4. 组装完整条目
		fullEntry := make([]byte, 4+8+1+8+8+8+4+4+entrySize)
		copy(fullEntry, header)
		copy(fullEntry[4+8+1+8+8+8+4+4:], keyAndValue)

		// 5. 反序列化
		entry := &WALEntry{}
		if err := entry.Unmarshal(fullEntry); err != nil {
			if IsWALCorrupted(err) {
				continue // 跳过损坏条目
			}
			return nil, fmt.Errorf("failed to unmarshal entry: %w", err)
		}

		entries = append(entries, entry)
	}

	return entries, nil
}

func (w *DiskWAL) Truncate(lsn LSN) error {
	if w.closed.Load() {
		return ErrWALClosed
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// 1. 检查 LSN 有效性
	currentLSN := w.currentLSN.Load()
	if LSN(lsn) > LSN(currentLSN) {
		return fmt.Errorf("cannot truncate to LSN %d: greater than current LSN %d", lsn, currentLSN)
	}

	// 2. 扫描 WAL 目录，删除旧文件
	files, err := os.ReadDir(w.config.Dir)
	if err != nil {
		return fmt.Errorf("failed to read wal directory: %w", err)
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		// 跳过非 .wal 文件
		if filepath.Ext(file.Name()) != ".wal" {
			continue
		}

		// 从文件名提取 LSN
		fileLSNStr := strings.TrimSuffix(file.Name(), ".wal")
		fileLSN, err := strconv.ParseUint(fileLSNStr, 10, 64)
		if err != nil {
			// 无法解析的文件名，跳过
			continue
		}

		// 删除 LSN 小于截断点的文件
		if fileLSN < uint64(lsn) {
			filePath := filepath.Join(w.config.Dir, file.Name())
			if err := os.Remove(filePath); err != nil {
				// 记录错误但继续处理其他文件
				continue
			}
		}
	}

	return nil
}

// AppendAsync 异步追加日志（v4 模式）
func (w *DiskWAL) AppendAsync(ctx context.Context, entry *WALEntry) model.Task[LSN] {
	// 创建立即完成的任务（MVP 简化实现）
	return NewCompletedWALTask(func() (LSN, error) {
		return w.Append(entry)
	})
}

// TruncateAsync 异步截断日志（v4 模式）
func (w *DiskWAL) TruncateAsync(ctx context.Context, lsn LSN) model.Task[struct{}] {
	// 创建立即完成的任务（MVP 简化实现）
	return NewCompletedTruncateTask(func() (struct{}, error) {
		return struct{}{}, w.Truncate(lsn)
	})
}

// Close 关闭 WAL
func (w *DiskWAL) Close() error {
	if !w.closed.CompareAndSwap(false, true) {
		return ErrWALClosed
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// 最后一次 Sync
	if err := w.syncLocked(); err != nil {
		return err
	}

	// 关闭文件
	if err := w.file.Close(); err != nil {
		return fmt.Errorf("failed to close wal file: %w", err)
	}

	return nil
}

// GetStats 获取统计信息
func (w *DiskWAL) GetStats() WALStats {
	w.mu.RLock()
	defer w.mu.RUnlock()

	stats := w.stats
	stats.CurrentLSN = LSN(w.currentLSN.Load())
	stats.SyncCount = w.syncCount.Load()

	return stats
}
