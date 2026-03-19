// Package concurrency 提供并发控制和任务调度机制
package concurrency

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRingQueue_EnqueueDequeue 测试基本入队出队
func TestRingQueue_EnqueueDequeue(t *testing.T) {
	q := NewRingQueue(4)

	// 入队
	q.Enqueue("a")
	q.Enqueue("b")
	q.Enqueue("c")
	assert.Equal(t, 3, q.Len())

	// 出队
	item, ok := q.Dequeue()
	require.True(t, ok)
	assert.Equal(t, "a", item)
	assert.Equal(t, 2, q.Len())

	item, ok = q.Dequeue()
	require.True(t, ok)
	assert.Equal(t, "b", item)

	item, ok = q.Dequeue()
	require.True(t, ok)
	assert.Equal(t, "c", item)

	// 队列为空
	item, ok = q.Dequeue()
	require.False(t, ok)
	assert.Nil(t, item)
	assert.Equal(t, 0, q.Len())
}

// TestRingQueue_Peek 测试查看队首
func TestRingQueue_Peek(t *testing.T) {
	q := NewRingQueue(4)

	q.Enqueue("a")
	q.Enqueue("b")

	// Peek 不移除元素
	item, ok := q.Peek()
	require.True(t, ok)
	assert.Equal(t, "a", item)
	assert.Equal(t, 2, q.Len())

	// 再次 Peek 仍然返回相同元素
	item, ok = q.Peek()
	require.True(t, ok)
	assert.Equal(t, "a", item)

	// Dequeue 移除元素
	item, ok = q.Dequeue()
	require.True(t, ok)
	assert.Equal(t, "a", item)

	// Peek 现在返回下一个元素
	item, ok = q.Peek()
	require.True(t, ok)
	assert.Equal(t, "b", item)
}

// TestRingQueue_Empty 测试空队列
func TestRingQueue_Empty(t *testing.T) {
	q := NewRingQueue(4)

	assert.True(t, q.IsEmpty())
	assert.Equal(t, 0, q.Len())

	_, ok := q.Dequeue()
	assert.False(t, ok)

	_, ok = q.Peek()
	assert.False(t, ok)

	q.Enqueue("a")
	assert.False(t, q.IsEmpty())
	assert.Equal(t, 1, q.Len())
}

// TestRingQueue_WrapAround 测试环形环绕
func TestRingQueue_WrapAround(t *testing.T) {
	q := NewRingQueue(4)

	// 填满队列
	q.Enqueue(1)
	q.Enqueue(2)
	q.Enqueue(3)
	q.Enqueue(4)
	assert.Equal(t, 4, q.Len())

	// 出队两个元素
	item, _ := q.Dequeue()
	assert.Equal(t, 1, item)
	item, _ = q.Dequeue()
	assert.Equal(t, 2, item)

	// 入队两个新元素（触发环绕）
	q.Enqueue(5)
	q.Enqueue(6)

	// 验证顺序
	item, _ = q.Dequeue()
	assert.Equal(t, 3, item)
	item, _ = q.Dequeue()
	assert.Equal(t, 4, item)
	item, _ = q.Dequeue()
	assert.Equal(t, 5, item)
	item, _ = q.Dequeue()
	assert.Equal(t, 6, item)

	assert.True(t, q.IsEmpty())
}

// TestRingQueue_Expand 测试扩容
func TestRingQueue_Expand(t *testing.T) {
	q := NewRingQueue(4)

	// 入队超过容量
	for i := 0; i < 10; i++ {
		q.Enqueue(i)
	}

	assert.Equal(t, 10, q.Len())

	// 验证所有元素
	for i := 0; i < 10; i++ {
		item, ok := q.Dequeue()
		require.True(t, ok)
		assert.Equal(t, i, item)
	}

	assert.True(t, q.IsEmpty())
}

// TestRingQueue_Concurrent 测试并发安全
func TestRingQueue_Concurrent(t *testing.T) {
	q := NewRingQueue(64)
	const numItems = 1000

	var wg sync.WaitGroup
	wg.Add(2)

	// 生产者
	go func() {
		defer wg.Done()
		for i := 0; i < numItems; i++ {
			q.Enqueue(i)
		}
	}()

	// 消费者 - 循环直到队列为空且生产者完成
	go func() {
		defer wg.Done()
		for i := 0; i < numItems; i++ {
			for {
				if _, ok := q.Dequeue(); ok {
					break
				}
			}
		}
	}()

	wg.Wait()
	// 等待所有消费完成
	for !q.IsEmpty() {
		q.Dequeue()
	}
	assert.True(t, q.IsEmpty())
	assert.Equal(t, 0, q.Len())
}

// BenchmarkRingQueue_EnqueueDequeue 基准测试
func BenchmarkRingQueue_EnqueueDequeue(b *testing.B) {
	q := NewRingQueue(1024)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			q.Enqueue("item")
			q.Dequeue()
		}
	})
}

// BenchmarkRingQueue_EnqueueOnly 入队基准
func BenchmarkRingQueue_EnqueueOnly(b *testing.B) {
	q := NewRingQueue(1024)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q.Enqueue(i)
	}
}

// BenchmarkRingQueue_DequeueOnly 出队基准
func BenchmarkRingQueue_DequeueOnly(b *testing.B) {
	q := NewRingQueue(1024)
	for i := 0; i < b.N; i++ {
		q.Enqueue(i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q.Dequeue()
	}
}
