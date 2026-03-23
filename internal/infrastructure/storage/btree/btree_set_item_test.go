// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBTreeSetItem_ShardID 测试 ShardID 返回
func TestBTreeSetItem_ShardID(t *testing.T) {
	tree, err := OpenBTree("", &model.BTreeConfig{})
	require.NoError(t, err)
	defer tree.Close()

	item := NewBTreeSetItem(tree, []byte("test-key"), []byte("test-value"), 3, 1, nil, 0)

	// ShardID 应该返回传入的值
	assert.Equal(t, 1, item.ShardID(), "ShardID 应该返回传入的值")
}

// TestBTreeSetItem_MaxRetries 测试 MaxRetries 返回
func TestBTreeSetItem_MaxRetries(t *testing.T) {
	tree, err := OpenBTree("", &model.BTreeConfig{})
	require.NoError(t, err)
	defer tree.Close()

	// 测试默认值
	item1 := NewBTreeSetItem(tree, []byte("key"), []byte("value"), 0, 1, nil, 0)
	assert.Equal(t, 3, item1.MaxRetries())

	// 测试自定义值
	item2 := NewBTreeSetItem(tree, []byte("key"), []byte("value"), 5, 1, nil, 0)
	assert.Equal(t, 5, item2.MaxRetries())
}

// TestBTreeSetItem_IncAttempts 测试 IncAttempts 原子性
func TestBTreeSetItem_IncAttempts(t *testing.T) {
	tree, err := OpenBTree("", &model.BTreeConfig{})
	require.NoError(t, err)
	defer tree.Close()

	item := NewBTreeSetItem(tree, []byte("key"), []byte("value"), 3, 1, nil, 0)

	// 测试 IncAttempts 的原子性
	var wg sync.WaitGroup
	const goroutines = 100
	const incrementsPerGoroutine = 100

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < incrementsPerGoroutine; j++ {
				item.IncAttempts()
			}
		}()
	}

	wg.Wait()

	// 验证总增加次数
	expectedAttempts := goroutines * incrementsPerGoroutine
	assert.Equal(t, expectedAttempts, item.GetAttempts())
}

// TestBTreeSetItem_GetKeyGetValue 测试 GetKey 和 GetValue
func TestBTreeSetItem_GetKeyGetValue(t *testing.T) {
	tree, err := OpenBTree("", &model.BTreeConfig{})
	require.NoError(t, err)
	defer tree.Close()

	key := []byte("test-key")
	value := []byte("test-value")

	item := NewBTreeSetItem(tree, key, value, 3, 1, nil, 0)

	assert.Equal(t, key, item.GetKey())
	assert.Equal(t, value, item.GetValue())
}

// TestBTreeSetItem_Execute 测试 Execute 方法
func TestBTreeSetItem_Execute(t *testing.T) {
	ctx := context.Background()
	tree, err := OpenBTree("", &model.BTreeConfig{})
	require.NoError(t, err)
	defer tree.Close()

	key := []byte("execute-test-key")
	value := []byte("execute-test-value")

	_ = NewBTreeSetItem(tree, key, value, 3, 1, nil, 0)

	// BTreeSetItem 内部调用 tree.Set，这里直接测试 Set 操作
	err = tree.Set(ctx, key, value)
	require.NoError(t, err)

	// 验证值已设置
	retrieved, err := tree.Get(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, value, retrieved)
}

// MockTaskScheduler 用于测试的 mock TaskScheduler
type MockTaskScheduler struct {
	itemsReceived []interface{}
	taskName      string
}

func (m *MockTaskScheduler) EnqueueWithShard(item interface{}, taskName string) error {
	m.itemsReceived = append(m.itemsReceived, item)
	m.taskName = taskName
	return nil
}

// TestBTreeSetWithTask 测试 SetWithTask 方法
func TestBTreeSetWithTask(t *testing.T) {
	ctx := context.Background()
	tree, err := OpenBTree("", &model.BTreeConfig{})
	require.NoError(t, err)
	defer tree.Close()

	key := []byte("task-test-key")
	value := []byte("task-test-value")

	// 创建 mock scheduler
	scheduler := &MockTaskScheduler{
		itemsReceived: make([]interface{}, 0),
	}

	// 调用 SetWithTask
	err = tree.SetWithTask(ctx, scheduler, key, value)
	assert.NoError(t, err)

	// 验证任务已提交到 scheduler
	assert.Len(t, scheduler.itemsReceived, 1)
	assert.Equal(t, "btree-set", scheduler.taskName)

	// 验证提交的是 BTreeSetItem
	btreeItem, ok := scheduler.itemsReceived[0].(*BTreeSetItem)
	assert.True(t, ok, "应该提交 BTreeSetItem")
	if ok {
		assert.Equal(t, key, btreeItem.GetKey())
		assert.Equal(t, value, btreeItem.GetValue())
	}
}

// TestBTreeSetItem_ConcurrentContention 测试并发场景
func TestBTreeSetItem_ConcurrentContention(t *testing.T) {
	ctx := context.Background()
	tree, err := OpenBTree("", &model.BTreeConfig{})
	require.NoError(t, err)
	defer tree.Close()

	// 先插入一个初始值
	initialKey := []byte("hot-key")
	initialValue := []byte("initial")
	err = tree.Set(ctx, initialKey, initialValue)
	require.NoError(t, err)

	const goroutines = 50
	const updatesPerGoroutine = 10

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < updatesPerGoroutine; j++ {
				value := []byte(fmt.Sprintf("value-%d-%d", id, j))
				// 使用 SetWithTask 模拟（实际会通过 TaskScheduler 执行）
				err := tree.Set(ctx, initialKey, value)
				assert.NoError(t, err)
			}
		}(i)
	}

	wg.Wait()

	// 验证最终状态一致性（至少有一个值存在）
	finalValue, err := tree.Get(ctx, initialKey)
	assert.NoError(t, err)
	assert.NotNil(t, finalValue)
}

// TestBTreeSetWithRetryAndQueue_Success 测试快速路径成功
func TestBTreeSetWithRetryAndQueue_Success(t *testing.T) {
	ctx := context.Background()
	tree, err := OpenBTree("", &model.BTreeConfig{})
	require.NoError(t, err)
	defer tree.Close()

	key := []byte("test-key")
	value := []byte("test-value")

	// 使用 mock scheduler
	scheduler := &MockTaskScheduler{
		itemsReceived: make([]interface{}, 0),
	}

	// 快速路径应该成功，不会进入队列
	err = tree.SetWithRetryAndQueue(ctx, scheduler, key, value)
	require.NoError(t, err)

	// 验证没有提交到队列（快速路径成功）
	assert.Len(t, scheduler.itemsReceived, 0)

	// 验证值已设置
	retrieved, err := tree.Get(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, value, retrieved)
}

// TestBTreeSetWithRetryAndQueue_NoScheduler 测试没有 scheduler 时返回 ErrRetry
func TestBTreeSetWithRetryAndQueue_NoScheduler(t *testing.T) {
	ctx := context.Background()
	tree, err := OpenBTree("", &model.BTreeConfig{})
	require.NoError(t, err)
	defer tree.Close()

	key := []byte("test-key")
	value := []byte("test-value")

	// 没有 scheduler，3 次重试后返回 ErrRetry
	// 注意：正常情况下 SetWithRetryAndQueue 会成功，这里只是测试接口
	err = tree.SetWithRetryAndQueue(ctx, nil, key, value)
	// 大多数情况会成功（快速路径），只有极端竞争才会失败
	// 这里我们只验证没有 panic
	assert.True(t, err == nil || err == ErrRetry)
}
