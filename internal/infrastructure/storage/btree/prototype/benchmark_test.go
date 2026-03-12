package prototype

import (
	"sync/atomic"
	"testing"
)

// Benchmark_DirectPointer_Read 基准测试：直接指针读取
// 作为对比基准，测试直接通过指针访问的性能
func Benchmark_DirectPointer_Read(b *testing.B) {
	page := NewPage(1)
	ptr := &struct {
		page *Page
	}{page: page}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ptr.page // 直接访问，无原子操作
	}
}

// Benchmark_AtomicPointer_Read 基准测试：原子指针读取
// 测试目标：验证 atomic.Pointer.Load() 的开销
// 成功标准：<100ns/op（可接受），<50ns/op（优秀）
func Benchmark_AtomicPointer_Read(b *testing.B) {
	page := NewPage(1)
	ref := NewPageReferenceWithPage(page)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ref.GetPage() // 原子加载
	}
}

// Benchmark_AtomicPointer_GetPageInfo 基准测试：获取 PageInfo
// 测试获取完整 PageInfo 的性能
func Benchmark_AtomicPointer_GetPageInfo(b *testing.B) {
	page := NewPage(1)
	ref := NewPageReferenceWithPage(page)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ref.GetPageInfo() // 原子加载 PageInfo
	}
}

// Benchmark_AtomicPointer_CAS 基准测试：CAS 操作
// 测试 CompareAndSwap 的性能
// 成功标准：<200ns/op
func Benchmark_AtomicPointer_CAS(b *testing.B) {
	oldInfo := NewPageInfo(NewPage(1))
	newInfo := NewPageInfo(NewPage(2))
	ref := NewPageReference()
	ref.pInfo.Store(oldInfo)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// CAS 操作
		ref.pInfo.CompareAndSwap(oldInfo, newInfo)
		// 交换以便下次 CAS 成功
		oldInfo, newInfo = newInfo, oldInfo
	}
}

// Benchmark_AtomicPointer_UpdatePage 基准测试：更新页面
// 测试 UpdatePage 方法的性能（包含重试逻辑）
func Benchmark_AtomicPointer_UpdatePage(b *testing.B) {
	ref := NewPageReferenceWithPage(NewPage(1))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		newPage := NewPage(i + 10)
		ref.UpdatePage(newPage)
	}
}

// Benchmark_PageReference_ConcurrentRead 基准测试：并发读取
// 测试高并发场景下的读取性能
// 成功标准：>8M ops/sec
func Benchmark_PageReference_ConcurrentRead(b *testing.B) {
	ref := NewPageReferenceWithPage(NewPage(1))

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = ref.GetPage()
		}
	})
}

// Benchmark_PageReference_ConcurrentWrite 基准测试：并发写入
// 测试高并发场景下的 CAS 更新性能
func Benchmark_PageReference_ConcurrentWrite(b *testing.B) {
	ref := NewPageReferenceWithPage(NewPage(1))
	var counter atomic.Int64

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			id := int(counter.Add(1))
			newPage := NewPage(id)
			ref.UpdatePage(newPage)
		}
	})
}

// Benchmark_PageReference_MixedReadWrite 基准测试：混合读写
// 模拟真实场景：90% 读，10% 写
// 成功标准：>5M ops/sec
func Benchmark_PageReference_MixedReadWrite(b *testing.B) {
	ref := NewPageReferenceWithPage(NewPage(1))
	var counter atomic.Int64

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			// 10% 概率写，90% 概率读
			if counter.Add(1)%10 == 0 {
				newPage := NewPage(int(counter.Load()))
				ref.UpdatePage(newPage)
			} else {
				_ = ref.GetPage()
			}
		}
	})
}

// Benchmark_PageInfo_Clone 基准测试：Clone 操作
// 测试 Copy-on-Write 的性能
func Benchmark_PageInfo_Clone(b *testing.B) {
	page := NewPage(1)
	page.Keys = [][]byte{[]byte("key1"), []byte("key2")}
	page.Values = [][]byte{[]byte("value1"), []byte("value2")}
	info := NewPageInfo(page)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = info.Clone()
	}
}

// Benchmark_Page_Create 基准测试：创建页面
// 作为性能基线
func Benchmark_Page_Create(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = NewPage(i)
	}
}

// Benchmark_DirectPointer_vs_AtomicPointer 对比测试
// 直接对比两种实现方式
func Benchmark_DirectPointer_vs_AtomicPointer(b *testing.B) {
	b.Run("DirectPointer", func(b *testing.B) {
		page := NewPage(1)
		ptr := &struct {
			page *Page
		}{page: page}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = ptr.page
		}
	})

	b.Run("AtomicPointer", func(b *testing.B) {
		page := NewPage(1)
		ref := NewPageReferenceWithPage(page)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = ref.GetPage()
		}
	})
}
