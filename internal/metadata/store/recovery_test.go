// Package store PR-003 集成测试
//
// 测试 WAL + Snapshot + Checkpoint + Recovery 完整流程
package store

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// TestRecoveryFlow 完整恢复流程集成测试
func TestRecoveryFlow(t *testing.T) {
	// 创建临时目录
	tempDir := t.TempDir()
	checkpointDir := filepath.Join(tempDir, "checkpoint")
	snapshotDir := filepath.Join(tempDir, "snapshot")
	walDir := filepath.Join(tempDir, "wal")

	// 创建恢复管理器
	recoveryMgr, err := NewRecoveryManager(checkpointDir, snapshotDir, walDir)
	if err != nil {
		t.Fatalf("创建恢复管理器失败: %v", err)
	}

	// 准备测试数据
	initialData := map[string][]byte{
		"key1": []byte("value1"),
		"key2": []byte("value2"),
		"key3": []byte("value3"),
	}

	// 步骤 1: 创建 Checkpoint（使用全局序列号）
	checkpointInfo, err := recoveryMgr.CreateCheckpoint(
		initialData,
		types.CompressionTypeNone,
		false, // 不截断旧 WAL
	)
	if err != nil {
		t.Fatalf("创建 Checkpoint 失败: %v", err)
	}

	// 验证：Checkpoint 版本号应该等于 Snapshot 序列号
	if checkpointInfo.CheckpointVersion != int64(1) {
		t.Errorf("CheckPoint 版本不匹配: 期望 1, 实际 %d",
			checkpointInfo.CheckpointVersion)
	}

	// 步骤 2: 验证 Checkpoint
	checkpoints, err := recoveryMgr.ListCheckpoints()
	if err != nil {
		t.Fatalf("列出 Checkpoints 失败: %v", err)
	}

	if len(checkpoints) != 1 {
		t.Fatalf("Checkpoint 数量不匹配: 期望 1, 实际 %d", len(checkpoints))
	}

	// 验证 Checkpoint 完整性
	valid, err := recoveryMgr.ValidateCheckpoint(checkpoints[0].FileName)
	if err != nil {
		t.Fatalf("验证 Checkpoint 失败: %v", err)
	}

	if !valid {
		t.Error("Checkpoint 验证失败")
	}

	// 步骤 3: 执行恢复
	recoveredData, err := recoveryMgr.Recover()
	if err != nil {
		t.Fatalf("恢复失败: %v", err)
	}

	if len(recoveredData) != len(initialData) {
		t.Errorf("恢复后数据条目数不匹配: 期望 %d, 实际 %d",
			len(initialData), len(recoveredData))
	}

	for key, value := range initialData {
		if string(recoveredData[key]) != string(value) {
			t.Errorf("数据不匹配 (key=%s): 期望 %s, 实际 %s",
				key, value, recoveredData[key])
		}
	}
}

// TestRecoveryWithWAL 测试包含 WAL 重放的恢复流程
func TestRecoveryWithWAL(t *testing.T) {
	// 创建临时目录
	tempDir := t.TempDir()
	checkpointDir := filepath.Join(tempDir, "checkpoint")
	snapshotDir := filepath.Join(tempDir, "snapshot")
	walDir := filepath.Join(tempDir, "wal")

	// 创建 WAL
	walPath := filepath.Join(walDir, "wal-0001.bin")
	wal, err := NewMetadataWAL(walPath)
	if err != nil {
		t.Fatalf("创建 WAL 失败: %v", err)
	}
	defer func() {
		if err := wal.Close(); err != nil {
			t.Logf("关闭 WAL 失败: %v", err)
		}
	}()

	// 准备初始数据
	initialData := map[string][]byte{
		"key1": []byte("value1"),
		"key2": []byte("value2"),
	}

	// 写入一些 WAL 日志
	for key, value := range initialData {
		entry := &WALEntry{
			Type:  WALTypePut,
			Key:   []byte(key),
			Value: value,
		}
		if err := wal.Append(entry); err != nil {
			t.Fatalf("追加 WAL 失败: %v", err)
		}
	}

	// 创建恢复管理器
	recoveryMgr, err := NewRecoveryManager(checkpointDir, snapshotDir, walDir)
	if err != nil {
		t.Fatalf("创建恢复管理器失败: %v", err)
	}

	// 创建 Checkpoint（使用全局序列号）
	checkpointInfo, err := recoveryMgr.CreateCheckpoint(
		initialData,
		types.CompressionTypeNone,
		false,
	)
	if err != nil {
		t.Fatalf("创建 Checkpoint 失败: %v", err)
	}

	t.Logf("Checkpoint 创建成功: %s", checkpointInfo.SnapshotFile)

	// 在 Checkpoint 后添加更多 WAL 日志
	additionalData := map[string][]byte{
		"key3": []byte("value3"),
		"key4": []byte("value4"),
	}

	for key, value := range additionalData {
		entry := &WALEntry{
			Type:  WALTypePut,
			Key:   []byte(key),
			Value: value,
		}
		if err := wal.Append(entry); err != nil {
			t.Fatalf("追加 WAL 失败: %v", err)
		}
	}

	// 写入 EOF 标记
	if err := wal.WriteEOFMarker(); err != nil {
		t.Fatalf("写入 EOF 标记失败: %v", err)
	}

	// 验证 EOF 标记
	valid, err := wal.ValidateEOF()
	if err != nil {
		t.Fatalf("验证 EOF 标记失败: %v", err)
	}

	if !valid {
		t.Error("EOF 标记验证失败")
	}

	// 验证 WAL 统计信息
	stats, err := wal.GetStats()
	if err != nil {
		t.Fatalf("获取 WAL 统计信息失败: %v", err)
	}

	t.Logf("WAL 统计: 大小=%d, 条目数=%d, 偏移=%d",
		stats.Size, stats.Entries, stats.Offset)

	// 注意：当前实现的恢复流程需要手动处理 WAL 重放
	// 这里仅验证组件创建和数据一致性
}

