// Package store Snapshot 文件管理器实现
//
// 负责 Snapshot 文件的创建、读取、删除和列表管理
// SnapshotFileManager 管理 PR-003 定义的分层式压缩 Snapshot 文件
package store

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/jzhang405/NexKV/internal/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/types"
)

const (
	// SnapshotFilePattern Snapshot 文件命名模式
	// 格式：snapshot-{timestamp}-{sequence}.snap
	SnapshotFilePattern = `^snapshot-(\d+)-(\d+)\.snap$`
)

// SnapshotInfo Snapshot 文件元信息
type SnapshotInfo struct {
	FileName    string                // 文件名（如 snapshot-1735689600-0001.snap）
	FilePath    string                // 文件完整路径
	Timestamp   int64                 // 创建时间戳（Unix 秒）
	Sequence    int                   // 序列号
	Size        int64                 // 文件大小（字节）
	Checksum    string                // SHA256 校验和（十六进制字符串）
	Compression types.CompressionType // 压缩算法类型
	Version     uint16                // 格式版本号
}

// SnapshotFileManager PR-003 Snapshot 文件管理器
type SnapshotFileManager struct {
	snapshotDir string
	compression types.CompressionType
}

// NewSnapshotFileManager 创建 Snapshot 文件管理器
//
// 参数：
// - snapshotDir: Snapshot 文件存储目录
// - compression: 压缩算法类型
//
// 返回 SnapshotManager 实例和错误信息
func NewSnapshotFileManager(snapshotDir string, compression types.CompressionType) (*SnapshotFileManager, error) {
	// 确保目录存在
	if err := os.MkdirAll(snapshotDir, 0755); err != nil {
		return nil, types.NewStoreSnapshotCreateDirectoryFailedError(err)
	}

	return &SnapshotFileManager{
		snapshotDir: snapshotDir,
		compression: compression,
	}, nil
}

// CreateSnapshot 创建新的 Snapshot 文件
//
// 职责：
// - 管理 Snapshot 目录和文件命名
// - 调用 SnapshotWriter 完成数据写入
// - 处理最终的文件重命名和目录管理
//
// 参数：
// - metadata: 元数据（map 版本、条目数量等）
// - data: MVStore 数据（map[string][]byte）
//
// 返回 SnapshotInfo 和错误信息
func (m *SnapshotFileManager) CreateSnapshot(metadata map[string]any, data map[string][]byte) (*SnapshotInfo, error) {
	// 1. 生成序列号
	timestamp := time.Now().Unix()
	sequence := m.getNextSequence(timestamp)

	// 2. 生成临时文件路径（在目标目录中）
	tempFileBase := fmt.Sprintf("snapshot-%d-%04d", timestamp, sequence)
	tempFilePath := filepath.Join(m.snapshotDir, tempFileBase)

	// 3. 创建 Snapshot 写入器（传递完整临时文件路径）
	writer, err := NewSnapshotWriter(tempFilePath, m.compression, sequence)
	if err != nil {
		return nil, types.NewStoreSnapshotCreateWriterFailedError(err)
	}
	defer func() {
		if err := writer.Close(); err != nil {
			logging.Warnf("关闭 Snapshot 写入器失败: %v", err)
		}
	}()

	// 4. 写入元数据段
	if err := writer.WriteMetadata(metadata); err != nil {
		return nil, types.NewStoreSnapshotWriteMetadataSectionFailedError(err)
	}

	// 5. 写入数据段
	if err := writer.WriteData(data); err != nil {
		return nil, types.NewStoreSnapshotWriteDataSectionFailedError(err)
	}

	// 6. 完成写入（返回临时文件名）
	tempFileName, err := writer.Finalize()
	if err != nil {
		return nil, types.NewStoreSnapshotFinalizeFailedError(err)
	}

	// 7. 生成最终文件名和路径
	finalFileName := fmt.Sprintf("snapshot-%d-%04d.snap", timestamp, sequence)
	finalPath := filepath.Join(m.snapshotDir, finalFileName)

	// 8. 原子重命名临时文件到最终位置
	tempPath := filepath.Join(m.snapshotDir, tempFileName)
	if err := os.Rename(tempPath, finalPath); err != nil {
		_ = os.Remove(tempPath) // 清理临时文件
		return nil, types.NewStoreSnapshotRenameFailedError(err)
	}

	// 9. 构建 SnapshotInfo
	info := &SnapshotInfo{
		FileName:    finalFileName,
		FilePath:    finalPath,
		Timestamp:   timestamp,
		Sequence:    sequence,
		Compression: m.compression,
		Version:     SnapshotVersion,
	}

	// 10. 获取文件大小
	if stat, err := os.Stat(finalPath); err == nil {
		info.Size = stat.Size()
	}

	logging.Infof("Snapshot 创建成功: %s (大小: %d bytes)", finalFileName, info.Size)

	return info, nil
}

