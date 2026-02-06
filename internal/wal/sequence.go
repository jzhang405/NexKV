// Package store 全局序列号管理器实现
//
// 提供 Checkpoint 和 Snapshot 的全局序列号管理
// 序列号完全依托 Checkpoint 实现闭环持久化，无需单独文件
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// SequenceGenerator 全局序列号生成器
//
// 核心特性：
//   - 序列号完全依托 Checkpoint 持久化，无需单独文件
//   - 启动时从最新 Checkpoint 加载序列号
//   - 生成快照时原子分配序列号，失败自动回滚
//   - 保证 checkpoint_version ≡ snapshot_sequence
type SequenceGenerator struct {
	mu             sync.Mutex
	checkpointDir  string
	currentVersion uint64 // 当前全局序列号
}

// NewSequenceGenerator 创建序列号生成器
//
// 参数：
// - checkpointDir: Checkpoint 文件存储目录
//
// 返回 SequenceGenerator 实例和错误信息
func NewSequenceGenerator(checkpointDir string) (*SequenceGenerator, error) {
	// 确保目录存在
	if err := os.MkdirAll(checkpointDir, 0755); err != nil {
		return nil, types.NewStoreDirectoryCreationError(checkpointDir, err)
	}

	gen := &SequenceGenerator{
		checkpointDir:  checkpointDir,
		currentVersion: 0, // 默认从 0 开始
	}

	// 从最新 Checkpoint 加载序列号
	if err := gen.loadFromLatestCheckpoint(); err != nil {
		logging.Warnf("加载序列号失败，使用默认值 0: %v", err)
	}

	return gen, nil
}

// loadFromLatestCheckpoint 从最新 Checkpoint 加载序列号
//
// 实现闭环持久化：无需单独文件，完全依托 Checkpoint
func (g *SequenceGenerator) loadFromLatestCheckpoint() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	// 1. 创建 Checkpoint 管理器
	checkpointMgr, err := NewCheckpointManager(g.checkpointDir)
	if err != nil {
		return types.NewStoreCheckpointDirectoryReadFailedError(err)
	}

	// 2. 加载最新 Checkpoint
	checkpoint, err := checkpointMgr.GetLatestCheckpoint()
	if err != nil {
		return types.NewStoreCheckpointDirectoryReadFailedError(err)
	}

	// 3. 无历史 Checkpoint，使用默认值 0（新节点启动）
	if checkpoint == nil {
		logging.Infof("未找到历史 Checkpoint，序列号初始化为 0")
		g.currentVersion = 0
		return nil
	}

	// 4. 提取并验证序列号
	// 核心约束：checkpoint_version ≡ snapshot_sequence
	if checkpoint.CheckpointVersion <= 0 {
		return types.NewStoreCheckpointVersionInvalidError(checkpoint.CheckpointVersion)
	}

	if checkpoint.SnapshotInfo == nil {
		return types.NewStoreCheckpointMissingSnapshotInfoError()
	}

	snapshotSeq := uint64(checkpoint.SnapshotInfo.SnapshotSequence)
	if checkpoint.CheckpointVersion != int64(snapshotSeq) {
		return types.NewStoreSequenceMismatchError(checkpoint.CheckpointVersion, int64(checkpoint.SnapshotInfo.SnapshotSequence))
	}

	// 5. 赋值全局序列号（实现持久化加载）
	g.currentVersion = uint64(checkpoint.CheckpointVersion)

	logging.Infof("从 Checkpoint 加载序列号成功: version=%d, snapshot=%s",
		g.currentVersion, checkpoint.SnapshotInfo.SnapshotFile)

	return nil
}

// Next 获取下一个序列号（预分配，用于原子化生成）
//
// 返回下一个可用的序列号
func (g *SequenceGenerator) Next() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.currentVersion++
	return g.currentVersion
}

// Current 获取当前序列号
func (g *SequenceGenerator) Current() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.currentVersion
}

