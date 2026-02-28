// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ==========================================
// 内存访问模式优化示例
// ==========================================
// 本文件演示如何优化内存访问模式以提高缓存命中率
// 配合 perf 工具使用，对比优化前后的性能差异

// ==========================================
// ❌ 反模式：共享内存导致缓存竞争
// ==========================================

// BadSharedData 所有 worker 共享同一数据，导致严重的缓存失效
type BadSharedData struct {
	mu    sync.Mutex
	data  [1024]int // 共享数据
	count int64
}

func BadSharedDataWorkerExample() {
	shared := &BadSharedData{}

	// 多个 worker 竞争访问共享数据
	for i := 0; i < runtime.NumCPU(); i++ {
		go func() {
			for {
				shared.mu.Lock()
				for j := 0; j < len(shared.data); j++ {
					shared.data[j]++ // ❌ 导致其他核心的缓存失效
				}
				shared.count++
				shared.mu.Unlock()
			}
		}()
	}
}

// ==========================================
// ✅ 正确模式：核心本地数据
// ==========================================

// GoodCoreLocalData 每个核心有独立数据，缓存友好
type GoodCoreLocalData struct {
	workers []*CoreLocalWorker
}

type CoreLocalWorker struct {
	coreID int
	data   [1024]int // 核心本地数据
	count  int64
}

func NewGoodCoreLocalData(numCores int) *GoodCoreLocalData {
	g := &GoodCoreLocalData{
		workers: make([]*CoreLocalWorker, numCores),
	}
	for i := 0; i < numCores; i++ {
		g.workers[i] = &CoreLocalWorker{
			coreID: i,
		}
	}
	return g
}

func (g *GoodCoreLocalData) GetWorker(coreID int) *CoreLocalWorker {
	return g.workers[coreID%len(g.workers)]
}

func GoodCoreLocalDataWorkerExample() {
	g := NewGoodCoreLocalData(runtime.NumCPU())

	// 每个 worker 访问自己的数据，无缓存竞争
	for i := 0; i < runtime.NumCPU(); i++ {
		worker := g.GetWorker(i)
		go func(w *CoreLocalWorker) {
			for {
				for j := 0; j < len(w.data); j++ {
					w.data[j]++ // ✅ 缓存友好，仅在本地核心缓存中
				}
				atomic.AddInt64(&w.count, 1)
			}
		}(worker)
	}
}

// ==========================================
// ❌ 反模式：伪共享（False Sharing）
// ==========================================

// BadCounter 结构体字段在同一缓存行（64 字节）
type BadCounter struct {
	value1 int64 // 与 value2 可能在同一缓存行
	value2 int64
}

func (c *BadCounter) Increment1() {
	atomic.AddInt64(&c.value1, 1) // ❌ 导致 value2 的缓存失效
}

func (c *BadCounter) Increment2() {
	atomic.AddInt64(&c.value2, 1) // ❌ 导致 value1 的缓存失效
}

// ==========================================
// ✅ 正确模式：避免伪共享
// ==========================================

// GoodCounter 使用 padding 避免伪共享
type GoodCounter struct {
	value1 int64
	_      [7]int64 // 填充一个缓存行（64 字节）
	value2 int64
}

func (c *GoodCounter) Increment1() {
	atomic.AddInt64(&c.value1, 1) // ✅ 不影响 value2 的缓存
}

func (c *GoodCounter) Increment2() {
	atomic.AddInt64(&c.value2, 1) // ✅ 不影响 value1 的缓存
}

// ==========================================
// 性能对比测试
// ==========================================

// BenchmarkBadSharedData 共享内存（性能差）
func BenchmarkBadSharedData(b *testing.B) {
	executor, _ := NewPerCoreExecutor(
		WithNumCores(runtime.NumCPU()),
		WithQueueSize(10000),
	)

	shared := &BadSharedData{}

	task := func(ctx context.Context) {
		shared.mu.Lock()
		for i := 0; i < 100; i++ {
			shared.data[i]++
		}
		shared.count++
		shared.mu.Unlock()
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = executor.Submit(context.Background(), task)
	}
}

// BenchmarkGoodCoreLocalData 核心本地数据（性能好）
func BenchmarkGoodCoreLocalData(b *testing.B) {
	executor, _ := NewPerCoreExecutor(
		WithNumCores(runtime.NumCPU()),
		WithQueueSize(10000),
	)

	g := NewGoodCoreLocalData(runtime.NumCPU())

	task := func(ctx context.Context) {
		// 模拟获取当前 worker 的 coreID（简化实现）
		coreID := int(time.Now().UnixNano()) % runtime.NumCPU()
		worker := g.GetWorker(coreID)

		for i := 0; i < 100; i++ {
			worker.data[i]++
		}
		atomic.AddInt64(&worker.count, 1)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = executor.Submit(context.Background(), task)
	}
}

