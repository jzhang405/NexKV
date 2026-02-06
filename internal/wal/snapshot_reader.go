// Package store Snapshot 文件读取器实现
//
// 实现分层式压缩 Snapshot 文件的读取功能
package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"

	"github.com/jzhang405/NexKV/internal/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// SnapshotReader Snapshot 读取器
type SnapshotReader struct {
	path        string
	file        *os.File
	header      SnapshotHeader
	compressor  types.Compressor
	compression types.CompressionType
}

// NewSnapshotReader 创建 Snapshot 读取器
//
// 参数：
// - path: Snapshot 文件路径
//
// 返回 SnapshotReader 实例和错误信息
func NewSnapshotReader(path string) (*SnapshotReader, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, types.NewStoreSnapshotOpenFailedError(err)
	}

	reader := &SnapshotReader{
		path: path,
		file: file,
	}

	// 读取并验证文件头
	if err := reader.readAndValidateHeader(); err != nil {
		_ = file.Close()
		return nil, err
	}

	return reader, nil
}

// readAndValidateHeader 读取并验证文件头
func (r *SnapshotReader) readAndValidateHeader() error {
	// 1. 读取 64B 文件头
	headerData := make([]byte, SnapshotHeaderSize)
	if _, err := io.ReadFull(r.file, headerData); err != nil {
		return types.NewStoreSnapshotReadHeaderFailedError(err)
	}

	// 2. 解析文件头
	if err := binary.Read(bytes.NewReader(headerData), binary.BigEndian, &r.header); err != nil {
		return types.NewStoreSnapshotParseHeaderFailedError(err)
	}

	// 3. 验证魔术字
	if string(r.header.Magic[:]) != SnapshotMagic {
		return types.NewStoreSnapshotInvalidMagicError(SnapshotMagic, string(r.header.Magic[:]))
	}

	// 4. 验证版本号
	if r.header.Version != SnapshotVersion {
		return types.NewStoreSnapshotUnsupportedVersionError(uint32(r.header.Version), uint32(SnapshotVersion))
	}

	// 5. 解析压缩算法类型
	r.compression = types.CompressionType(r.header.Compression)
	if err := r.compression.Validate(); err != nil {
		return types.NewStoreSnapshotInvalidCompressionError(err)
	}

	// 6. 获取压缩器
	compressor, err := types.NewCompressor(r.compression)
	if err != nil {
		return types.NewStoreSnapshotCompressorFailedError(err)
	}
	r.compressor = compressor

	logging.Infof("Snapshot 文件头验证成功: 版本=%d, 压缩=%s, 时间戳=%d",
		r.header.Version, r.compression.String(), r.header.Timestamp)

	return nil
}

// ReadMetadata 读取并解析元数据段
//
// 返回元数据（map[string]any）和错误信息
func (r *SnapshotReader) ReadMetadata() (map[string]any, error) {
	// 1. 定位到元数据段起始位置（文件头之后）
	if _, err := r.file.Seek(int64(SnapshotHeaderSize), io.SeekStart); err != nil {
		return nil, types.NewStoreSnapshotSeekMetadataFailedError(err)
	}

	// 2. 读取压缩的元数据段
	compressedMetadata := make([]byte, r.header.MetadataSize)
	if _, err := io.ReadFull(r.file, compressedMetadata); err != nil {
		return nil, types.NewStoreSnapshotReadMetadataFailedError(err)
	}

	// 3. 解压缩元数据
	metadataData, err := r.compressor.Decompress(compressedMetadata)
	if err != nil {
		return nil, types.NewStoreSnapshotDecompressMetadataFailedError(err)
	}

	// 4. JSON 反序列化元数据
	var metadata map[string]any
	if err := json.Unmarshal(metadataData, &metadata); err != nil {
		return nil, types.NewStoreSnapshotUnmarshalMetadataFailedError(err)
	}

	logging.Infof("Snapshot 元数据段读取成功: 压缩大小=%d, 解压后大小=%d",
		len(compressedMetadata), len(metadataData))

	return metadata, nil
}

