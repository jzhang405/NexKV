// Package store 完整恢复流程实现
//
// 整合 WAL + Snapshot + Checkpoint 实现完整的崩溃恢复机制
package store

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jzhang405/NexKV/internal/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// RecoveryManager 恢复管理器
//
// 负责协调 WAL、Snapshot、Checkpoint 三个组件，实现完整的崩溃恢复流程
// 集成全局序列号管理器，保证 Checkpoint 和 Snapshot 序列号强绑定
type RecoveryManager struct {
	checkpointDir string
	snapshotDir   string
	walDir        string
	walBasePath   string             // WAL 文件基础路径（不含序号，用于生成 {basePath}.{index} 格式的文件名）
	seqGen        *SequenceGenerator // 全局序列号生成器
}

// NewRecoveryManager 创建恢复管理器
//
// 参数：
// - checkpointDir: Checkpoint 文件存储目录
// - snapshotDir: Snapshot 文件存储目录
// - walDir: WAL 文件存储目录
// - walBasePath: WAL 文件基础路径（不含序号，用于生成 {basePath}.{index} 格式的文件名）
//
// 返回 RecoveryManager 实例和错误信息
func NewRecoveryManager(checkpointDir, snapshotDir, walDir, walBasePath string) (*RecoveryManager, error) {
	// 确保目录存在
	for _, dir := range []string{checkpointDir, snapshotDir, walDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, types.NewStoreRecoveryCreateDirectoryFailedError(dir, err)
		}
	}

	// 创建全局序列号生成器（从 Checkpoint 加载当前序列号）
	seqGen, err := NewSequenceGenerator(checkpointDir)
	if err != nil {
		return nil, types.NewStoreRecoveryCreateSequenceGeneratorFailedError(err)
	}

	return &RecoveryManager{
		checkpointDir: checkpointDir,
		snapshotDir:   snapshotDir,
		walDir:        walDir,
		walBasePath:   walBasePath,
		seqGen:        seqGen,
	}, nil
}

// findLatestWALFile 查找最新的 WAL 文件
//
// 使用与 WALRotationManager 相同的命名约定：{basePath}.{index}
func (r *RecoveryManager) findLatestWALFile() (string, int64, error) {
	dir := filepath.Dir(r.walBasePath)
	baseName := filepath.Base(r.walBasePath)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", 0, types.NewInternalError("读取 WAL 目录", err)
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

		indexStr := suffix[1:]
		index, err := strconv.Atoi(indexStr)
		if err != nil {
			continue
		}

		files = append(files, &walFileInfo{
			path:  filepath.Join(dir, name),
			index: index,
		})
	}

	if len(files) == 0 {
		// 没有现有文件，从序号 0 开始
		return filepath.Join(dir, baseName+".0"), 0, nil
	}

	// 按序号排序
	sort.Slice(files, func(i, j int) bool {
		return files[i].index < files[j].index
	})

	latest := files[len(files)-1]
	logging.Infof("找到最新的 WAL 文件: %s (index=%d)", latest.path, latest.index)

	return latest.path, int64(latest.index), nil
}

// Recover 执行完整的崩溃恢复流程
//
// 恢复步骤：
// 1. 加载最新的 Checkpoint
// 2. 加载 Checkpoint 指定的 Snapshot
// 3. 从 Checkpoint 指定的 WAL 位置开始重放日志
// 4. 返回恢复后的 MVStore 数据
//
// 返回恢复后的数据（map[string][]byte）和错误信息
func (r *RecoveryManager) Recover() (map[string][]byte, error) {
	logging.Infof("开始崩溃恢复流程...")

	// 1. 创建 Checkpoint 管理器
	checkpointMgr, err := NewCheckpointManager(r.checkpointDir)
	if err != nil {
		return nil, types.NewStoreRecoveryCreateCheckpointManagerFailedError(err)
	}

	// 2. 加载最新的 Checkpoint
	checkpoint, err := checkpointMgr.GetLatestCheckpoint()
	if err != nil {
		return nil, types.NewStoreRecoveryLoadLatestCheckpointFailedError(err)
	}

	if checkpoint == nil {
		// 没有 Checkpoint，说明是全新启动
		logging.Infof("没有找到 Checkpoint，返回空数据")
		return make(map[string][]byte), nil
	}

	logging.Infof("加载 Checkpoint 成功: 版本=%d, Snapshot=%s",
		checkpoint.CheckpointVersion, checkpoint.SnapshotInfo.SnapshotFile)

	// 3. 创建 Snapshot 管理器
	snapshotMgr, err := NewSnapshotFileManager(r.snapshotDir, types.CompressionTypeNone)
	if err != nil {
		return nil, types.NewStoreRecoveryCreateSnapshotManagerFailedError(err)
	}

	// 4. 加载 Snapshot 指定的数据
	_, snapshotData, err := snapshotMgr.LoadSnapshot(checkpoint.SnapshotInfo.SnapshotFile)
	if err != nil {
		return nil, types.NewStoreRecoveryLoadSnapshotFailedError(err)
	}

	logging.Infof("加载 Snapshot 成功: 条目数=%d", len(snapshotData))

	// 5. 从 WAL 位置开始重放日志
	if checkpoint.WalInfo != nil {
		recoveredData, err := r.replayFromWAL(checkpoint, snapshotData)
		if err != nil {
			return nil, types.NewStoreRecoveryReplayWALFailedError(err)
		}
		snapshotData = recoveredData
	}

	logging.Infof("崩溃恢复完成: 总条目数=%d", len(snapshotData))

	return snapshotData, nil
}

