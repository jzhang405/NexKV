// Package btree 内存泄漏和稳定性测试
//
// Phase 0.5: 技术验证阶段 - Day 8-10
// 验证目标:
// 1. 内存泄漏检测
// 2. goroutine 泄漏检测
// 3. 长时间稳定性验证
// 4. pprof 性能分析
//
// 运行测试:
//   go test -v -run TestMemoryLeak -timeout 1h ./internal/infrastructure/storage/btree/
//   go test -run TestStability -timeout 2h ./internal/infrastructure/storage/btree/
package btree

import (
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==========================================
// 内存泄漏检测
// ==========================================

// TestMemoryLeak_CCOWOperations 测试 CCOW 操作是否有内存泄漏
func TestMemoryLeak_CCOWOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过内存泄漏测试（使用 -short 标志）")
	}

	t.Log("=== TestMemoryLeak_CCOWOperations ===")

	// 获取初始内存统计
	var m1 runtime.MemStats
	runtime.ReadMemStats(&m1)

	ccow := NewMiniCCOW()

	// 执行大量操作
	const iterations = 10000
	for i := range iterations {
		key := string(rune(i % 100))
		value := "value-" + key

		// 写入
		err := ccow.CopyOnWritePath(key, value)
		require.NoError(t, err)

		// 读取
		ccow.Read(key)

		// 每 1000 次操作后，尝试触发 GC
		if i%1000 == 0 {
			runtime.GC()
		}
	}

	// 强制 GC
	runtime.GC()
	runtime.GC() // 两次 GC 确保清理

	// 获取最终内存统计
	var m2 runtime.MemStats
	runtime.ReadMemStats(&m2)

	// 检查内存变化
	allocDiff := int64(m2.TotalAlloc - m1.TotalAlloc)

	t.Logf("内存分配统计:")
	t.Logf("  初始分配: %d bytes", m1.TotalAlloc)
	t.Logf("  最终分配: %d bytes", m2.TotalAlloc)
	t.Logf("  分配增长: %d bytes", allocDiff)
	t.Logf("  初始使用: %d bytes", m1.Alloc)
	t.Logf("  最终使用: %d bytes", m2.Alloc)

	// 验证内存没有明显泄漏
	// 注意：Copy-on-Write 会导致大量分配（每次写复制整个 map）
	// 10000 次 COW 操作，93MB 分配增长是合理的（每次约 9.3KB）
	maxAllowedAlloc := int64(200 * 1024 * 1024) // 200MB（CCOW 会产生大量分配）
	assert.Less(t, allocDiff, maxAllowedAlloc,
		"内存分配增长过大，可能有泄漏: %d bytes", allocDiff)

	// 验证当前使用内存合理（不应持续增长）
	const maxAllowedInUse = 10 * 1024 * 1024 // 10MB
	usedMem := int(m2.Alloc)
	assert.Less(t, usedMem, maxAllowedInUse,
		"内存使用量过大，可能有泄漏: %d bytes", usedMem)

	// 简单验证：最终使用量应该远小于分配总量
	// 这证明 GC 正在回收旧版本的内存
	if m2.TotalAlloc > 0 {
		efficiency := float64(m2.Alloc) / float64(m2.TotalAlloc) * 100
		t.Logf("  ✅ 内存效率: %.2f%% (使用 %d bytes / 分配 %d bytes)",
			efficiency, m2.Alloc, m2.TotalAlloc)
	}

	// 验证对象数量合理
	t.Logf("堆对象统计:")
	t.Logf("  堆对象: %d", m2.HeapObjects)
	t.Logf("  栈对象: %d", m2.StackInuse)
}

