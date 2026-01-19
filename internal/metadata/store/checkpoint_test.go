// Package store Checkpoint 单元测试
//
// 测试 Checkpoint 管理功能
package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewCheckpointManager 测试创建 Checkpoint 管理器
func TestNewCheckpointManager(t *testing.T) {
	t.Run("创建管理器", func(t *testing.T) {
		tmpDir := t.TempDir()
		mgr, err := NewCheckpointManager(tmpDir)
		require.NoError(t, err)
		assert.NotNil(t, mgr)
		assert.Equal(t, tmpDir, mgr.checkpointDir)
	})

	t.Run("目录不存在时自动创建", func(t *testing.T) {
		tmpDir := t.TempDir()
		subDir := filepath.Join(tmpDir, "checkpoints")

		// 确保子目录不存在
		_, err := os.Stat(subDir)
		assert.True(t, os.IsNotExist(err))

		// 创建管理器应该自动创建目录
		mgr, err := NewCheckpointManager(subDir)
		require.NoError(t, err)
		assert.NotNil(t, mgr)

		// 验证目录已创建
		info, err := os.Stat(subDir)
		require.NoError(t, err)
		assert.True(t, info.IsDir())
	})
}

// TestCreateCheckpoint 测试创建 Checkpoint
func TestCreateCheckpoint(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := NewCheckpointManager(tmpDir)
	require.NoError(t, err)

	// 准备测试数据
	snapshotInfo := &SnapshotInfoInCheckpoint{
		SnapshotFile:      "snapshot-1735689600-0001.json",
		SnapshotChecksum:  "abc123",
		SnapshotTimestamp: 1735689600,
		SnapshotSequence:  1,
	}

	walInfo := &WalInfoInCheckpoint{
		WalStartFile:   "wal-0001.bin",
		WalStartOffset: 0,
	}

	metadata := map[string]interface{}{
		"version":     1,
		"entry_count": 100,
	}

	// 创建 Checkpoint
	checkpoint, err := mgr.CreateCheckpoint(1, snapshotInfo, walInfo, metadata)
	require.NoError(t, err)

	// 验证 Checkpoint
	assert.NotNil(t, checkpoint)
	assert.Equal(t, CheckpointMagic, checkpoint.Magic)
	assert.Equal(t, int64(1), checkpoint.CheckpointVersion)
	assert.NotNil(t, checkpoint.SnapshotInfo)
	assert.NotNil(t, checkpoint.WalInfo)
	assert.Equal(t, "snapshot-1735689600-0001.json", checkpoint.SnapshotInfo.SnapshotFile)

	// 验证文件已创建
	filePath := filepath.Join(tmpDir, checkpoint.FileName)
	_, err = os.Stat(filePath)
	require.NoError(t, err)
}

// TestLoadCheckpoint 测试加载 Checkpoint
func TestLoadCheckpoint(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := NewCheckpointManager(tmpDir)
	require.NoError(t, err)

	// 准备测试数据
	snapshotInfo := &SnapshotInfoInCheckpoint{
		SnapshotFile:      "snapshot-1735689600-0001.json",
		SnapshotChecksum:  "abc123",
		SnapshotTimestamp: 1735689600,
		SnapshotSequence:  1,
	}

	walInfo := &WalInfoInCheckpoint{
		WalStartFile:   "wal-0001.bin",
		WalStartOffset: 0,
	}

	// 创建 Checkpoint
	created, err := mgr.CreateCheckpoint(1, snapshotInfo, walInfo, nil)
	require.NoError(t, err)

	// 加载 Checkpoint
	loaded, err := mgr.LoadCheckpoint(created.FileName)
	require.NoError(t, err)

	// 验证加载的数据
	assert.Equal(t, created.Magic, loaded.Magic)
	assert.Equal(t, created.CheckpointVersion, loaded.CheckpointVersion)
	assert.Equal(t, created.SnapshotInfo.SnapshotFile, loaded.SnapshotInfo.SnapshotFile)
	assert.Equal(t, created.WalInfo.WalStartFile, loaded.WalInfo.WalStartFile)
}

