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

func TestMPSCExtQueue_Basic(t *testing.T) {
	r := NewMPSCExtQueue()

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

	// Dequeue
	r.Dequeue()
	if size := r.Size(); size != 0 {
		t.Errorf("size after Dequeue = %d, want 0", size)
	}
	if !r.IsEmpty() {
		t.Error("ring should be empty after dequeue")
	}

	// Dequeue 空队列
	if got, ok := r.Dequeue(); ok || got != nil {
		t.Error("Dequeue on empty ring should return nil, false")
	}
}

func TestMPSCExtQueue_FIFO(t *testing.T) {
	r := NewMPSCExtQueue()

	// 按顺序入队，验证 FIFO
	tasks := make([]unsafe.Pointer, 5)
	for i := range 5 {
		tasks[i] = unsafe.Pointer(&tasks[i])
		r.Enqueue(tasks[i])
	}

	// 顺序出队
	for i := range 5 {
		got, ok := r.Dequeue()
		if !ok {
			t.Fatalf("Dequeue %d returned false", i)
		}
		if got != tasks[i] {
			t.Errorf("Dequeue[%d] = %v, want %v", i, got, tasks[i])
		}
	}

	if !r.IsEmpty() {
		t.Error("ring should be empty after all commit")
	}
}

func TestMPSCExtQueue_Overflow(t *testing.T) {
	r := NewMPSCExtQueue()

	// 填满数组
	for i := range ringBufferSize {
		task := unsafe.Pointer(&i)
		if !r.Enqueue(task) {
			t.Errorf("Enqueue %d failed, array should not be full yet", i)
		}
	}

	if !r.IsFull() {
		t.Error("array should be full")
	}

	// 继续入队应该成功（溢出到链表）
	overflowTask := unsafe.Pointer(new(int))
	if !r.Enqueue(overflowTask) {
		t.Error("Enqueue should succeed via overflow")
	}

	// 队列总大小 = 数组 + 溢出
	if size := r.Size(); size != ringBufferSize+1 {
		t.Errorf("size = %d, want %d (array + 1 overflow)", size, ringBufferSize+1)
	}

	// 消费所有 items，包括溢出的
	count := 0
	for {
		got, ok := r.Dequeue()
		if !ok {
			break
		}
		_ = got
		count++
	}
	if count != ringBufferSize+1 {
		t.Errorf("dequeued %d, want %d", count, ringBufferSize+1)
	}
}

func TestMPSCExtQueue_Dequeue(t *testing.T) {
	r := NewMPSCExtQueue()

	// 入队 3 个
	for i := range 3 {
		task := unsafe.Pointer(&i)
		r.Enqueue(task)
	}

	// Dequeue 2 个
	for i := range 2 {
		got, ok := r.Dequeue()
		if !ok {
			t.Errorf("Dequeue %d returned false", i)
		}
		_ = got
	}

	if size := r.Size(); size != 1 {
		t.Errorf("size = %d, want 1", size)
	}

	// Dequeue 剩余 1 个
	got, ok := r.Dequeue()
	if !ok {
		t.Error("last Dequeue returned false")
	}
	_ = got

	// 再 Dequeue 应该 nil
	if got, ok := r.Dequeue(); ok || got != nil {
		t.Error("Dequeue on empty should return nil, false")
	}
}

func TestMPSCExtQueue_DequeueN_Basic(t *testing.T) {
	r := NewMPSCExtQueue()

	// 入队 5 个
	for i := range 5 {
		r.Enqueue(unsafe.Pointer(&i))
	}

	// DequeueN 3 个
	buf := make([]unsafe.Pointer, 3)
	n := r.DequeueN(buf)
	if n != 3 {
		t.Errorf("DequeueN returned %d, want 3", n)
	}
	if size := r.Size(); size != 2 {
		t.Errorf("size after DequeueN(3) = %d, want 2", size)
	}

	// DequeueN 2 个
	buf2 := make([]unsafe.Pointer, 2)
	n2 := r.DequeueN(buf2)
	if n2 != 2 {
		t.Errorf("DequeueN returned %d, want 2", n2)
	}
	if size := r.Size(); size != 0 {
		t.Errorf("size after DequeueN(2) = %d, want 0", size)
	}
}

func TestMPSCExtQueue_PeekN(t *testing.T) {
	r := NewMPSCExtQueue()

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

func TestMPSCExtQueue_PeekN_Empty(t *testing.T) {
	r := NewMPSCExtQueue()

	buf := make([]unsafe.Pointer, 3)
	n := r.PeekN(buf)
	if n != 0 {
		t.Errorf("PeekN on empty returned %d, want 0", n)
	}
}

func TestMPSCExtQueue_Retry(t *testing.T) {
	r := NewMPSCExtQueue()

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

	// Dequeue 后再 Peek，得到 t2
	r.Dequeue()
	got3, ok3 := r.Peek()
	if !ok3 {
		t.Fatal("Peek 3 failed")
	}
	if got3 != t2 {
		t.Errorf("Peek 3 = %v, want t2", got3)
	}
}

func TestMPSCExtQueue_RetryBatch(t *testing.T) {
	r := NewMPSCExtQueue()

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
	// DequeueN 已处理的前 2 个
	r.DequeueN(buf[:2])

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

func TestMPSCExtQueue_ConcurrentEnqueue(t *testing.T) {
	r := NewMPSCExtQueue()

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
		got, ok := r.Dequeue()
		if !ok {
			break
		}
		_ = got
		count++
	}
	if count != goroutines*perGoroutine {
		t.Errorf("dequeued %d, want %d", count, goroutines*perGoroutine)
	}
}

func TestMPSCExtQueue_ConcurrentEnqueueDequeue(t *testing.T) {
	r := NewMPSCExtQueue()

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
			got, ok := r.Dequeue()
			if !ok {
				if done.Load() && r.IsEmpty() {
					break
				}
				continue
			}
			_ = got
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

func TestMPSCExtQueue_DequeueN_Partial(t *testing.T) {
	r := NewMPSCExtQueue()

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

func TestMPSCExtQueue_DequeueN_Empty(t *testing.T) {
	r := NewMPSCExtQueue()

	buf := make([]unsafe.Pointer, 3)
	n := r.DequeueN(buf)
	if n != 0 {
		t.Errorf("DequeueN on empty returned %d, want 0", n)
	}
}

// ==================== 基准测试 ====================

// Peek + Dequeue（单条）：模拟 Scheduler runLoop 的热路径
func BenchmarkMPSCExtQueue_PeekDequeue(b *testing.B) {
	r := NewMPSCExtQueue()
	task := unsafe.Pointer(&[1]int{0})

	// 预填队列
	for range ringBufferSize / 2 {
		r.Enqueue(task)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for range b.N {
		if t, ok := r.Peek(); ok {
			r.Dequeue()
			_ = t
		}
	}
}

// PeekN + DequeueN（批量）：模拟批量处理场景
func BenchmarkMPSCExtQueue_PeekNDequeueN(b *testing.B) {
	r := NewMPSCExtQueue()
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
		r.DequeueN(buf[:n])
	}
}
