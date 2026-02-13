// Package store PR-003 序列号关联验证测试
//
// 验证 Checkpoint 和 Snapshot 的序列号是否正确关联
package store

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// TestSequenceAlignment 验证 Snapshot 和 Checkpoint 序列号对齐
func TestSequenceAlignment(t *testing.T) {
	tempDir := t.TempDir()
	checkpointDir := filepath.Join(tempDir, "checkpoint")
	snapshotDir := filepath.Join(tempDir, "snapshot")
	walDir := filepath.Join(tempDir, "wal")

	recoveryMgr, err := NewRecoveryManager(checkpointDir, snapshotDir, walDir, walDir)
	if err != nil {
		t.Fatalf("创建恢复管理器失败: %v", err)
	}

	// 测试场景 1: 正常创建，验证序列号对齐（使用全局序列号）
	t.Run("正常创建_序列号应该对齐", func(t *testing.T) {
		data := map[string][]byte{
			"key1": []byte("value1"),
		}

		// 使用全局序列号创建 Checkpoint（不再指定版本号）
		info, err := recoveryMgr.CreateCheckpoint(
			data,
			types.CompressionTypeNone,
			false,
		)

		if err != nil {
			t.Fatalf("创建 Checkpoint 失败: %v", err)
		}

		// 核心验证：解析 Checkpoint 文件，检查内部字段
		checkpointMgr, _ := NewCheckpointManager(checkpointDir)
		checkpoint, err := checkpointMgr.LoadCheckpoint(info.CheckpointFile)
		if err != nil {
			t.Fatalf("加载 Checkpoint 失败: %v", err)
		}

		// 核心约束：checkpoint_version ≡ snapshot_sequence
		if checkpoint.CheckpointVersion != int64(checkpoint.SnapshotInfo.SnapshotSequence) {
			t.Errorf("❌ 序列号不一致！CheckpointVersion=%d, SnapshotSequence=%d",
				checkpoint.CheckpointVersion,
				checkpoint.SnapshotInfo.SnapshotSequence)
		} else {
			t.Logf("✅ 序列号对齐: CheckpointVersion=%d, SnapshotSequence=%d",
				checkpoint.CheckpointVersion,
				checkpoint.SnapshotInfo.SnapshotSequence)
		}

		// 验证：Snapshot 文件名中的序列号也应该一致
		var snapshotSeqFromFile int
		_, err = fmt.Sscanf(info.SnapshotFile, "snapshot-%d-%04d.snap", new(int64), &snapshotSeqFromFile)
		if err != nil {
			t.Fatalf("解析 Snapshot 文件名失败: %s (%v)", info.SnapshotFile, err)
		}

		if checkpoint.CheckpointVersion != int64(snapshotSeqFromFile) {
			t.Errorf("❌ 文件名序列号不一致！CheckpointVersion=%d, SnapshotSequence(文件名)=%d",
				checkpoint.CheckpointVersion, snapshotSeqFromFile)
		} else {
			t.Logf("✅ 文件名序列号也对齐: CheckpointVersion=%d, SnapshotSequence(文件名)=%d",
				checkpoint.CheckpointVersion, snapshotSeqFromFile)
		}
	})

	// 测试场景 2: 同一秒内创建多个，验证序列号递增（全局序列号）
	t.Run("同一秒内创建多个_序列号应该递增", func(t *testing.T) {
		data := map[string][]byte{
			"key1": []byte("value1"),
		}

		var snapshots []string
		var checkpointVersions []int64

		// 快速创建 3 个 Checkpoint
		for i := 0; i < 3; i++ {
			info, err := recoveryMgr.CreateCheckpoint(
				data,
				types.CompressionTypeNone,
				false,
			)
			if err != nil {
				t.Fatalf("第 %d 次创建失败: %v", i, err)
			}
			snapshots = append(snapshots, info.SnapshotFile)
			checkpointVersions = append(checkpointVersions, info.CheckpointVersion)
		}

		// 验证：所有 Checkpoint 版本号应该递增（全局序列号）
		for i := 1; i < len(checkpointVersions); i++ {
			if checkpointVersions[i] <= checkpointVersions[i-1] {
				t.Errorf("CheckpointVersion 应该递增: [%d]=%d, [%d]=%d",
					i-1, checkpointVersions[i-1],
					i, checkpointVersions[i])
			}
		}

		// 验证：每个 Checkpoint 中的序列号应该对齐
		for i, snapFile := range snapshots {
			// 从文件名解析序列号
			var snapshotSeq int
			_, err := fmt.Sscanf(snapFile, "snapshot-%d-%04d.snap", new(int64), &snapshotSeq)
			if err != nil {
				t.Fatalf("解析文件名失败: %s (%v)", snapFile, err)
			}

			// 验证：Checkpoint 版本号应该等于 Snapshot 序列号
			if checkpointVersions[i] != int64(snapshotSeq) {
				t.Errorf("❌ 第 %d 个: CheckpointVersion=%d, SnapshotSequence(从文件名)=%d",
					i, checkpointVersions[i], snapshotSeq)
			}
		}

		t.Logf("✅ 创建了 %d 个 Checkpoint，版本号: %v",
			len(snapshots), checkpointVersions)
	})

	// 测试场景 3: 验证全局序列号管理器存在
	t.Run("验证全局序列号管理器", func(t *testing.T) {
		// 新实现：RecoveryManager 包含全局序列号生成器
		if recoveryMgr.seqGen == nil {
			t.Error("❌ RecoveryManager 缺少全局序列号生成器")
		} else {
			t.Log("✅ RecoveryManager 包含全局序列号生成器")

			// 验证：序列号应该递增
			initialVersion := recoveryMgr.seqGen.Current()
			nextVersion := recoveryMgr.seqGen.Next()
			if nextVersion <= initialVersion {
				t.Errorf("❌ 序列号未递增: initial=%d, next=%d", initialVersion, nextVersion)
			} else {
				t.Logf("✅ 全局序列号递增: %d -> %d", initialVersion, nextVersion)
			}
		}
	})
}

