// Package store Snapshot 文件写入器实现
//
// 实现分层式压缩 Snapshot 文件的写入功能
package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/types"
)

const (
	// SnapshotMagic Snapshot 文件魔术字
	SnapshotMagic = "NxSN"

	// SnapshotHeaderSize Snapshot 文件头大小（固定 64B）
	SnapshotHeaderSize = 64

	// SnapshotChecksumSize SHA256 校验和大小（32B）
	SnapshotChecksumSize = 32

	// SnapshotVersion 当前格式版本号
	SnapshotVersion = 1
)

// SnapshotHeader Snapshot 文件头（固定 64B）
//
// 分段式格式：
// - Magic:        4 bytes  (魔术字 "NxSN")                  [0:4]
// - Version:      2 bytes  (格式版本号，当前=1)              [4:6]
// - Compression:  2 bytes  (压缩算法类型)                  [6:8]
// - Flags:        2 bytes  (标志位，预留)                    [8:10]
// - Timestamp:    8 bytes  (创建时间戳，Unix 毫秒)          [10:18]
// - MetadataSize: 4 bytes  (元数据段压缩后大小)             [18:22]
// - DataSize:     4 bytes  (数据段压缩后大小)                [22:26]
// - Reserved:     38 bytes (预留字段，填充 0)               [26:64]
type SnapshotHeader struct {
	Magic        [4]byte  // 魔术字 "NxSN"
	Version      uint16   // 格式版本号
	Compression  uint16   // 压缩算法类型（types.CompressionType）
	Flags        uint16   // 标志位（预留）
	Timestamp    int64    // 创建时间戳（Unix 毫秒）
	MetadataSize uint32   // 元数据段压缩后大小
	DataSize     uint32   // 数据段压缩后大小
	Reserved     [38]byte // 预留字段
}

// SnapshotWriter Snapshot 写入器
type SnapshotWriter struct {
	path        string
	tempFile    *os.File
	header      SnapshotHeader
	compressor  types.Compressor
	compression types.CompressionType
	metadataBuf *bytes.Buffer
	dataBuf     *bytes.Buffer
	sequence    int // 序列号（用于生成最终文件名）
}

// NewSnapshotWriter 创建 Snapshot 写入器
//
// 参数：
// - path: Snapshot 文件路径（不含扩展名）
// - compression: 压缩算法类型
// - sequence: 序列号（用于生成最终文件名，同一时间戳内递增）
//
// 返回 SnapshotWriter 实例和错误信息
func NewSnapshotWriter(path string, compression types.CompressionType, sequence int) (*SnapshotWriter, error) {
	// 创建临时文件
	tempPath := path + ".tmp"
	file, err := os.Create(tempPath)
	if err != nil {
		return nil, fmt.Errorf("创建临时文件失败: %w", err)
	}

	// 获取压缩器
	compressor, err := types.NewCompressor(compression)
	if err != nil {
		_ = file.Close()
		_ = os.Remove(tempPath)
		return nil, fmt.Errorf("创建压缩器失败: %w", err)
	}

	return &SnapshotWriter{
		path:        path,
		tempFile:    file,
		compression: compression,
		compressor:  compressor,
		metadataBuf: bytes.NewBuffer(nil),
		dataBuf:     bytes.NewBuffer(nil),
		sequence:    sequence,
	}, nil
}

// WriteMetadata 写入元数据段（压缩）
//
// 参数：
// - metadata: 元数据（map 版本、条目数量等）
//
// 返回错误信息
func (w *SnapshotWriter) WriteMetadata(metadata map[string]any) error {
	// 1. 序列化元数据（使用 JSON，简单可读）
	jsonData, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("序列化元数据失败: %w", err)
	}

	// 2. 压缩元数据
	compressed, err := w.compressor.Compress(jsonData)
	if err != nil {
		return fmt.Errorf("压缩元数据失败: %w", err)
	}

	// 3. 缓存到内存缓冲区（将在 Finalize 时按正确顺序写入文件）
	w.header.MetadataSize = uint32(len(compressed))
	if _, err := w.metadataBuf.Write(compressed); err != nil {
		return fmt.Errorf("缓存元数据段失败: %w", err)
	}

	logging.Infof("Snapshot 元数据段准备成功: 原始大小=%d, 压缩后大小=%d, 压缩率=%.2f%%",
		len(jsonData), len(compressed),
		float64(len(compressed))/float64(len(jsonData))*100)

	return nil
}

// WriteData 写入数据段（压缩）
//
// 参数：
// - data: MVStore 数据（map[string][]byte）
//
// 返回错误信息
func (w *SnapshotWriter) WriteData(data map[string][]byte) error {
	// 1. 序列化数据（使用 JSON）
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("序列化数据失败: %w", err)
	}

	// 2. 压缩数据
	compressed, err := w.compressor.Compress(jsonData)
	if err != nil {
		return fmt.Errorf("压缩数据失败: %w", err)
	}

	// 3. 缓存到内存缓冲区（将在 Finalize 时按正确顺序写入文件）
	w.header.DataSize = uint32(len(compressed))
	if _, err := w.dataBuf.Write(compressed); err != nil {
		return fmt.Errorf("缓存数据段失败: %w", err)
	}

	logging.Infof("Snapshot 数据段准备成功: 原始大小=%d, 压缩后大小=%d, 压缩率=%.2f%%",
		len(jsonData), len(compressed),
		float64(len(compressed))/float64(len(jsonData))*100)

	return nil
}