// ReadData 读取并解析数据段
//
// 返回数据（map[string][]byte）和错误信息
func (r *SnapshotReader) ReadData() (map[string][]byte, error) {
	// 1. 定位到数据段起始位置（文件头 + 元数据段）
	dataOffset := int64(SnapshotHeaderSize + r.header.MetadataSize)
	if _, err := r.file.Seek(dataOffset, io.SeekStart); err != nil {
		return nil, types.NewStoreSnapshotSeekDataFailedError(err)
	}

	// 2. 读取压缩的数据段
	compressedData := make([]byte, r.header.DataSize)
	if _, err := io.ReadFull(r.file, compressedData); err != nil {
		return nil, types.NewStoreSnapshotReadDataFailedError(err)
	}

	// 3. 解压缩数据
	dataBytes, err := r.compressor.Decompress(compressedData)
	if err != nil {
		return nil, types.NewStoreSnapshotDecompressDataFailedError(err)
	}

	// 4. JSON 反序列化数据
	var data map[string][]byte
	if err := json.Unmarshal(dataBytes, &data); err != nil {
		return nil, types.NewStoreSnapshotUnmarshalDataFailedError(err)
	}

	logging.Infof("Snapshot 数据段读取成功: 压缩大小=%d, 解压后大小=%d",
		len(compressedData), len(dataBytes))

	return data, nil
}

// ValidateChecksum 验证 SHA256 校验和
//
// 返回验证结果和错误信息
func (r *SnapshotReader) ValidateChecksum() (bool, error) {
	// 1. 定位到校验和位置（文件末尾 - 32B）
	fileInfo, err := r.file.Stat()
	if err != nil {
		return false, types.NewStoreSnapshotGetFileInfoFailedError(err)
	}

	checksumOffset := fileInfo.Size() - int64(SnapshotChecksumSize)
	if _, err := r.file.Seek(checksumOffset, io.SeekStart); err != nil {
		return false, types.NewStoreSnapshotSeekChecksumFailedError(err)
	}

	// 2. 读取存储的校验和
	storedChecksum := make([]byte, SnapshotChecksumSize)
	if _, err := io.ReadFull(r.file, storedChecksum); err != nil {
		return false, types.NewStoreSnapshotReadChecksumFailedError(err)
	}

	// 3. 计算实际校验和（文件头 + 元数据段 + 数据段）
	if _, err := r.file.Seek(0, io.SeekStart); err != nil {
		return false, types.NewStoreSnapshotSeekStartFailedError(err)
	}

	dataSize := int64(SnapshotHeaderSize + r.header.MetadataSize + r.header.DataSize)
	dataToHash := make([]byte, dataSize)
	if _, err := io.ReadFull(r.file, dataToHash); err != nil {
		return false, types.NewStoreSnapshotReadDataForHashFailedError(err)
	}

	hasher := sha256.New()
	if _, err := hasher.Write(dataToHash); err != nil {
		return false, types.NewStoreSnapshotHashFailedError(err)
	}

	actualChecksum := hasher.Sum(nil)

	// 4. 比对校验和
	valid := bytes.Equal(storedChecksum, actualChecksum)
	if valid {
		logging.Infof("SHA256 校验和验证成功")
	} else {
		logging.Warnf("SHA256 校验和不匹配! 存储: %x, 实际: %x", storedChecksum, actualChecksum)
	}

	return valid, nil
}

// GetHeader 获取文件头信息
func (r *SnapshotReader) GetHeader() SnapshotHeader {
	return r.header
}

// Close 关闭读取器并释放资源
func (r *SnapshotReader) Close() error {
	if r.file != nil {
		if err := r.file.Close(); err != nil {
			return err
		}
		r.file = nil
	}
	return nil
}
