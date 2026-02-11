// Copyright 2025 The NexKV Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package consistency

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/clock"
	"github.com/jzhang405/NexKV/internal/metadata/kvstore"
	"github.com/stretchr/testify/require"
)

// mockStore 模拟 MVStore
type mockStore struct {
	data map[string][]byte
}

func newMockStore() *mockStore {
	return &mockStore{
		data: make(map[string][]byte),
	}
}

func (m *mockStore) Put(key string, value []byte) error {
	m.data[key] = value
	return nil
}

func (m *mockStore) Get(key string) ([]byte, error) {
	if value, ok := m.data[key]; ok {
		return value, nil
	}
	return nil, kvstore.ErrKeyNotFound
}

func (m *mockStore) GetVersion(key string, hlcTS *clock.HLC) ([]byte, error) {
	return m.Get(key)
}

func (m *mockStore) Delete(key string) error {
	delete(m.data, key)
	return nil
}

func (m *mockStore) Exists(key string) (bool, error) {
	_, ok := m.data[key]
	return ok, nil
}

func (m *mockStore) ListPrefix(prefix string, offset, limit int) ([]string, error) {
	var keys []string
	for k := range m.data {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			keys = append(keys, k)
		}
	}
	return keys, nil
}

func (m *mockStore) Close() error {
	return nil
}

// mockMetadataKV 模拟 MetadataKV
type mockMetadataKV struct {
	store *mockStore
}

func newMockMetadataKV() *mockMetadataKV {
	return &mockMetadataKV{
		store: newMockStore(),
	}
}

func (m *mockMetadataKV) Put(ctx context.Context, ns, key string, value any) error {
	return nil
}

func (m *mockMetadataKV) Get(ctx context.Context, ns, key string, value any) error {
	return nil
}

func (m *mockMetadataKV) GetVersion(ctx context.Context, ns, key string, hlcTS *clock.HLC, value any) error {
	return nil
}

func (m *mockMetadataKV) Delete(ctx context.Context, ns, key string) error {
	return nil
}

func (m *mockMetadataKV) Exists(ctx context.Context, ns, key string) (bool, error) {
	return true, nil
}

func (m *mockMetadataKV) ListPrefix(ctx context.Context, ns, prefix string) ([]string, error) {
	return []string{}, nil
}

func (m *mockMetadataKV) Close() error {
	return nil
}

func (m *mockMetadataKV) PutRaw(ctx context.Context, ns, key string, data []byte) error {
	fullKey := kvstore.BuildKey(ns, key)
	return m.store.Put(fullKey, data)
}

func (m *mockMetadataKV) GetRaw(ctx context.Context, ns, key string) ([]byte, error) {
	fullKey := kvstore.BuildKey(ns, key)
	return m.store.Get(fullKey)
}

func (m *mockMetadataKV) BatchGetRaw(ctx context.Context, ns string, keys []string) (map[string][]byte, error) {
	return make(map[string][]byte), nil
}

// TestNewTwoPCMerkleCoordinator 测试创建 2PC 协调器
func TestNewTwoPCMerkleCoordinator(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)
	metadataKV := newMockMetadataKV()

	coordinator, err := NewTwoPCMerkleCoordinator(&TwoPCOptions{
		MetadataKV:     metadataKV,
		MerkleTree:     merkleTree,
		HLC:            hlc,
		DefaultTimeout: 5 * time.Second,
	})

	require.NoError(t, err)
	require.NotNil(t, coordinator)
	require.Equal(t, 5*time.Second, coordinator.defaultTimeout)
}