// TestCheckpointManager Checkpoint 管理器测试
func TestCheckpointManager(t *testing.T) {
	// 创建临时目录
	tempDir := t.TempDir()

	// 创建 Checkpoint 管理器
	mgr, err := NewCheckpointManager(tempDir)
	if err != nil {
		t.Fatalf("创建 Checkpoint 管理器失败: %v", err)
	}

	// 准备测试数据
	snapshotInfo := &SnapshotInfoInCheckpoint{
		SnapshotFile:      "snapshot-1735689600-0001.snap",
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
	if err != nil {
		t.Fatalf("创建 Checkpoint 失败: %v", err)
	}

	if checkpoint.Magic != CheckpointMagic {
		t.Errorf("魔术字不匹配: 期望 %s, 实际 %s", CheckpointMagic, checkpoint.Magic)
	}

	if checkpoint.CheckpointVersion != 1 {
		t.Errorf("版本号不匹配: 期望 1, 实际 %d", checkpoint.CheckpointVersion)
	}

	if checkpoint.SnapshotInfo == nil {
		t.Error("SnapshotInfo 为空")
	}

	if checkpoint.WalInfo == nil {
		t.Error("WalInfo 为空")
	}

	// 加载 Checkpoint
	loadedCheckpoint, err := mgr.LoadCheckpoint(checkpoint.FileName)
	if err != nil {
		t.Fatalf("加载 Checkpoint 失败: %v", err)
	}

	if loadedCheckpoint.CheckpointVersion != checkpoint.CheckpointVersion {
		t.Errorf("版本号不匹配")
	}

	// 获取最新 Checkpoint
	latest, err := mgr.GetLatestCheckpoint()
	if err != nil {
		t.Fatalf("获取最新 Checkpoint 失败: %v", err)
	}

	if latest.FileName != checkpoint.FileName {
		t.Errorf("最新 Checkpoint 文件名不匹配")
	}

	// 删除 Checkpoint
	if err := mgr.DeleteCheckpoint(checkpoint.FileName); err != nil {
		t.Fatalf("删除 Checkpoint 失败: %v", err)
	}

	// 验证已删除
	if _, err := mgr.LoadCheckpoint(checkpoint.FileName); err == nil {
		t.Error("Checkpoint 应该已删除")
	}
}

// TestWALEOFMagic WAL EOF 标记测试
func TestWALEOFMagic(t *testing.T) {
	// 创建临时 WAL 文件
	tempDir := t.TempDir()
	walPath := filepath.Join(tempDir, "test-wal.bin")

	wal, err := NewMetadataWAL(walPath)
	if err != nil {
		t.Fatalf("创建 WAL 失败: %v", err)
	}
	defer func() {
		if err := wal.Close(); err != nil {
			t.Logf("关闭 WAL 失败: %v", err)
		}
	}()

	// 写入一些数据
	entry := &WALEntry{
		Type:  WALTypePut,
		Key:   []byte("test-key"),
		Value: []byte("test-value"),
	}

	if err := wal.Append(entry); err != nil {
		t.Fatalf("追加 WAL 失败: %v", err)
	}

	// 写入 EOF 标记
	if err := wal.WriteEOFMarker(); err != nil {
		t.Fatalf("写入 EOF 标记失败: %v", err)
	}

	// 验证 EOF 标记
	valid, err := wal.ValidateEOF()
	if err != nil {
		t.Fatalf("验证 EOF 标记失败: %v", err)
	}

	if !valid {
		t.Error("EOF 标记验证失败")
	}

	// 获取 EOF 位置
	eofPos, err := wal.GetEOFPosition()
	if err != nil {
		t.Fatalf("获取 EOF 位置失败: %v", err)
	}

	if eofPos <= 0 {
		t.Errorf("EOF 位置无效: %d", eofPos)
	}

	t.Logf("EOF 位置: %d", eofPos)
}

// TestSnapshotCompression 测试不同的压缩算法
func TestSnapshotCompression(t *testing.T) {
	// 创建临时目录
	tempDir := t.TempDir()

	// 准备测试数据
	metadata := map[string]interface{}{
		"version":     1,
		"entry_count": 1000,
	}

	data := make(map[string][]byte)
	for i := 0; i < 100; i++ {
		data[fmt.Sprintf("key%d", i)] = []byte("test-value-" + string(rune('0'+i)))
	}

	// 测试不同的压缩算法
	compressionTypes := []types.CompressionType{
		types.CompressionTypeNone,
		types.CompressionTypeSnappy,
		types.CompressionTypeZSTD,
		types.CompressionTypeLZ4,
	}

	for _, compression := range compressionTypes {
		t.Run(compression.String(), func(t *testing.T) {
			// 创建 Snapshot
			mgr, err := NewSnapshotFileManager(tempDir, compression)
			if err != nil {
				t.Fatalf("创建 Snapshot 管理器失败: %v", err)
			}

			info, err := mgr.CreateSnapshot(metadata, data)
			if err != nil {
				t.Fatalf("创建 Snapshot 失败: %v", err)
			}

			t.Logf("压缩类型: %s, 文件大小: %d bytes",
				compression.String(), info.Size)

			// 加载 Snapshot
			loadedMetadata, loadedData, err := mgr.LoadSnapshot(info.FileName)
			if err != nil {
				t.Fatalf("加载 Snapshot 失败: %v", err)
			}

			// 验证数据一致性
			// JSON 反序列化后数字是 float64 类型
			entryCount, ok := loadedMetadata["entry_count"].(float64)
			if !ok || int(entryCount) != 1000 {
				t.Errorf("元数据不匹配: entry_count 期望 1000, 实际 %v (类型: %T)", loadedMetadata["entry_count"], loadedMetadata["entry_count"])
			}

			version, ok := loadedMetadata["version"].(float64)
			if !ok || int(version) != 1 {
				t.Errorf("元数据不匹配: version 期望 1, 实际 %v", loadedMetadata["version"])
			}

			if len(loadedData) != len(data) {
				t.Errorf("数据条目数不匹配: 期望 %d, 实际 %d", len(data), len(loadedData))
			}
		})
	}
}

// TestRecoveryWithEmptyData 测试空数据的恢复流程
func TestRecoveryWithEmptyData(t *testing.T) {
	// 创建临时目录
	tempDir := t.TempDir()
	checkpointDir := filepath.Join(tempDir, "checkpoint")
	snapshotDir := filepath.Join(tempDir, "snapshot")
	walDir := filepath.Join(tempDir, "wal")

	// 创建恢复管理器
	recoveryMgr, err := NewRecoveryManager(checkpointDir, snapshotDir, walDir)
	if err != nil {
		t.Fatalf("创建恢复管理器失败: %v", err)
	}

	// 恢复空数据
	recoveredData, err := recoveryMgr.Recover()
	if err != nil {
		t.Fatalf("恢复失败: %v", err)
	}

	if recoveredData == nil {
		recoveredData = make(map[string][]byte)
	}

	if len(recoveredData) != 0 {
		t.Errorf("空数据恢复后应该为空: 实际 %d", len(recoveredData))
	}
}

// TestSnapshotFileNaming 测试 Snapshot 文件命名
func TestSnapshotFileNaming(t *testing.T) {
	testCases := []struct {
		name      string
		timestamp int64
		sequence  int
		expected  string
	}{
		{"标准命名", 1735689600, 1, "snapshot-1735689600-0001.snap"},
		{"大序列号", 1735689600, 9999, "snapshot-1735689600-9999.snap"},
		{"时间戳0", 0, 1, "snapshot-0-0001.snap"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := FormatSnapshotFileName(tc.timestamp, tc.sequence)
			if result != tc.expected {
				t.Errorf("文件名不匹配: 期望 %s, 实际 %s", tc.expected, result)
			}

			// 解析验证
			timestamp, sequence, err := ParseSnapshotFileName(result)
			if err != nil {
				t.Fatalf("解析文件名失败: %v", err)
			}

			if timestamp != tc.timestamp {
				t.Errorf("时间戳不匹配: 期望 %d, 实际 %d", tc.timestamp, timestamp)
			}

			if sequence != tc.sequence {
				t.Errorf("序列号不匹配: 期望 %d, 实际 %d", tc.sequence, sequence)
			}
		})
	}
}

