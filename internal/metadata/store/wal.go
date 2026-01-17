// Package store WAL (Write-Ahead Log) 实现
// 支持元数据 WAL 和业务 WAL 双层架构
package store

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/clock"
	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
)

// WAL 文件格式:
// [Header 12 bytes][Entry N bytes][Checksum 4 bytes]...
//
// Header 格式:
// - Type: 1 byte (WALType)
// - KeyLen: 4 bytes (key 长度)
// - ValueLen: 4 bytes (value 长度)
// - TimestampSize: 2 bytes (HLC 序列化后大小)
//
// 总 Header: 1 + 4 + 4 + 2 = 11 bytes，加上对齐到 4 字节 = 12 bytes

const (
	walHeaderSize = 12
	walMagic      = "NxKVWAL"
)

// MetadataWAL 元数据 WAL 实现
//
// 特性:
//   - 追加写入：只追加，不修改
//   - 持久化保证：fsync 确保数据落盘
//   - 崩溃恢复：Recover 重放所有日志
//   - 精简：Truncate 删除旧日志
type MetadataWAL struct {
	file   *os.File
	path   string
	mu     sync.Mutex
	offset int64 // 当前写入位置
	closed bool
}

// NewMetadataWAL 创建元数据 WAL
func NewMetadataWAL(path string) (*MetadataWAL, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, &StoreError{Code: ErrCodeInternal, Message: "创建 WAL 目录失败", Err: err}
	}

	// 以追加模式打开文件
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return nil, &StoreError{Code: ErrCodeInternal, Message: "打开 WAL 文件失败", Err: err}
	}

	// 获取当前文件大小
	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, &StoreError{Code: ErrCodeInternal, Message: "获取 WAL 文件信息失败", Err: err}
	}

	wal := &MetadataWAL{
		file:   file,
		path:   path,
		offset: stat.Size(),
	}

	return wal, nil
}

