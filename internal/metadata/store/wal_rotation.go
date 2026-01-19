// Package store WAL Rotation 实现
//
// 提供 WAL 文件自动切换能力
// 当文件大小超过阈值时自动创建新文件
package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// ========================================
// WAL Rotation 配置
// ========================================

const (
	// DefaultMaxWALSize 默认 WAL 文件大小限制
	// 64MB 限制，平衡恢复时间和写入性能
	DefaultMaxWALSize = 64 * 1024 * 1024

	// MinWALSize 最小 WAL 文件大小（防止频繁切换）
	MinWALSize = 1 * 1024 * 1024 // 1MB

	// MaxWALFiles 最大保留的 WAL 文件数量
	// 超过后自动删除最旧的文件
	MaxWALFiles = 10
)

// ========================================
// WALRotationManager WAL 轮转管理器
// ========================================

// WALRotationManager WAL 轮转管理器
//
// 功能：
//   - 监控 WAL 文件大小
//   - 超过阈值时自动创建新文件
//   - 限制 WAL 文件数量
//   - 提供完整的 WAL 文件列表
type WALRotationManager struct {
	basePath  string       // WAL 文件基础路径（不含序号）
	maxSize   int64        // 单个文件最大大小
	maxFiles  int          // 最大文件数量
	current   *MetadataWAL // 当前活跃的 WAL
	fileIndex int          // 当前文件序号
	mu        sync.Mutex   // 保护并发访问
	closed    bool
}

// NewWALRotationManager 创建 WAL 轮转管理器
//
// basePath: WAL 文件基础路径（如 "./data/wal/metadata.wal"）
// 实际文件名会添加序号后缀（如 metadata.wal.0, metadata.wal.1）
func NewWALRotationManager(basePath string, maxSize int64) (*WALRotationManager, error) {
	if maxSize < MinWALSize {
		maxSize = DefaultMaxWALSize
	}

	// 创建 WAL 目录
	if err := os.MkdirAll(filepath.Dir(basePath), 0755); err != nil {
		return nil, types.NewStoreDirectoryCreationError(filepath.Dir(basePath), err)
	}

	manager := &WALRotationManager{
		basePath:  basePath,
		maxSize:   maxSize,
		maxFiles:  MaxWALFiles,
		fileIndex: 0,
	}

	// 查找最新的 WAL 文件
	if err := manager.findLatestWAL(); err != nil {
		return nil, types.NewStoreWALError("初始化", err)
	}

	// 打开当前 WAL 文件
	currentPath := manager.getCurrentPath()
	wal, err := NewMetadataWAL(currentPath)
	if err != nil {
		return nil, types.NewStoreWALError("打开 WAL", err)
	}
	manager.current = wal

	logging.Infof("WAL Rotation Manager 初始化成功: basePath=%s, maxSize=%d, index=%d",
		basePath, maxSize, manager.fileIndex)

	return manager, nil
}

