// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package concurrency

import (
	"sync/atomic"
	"unsafe"
)

// ==========================================
// 配置
// ==========================================

const (
	// ringBufferSize 环形缓冲区大小（必须是 2 的幂）
	ringBufferSize = 1024
	// ringMask 取模掩码（ringBufferSize - 1，要求 2^n）
	ringMask = ringBufferSize - 1
	// cacheLineSize CPU 缓存行大小（防止伪共享）
	cacheLineSize = 64
)

// ==========================================
// 缓存行对齐结构（杜绝伪共享）
// ==========================================

type taskPtr struct {
	ptr unsafe.Pointer
}

type paddedTask struct {
	task atomic.Value
	_    [cacheLineSize - 8]byte
}

// ==========================================
// MPSC 无锁环形缓冲区
//
// Multi-Producer: 多个 goroutine 并发 Enqueue（通过 CAS 抢占 slot）
// Single-Consumer: 单个 goroutine 串行 Peek/Dequeue（无需 CAS）
//
// 核心原理：
//   - writeIdx: 原子 CAS（多生产者竞争）
//   - readIdx:  单线程修改（无需同步）
//   - slot 在 write 后、read 前这段时间内被独占，无竞争
// ==========================================

type MPSCRingBuffer struct {
	// === 生产者端（多 goroutine 竞争）===
	writeIdx atomic.Uint64
	_        [cacheLineSize]byte

	// === 消费者端（单 goroutine，无竞争）===
	readIdx atomic.Uint64
	_       [cacheLineSize]byte

	// 数据区（每个 slot 独占一个缓存行）
	buffer [ringBufferSize]paddedTask
}

// NewMPSCRingBuffer 创建 MPSC 环形缓冲区
func NewMPSCRingBuffer() *MPSCRingBuffer {
	return &MPSCRingBuffer{}
}

// ==========================================
// MP: 多生产者入队（多 goroutine 安全）
// ==========================================

// Enqueue 单条入队
//
// 多生产者安全：CAS 抢占 slot
// 返回值：
//   - true:  入队成功
//   - false: 队列满
func (r *MPSCRingBuffer) Enqueue(task unsafe.Pointer) bool {
	for {
		read := r.readIdx.Load()
		write := r.writeIdx.Load()

		// 队列满（write 领先 read 超过 buffer 容量）
		if write-read >= ringBufferSize {
			return false
		}

		// CAS 抢占下一个可用 slot
		if r.writeIdx.CompareAndSwap(write, write+1) {
			r.buffer[write&ringMask].task.Store(taskPtr{ptr: task})
			return true
		}
		// CAS 失败：其他生产者抢占了，循环重试
	}
}

// EnqueueN 批量入队
//
// 原子抢占连续 k 个 slot，然后批量写入。
// 返回实际写入的数量（可能小于请求数量）。
func (r *MPSCRingBuffer) EnqueueN(tasks []unsafe.Pointer) int {
	n := len(tasks)
	if n == 0 {
		return 0
	}

	read := r.readIdx.Load()
	write := r.writeIdx.Load()
	free := ringBufferSize - (write - read)
	if free == 0 {
		return 0
	}

	k := n
	if int(free) < k {
		k = int(free)
	}

	// 原子抢占连续 k 个 slot
	old := r.writeIdx.Add(uint64(k)) - uint64(k)

	// 批量写入
	for i := 0; i < k; i++ {
		pos := (old + uint64(i)) & ringMask
		r.buffer[pos].task.Store(taskPtr{ptr: tasks[i]})
	}

	return k
}

// ==========================================
// SC: 单消费者 Peek（只读不消费）
// ==========================================

// Peek 查看队首元素（只读，不移动 readIdx）
//
// 单消费者安全：readIdx 仅由此 goroutine 修改，无竞争。
// 返回值：
//   - (task, true):  查看成功
//   - (nil, false):  队列空
func (r *MPSCRingBuffer) Peek() (unsafe.Pointer, bool) {
	write := r.writeIdx.Load()
	read := r.readIdx.Load()

	if write == read {
		return nil, false
	}

	task := r.buffer[read&ringMask].task.Load().(taskPtr).ptr
	if task != nil {
		return task, true
	}
	return nil, false
}

// PeekN 批量查看（只读，不移动 readIdx）
//
// 单消费者安全。
// 参数 out：预分配的输出缓冲区（避免每次分配）
// 返回值：
//   - > 0:  实际查看到的元素数量
//   - 0:    队列空
func (r *MPSCRingBuffer) PeekN(out []unsafe.Pointer) int {
	write := r.writeIdx.Load()
	read := r.readIdx.Load()
	avail := write - read
	if avail == 0 {
		return 0
	}

	k := len(out)
	if uint64(k) > avail {
		k = int(avail)
	}

	for i := 0; i < k; i++ {
		pos := (read + uint64(i)) & ringMask
		out[i] = r.buffer[pos].task.Load().(taskPtr).ptr
	}
	return k
}

// ==========================================
// SC: 单消费者 Commit（确认消费，推进读指针）
// ==========================================

// Commit 确认消费 1 个元素（推进 readIdx）
//
// 单消费者安全：直接 Add 无需 CAS。
func (r *MPSCRingBuffer) Commit() {
	r.readIdx.Add(1)
}

// CommitN 批量确认消费（推进 readIdx）
//
// 单消费者安全。
func (r *MPSCRingBuffer) CommitN(n uint64) {
	r.readIdx.Add(n)
}

// ==========================================
// SC: 快速出队（fire-and-forget，无重试）
// ==========================================

// Dequeue 出队 1 个元素（消费并推进 readIdx）
//
// 单消费者安全。
// 返回值：
//   - task:  出队成功
//   - nil:   队列空
func (r *MPSCRingBuffer) Dequeue() unsafe.Pointer {
	write := r.writeIdx.Load()
	read := r.readIdx.Load()
	if write == read {
		return nil
	}

	taskVal := r.buffer[read&ringMask].task.Load()
	if taskVal == nil {
		return nil
	}
	task := taskVal.(taskPtr).ptr
	r.readIdx.Store(read + 1)
	return task
}

// DequeueN 批量出队（消费并推进 readIdx）
//
// 单消费者安全。
// 参数 out：预分配的输出缓冲区。
// 返回值：实际出队的元素数量。
func (r *MPSCRingBuffer) DequeueN(out []unsafe.Pointer) int {
	n := len(out)
	if n == 0 {
		return 0
	}

	write := r.writeIdx.Load()
	read := r.readIdx.Load()
	avail := write - read
	if avail == 0 {
		return 0
	}

	k := n
	if uint64(k) > avail {
		k = int(avail)
	}

	for i := 0; i < k; i++ {
		pos := (read + uint64(i)) & ringMask
		out[i] = r.buffer[pos].task.Load().(taskPtr).ptr
	}

	r.readIdx.Store(read + uint64(k))
	return k
}

// ==========================================
// 状态查询
// ==========================================

// Size 返回当前队列长度
func (r *MPSCRingBuffer) Size() uint64 {
	return r.writeIdx.Load() - r.readIdx.Load()
}

// IsEmpty 判断队列是否为空
func (r *MPSCRingBuffer) IsEmpty() bool {
	return r.Size() == 0
}

// IsFull 判断队列是否已满
func (r *MPSCRingBuffer) IsFull() bool {
	return r.Size() >= ringBufferSize
}