// Append 追加日志条目
func (w *MetadataWAL) Append(entry *WALEntry) error {
	if w.closed {
		return ErrClosed
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// 序列化 Entry
	data, err := w.encodeEntry(entry)
	if err != nil {
		return &StoreError{Code: ErrCodeInternal, Message: "编码 WAL 条目失败", Err: err}
	}

	// 计算校验和
	checksum := crc32.ChecksumIEEE(data)

	// 写入：[Data][Checksum]
	if _, err := w.file.Write(data); err != nil {
		return &StoreError{Code: ErrCodeInternal, Message: "写入 WAL 数据失败", Err: err}
	}

	if err := binary.Write(w.file, binary.BigEndian, checksum); err != nil {
		return &StoreError{Code: ErrCodeInternal, Message: "写入校验和失败", Err: err}
	}

	// 强制刷盘
	if err := w.file.Sync(); err != nil {
		return &StoreError{Code: ErrCodeInternal, Message: "WAL sync 失败", Err: err}
	}

	w.offset += int64(len(data) + 4)

	return nil
}

// Recover 从 WAL 恢复
func (w *MetadataWAL) Recover() ([]*WALEntry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// 保存当前 offset
	currentOffset := w.offset

	// 重新打开文件，从头读取
	if err := w.file.Close(); err != nil {
		return nil, &StoreError{Code: ErrCodeInternal, Message: "关闭 WAL 文件失败", Err: err}
	}

	file, err := os.Open(w.path)
	if err != nil {
		return nil, &StoreError{Code: ErrCodeInternal, Message: "打开 WAL 文件失败", Err: err}
	}
	w.file = file

	var entries []*WALEntry
	reader := bufio.NewReader(file)

	for {
		// 读取 Header
		header := make([]byte, walHeaderSize)
		if _, err := io.ReadFull(reader, header); err != nil {
			if err == io.EOF {
				break
			}
			return nil, &StoreError{Code: ErrCodeInternal, Message: "读取 WAL header 失败", Err: err}
		}

		// 解析 Header
		typ := WALType(header[0])
		keyLen := binary.BigEndian.Uint32(header[1:5])
		valueLen := binary.BigEndian.Uint32(header[5:9])
		timestampSize := binary.BigEndian.Uint16(header[9:11])

		// 读取 Data (修复类型转换)
		dataSize := uint32(keyLen) + uint32(valueLen) + uint32(timestampSize)
		data := make([]byte, dataSize)
		if _, err := io.ReadFull(reader, data); err != nil {
			return nil, &StoreError{Code: ErrCodeInternal, Message: "读取 WAL data 失败", Err: err}
		}

		// 读取 Checksum
		var checksum uint32
		if err := binary.Read(reader, binary.BigEndian, &checksum); err != nil {
			return nil, &StoreError{Code: ErrCodeInternal, Message: "读取校验和失败", Err: err}
		}

		// 验证校验和
		computedChecksum := crc32.ChecksumIEEE(append(header, data...))
		if computedChecksum != checksum {
			logging.Warnf("WAL 条目校验和不匹配，跳过")
			continue
		}

		// 解析 Entry
		entry, err := w.decodeEntry(typ, keyLen, valueLen, timestampSize, data)
		if err != nil {
			logging.Warnf("解码 WAL 条目失败: %v", err)
			continue
		}

		entries = append(entries, entry)
	}

	// 恢复完成后，重新以追加模式打开文件用于后续写入
	if err := w.file.Close(); err != nil {
		return nil, &StoreError{Code: ErrCodeInternal, Message: "关闭 WAL 文件失败", Err: err}
	}

	file, err = os.OpenFile(w.path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return nil, &StoreError{Code: ErrCodeInternal, Message: "重新打开 WAL 文件失败", Err: err}
	}
	w.file = file
	w.offset = currentOffset

	return entries, nil
}

// Truncate 截断 WAL
func (w *MetadataWAL) Truncate(offset int64) error {
	if w.closed {
		return ErrClosed
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.file.Close(); err != nil {
		return &StoreError{Code: ErrCodeInternal, Message: "关闭 WAL 文件失败", Err: err}
	}

	// 重新创建文件，只保留 offset 之后的内容
	if err := os.Truncate(w.path, offset); err != nil {
		return &StoreError{Code: ErrCodeInternal, Message: "截断 WAL 文件失败", Err: err}
	}

	// 重新打开
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return &StoreError{Code: ErrCodeInternal, Message: "重新打开 WAL 文件失败", Err: err}
	}

	w.file = file
	w.offset = offset

	return nil
}

// Sync 强制刷盘
func (w *MetadataWAL) Sync() error {
	if w.closed {
		return ErrClosed
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	return w.file.Sync()
}

// Close 关闭 WAL
func (w *MetadataWAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil
	}

	w.closed = true

	if err := w.file.Close(); err != nil {
		return &StoreError{Code: ErrCodeInternal, Message: "关闭 WAL 文件失败", Err: err}
	}

	return nil
}

// encodeEntry 编码 WAL 条目
func (w *MetadataWAL) encodeEntry(entry *WALEntry) ([]byte, error) {
	// 序列化 HLC 时间戳
	timestampData, err := entry.Timestamp.MarshalBinary()
	if err != nil {
		return nil, err
	}

	// 构建 Header
	header := make([]byte, walHeaderSize)
	header[0] = byte(entry.Type)
	binary.BigEndian.PutUint32(header[1:5], uint32(len(entry.Key)))
	binary.BigEndian.PutUint32(header[5:9], uint32(len(entry.Value)))
	binary.BigEndian.PutUint16(header[9:11], uint16(len(timestampData)))

	// 构建 Data
	data := make([]byte, 0, walHeaderSize+len(timestampData)+len(entry.Key)+len(entry.Value))
	data = append(data, header...)
	data = append(data, timestampData...)
	data = append(data, []byte(entry.Key)...)
	data = append(data, entry.Value...)

	return data, nil
}

// decodeEntry 解码 WAL 条目
func (w *MetadataWAL) decodeEntry(typ WALType, keyLen, valueLen uint32, timestampSize uint16, data []byte) (*WALEntry, error) {
	entry := &WALEntry{
		Type: typ,
	}

	offset := 0

	// 解析时间戳
	timestampData := data[offset : offset+int(timestampSize)]
	entry.Timestamp = &clock.HLC{}
	if err := entry.Timestamp.UnmarshalBinary(timestampData); err != nil {
		return nil, err
	}
	offset += int(timestampSize)

	// 解析 Key
	entry.Key = string(data[offset : offset+int(keyLen)])
	offset += int(keyLen)

	// 解析 Value
	if valueLen > 0 {
		entry.Value = make([]byte, valueLen)
		copy(entry.Value, data[offset:offset+int(valueLen)])
	}

	return entry, nil
}

// BusinessWAL 业务 WAL 实现
//
// 与 MetadataWAL 的区别：
//   - 更长的保留时间（3个月 vs 30天）
//   - 可能更大的数据量
//   - 支持批量写入优化
type BusinessWAL struct {
	*MetadataWAL
	batchSize    int
	batchEntries []*WALEntry
}

// NewBusinessWAL 创建业务 WAL
func NewBusinessWAL(path string) (*BusinessWAL, error) {
	metadataWAL, err := NewMetadataWAL(path)
	if err != nil {
		return nil, err
	}

	return &BusinessWAL{
		MetadataWAL:  metadataWAL,
		batchSize:    100,
		batchEntries: make([]*WALEntry, 0, 100),
	}, nil
}

// AppendBatch 批量追加
func (w *BusinessWAL) AppendBatch(entries []*WALEntry) error {
	for _, entry := range entries {
		if err := w.Append(entry); err != nil {
			return err
		}
	}
	return nil
}

// snapshotManagerImpl 快照管理器实现
type snapshotManagerImpl struct {
	dataDir   string
	retention int // 保留快照数量
}

// NewSnapshotManager 创建快照管理器
func NewSnapshotManager(dataDir string) (SnapshotManager, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, &StoreError{Code: ErrCodeInternal, Message: "创建快照目录失败", Err: err}
	}

	return &snapshotManagerImpl{
		dataDir:   dataDir,
		retention: 5, // 保留最近 5 个快照
	}, nil
}