// LoadSnapshot 加载指定的 Snapshot 文件
//
// 参数：
// - fileName: Snapshot 文件名（如 snapshot-1735689600-0001.snap）
//
// 返回元数据、数据和错误信息
func (m *SnapshotFileManager) LoadSnapshot(fileName string) (map[string]any, map[string][]byte, error) {
	filePath := filepath.Join(m.snapshotDir, fileName)

	// 1. 创建 Snapshot 读取器
	reader, err := NewSnapshotReader(filePath)
	if err != nil {
		return nil, nil, types.NewStoreSnapshotCreateReaderFailedError(err)
	}
	defer func() {
		if err := reader.Close(); err != nil {
			logging.Warnf("关闭 Snapshot 读取器失败: %v", err)
		}
	}()

	// 2. 验证校验和
	valid, err := reader.ValidateChecksum()
	if err != nil {
		return nil, nil, types.NewStoreSnapshotVerifyChecksumFailedError(err)
	}
	if !valid {
		return nil, nil, types.NewStoreSnapshotChecksumMismatchError()
	}

	// 3. 读取元数据段
	metadata, err := reader.ReadMetadata()
	if err != nil {
		return nil, nil, types.NewStoreSnapshotReadMetadataSectionFailedError(err)
	}

	// 4. 读取数据段
	data, err := reader.ReadData()
	if err != nil {
		return nil, nil, types.NewStoreSnapshotReadDataSectionFailedError(err)
	}

	logging.Infof("Snapshot 加载成功: %s", fileName)

	return metadata, data, nil
}

// ListSnapshots 列出所有 Snapshot 文件
//
// 返回 SnapshotInfo 列表（按时间戳降序排序）
func (m *SnapshotFileManager) ListSnapshots() ([]*SnapshotInfo, error) {
	// 1. 读取目录中的所有文件
	entries, err := os.ReadDir(m.snapshotDir)
	if err != nil {
		return nil, types.NewStoreSnapshotReadDirectoryFailedError(err)
	}

	// 2. 解析 Snapshot 文件
	pattern := regexp.MustCompile(SnapshotFilePattern)
	var snapshots []*SnapshotInfo

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		matches := pattern.FindStringSubmatch(entry.Name())
		if matches == nil {
			continue // 不是 Snapshot 文件
		}

		// 解析时间戳和序列号
		var timestamp, sequence int64
		_, _ = fmt.Sscanf(matches[1], "%d", &timestamp)
		_, _ = fmt.Sscanf(matches[2], "%d", &sequence)

		// 获取文件信息
		filePath := filepath.Join(m.snapshotDir, entry.Name())
		stat, err := os.Stat(filePath)
		if err != nil {
			logging.Warnf("无法获取文件信息: %s (%v)", entry.Name(), err)
			continue
		}

		// 读取文件头获取压缩算法和版本号
		compression := types.CompressionTypeNone
		version := uint16(SnapshotVersion)
		if header, err := readSnapshotHeader(filePath); err == nil {
			compression = types.CompressionType(header.Compression)
			version = header.Version
		}

		snapshots = append(snapshots, &SnapshotInfo{
			FileName:    entry.Name(),
			FilePath:    filePath,
			Timestamp:   timestamp,
			Sequence:    int(sequence),
			Size:        stat.Size(),
			Compression: compression,
			Version:     version,
		})
	}

	// 3. 按时间戳降序排序（最新的在前）
	sort.Slice(snapshots, func(i, j int) bool {
		if snapshots[i].Timestamp != snapshots[j].Timestamp {
			return snapshots[i].Timestamp > snapshots[j].Timestamp
		}
		return snapshots[i].Sequence > snapshots[j].Sequence
	})

	return snapshots, nil
}

