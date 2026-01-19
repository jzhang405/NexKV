// Package store Checkpoint 文件实现
//
// Checkpoint 是 JSON 格式的元数据文件，作为 Snapshot 和 WAL 之间的关联桥梁
// 设计定位：指针类（pointer class），轻量级元数据管理
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
)

const (
	// CheckpointFilePattern Checkpoint 文件命名模式
	// 格式：checkpoint-{timestamp}-{sequence}.json
	CheckpointFilePattern = "checkpoint-%d-%04d.json"

	// CheckpointMagic Checkpoint 文件魔术字（JSON 字段）
	CheckpointMagic = "NexKV-Checkpoint"
)

// Checkpoint Checkpoint 结构（JSON 格式）
//
// 核心定位：关联 Snapshot 和 WAL，提供恢复起点
//
// JSON 示例：
//
//	{
//	  "magic": "NexKV-Checkpoint",
//	  "checkpoint_version": 5,
//	  "created_at": "2026-01-19T12:34:56Z",
//	  "snapshot_info": {
//	    "snapshot_file": "snapshot-1735689600-0005.snap",
//	    "snapshot_checksum": "abc123...",
//	    "snapshot_timestamp": 1735689600,
//	    "snapshot_sequence": 5
//	  },
//	  "wal_info": {
//	    "wal_start_file": "wal-1735689600-0001.bin",
//	    "wal_start_offset": 12345,
//	    "wal_end_file": "wal-1735689700-0002.bin",
//	    "wal_end_offset": 67890
//	  },
//	  "metadata": {
//	    "mvstore_version": 10,
//	    "entry_count": 1000,
//	    "last_entry_hlc": "1234567890.000000001"
//	  }
//	}
type Checkpoint struct {
	// FileName 文件名（不序列化到 JSON）
	FileName string `json:"-"`

	// Magic 魔术字，用于验证文件类型
	Magic string `json:"magic"`

	// CheckpointVersion Checkpoint 版本号（单调递增）
	CheckpointVersion int64 `json:"checkpoint_version"`

	// CreatedAt 创建时间（RFC3339 格式）
	CreatedAt time.Time `json:"created_at"`

	// SnapshotInfo Snapshot 文件信息
	SnapshotInfo *SnapshotInfoInCheckpoint `json:"snapshot_info"`

	// WalInfo WAL 文件信息（恢复起点）
	WalInfo *WalInfoInCheckpoint `json:"wal_info"`

	// Metadata 扩展元数据（KV 对）
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// SnapshotInfoInCheckpoint Snapshot 文件信息
type SnapshotInfoInCheckpoint struct {
	// SnapshotFile Snapshot 文件名
	SnapshotFile string `json:"snapshot_file"`

	// SnapshotChecksum SHA256 校验和（十六进制字符串）
	SnapshotChecksum string `json:"snapshot_checksum"`

	// SnapshotTimestamp Snapshot 创建时间戳（Unix 秒）
	SnapshotTimestamp int64 `json:"snapshot_timestamp"`

	// SnapshotSequence Snapshot 序列号
	SnapshotSequence int `json:"snapshot_sequence"`
}

// WalInfoInCheckpoint WAL 文件信息（恢复起点）
type WalInfoInCheckpoint struct {
	// WalStartFile 起始 WAL 文件名
	WalStartFile string `json:"wal_start_file"`

	// WalStartOffset 起始 WAL 文件内的偏移量
	WalStartOffset int64 `json:"wal_start_offset"`

	// WalEndFile 结束 WAL 文件名（当前写入的 WAL 文件）
	WalEndFile string `json:"wal_end_file,omitempty"`

	// WalEndOffset 结束 WAL 文件内的偏移量（当前写入位置）
	WalEndOffset int64 `json:"wal_end_offset,omitempty"`
}

// CheckpointManager Checkpoint 管理器
type CheckpointManager struct {
	checkpointDir string
}

// NewCheckpointManager 创建 Checkpoint 管理器
//
// 参数：
// - checkpointDir: Checkpoint 文件存储目录
//
// 返回 CheckpointManager 实例和错误信息
func NewCheckpointManager(checkpointDir string) (*CheckpointManager, error) {
	// 确保目录存在
	if err := os.MkdirAll(checkpointDir, 0755); err != nil {
		return nil, fmt.Errorf("创建 Checkpoint 目录失败: %w", err)
	}

	return &CheckpointManager{
		checkpointDir: checkpointDir,
	}, nil
}

// CreateCheckpoint 创建新的 Checkpoint 文件
//
// 参数：
// - checkpointVersion: Checkpoint 版本号
// - snapshotInfo: Snapshot 文件信息
// - walInfo: WAL 文件信息
// - metadata: 扩展元数据
//
// 返回 Checkpoint 和错误信息
func (m *CheckpointManager) CreateCheckpoint(
	checkpointVersion int64,
	snapshotInfo *SnapshotInfoInCheckpoint,
	walInfo *WalInfoInCheckpoint,
	metadata map[string]interface{},
) (*Checkpoint, error) {
	// 1. 构建 Checkpoint 结构
	checkpoint := &Checkpoint{
		Magic:             CheckpointMagic,
		CheckpointVersion: checkpointVersion,
		CreatedAt:         time.Now(),
		SnapshotInfo:      snapshotInfo,
		WalInfo:           walInfo,
		Metadata:          metadata,
	}

	// 2. 序列化为 JSON
	jsonData, err := json.MarshalIndent(checkpoint, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("序列化 Checkpoint 失败: %w", err)
	}

	// 3. 生成 Checkpoint 文件名
	timestamp := time.Now().Unix()
	sequence := m.getNextSequence(timestamp)
	fileName := fmt.Sprintf(CheckpointFilePattern, timestamp, sequence)
	filePath := filepath.Join(m.checkpointDir, fileName)

	// 4. 先写入临时文件
	tempPath := filePath + ".tmp"
	if err := os.WriteFile(tempPath, jsonData, 0644); err != nil {
		return nil, fmt.Errorf("写入 Checkpoint 临时文件失败: %w", err)
	}

	// 5. 原子重命名
	if err := os.Rename(tempPath, filePath); err != nil {
		_ = os.Remove(tempPath) // 清理临时文件，忽略错误
		return nil, fmt.Errorf("重命名 Checkpoint 文件失败: %w", err)
	}

	logging.Infof("Checkpoint 创建成功: %s (版本: %d)", fileName, checkpointVersion)

	// 设置文件名
	checkpoint.FileName = fileName

	return checkpoint, nil
}

// CreateCheckpointWithVersion 使用指定版本号创建 Checkpoint（用于全局序列号管理）
//
// 核心改进：
//   - 使用全局序列号生成器提供的版本号，不再自己生成序列号
//   - 确保 checkpoint_version ≡ snapshot_sequence
//
// 参数：
// - checkpointVersion: Checkpoint 版本号（由 SequenceGenerator 提供）
// - snapshotInfo: Snapshot 文件信息
// - walInfo: WAL 文件信息
// - metadata: 扩展元数据
//
// 返回 Checkpoint 和错误信息
func (m *CheckpointManager) CreateCheckpointWithVersion(
	checkpointVersion int64,
	snapshotInfo *SnapshotInfoInCheckpoint,
	walInfo *WalInfoInCheckpoint,
	metadata map[string]interface{},
) (*Checkpoint, error) {
	// 1. 构建 Checkpoint 结构
	checkpoint := &Checkpoint{
		Magic:             CheckpointMagic,
		CheckpointVersion: checkpointVersion,
		CreatedAt:         time.Now(),
		SnapshotInfo:      snapshotInfo,
		WalInfo:           walInfo,
		Metadata:          metadata,
	}

	// 2. 序列化为 JSON
	jsonData, err := json.MarshalIndent(checkpoint, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("序列化 Checkpoint 失败: %w", err)
	}

	// 3. 生成 Checkpoint 文件名（使用全局序列号）
	timestamp := time.Now().Unix()
	sequence := int(checkpointVersion) // 使用全局序列号
	fileName := fmt.Sprintf(CheckpointFilePattern, timestamp, sequence)
	filePath := filepath.Join(m.checkpointDir, fileName)

	// 4. 先写入临时文件
	tempPath := filePath + ".tmp"
	if err := os.WriteFile(tempPath, jsonData, 0644); err != nil {
		return nil, fmt.Errorf("写入 Checkpoint 临时文件失败: %w", err)
	}

	// 5. 原子重命名
	if err := os.Rename(tempPath, filePath); err != nil {
		_ = os.Remove(tempPath) // 清理临时文件，忽略错误
		return nil, fmt.Errorf("重命名 Checkpoint 文件失败: %w", err)
	}

	logging.Infof("Checkpoint 创建成功（全局序列号）: %s (版本: %d)", fileName, checkpointVersion)

	// 设置文件名
	checkpoint.FileName = fileName

	return checkpoint, nil
}

// LoadCheckpoint 加载指定的 Checkpoint 文件
//
// 参数：
// - fileName: Checkpoint 文件名
//
// 返回 Checkpoint 和错误信息
func (m *CheckpointManager) LoadCheckpoint(fileName string) (*Checkpoint, error) {
	filePath := filepath.Join(m.checkpointDir, fileName)

	// 1. 读取文件内容
	jsonData, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("读取 Checkpoint 文件失败: %w", err)
	}

	// 2. 反序列化 JSON
	var checkpoint Checkpoint
	if err := json.Unmarshal(jsonData, &checkpoint); err != nil {
		return nil, fmt.Errorf("反序列化 Checkpoint 失败: %w", err)
	}

	// 3. 验证魔术字
	if checkpoint.Magic != CheckpointMagic {
		return nil, fmt.Errorf("无效的 Checkpoint 魔术字: %s", checkpoint.Magic)
	}

	logging.Infof("Checkpoint 加载成功: %s (版本: %d)", fileName, checkpoint.CheckpointVersion)

	// 设置文件名
	checkpoint.FileName = fileName

	return &checkpoint, nil
}