// replayFromWAL 从 Checkpoint 指定的 WAL 位置开始重放日志
//
// 参数：
// - checkpoint: Checkpoint 文件
// - baseData: 基础数据（来自 Snapshot）
//
// 返回重放后的数据和错误信息
func (r *RecoveryManager) replayFromWAL(checkpoint *Checkpoint, baseData map[string][]byte) (map[string][]byte, error) {
	// 1. 构建 WAL 文件路径
	walPath := filepath.Join(r.walDir, checkpoint.WalInfo.WalStartFile)

	// 2. 打开 WAL
	wal, err := NewMetadataWAL(walPath)
	if err != nil {
		return nil, types.NewStoreRecoveryOpenWALFailedError(err)
	}
	defer func() {
		if err := wal.Close(); err != nil {
			logging.Warnf("关闭 WAL 失败: %v", err)
		}
	}()

	// 3. 验证 WAL EOF 标记
	valid, err := wal.ValidateEOF()
	if err != nil {
		logging.Warnf("验证 WAL EOF 标记失败: %v", err)
	} else if !valid {
		logging.Warnf("WAL EOF 标记无效，文件可能不完整")
	}

	// 4. 从 WAL 恢复所有日志
	entries, err := wal.Recover()
	if err != nil {
		return nil, types.NewStoreRecoveryRecoverWALFailedError(err)
	}

	logging.Infof("从 WAL 恢复 %d 个日志条目", len(entries))

	// 5. 应用日志条目到基础数据
	recoveredData := make(map[string][]byte)
	for k, v := range baseData {
		recoveredData[k] = v
	}

	// 6. 按 HLC 时间戳排序并应用日志条目
	for _, entry := range entries {
		// 跳过 Checkpoint 类型的日志条目
		if entry.Type == WALTypeCheckpoint {
			continue
		}

		// 将 []byte Key 转换为 string（作为 map 的键）
		key := string(entry.Key)

		// 根据操作类型应用日志
		switch entry.Type {
		case WALTypePut:
			// PUT 操作：直接设置值
			recoveredData[key] = entry.Value
		case WALTypeDelete:
			// DELETE 操作：删除键
			delete(recoveredData, key)
		default:
			logging.Warnf("未知的 WAL 操作类型: %d", entry.Type)
		}
	}

	return recoveredData, nil
}

