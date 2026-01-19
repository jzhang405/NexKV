// Package store WAL (Write-Ahead Log) 实现
// 支持元数据 WAL 持久化
package store

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/clock"
	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// WAL 文件格式:
// 每个条目都是独立的: [Header 24 bytes][Entry Data N bytes]...
//
// Header 格式 (固定 24 字节，两段式):
// - Magic:        4 bytes  (魔术字 "NxWL")                        [0:4]
// - Type:         2 bytes  (WALType, uint16)                      [4:6]
// - KeyLen:       4 bytes  (key 长度)                             [6:10]
// - ValueLen:     4 bytes  (value 长度)                           [10:14]
// - OldValueLen:  4 bytes  (old value 长度)                       [14:18]
// - TimestampLen: 2 bytes  (HLC 长度，固定 10)                     [18:20]
// - CRC:          4 bytes  (CRC32 校验和)                          [20:24]
//
// Entry Data 格式 (变长，紧接 Header):
// - Key:          KeyLen  bytes
// - Value:        ValueLen bytes
// - OldValue:     OldValueLen bytes
// - Timestamp:    TimestampLen bytes (HLC 序列化: 8字节 pt + 2字节 c)
//
// 总 Header: 4 + 2 + 4 + 4 + 4 + 2 + 4 = 24 bytes (4 字节对齐)

const (
	walHeaderSize = 24
	walMagic      = "NxWL"
)

// MetadataWAL 元数据 WAL 实现
//
// 特性:
//   - 追加写入：只追加，不修改
//   - 持久化保证：fsync 确保数据落盘
//   - 崩溃恢复：Recover 重放所有日志
//   - 精简：Truncate 删除旧日志
type MetadataWAL struct {
	file    *os.File
	path    string
	mu      sync.Mutex
	offset  int64  // 当前写入位置
	entries uint64 // 条目计数
	closed  bool
}

// NewMetadataWAL 创建元数据 WAL
func NewMetadataWAL(path string) (*MetadataWAL, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, types.NewInternalError("创建 WAL 目录失败", err)
	}

	// 以追加模式打开文件
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return nil, types.NewInternalError("打开 WAL 文件失败", err)
	}

	// 获取当前文件大小
	stat, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, types.NewInternalError("获取 WAL 文件信息失败", err)
	}

	wal := &MetadataWAL{
		file:    file,
		path:    path,
		offset:  stat.Size(),
		entries: 0,
	}

	return wal, nil
}