// GetLatestSnapshot 获取最新的 Snapshot 文件
//
// 返回最新的 SnapshotInfo 和错误信息
func (m *SnapshotFileManager) GetLatestSnapshot() (*SnapshotInfo, error) {
	snapshots, err := m.ListSnapshots()
	if err != nil {
		return nil, err
	}

	if len(snapshots) == 0 {
		return nil, types.NewStoreSnapshotNoSnapshotFoundError()
	}

	return snapshots[0], nil
}

// DeleteSnapshot 删除指定的 Snapshot 文件
//
// 参数：
// - fileName: Snapshot 文件名
//
// 返回错误信息
func (m *SnapshotFileManager) DeleteSnapshot(fileName string) error {
	filePath := filepath.Join(m.snapshotDir, fileName)

	if err := os.Remove(filePath); err != nil {
		return types.NewStoreSnapshotDeleteFailedError(err)
	}

	logging.Infof("Snapshot 删除成功: %s", fileName)

	return nil
}

// CleanupOldSnapshots 清理旧的 Snapshot 文件，保留最新的 N 个
//
// 参数：
// - keep: 保留的文件数量
//
// 返回删除的文件列表和错误信息
func (m *SnapshotFileManager) CleanupOldSnapshots(keep int) ([]string, error) {
	snapshots, err := m.ListSnapshots()
	if err != nil {
		return nil, err
	}

	if len(snapshots) <= keep {
		return nil, nil // 没有需要删除的文件
	}

	var deletedFiles []string
	for i := keep; i < len(snapshots); i++ {
		if err := m.DeleteSnapshot(snapshots[i].FileName); err == nil {
			deletedFiles = append(deletedFiles, snapshots[i].FileName)
		}
	}

	if len(deletedFiles) > 0 {
		logging.Infof("清理旧 Snapshot 完成，删除 %d 个文件: %v", len(deletedFiles), deletedFiles)
	}

	return deletedFiles, nil
}

// CreateSnapshotWithVersion 使用指定版本号创建 Snapshot（用于全局序列号管理）
//
// 核心改进：
//   - 使用全局序列号生成器提供的版本号，不再自己生成序列号
//   - 确保 checkpoint_version ≡ snapshot_sequence
//   - 目录逻辑由 SnapshotFileManager 管理，SnapshotWriter 只负责文件写入
//
// 参数：
// - metadata: 元数据（map 版本、条目数量等）
// - data: MVStore 数据（map[string][]byte）
// - version: 全局序列号（由 SequenceGenerator 提供）
//
// 返回 SnapshotInfo 和错误信息
func (m *SnapshotFileManager) CreateSnapshotWithVersion(
	metadata map[string]any,
	data map[string][]byte,
	version int,
) (*SnapshotInfo, error) {
	// 1. 使用全局序列号
	timestamp := time.Now().Unix()
	sequence := version // 使用全局序列号

	// 2. 生成临时文件路径（在目标目录中）
	tempFileBase := fmt.Sprintf("snapshot-%d-%04d", timestamp, sequence)
	tempFilePath := filepath.Join(m.snapshotDir, tempFileBase)

	// 3. 创建 Snapshot 写入器（传递完整临时文件路径）
	writer, err := NewSnapshotWriter(tempFilePath, m.compression, sequence)
	if err != nil {
		return nil, types.NewStoreSnapshotCreateWriterFailedError(err)
	}
	defer func() {
		if err := writer.Close(); err != nil {
			logging.Warnf("关闭 Snapshot 写入器失败: %v", err)
		}
	}()

	// 4. 写入元数据段
	if err := writer.WriteMetadata(metadata); err != nil {
		return nil, types.NewStoreSnapshotWriteMetadataSectionFailedError(err)
	}

	// 5. 写入数据段
	if err := writer.WriteData(data); err != nil {
		return nil, types.NewStoreSnapshotWriteDataSectionFailedError(err)
	}

	// 6. 完成写入（返回临时文件名）
	tempFileName, err := writer.Finalize()
	if err != nil {
		return nil, types.NewStoreSnapshotFinalizeFailedError(err)
	}

	// 7. 生成最终文件名和路径
	finalFileName := fmt.Sprintf("snapshot-%d-%04d.snap", timestamp, sequence)
	finalPath := filepath.Join(m.snapshotDir, finalFileName)

	// 8. 原子重命名临时文件到最终位置
	tempPath := filepath.Join(m.snapshotDir, tempFileName)
	if err := os.Rename(tempPath, finalPath); err != nil {
		_ = os.Remove(tempPath) // 清理临时文件
		return nil, types.NewStoreSnapshotRenameFailedError(err)
	}

	// 9. 构建 SnapshotInfo
	info := &SnapshotInfo{
		FileName:    finalFileName,
		FilePath:    finalPath,
		Timestamp:   timestamp,
		Sequence:    sequence,
		Compression: m.compression,
		Version:     SnapshotVersion,
	}

	// 10. 获取文件大小
	if stat, err := os.Stat(finalPath); err == nil {
		info.Size = stat.Size()
	}

	logging.Infof("Snapshot 创建成功（全局序列号）: %s (sequence=%d, 大小: %d bytes)",
		finalFileName, sequence, info.Size)

	return info, nil
}