// TestGetLatestCheckpoint 测试获取最新 Checkpoint
func TestGetLatestCheckpoint(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := NewCheckpointManager(tmpDir)
	require.NoError(t, err)

	t.Run("没有 Checkpoint 时返回 nil", func(t *testing.T) {
		latest, err := mgr.GetLatestCheckpoint()
		require.NoError(t, err)
		assert.Nil(t, latest)
	})

	t.Run("有多个 Checkpoint 时返回最新的", func(t *testing.T) {
		// 创建第一个 Checkpoint
		snapshotInfo1 := &SnapshotInfoInCheckpoint{
			SnapshotFile:      "snapshot-1735689600-0001.json",
			SnapshotChecksum:  "abc123",
			SnapshotTimestamp: 1735689600,
			SnapshotSequence:  1,
		}

		walInfo1 := &WalInfoInCheckpoint{
			WalStartFile:   "wal-0001.bin",
			WalStartOffset: 0,
		}

		_, err := mgr.CreateCheckpoint(1, snapshotInfo1, walInfo1, nil)
		require.NoError(t, err)

		// 等待一小段时间确保时间戳不同
		time.Sleep(10 * time.Millisecond)

		// 创建第二个 Checkpoint
		snapshotInfo2 := &SnapshotInfoInCheckpoint{
			SnapshotFile:      "snapshot-1735689700-0002.json",
			SnapshotChecksum:  "def456",
			SnapshotTimestamp: 1735689700,
			SnapshotSequence:  2,
		}

		_, err = mgr.CreateCheckpoint(2, snapshotInfo2, walInfo1, nil)
		require.NoError(t, err)

		// 获取最新 Checkpoint
		latest, err := mgr.GetLatestCheckpoint()
		require.NoError(t, err)
		assert.NotNil(t, latest)

		// 第二个 Checkpoint 应该是最新的（版本号更大）
		assert.Equal(t, int64(2), latest.CheckpointVersion)
		assert.Equal(t, "snapshot-1735689700-0002.json", latest.SnapshotInfo.SnapshotFile)
	})
}

// TestDeleteCheckpoint 测试删除 Checkpoint
func TestDeleteCheckpoint(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := NewCheckpointManager(tmpDir)
	require.NoError(t, err)

	// 创建 Checkpoint
	snapshotInfo := &SnapshotInfoInCheckpoint{
		SnapshotFile:      "snapshot-1735689600-0001.json",
		SnapshotChecksum:  "abc123",
		SnapshotTimestamp: 1735689600,
		SnapshotSequence:  1,
	}

	walInfo := &WalInfoInCheckpoint{
		WalStartFile:   "wal-0001.bin",
		WalStartOffset: 0,
	}

	checkpoint, err := mgr.CreateCheckpoint(1, snapshotInfo, walInfo, nil)
	require.NoError(t, err)

	// 验证文件存在
	filePath := filepath.Join(tmpDir, checkpoint.FileName)
	_, err = os.Stat(filePath)
	require.NoError(t, err)

	// 删除 Checkpoint
	err = mgr.DeleteCheckpoint(checkpoint.FileName)
	require.NoError(t, err)

	// 验证文件已删除
	_, err = os.Stat(filePath)
	assert.True(t, os.IsNotExist(err))
}

// TestIsCheckpointFile 测试 Checkpoint 文件识别
func TestIsCheckpointFile(t *testing.T) {
	t.Run("有效文件名", func(t *testing.T) {
		validFiles := []string{
			"checkpoint-1735689600-0001.json",
			"checkpoint-0-0001.json",
			"checkpoint-9999999999-9999.json",
		}

		for _, fileName := range validFiles {
			assert.True(t, isCheckpointFile(fileName), "应该识别为 Checkpoint 文件: %s", fileName)
		}
	})

	t.Run("无效文件名", func(t *testing.T) {
		invalidFiles := []string{
			"checkpoint-1735689600-0001.txt",
			"checkpoint-1735689600-001.json", // 序列号不是4位
			"snapshot-1735689600-0001.json",
			"checkpoint.json",
			"wast-1735689600-0001.json",
		}

		for _, fileName := range invalidFiles {
			assert.False(t, isCheckpointFile(fileName), "不应该识别为 Checkpoint 文件: %s", fileName)
		}
	})
}

