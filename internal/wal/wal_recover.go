// Package store WAL Recover 优化实现
//
// 优化 WAL Recover 性能，避免重复打开/关闭文件
// 使用文件指针定位，提高恢复效率
package store

import (
	"bufio"
	"encoding/binary"
	"hash/crc32"
	"io"

	"github.com/jzhang405/NexKV/internal/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// ========================================
// Recover 优化实现
// ========================================

// RecoverOptimized 优化的 WAL 恢复方法
//
// 优化点：
//  1. 避免关闭和重新打开文件
//  2. 使用文件指针定位到读取起始位置
//  3. 支持增量恢复（从指定 offset 开始）
//  4. 批量读取条目，减少系统调用
func (w *MetadataWAL) RecoverOptimized() ([]*WALEntry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// 保存当前写入位置
	writeOffset := w.offset

	// 获取当前文件描述符
	file := w.file

	// 使用文件指针定位到文件开头
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, types.NewInternalError("定位 WAL 文件开头失败", err)
	}

	var entries []*WALEntry
	reader := bufio.NewReader(file)

	for {
		// 记录当前读取位置
		offset, err := file.Seek(0, io.SeekCurrent)
		if err != nil {
			return nil, types.NewInternalError("获取文件位置失败", err)
		}

		// 读取 Header
		header := make([]byte, walHeaderSize)
		if _, err := io.ReadFull(reader, header); err != nil {
			if err == io.EOF {
				break
			}
			return nil, types.NewInternalError("读取 WAL header 失败", err)
		}

		// 解析 Header
		// Header 格式: Magic(4) + Type(2) + KeyLen(4) + ValueLen(4) + OldValueLen(4) + TimestampLen(2) + CRC(4)
		magic := string(header[0:4])
		if magic != walMagic {
			logging.Warnf("WAL 条目魔术字不匹配: 期望 %s, 实际 %s", walMagic, magic)
			// 尝试恢复：跳过无效数据
			continue
		}

		typ := WALType(binary.BigEndian.Uint16(header[4:6]))
		keyLen := binary.BigEndian.Uint32(header[6:10])
		valueLen := binary.BigEndian.Uint32(header[10:14])
		oldValueLen := binary.BigEndian.Uint32(header[14:18])
		timestampSize := binary.BigEndian.Uint16(header[18:20])

		// 读取 Data（包含 OldValue）
		dataSize := uint32(keyLen) + uint32(valueLen) + uint32(timestampSize) + oldValueLen
		data := make([]byte, dataSize)
		if _, err := io.ReadFull(reader, data); err != nil {
			return nil, types.NewInternalError("读取 WAL data 失败", err)
		}

		// 验证校验和 (覆盖 Header[0:20] + Data)
		// CRC 位于 header[20:24]
		headerCRC := binary.BigEndian.Uint32(header[20:24])
		computedChecksum := crc32.ChecksumIEEE(append(header[:20], data...))
		if computedChecksum != headerCRC {
			logging.Warnf("WAL 条目校验和不匹配（offset=%d），跳过", offset)
			continue
		}

		// 解析 Entry
		entry, err := w.decodeEntry(typ, keyLen, valueLen, oldValueLen, timestampSize, data)
		if err != nil {
			logging.Warnf("解码 WAL 条目失败（offset=%d）: %v", offset, err)
			continue
		}

		entries = append(entries, entry)
	}

	// 恢复写入位置到文件末尾
	if _, err := file.Seek(writeOffset, io.SeekStart); err != nil {
		return nil, types.NewInternalError("恢复写入位置失败", err)
	}

	logging.Infof("WAL Recover 完成: 读取了 %d 条有效记录", len(entries))

	return entries, nil
}

