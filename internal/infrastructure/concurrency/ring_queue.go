// Package concurrency 提供并发控制和任务调度机制
package concurrency

import (
	"sync"
)

// RingQueue 环形队列（O(1) 入队和出队）
type RingQueue struct {
	data     []any
	head     int // 读指针
	tail     int // 写指针
	size     int // 当前元素数量
	capacity int // 容量
	mu       sync.Mutex
}

// NewRingQueue 创建环形队列
func NewRingQueue(initialCapacity int) *RingQueue {
	if initialCapacity <= 0 {
		initialCapacity = 64 // 默认容量
	}
	return &RingQueue{
		data:     make([]any, initialCapacity),
		capacity: initialCapacity,
	}
}

// Len 返回队列长度
func (q *RingQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.size
}

// Enqueue 入队（O(1) 均摊）
func (q *RingQueue) Enqueue(item any) {
	q.mu.Lock()
	defer q.mu.Unlock()

	// 检查是否需要扩容
	if q.size == q.capacity {
		q.expand()
	}

	q.data[q.tail] = item
	q.tail = (q.tail + 1) % q.capacity
	q.size++
}

// Dequeue 出队（O(1)）
func (q *RingQueue) Dequeue() (any, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.size == 0 {
		return nil, false
	}

	item := q.data[q.head]
	q.data[q.head] = nil // 释放引用，帮助 GC
	q.head = (q.head + 1) % q.capacity
	q.size--
	return item, true
}

// Peek 查看队首元素（O(1)）
func (q *RingQueue) Peek() (any, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.size == 0 {
		return nil, false
	}

	return q.data[q.head], true
}

// expand 扩容（2 倍增长）
func (q *RingQueue) expand() {
	newCapacity := q.capacity * 2
	newData := make([]any, newCapacity)

	// 将元素从旧数组复制到新数组（保持顺序）
	if q.head < q.tail {
		// 没有环绕：直接复制 [head:tail]
		copy(newData, q.data[q.head:q.tail])
	} else {
		// 环绕：复制 [head:capacity] 和 [0:tail]
		n := copy(newData, q.data[q.head:])
		copy(newData[n:], q.data[:q.tail])
	}

	q.data = newData
	q.head = 0
	q.tail = q.size
	q.capacity = newCapacity
}

// IsEmpty 检查队列是否为空
func (q *RingQueue) IsEmpty() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.size == 0
}