// getNextSequence 获取下一个序列号
//
// 参数：
// - timestamp: 时间戳（Unix 秒）
//
// 返回序列号
func (m *SnapshotFileManager) getNextSequence(timestamp int64) int {
	snapshots, err := m.ListSnapshots()
	if err != nil {
		return 1 // 默认序列号
	}

	// 查找相同时间戳的最大序列号
	maxSequence := 0
	for _, snap := range snapshots {
		if snap.Timestamp == timestamp && snap.Sequence > maxSequence {
			maxSequence = snap.Sequence
		}
	}

	return maxSequence + 1
}

// readSnapshotHeader 读取 Snapshot 文件头（辅助函数）
//
// 参数：
// - filePath: Snapshot 文件路径
//
// 返回 SnapshotHeader 和错误信息
func readSnapshotHeader(filePath string) (SnapshotHeader, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return SnapshotHeader{}, err
	}
	defer func() {
		_ = file.Close()
	}()

	var header SnapshotHeader
	if err := binary.Read(file, binary.BigEndian, &header); err != nil {
		return SnapshotHeader{}, err
	}

	return header, nil
}

// ParseSnapshotFileName 解析 Snapshot 文件名
//
// 参数：
// - fileName: Snapshot 文件名（如 snapshot-1735689600-0001.snap）
//
// 返回时间戳、序列号和错误信息
func ParseSnapshotFileName(fileName string) (int64, int, error) {
	pattern := regexp.MustCompile(SnapshotFilePattern)
	matches := pattern.FindStringSubmatch(fileName)
	if matches == nil {
		return 0, 0, types.NewStoreSnapshotInvalidFileNameError(fileName)
	}

	var timestamp int64
	var sequence int
	_, _ = fmt.Sscanf(matches[1], "%d", &timestamp)
	_, _ = fmt.Sscanf(matches[2], "%d", &sequence)

	return timestamp, sequence, nil
}

// FormatSnapshotFileName 格式化 Snapshot 文件名
//
// 参数：
// - timestamp: 时间戳（Unix 秒）
// - sequence: 序列号
//
// 返回文件名
func FormatSnapshotFileName(timestamp int64, sequence int) string {
	return fmt.Sprintf("snapshot-%d-%04d.snap", timestamp, sequence)
}

// IsSnapshotFile 判断是否为 Snapshot 文件
//
// 参数：
// - fileName: 文件名
//
// 返回判断结果
func IsSnapshotFile(fileName string) bool {
	pattern := regexp.MustCompile(SnapshotFilePattern)
	return pattern.MatchString(fileName)
}