// Append 追加日志条目
func (w *MetadataWAL) Append(entry *WALEntry) error {
	if w.closed {
		return types.NewClosedError("WAL")
	}

	if entry == nil {
		return types.NewStoreInvalidParameterError("entry 不能为空")
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// 序列化 Entry
	data, err := w.encodeEntry(entry)
	if err != nil {
		return types.NewInternalError("编码 WAL 条目失败", err)
	}

	// 计算校验和
	checksum := crc32.ChecksumIEEE(data)

	// 写入：[Data][Checksum]
	if _, err := w.file.Write(data); err != nil {
		return types.NewInternalError("写入 WAL 数据失败", err)
	}

	if err := binary.Write(w.file, binary.BigEndian, checksum); err != nil {
		return types.NewInternalError("写入校验和失败", err)
	}

	// 强制刷盘
	if err := w.file.Sync(); err != nil {
		return types.NewInternalError("WAL sync 失败", err)
	}

	w.offset += int64(len(data) + 4)
	w.entries++

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
		return nil, types.NewInternalError("关闭 WAL 文件失败", err)
	}

	file, err := os.Open(w.path)
	if err != nil {
		return nil, types.NewInternalError("打开 WAL 文件失败", err)
	}
	w.file = file

	var entries []*WALEntry
	reader := bufio.NewReader(file)

	for {
		// 读取 Header (24 bytes)
		header := make([]byte, walHeaderSize)
		if _, err := io.ReadFull(reader, header); err != nil {
			if err == io.EOF {
				break
			}
			return nil, types.NewInternalError("读取 WAL header 失败", err)
		}

		// 验证 Magic
		magic := string(header[0:4])
		if magic != walMagic {
			logging.Warnf("WAL 条目魔术字不匹配: 期望 %s, 实际 %s", walMagic, magic)
			// 尝试恢复：查找下一个魔术字位置
			continue
		}

		// 解析 Header
		typ := WALType(binary.BigEndian.Uint16(header[4:6]))
		keyLen := binary.BigEndian.Uint32(header[6:10])
		valueLen := binary.BigEndian.Uint32(header[10:14])
		oldValueLen := binary.BigEndian.Uint32(header[14:18])
		timestampSize := binary.BigEndian.Uint16(header[18:20])
		headerCRC := binary.BigEndian.Uint32(header[20:24])

		// 读取 Data (新格式: key + value + oldvalue + timestamp)
		dataSize := uint32(keyLen) + uint32(valueLen) + oldValueLen + uint32(timestampSize)
		data := make([]byte, dataSize)
		if _, err := io.ReadFull(reader, data); err != nil {
			return nil, types.NewInternalError("读取 WAL data 失败", err)
		}

		// 验证 CRC (覆盖 Header + Data)
		computedCRC := crc32.ChecksumIEEE(append(header, data...))
		if computedCRC != headerCRC {
			logging.Warnf("WAL 条目校验和不匹配，跳过")
			continue
		}

		// 解析 Entry
		entry, err := w.decodeEntry(typ, keyLen, valueLen, oldValueLen, timestampSize, data)
		if err != nil {
			logging.Warnf("解码 WAL 条目失败: %v", err)
			continue
		}

		entries = append(entries, entry)
	}

	// 恢复完成后，重新以追加模式打开文件用于后续写入
	if err := w.file.Close(); err != nil {
		return nil, types.NewInternalError("关闭 WAL 文件失败", err)
	}

	file, err = os.OpenFile(w.path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return nil, types.NewInternalError("重新打开 WAL 文件失败", err)
	}
	w.file = file
	w.offset = currentOffset

	return entries, nil
}

// Truncate 截断 WAL
func (w *MetadataWAL) Truncate(offset int64) error {
	if w.closed {
		return types.NewClosedError("WAL")
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.file.Close(); err != nil {
		return types.NewInternalError("关闭 WAL 文件失败", err)
	}

	// 重新创建文件，只保留 offset 之后的内容
	if err := os.Truncate(w.path, offset); err != nil {
		return types.NewInternalError("截断 WAL 文件失败", err)
	}

	// 重新打开
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return types.NewInternalError("重新打开 WAL 文件失败", err)
	}

	w.file = file
	w.offset = offset

	return nil
}

// Sync 强制刷盘
func (w *MetadataWAL) Sync() error {
	if w.closed {
		return types.NewClosedError("WAL")
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
		return types.NewInternalError("关闭 WAL 文件失败", err)
	}

	return nil
}

// encodeEntry 编码 WAL 条目
func (w *MetadataWAL) encodeEntry(entry *WALEntry) ([]byte, error) {
	// 序列化 HLC 时间戳（如果为 nil，使用零值 HLC）
	var timestampData []byte
	if entry.Timestamp != nil {
		var err error
		timestampData, err = entry.Timestamp.MarshalBinary()
		if err != nil {
			return nil, err
		}
	} else {
		// 使用零值 HLC
		hlc := clock.NewHLC()
		var err error
		timestampData, err = hlc.MarshalBinary()
		if err != nil {
			return nil, err
		}
	}

	// 获取各字段长度
	keyLen := uint32(len(entry.Key))
	valueLen := uint32(len(entry.Value))
	oldValueLen := uint32(len(entry.OldValue))
	timestampLen := uint16(len(timestampData))

	// 构建 Data (新格式: key + value + oldvalue + timestamp)
	data := make([]byte, 0, walHeaderSize+keyLen+valueLen+oldValueLen+int(timestampLen))
	data = append(data, []byte(entry.Key)...)
	data = append(data, entry.Value...)
	data = append(data, entry.OldValue...)
	data = append(data, timestampData...)

	// 构建 Header (24 字节): Magic(4) + Type(2) + KeyLen(4) + ValueLen(4) + OldValueLen(4) + TimestampLen(2) + CRC(4)
	header := make([]byte, walHeaderSize)
	copy(header[0:4], []byte(walMagic))                               // Magic: "NxWL"
	binary.BigEndian.PutUint16(header[4:6], uint16(entry.Type))       // Type
	binary.BigEndian.PutUint32(header[6:10], keyLen)                  // KeyLen
	binary.BigEndian.PutUint32(header[10:14], valueLen)               // ValueLen
	binary.BigEndian.PutUint32(header[14:18], oldValueLen)            // OldValueLen
	binary.BigEndian.PutUint16(header[18:20], timestampLen)           // TimestampLen

	// 计算 CRC (覆盖 Header + Data)
	crc := crc32.ChecksumIEEE(append(header, data...))
	binary.BigEndian.PutUint32(header[20:24], crc)                    // CRC

	// 最终数据: Header + Data
	result := make([]byte, 0, len(header)+len(data))
	result = append(result, header...)
	result = append(result, data...)

	return result, nil
}