// Commit 提交序列号（生成成功后调用，确认序列号有效）
//
// 在实际实现中，由于序列号在 Next() 时已分配，
// 这里的 Commit 主要是确认机制，未来可用于持久化
func (g *SequenceGenerator) Commit(version uint64) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if version != g.currentVersion {
		return types.NewStoreSequenceVersionMismatchError(g.currentVersion, version)
	}

	// 序列号已通过 Checkpoint 持久化，无需额外操作
	logging.Infof("序列号提交成功: version=%d", version)
	return nil
}

// Rollback 回滚序列号（生成失败后调用，恢复到上一个版本）
func (g *SequenceGenerator) Rollback() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()

	// 回滚到上一个版本
	if g.currentVersion > 0 {
		g.currentVersion--
	}

	logging.Infof("序列号回滚完成: version=%d", g.currentVersion)
	return g.currentVersion
}

// ValidateCheckpoint 验证 Checkpoint 中的序列号一致性
//
// 这是核心验证逻辑，确保 checkpoint_version ≡ snapshot_sequence
//
// 参数：
// - checkpoint: 待验证的 Checkpoint
//
// 返回验证结果和错误信息
func (g *SequenceGenerator) ValidateCheckpoint(checkpoint *Checkpoint) error {
	if checkpoint == nil {
		return types.NewStoreCheckpointEmptyError()
	}

	// 验证 1: Checkpoint 版本号必须大于 0
	if checkpoint.CheckpointVersion <= 0 {
		return types.NewStoreCheckpointVersionInvalidError(checkpoint.CheckpointVersion)
	}

	// 验证 2: SnapshotInfo 不能为空
	if checkpoint.SnapshotInfo == nil {
		return types.NewStoreCheckpointMissingSnapshotInfoError()
	}

	// 验证 3: 核心约束 - checkpoint_version ≡ snapshot_sequence
	if checkpoint.CheckpointVersion != int64(checkpoint.SnapshotInfo.SnapshotSequence) {
		return types.NewStoreSequenceMismatchError(checkpoint.CheckpointVersion, int64(checkpoint.SnapshotInfo.SnapshotSequence))
	}

	// 验证 4: 快照文件名中的序列号必须一致
	snapshotFile := checkpoint.SnapshotInfo.SnapshotFile
	var snapshotSeqFromFile int
	_, err := fmt.Sscanf(snapshotFile, "snapshot-%d-%04d.snap",
		new(int64), &snapshotSeqFromFile)
	if err != nil {
		return types.NewStoreSnapshotFileNameParseFailedError(snapshotFile, err)
	}

	if checkpoint.SnapshotInfo.SnapshotSequence != snapshotSeqFromFile {
		return types.NewStoreSnapshotFileNameSequenceMismatchError(checkpoint.SnapshotInfo.SnapshotSequence, snapshotSeqFromFile)
	}

	return nil
}

// GetLatestCheckpointVersion 获取最新 Checkpoint 的版本号
//
// 用于恢复时快速获取当前序列号
func (g *SequenceGenerator) GetLatestCheckpointVersion() (uint64, error) {
	checkpointMgr, err := NewCheckpointManager(g.checkpointDir)
	if err != nil {
		return 0, types.NewStoreCheckpointDirectoryReadFailedError(err)
	}

	checkpoint, err := checkpointMgr.GetLatestCheckpoint()
	if err != nil {
		return 0, types.NewStoreCheckpointDirectoryReadFailedError(err)
	}

	if checkpoint == nil {
		return 0, nil // 无历史 Checkpoint
	}

	return uint64(checkpoint.CheckpointVersion), nil
}

// ========================================
// 辅助类型和常量
// ========================================

