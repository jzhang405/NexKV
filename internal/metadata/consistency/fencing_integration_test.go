// Package consistency 提供 Fencing Token 一致性集成测试
//
// 集成测试覆盖：
//   - 脑裂场景测试：验证 Fencing Token 防护
//   - 2PC 超时恢复测试：验证 PreCommitWithTimeout 机制
//   - Leader 切换测试：验证 Term 递增和 Token 生成
//   - 持久化恢复测试：验证重启后 Term 恢复
package consistency

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jzhang405/NexKV/internal/clock"
	"github.com/jzhang405/NexKV/internal/metadata/kvstore"
)

// ==================== 脑裂场景测试 ====================

// TestFencing_SplitBrainScenario 测试脑裂场景
//
// 场景描述：
// 1. Leader (Term=100) 写入数据
// 2. 网络分区，node-2 以为自己成为 Leader (Term=99)
// 3. 分区恢复，node-2 尝试写入，应该被拒绝
func TestFencing_SplitBrainScenario(t *testing.T) {
	store := newFencingMockStore()
	termStore := NewTermStorage(store)
	fencingStore := NewFencingStore(termStore, store)

	// 模拟 Term=100 的 Leader 写入
	token100 := NewFencingToken(100, "leader-1")
	err := fencingStore.Write(context.Background(), kvstore.NamespaceCluster, "key1", "value1", token100)
	require.NoError(t, err, "Term=100 写入应该成功")

	// 模拟 Term=99 的旧 Leader 尝试写入（脑裂场景）
	token99 := NewFencingToken(99, "leader-2")
	err = fencingStore.Write(context.Background(), kvstore.NamespaceCluster, "key1", "value2", token99)
	require.ErrorIs(t, err, ErrStaleToken, "Term=99 写入应该被拒绝")

	// 验证当前 Token 仍然是 Term=100
	currentToken := fencingStore.GetCurrentToken()
	require.NotNil(t, currentToken)
	require.Equal(t, uint64(100), currentToken.Term)
}

// TestFencing_MultiplePartitions 测试多次分区场景
func TestFencing_MultiplePartitions(t *testing.T) {
	store := newFencingMockStore()
	termStore := NewTermStorage(store)
	fencingStore := NewFencingStore(termStore, store)

	// 模拟连续的 Term 推进
	for term := uint64(1); term <= 10; term++ {
		token := NewFencingToken(term, "leader")
		err := fencingStore.Write(context.Background(), kvstore.NamespaceCluster, "key1", term, token)
		require.NoError(t, err, "Term=%d 写入应该成功", term)
	}

	// 尝试用所有旧的 Term 写入，都应该失败
	for term := uint64(1); term <= 9; term++ {
		token := NewFencingToken(term, "old-leader")
		err := fencingStore.Write(context.Background(), kvstore.NamespaceCluster, "key1", "stale", token)
		require.ErrorIs(t, err, ErrStaleToken, "Term=%d 旧写入应该被拒绝", term)
	}
}

// TestFencing_LeaderManager_BecomeLeader 测试 Leader 选举和 Term 推进
func TestFencing_LeaderManager_BecomeLeader(t *testing.T) {
	store := newFencingMockStore()
	termStore := NewTermStorage(store)
	manager := NewLeaderManager(termStore, "node-1")

	// 初始化
	err := manager.Initialize(context.Background())
	require.NoError(t, err)

	// 成为 Leader
	token, err := manager.BecomeLeader(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(1), token.Term)
	require.True(t, manager.IsLeader())

	// 生成 Token
	genToken, err := manager.GenerateToken()
	require.NoError(t, err)
	require.Equal(t, uint64(1), genToken.Term)

	// 退位
	manager.StepDown()
	require.False(t, manager.IsLeader())

	// 再次成为 Leader，Term 应该递增
	token2, err := manager.BecomeLeader(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(2), token2.Term)
}

// ==================== 2PC 超时恢复测试 ====================