// Append 追加日志条目（带自动轮转）
func (r *WALRotationManager) Append(entry *WALEntry) error {
	if r.closed {
		return types.NewClosedError("WALRotationManager")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// 检查是否需要轮转
	if err := r.checkRotation(); err != nil {
		return err
	}

	// 追加到当前 WAL
	return r.current.Append(entry)
}

// Recover 从所有 WAL 文件恢复
//
// 按序号顺序读取所有 WAL 文件
// 跳过损坏的文件，尽可能恢复数据
func (r *WALRotationManager) Recover() ([]*WALEntry, error) {
	if r.closed {
		return nil, types.NewClosedError("WALRotationManager")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// 获取所有 WAL 文件
	files, err := r.listWALFiles()
	if err != nil {
		return nil, types.NewStoreWALError("列出 WAL 文件", err)
	}

	var allEntries []*WALEntry

	// 按序号顺序恢复
	for _, file := range files {
		wal, err := NewMetadataWAL(file.path)
		if err != nil {
			logging.Warnf("打开 WAL 文件失败: %s, 跳过", file.path)
			continue
		}

		entries, err := wal.Recover()
		if err != nil {
			logging.Warnf("从 WAL 文件恢复失败: %s, 错误: %v", file.path, err)
			_ = wal.Close()
			continue
		}

		allEntries = append(allEntries, entries...)
		_ = wal.Close()
	}

	logging.Infof("从 %d 个 WAL 文件恢复了 %d 条记录", len(files), len(allEntries))

	return allEntries, nil
}

// Sync 强制刷盘
func (r *WALRotationManager) Sync() error {
	if r.closed {
		return types.NewClosedError("WALRotationManager")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	return r.current.Sync()
}

// Truncate 截断当前 WAL
func (r *WALRotationManager) Truncate(offset int64) error {
	if r.closed {
		return types.NewClosedError("WALRotationManager")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	return r.current.Truncate(offset)
}

// Close 关闭 WAL 轮转管理器
func (r *WALRotationManager) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return nil
	}

	r.closed = true

	if r.current != nil {
		if err := r.current.Close(); err != nil {
			return types.NewStoreWALError("关闭 WAL", err)
		}
	}

	return nil
}

// ========================================
// 内部方法
// ========================================

// checkRotation 检查并执行 WAL 轮转
func (r *WALRotationManager) checkRotation() error {
	stats, err := r.current.GetStats()
	if err != nil {
		return types.NewStoreWALError("获取 WAL 统计", err)
	}

	// 文件大小超过阈值，执行轮转
	if stats.Size >= r.maxSize {
		logging.Infof("WAL 文件大小 %d 字节，超过阈值 %d，执行轮转",
			stats.Size, r.maxSize)

		return r.rotate()
	}

	return nil
}

// rotate 执行 WAL 轮转
func (r *WALRotationManager) rotate() error {
	// 关闭当前 WAL
	if err := r.current.Close(); err != nil {
		return types.NewStoreWALError("关闭当前 WAL", err)
	}

	// 增加序号
	r.fileIndex++

	// 创建新 WAL 文件
	newPath := r.getCurrentPath()
	wal, err := NewMetadataWAL(newPath)
	if err != nil {
		return types.NewStoreWALError("创建新 WAL", err)
	}

	r.current = wal
	logging.Infof("WAL 轮转完成: 新文件 %s", newPath)

	// 清理旧 WAL 文件
	r.cleanupOldWALs()

	return nil
}

// cleanupOldWALs 清理旧的 WAL 文件
func (r *WALRotationManager) cleanupOldWALs() {
	files, err := r.listWALFiles()
	if err != nil {
		logging.Warnf("列出 WAL 文件失败: %v", err)
		return
	}

	// 删除超过最大数量的文件
	if len(files) > r.maxFiles {
		for i := 0; i < len(files)-r.maxFiles; i++ {
			oldFile := files[i]
			if err := os.Remove(oldFile.path); err != nil {
				logging.Warnf("删除旧 WAL 文件失败: %s, 错误: %v", oldFile.path, err)
			} else {
				logging.Infof("删除旧 WAL 文件: %s", oldFile.path)
			}
		}
	}
}

// getCurrentPath 获取当前 WAL 文件路径
func (r *WALRotationManager) getCurrentPath() string {
	return fmt.Sprintf("%s.%d", r.basePath, r.fileIndex)
}

// findLatestWAL 查找最新的 WAL 文件
func (r *WALRotationManager) findLatestWAL() error {
	files, err := r.listWALFiles()
	if err != nil {
		return err
	}

	if len(files) == 0 {
		// 没有现有文件，从序号 0 开始
		r.fileIndex = 0
		return nil
	}

	// 使用最大的序号
	latest := files[len(files)-1]
	r.fileIndex = latest.index

	logging.Infof("找到最新的 WAL 文件: %s (index=%d)", latest.path, latest.index)

	return nil
}

// listWALFiles 列出所有 WAL 文件
func (r *WALRotationManager) listWALFiles() ([]*walFileInfo, error) {
	dir := filepath.Dir(r.basePath)
	baseName := filepath.Base(r.basePath)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, types.NewInternalError("读取 WAL 目录", err)
	}

	var files []*walFileInfo

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasPrefix(name, baseName) {
			continue
		}

		// 解析序号（文件名格式: basename.index）
		suffix := strings.TrimPrefix(name, baseName)
		if suffix == "" {
			// 没有序号后缀，当作序号 0
			files = append(files, &walFileInfo{
				path:  filepath.Join(dir, name),
				index: 0,
			})
			continue
		}

		if suffix[0] != '.' {
			continue
		}

		index, err := strconv.Atoi(suffix[1:])
		if err != nil {
			// 无法解析序号，跳过
			continue
		}

		files = append(files, &walFileInfo{
			path:  filepath.Join(dir, name),
			index: index,
		})
	}

	// 按序号排序
	sort.Slice(files, func(i, j int) bool {
		return files[i].index < files[j].index
	})

	return files, nil
}

// walFileInfo WAL 文件信息
type walFileInfo struct {
	path  string // 完整路径
	index int    // 文件序号
}

// ========================================
// 辅助功能
// ========================================

// GetStats 获取 WAL 轮转统计信息
type WALRotationStats struct {
	CurrentFileIndex int   // 当前文件序号
	CurrentFileSize  int64 // 当前文件大小
	TotalFiles       int   // 总文件数量
	TotalSize        int64 // 总大小
}

// GetRotationStats 获取统计信息
func (r *WALRotationManager) GetRotationStats() (*WALRotationStats, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	stats, err := r.current.GetStats()
	if err != nil {
		return nil, err
	}

	files, err := r.listWALFiles()
	if err != nil {
		return nil, err
	}

	var totalSize int64
	for _, file := range files {
		if info, err := os.Stat(file.path); err == nil {
			totalSize += info.Size()
		}
	}

	return &WALRotationStats{
		CurrentFileIndex: r.fileIndex,
		CurrentFileSize:  stats.Size,
		TotalFiles:       len(files),
		TotalSize:        totalSize,
	}, nil
}

// ListAllWALFiles 列出所有 WAL 文件路径
func (r *WALRotationManager) ListAllWALFiles() ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	files, err := r.listWALFiles()
	if err != nil {
		return nil, err
	}

	paths := make([]string, len(files))
	for i, file := range files {
		paths[i] = file.path
	}

	return paths, nil
}