// RecoverFromOffset 从指定 offset 开始恢复
//
// 用于增量恢复：
//   - 只读取指定位置之后的条目
//   - 提高恢复效率
func (w *MetadataWAL) RecoverFromOffset(offset int64) ([]*WALEntry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// 定位到指定 offset
	if _, err := w.file.Seek(offset, io.SeekStart); err != nil {
		return nil, types.NewInternalError("定位 WAL 位置失败", err)
	}

	var entries []*WALEntry
	reader := bufio.NewReader(w.file)

	for {
		// 读取 Header (24 字节)
		header := make([]byte, walHeaderSize)
		n, err := io.ReadFull(reader, header)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return nil, types.NewInternalError("读取 WAL header 失败", err)
		}
		if n < walHeaderSize {
			break
		}

		// 检查是否是 EOF 标记（前 7 字节）
		if len(header) >= 7 && string(header[:7]) == WALEOFMagic {
			logging.Infof("检测到 EOF 标记，停止恢复")
			break
		}

		// 解析 Header
		// Header 格式: Magic(4) + Type(2) + KeyLen(4) + ValueLen(4) + OldValueLen(4) + TimestampLen(2) + CRC(4)
		magic := string(header[0:4])
		if magic != walMagic {
			logging.Warnf("WAL 条目魔术字不匹配: 期望 %s, 实际 %s", walMagic, magic)
			// 尝试恢复：跳过无效数据
			continue
		}

		typ := WALType(binary.BigEndian.Uint16(header[4:6]))
		keyLen := binary.BigEndian.Uint32(header[6:10])
		valueLen := binary.BigEndian.Uint32(header[10:14])
		oldValueLen := binary.BigEndian.Uint32(header[14:18])
		timestampSize := binary.BigEndian.Uint16(header[18:20])
		headerCRC := binary.BigEndian.Uint32(header[20:24])

		// 读取 Data（包含 OldValue）
		dataSize := uint32(keyLen) + uint32(valueLen) + uint32(timestampSize) + oldValueLen
		data := make([]byte, dataSize)
		if _, err := io.ReadFull(reader, data); err != nil {
			return nil, types.NewInternalError("读取 WAL data 失败", err)
		}

		// 验证校验和
		computedChecksum := crc32.ChecksumIEEE(append(header[:20], data...))
		if computedChecksum != headerCRC {
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

	return entries, nil
}

// RecoverBatch 批量恢复 WAL 条目
//
// 优化点：
//   - 一次性读取多条条目
//   - 减少函数调用开销
//   - 提高恢复速度
func (w *MetadataWAL) RecoverBatch(maxCount int) ([]*WALEntry, int64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// 保存当前写入位置
	writeOffset := w.offset

	// 定位到文件开头
	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return nil, 0, types.NewInternalError("定位 WAL 文件开头失败", err)
	}

	entries := make([]*WALEntry, 0, maxCount)
	reader := bufio.NewReader(w.file)
	var nextOffset int64 = 0

	for len(entries) < maxCount {
		// 记录当前读取位置
		offset, err := w.file.Seek(0, io.SeekCurrent)
		if err != nil {
			return nil, 0, types.NewInternalError("获取文件位置失败", err)
		}

		// 读取 Header (24 字节)
		header := make([]byte, walHeaderSize)
		n, err := io.ReadFull(reader, header)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return nil, 0, types.NewInternalError("读取 WAL header 失败", err)
		}
		if n < walHeaderSize {
			break
		}

		// 检查是否是 EOF 标记（前 7 字节）
		if len(header) >= 7 && string(header[:7]) == WALEOFMagic {
			logging.Infof("检测到 EOF 标记，停止恢复")
			break
		}

		// 解析 Header
		// Header 格式: Magic(4) + Type(2) + KeyLen(4) + ValueLen(4) + OldValueLen(4) + TimestampLen(2) + CRC(4)
		magic := string(header[0:4])
		if magic != walMagic {
			logging.Warnf("WAL 条目魔术字不匹配（offset=%d）: 期望 %s, 实际 %s，尝试重新同步", offset, walMagic, magic)
			// 尝试重新同步：查找下一个魔术字
			// 使用 Seek 跳过 1 字节，然后继续尝试
			if _, err := w.file.Seek(offset+1, io.SeekStart); err != nil {
				return nil, 0, types.NewInternalError("Seek 失败", err)
			}
			// 重置 reader
			reader.Reset(w.file)
			continue
		}

		// 解析 Header
		typ := WALType(binary.BigEndian.Uint16(header[4:6]))
		keyLen := binary.BigEndian.Uint32(header[6:10])
		valueLen := binary.BigEndian.Uint32(header[10:14])
		oldValueLen := binary.BigEndian.Uint32(header[14:18])
		timestampSize := binary.BigEndian.Uint16(header[18:20])
		headerCRC := binary.BigEndian.Uint32(header[20:24])

		// 读取 Data（包含 OldValue）
		dataSize := uint32(keyLen) + uint32(valueLen) + uint32(timestampSize) + oldValueLen
		data := make([]byte, dataSize)
		if _, err := io.ReadFull(reader, data); err != nil {
			if err == io.EOF {
				break
			}
			return nil, 0, types.NewInternalError("读取 WAL data 失败", err)
		}

		// 验证校验和 (覆盖 Header[0:20] + Data)
		// 注意：不包含 header[20:24]（CRC 字段本身）
		computedChecksum := crc32.ChecksumIEEE(append(header[:20], data...))
		if computedChecksum != headerCRC {
			logging.Warnf("WAL 条目校验和不匹配（offset=%d, computed=%d, header=%d），尝试重新同步", offset, computedChecksum, headerCRC)
			// 重新同步：从当前位置跳过 1 字节
			currentPos, _ := w.file.Seek(0, io.SeekCurrent)
			if _, err := w.file.Seek(currentPos+1, io.SeekStart); err != nil {
				return nil, 0, types.NewInternalError("Seek 失败", err)
			}
			reader.Reset(w.file)
			continue
		}

		// 解析 Entry
		entry, err := w.decodeEntry(typ, keyLen, valueLen, oldValueLen, timestampSize, data)
		if err != nil {
			logging.Warnf("解码 WAL 条目失败（offset=%d）: %v，尝试重新同步", offset, err)
			currentPos, _ := w.file.Seek(0, io.SeekCurrent)
			if _, err := w.file.Seek(currentPos+1, io.SeekStart); err != nil {
				return nil, 0, types.NewInternalError("Seek 失败", err)
			}
			reader.Reset(w.file)
			continue
		}

		entries = append(entries, entry)
		nextOffset = offset + int64(walHeaderSize+int(dataSize)+4)
	}

	// 恢复写入位置
	if _, err := w.file.Seek(writeOffset, io.SeekStart); err != nil {
		return nil, 0, types.NewInternalError("恢复写入位置失败", err)
	}

	return entries, nextOffset, nil
}