// Test2PC_PreCommitWithTimeout_Success 测试正常 2PC 流程
func Test2PC_PreCommitWithTimeout_Success(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)
	metadataKV := newMockMetadataKV()

	coordinator, err := NewTwoPCMerkleCoordinator(&TwoPCOptions{
		MetadataKV: metadataKV,
		MerkleTree: merkleTree,
		HLC:        hlc,
	})
	require.NoError(t, err)
	defer coordinator.Close()

	// 创建事务
	participants := []string{"node-1", "node-2"}
	tx, err := coordinator.BeginTransaction(participants)
	require.NoError(t, err)

	// 添加操作
	tx.AddOperation(kvstore.NamespaceNode, "node-001", []byte(`{"status":"active"}`), 1)

	// 使用 PreCommitWithTimeout
	ctx := context.Background()
	err = coordinator.PreCommitWithTimeout(ctx, tx, 5*time.Second)
	require.NoError(t, err, "本地模式 PreCommitWithTimeout 应该成功")

	// 提交
	err = coordinator.Commit(ctx, tx)
	require.NoError(t, err)
	require.Equal(t, TxStateCommitted, tx.State)
}

// Test2PC_CommitWithTimeout 测试 CommitWithTimeout
func Test2PC_CommitWithTimeout(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)
	metadataKV := newMockMetadataKV()

	coordinator, err := NewTwoPCMerkleCoordinator(&TwoPCOptions{
		MetadataKV: metadataKV,
		MerkleTree: merkleTree,
		HLC:        hlc,
	})
	require.NoError(t, err)
	defer coordinator.Close()

	participants := []string{"node-1"}
	tx, err := coordinator.BeginTransaction(participants)
	require.NoError(t, err)

	tx.AddOperation(kvstore.NamespaceNode, "node-001", []byte(`{"status":"active"}`), 1)

	ctx := context.Background()
	err = coordinator.PreCommit(ctx, tx)
	require.NoError(t, err)

	// 使用 CommitWithTimeout
	err = coordinator.CommitWithTimeout(ctx, tx, 3*time.Second)
	require.NoError(t, err)
	require.Equal(t, TxStateCommitted, tx.State)
}

// Test2PC_TimeoutCleanup 测试超时事务清理
func Test2PC_TimeoutCleanup(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)
	metadataKV := newMockMetadataKV()

	coordinator, err := NewTwoPCMerkleCoordinator(&TwoPCOptions{
		MetadataKV:     metadataKV,
		MerkleTree:     merkleTree,
		HLC:            hlc,
		DefaultTimeout: 100 * time.Millisecond, // 短超时
	})
	require.NoError(t, err)
	defer coordinator.Close()

	// 创建多个事务
	for i := 0; i < 3; i++ {
		tx, _ := coordinator.BeginTransaction([]string{"node-1"})
		tx.AddOperation(kvstore.NamespaceNode, "node-001", []byte("value"), 1)
		// 不提交，让事务超时
		_ = tx
	}

	// 等待超时
	time.Sleep(150 * time.Millisecond)

	// 清理超时事务
	cleaned := coordinator.CleanupTimeoutTransactions()
	require.GreaterOrEqual(t, cleaned, 1, "应该清理至少 1 个超时事务")
}

// ==================== Fencing Token 并发测试 ====================

// TestFencing_ConcurrentWrite 测试并发写入
func TestFencing_ConcurrentWrite(t *testing.T) {
	store := newFencingMockStore()
	termStore := NewTermStorage(store)
	fencingStore := NewFencingStore(termStore, store)

	var wg sync.WaitGroup
	successCount := 0
	failCount := 0
	var mu sync.Mutex

	// 并发使用相同 Term 写入
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token := NewFencingToken(100, "node-1")
			err := fencingStore.Write(context.Background(), kvstore.NamespaceCluster, "key1", "value1", token)

			mu.Lock()
			switch err {
			case nil:
				successCount++
			case ErrStaleToken:
				failCount++
			}
			mu.Unlock()
		}()
	}

	wg.Wait()

	// 验证：至少有一个成功
	require.GreaterOrEqual(t, successCount, 1, "应该至少有一个成功")
}

// ==================== Leader 切换测试 ====================