// decodeEntry 解码 WAL 条目
func (w *MetadataWAL) decodeEntry(typ WALType, keyLen, valueLen, oldValueLen uint32, timestampSize uint16, data []byte) (*WALEntry, error) {
	entry := &WALEntry{
		Type: typ,
	}

	offset := 0

	// 解析 Key (新格式: key 在最前面)
	entry.Key = string(data[offset : offset+int(keyLen)])
	offset += int(keyLen)

	// 解析 Value
	if valueLen > 0 {
		entry.Value = make([]byte, valueLen)
		copy(entry.Value, data[offset:offset+int(valueLen)])
	}
	offset += int(valueLen)

	// 解析 OldValue
	if oldValueLen > 0 {
		entry.OldValue = make([]byte, oldValueLen)
		copy(entry.OldValue, data[offset:offset+int(oldValueLen)])
	}
	offset += int(oldValueLen)

	// 解析 Timestamp (新格式: timestamp 在最后)
	timestampData := data[offset : offset+int(timestampSize)]
	entry.Timestamp = &clock.HLC{}
	if err := entry.Timestamp.UnmarshalBinary(timestampData); err != nil {
		return nil, err
	}

	return entry, nil
}

// snapshotManagerImpl 快照管理器实现
type snapshotManagerImpl struct {
	dataDir   string
	retention int // 保留快照数量
}

const (
	snapshotMagic     = "NxSN" // NexKV Snapshot
	snapshotHeaderSize = 16     // Magic(4) + Version(2) + Codec(2) + Length(4) + CRC(4)
)

// NewSnapshotManager 创建快照管理器
func NewSnapshotManager(dataDir string) (SnapshotManager, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, types.NewInternalError("创建快照目录失败", err)
	}

	return &snapshotManagerImpl{
		dataDir:   dataDir,
		retention: 5, // 保留最近 5 个快照
	}, nil
}

// Create 创建快照
func (s *snapshotManagerImpl) Create(store MVStore) error {
	// 从 store 获取快照数据（Protobuf 编码）
	snapshotData, err := store.CreateSnapshot()
	if err != nil {
		return types.NewInternalError("获取快照数据失败", err)
	}

	snapshotName := fmt.Sprintf("metadata-store-checkpoint-%d.snap", time.Now().Unix())
	snapshotPath := filepath.Join(s.dataDir, snapshotName)

	// 构建文件头（16 字节）
	header := make([]byte, snapshotHeaderSize)
	copy(header[0:4], []byte(snapshotMagic))            // Magic: "NxSN"
	binary.BigEndian.PutUint16(header[4:6], 1)           // Version: 1
	binary.BigEndian.PutUint16(header[6:8], uint16(types.CodecTypeProtobuf)) // Codec: Protobuf(3)
	binary.BigEndian.PutUint32(header[8:12], uint32(len(snapshotData)))     // Length

	// 计算 CRC (覆盖 Header + Data)
	crc := crc32.ChecksumIEEE(append(header, snapshotData...))
	binary.BigEndian.PutUint32(header[12:16], crc)       // CRC

	// 写入文件：Header + Data
	fullData := append(header, snapshotData...)
	if err := os.WriteFile(snapshotPath, fullData, 0644); err != nil {
		return types.NewInternalError("写入快照失败", err)
	}

	// 清理旧快照
	s.cleanupOldSnapshots()

	return nil
}

