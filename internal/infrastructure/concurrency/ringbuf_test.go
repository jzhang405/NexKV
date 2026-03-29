// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package concurrency

import (
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"
)

func TestMPSCRingBuffer_Basic(t *testing.T) {
	r := NewMPSCRingBuffer()

	// 空队列
	if !r.IsEmpty() {
		t.Error("new ring should be empty")
	}
	if r.IsFull() {
		t.Error("new ring should not be full")
	}
	if size := r.Size(); size != 0 {
		t.Errorf("empty ring size = %d, want 0", size)
	}

	// 单条入队
	task := unsafe.Pointer(new(int))
	if !r.Enqueue(task) {
		t.Error("Enqueue failed on empty ring")
	}
	if r.IsEmpty() {
		t.Error("ring should not be empty after enqueue")
	}
	if size := r.Size(); size != 1 {
		t.Errorf("size = %d, want 1", size)
	}

	// Peek 查看
	got, ok := r.Peek()
	if !ok {
		t.Error("Peek failed on non-empty ring")
	}
	if got != task {
		t.Error("Peek returned wrong task")
	}
	if size := r.Size(); size != 1 {
		t.Errorf("size unchanged after Peek = %d, want 1", size)
	}

	// Commit
	r.Commit()
	if size := r.Size(); size != 0 {
		t.Errorf("size after Commit = %d, want 0", size)
	}
	if !r.IsEmpty() {
		t.Error("ring should be empty after commit")
	}

	// Dequeue 空队列
	if got := r.Dequeue(); got != nil {
		t.Error("Dequeue on empty ring should return nil")
	}
}

func TestMPSCRingBuffer_FIFO(t *testing.T) {
	r := NewMPSCRingBuffer()

	// 按顺序入队，验证 FIFO
	tasks := make([]unsafe.Pointer, 5)
	for i := range 5 {
		tasks[i] = unsafe.Pointer(&tasks[i])
		r.Enqueue(tasks[i])
	}

	// 顺序出队
	for i := range 5 {
		got, ok := r.Peek()
		if !ok {
			t.Fatalf("Peek %d failed", i)
		}
		if got != tasks[i] {
			t.Errorf("Peek[%d] = %v, want %v", i, got, tasks[i])
		}
		r.Commit()
	}

	if !r.IsEmpty() {
		t.Error("ring should be empty after all commit")
	}
}

func TestMPSCRingBuffer_Overflow(t *testing.T) {
	r := NewMPSCRingBuffer()

	// 填满队列
	for i := range ringBufferSize {
		task := unsafe.Pointer(&i)
		if !r.Enqueue(task) {
			t.Errorf("Enqueue %d failed, ring should not be full yet", i)
		}
	}

	if !r.IsFull() {
		t.Error("ring should be full")
	}

	// 继续入队应该失败
	task := unsafe.Pointer(new(int))
	if r.Enqueue(task) {
		t.Error("Enqueue should fail when ring is full")
	}
}

func TestMPSCRingBuffer_Dequeue(t *testing.T) {
	r := NewMPSCRingBuffer()

	// 入队 3 个
	for i := range 3 {
		task := unsafe.Pointer(&i)
		r.Enqueue(task)
	}

	// Dequeue 2 个
	for i := range 2 {
		got := r.Dequeue()
		if got == nil {
			t.Errorf("Dequeue %d returned nil", i)
		}
	}

	if size := r.Size(); size != 1 {
		t.Errorf("size = %d, want 1", size)
	}

	// Dequeue 剩余 1 个
	got := r.Dequeue()
	if got == nil {
		t.Error("last Dequeue returned nil")
	}

	// 再 Dequeue 应该 nil
	if got := r.Dequeue(); got != nil {
		t.Error("Dequeue on empty should return nil")
	}
}

func TestMPSCRingBuffer_CommitN(t *testing.T) {
	r := NewMPSCRingBuffer()

	// 入队 5 个
	for i := range 5 {
		r.Enqueue(unsafe.Pointer(&i))
	}

	// CommitN 3 个
	r.CommitN(3)
	if size := r.Size(); size != 2 {
		t.Errorf("size after CommitN(3) = %d, want 2", size)
	}

	// CommitN 2 个
	r.CommitN(2)
	if size := r.Size(); size != 0 {
		t.Errorf("size after CommitN(2) = %d, want 0", size)
	}
}