// TestNewTwoPCMerkleCoordinator_Errors 测试创建协调器的错误情况
func TestNewTwoPCMerkleCoordinator_Errors(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)

	tests := []struct {
		name    string
		opts    *TwoPCOptions
		wantErr string
	}{
		{
			name:    "nil options",
			opts:    nil,
			wantErr: "options cannot be nil",
		},
		{
			name: "nil MetadataKV",
			opts: &TwoPCOptions{
				MerkleTree: merkleTree,
				HLC:        hlc,
			},
			wantErr: "MetadataKV cannot be nil",
		},
		{
			name: "nil MerkleTree",
			opts: &TwoPCOptions{
				MetadataKV: newMockMetadataKV(),
				HLC:        hlc,
			},
			wantErr: "MerkleTree cannot be nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewTwoPCMerkleCoordinator(tt.opts)
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// TestTwoPCTransaction_State 状态转换测试
func TestTwoPCTransaction_State(t *testing.T) {
	tx := NewTwoPCTransaction("tx-001", []string{"node-1", "node-2"}, 5*time.Second)

	// 初始状态
	require.Equal(t, TxStateInit, tx.State)
	require.Equal(t, "Init", tx.State.String())

	// PreCommit 状态
	tx.State = TxStatePreCommit
	require.Equal(t, TxStatePreCommit, tx.State)
	require.Equal(t, "PreCommit", tx.State.String())

	// Commit 状态
	tx.State = TxStateCommitted
	require.Equal(t, TxStateCommitted, tx.State)
	require.Equal(t, "Committed", tx.State.String())

	// Rollback 状态
	tx.State = TxStateRolledBack
	require.Equal(t, TxStateRolledBack, tx.State)
	require.Equal(t, "RolledBack", tx.State.String())

	// Timeout 状态
	tx.State = TxStateTimeout
	require.Equal(t, TxStateTimeout, tx.State)
	require.Equal(t, "Timeout", tx.State.String())
}

// TestTwoPCTransaction_AddOperation 测试添加操作
func TestTwoPCTransaction_AddOperation(t *testing.T) {
	tx := NewTwoPCTransaction("tx-001", []string{"node-1"}, 5*time.Second)

	value := []byte(`{"status": "active"}`)
	tx.AddOperation(kvstore.NamespaceNode, "node-001", value, 1)

	require.Len(t, tx.Operations, 1)
	require.Equal(t, kvstore.NamespaceNode, tx.Operations[0].NS)
	require.Equal(t, "node-001", tx.Operations[0].Key)
	require.Equal(t, value, tx.Operations[0].Value)
	require.Equal(t, uint64(1), tx.Operations[0].Version)
}

// TestTwoPCTransaction_HasAllAcks 测试 ACK 检查
func TestTwoPCTransaction_HasAllAcks(t *testing.T) {
	participants := []string{"node-1", "node-2", "node-3"}
	tx := NewTwoPCTransaction("tx-001", participants, 5*time.Second)

	// 初始状态，没有 ACK
	require.False(t, tx.HasAllAcks())

	// 部分 ACK
	tx.Acks["node-1"] = true
	require.False(t, tx.HasAllAcks())

	// 全部 ACK
	tx.Acks["node-2"] = true
	tx.Acks["node-3"] = true
	require.True(t, tx.HasAllAcks())
}

// TestTwoPCTransaction_IsTimedOut 测试超时检查
func TestTwoPCTransaction_IsTimedOut(t *testing.T) {
	// 短超时事务
	tx := NewTwoPCTransaction("tx-001", []string{"node-1"}, 1*time.Millisecond)
	require.False(t, tx.IsTimedOut())

	// 等待超时
	time.Sleep(10 * time.Millisecond)
	require.True(t, tx.IsTimedOut())

	// 已提交的事务不会超时
	tx.State = TxStateCommitted
	require.False(t, tx.IsTimedOut())
}

// TestTwoPCMerkleCoordinator_BeginTransaction 测试开始事务
func TestTwoPCMerkleCoordinator_BeginTransaction(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)
	metadataKV := newMockMetadataKV()

	coordinator, _ := NewTwoPCMerkleCoordinator(&TwoPCOptions{
		MetadataKV: metadataKV,
		MerkleTree: merkleTree,
		HLC:        hlc,
	})

	participants := []string{"node-1", "node-2", "node-3"}
	tx, err := coordinator.BeginTransaction(participants)

	require.NoError(t, err)
	require.NotNil(t, tx)
	require.NotEmpty(t, tx.TxID)
	require.Equal(t, TxStateInit, tx.State)
	require.Equal(t, participants, tx.Participants)
	require.Equal(t, 3, tx.Quorum) // 2PC 需要全部确认
}

// TestTwoPCMerkleCoordinator_PreCommit 测试 PreCommit 阶段
func TestTwoPCMerkleCoordinator_PreCommit(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)
	metadataKV := newMockMetadataKV()

	coordinator, _ := NewTwoPCMerkleCoordinator(&TwoPCOptions{
		MetadataKV: metadataKV,
		MerkleTree: merkleTree,
		HLC:        hlc,
	})

	participants := []string{"node-1", "node-2"}
	tx, _ := coordinator.BeginTransaction(participants)

	// 添加操作
	value := []byte(`{"status": "active"}`)
	tx.AddOperation(kvstore.NamespaceNode, "node-001", value, 1)

	// PreCommit
	ctx := context.Background()
	err := coordinator.PreCommit(ctx, tx)

	require.NoError(t, err)
	require.Equal(t, TxStatePreCommit, tx.State)
	require.NotZero(t, tx.PreCommitTime)
}