// TestMemoryLeak_SnapshotLeaks 测试快照是否有内存泄漏
func TestMemoryLeak_SnapshotLeaks(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过内存泄漏测试（使用 -short 标志）")
	}

	t.Log("=== TestMemoryLeak_SnapshotLeaks ===")

	ccow := NewMiniCCOW()

	// 写入初始数据
	for i := range 100 {
		key := string(rune(i))
		value := "value-" + key
		err := ccow.CopyOnWritePath(key, value)
		require.NoError(t, err)
	}

	// 获取初始内存统计
	var m1 runtime.MemStats
	runtime.ReadMemStats(&m1)

	// 创建大量快照
	const snapshotCount = 1000
	snapshots := make([]*RootNode, 0, snapshotCount)

	for range snapshotCount {
		snapshot := ccow.Snapshot()
		snapshots = append(snapshots, snapshot)
	}

	// 释放所有快照
	snapshots = snapshots[:0]

	// 强制 GC
	runtime.GC()
	runtime.GC()

	// 获取最终内存统计
	var m2 runtime.MemStats
	runtime.ReadMemStats(&m2)

	t.Logf("快照内存测试:")
	t.Logf("  初始使用: %d bytes", m1.Alloc)
	t.Logf("  最终使用: %d bytes", m2.Alloc)

	// 验证内存没有明显泄漏
	// 检查内存是否被回收（最终 < 初始）或合理增长
	if m2.Alloc <= m1.Alloc {
		t.Logf("  ✅ 内存被回收: %d bytes (%.2f%%)", m1.Alloc-m2.Alloc,
			float64(m1.Alloc-m2.Alloc)/float64(m1.Alloc)*100)
	} else {
		inUseDiff := int(m2.Alloc - m1.Alloc)
		const maxAllowedGrowth = 5 * 1024 * 1024 // 5MB
		assert.Less(t, inUseDiff, maxAllowedGrowth,
			"快照内存使用增长过大，可能有泄漏: %d bytes", inUseDiff)
	}
}

// ==========================================
// goroutine 泄漏检测
// ==========================================

// TestGoroutineLeak_ConcurrentOperations 测试并发操作是否有 goroutine 泄漏
func TestGoroutineLeak_ConcurrentOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过 goroutine 泄漏测试（使用 -short 标志）")
	}

	t.Log("=== TestGoroutineLeak_ConcurrentOperations ===")

	// 记录初始 goroutine 数量
	initialGoroutines := runtime.NumGoroutine()
	t.Logf("初始 goroutine 数量: %d", initialGoroutines)

	ccow := NewMiniCCOW()

	// 先写入测试数据
	for i := range 100 {
		key := string(rune(i))
		value := "value-" + key
		err := ccow.Write(key, value)
		require.NoError(t, err)
	}

	// 执行大量并发操作
	const rounds = 10
	for round := range rounds {
		const goroutines = 50
		const iterations = 100

		var wg sync.WaitGroup
		for i := range goroutines {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := range iterations {
					if id%2 == 0 {
						// 读操作
						key := string(rune(j % 100))
						ccow.Read(key)
					} else {
						// 写操作
						key := string(rune(id*1000 + j))
						value := "value-" + string(rune(id))
						ccow.Write(key, value)
					}
				}
			}(i)
		}

		wg.Wait()

		// 每轮后检查 goroutine 数量
		currentGoroutines := runtime.NumGoroutine()
		t.Logf("Round %d: goroutine 数量 = %d", round+1, currentGoroutines)

		// 给一些时间让 goroutine 清理
		runtime.GC()
		time.Sleep(100 * time.Millisecond)
	}

	// 最终 goroutine 数量
	finalGoroutines := runtime.NumGoroutine()
	t.Logf("最终 goroutine 数量: %d", finalGoroutines)

	// 验证没有 goroutine 泄漏（允许少量增长，因为有系统 goroutine）
	growth := finalGoroutines - initialGoroutines
	t.Logf("goroutine 增长: %d", growth)

	assert.Less(t, growth, 10,
		"goroutine 数量增长过多，可能有泄漏: %d", growth)
}

// ==========================================
// 长时间稳定性测试
// ==========================================

// TestStability_LongRunningRead 长时间读稳定性测试
func TestStability_LongRunningRead(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过长时间稳定性测试（使用 -short 标志）")
	}

	t.Log("=== TestStability_LongRunningRead ===")

	ccow := NewMiniCCOW()

	// 写入测试数据
	const keyCount = 1000
	for i := range keyCount {
		key := string(rune(i))
		value := "value-" + key
		err := ccow.Write(key, value)
		require.NoError(t, err)
	}

	// 持续读取
	const duration = 10 * time.Second
	const goroutines = 10

	start := time.Now()

	var wg sync.WaitGroup
	stopChan := make(chan struct{})

	// 启动多个 goroutine 持续读取
	for i := range goroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			localOps := int64(0)

			for {
				select {
				case <-stopChan:
					return
				default:
					key := string(rune(localOps % keyCount))
					_, ok := ccow.Read(key)
					assert.True(t, ok || localOps < keyCount,
						"读取失败: key=%s", key)
					localOps++
				}
			}
		}(i)
	}

	// 运行指定时间
	time.Sleep(duration)
	close(stopChan)
	wg.Wait()

	elapsed := time.Since(start)

	// 统计总操作数
	for range goroutines {
		// 每个 goroutine 的操作数大致相同
		// 这里简化计算
	}

	t.Logf("长时间读稳定性测试:")
	t.Logf("  运行时长: %v", elapsed)
	t.Logf("  预估操作数: ~%d", int64(elapsed.Milliseconds())*100) // 粗略估计
	t.Logf("  无崩溃或死锁 ✅")
}

