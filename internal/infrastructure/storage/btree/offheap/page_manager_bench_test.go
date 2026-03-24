// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package offheap

import (
	"sync"
	"testing"
	"unsafe"
)

// Baseline 1: 当前 sync.Pool 方式
func BenchmarkSyncPool_AllocFree(b *testing.B) {
	var pool sync.Pool
	pool.New = func() any {
		buf := make([]byte, PageSize)
		return &buf
	}

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			buf := pool.Get().(*[]byte)
			// 模拟使用
			_ = (*buf)[0]
			pool.Put(buf)
		}
	})
}

// Baseline 2: sync.Mutex freeList（对比锁方案）
func BenchmarkMutexFreeList_AllocFree(b *testing.B) {
	type MutexFreeList struct {
		mu       sync.Mutex
		freeList []uint32
	}
	fl := &MutexFreeList{freeList: make([]uint32, 1000)}

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			fl.mu.Lock()
			if len(fl.freeList) > 0 {
				id := fl.freeList[len(fl.freeList)-1]
				fl.freeList = fl.freeList[:len(fl.freeList)-1]
				fl.freeList = append(fl.freeList, id)
			}
			fl.mu.Unlock()
		}
	})
}

// Baseline 3: Go 堆直接分配
func BenchmarkGoHeap_Alloc(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			buf := make([]byte, PageSize)
			_ = buf[0]
		}
	})
}

// Off-Heap mmap + lock-free 队列方式
func BenchmarkLockFreeQueue_AllocFree(b *testing.B) {
	pm, err := NewPageManager(64 << 20)
	if err != nil {
		b.Fatal(err)
	}
	defer pm.Close()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			// 分配
			pageID, err := pm.Alloc()
			if err != nil {
				b.Fatal(err)
			}
			// 模拟使用
			ptr := pm.PageIDToPtr(pageID)
			slice := unsafe.Slice((*byte)(unsafe.Pointer(ptr)), PageSize)
			slice[0] = 42
			// 释放
			pm.Free(pageID)
		}
	})
}

// 首次访问缺页测试
func BenchmarkMmap_FirstAccess(b *testing.B) {
	pm, err := NewPageManager(64 << 20) // 64MB = 16384 pages
	if err != nil {
		b.Fatal(err)
	}
	defer pm.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// 循环使用前 100 个页面
		pageID := uint32(i % 100)
		// 分配（如果需要）
		if pageID >= pm.GetStats().Total {
			b.Fatal("ran out of pages")
		}
		// 首次访问会触发缺页
		ptr := pm.PageIDToPtr(pageID)
		slice := unsafe.Slice((*byte)(unsafe.Pointer(ptr)), PageSize)
		slice[0] = 42
	}
}

// 高并发压力测试
func BenchmarkMmap_HighConcurrency(b *testing.B) {
	pm, err := NewPageManager(64 << 20)
	if err != nil {
		b.Fatal(err)
	}
	defer pm.Close()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		// 每个 goroutine 分配不同的页面
		for pb.Next() {
			pageID, err := pm.Alloc()
			if err != nil {
				b.Fatal(err)
			}
			ptr := pm.PageIDToPtr(pageID)
			slice := unsafe.Slice((*byte)(unsafe.Pointer(ptr)), PageSize)
			slice[0] = 42
			pm.Free(pageID)
		}
	})
}

// PageIDToPtr 性能测试
func BenchmarkPageIDToPtr(b *testing.B) {
	pm, err := NewPageManager(64 << 20)
	if err != nil {
		b.Fatal(err)
	}
	defer pm.Close()

	// 预先分配一个页面
	pageID, _ := pm.Alloc()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ptr := pm.PageIDToPtr(pageID)
		slice := unsafe.Slice((*byte)(unsafe.Pointer(ptr)), 1)
		_ = slice[0]
	}
}

// LockFreeQueue 专用基准测试
func BenchmarkLockFreeQueue_Only(b *testing.B) {
	q := NewLockFreeQueue()

	// 预先填充队列
	for i := uint32(0); i < 1000; i++ {
		q.Enqueue(i)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			q.Enqueue(42)
			_, _ = q.Dequeue()
		}
	})
}