// ========================================
// 辅助功能
// ========================================

// ValidateWALFile 验证 WAL 文件完整性
//
// 检查：
//   - 文件是否存在
//   - 文件是否可读
//   - 条目校验和是否正确
//   - 返回损坏条目的数量
func (w *MetadataWAL) ValidateWALFile() (validEntries, corruptedEntries int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// 保存当前位置
	writeOffset := w.offset

	// 定位到文件开头
	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return 0, 0, types.NewInternalError("定位 WAL 文件开头失败", err)
	}

	reader := bufio.NewReader(w.file)

	for {
		// 读取 Header
		header := make([]byte, walHeaderSize)
		n, err := io.ReadFull(reader, header)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return 0, 0, types.NewInternalError("读取 WAL header 失败", err)
		}
		if n < walHeaderSize {
			break
		}

		// 检查是否是 EOF 标记（前 7 字节）
		if len(header) >= 7 && string(header[:7]) == WALEOFMagic {
			break
		}

		// 解析 Header
		// Header 格式: Magic(4) + Type(2) + KeyLen(4) + ValueLen(4) + OldValueLen(4) + TimestampLen(2) + CRC(4)
		magic := string(header[0:4])
		if magic != walMagic {
			corruptedEntries++
			continue
		}

		keyLen := binary.BigEndian.Uint32(header[6:10])
		valueLen := binary.BigEndian.Uint32(header[10:14])
		oldValueLen := binary.BigEndian.Uint32(header[14:18])
		timestampSize := binary.BigEndian.Uint16(header[18:20])
		headerCRC := binary.BigEndian.Uint32(header[20:24])

		// 读取 Data（包含 OldValue）
		dataSize := uint32(keyLen) + uint32(valueLen) + uint32(timestampSize) + oldValueLen
		data := make([]byte, dataSize)
		if _, err := io.ReadFull(reader, data); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				corruptedEntries++
				break
			}
			return 0, 0, types.NewInternalError("读取 WAL data 失败", err)
		}

		// 验证校验和 (覆盖 Header[0:20] + Data)
		computedChecksum := crc32.ChecksumIEEE(append(header[:20], data...))
		if computedChecksum != headerCRC {
			corruptedEntries++
		} else {
			validEntries++
		}
	}

	// 恢复写入位置
	if _, err := w.file.Seek(writeOffset, io.SeekStart); err != nil {
		return 0, 0, types.NewInternalError("恢复写入位置失败", err)
	}

	return validEntries, corruptedEntries, nil
}

// ========================================
// WAL 检查点优化
// ========================================

// CreateCheckpointOptimized 创建优化的检查点
//
// 优化点：
//   - 不删除旧 WAL 文件
//   - 只记录检查点位置
//   - 支持快速恢复到检查点
func (w *MetadataWAL) CreateCheckpointOptimized() (int64, error) {
	w.mu.Lock()
	checkpointOffset := w.offset
	w.mu.Unlock()

	// 写入 checkpoint 条目（Append 内部会获取锁）
	checkpointEntry := &WALEntry{
		Type: WALTypeCheckpoint,
	}

	if err := w.Append(checkpointEntry); err != nil {
		return 0, types.NewInternalError("写入 checkpoint 失败", err)
	}

	logging.Infof("创建检查点: offset=%d", checkpointOffset)

	return checkpointOffset, nil
}

// RecoverToCheckpoint 恢复到最近的检查点
//
// 恢复流程：
//  1. 扫描所有 WAL 条目
//  2. 找到最新的 checkpoint 条目
//  3. 只恢复 checkpoint 之后的条目
func (w *MetadataWAL) RecoverToCheckpoint() ([]*WALEntry, int64, error) {
	entries, nextOffset, err := w.RecoverBatch(1000000) // 读取大量条目
	if err != nil {
		return nil, 0, err
	}

	// 从后往前查找最新的 checkpoint
	var checkpointIndex = -1
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type == WALTypeCheckpoint {
			checkpointIndex = i
			break
		}
	}

	// 没有找到 checkpoint，返回所有条目
	if checkpointIndex == -1 {
		return entries, nextOffset, nil
	}

	// 返回 checkpoint 之后的条目
	result := entries[checkpointIndex+1:]

	logging.Infof("恢复到检查点: checkpointIndex=%d, 恢复了 %d 条记录",
		checkpointIndex, len(result))

	return result, nextOffset, nil
}
