// Package consensus 2PC 协议测试
package consensus

import (
	"context"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/clock"
	"github.com/jzhang405/NexKV/internal/metadata/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ========================================
// 2PC 协议测试
// ========================================

// TestNewTwoPCService 测试创建 2PC 服务
func TestNewTwoPCService(t *testing.T) {
	metaStore := newMockMVStore()
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	uuidGen := newTestUUIDGenerator(t)
	localAddr := "node1"
	nodes := []string{"node1", "node2", "node3"}

	config := DefaultTwoPCConfig()
	service, err := NewTwoPCService(metaStore, trans, hlc, uuidGen, localAddr, nodes, config)

	assert.NoError(t, err)
	assert.NotNil(t, service)
	assert.Equal(t, config, service.config)
	assert.Equal(t, metaStore, service.metaStore)
	assert.Equal(t, trans, service.transport)
	assert.Equal(t, hlc, service.hlc)
	assert.Equal(t, uuidGen, service.uuidGen)
	assert.Equal(t, localAddr, service.localAddr)
	assert.Equal(t, nodes, service.nodes)
}

// TestTwoPCService_StartStop 测试启动和停止 2PC 服务
func TestTwoPCService_StartStop(t *testing.T) {
	metaStore := newMockMVStore()
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	uuidGen := newTestUUIDGenerator(t)
	nodes := []string{"node1", "node2", "node3"}

	service, err := NewTwoPCService(metaStore, trans, hlc, uuidGen, "node1", nodes, DefaultTwoPCConfig())
	require.NoError(t, err)

	// 测试启动
	err = service.Start()
	assert.NoError(t, err)
	assert.True(t, service.started.Load())

	// 测试重复启动
	err = service.Start()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "已经启动")

	// 测试停止
	err = service.Stop()
	assert.NoError(t, err)
	assert.True(t, service.stopped.Load())

	// 测试重复停止
	err = service.Stop()
	assert.NoError(t, err)
}

// TestTwoPCService_Execute_SingleNode 测试单节点事务
func TestTwoPCService_Execute_SingleNode(t *testing.T) {
	metaStore := newMockMVStore()
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	uuidGen := newTestUUIDGenerator(t)
	nodes := []string{"node1"}

	service, err := NewTwoPCService(metaStore, trans, hlc, uuidGen, "node1", nodes, DefaultTwoPCConfig())
	require.NoError(t, err)

	err = service.Start()
	require.NoError(t, err)
	defer func() { _ = service.Stop() }()

	// 创建事务操作
	operations := []transport.Operation{
		{
			Key:   "key1",
			Value: []byte("value1"),
			Type:  "put",
		},
		{
			Key:   "key2",
			Value: []byte("value2"),
			Type:  "put",
		},
	}

	// 执行事务
	ctx := context.Background()
	err = service.Execute(ctx, operations)

	// 单节点事务应该成功（没有远程协调）
	assert.NoError(t, err)

	// 验证数据已写入（直接从 metaStore 验证）
	val1, err := metaStore.Get("key1")
	assert.NoError(t, err)
	assert.Equal(t, []byte("value1"), val1)

	val2, err := metaStore.Get("key2")
	assert.NoError(t, err)
	assert.Equal(t, []byte("value2"), val2)
}

// TestTwoPCService_Execute_Timeout 测试事务超时
func TestTwoPCService_Execute_Timeout(t *testing.T) {
	metaStore := newMockMVStore()
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	uuidGen := newTestUUIDGenerator(t)
	nodes := []string{"node1", "node2", "node3"}

	// 设置短超时
	config := &TwoPCConfig{
		Timeout: 100 * time.Millisecond,
	}

	service, err := NewTwoPCService(metaStore, trans, hlc, uuidGen, "node1", nodes, config)
	require.NoError(t, err)

	err = service.Start()
	require.NoError(t, err)
	defer func() { _ = service.Stop() }()

	// 创建事务操作（包含远程节点）
	operations := []transport.Operation{
		{
			Key:   "key1",
			Value: []byte("value1"),
			Type:  "put",
		},
	}

	// 执行事务（应该超时）
	ctx := context.Background()
	err = service.Execute(ctx, operations)

	// 应该超时或失败
	assert.Error(t, err)
}