// List 列出所有快照
func (s *snapshotManagerImpl) List() ([]string, error) {
	entries, err := os.ReadDir(s.dataDir)
	if err != nil {
		return nil, types.NewInternalError("读取快照目录失败", err)
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

	// 读取整个文件
	fullData, err := os.ReadFile(snapshotPath)
	if err != nil {
		return nil, types.NewInternalError("读取快照失败", err)
	}

	// 检查文件大小
	if len(fullData) < snapshotHeaderSize {
		return nil, types.NewInternalError("快照文件太小", nil)
	}

	// 解析文件头
	header := fullData[0:snapshotHeaderSize]
	magic := string(header[0:4])
	if magic != snapshotMagic {
		return nil, types.NewInternalError(fmt.Sprintf("快照魔术字不匹配: 期望 %s, 实际 %s", snapshotMagic, magic), nil)
	}

	version := binary.BigEndian.Uint16(header[4:6])
	if version != 1 {
		return nil, types.NewInternalError(fmt.Sprintf("不支持的快照版本: %d", version), nil)
	}

	codec := types.CodecType(binary.BigEndian.Uint16(header[6:8]))
	length := binary.BigEndian.Uint32(header[8:12])
	headerCRC := binary.BigEndian.Uint32(header[12:16])

	// 提取数据区
	data := fullData[snapshotHeaderSize:]
	if uint32(len(data)) != length {
		return nil, types.NewInternalError(fmt.Sprintf("快照数据长度不匹配: 期望 %d, 实际 %d", length, len(data)), nil)
	}

	// 验证 CRC
	computedCRC := crc32.ChecksumIEEE(fullData)
	if computedCRC != headerCRC {
		return nil, types.NewInternalError("快照 CRC 校验失败", nil)
	}

	// 返回数据区（Protobuf 编码）
	return data, nil
}

// Delete 删除快照
func (s *snapshotManagerImpl) Delete(snapshotName string) error {
	snapshotPath := filepath.Join(s.dataDir, snapshotName)

	if err := os.Remove(snapshotPath); err != nil {
		return types.NewInternalError("删除快照失败", err)
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
	// 检查是否符合 "metadata-store-checkpoint-xxx.snap" 格式
	const prefix = "metadata-store-checkpoint-"
	const suffix = ".snap"
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	if !strings.HasSuffix(name, suffix) {
		return false
	}
	// 检查中间是否为数字时间戳
	timestampStr := strings.TrimPrefix(name, prefix)
	timestampStr = strings.TrimSuffix(timestampStr, suffix)
	_, err := strconv.ParseInt(timestampStr, 10, 64)
	return err == nil
}

// sortSnapshots 按时间排序快照（旧到新）
func sortSnapshots(snapshots []string) []string {
	// 简单实现：基于文件名排序
	// 实际应该解析时间戳并排序
	result := make([]string, len(snapshots))
	copy(result, snapshots)

	// 使用文件名排序（metadata-store-checkpoint-xxx.snap）
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
		return types.NewInternalError("写入 checkpoint 失败", err)
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
		return nil, "", types.NewInternalError("列出快照失败", err)
	}

	if len(snapshots) == 0 {
		return nil, "", nil
	}

	// 获取最新快照
	latest := snapshots[len(snapshots)-1]
	data, err := c.snapMgr.Restore(latest)
	if err != nil {
		return nil, "", types.NewInternalError("恢复快照失败", err)
	}

	return data, latest, nil
}

// WALStats WAL 统计信息
type WALStats struct {
	Size             int64
	Entries          uint64
	Offset           int64
	CheckpointOffset int64
}

// GetStats 获取 WAL 统计信息
func (w *MetadataWAL) GetStats() (*WALStats, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	stat, err := w.file.Stat()
	if err != nil {
		return nil, types.NewInternalError("获取 WAL 文件信息失败", err)
	}

	return &WALStats{
		Size:    stat.Size(),
		Entries: w.entries,
		Offset:  w.offset,
	}, nil
}