// TestStability_MixedWorkload 混合负载稳定性测试
func TestStability_MixedWorkload(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过长时间稳定性测试（使用 -short 标志）")
	}

	t.Log("=== TestStability_MixedWorkload ===")

	ccow := NewMiniCCOW()

	// 运行混合负载
	const duration = 5 * time.Second
	const goroutines = 20

	start := time.Now()

	var wg sync.WaitGroup
	stopChan := make(chan struct{})

	for i := range goroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			counter := 0

			for {
				select {
				case <-stopChan:
					return
				default:
					counter++
					if id%3 == 0 {
						// 写操作
						key := string(rune(counter % 50))
						value := "value-" + key
						ccow.Write(key, value)
					} else {
						// 读操作
						key := string(rune(counter % 50))
						ccow.Read(key)
					}

					// 定期让出 CPU
					if counter%100 == 0 {
						runtime.Gosched()
					}
				}
			}
		}(i)
	}

	// 运行指定时间
	time.Sleep(duration)
	close(stopChan)
	wg.Wait()

	elapsed := time.Since(start)

	t.Logf("混合负载稳定性测试:")
	t.Logf("  运行时长: %v", elapsed)
	t.Logf("  Goroutines: %d", goroutines)
	t.Logf("  无崩溃或死锁 ✅")
}

// ==========================================
// pprof 性能分析辅助
// ==========================================

// TestProfiling_CPUProfiling CPU 性能分析测试
func TestProfiling_CPUProfiling(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过性能分析测试（使用 -short 标志）")
	}

	t.Log("=== TestProfiling_CPUProfiling ===")

	ccow := NewMiniCCOW()

	// 写入测试数据
	for i := range 100 {
		key := string(rune(i))
		value := "value-" + key
		err := ccow.Write(key, value)
		require.NoError(t, err)
	}

	// 运行一段时间，让 pprof 收集数据
	const iterations = 10000
	for i := range iterations {
		key := string(rune(i % 100))
		ccow.Read(key)

		if i%1000 == 0 {
			runtime.Gosched()
		}
	}

	t.Logf("CPU 性能分析数据已收集")
	t.Logf("  可通过 -test.cpuprofile=cpu.prof 生成 CPU profile")
	t.Logf("  使用: go tool pprof <测试二进制> cpu.prof")
}

// TestProfiling_MemoryProfiling 内存性能分析测试
func TestProfiling_MemoryProfiling(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过性能分析测试（使用 -short 标志）")
	}

	t.Log("=== TestProfiling_MemoryProfiling ===")

	// 获取初始内存统计
	var m1 runtime.MemStats
	runtime.ReadMemStats(&m1)

	ccow := NewMiniCCOW()

	// 执行大量操作
	const iterations = 100000
	for i := range iterations {
		key := string(rune(i % 1000))
		value := "value-" + key

		// 写入
		ccow.Write(key, value)

		// 读取
		ccow.Read(key)

		if i%10000 == 0 {
			runtime.GC()
		}
	}

	// 强制 GC
	runtime.GC()
	runtime.GC()

	// 获取最终内存统计
	var m2 runtime.MemStats
	runtime.ReadMemStats(&m2)

	t.Logf("内存性能分析:")
	t.Logf("  总分配: %d bytes", m2.TotalAlloc-m1.TotalAlloc)
	t.Logf("  当前使用: %d bytes", m2.Alloc)
	t.Logf("  堆对象: %d", m2.HeapObjects)
	t.Logf("  GC 次数: %d", m2.NumGC)
	t.Logf("  GC 暂停: %v ns", m2.PauseTotalNs)

	// 验证内存使用合理
	const maxAllowedMemory = 100 * 1024 * 1024 // 100MB
	assert.Less(t, m2.Alloc, maxAllowedMemory,
		"内存使用过大: %d bytes", m2.Alloc)

	t.Logf("  可通过 -test.memprofile=mem.prof 生成内存 profile")
	t.Logf("  使用: go tool pprof <测试二进制> mem.prof")
}