// TestLeader_Switch 测试 Leader 切换场景
func TestLeader_Switch(t *testing.T) {
	store := newFencingMockStore()
	termStore := NewTermStorage(store)

	// 创建两个 Leader Manager
	manager1 := NewLeaderManager(termStore, "node-1")
	manager2 := NewLeaderManager(termStore, "node-2")

	// 初始化
	_ = manager1.Initialize(context.Background())
	_ = manager2.Initialize(context.Background())

	// node-1 成为 Leader
	token1, err := manager1.BecomeLeader(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(1), token1.Term)

	// node-1 退位
	manager1.StepDown()

	// node-2 成为 Leader，Term 应该递增
	token2, err := manager2.BecomeLeader(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(2), token2.Term)

	// node-1 尝试用旧 Term 生成 Token，应该失败
	manager1.StepDown() // 确保不是 Leader
	manager1.mu.Lock()
	manager1.isLeader = false
	manager1.currentTerm = 1 // 模拟旧的 Term
	manager1.mu.Unlock()

	_, err = manager1.GenerateToken()
	require.ErrorIs(t, err, ErrTokenNotFromLeader, "非 Leader 不能生成 Token")
}

// ==================== 持久化恢复测试 ====================

// TestPersistence_Recovery 测试重启恢复
func TestPersistence_Recovery(t *testing.T) {
	store := newFencingMockStore()
	termStore := NewTermStorage(store)

	// 推进 Term 到 100
	for i := 0; i < 100; i++ {
		_, _ = termStore.AdvanceTerm(context.Background())
	}

	// 模拟重启：创建新的 TermStorage
	newTermStore := NewTermStorage(store)

	// 从持久化恢复 Term
	recoveredTerm, err := newTermStore.GetCurrentTerm(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(100), recoveredTerm, "应该恢复到 Term=100")

	// 验证防护仍然有效
	fencingStore := NewFencingStore(newTermStore, store)
	token99 := NewFencingToken(99, "old-node")

	// 先设置当前 Token
	_ = fencingStore.Write(context.Background(), kvstore.NamespaceCluster, "key1", "value1", NewFencingToken(100, "new-node"))

	// 尝试用旧 Term 写入
	err = fencingStore.Write(context.Background(), kvstore.NamespaceCluster, "key1", "value2", token99)
	require.ErrorIs(t, err, ErrStaleToken, "重启后旧 Token 应该被拒绝")
}

// ==================== 压力测试 ====================

// TestStress_ConcurrentTransactions 测试并发事务
func TestStress_ConcurrentTransactions(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)
	metadataKV := newMockMetadataKV()

	coordinator, err := NewTwoPCMerkleCoordinator(&TwoPCOptions{
		MetadataKV: metadataKV,
		MerkleTree: merkleTree,
		HLC:        hlc,
	})
	require.NoError(t, err)
	defer coordinator.Close()

	// 并发执行 50 个事务
	done := make(chan bool, 50)
	for i := 0; i < 50; i++ {
		go func(idx int) {
			tx, _ := coordinator.BeginTransaction([]string{"node-1"})
			tx.AddOperation(kvstore.NamespaceNode, "node-001", []byte("value"), 1)

			ctx := context.Background()
			_ = coordinator.PreCommit(ctx, tx)
			_ = coordinator.Commit(ctx, tx)
			done <- true
		}(i)
	}

	// 等待所有事务完成
	for i := 0; i < 50; i++ {
		<-done
	}
}

// ==================== Benchmark ====================

// BenchmarkFencing_Write 基准测试 Fencing 写入
func BenchmarkFencing_Write(b *testing.B) {
	store := newFencingMockStore()
	termStore := NewTermStorage(store)
	fencingStore := NewFencingStore(termStore, store)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		token := NewFencingToken(uint64(i+1), "node-1")
		_ = fencingStore.Write(context.Background(), kvstore.NamespaceCluster, "key1", "value1", token)
	}
}

// Benchmark2PC_PreCommitWithTimeout 基准测试带超时的 PreCommit
func Benchmark2PC_PreCommitWithTimeout(b *testing.B) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)
	metadataKV := newMockMetadataKV()

	coordinator, _ := NewTwoPCMerkleCoordinator(&TwoPCOptions{
		MetadataKV: metadataKV,
		MerkleTree: merkleTree,
		HLC:        hlc,
	})
	defer coordinator.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tx, _ := coordinator.BeginTransaction([]string{"node-1"})
		tx.AddOperation(kvstore.NamespaceNode, "node-001", []byte("value"), 1)
		_ = coordinator.PreCommitWithTimeout(context.Background(), tx, 5*time.Second)
		_ = coordinator.Commit(context.Background(), tx)
	}
}