// Create 创建快照
func (s *snapshotManagerImpl) Create(store MVStore) error {
	// 从 store 获取快照数据
	snapshot, err := store.CreateSnapshot()
	if err != nil {
		return &StoreError{Code: ErrCodeInternal, Message: "获取快照数据失败", Err: err}
	}

	snapshotName := fmt.Sprintf("snapshot-%d.json", time.Now().Unix())
	snapshotPath := filepath.Join(s.dataDir, snapshotName)

	if err := os.WriteFile(snapshotPath, snapshot, 0644); err != nil {
		return &StoreError{Code: ErrCodeInternal, Message: "写入快照失败", Err: err}
	}

	// 清理旧快照
	s.cleanupOldSnapshots()

	return nil
}

// List 列出所有快照
func (s *snapshotManagerImpl) List() ([]string, error) {
	entries, err := os.ReadDir(s.dataDir)
	if err != nil {
		return nil, &StoreError{Code: ErrCodeInternal, Message: "读取快照目录失败", Err: err}
	}

	var snapshots []string
	for _, entry := range entries {
		if !entry.IsDir() && isSnapshotFile(entry.Name()) {
			snapshots = append(snapshots, entry.Name())
		}
	}

	return snapshots, nil
}

// Restore 从快照恢复
func (s *snapshotManagerImpl) Restore(snapshotName string) ([]byte, error) {
	snapshotPath := filepath.Join(s.dataDir, snapshotName)

	data, err := os.ReadFile(snapshotPath)
	if err != nil {
		return nil, &StoreError{Code: ErrCodeInternal, Message: "读取快照失败", Err: err}
	}

	return data, nil
}

// Delete 删除快照
func (s *snapshotManagerImpl) Delete(snapshotName string) error {
	snapshotPath := filepath.Join(s.dataDir, snapshotName)

	if err := os.Remove(snapshotPath); err != nil {
		return &StoreError{Code: ErrCodeInternal, Message: "删除快照失败", Err: err}
	}

	return nil
}