// ValidateSnapshotDir 验证 Snapshot 目录是否有效
//
// 参数：
// - dir: Snapshot 目录路径
//
// 返回验证结果和错误信息
func ValidateSnapshotDir(dir string) (bool, error) {
	// 1. 检查目录是否存在
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, types.NewStoreSnapshotDirectoryNotExistError(dir)
		}
		return false, types.NewStoreSnapshotDirectoryNotAccessibleError(err)
	}

	// 2. 检查是否为目录
	if !info.IsDir() {
		return false, types.NewStoreSnapshotPathNotDirectoryError(dir)
	}

	// 3. 检查写权限
	testFile := filepath.Join(dir, ".write_test")
	f, err := os.Create(testFile)
	if err != nil {
		return false, types.NewStoreSnapshotDirectoryNotWritableError(err)
	}
	_ = f.Close()
	_ = os.Remove(testFile)

	return true, nil
}

// ============================================================================
// SnapshotManager 接口适配器
// ============================================================================

// Create 创建快照（实现 SnapshotManager 接口）
//
// 适配说明：此方法实现了旧的 SnapshotManager 接口，用于与 MemoryMVStore 兼容
// - 从 MVStore 获取快照数据（JSON 编码）
// - 使用新的 Snapshot 格式保存（JSON + 压缩）
// - 保持向后兼容性
func (m *SnapshotFileManager) Create(store MVStore) error {
	// 1. 从 MVStore 获取快照数据（JSON 编码）
	snapshotData, err := store.CreateSnapshot()
	if err != nil {
		return types.NewStoreSnapshotGetDataFailedError(err)
	}

	// 2. 准备元数据
	metadata := map[string]any{
		"version":     1,
		"entry_count": 0, // JSON 解析后无法准确统计条目数
		"created_at":  time.Now().Format(time.RFC3339),
		"source":      "MemoryMVStore",
	}

	// 3. 解析 JSON 数据获取 key-value 对
	var data map[string][]byte
	if err := json.Unmarshal(snapshotData, &data); err != nil {
		// 如果 JSON 解析失败，直接存储原始数据
		// 注意：旧实现返回的是 map[string][]*versionEntry 的 JSON
		// 我们需要将其转换为 map[string][]byte 或保持原始格式
		data = map[string][]byte{
			"snapshot_data": snapshotData,
		}
		metadata["encoding"] = "json_raw"
	} else {
		metadata["encoding"] = "json_map"
		metadata["entry_count"] = len(data)
	}

	// 4. 创建 Snapshot（使用新实现）
	info, err := m.CreateSnapshot(metadata, data)
	if err != nil {
		return types.NewStoreSnapshotCreationFailedError(err)
	}

	logging.Infof("快照创建成功: %s", info.FileName)
	return nil
}

// List 列出所有快照（实现 SnapshotManager 接口）
//
// 适配说明：返回新格式 Snapshot 文件名列表
func (m *SnapshotFileManager) List() ([]string, error) {
	snapshots, err := m.ListSnapshots()
	if err != nil {
		return nil, err
	}

	result := make([]string, len(snapshots))
	for i, snap := range snapshots {
		result[i] = snap.FileName
	}
	return result, nil
}

// Restore 从快照恢复（实现 SnapshotManager 接口）
//
// 适配说明：从新格式 Snapshot 文件读取数据并返回
func (m *SnapshotFileManager) Restore(snapshotName string) ([]byte, error) {
	// 1. 加载 Snapshot
	metadata, data, err := m.LoadSnapshot(snapshotName)
	if err != nil {
		return nil, types.NewStoreSnapshotLoadFailedError(err)
	}

	// 2. 检查编码格式
	encoding, ok := metadata["encoding"].(string)
	if !ok {
		encoding = "unknown"
	}

	// 3. 根据编码格式返回数据
	switch encoding {
	case "json_raw":
		// 直接返回原始 JSON 数据
		if rawData, ok := data["snapshot_data"]; ok {
			return rawData, nil
		}
		return nil, types.NewStoreSnapshotMissingSnapshotDataError()
	case "json_map", "unknown":
		// 返回 JSON 编码的数据
		return json.Marshal(data)
	default:
		return json.Marshal(data)
	}
}

// Delete 删除快照（实现 SnapshotManager 接口）
//
// 适配说明：使用新实现删除 Snapshot
func (m *SnapshotFileManager) Delete(snapshotName string) error {
	return m.DeleteSnapshot(snapshotName)
}

// Close 关闭快照管理器（实现 SnapshotManager 接口）
//
// 适配说明：新实现无需显式关闭，此方法为空操作
func (m *SnapshotFileManager) Close() error {
	return nil
}
