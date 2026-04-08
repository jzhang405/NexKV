// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package offheap

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLockFreeQueue_Empty(t *testing.T) {
	q := NewLockFreeQueue()

	assert.True(t, q.IsEmpty(), "new queue should be empty")
	assert.Equal(t, 0, q.Size(), "new queue size should be 0")

	_, ok := q.Dequeue()
	assert.False(t, ok, "dequeue from empty queue should return false")
}

func TestLockFreeQueue_EnqueueDequeue(t *testing.T) {
	q := NewLockFreeQueue()

	q.Enqueue(42)
	assert.False(t, q.IsEmpty())
	assert.Equal(t, 1, q.Size())

	val, ok := q.Dequeue()
	require.True(t, ok)
	assert.Equal(t, uint32(42), val)
	assert.True(t, q.IsEmpty())
}

func TestLockFreeQueue_FIFO(t *testing.T) {
	q := NewLockFreeQueue()

	values := []uint32{10, 20, 30, 40, 50}
	for _, v := range values {
		q.Enqueue(v)
	}
	assert.Equal(t, len(values), q.Size())

	for _, expected := range values {
		val, ok := q.Dequeue()
		require.True(t, ok)
		assert.Equal(t, expected, val)
	}
	assert.True(t, q.IsEmpty())
}

func TestLockFreeQueue_Drain(t *testing.T) {
	q := NewLockFreeQueue()

	inserted := []uint32{1, 2, 3, 4, 5}
	for _, v := range inserted {
		q.Enqueue(v)
	}

	drained := q.Drain()
	assert.Equal(t, inserted, drained)
	assert.True(t, q.IsEmpty(), "queue should be empty after drain")

	// drain 空队列
	empty := q.Drain()
	assert.Nil(t, empty)
}

func TestLockFreeQueue_ConcurrentEnqueue(t *testing.T) {
	q := NewLockFreeQueue()
	const numGoroutines = 8
	const perGoroutine = 1000

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		go func(base uint32) {
			defer wg.Done()
			for i := uint32(0); i < perGoroutine; i++ {
				q.Enqueue(base*perGoroutine + i)
			}
		}(uint32(g))
	}
	wg.Wait()

	assert.Equal(t, numGoroutines*perGoroutine, q.Size(), "all items should be enqueued")

	// 验证全部能出队
	count := 0
	for {
		_, ok := q.Dequeue()
		if !ok {
			break
		}
		count++
	}
	assert.Equal(t, numGoroutines*perGoroutine, count)
}

func TestLockFreeQueue_ConcurrentDequeue(t *testing.T) {
	q := NewLockFreeQueue()
	const total = 10000

	for i := uint32(0); i < total; i++ {
		q.Enqueue(i)
	}

	var consumed atomic.Int32
	var wg sync.WaitGroup
	const numConsumers = 8
	wg.Add(numConsumers)

	for g := 0; g < numConsumers; g++ {
		go func() {
			defer wg.Done()
			for {
				_, ok := q.Dequeue()
				if !ok {
					return
				}
				consumed.Add(1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(total), consumed.Load(), "all items should be consumed")
}

func TestLockFreeQueue_ConcurrentEnqueueDequeue(t *testing.T) {
	q := NewLockFreeQueue()
	const numProducers = 4
	const numConsumers = 4
	const perProducer = 500
	const totalItems = numProducers * perProducer

	var consumed atomic.Int32
	var wg sync.WaitGroup
	producersDone := make(chan struct{})

	// 生产者
	wg.Add(numProducers)
	for g := 0; g < numProducers; g++ {
		go func(base uint32) {
			defer wg.Done()
			for i := uint32(0); i < perProducer; i++ {
				q.Enqueue(base*perProducer + i)
			}
		}(uint32(g))
	}

	// 生产者完成后关闭信号
	go func() {
		wg.Wait()
		close(producersDone)
	}()

	// 消费者
	var wgC sync.WaitGroup
	wgC.Add(numConsumers)
	for g := 0; g < numConsumers; g++ {
		go func() {
			defer wgC.Done()
			for {
				_, ok := q.Dequeue()
				if ok {
					consumed.Add(1)
				} else {
					select {
					case <-producersDone:
						// 最终清理一轮
						for {
							_, ok := q.Dequeue()
							if !ok {
								return
							}
							consumed.Add(1)
						}
					default:
					}
				}
			}
		}()
	}

	wgC.Wait()

	actual := consumed.Load()
	assert.Equal(t, int32(totalItems), actual,
		"all %d items should be consumed, got %d", totalItems, actual)
}