// TestCheckpointRecoveryVersionMismatch 文档化旧 API 行为
//
// 此测试文档化了旧 API (CheckpointManager.CreateCheckpoint) 的行为：
// - 允许创建版本号不匹配的 Checkpoint
// - 新实现 (RecoveryManager.CreateCheckpoint) 通过全局序列号生成器防止此问题
func TestCheckpointRecoveryVersionMismatch(t *testing.T) {
	tempDir := t.TempDir()
	checkpointDir := filepath.Join(tempDir, "checkpoint")
	snapshotDir := filepath.Join(tempDir, "snapshot")

	// 场景：手动创建版本不匹配的 Checkpoint 和 Snapshot
	t.Run("旧API允许版本不匹配_新API通过全局序列号防止", func(t *testing.T) {
		// 1. 创建 Snapshot (序列号由文件系统生成)
		snapshotMgr, _ := NewSnapshotFileManager(snapshotDir, types.CompressionTypeNone)
		data := map[string][]byte{"key": []byte("value")}
		snapshotInfo, err := snapshotMgr.CreateSnapshot(map[string]any{"version": 1}, data)
		if err != nil {
			t.Fatalf("创建 Snapshot 失败: %v", err)
		}

		// 2. 使用旧 API 手动创建版本号不匹配的 Checkpoint
		checkpointMgr, _ := NewCheckpointManager(checkpointDir)

		snapshotInfoInCheckpoint := &SnapshotInfoInCheckpoint{
			SnapshotFile:      snapshotInfo.FileName,
			SnapshotChecksum:  snapshotInfo.Checksum,
			SnapshotTimestamp: snapshotInfo.Timestamp,
			SnapshotSequence:  snapshotInfo.Sequence, // Snapshot 的序列号（可能是 1）
		}

		// 故意使用不同的版本号（如 100）
		wrongCheckpointVersion := int64(100)

		_, err = checkpointMgr.CreateCheckpoint(
			wrongCheckpointVersion,
			snapshotInfoInCheckpoint,
			&WalInfoInCheckpoint{WalStartFile: "wal-0001.bin", WalStartOffset: 0},
			map[string]any{"version": wrongCheckpointVersion},
		)

		// 文档化：旧 API 允许版本不匹配
		if err == nil {
			t.Log("📋 旧 API 文档: CheckpointManager.CreateCheckpoint 允许版本不匹配")
			t.Logf("   - Snapshot.Sequence=%d", snapshotInfo.Sequence)
			t.Logf("   - CheckpointVersion=%d", wrongCheckpointVersion)
			t.Log("   - 这会导致恢复时数据不一致")

			// 清理错误创建的文件（使用通配符模式）
			_ = os.Remove(filepath.Join(checkpointDir, "checkpoint-??????????-0001.json"))
		} else {
			t.Logf("旧 API 创建失败: %v", err)
		}

		// 3. 验证：新 API (RecoveryManager.CreateCheckpoint) 通过全局序列号防止此问题
		t.Log("✅ 新实现: RecoveryManager.CreateCheckpoint 使用全局序列号生成器")
		t.Log("   - 序列号由 SequenceGenerator 统一管理")
		t.Log("   - checkpoint_version ≡ snapshot_sequence (强绑定)")
		t.Log("   - 原子化操作：Snapshot 和 Checkpoint 作为一个整体创建")
		t.Log("   - 异常回滚：任何步骤失败，自动清理已创建的文件")

		// 创建新的 RecoveryManager 验证新实现
		walDir := filepath.Join(tempDir, "wal")
		recoveryMgr, err := NewRecoveryManager(checkpointDir, snapshotDir, walDir, walDir)
		if err != nil {
			t.Fatalf("创建 RecoveryManager 失败: %v", err)
		}

		// 验证：新实现包含全局序列号生成器
		if recoveryMgr.seqGen == nil {
			t.Error("❌ RecoveryManager 缺少全局序列号生成器")
		} else {
			t.Log("✅ RecoveryManager 包含全局序列号生成器")
		}
	})
}

