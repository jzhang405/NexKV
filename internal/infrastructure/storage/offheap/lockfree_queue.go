// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package offheap

import (
	"sync/atomic"
)

// LockFreeQueue 基于 Michael-Scott 算法的无锁队列
// 用于管理空闲 PageID，消除 sync.Mutex 的并发瓶颈
type LockFreeQueue struct {
	head  atomic.Pointer[node]
	tail  atomic.Pointer[node]
	dummy node
}

type node struct {
	value uint32
	next  atomic.Pointer[node]
}

// NewLockFreeQueue 创建一个新的无锁队列
func NewLockFreeQueue() *LockFreeQueue {
	q := &LockFreeQueue{}
	q.head.Store(&q.dummy)
	q.tail.Store(&q.dummy)
	return q
}

// Enqueue 将 PageID 加入队列
func (q *LockFreeQueue) Enqueue(value uint32) {
	n := &node{value: value}
	for {
		tail := q.tail.Load()
		next := tail.next.Load()
		if tail != q.tail.Load() {
			continue
		}
		if next != nil {
			q.tail.CompareAndSwap(tail, next)
			continue
		}
		if tail.next.CompareAndSwap(nil, n) {
			q.tail.CompareAndSwap(tail, n)
			return
		}
	}
}

// Dequeue 从队列取出 PageID
func (q *LockFreeQueue) Dequeue() (uint32, bool) {
	for {
		head := q.head.Load()
		tail := q.tail.Load()
		next := head.next.Load()
		if head != q.head.Load() {
			continue
		}
		if next == nil {
			return 0, false
		}
		if head == tail {
			q.tail.CompareAndSwap(tail, next)
			continue
		}
		value := next.value
		if q.head.CompareAndSwap(head, next) {
			return value, true
		}
	}
}

// Size 返回队列的近似大小
// 注意：由于并发访问，此值可能不准确
func (q *LockFreeQueue) Size() int {
	count := 0
	head := q.head.Load()
	for n := head.next.Load(); n != nil; n = n.next.Load() {
		count++
	}
	return count
}

// IsEmpty 返回队列是否为空
func (q *LockFreeQueue) IsEmpty() bool {
	head := q.head.Load()
	return head.next.Load() == nil
}

// Drain 清空队列并返回所有元素
// 注意：此操作不是原子的，仅用于测试或初始化
func (q *LockFreeQueue) Drain() []uint32 {
	var values []uint32
	for {
		pageID, ok := q.Dequeue()
		if !ok {
			break
		}
		values = append(values, pageID)
	}
	return values
}