// CheckpointInfoForSeqGen 序列号生成器专用的 Checkpoint 信息
// 用于简化序列号管理逻辑
type CheckpointInfoForSeqGen struct {
	CheckpointVersion uint64 `json:"checkpoint_version"`
	SnapshotSequence  int    `json:"snapshot_sequence"`
	SnapshotFile      string `json:"snapshot_file"`
	SnapshotTimestamp int64  `json:"snapshot_timestamp"`
	CreateTimestamp   int64  `json:"create_timestamp"`
}

// LoadCheckpointInfo 加载 Checkpoint 信息（用于序列号加载）
//
// 参数：
// - checkpointDir: Checkpoint 目录
//
// 返回 Checkpoint 信息和错误信息
func LoadCheckpointInfo(checkpointDir string) (*CheckpointInfoForSeqGen, error) {
	// 1. 创建 Checkpoint 管理器
	checkpointMgr, err := NewCheckpointManager(checkpointDir)
	if err != nil {
		return nil, types.NewStoreCheckpointDirectoryReadFailedError(err)
	}

	// 2. 加载最新 Checkpoint
	checkpoint, err := checkpointMgr.GetLatestCheckpoint()
	if err != nil {
		return nil, types.NewStoreCheckpointDirectoryReadFailedError(err)
	}

	// 3. 无历史 Checkpoint
	if checkpoint == nil {
		return &CheckpointInfoForSeqGen{
			CheckpointVersion: 0,
			SnapshotSequence:  0,
			CreateTimestamp:   time.Now().UnixMilli(),
		}, nil
	}

	// 4. 提取关键信息
	return &CheckpointInfoForSeqGen{
		CheckpointVersion: uint64(checkpoint.CheckpointVersion),
		SnapshotSequence:  checkpoint.SnapshotInfo.SnapshotSequence,
		SnapshotFile:      checkpoint.SnapshotInfo.SnapshotFile,
		SnapshotTimestamp: checkpoint.SnapshotInfo.SnapshotTimestamp,
		CreateTimestamp:   checkpoint.CreatedAt.UnixMilli(),
	}, nil
}

// PersistCheckpointVersion 持久化 Checkpoint 版本号（内部方法）
//
// 注意：这是内部方法，仅供 RecoveryManager 使用
// 实际的 Checkpoint 创建应通过 RecoveryManager.CreateCheckpoint
//
// 参数：
// - checkpointDir: Checkpoint 目录
// - version: 版本号
// - snapshotFile: 快照文件名
// - snapshotSequence: 快照序列号
//
// 返回错误信息
func PersistCheckpointVersion(checkpointDir string, version uint64, snapshotFile string, snapshotSequence int) error {
	// 1. 创建临时 Checkpoint 信息
	checkpoint := &Checkpoint{
		Magic:             CheckpointMagic,
		CheckpointVersion: int64(version),
		CreatedAt:         time.Now(),
		SnapshotInfo: &SnapshotInfoInCheckpoint{
			SnapshotFile:      snapshotFile,
			SnapshotChecksum:  "", // 由 SnapshotManager 填充
			SnapshotTimestamp: time.Now().Unix(),
			SnapshotSequence:  snapshotSequence,
		},
		WalInfo: &WalInfoInCheckpoint{
			WalStartFile:   "",
			WalStartOffset: 0,
		},
		Metadata: map[string]any{
			"version": version,
		},
	}

	// 2. 序列化为 JSON
	jsonData, err := json.MarshalIndent(checkpoint, "", "  ")
	if err != nil {
		return types.NewStoreCheckpointSerializationFailedError(err)
	}

	// 3. 生成 Checkpoint 文件名
	timestamp := time.Now().Unix()
	fileName := fmt.Sprintf(CheckpointFilePattern, timestamp, snapshotSequence)
	filePath := filepath.Join(checkpointDir, fileName)

	// 4. 写入文件
	if err := os.WriteFile(filePath, jsonData, 0644); err != nil {
		return types.NewStoreCheckpointWriteFailedError(err)
	}

	logging.Infof("Checkpoint 版本号持久化成功: version=%d, file=%s", version, fileName)

	return nil
}