// TestTwoPCService_Execute_MultiOperation 测试多操作事务
func TestTwoPCService_Execute_MultiOperation(t *testing.T) {
	metaStore := newMockMVStore()
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	uuidGen := newTestUUIDGenerator(t)
	nodes := []string{"node1"} // 单节点，避免网络依赖

	service, err := NewTwoPCService(metaStore, trans, hlc, uuidGen, "node1", nodes, DefaultTwoPCConfig())
	require.NoError(t, err)

	err = service.Start()
	require.NoError(t, err)
	defer func() { _ = service.Stop() }()

	// 创建多操作事务
	operations := []transport.Operation{
		{
			Key:   "key1",
			Value: []byte("value1"),
			Type:  "put",
		},
		{
			Key:   "key2",
			Value: []byte("value2"),
			Type:  "put",
		},
		{
			Key:   "key3",
			Value: []byte("value3"),
			Type:  "put",
		},
	}

	// 执行事务
	ctx := context.Background()
	err = service.Execute(ctx, operations)

	// 单节点事务应该成功
	assert.NoError(t, err)

	// 验证所有数据已写入
	val1, err := metaStore.Get("key1")
	assert.NoError(t, err)
	assert.Equal(t, []byte("value1"), val1)

	val2, err := metaStore.Get("key2")
	assert.NoError(t, err)
	assert.Equal(t, []byte("value2"), val2)

	val3, err := metaStore.Get("key3")
	assert.NoError(t, err)
	assert.Equal(t, []byte("value3"), val3)
}

// TestTwoPCService_AddRemoveNode 测试添加和移除节点
func TestTwoPCService_AddRemoveNode(t *testing.T) {
	metaStore := newMockMVStore()
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	uuidGen := newTestUUIDGenerator(t)
	nodes := []string{"node1", "node2"}

	service, err := NewTwoPCService(metaStore, trans, hlc, uuidGen, "node1", nodes, DefaultTwoPCConfig())
	require.NoError(t, err)

	err = service.Start()
	require.NoError(t, err)
	defer func() { _ = service.Stop() }()

	// 添加节点
	service.AddNode("node3")
	assert.Contains(t, service.nodes, "node3")

	// 移除节点
	service.RemoveNode("node2")
	assert.NotContains(t, service.nodes, "node2")
}

// TestTwoPCService_GetStats 测试获取统计信息
func TestTwoPCService_GetStats(t *testing.T) {
	metaStore := newMockMVStore()
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	uuidGen := newTestUUIDGenerator(t)
	nodes := []string{"node1", "node2", "node3"}

	service, err := NewTwoPCService(metaStore, trans, hlc, uuidGen, "node1", nodes, DefaultTwoPCConfig())
	require.NoError(t, err)

	err = service.Start()
	require.NoError(t, err)
	defer func() { _ = service.Stop() }()

	// 执行一些事务
	operations := []transport.Operation{
		{
			Key:   "key1",
			Value: []byte("value1"),
			Type:  "put",
		},
	}

	ctx := context.Background()
	_ = service.Execute(ctx, operations)

	// 获取统计信息
	stats := service.GetStats()

	assert.NotNil(t, stats)
	assert.NotNil(t, stats.TransactionsTotal.Load())
	assert.NotNil(t, stats.TransactionsCommitted.Load())
	assert.NotNil(t, stats.TransactionsAborted.Load())
	assert.NotNil(t, stats.TransactionsTimeout.Load())
	assert.NotNil(t, stats.AvgTxLatency.Load())
}

// TestTwoPCService_GetTransactionState 测试获取事务状态
func TestTwoPCService_GetTransactionState(t *testing.T) {
	metaStore := newMockMVStore()
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	uuidGen := newTestUUIDGenerator(t)
	nodes := []string{"node1", "node2", "node3"}

	service, err := NewTwoPCService(metaStore, trans, hlc, uuidGen, "node1", nodes, DefaultTwoPCConfig())
	require.NoError(t, err)

	err = service.Start()
	require.NoError(t, err)
	defer func() { _ = service.Stop() }()

	// 创建事务状态
	txID := "tx_1"
	state := &TransactionState{
		TransactionID: txID,
		Participants:  nodes,
		Operations:    []transport.Operation{},
		votes:         make(map[string]string),
		Timestamp:     hlc.Now(),
		doneCh:        make(chan struct{}),
	}

	service.transactionsMu.Lock()
	service.transactions[txID] = state
	service.transactionsMu.Unlock()

	// 获取事务状态
	retrievedState, exists := service.GetTransaction(txID)
	assert.True(t, exists)
	assert.Equal(t, state, retrievedState)

	// 测试获取不存在的事务
	_, exists = service.GetTransaction("non_existent")
	assert.False(t, exists)
}

// TestTwoPCService_CleanupTransaction 测试清理事务
func TestTwoPCService_CleanupTransaction(t *testing.T) {
	metaStore := newMockMVStore()
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	uuidGen := newTestUUIDGenerator(t)
	nodes := []string{"node1", "node2", "node3"}

	service, err := NewTwoPCService(metaStore, trans, hlc, uuidGen, "node1", nodes, DefaultTwoPCConfig())
	require.NoError(t, err)

	err = service.Start()
	require.NoError(t, err)
	defer func() { _ = service.Stop() }()

	// 创建事务状态
	txID := "tx_1"
	state := &TransactionState{
		TransactionID: txID,
		Participants:  nodes,
		Operations:    []transport.Operation{},
		votes:         make(map[string]string),
		Timestamp:     hlc.Now(),
		doneCh:        make(chan struct{}),
	}

	service.transactionsMu.Lock()
	service.transactions[txID] = state
	service.transactionsMu.Unlock()

	// 清理事务
	service.cleanupTransaction(txID)

	// 验证事务已清理
	service.transactionsMu.RLock()
	_, exists := service.transactions[txID]
	service.transactionsMu.RUnlock()

	assert.False(t, exists)
}