// TestTwoPCMerkleCoordinator_Commit 测试 Commit 阶段
func TestTwoPCMerkleCoordinator_Commit(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)
	metadataKV := newMockMetadataKV()

	coordinator, _ := NewTwoPCMerkleCoordinator(&TwoPCOptions{
		MetadataKV: metadataKV,
		MerkleTree: merkleTree,
		HLC:        hlc,
	})

	participants := []string{"node-1", "node-2"}
	tx, _ := coordinator.BeginTransaction(participants)

	// 添加操作
	value := []byte(`{"status": "active"}`)
	tx.AddOperation(kvstore.NamespaceNode, "node-001", value, 1)

	// PreCommit
	ctx := context.Background()
	_ = coordinator.PreCommit(ctx, tx)

	// 本地模式：手动设置所有 ACK（模拟网络响应）
	tx.Acks["node-1"] = true
	tx.Acks["node-2"] = true

	// Commit
	err := coordinator.Commit(ctx, tx)

	require.NoError(t, err)
	require.Equal(t, TxStateCommitted, tx.State)
	require.NotZero(t, tx.CommitTime)

	// 验证数据已写入
	storedValue, err := metadataKV.GetRaw(ctx, kvstore.NamespaceNode, "node-001")
	require.NoError(t, err)
	require.Equal(t, value, storedValue)

	// 验证 Merkle Tree 已更新
	hash, err := merkleTree.GetKeyHash(kvstore.NamespaceNode, "node-001")
	require.NoError(t, err)
	require.NotEmpty(t, hash)
}

// TestTwoPCMerkleCoordinator_Rollback 测试 Rollback
func TestTwoPCMerkleCoordinator_Rollback(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)
	metadataKV := newMockMetadataKV()

	coordinator, _ := NewTwoPCMerkleCoordinator(&TwoPCOptions{
		MetadataKV: metadataKV,
		MerkleTree: merkleTree,
		HLC:        hlc,
	})

	participants := []string{"node-1"}
	tx, _ := coordinator.BeginTransaction(participants)

	// 添加操作
	value := []byte(`{"status": "active"}`)
	tx.AddOperation(kvstore.NamespaceNode, "node-001", value, 1)

	// PreCommit
	ctx := context.Background()
	_ = coordinator.PreCommit(ctx, tx)

	// Rollback
	err := coordinator.Rollback(ctx, tx)

	require.NoError(t, err)
	require.Equal(t, TxStateRolledBack, tx.State)
}

// TestTwoPCMerkleCoordinator_Commit_WithoutAllAcks 测试未收到全部 ACK 时提交失败
func TestTwoPCMerkleCoordinator_Commit_WithoutAllAcks(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)
	metadataKV := newMockMetadataKV()

	coordinator, _ := NewTwoPCMerkleCoordinator(&TwoPCOptions{
		MetadataKV: metadataKV,
		MerkleTree: merkleTree,
		HLC:        hlc,
	})

	participants := []string{"node-1", "node-2", "node-3"}
	tx, _ := coordinator.BeginTransaction(participants)

	// 添加操作
	value := []byte(`{"status": "active"}`)
	tx.AddOperation(kvstore.NamespaceNode, "node-001", value, 1)

	// PreCommit
	ctx := context.Background()
	_ = coordinator.PreCommit(ctx, tx)

	// 模拟只收到部分 ACK（模拟网络问题）
	tx.Acks = map[string]bool{
		"node-1": true,
		"node-2": true,
		// node-3 没有确认
	}

	// Commit 应该失败
	err := coordinator.Commit(ctx, tx)

	require.Error(t, err)
	require.Contains(t, err.Error(), "not all ACKs received")
	require.Equal(t, TxStateRolledBack, tx.State)
}