// Close 关闭快照管理器
func (s *snapshotManagerImpl) Close() error {
	return nil
}

// cleanupOldSnapshots 清理旧快照
func (s *snapshotManagerImpl) cleanupOldSnapshots() {
	snapshots, err := s.List()
	if err != nil {
		logging.Warnf("列出快照失败: %v", err)
		return
	}

	// 按时间排序（旧到新）
	snapshots = sortSnapshots(snapshots)

	// 删除超过保留数量的快照
	for i := 0; i < len(snapshots)-s.retention; i++ {
		if err := s.Delete(snapshots[i]); err != nil {
			logging.Warnf("删除快照 %s 失败: %v", snapshots[i], err)
		}
	}
}

// isSnapshotFile 检查是否是快照文件
func isSnapshotFile(name string) bool {
	return len(name) > 12 && name[:9] == "snapshot-"
}

// sortSnapshots 按时间排序快照（旧到新）
func sortSnapshots(snapshots []string) []string {
	// 简单实现：基于文件名排序
	// 实际应该解析时间戳并排序
	result := make([]string, len(snapshots))
	copy(result, snapshots)

	// 使用文件名排序（snapshot-xxx.json）
	// 这里简化处理，实际应该解析时间戳
	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			if result[i] > result[j] {
				result[i], result[j] = result[j], result[i]
			}
		}
	}

	return result
}

// WALCheckpoint WAL 检查点
//
// 定期创建检查点，可以：
//   - 截断 WAL，删除已应用的日志
//   - 创建快照，加速恢复
type WALCheckpoint struct {
	wal       WAL
	snapMgr   SnapshotManager
	offset    int64
	lastCheck int64
}

// NewWALCheckpoint 创建检查点管理器
func NewWALCheckpoint(wal WAL, snapMgr SnapshotManager) *WALCheckpoint {
	return &WALCheckpoint{
		wal:     wal,
		snapMgr: snapMgr,
	}
}

// CreateCheckpoint 创建检查点
func (c *WALCheckpoint) CreateCheckpoint() error {
	// 写入 checkpoint 条目
	checkpointEntry := &WALEntry{
		Type: WALTypeCheckpoint,
	}

	if err := c.wal.Append(checkpointEntry); err != nil {
		return &StoreError{Code: ErrCodeInternal, Message: "写入 checkpoint 失败", Err: err}
	}

	c.lastCheck = c.offset

	return nil
}

// Truncate 截断到指定位置
func (c *WALCheckpoint) Truncate(offset int64) error {
	if err := c.wal.Truncate(offset); err != nil {
		return err
	}

	c.offset = offset
	return nil
}

// LoadSnapshot 加载最新快照
func (c *WALCheckpoint) LoadSnapshot() ([]byte, string, error) {
	snapshots, err := c.snapMgr.List()
	if err != nil {
		return nil, "", &StoreError{Code: ErrCodeInternal, Message: "列出快照失败", Err: err}
	}

	if len(snapshots) == 0 {
		return nil, "", nil
	}

	// 获取最新快照
	latest := snapshots[len(snapshots)-1]
	data, err := c.snapMgr.Restore(latest)
	if err != nil {
		return nil, "", &StoreError{Code: ErrCodeInternal, Message: "恢复快照失败", Err: err}
	}

	return data, latest, nil
}

// WALStats WAL 统计信息
type WALStats struct {
	Size             int64
	Entries          int
	Offset           int64
	CheckpointOffset int64
}

// GetStats 获取 WAL 统计信息
func (w *MetadataWAL) GetStats() (*WALStats, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	stat, err := w.file.Stat()
	if err != nil {
		return nil, &StoreError{Code: ErrCodeInternal, Message: "获取 WAL 文件信息失败", Err: err}
	}

	return &WALStats{
		Size:   stat.Size(),
		Offset: w.offset,
	}, nil
}