func TestMPSCRingBuffer_PeekN(t *testing.T) {
	r := NewMPSCRingBuffer()

	// 入队 5 个
	for i := range 5 {
		r.Enqueue(unsafe.Pointer(&i))
	}

	// PeekN 3 个
	buf := make([]unsafe.Pointer, 3)
	n := r.PeekN(buf)
	if n != 3 {
		t.Errorf("PeekN returned %d, want 3", n)
	}

	// size 不变
	if size := r.Size(); size != 5 {
		t.Errorf("size unchanged after PeekN = %d, want 5", size)
	}

	// PeekN 超过可用数量
	buf2 := make([]unsafe.Pointer, 10)
	n = r.PeekN(buf2)
	if n != 5 {
		t.Errorf("PeekN returned %d, want 5 (max available)", n)
	}
}

func TestMPSCRingBuffer_PeekN_Empty(t *testing.T) {
	r := NewMPSCRingBuffer()

	buf := make([]unsafe.Pointer, 3)
	n := r.PeekN(buf)
	if n != 0 {
		t.Errorf("PeekN on empty returned %d, want 0", n)
	}
}

func TestMPSCRingBuffer_Retry(t *testing.T) {
	r := NewMPSCRingBuffer()

	// 入队 2 个
	t1 := unsafe.Pointer(&[1]int{1})
	t2 := unsafe.Pointer(&[1]int{2})
	r.Enqueue(t1)
	r.Enqueue(t2)

	// Peek 第 1 个（不 commit）
	got, ok := r.Peek()
	if !ok {
		t.Fatal("Peek failed")
	}
	if got != t1 {
		t.Errorf("Peek = %v, want t1", got)
	}

	// size 不变
	if size := r.Size(); size != 2 {
		t.Errorf("size after Peek = %d, want 2", size)
	}

	// 再次 Peek，还是同一个（因为没 commit）
	got2, ok2 := r.Peek()
	if !ok2 {
		t.Fatal("Peek 2 failed")
	}
	if got2 != t1 {
		t.Errorf("Peek 2 = %v, want t1 (not advanced)", got2)
	}

	// Commit 后再 Peek，得到 t2
	r.Commit()
	got3, ok3 := r.Peek()
	if !ok3 {
		t.Fatal("Peek 3 failed")
	}
	if got3 != t2 {
		t.Errorf("Peek 3 = %v, want t2", got3)
	}
}

func TestMPSCRingBuffer_RetryBatch(t *testing.T) {
	r := NewMPSCRingBuffer()

	// 入队 5 个
	tasks := make([]unsafe.Pointer, 5)
	for i := range 5 {
		tasks[i] = unsafe.Pointer(&i)
		r.Enqueue(tasks[i])
	}

	buf := make([]unsafe.Pointer, 5)
	n := r.PeekN(buf)
	if n != 5 {
		t.Fatalf("PeekN returned %d, want 5", n)
	}

	// 假设第 3 个任务失败，前 2 个成功
	// Commit 已处理的前 2 个
	r.CommitN(2)

	// 重新 PeekN，应该还是从第 3 个开始
	buf2 := make([]unsafe.Pointer, 5)
	n2 := r.PeekN(buf2)
	if n2 != 3 {
		t.Errorf("PeekN after partial commit = %d, want 3 remaining", n2)
	}
	if buf2[0] != tasks[2] {
		t.Errorf("PeekN[0] = %v, want tasks[2] = %v", buf2[0], tasks[2])
	}
}

func TestMPSCRingBuffer_ConcurrentEnqueue(t *testing.T) {
	r := NewMPSCRingBuffer()

	const goroutines = 10
	const perGoroutine = 100
	var submitted atomic.Int64
	var failed atomic.Int64

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := range goroutines {
		go func(id int) {
			defer wg.Done()
			for i := range perGoroutine {
				task := unsafe.Pointer(&[2]int{id, i})
				if r.Enqueue(task) {
					submitted.Add(1)
				} else {
					failed.Add(1)
				}
			}
		}(g)
	}

	wg.Wait()

	// 所有入队都应该成功（队列足够大）
	if failed.Load() != 0 {
		t.Errorf("%d enqueues failed (ring too small)", failed.Load())
	}
	if submitted.Load() != goroutines*perGoroutine {
		t.Errorf("submitted = %d, want %d", submitted.Load(), goroutines*perGoroutine)
	}

	// 验证总数量
	if size := r.Size(); size != goroutines*perGoroutine {
		t.Errorf("size = %d, want %d", size, goroutines*perGoroutine)
	}

	// 验证 FIFO：全部出队
	count := 0
	for {
		if r.Dequeue() == nil {
			break
		}
		count++
	}
	if count != goroutines*perGoroutine {
		t.Errorf("dequeued %d, want %d", count, goroutines*perGoroutine)
	}
}

