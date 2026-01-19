// Package store WAL (Write-Ahead Log) 实现
// 支持元数据 WAL 持久化
package store

import (
	"bufio"
	"encoding/binary"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sync"

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

	// WALEOFMagic WAL EOF 魔术字（7 字节）
	// 用于标识 WAL 文件的完整结束位置，支持文件截断和恢复验证
	WALEOFMagic = "NxWLEOF"
	// WALEOFSize EOF 标记大小（7 字节）
	WALEOFSize = 7
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

	// 序列化 Entry（encodeEntry 已经包含 Header 和 CRC）
	data, err := w.encodeEntry(entry)
	if err != nil {
		return types.NewInternalError("编码 WAL 条目失败", err)
	}

	// 写入：[Header(含CRC)][Payload]
	// 注意：encodeEntry 已经在 header 中包含了 CRC，所以不需要额外写入 checksum
	if _, err := w.file.Write(data); err != nil {
		return types.NewInternalError("写入 WAL 数据失败", err)
	}

	// 强制刷盘
	if err := w.file.Sync(); err != nil {
		return types.NewInternalError("WAL sync 失败", err)
	}

	w.offset += int64(len(data))
	w.entries++

	return nil
}

// Recover 从 WAL 恢复
//
// 恢复过程中会自动检测 EOF 标记，如果遇到 EOF 标记则停止恢复。
// EOF 标记之后的任何数据都会被忽略（可能是截断后的残留数据）。
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

	// 首先查找 EOF 标记位置
	eofPos, err := w.getEOFPositionUnlocked()
	if err != nil {
		logging.Warnf("查找 EOF 标记失败: %v，继续恢复", err)
		eofPos = -1
	}

	for {
		// 获取当前读取位置
		currentPos, _ := file.Seek(0, io.SeekCurrent)

		// 如果设置了 EOF 位置且已到达或超过，停止恢复
		if eofPos > 0 && currentPos >= eofPos {
			logging.Infof("恢复到达 EOF 标记位置: %d，停止恢复", eofPos)
			break
		}

		// 读取 Header (24 bytes)
		header := make([]byte, walHeaderSize)
		_, err := io.ReadFull(reader, header)
		if err != nil {
			// 文件末尾或数据不足（如只有 EOF 标记）时，正常退出
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return nil, types.NewInternalError("读取 WAL header 失败", err)
		}

		// 检查是否到达 EOF 标记位置
		if eofPos > 0 && currentPos+int64(len(header)) > eofPos {
			logging.Infof("检测到 EOF 标记位置，停止恢复")
			break
		}

		// 验证 Magic
		magic := string(header[0:4])
		if magic != walMagic {
			// 检查是否是 EOF 标记
			if string(header[0:4]) == WALEOFMagic[:4] {
				logging.Infof("检测到 EOF 标记，停止恢复")
				break
			}
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

		// 检查是否超过 EOF 位置
		if eofPos > 0 {
			nextPos, _ := file.Seek(0, io.SeekCurrent)
			if nextPos > eofPos {
				logging.Infof("数据读取超过 EOF 位置，回退并停止恢复")
				// 回退到读取数据之前
				if _, err := file.Seek(currentPos, io.SeekStart); err != nil {
					logging.Warnf("回退文件位置失败: %v", err)
				}
				break
			}
		}

		// 验证 CRC (覆盖 Header[0:20] + Data)
		// 注意：不包含 header[20:24]（CRC 字段本身）
		computedCRC := crc32.ChecksumIEEE(append(header[:20], data...))
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

	logging.Infof("WAL 恢复完成: 恢复 %d 个条目", len(entries))

	return entries, nil
}

// getEOFPositionUnlocked 获取 EOF 标记位置（不加锁版本，内部使用）
func (w *MetadataWAL) getEOFPositionUnlocked() (int64, error) {
	// 获取当前文件大小
	stat, err := w.file.Stat()
	if err != nil {
		return -1, types.NewInternalError("获取 WAL 文件信息失败", err)
	}

	fileSize := stat.Size()

	// 文件太小，不足以容纳 EOF 标记
	if fileSize < WALEOFSize {
		return -1, nil
	}

	// 从文件末尾向前扫描，查找 EOF 标记
	const scanChunkSize = 4096
	buffer := make([]byte, scanChunkSize)

	scanOffset := fileSize
	for scanOffset > 0 {
		scanSize := scanChunkSize
		if scanOffset < scanChunkSize {
			scanSize = int(scanOffset)
		}
		scanOffset -= int64(scanSize)

		if _, err := w.file.ReadAt(buffer[:scanSize], scanOffset); err != nil {
			return -1, types.NewInternalError("扫描 WAL 文件失败", err)
		}

		eofBytes := []byte(WALEOFMagic)
		for i := scanSize - 1; i >= 0; i-- {
			if i+WALEOFSize <= scanSize {
				match := true
				for j := 0; j < WALEOFSize; j++ {
					if buffer[i+j] != eofBytes[j] {
						match = false
						break
					}
				}
				if match {
					return scanOffset + int64(i), nil
				}
			}
		}
	}

	return -1, nil
}

// Truncate 截断 WAL
//
// 截断 WAL 文件到指定偏移量，并写入新的 EOF 标记。
//
// 注意：截断操作会移除 EOF 标记之后的所有数据，截断完成后会写入新的 EOF 标记。
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

	// 写入新的 EOF 标记
	if _, err := w.file.WriteString(WALEOFMagic); err != nil {
		return types.NewInternalError("写入 EOF 标记失败", err)
	}

	// 强制刷盘
	if err := w.file.Sync(); err != nil {
		return types.NewInternalError("EOF 标记 sync 失败", err)
	}

	w.offset += WALEOFSize

	logging.Infof("WAL 截断完成: offset=%d, EOF offset=%d", offset, w.offset)

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

// WriteEOFMarker 写入 EOF 标记
//
// 在 WAL 文件末尾写入 EOF 魔术字（"NxWLEOF"，8 字节），
// 用于标识文件的完整结束位置。
//
// 应用场景：
//   - Checkpoint 创建完成后写入 EOF 标记
//   - WAL 截断后写入新的 EOF 标记
//   - 文件关闭前写入 EOF 标记
//
// 返回错误信息
func (w *MetadataWAL) WriteEOFMarker() error {
	if w.closed {
		return types.NewClosedError("WAL")
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// 在 O_APPEND 模式下，直接使用 Write() 会自动追加到文件末尾
	// 不需要手动 Seek，因为 O_APPEND 标志会自动处理文件位置
	if _, err := w.file.Write([]byte(WALEOFMagic)); err != nil {
		return types.NewInternalError("写入 EOF 标记失败", err)
	}

	// 强制刷盘
	if err := w.file.Sync(); err != nil {
		return types.NewInternalError("EOF 标记 sync 失败", err)
	}

	// 更新 offset
	w.offset += WALEOFSize

	return nil
}

// ValidateEOF 验证 EOF 标记
//
// 检查 WAL 文件末尾是否有有效的 EOF 标记。
// 如果存在有效标记，说明文件完整；否则文件可能不完整或已损坏。
//
// 返回验证结果和错误信息
func (w *MetadataWAL) ValidateEOF() (bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// 获取当前文件大小
	stat, err := w.file.Stat()
	if err != nil {
		return false, types.NewInternalError("获取 WAL 文件信息失败", err)
	}

	fileSize := stat.Size()

	// 调试：记录文件大小
	logging.Infof("ValidateEOF: fileSize=%d, WALEOFSize=%d, readAt=%d", fileSize, WALEOFSize, fileSize-WALEOFSize)

	// 文件太小，不足以容纳 EOF 标记
	if fileSize < WALEOFSize {
		return false, nil
	}

	// 读取文件末尾的 8 字节
	eofMarker := make([]byte, WALEOFSize)
	if _, err := w.file.ReadAt(eofMarker, fileSize-WALEOFSize); err != nil {
		return false, types.NewInternalError("读取 EOF 标记失败", err)
	}

	// 调试：记录实际读取的内容
	logging.Infof("ValidateEOF: 读取内容 (hex)=%x, (ascii)=%s", eofMarker, string(eofMarker))

	// 验证魔术字
	valid := string(eofMarker) == WALEOFMagic

	if valid {
		logging.Infof("WAL EOF 标记验证成功: offset=%d", fileSize-WALEOFSize)
	} else {
		logging.Warnf("WAL EOF 标记验证失败: 期望 %s, 实际 %s", WALEOFMagic, string(eofMarker))
	}

	return valid, nil
}

// GetEOFPosition 获取 EOF 标记位置
//
// 扫描 WAL 文件，查找 EOF 标记的位置。
// 如果找到 EOF 标记，返回其位置；否则返回 -1。
//
// 返回 EOF 标记位置和错误信息
func (w *MetadataWAL) GetEOFPosition() (int64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// 获取当前文件大小
	stat, err := w.file.Stat()
	if err != nil {
		return -1, types.NewInternalError("获取 WAL 文件信息失败", err)
	}

	fileSize := stat.Size()

	// 文件太小，不足以容纳 EOF 标记
	if fileSize < WALEOFSize {
		return -1, nil
	}

	// 从文件末尾向前扫描，查找 EOF 标记
	// 每次读取 4KB 块，提高大文件扫描效率
	const scanChunkSize = 4096
	buffer := make([]byte, scanChunkSize)

	scanOffset := fileSize
	for scanOffset > 0 {
		// 计算本次扫描大小
		scanSize := scanChunkSize
		if scanOffset < scanChunkSize {
			scanSize = int(scanOffset)
		}
		scanOffset -= int64(scanSize)

		// 读取数据块
		if _, err := w.file.ReadAt(buffer[:scanSize], scanOffset); err != nil {
			return -1, types.NewInternalError("扫描 WAL 文件失败", err)
		}

		// 在数据块中查找 EOF 标记
		eofBytes := []byte(WALEOFMagic)
		for i := scanSize - 1; i >= 0; i-- {
			// 检查是否匹配 EOF 标记
			if i+WALEOFSize <= scanSize {
				match := true
				for j := 0; j < WALEOFSize; j++ {
					if buffer[i+j] != eofBytes[j] {
						match = false
						break
					}
				}
				if match {
					return scanOffset + int64(i), nil
				}
			}
		}
	}

	// 未找到 EOF 标记
	return -1, nil
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
	data := make([]byte, 0, int(keyLen)+int(valueLen)+int(oldValueLen)+int(timestampLen))
	data = append(data, entry.Key...)
	data = append(data, entry.Value...)
	data = append(data, entry.OldValue...)
	data = append(data, timestampData...)

	// 构建 Header (20 字节，不含 CRC): Magic(4) + Type(2) + KeyLen(4) + ValueLen(4) + OldValueLen(4) + TimestampLen(2)
	header := make([]byte, walHeaderSize)
	copy(header[0:4], []byte(walMagic))                         // Magic: "NxWL"
	binary.BigEndian.PutUint16(header[4:6], uint16(entry.Type)) // Type
	binary.BigEndian.PutUint32(header[6:10], keyLen)            // KeyLen
	binary.BigEndian.PutUint32(header[10:14], valueLen)         // ValueLen
	binary.BigEndian.PutUint32(header[14:18], oldValueLen)      // OldValueLen
	binary.BigEndian.PutUint16(header[18:20], timestampLen)     // TimestampLen
	// header[20:24] 保持为 0（CRC 字段）

	// 计算 CRC (覆盖 Header[0:20] + Data)
	// 注意：不包含 header[20:24]（CRC 字段本身）
	crc := crc32.ChecksumIEEE(append(header[:20], data...))
	binary.BigEndian.PutUint32(header[20:24], crc) // CRC

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
	if keyLen > 0 {
		entry.Key = make([]byte, keyLen)
		copy(entry.Key, data[offset:offset+int(keyLen)])
	}
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
