// Package consistency 提供 2PC 强一致性协调器实现
//
// 强制优化 7.2: 事务状态持久化缓冲区测试
package consistency

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestNewTransactionPersistenceBuffer 测试创建缓冲区
func TestNewTransactionPersistenceBuffer(t *testing.T) {
	mockKV := newPersistableMockMetadataKV()

	// 默认配置
	buffer := NewTransactionPersistenceBuffer(TransactionPersistenceBufferConfig{
		KVStore: mockKV,
	})
	defer buffer.Close()

	require.NotNil(t, buffer)
	require.Equal(t, 100, buffer.maxBatch)
	require.Equal(t, 100*time.Millisecond, buffer.maxInterval)

	// 自定义配置
	buffer2 := NewTransactionPersistenceBuffer(TransactionPersistenceBufferConfig{
		MaxBatch:    50,
		MaxInterval: 200 * time.Millisecond,
		KVStore:     mockKV,
	})
	defer buffer2.Close()

	require.Equal(t, 50, buffer2.maxBatch)
	require.Equal(t, 200*time.Millisecond, buffer2.maxInterval)
}

// TestTransactionPersistenceBuffer_Add 测试添加事务
func TestTransactionPersistenceBuffer_Add(t *testing.T) {
	mockKV := newPersistableMockMetadataKV()

	buffer := NewTransactionPersistenceBuffer(TransactionPersistenceBufferConfig{
		MaxBatch:    10,
		MaxInterval: 1 * time.Second, // 长间隔，避免自动刷新
		KVStore:     mockKV,
	})
	defer buffer.Close()

	// 添加事务
	tx := NewTwoPCTransaction("tx-test-1", []string{"node-1"}, 5*time.Second)
	tx.State = TxStatePreCommit

	err := buffer.Add(tx)
	require.NoError(t, err)

	// 验证缓冲区大小
	require.Equal(t, 1, buffer.Size())
}

// TestTransactionPersistenceBuffer_Flush 测试刷新缓冲区
func TestTransactionPersistenceBuffer_Flush(t *testing.T) {
	mockKV := newPersistableMockMetadataKV()

	buffer := NewTransactionPersistenceBuffer(TransactionPersistenceBufferConfig{
		MaxBatch:    100,
		MaxInterval: 1 * time.Second,
		KVStore:     mockKV,
	})
	defer buffer.Close()

	// 添加多个事务
	for i := 0; i < 5; i++ {
		tx := NewTwoPCTransaction("tx-flush-test", []string{"node-1"}, 5*time.Second)
		tx.State = TxStatePreCommit
		err := buffer.Add(tx)
		require.NoError(t, err)
	}

	// 手动刷新
	err := buffer.Flush()
	require.NoError(t, err)

	// 验证缓冲区已清空
	require.Equal(t, 0, buffer.Size())
}

// TestTransactionPersistenceBuffer_BatchFlush 测试批量阈值触发刷新
func TestTransactionPersistenceBuffer_BatchFlush(t *testing.T) {
	mockKV := newPersistableMockMetadataKV()

	buffer := NewTransactionPersistenceBuffer(TransactionPersistenceBufferConfig{
		MaxBatch:    5, // 5 个事务触发刷新
		MaxInterval: 1 * time.Second,
		KVStore:     mockKV,
	})
	defer buffer.Close()

	// 添加 4 个事务（不触发）
	for i := 0; i < 4; i++ {
		tx := NewTwoPCTransaction("tx-batch-test", []string{"node-1"}, 5*time.Second)
		err := buffer.Add(tx)
		require.NoError(t, err)
	}

	// 等待异步刷新（不应该发生）
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, 4, buffer.Size())

	// 添加第 5 个事务（触发刷新）
	tx := NewTwoPCTransaction("tx-batch-test-5", []string{"node-1"}, 5*time.Second)
	err := buffer.Add(tx)
	require.NoError(t, err)

	// 等待异步刷新完成
	time.Sleep(100 * time.Millisecond)

	// 验证缓冲区已清空
	require.Equal(t, 0, buffer.Size())
}

// TestTransactionPersistenceBuffer_TimedFlush 测试定时刷新
func TestTransactionPersistenceBuffer_TimedFlush(t *testing.T) {
	mockKV := newPersistableMockMetadataKV()

	buffer := NewTransactionPersistenceBuffer(TransactionPersistenceBufferConfig{
		MaxBatch:    100, // 大批量，不会触发
		MaxInterval: 100 * time.Millisecond,
		KVStore:     mockKV,
	})
	defer buffer.Close()

	// 添加事务
	tx := NewTwoPCTransaction("tx-timed-test", []string{"node-1"}, 5*time.Second)
	err := buffer.Add(tx)
	require.NoError(t, err)

	// 等待定时刷新
	time.Sleep(150 * time.Millisecond)

	// 验证缓冲区已清空
	require.Equal(t, 0, buffer.Size())
}

// TestTransactionPersistenceBuffer_Concurrent 测试并发安全
func TestTransactionPersistenceBuffer_Concurrent(t *testing.T) {
	mockKV := newPersistableMockMetadataKV()

	buffer := NewTransactionPersistenceBuffer(TransactionPersistenceBufferConfig{
		MaxBatch:    100,
		MaxInterval: 1 * time.Second,
		KVStore:     mockKV,
	})
	defer buffer.Close()

	// 并发添加 100 个事务
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			tx := NewTwoPCTransaction("tx-concurrent-test", []string{"node-1"}, 5*time.Second)
			_ = buffer.Add(tx)
		}(i)
	}

	wg.Wait()

	// 手动刷新
	err := buffer.Flush()
	require.NoError(t, err)

	// 验证缓冲区已清空
	require.Equal(t, 0, buffer.Size())
}

// TestTransactionPersistenceBuffer_Close 测试关闭缓冲区
func TestTransactionPersistenceBuffer_Close(t *testing.T) {
	mockKV := newPersistableMockMetadataKV()

	buffer := NewTransactionPersistenceBuffer(TransactionPersistenceBufferConfig{
		MaxBatch:    100,
		MaxInterval: 1 * time.Second,
		KVStore:     mockKV,
	})

	// 添加事务
	tx := NewTwoPCTransaction("tx-close-test", []string{"node-1"}, 5*time.Second)
	err := buffer.Add(tx)
	require.NoError(t, err)

	// 关闭缓冲区（应该刷新剩余事务）
	err = buffer.Close()
	require.NoError(t, err)

	// 验证缓冲区已清空
	require.Equal(t, 0, buffer.Size())
}

// TestTransactionPersistenceBuffer_Stats 测试统计信息
func TestTransactionPersistenceBuffer_Stats(t *testing.T) {
	mockKV := newPersistableMockMetadataKV()

	buffer := NewTransactionPersistenceBuffer(TransactionPersistenceBufferConfig{
		MaxBatch:    50,
		MaxInterval: 200 * time.Millisecond,
		KVStore:     mockKV,
	})
	defer buffer.Close()

	// 添加事务
	for i := 0; i < 3; i++ {
		tx := NewTwoPCTransaction("tx-stats-test", []string{"node-1"}, 5*time.Second)
		_ = buffer.Add(tx)
	}

	// 获取统计信息
	stats := buffer.Stats()
	require.Equal(t, 3, stats.BufferSize)
	require.Equal(t, 50, stats.MaxBatch)
	require.Equal(t, 200*time.Millisecond, stats.MaxInterval)
}