func TestMPSCRingBuffer_ConcurrentEnqueueDequeue(t *testing.T) {
	r := NewMPSCRingBuffer()

	const producers = 8
	const itemsPerProducer = 100
	var totalEnqueued atomic.Int64
	var totalDequeued atomic.Int64
	var done atomic.Bool

	var producerWg sync.WaitGroup
	var consumerWg sync.WaitGroup

	// 启动消费者：spin-wait 直到 done && IsEmpty
	consumerWg.Add(1)
	go func() {
		defer consumerWg.Done()
		for {
			got := r.Dequeue()
			if got == nil {
				if done.Load() && r.IsEmpty() {
					break
				}
				continue
			}
			totalDequeued.Add(1)
		}
	}()

	// 启动生产者
	for p := range producers {
		producerWg.Add(1)
		go func(id int) {
			defer producerWg.Done()
			for i := range itemsPerProducer {
				task := unsafe.Pointer(&[2]int{id, i})
				if r.Enqueue(task) {
					totalEnqueued.Add(1)
				}
			}
		}(p)
	}

	producerWg.Wait()
	done.Store(true)

	// 等待消费者处理完所有入队的 items
	consumerWg.Wait()

	if totalEnqueued.Load() != int64(producers*itemsPerProducer) {
		t.Errorf("enqueued = %d, want %d", totalEnqueued.Load(), producers*itemsPerProducer)
	}
	if totalDequeued.Load() != totalEnqueued.Load() {
		t.Errorf("dequeued = %d, enqueued = %d", totalDequeued.Load(), totalEnqueued.Load())
	}
}

func TestMPSCRingBuffer_EnqueueN(t *testing.T) {
	r := NewMPSCRingBuffer()

	batch := make([]unsafe.Pointer, 5)
	for i := range 5 {
		batch[i] = unsafe.Pointer(&i)
	}

	n := r.EnqueueN(batch)
	if n != 5 {
		t.Errorf("EnqueueN returned %d, want 5", n)
	}

	if size := r.Size(); size != 5 {
		t.Errorf("size = %d, want 5", size)
	}

	// 出队验证
	count := 0
	for {
		if r.Dequeue() == nil {
			break
		}
		count++
	}
	if count != 5 {
		t.Errorf("dequeued %d, want 5", count)
	}
}

func TestMPSCRingBuffer_EnqueueN_Partial(t *testing.T) {
	r := NewMPSCRingBuffer()

	// 先入队填满大部分
	for i := range ringBufferSize - 3 {
		r.Enqueue(unsafe.Pointer(&i))
	}

	// 尝试批量入队 10 个，应该只能入 3 个
	batch := make([]unsafe.Pointer, 10)
	for i := range 10 {
		batch[i] = unsafe.Pointer(&i)
	}

	n := r.EnqueueN(batch)
	if n != 3 {
		t.Errorf("EnqueueN returned %d, want 3 (remaining space)", n)
	}
}

func TestMPSCRingBuffer_DequeueN(t *testing.T) {
	r := NewMPSCRingBuffer()

	// 入队 10 个
	for i := range 10 {
		r.Enqueue(unsafe.Pointer(&i))
	}

	// 批量出队 3 个
	buf := make([]unsafe.Pointer, 3)
	n := r.DequeueN(buf)
	if n != 3 {
		t.Errorf("DequeueN returned %d, want 3", n)
	}

	if size := r.Size(); size != 7 {
		t.Errorf("size = %d, want 7", size)
	}
}

func TestMPSCRingBuffer_DequeueN_Empty(t *testing.T) {
	r := NewMPSCRingBuffer()

	buf := make([]unsafe.Pointer, 3)
	n := r.DequeueN(buf)
	if n != 0 {
		t.Errorf("DequeueN on empty returned %d, want 0", n)
	}
}

// ==================== 基准测试 ====================

// Peek + Commit（单条）：模拟 Scheduler runLoop 的热路径
func BenchmarkMPSCRingBuffer_PeekCommit(b *testing.B) {
	r := NewMPSCRingBuffer()
	task := unsafe.Pointer(&[1]int{0})

	// 预填队列
	for range ringBufferSize / 2 {
		r.Enqueue(task)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for range b.N {
		if t, ok := r.Peek(); ok {
			r.Commit()
			_ = t
		}
	}
}

// PeekN + CommitN（批量）：模拟批量处理场景
func BenchmarkMPSCRingBuffer_PeekNCommit(b *testing.B) {
	r := NewMPSCRingBuffer()
	task := unsafe.Pointer(&[1]int{0})

	// 预填队列
	for range ringBufferSize / 2 {
		r.Enqueue(task)
	}

	buf := make([]unsafe.Pointer, 64)

	b.ResetTimer()
	b.ReportAllocs()

	for range b.N {
		n := r.PeekN(buf)
		r.CommitN(uint64(n))
	}
}