// TestTwoPCMerkleCoordinator_GetTransaction 测试获取事务
func TestTwoPCMerkleCoordinator_GetTransaction(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)
	metadataKV := newMockMetadataKV()

	coordinator, _ := NewTwoPCMerkleCoordinator(&TwoPCOptions{
		MetadataKV: metadataKV,
		MerkleTree: merkleTree,
		HLC:        hlc,
	})

	participants := []string{"node-1"}
	tx, _ := coordinator.BeginTransaction(participants)

	// 获取事务
	retrievedTx, err := coordinator.GetTransaction(tx.TxID)

	require.NoError(t, err)
	require.Equal(t, tx.TxID, retrievedTx.TxID)

	// 获取不存在的事务
	_, err = coordinator.GetTransaction("non-existent")
	require.Error(t, err)
	require.Contains(t, err.Error(), "transaction not found")
}

// TestTwoPCMerkleCoordinator_CleanupTimeoutTransactions 测试清理超时事务
func TestTwoPCMerkleCoordinator_CleanupTimeoutTransactions(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)
	metadataKV := newMockMetadataKV()

	coordinator, _ := NewTwoPCMerkleCoordinator(&TwoPCOptions{
		MetadataKV:     metadataKV,
		MerkleTree:     merkleTree,
		HLC:            hlc,
		DefaultTimeout: 1 * time.Millisecond, // 极短超时
	})

	participants := []string{"node-1"}
	tx1, _ := coordinator.BeginTransaction(participants)
	tx2, _ := coordinator.BeginTransaction(participants)

	// 等待超时
	time.Sleep(10 * time.Millisecond)

	// 清理超时事务
	cleaned := coordinator.CleanupTimeoutTransactions()

	require.Equal(t, 2, cleaned)

	// 验证事务已被清理
	_, err := coordinator.GetTransaction(tx1.TxID)
	require.Error(t, err)

	_, err = coordinator.GetTransaction(tx2.TxID)
	require.Error(t, err)
}

// TestTwoPCMerkleCoordinator_Close 测试关闭协调器
func TestTwoPCMerkleCoordinator_Close(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)
	metadataKV := newMockMetadataKV()

	coordinator, _ := NewTwoPCMerkleCoordinator(&TwoPCOptions{
		MetadataKV: metadataKV,
		MerkleTree: merkleTree,
		HLC:        hlc,
	})

	// 开始事务
	participants := []string{"node-1"}
	tx, _ := coordinator.BeginTransaction(participants)

	// 关闭协调器
	err := coordinator.Close()

	require.NoError(t, err)

	// 验证事务已被回滚
	_, err = coordinator.GetTransaction(tx.TxID)
	require.Error(t, err)

	// 验证协调器已关闭
	require.True(t, coordinator.closed)

	// 再次关闭应该是安全的
	err = coordinator.Close()
	require.NoError(t, err)
}

// BenchmarkTwoPCMerkleCoordinator_PreCommit 性能测试：PreCommit 阶段
func BenchmarkTwoPCMerkleCoordinator_PreCommit(b *testing.B) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)
	metadataKV := newMockMetadataKV()

	coordinator, _ := NewTwoPCMerkleCoordinator(&TwoPCOptions{
		MetadataKV: metadataKV,
		MerkleTree: merkleTree,
		HLC:        hlc,
	})

	ctx := context.Background()
	value := []byte(`{"status": "active"}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		participants := []string{"node-1", "node-2"}
		tx, _ := coordinator.BeginTransaction(participants)
		tx.AddOperation(kvstore.NamespaceNode, "node-001", value, 1)
		_ = coordinator.PreCommit(ctx, tx)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		participants := []string{"node-1", "node-2"}
		tx, _ := coordinator.BeginTransaction(participants)
		tx.AddOperation(kvstore.NamespaceNode, "node-001", value, 1)
		b.StopTimer()
		_ = coordinator.Commit(ctx, tx)
		b.StartTimer()
	}
}

// BenchmarkTwoPCMerkleCoordinator_Commit 性能测试：Commit 阶段
func BenchmarkTwoPCMerkleCoordinator_Commit(b *testing.B) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)
	metadataKV := newMockMetadataKV()

	coordinator, _ := NewTwoPCMerkleCoordinator(&TwoPCOptions{
		MetadataKV: metadataKV,
		MerkleTree: merkleTree,
		HLC:        hlc,
	})

	ctx := context.Background()
	value := []byte(`{"status": "active"}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		participants := []string{"node-1", "node-2"}
		tx, _ := coordinator.BeginTransaction(participants)
		tx.AddOperation(kvstore.NamespaceNode, "node-001", value, 1)
		b.StopTimer()
		_ = coordinator.Commit(ctx, tx)
		b.StartTimer()
	}
}