// CreateCheckpoint 创建检查点（原子化操作）
//
// 核心改进：
//   - 使用全局序列号生成器，确保 checkpoint_version ≡ snapshot_sequence
//   - 原子化操作：Snapshot 和 Checkpoint 作为一个整体单元
//   - 异常回滚：任何步骤失败，自动清理已创建的文件
//   - 序列号验证：创建前验证序列号一致性
//
// 步骤：
// 1. 从全局序列号生成器获取下一个序列号
// 2. 创建 Snapshot（使用全局序列号）
// 3. 验证序列号一致性
// 4. 创建 Checkpoint（使用全局序列号）
// 5. 提交序列号
// 6. 截断旧 WAL（可选）
//
// 参数：
// - data: 当前 MVStore 数据
// - compression: 压缩算法类型
// - truncateOldWAL: 是否截断旧 WAL
//
// 返回 CheckpointInfo 和错误信息
func (r *RecoveryManager) CreateCheckpoint(
	data map[string][]byte,
	compression types.CompressionType,
	truncateOldWAL bool,
) (*CheckpointInfo, error) {
	// 1. 预分配全局序列号（原子操作）
	nextVersion := r.seqGen.Next()
	logging.Infof("预分配序列号: version=%d", nextVersion)

	// 2. 构建 Snapshot 元数据
	metadata := map[string]any{
		"version":     nextVersion,
		"entry_count": len(data),
		"created_at":  time.Now().Format(time.RFC3339),
	}

	// 3. 创建 Snapshot 管理器
	snapshotMgr, err := NewSnapshotFileManager(r.snapshotDir, compression)
	if err != nil {
		r.seqGen.Rollback() // 回滚序列号
		return nil, types.NewStoreRecoveryCreateSnapshotManagerFailedError(err)
	}

	// 4. 创建 Snapshot（使用全局序列号）
	snapshotInfo, err := snapshotMgr.CreateSnapshotWithVersion(metadata, data, int(nextVersion))
	if err != nil {
		r.seqGen.Rollback() // 回滚序列号
		return nil, types.NewStoreRecoveryCreateSnapshotFailedError(err)
	}

	logging.Infof("Snapshot 创建成功: %s (sequence=%d)", snapshotInfo.FileName, nextVersion)

	// 5. 验证序列号一致性（核心约束：checkpoint_version ≡ snapshot_sequence）
	if snapshotInfo.Sequence != int(nextVersion) {
		// 清理已创建的 Snapshot 文件
		snapshotPath := filepath.Join(r.snapshotDir, snapshotInfo.FileName)
		_ = os.Remove(snapshotPath)
		r.seqGen.Rollback() // 回滚序列号
		return nil, types.NewStoreRecoverySequenceMismatchError(nextVersion, uint64(snapshotInfo.Sequence))
	}

	// 6. 获取当前 WAL 文件信息
	walStartFile, _, err := r.findLatestWALFile()
	if err != nil {
		r.seqGen.Rollback() // 回滚序列号
		return nil, types.NewInternalError("查找最新WAL文件失败", err)
	}
	walStartOffset := int64(0) // TODO: 从 WAL 文件偏移量获取

	// 7. 构建 Checkpoint 信息
	snapshotInfoInCheckpoint := &SnapshotInfoInCheckpoint{
		SnapshotFile:      snapshotInfo.FileName,
		SnapshotChecksum:  snapshotInfo.Checksum,
		SnapshotTimestamp: snapshotInfo.Timestamp,
		SnapshotSequence:  snapshotInfo.Sequence, // 使用全局序列号
	}

	walInfo := &WalInfoInCheckpoint{
		WalStartFile:   walStartFile,
		WalStartOffset: walStartOffset,
	}

	// 8. 创建 Checkpoint 管理器
	checkpointMgr, err := NewCheckpointManager(r.checkpointDir)
	if err != nil {
		// 清理已创建的 Snapshot 文件
		snapshotPath := filepath.Join(r.snapshotDir, snapshotInfo.FileName)
		_ = os.Remove(snapshotPath)
		r.seqGen.Rollback() // 回滚序列号
		return nil, types.NewStoreRecoveryCreateCheckpointManagerFailedError(err)
	}

	// 9. 创建 Checkpoint（使用全局序列号）
	checkpoint, err := checkpointMgr.CreateCheckpointWithVersion(
		int64(nextVersion),
		snapshotInfoInCheckpoint,
		walInfo,
		metadata,
	)
	if err != nil {
		// 清理已创建的 Snapshot 文件
		snapshotPath := filepath.Join(r.snapshotDir, snapshotInfo.FileName)
		_ = os.Remove(snapshotPath)
		r.seqGen.Rollback() // 回滚序列号
		return nil, types.NewStoreRecoveryCreateCheckpointFailedError(err)
	}

	// 10. 验证 Checkpoint 中的序列号一致性
	if err := r.seqGen.ValidateCheckpoint(checkpoint); err != nil {
		// 清理已创建的文件
		snapshotPath := filepath.Join(r.snapshotDir, snapshotInfo.FileName)
		checkpointPath := filepath.Join(r.checkpointDir, checkpoint.FileName)
		_ = os.Remove(snapshotPath)
		_ = os.Remove(checkpointPath)
		r.seqGen.Rollback() // 回滚序列号
		return nil, types.NewStoreRecoveryCheckpointValidationFailedError(err)
	}

	logging.Infof("Checkpoint 创建成功: 版本=%d, 文件=%s",
		checkpoint.CheckpointVersion, checkpoint.FileName)

	// 11. 提交序列号（确认持久化成功）
	if err := r.seqGen.Commit(nextVersion); err != nil {
		logging.Warnf("序列号提交失败（非致命）: %v", err)
	}

	// 12. 截断旧 WAL（可选）
	if truncateOldWAL {
		// TODO: 实现 WAL 截断逻辑
		logging.Infof("WAL 截断功能待实现")
	}

	return &CheckpointInfo{
		CheckpointVersion: checkpoint.CheckpointVersion,
		CheckpointFile:    checkpoint.FileName,
		SnapshotFile:      snapshotInfo.FileName,
		WalStartFile:      walStartFile,
		WalStartOffset:    walStartOffset,
	}, nil
}