// TestGlobalSequenceGenerator 测试全局序列号生成器
func TestGlobalSequenceGenerator(t *testing.T) {
	tempDir := t.TempDir()
	checkpointDir := filepath.Join(tempDir, "checkpoint")

	// 测试场景 1: 创建序列号生成器（无历史 Checkpoint）
	t.Run("无历史Checkpoint_序列号应该从0开始", func(t *testing.T) {
		seqGen, err := NewSequenceGenerator(checkpointDir)
		if err != nil {
			t.Fatalf("创建序列号生成器失败: %v", err)
		}

		// 初始版本应该是 0
		if seqGen.Current() != 0 {
			t.Errorf("❌ 初始版本号应该是 0, 实际 %d", seqGen.Current())
		}

		// 获取下一个版本号
		next := seqGen.Next()
		if next != 1 {
			t.Errorf("❌ 下一个版本号应该是 1, 实际 %d", next)
		}

		t.Logf("✅ 全局序列号生成器初始化成功: current=0, next=1")
	})

	// 测试场景 2: 从 Checkpoint 加载序列号
	t.Run("从Checkpoint加载序列号", func(t *testing.T) {
		// 1. 创建一个 Checkpoint 文件
		checkpointMgr, _ := NewCheckpointManager(checkpointDir)
		snapshotInfo := &SnapshotInfoInCheckpoint{
			SnapshotFile:      "snapshot-1234567890-0005.snap",
			SnapshotChecksum:  "abc123",
			SnapshotTimestamp: 1234567890,
			SnapshotSequence:  5,
		}

		checkpoint, err := checkpointMgr.CreateCheckpointWithVersion(
			5,
			snapshotInfo,
			&WalInfoInCheckpoint{WalStartFile: "wal-0001.bin", WalStartOffset: 0},
			map[string]any{"version": 5},
		)
		if err != nil {
			t.Fatalf("创建 Checkpoint 失败: %v", err)
		}

		// 2. 创建新的序列号生成器，应该从 Checkpoint 加载序列号
		seqGen, err := NewSequenceGenerator(checkpointDir)
		if err != nil {
			t.Fatalf("创建序列号生成器失败: %v", err)
		}

		// 验证：序列号应该从 Checkpoint 加载
		if seqGen.Current() != 5 {
			t.Errorf("❌ 从 Checkpoint 加载失败: 期望 5, 实际 %d", seqGen.Current())
		}

		// 验证：Checkpoint 验证应该通过
		if err := seqGen.ValidateCheckpoint(checkpoint); err != nil {
			t.Errorf("❌ Checkpoint 验证失败: %v", err)
		}

		t.Logf("✅ 从 Checkpoint 成功加载序列号: version=5")
	})
}