// Finalize 完成写入并计算校验和
//
// 职责：只负责写入数据到临时文件，不处理文件命名和重命名
//
// 步骤：
// 1. 构建文件头
// 2. 写入文件头（64B）
// 3. 写入元数据段（从内存缓冲区）
// 4. 写入数据段（从内存缓冲区）
// 5. 计算并写入 SHA256 校验和（32B）
// 6. Sync 并关闭临时文件
//
// 返回：临时文件名（如 "snapshot-xxx.tmp"，不含路径，由调用者负责重命名）
//
// 错误：多次调用会返回错误
func (w *SnapshotWriter) Finalize() (string, error) {
	// 防止多次调用
	if w.tempFile == nil {
		return "", fmt.Errorf("Finalize 已调用，不能重复调用")
	}
	// 1. 构建文件头
	copy(w.header.Magic[:], SnapshotMagic)
	w.header.Version = SnapshotVersion
	w.header.Compression = uint16(w.compression)
	w.header.Timestamp = time.Now().UnixMilli()

	// 2. 写入文件头（文件起始位置）
	if err := binary.Write(w.tempFile, binary.BigEndian, &w.header); err != nil {
		_ = w.tempFile.Close()
		_ = os.Remove(w.tempFile.Name())
		return "", fmt.Errorf("写入文件头失败: %w", err)
	}

	// 3. 写入元数据段（从内存缓冲区）
	if _, err := w.tempFile.Write(w.metadataBuf.Bytes()); err != nil {
		_ = w.tempFile.Close()
		_ = os.Remove(w.tempFile.Name())
		return "", fmt.Errorf("写入元数据段失败: %w", err)
	}

	// 4. 写入数据段（从内存缓冲区）
	if _, err := w.tempFile.Write(w.dataBuf.Bytes()); err != nil {
		_ = w.tempFile.Close()
		_ = os.Remove(w.tempFile.Name())
		return "", fmt.Errorf("写入数据段失败: %w", err)
	}

	// 5. 计算全局 SHA256 校验和（文件头 + 元数据段 + 数据段）
	hasher := sha256.New()

	// 5.1 序列化文件头并计算哈希
	headerBuf := new(bytes.Buffer)
	if err := binary.Write(headerBuf, binary.BigEndian, &w.header); err != nil {
		_ = w.tempFile.Close()
		_ = os.Remove(w.tempFile.Name())
		return "", fmt.Errorf("序列化文件头失败: %w", err)
	}
	if _, err := hasher.Write(headerBuf.Bytes()); err != nil {
		_ = w.tempFile.Close()
		_ = os.Remove(w.tempFile.Name())
		return "", fmt.Errorf("计算文件头哈希失败: %w", err)
	}

	// 5.2 计算元数据段哈希
	if _, err := hasher.Write(w.metadataBuf.Bytes()); err != nil {
		_ = w.tempFile.Close()
		_ = os.Remove(w.tempFile.Name())
		return "", fmt.Errorf("计算元数据段哈希失败: %w", err)
	}

	// 5.3 计算数据段哈希
	if _, err := hasher.Write(w.dataBuf.Bytes()); err != nil {
		_ = w.tempFile.Close()
		_ = os.Remove(w.tempFile.Name())
		return "", fmt.Errorf("计算数据段哈希失败: %w", err)
	}

	checksum := hasher.Sum(nil)

	// 6. Sync 确保数据落盘
	if err := w.tempFile.Sync(); err != nil {
		_ = w.tempFile.Close()
		_ = os.Remove(w.tempFile.Name())
		return "", fmt.Errorf("同步数据失败: %w", err)
	}

	// 7. 写入校验和
	if _, err := w.tempFile.Write(checksum); err != nil {
		_ = w.tempFile.Close()
		_ = os.Remove(w.tempFile.Name())
		return "", fmt.Errorf("写入校验和失败: %w", err)
	}

	// 8. 最终 Sync
	if err := w.tempFile.Sync(); err != nil {
		_ = w.tempFile.Close()
		_ = os.Remove(w.tempFile.Name())
		return "", fmt.Errorf("最终同步失败: %w", err)
	}

	// 9. 关闭文件
	if err := w.tempFile.Close(); err != nil {
		_ = os.Remove(w.tempFile.Name())
		return "", fmt.Errorf("关闭文件失败: %w", err)
	}

	// 10. 返回临时文件名（不含路径），由调用者负责最终命名和重命名
	tempFileName := filepath.Base(w.tempFile.Name())
	w.tempFile = nil // 标记已关闭，防止 Close() 重复清理

	totalSize := w.header.MetadataSize + w.header.DataSize + SnapshotHeaderSize + SnapshotChecksumSize
	logging.Infof("Snapshot 临时文件创建成功: %s (大小: %d bytes)", tempFileName, totalSize)

	return tempFileName, nil
}

// Close 关闭写入器并清理资源
//
// 注意：如果已调用 Finalize()，临时文件已被关闭且由调用者管理
// 如果未调用 Finalize()，将清理临时文件
func (w *SnapshotWriter) Close() error {
	if w.tempFile != nil {
		// 未调用 Finalize，需要关闭并清理临时文件
		if err := w.tempFile.Close(); err != nil {
			return err
		}
		_ = os.Remove(w.tempFile.Name())
		w.tempFile = nil
	}
	// 如果 tempFile 为 nil，说明已调用 Finalize，无需清理
	return nil
}