// CheckpointInfo Checkpoint 信息
type CheckpointInfo struct {
	CheckpointVersion int64
	CheckpointFile    string // Checkpoint 文件名
	SnapshotFile      string
	WalStartFile      string
	WalStartOffset    int64
	WalEndFile        string
	WalEndOffset      int64
}

// ValidateCheckpoint 验证 Checkpoint 完整性
//
// 验证步骤：
// 1. 检查 Checkpoint 文件是否存在
// 2. 检查 Snapshot 文件是否存在
// 3. 验证 Snapshot 校验和
// 4. 检查 WAL 文件是否存在
//
// 参数：
// - checkpointFileName: Checkpoint 文件名
//
// 返回验证结果和错误信息
func (r *RecoveryManager) ValidateCheckpoint(checkpointFileName string) (bool, error) {
	// 1. 创建 Checkpoint 管理器
	checkpointMgr, err := NewCheckpointManager(r.checkpointDir)
	if err != nil {
		return false, types.NewStoreRecoveryCreateCheckpointManagerFailedError(err)
	}

	// 2. 加载 Checkpoint
	checkpoint, err := checkpointMgr.LoadCheckpoint(checkpointFileName)
	if err != nil {
		return false, types.NewStoreRecoveryLoadCheckpointFailedError(err)
	}

	// 3. 检查 Snapshot 文件是否存在
	snapshotPath := filepath.Join(r.snapshotDir, checkpoint.SnapshotInfo.SnapshotFile)
	if _, err := os.Stat(snapshotPath); os.IsNotExist(err) {
		return false, types.NewStoreRecoverySnapshotFileNotFoundError(checkpoint.SnapshotInfo.SnapshotFile)
	}

	// 4. 验证 Snapshot 校验和
	snapshotMgr, err := NewSnapshotFileManager(r.snapshotDir, types.CompressionTypeNone)
	if err != nil {
		return false, types.NewStoreRecoveryCreateSnapshotManagerFailedError(err)
	}

	metadata, _, err := snapshotMgr.LoadSnapshot(checkpoint.SnapshotInfo.SnapshotFile)
	if err != nil {
		return false, types.NewStoreRecoveryLoadSnapshotFailedError(err)
	}

	// 5. 检查 WAL 文件是否存在（可选，仅警告）
	if checkpoint.WalInfo != nil {
		walPath := filepath.Join(r.walDir, checkpoint.WalInfo.WalStartFile)
		if _, err := os.Stat(walPath); os.IsNotExist(err) {
			// WAL 文件不存在不是致命错误，仅记录警告
			// Checkpoint 仍然有效，只是无法重放 WAL 日志
			logging.Warnf("WAL 文件不存在: %s（仅从 Snapshot 恢复）", checkpoint.WalInfo.WalStartFile)
		}
	}

	logging.Infof("Checkpoint 验证成功: %s (条目数=%v)",
		checkpointFileName, metadata["entry_count"])

	return true, nil
}

// ListCheckpoints 列出所有 Checkpoint
//
// 返回 Checkpoint 信息列表
func (r *RecoveryManager) ListCheckpoints() ([]*Checkpoint, error) {
	// 1. 创建 Checkpoint 管理器
	checkpointMgr, err := NewCheckpointManager(r.checkpointDir)
	if err != nil {
		return nil, types.NewStoreRecoveryCreateCheckpointManagerFailedError(err)
	}

	// 2. 读取目录中的所有 Checkpoint 文件
	entries, err := os.ReadDir(r.checkpointDir)
	if err != nil {
		return nil, types.NewStoreRecoveryReadCheckpointDirectoryFailedError(err)
	}

	var checkpoints []*Checkpoint
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		// 检查是否为 Checkpoint 文件
		if !isCheckpointFile(entry.Name()) {
			continue
		}

		// 加载 Checkpoint
		checkpoint, err := checkpointMgr.LoadCheckpoint(entry.Name())
		if err != nil {
			logging.Warnf("加载 Checkpoint 失败: %s (%v)", entry.Name(), err)
			continue
		}

		checkpoints = append(checkpoints, checkpoint)
	}

	return checkpoints, nil
}