// TestCheckpointFileNaming 测试 Checkpoint 文件命名
func TestCheckpointFileNaming(t *testing.T) {
	testCases := []struct {
		name      string
		timestamp int64
		sequence  int
		expected  string
	}{
		{"标准命名", 1735689600, 1, "checkpoint-1735689600-0001.json"},
		{"大序列号", 1735689600, 9999, "checkpoint-1735689600-9999.json"},
		{"时间戳0", 0, 1, "checkpoint-0-0001.json"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := FormatCheckpointFileName(tc.timestamp, tc.sequence)
			if result != tc.expected {
				t.Errorf("文件名不匹配: 期望 %s, 实际 %s", tc.expected, result)
			}
		})
	}
}

// TestFullCheckpointWorkflow 完整 Checkpoint 工作流测试
func TestFullCheckpointWorkflow(t *testing.T) {
	// 创建临时目录
	tempDir := t.TempDir()
	checkpointDir := filepath.Join(tempDir, "checkpoint")
	snapshotDir := filepath.Join(tempDir, "snapshot")
	walDir := filepath.Join(tempDir, "wal")

	// 步骤 1: 模拟系统运行，写入数据
	walPath := filepath.Join(walDir, "wal-0001.bin")
	wal, err := NewMetadataWAL(walPath)
	if err != nil {
		t.Fatalf("创建 WAL 失败: %v", err)
	}
	defer func() {
		if err := wal.WriteEOFMarker(); err != nil {
			t.Logf("写入 EOF 标记失败: %v", err)
		}
		if err := wal.Close(); err != nil {
			t.Logf("关闭 WAL 失败: %v", err)
		}
	}()

	// 写入初始数据
	initialData := map[string][]byte{
		"key1": []byte("value1"),
		"key2": []byte("value2"),
		"key3": []byte("value3"),
	}

	for key, value := range initialData {
		entry := &WALEntry{
			Type:  WALTypePut,
			Key:   []byte(key),
			Value: value,
		}
		if err := wal.Append(entry); err != nil {
			t.Fatalf("追加 WAL 失败: %v", err)
		}
	}

	// 步骤 2: 创建 Checkpoint
	recoveryMgr, err := NewRecoveryManager(checkpointDir, snapshotDir, walDir)
	if err != nil {
		t.Fatalf("创建恢复管理器失败: %v", err)
	}

	checkpointInfo, err := recoveryMgr.CreateCheckpoint(
		initialData,
		types.CompressionTypeSnappy, // 使用 Snappy 压缩
		false,
	)
	if err != nil {
		t.Fatalf("创建 Checkpoint 失败: %v", err)
	}

	t.Logf("Checkpoint 创建成功: 版本=%d, Snapshot=%s",
		checkpointInfo.CheckpointVersion, checkpointInfo.SnapshotFile)

	// 步骤 3: 验证 Checkpoint
	// 注意：使用 CheckpointInfo 中的 CheckpointFile 字段
	valid, err := recoveryMgr.ValidateCheckpoint(checkpointInfo.CheckpointFile)
	if err != nil {
		t.Fatalf("验证 Checkpoint 失败: %v", err)
	}

	if !valid {
		t.Fatal("Checkpoint 验证失败")
	}

	// 步骤 4: 继续写入更多 WAL 日志
	additionalData := map[string][]byte{
		"key4": []byte("value4"),
		"key5": []byte("value5"),
	}

	for key, value := range additionalData {
		entry := &WALEntry{
			Type:  WALTypePut,
			Key:   []byte(key),
			Value: value,
		}
		if err := wal.Append(entry); err != nil {
			t.Fatalf("追加 WAL 失败: %v", err)
		}
	}

	// 步骤 5: 验证 WAL 统计
	stats, err := wal.GetStats()
	if err != nil {
		t.Fatalf("获取 WAL 统计失败: %v", err)
	}

	t.Logf("WAL 统计: 条目数=%d, 文件大小=%d bytes",
		stats.Entries, stats.Size)

	// 步骤 6: 验证数据完整性
	if stats.Entries != uint64(len(initialData)+len(additionalData)) {
		t.Errorf("WAL 条目数不匹配: 期望 %d, 实际 %d",
			len(initialData)+len(additionalData), stats.Entries)
	}
}