// TestFormatCheckpointFileName 测试 Checkpoint 文件名格式化
func TestFormatCheckpointFileName(t *testing.T) {
	testCases := []struct {
		name      string
		timestamp int64
		sequence  int
		expected  string
	}{
		{"标准命名", 1735689600, 1, "checkpoint-1735689600-0001.json"},
		{"大序列号", 1735689600, 9999, "checkpoint-1735689600-9999.json"},
		{"时间戳0", 0, 1, "checkpoint-0-0001.json"},
		{"大时间戳", 9999999999, 1, "checkpoint-9999999999-0001.json"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := FormatCheckpointFileName(tc.timestamp, tc.sequence)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// TestCheckpointJSONSerialization 测试 Checkpoint JSON 序列化
func TestCheckpointJSONSerialization(t *testing.T) {
	// 准备测试数据
	checkpoint := &Checkpoint{
		FileName:          "checkpoint-1735689600-0001.json",
		Magic:             CheckpointMagic,
		CheckpointVersion: 1,
		CreatedAt:         time.Unix(1735689600, 0).UTC(),
		SnapshotInfo: &SnapshotInfoInCheckpoint{
			SnapshotFile:      "snapshot-1735689600-0001.json",
			SnapshotChecksum:  "abc123",
			SnapshotTimestamp: 1735689600,
			SnapshotSequence:  1,
		},
		WalInfo: &WalInfoInCheckpoint{
			WalStartFile:   "wal-0001.bin",
			WalStartOffset: 0,
		},
		Metadata: map[string]interface{}{
			"version":     1,
			"entry_count": 100,
		},
	}

	t.Run("序列化到 JSON", func(t *testing.T) {
		tmpDir := t.TempDir()
		mgr, err := NewCheckpointManager(tmpDir)
		require.NoError(t, err)

		// 创建 Checkpoint（内部会序列化到文件）
		created, err := mgr.CreateCheckpoint(
			checkpoint.CheckpointVersion,
			checkpoint.SnapshotInfo,
			checkpoint.WalInfo,
			checkpoint.Metadata,
		)
		require.NoError(t, err)

		// 验证文件内容
		filePath := filepath.Join(tmpDir, created.FileName)
		data, err := os.ReadFile(filePath)
		require.NoError(t, err)

		// 验证是有效的 JSON
		var jsonMap map[string]interface{}
		err = json.Unmarshal(data, &jsonMap)
		require.NoError(t, err)

		// 验证关键字段
		assert.Equal(t, CheckpointMagic, jsonMap["magic"])
		assert.Equal(t, float64(1), jsonMap["checkpoint_version"])
		assert.NotNil(t, jsonMap["snapshot_info"])
		assert.NotNil(t, jsonMap["wal_info"])
	})

	t.Run("从 JSON 反序列化", func(t *testing.T) {
		tmpDir := t.TempDir()
		mgr, err := NewCheckpointManager(tmpDir)
		require.NoError(t, err)

		// 创建 Checkpoint
		created, err := mgr.CreateCheckpoint(
			checkpoint.CheckpointVersion,
			checkpoint.SnapshotInfo,
			checkpoint.WalInfo,
			checkpoint.Metadata,
		)
		require.NoError(t, err)

		// 加载 Checkpoint（内部会从 JSON 反序列化）
		loaded, err := mgr.LoadCheckpoint(created.FileName)
		require.NoError(t, err)

		// 验证反序列化后的数据
		assert.Equal(t, checkpoint.Magic, loaded.Magic)
		assert.Equal(t, checkpoint.CheckpointVersion, loaded.CheckpointVersion)
		assert.Equal(t, checkpoint.SnapshotInfo.SnapshotFile, loaded.SnapshotInfo.SnapshotFile)
		assert.Equal(t, checkpoint.SnapshotInfo.SnapshotChecksum, loaded.SnapshotInfo.SnapshotChecksum)
		assert.Equal(t, checkpoint.WalInfo.WalStartFile, loaded.WalInfo.WalStartFile)
		assert.Equal(t, float64(1), loaded.Metadata["version"])
		assert.Equal(t, float64(100), loaded.Metadata["entry_count"])
	})
}

// TestCheckpointValidation 测试 Checkpoint 验证
func TestCheckpointValidation(t *testing.T) {
	t.Run("有效的 Checkpoint", func(t *testing.T) {
		tmpDir := t.TempDir()
		mgr, err := NewCheckpointManager(tmpDir)
		require.NoError(t, err)

		// 创建 Checkpoint
		snapshotInfo := &SnapshotInfoInCheckpoint{
			SnapshotFile:      "snapshot-1735689600-0001.json",
			SnapshotChecksum:  "abc123",
			SnapshotTimestamp: 1735689600,
			SnapshotSequence:  1,
		}

		walInfo := &WalInfoInCheckpoint{
			WalStartFile:   "wal-0001.bin",
			WalStartOffset: 0,
		}

		checkpoint, err := mgr.CreateCheckpoint(1, snapshotInfo, walInfo, nil)
		require.NoError(t, err)

		// 加载应该成功
		loaded, err := mgr.LoadCheckpoint(checkpoint.FileName)
		require.NoError(t, err)
		assert.NotNil(t, loaded)
	})

	t.Run("无效的 JSON 文件", func(t *testing.T) {
		tmpDir := t.TempDir()
		mgr, err := NewCheckpointManager(tmpDir)
		require.NoError(t, err)

		// 创建无效的 JSON 文件
		invalidFile := filepath.Join(tmpDir, "checkpoint-1735689600-0001.json")
		err = os.WriteFile(invalidFile, []byte("invalid json content"), 0644)
		require.NoError(t, err)

		// 加载应该失败
		_, err = mgr.LoadCheckpoint("checkpoint-1735689600-0001.json")
		assert.Error(t, err)
	})

	t.Run("缺少魔术字", func(t *testing.T) {
		tmpDir := t.TempDir()
		mgr, err := NewCheckpointManager(tmpDir)
		require.NoError(t, err)

		// 创建缺少魔术字的 JSON 文件
		invalidMagic := filepath.Join(tmpDir, "checkpoint-1735689600-0002.json")
		invalidContent := `{"checkpoint_version":1,"snapshot_info":{}}`
		err = os.WriteFile(invalidMagic, []byte(invalidContent), 0644)
		require.NoError(t, err)

		// 加载应该失败（魔术字验证）
		_, err = mgr.LoadCheckpoint("checkpoint-1735689600-0002.json")
		assert.Error(t, err)
	})
}
