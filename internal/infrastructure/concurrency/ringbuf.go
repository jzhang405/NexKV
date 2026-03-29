// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package concurrency

import (
	"sync/atomic"
	"unsafe"
)

const (
	// ringBufferSize 环形缓冲区初始大小（必须是 2 的幂）
	// 每 core 一个 buffer：16K × 64B = 1MB / core
	ringBufferSize = 16384
	ringBufferMask = ringBufferSize - 1
	cacheLineSize  = 64
	ptrSize        = 8 // unsafe.Pointer = 8 bytes (64-bit)
)

// ==========================================
// MPSC 扩展队列（固定 Ring + 链表溢出）
//
// 1. Fast path: 固定 16K 数组（cache hot，零分配）
// 2. Slow path: 链表溢出（无限扩展，吸收 burst）
// 3. Per-core 设计：1 queue / CPU core
// ==========================================

// slot 缓存行对齐的数组槽位
// 使用 unsafe.Pointer 字段 + atomic.LoadPointer/StorePointer
// 避免 uintptr ↔ unsafe.Pointer 转换触发 checkptr
type slot struct {
	task unsafe.Pointer // atomic access via runtime
	_    [cacheLineSize - ptrSize]byte
}

// node 链表溢出节点
type node struct {
	next atomic.Pointer[node]
	task unsafe.Pointer                    // atomic access via runtime
	_    [cacheLineSize - 8 - ptrSize]byte // next(8) + task(8) + pad → 64
}

// MPSCExtQueue 固定 Ring + 链表溢出
type MPSCExtQueue struct {
	buffer [ringBufferSize]slot
	_      [cacheLineSize]byte

	writeIdx atomic.Uint64
	_        [cacheLineSize]byte
	readIdx  atomic.Uint64
	_        [cacheLineSize]byte

	overflowHead atomic.Pointer[node]
	overflowTail atomic.Pointer[node]
	overflowLen  atomic.Int64 // overflow 链表中的节点数
	_            [cacheLineSize]byte
}

// NewMPSCExtQueue 创建 MPSCExtQueue
func NewMPSCExtQueue() *MPSCExtQueue {
	q := &MPSCExtQueue{}
	stub := &node{}
	q.overflowHead.Store(stub)
	q.overflowTail.Store(stub)
	return q
}

// ==========================================
// MP: 多生产者入队（永远成功）
// ==========================================

func (q *MPSCExtQueue) Enqueue(task unsafe.Pointer) bool {
	for {
		read := q.readIdx.Load()
		write := q.writeIdx.Load()

		// Fast path: 固定 ring buffer
		if write-read < ringBufferSize {
			if q.writeIdx.CompareAndSwap(write, write+1) {
				slotPtr := &q.buffer[write&ringBufferMask].task
				atomic.StorePointer(slotPtr, task)
				return true
			}
			continue
		}

		// Slow path: 链表溢出（无限扩展）
		q.enqueueOverflow(task)
		return true
	}
}

func (q *MPSCExtQueue) enqueueOverflow(task unsafe.Pointer) {
	newNode := &node{}
	atomic.StorePointer(&newNode.task, task)

	for {
		tail := q.overflowTail.Load()
		next := tail.next.Load()

		if tail == q.overflowTail.Load() {
			if next == nil {
				if tail.next.CompareAndSwap(nil, newNode) {
					q.overflowTail.CompareAndSwap(tail, newNode)
					q.overflowLen.Add(1)
					return
				}
			} else {
				q.overflowTail.CompareAndSwap(tail, next)
			}
		}
	}
}

// ==========================================
// SC: Peek / PeekN
// ==========================================

func (q *MPSCExtQueue) Peek() (unsafe.Pointer, bool) {
	read := q.readIdx.Load()
	write := q.writeIdx.Load()

	if read < write {
		ptr := atomic.LoadPointer(&q.buffer[read&ringBufferMask].task)
		return ptr, true
	}

	head := q.overflowHead.Load()
	next := head.next.Load()
	if next == nil {
		return nil, false
	}
	ptr := atomic.LoadPointer(&next.task)
	return ptr, true
}

func (q *MPSCExtQueue) PeekN(out []unsafe.Pointer) int {
	n := len(out)
	count := 0
	read := q.readIdx.Load()
	write := q.writeIdx.Load()

	for count < n && read < write {
		ptr := atomic.LoadPointer(&q.buffer[read&ringBufferMask].task)
		out[count] = ptr
		read++
		count++
	}

	if count < n {
		curr := q.overflowHead.Load()
		for count < n {
			next := curr.next.Load()
			if next == nil {
				break
			}
			ptr := atomic.LoadPointer(&next.task)
			out[count] = ptr
			curr = next
			count++
		}
	}

	return count
}

// ==========================================
// SC: Commit / CommitN（推进读指针）
// ==========================================

func (q *MPSCExtQueue) Commit() {
	read := q.readIdx.Load()
	write := q.writeIdx.Load()

	if read < write {
		q.readIdx.Store(read + 1)
		return
	}

	head := q.overflowHead.Load()
	next := head.next.Load()
	if next != nil {
		q.overflowHead.Store(next)
		q.overflowLen.Add(-1)
	}
}

func (q *MPSCExtQueue) CommitN(n uint64) {
	for i := uint64(0); i < n; i++ {
		q.Commit()
	}
}

// ==========================================
// SC: Dequeue / DequeueN（Peek + Commit 便捷组合）
// ==========================================

func (q *MPSCExtQueue) Dequeue() (unsafe.Pointer, bool) {
	task, ok := q.Peek()
	if !ok {
		return nil, false
	}
	q.Commit()
	return task, true
}

func (q *MPSCExtQueue) DequeueN(out []unsafe.Pointer) int {
	n := q.PeekN(out)
	if n > 0 {
		q.CommitN(uint64(n))
	}
	return n
}

// ==========================================
// 状态查询
// ==========================================

func (q *MPSCExtQueue) Size() uint64 {
	ringSize := q.writeIdx.Load() - q.readIdx.Load()
	return ringSize + uint64(q.overflowLen.Load())
}

func (q *MPSCExtQueue) IsEmpty() bool {
	read := q.readIdx.Load()
	write := q.writeIdx.Load()
	if read < write {
		return false
	}
	head := q.overflowHead.Load()
	return head.next.Load() == nil
}

func (q *MPSCExtQueue) IsFull() bool {
	return (q.writeIdx.Load() - q.readIdx.Load()) >= ringBufferSize
}

func (q *MPSCExtQueue) Reset() {
	q.writeIdx.Store(0)
	q.readIdx.Store(0)
	stub := &node{}
	q.overflowHead.Store(stub)
	q.overflowTail.Store(stub)
	q.overflowLen.Store(0)
}