// ==================== 网络通信测试 ====================

// mockTransport 模拟 Transport 接口
type mockTransport struct {
	mu       sync.Mutex
	messages map[string][]byte // nodeID -> 消息记录
	handler  func(string, []byte)
}

func newMockTransport() *mockTransport {
	return &mockTransport{
		messages: make(map[string][]byte),
	}
}

// Send 实现 Transport.Send
func (m *mockTransport) Send(nodeID string, msg []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 记录发送的消息
	m.messages[nodeID] = msg

	// 如果有处理器，模拟异步接收（直接调用处理器）
	if m.handler != nil {
		// 模拟 ACK 响应（在实际场景中，这是通过网络接收的）
		// 这里直接调用 handler 来模拟响应
		go m.handler(nodeID, msg)
	}

	return nil
}

// Receive 实现 Transport.Receive
func (m *mockTransport) Receive(handler func(nodeID string, msg []byte)) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.handler = handler
	return nil
}

// Close 实现 Transport.Close
func (m *mockTransport) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handler = nil
	return nil
}

// getSentMessages 获取发送的消息（测试验证用）
func (m *mockTransport) getSentMessages(nodeID string) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.messages[nodeID]
}

// TestTwoPCMerkleCoordinator_PreCommit_Network_SendMessages 测试 PreCommit 网络发送
func TestTwoPCMerkleCoordinator_PreCommit_Network_SendMessages(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)
	metadataKV := newMockMetadataKV()
	mockTransport := newMockTransport()

	coordinator, err := NewTwoPCMerkleCoordinatorWithTransport(
		metadataKV,
		merkleTree,
		hlc,
		mockTransport,
		"coordinator-001",
	)
	require.NoError(t, err)

	participants := []string{"node-1", "node-2"}
	tx, err := coordinator.BeginTransaction(participants)
	require.NoError(t, err)

	// 添加操作
	value := []byte(`{"status": "active"}`)
	tx.AddOperation(kvstore.NamespaceNode, "node-001", value, 1)

	// PreCommit
	ctx := context.Background()
	err = coordinator.PreCommit(ctx, tx)
	require.NoError(t, err)
	require.Equal(t, TxStatePreCommit, tx.State)

	// 验证消息已发送到所有参与者
	msg1 := mockTransport.getSentMessages("node-1")
	require.NotNil(t, msg1, "应该发送消息到 node-1")

	msg2 := mockTransport.getSentMessages("node-2")
	require.NotNil(t, msg2, "应该发送消息到 node-2")
}

// TestTwoPCMerkleCoordinator_PreCommit_Network_Timeout 测试网络超时场景
func TestTwoPCMerkleCoordinator_PreCommit_Network_Timeout(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)
	metadataKV := newMockMetadataKV()
	mockTransport := newMockTransport()

	coordinator, err := NewTwoPCMerkleCoordinatorWithTransport(
		metadataKV,
		merkleTree,
		hlc,
		mockTransport,
		"coordinator-001",
	)
	require.NoError(t, err)

	// 设置极短超时
	participants := []string{"node-1"}
	tx, err := coordinator.BeginTransaction(participants)
	require.NoError(t, err)
	tx.Timeout = 1 * time.Millisecond

	value := []byte(`{"status": "active"}`)
	tx.AddOperation(kvstore.NamespaceNode, "node-001", value, 1)

	ctx := context.Background()
	err = coordinator.PreCommit(ctx, tx)
	require.NoError(t, err)

	// 等待超时
	time.Sleep(10 * time.Millisecond)

	// 验证事务超时
	require.True(t, tx.IsTimedOut(), "事务应该超时")
}