// BenchmarkBadFalseSharing 伪共享（性能差）
func BenchmarkBadFalseSharing(b *testing.B) {
	executor, _ := NewPerCoreExecutor(
		WithNumCores(runtime.NumCPU()),
		WithQueueSize(10000),
	)

	counter := &BadCounter{}

	task := func(ctx context.Context) {
		counter.Increment1()
		counter.Increment2()
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = executor.Submit(context.Background(), task)
	}
}

// BenchmarkGoodNoFalseSharing 避免伪共享（性能好）
func BenchmarkGoodNoFalseSharing(b *testing.B) {
	executor, _ := NewPerCoreExecutor(
		WithNumCores(runtime.NumCPU()),
		WithQueueSize(10000),
	)

	counter := &GoodCounter{}

	task := func(ctx context.Context) {
		counter.Increment1()
		counter.Increment2()
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = executor.Submit(context.Background(), task)
	}
}

// ==========================================
// 实际应用示例：WAL 写入优化
// ==========================================

// BadWALWriter 所有 worker 共享写入缓冲区
type BadWALWriter struct {
	mu     sync.Mutex
	buffer []byte
}

func (w *BadWALWriter) Write(data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.buffer = append(w.buffer, data...)
	if len(w.buffer) >= 4096 {
		// 模拟写入 WAL
		w.flush()
	}
	return nil
}

func (w *BadWALWriter) flush() {
	// 写入底层存储
	w.buffer = w.buffer[:0]
}

// GoodWALWriter 每个 worker 独立写入缓冲区
type GoodWALWriter struct {
	coreID int
	buffer []byte
	wal    any
	mu     sync.Mutex // 仅在 flush 时使用
}

func NewGoodWALWriter(coreID int, wal any) *GoodWALWriter {
	return &GoodWALWriter{
		coreID: coreID,
		buffer: make([]byte, 0, 4096),
		wal:    wal,
	}
}

func (w *GoodWALWriter) Write(data []byte) error {
	w.buffer = append(w.buffer, data...)
	if len(w.buffer) >= cap(w.buffer) {
		w.flush()
	}
	return nil
}

func (w *GoodWALWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()

	// 写入底层存储
	fmt.Printf("Core %d flushing %d bytes\n", w.coreID, len(w.buffer))
	w.buffer = w.buffer[:0]
}

// WALWriterManager 管理多个 WAL writer
type WALWriterManager struct {
	writers []*GoodWALWriter
}

func NewWALWriterManager(numCores int, wal any) *WALWriterManager {
	m := &WALWriterManager{
		writers: make([]*GoodWALWriter, numCores),
	}
	for i := 0; i < numCores; i++ {
		m.writers[i] = NewGoodWALWriter(i, wal)
	}
	return m
}

func (m *WALWriterManager) GetWriter(coreID int) *GoodWALWriter {
	return m.writers[coreID%len(m.writers)]
}

// BenchmarkBadWALWriter 共享缓冲区（性能差）
func BenchmarkBadWALWriter(b *testing.B) {
	executor, _ := NewPerCoreExecutor(
		WithNumCores(runtime.NumCPU()),
		WithQueueSize(10000),
	)

	wal := &BadWALWriter{buffer: make([]byte, 0, 4096)}

	task := func(ctx context.Context) {
		data := make([]byte, 128)
		_ = wal.Write(data)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = executor.Submit(context.Background(), task)
	}
}

// BenchmarkGoodWALWriter 独立缓冲区（性能好）
func BenchmarkGoodWALWriter(b *testing.B) {
	executor, _ := NewPerCoreExecutor(
		WithNumCores(runtime.NumCPU()),
		WithQueueSize(10000),
	)

	manager := NewWALWriterManager(runtime.NumCPU(), nil)

	task := func(ctx context.Context) {
		coreID := int(time.Now().UnixNano()) % runtime.NumCPU()
		writer := manager.GetWriter(coreID)

		data := make([]byte, 128)
		_ = writer.Write(data)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = executor.Submit(context.Background(), task)
	}
}

// ==========================================
// 使用 perf 分析这些基准测试
// ==========================================
//
// 运行方式：
//
// 1. 编译
//    go test -c -o /tmp/optimization_test \
//      ./internal/infrastructure/concurrency/
//
// 2. 使用 perf stat 对比缓存命中率
//
//    # 共享内存版本（性能差）
//    perf stat -e cache-references,cache-misses,cycles \
//      /tmp/optimization_test -test.bench=BenchmarkBadSharedData
//
//    # 核心本地数据版本（性能好）
//    perf stat -e cache-references,cache-misses,cycles \
//      /tmp/optimization_test -test.bench=BenchmarkGoodCoreLocalData
//
// 3. 使用 perf record 查找热点
//
//    perf record -g /tmp/optimization_test \
//      -test.bench=BenchmarkBadSharedData
//    perf report
//
// 预期结果：
//   - BenchmarkGoodCoreLocalData 应该有更低的 cache-misses
//   - BenchmarkGoodNoFalseSharing 应该比 BenchmarkBadFalseSharing 快
//   - BenchmarkGoodWALWriter 应该比 BenchmarkBadWALWriter 快
//
// ==========================================