// GetLatestCheckpoint 获取最新的 Checkpoint 文件
//
// 返回 Checkpoint 和错误信息
func (m *CheckpointManager) GetLatestCheckpoint() (*Checkpoint, error) {
	// 1. 列出所有 Checkpoint 文件
	entries, err := os.ReadDir(m.checkpointDir)
	if err != nil {
		return nil, fmt.Errorf("读取 Checkpoint 目录失败: %w", err)
	}

	// 2. 找到最新的 Checkpoint 文件
	var latestFile string
	var latestModTime time.Time

	for _, entry := range entries {
		if entry.IsDir() || !isCheckpointFile(entry.Name()) {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		if info.ModTime().After(latestModTime) {
			latestModTime = info.ModTime()
			latestFile = entry.Name()
		}
	}

	if latestFile == "" {
		// 没有找到 Checkpoint 文件，返回 nil（表示全新启动）
		return nil, nil
	}

	// 3. 加载最新的 Checkpoint
	return m.LoadCheckpoint(latestFile)
}

// DeleteCheckpoint 删除指定的 Checkpoint 文件
//
// 参数：
// - fileName: Checkpoint 文件名
//
// 返回错误信息
func (m *CheckpointManager) DeleteCheckpoint(fileName string) error {
	filePath := filepath.Join(m.checkpointDir, fileName)

	if err := os.Remove(filePath); err != nil {
		return fmt.Errorf("删除 Checkpoint 文件失败: %w", err)
	}

	logging.Infof("Checkpoint 删除成功: %s", fileName)

	return nil
}

// isCheckpointFile 判断是否为 Checkpoint 文件
func isCheckpointFile(fileName string) bool {
	base := filepath.Base(fileName)
	ext := filepath.Ext(base)
	if ext != ".json" {
		return false
	}
	// 文件名格式：checkpoint-{timestamp}-{sequence}.json
	// 其中 timestamp 和 sequence 都是数字，sequence 必须是4位
	nameWithoutExt := base[:len(base)-len(ext)]
	pattern := `^checkpoint-\d+-\d{4}$`
	matched, _ := regexp.MatchString(pattern, nameWithoutExt)
	return matched
}

// getNextSequence 获取下一个序列号
//
// 参数：
// - timestamp: 时间戳（Unix 秒）
//
// 返回序列号
func (m *CheckpointManager) getNextSequence(timestamp int64) int {
	// 1. 列出所有 Checkpoint 文件
	entries, err := os.ReadDir(m.checkpointDir)
	if err != nil {
		return 1 // 默认序列号
	}

	// 2. 查找相同时间戳的最大序列号
	maxSequence := 0
	pattern := regexp.MustCompile(`^checkpoint-(\d+)-(\d+)\.json$`)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		matches := pattern.FindStringSubmatch(entry.Name())
		if matches == nil {
			continue
		}

		var fileTimestamp int64
		var sequence int
		_, _ = fmt.Sscanf(matches[1], "%d", &fileTimestamp)
		_, _ = fmt.Sscanf(matches[2], "%d", &sequence)

		if fileTimestamp == timestamp && sequence > maxSequence {
			maxSequence = sequence
		}
	}

	return maxSequence + 1
}

// FormatCheckpointFileName 格式化 Checkpoint 文件名
//
// 参数：
// - timestamp: 时间戳（Unix 秒）
// - sequence: 序列号
//
// 返回文件名
func FormatCheckpointFileName(timestamp int64, sequence int) string {
	return fmt.Sprintf(CheckpointFilePattern, timestamp, sequence)
}