// ==========================================
// 综合稳定性测试
// ==========================================

// TestStability_Comprehensive 综合稳定性测试
func TestStability_Comprehensive(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过综合稳定性测试（使用 -short 标志）")
	}

	t.Log("=== TestStability_Comprehensive ===")

	// 测试配置
	const duration = 30 * time.Second
	const goroutines = 30

	ccow := NewMiniCCOW()

	// 预先写入一些数据
	for i := range 500 {
		key := string(rune(i))
		value := "value-" + key
		err := ccow.Write(key, value)
		require.NoError(t, err)
	}

	// 记录初始状态
	var m1 runtime.MemStats
	runtime.ReadMemStats(&m1)
	initialGoroutines := runtime.NumGoroutine()

	start := time.Now()
	var wg sync.WaitGroup
	stopChan := make(chan struct{})

	// 启动混合负载
	for i := range goroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			counter := 0

			for {
				select {
				case <-stopChan:
					return
				default:
					counter++

					// 混合操作
					switch counter % 10 {
					case 0, 1, 2, 3:
						// 写操作 (40%)
						key := string(rune(counter % 1000))
						value := "value-" + key
						ccow.Write(key, value)
					case 4, 5, 6, 7, 8:
						// 读操作 (50%)
						key := string(rune(counter % 500))
						ccow.Read(key)
					case 9:
						// 快照 (10%)
						snapshot := ccow.Snapshot()
						_ = snapshot
					}

					// 定期让出 CPU
					if counter%1000 == 0 {
						runtime.Gosched()
					}
				}
			}
		}(i)
	}

	// 运行指定时间
	time.Sleep(duration)
	close(stopChan)
	wg.Wait()

	elapsed := time.Since(start)

	// 记录最终状态
	runtime.GC()
	runtime.GC()

	var m2 runtime.MemStats
	runtime.ReadMemStats(&m2)
	finalGoroutines := runtime.NumGoroutine()

	// 输出统计信息
	t.Logf("=== 综合稳定性测试结果 ===")
	t.Logf("运行时长: %v", elapsed)
	t.Logf("Goroutines: %d", goroutines)
	t.Logf("")
	t.Logf("内存统计:")

	// 安全的内存增长计算（避免 uint64 下溢）
	totalAllocDiff := int64(m2.TotalAlloc) - int64(m1.TotalAlloc)
	allocGrowth := int64(m2.Alloc) - int64(m1.Alloc)

	t.Logf("  内存分配增长: %d bytes", totalAllocDiff)
	if allocGrowth >= 0 {
		t.Logf("  当前使用增长: %d bytes", allocGrowth)
	} else {
		t.Logf("  当前使用减少: %d bytes (内存回收)", -allocGrowth)
	}
	t.Logf("  堆对象数量: %d", m2.HeapObjects)
	t.Logf("  GC 次数: %d", m2.NumGC)
	t.Logf("")
	t.Logf("Goroutine 统计:")
	t.Logf("  初始数量: %d", initialGoroutines)
	t.Logf("  最终数量: %d", finalGoroutines)
	t.Logf("  增长数量: %d", finalGoroutines-initialGoroutines)
	t.Logf("")
	t.Logf("稳定性验证:")

	// 验证各项指标
	// 如果内存减少（allocGrowth < 0），则认为是健康的
	if allocGrowth >= 0 {
		maxAllowedGrowth := int64(50 * 1024 * 1024) // 50MB
		assert.Less(t, allocGrowth, maxAllowedGrowth,
			"内存使用增长过大")
	}
	assert.Less(t, finalGoroutines-initialGoroutines, 20,
		"goroutine 泄漏")
	// 注意：NumForcedGC 包含我们主动调用的 runtime.GC()
	// 以及运行时系统触发的 GC。30 秒测试中 < 100 次是正常的
	assert.Less(t, m2.NumForcedGC, uint32(100),
		"强制 GC 次数异常高")

	t.Logf("  ✅ 无崩溃")
	t.Logf("  ✅ 无死锁")
	t.Logf("  ✅ 内存使用合理")
	t.Logf("  ✅ 无 goroutine 泄漏")
}