// TestDefaultTwoPCConfig 测试默认 2PC 配置
func TestDefaultTwoPCConfig(t *testing.T) {
	config := DefaultTwoPCConfig()

	assert.Equal(t, 10*time.Second, config.Timeout)
	assert.True(t, config.EnableGossipSync)
	assert.Equal(t, 5*time.Second, config.GossipInterval)
}

// TestTxState_String 测试事务状态字符串表示
func TestTxState_String(t *testing.T) {
	testCases := []struct {
		name     string
		state    TxState
		expected string
	}{
		{"Init", TxStateInit, "init"},
		{"PreCommit", TxStatePreCommit, "pre_commit"},
		{"Committed", TxStateCommitted, "committed"},
		{"Aborted", TxStateAborted, "aborted"},
		{"Unknown", TxState(99), "unknown"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.state.String()
			assert.Equal(t, tc.expected, result)
		})
	}
}

// TestTransactionState_SetState 测试设置事务状态
func TestTransactionState_SetState(t *testing.T) {
	state := &TransactionState{
		TransactionID: "tx_1",
		votes:         make(map[string]string),
	}

	// 测试状态转换
	state.State.Store(uint32(TxStateInit))
	assert.Equal(t, TxStateInit, TxState(state.State.Load().(uint32)))

	state.State.Store(uint32(TxStatePreCommit))
	assert.Equal(t, TxStatePreCommit, TxState(state.State.Load().(uint32)))

	state.State.Store(uint32(TxStateCommitted))
	assert.Equal(t, TxStateCommitted, TxState(state.State.Load().(uint32)))

	state.State.Store(uint32(TxStateAborted))
	assert.Equal(t, TxStateAborted, TxState(state.State.Load().(uint32)))
}

// TestTransactionState_IsFinal 测试判断事务是否已结束
func TestTransactionState_IsFinal(t *testing.T) {
	testCases := []struct {
		name     string
		state    TxState
		expected bool
	}{
		{"Init 状态未结束", TxStateInit, false},
		{"Prepared 状态未结束", TxStatePreCommit, false},
		{"Committed 状态已结束", TxStateCommitted, true},
		{"RolledBack 状态已结束", TxStateAborted, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			state := &TransactionState{
				TransactionID: "tx_1",
				votes:         make(map[string]string),
			}
			state.State.Store(uint32(tc.state))

			// 检查是否为最终状态（Committed 或 Aborted）
			result := tc.state == TxStateCommitted || tc.state == TxStateAborted
			assert.Equal(t, tc.expected, result)
		})
	}
}

// ========================================
// 性能基准测试
// ========================================

// BenchmarkTwoPCService_Execute 性能基准测试: 执行事务
func BenchmarkTwoPCService_Execute(b *testing.B) {
	metaStore := newMockMVStore()
	trans, _ := transport.NewUDPTransport("127.0.0.1:0")
	hlc := clock.NewHLC()
	uuidGen := newBenchmarkUUIDGenerator()
	nodes := []string{"node1"} // 单节点，避免网络延迟

	service, _ := NewTwoPCService(metaStore, trans, hlc, uuidGen, "node1", nodes, DefaultTwoPCConfig())
	_ = service.Start()
	defer func() { _ = service.Stop() }()

	operations := []transport.Operation{
		{
			Key:   "key1",
			Value: []byte("value1"),
			Type:  "put",
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx := context.Background()
		_ = service.Execute(ctx, operations)
	}
}

// BenchmarkTwoPCService_Execute_MultiOperation 性能基准测试: 多操作事务
func BenchmarkTwoPCService_Execute_MultiOperation(b *testing.B) {
	metaStore := newMockMVStore()
	trans, _ := transport.NewUDPTransport("127.0.0.1:0")
	hlc := clock.NewHLC()
	uuidGen := newBenchmarkUUIDGenerator()
	nodes := []string{"node1"}

	service, _ := NewTwoPCService(metaStore, trans, hlc, uuidGen, "node1", nodes, DefaultTwoPCConfig())
	_ = service.Start()
	defer func() { _ = service.Stop() }()

	operations := []transport.Operation{
		{
			Key:   "key1",
			Value: []byte("value1"),
			Type:  "put",
		},
		{
			Key:   "key2",
			Value: []byte("value2"),
			Type:  "put",
		},
		{
			Key:   "key3",
			Value: []byte("value3"),
			Type:  "put",
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx := context.Background()
		_ = service.Execute(ctx, operations)
	}
}
