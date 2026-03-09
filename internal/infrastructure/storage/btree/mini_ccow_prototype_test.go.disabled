// Package btree Mini CCOW 原型测试
//
// Phase 0.5: 技术验证阶段 - Day 3-5
// 验证目标:
// 1. 无锁读操作正确性
// 2. 路径复制算法正确性
// 3. 原子切换根指针正确性
// 4. 基本性能测试 (1000 ops/s)
//
// 运行测试:
//   go test -v ./internal/infrastructure/storage/btree/ -run TestMiniCCOW
package btree

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMiniCCOW_SimpleReadWrite 测试基本的读写功能
func TestMiniCCOW_SimpleReadWrite(t *testing.T) {
	t.Log("=== TestMiniCCOW_SimpleReadWrite ===")

	ccow := NewMiniCCOW()

	// 测试写操作
	err := ccow.Write("key1", "value1")
	require.NoError(t, err, "写操作应该成功")

	err = ccow.Write("key2", "value2")
	require.NoError(t, err, "写操作应该成功")

	// 测试读操作
	val, ok := ccow.Read("key1")
	assert.True(t, ok, "读取存在的键应该返回 true")
	assert.Equal(t, "value1", val, "读取的值应该匹配")

	val, ok = ccow.Read("key2")
	assert.True(t, ok, "读取存在的键应该返回 true")
	assert.Equal(t, "value2", val, "读取的值应该匹配")

	// 测试读取不存在的键
	val, ok = ccow.Read("nonexistent")
	assert.False(t, ok, "读取不存在的键应该返回 false")
	assert.Empty(t, val, "读取不存在的键应该返回空值")
}

// TestMiniCCOW_CopyOnWritePath 测试路径复制算法
func TestMiniCCOW_CopyOnWritePath(t *testing.T) {
	t.Log("=== TestMiniCCOW_CopyOnWritePath ===")

	ccow := NewMiniCCOW()

	// 初始版本
	version1 := ccow.GetVersion()
	assert.Equal(t, 0, version1, "初始版本应该是 0")

	// 第一次写入 - 应该创建新版本
	err := ccow.CopyOnWritePath("key1", "value1")
	require.NoError(t, err, "路径复制应该成功")

	version2 := ccow.GetVersion()
	assert.Equal(t, 1, version2, "版本应该递增")

	// 验证数据
	val, ok := ccow.Read("key1")
	assert.True(t, ok, "读取应该成功")
	assert.Equal(t, "value1", val, "值应该匹配")

	// 第二次写入 - 应该再创建新版本
	err = ccow.CopyOnWritePath("key2", "value2")
	require.NoError(t, err, "路径复制应该成功")

	version3 := ccow.GetVersion()
	assert.Equal(t, 2, version3, "版本应该递增")

	// 验证旧数据仍然存在
	val, ok = ccow.Read("key1")
	assert.True(t, ok, "旧数据应该仍然存在")
	assert.Equal(t, "value1", val, "旧值应该匹配")

	// 验证新数据
	val, ok = ccow.Read("key2")
	assert.True(t, ok, "新数据应该存在")
	assert.Equal(t, "value2", val, "新值应该匹配")
}

// TestMiniCCOW_UpdateExistingKey 测试更新现有键
func TestMiniCCOW_UpdateExistingKey(t *testing.T) {
	t.Log("=== TestMiniCCOW_UpdateExistingKey ===")

	ccow := NewMiniCCOW()

	// 写入初始值
	err := ccow.CopyOnWritePath("key1", "value1")
	require.NoError(t, err)

	// 更新现有键
	err = ccow.CopyOnWritePath("key1", "value1-updated")
	require.NoError(t, err)

	// 验证更新
	val, ok := ccow.Read("key1")
	assert.True(t, ok, "读取应该成功")
	assert.Equal(t, "value1-updated", val, "值应该是更新后的值")

	// 验证版本递增
	version := ccow.GetVersion()
	assert.Equal(t, 2, version, "更新操作应该创建新版本")
}

// TestMiniCCOW_SnapshotIsolation 测试快照隔离
func TestMiniCCOW_SnapshotIsolation(t *testing.T) {
	t.Log("=== TestMiniCCOW_SnapshotIsolation ===")

	ccow := NewMiniCCOW()

	// 写入初始数据
	err := ccow.CopyOnWritePath("key1", "value1")
	require.NoError(t, err)

	// 创建快照
	snapshot := ccow.Snapshot()
	require.NotNil(t, snapshot, "快照不应该为 nil")

	// 验证快照内容
	val, ok := snapshot.Data["key1"]
	assert.True(t, ok, "快照应该包含写入的数据")
	assert.Equal(t, "value1", val, "快照中的值应该匹配")

	// 在快照之后修改数据
	err = ccow.CopyOnWritePath("key1", "value2")
	require.NoError(t, err)

	// 当前应该看到新数据
	val, ok = ccow.Read("key1")
	assert.Equal(t, "value2", val, "当前应该看到新值")

	// 但快照应该仍然保持旧值
	val, ok = snapshot.Data["key1"]
	assert.Equal(t, "value1", val, "快照应该保持旧值（快照隔离）")
}

// TestMiniCCOW_ConcurrentRead 测试并发读操作
func TestMiniCCOW_ConcurrentRead(t *testing.T) {
	t.Log("=== TestMiniCCOW_ConcurrentRead ===")

	ccow := NewMiniCCOW()

	// 先写入测试数据
	for i := range 100 {
		key := string(rune(i))
		value := "value-" + key
		err := ccow.Write(key, value)
		require.NoError(t, err)
	}

	const goroutines = 100
	const iterationsPerGoroutine = 100

	// 测试并发读
	var wg sync.WaitGroup
	for i := range goroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range iterationsPerGoroutine {
				key := string(rune(j % 100))
				val, ok := ccow.Read(key)
				if j < 100 { // 只验证前100个数据
					if id == 0 && j < 10 {
						// 主 goroutine 验证数据完整性
						assert.True(t, ok, "并发读取应该成功")
						assert.NotEmpty(t, val, "值不应该为空")
					}
				}
			}
		}(i)
	}

	wg.Wait()
	t.Log("并发读测试通过 - 无数据竞争或崩溃")
}

// TestMiniCCOW_ConcurrentWrite 测试并发写操作
func TestMiniCCOW_ConcurrentWrite(t *testing.T) {
	t.Log("=== TestMiniCCOW_ConcurrentWrite ===")

	ccow := NewMiniCCOW()

	const goroutines = 10
	const iterationsPerGoroutine = 100

	// 测试并发写
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterationsPerGoroutine; j++ {
				key := string(rune(id*1000 + j))
				value := "value-" + string(rune(id))
				err := ccow.Write(key, value)
				assert.NoError(t, err, "并发写入应该成功")
			}
		}(i)
	}

	wg.Wait()
	t.Log("并发写测试通过 - 原子切换正常")
}

// TestMiniCCOW_MixedWorkload 测试混合读写负载
func TestMiniCCOW_MixedWorkload(t *testing.T) {
	t.Log("=== TestMiniCCOW_MixedWorkload ===")

	ccow := NewMiniCCOW()

	// 先写入一些初始数据
	for i := range 10 {
		key := string(rune(i))
		value := "value-" + key
		err := ccow.Write(key, value)
		require.NoError(t, err)
	}

	const goroutines = 50
	const iterationsPerGoroutine = 100

	// 混合负载测试
	var wg sync.WaitGroup
	for i := range goroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range iterationsPerGoroutine {
				if id%2 == 0 {
					// 读操作
					key := string(rune(j % 10))
					ccow.Read(key)
				} else {
					// 写操作
					key := string(rune(id*1000 + j%10))
					value := "value-" + string(rune(id))
					err := ccow.Write(key, value)
					assert.NoError(t, err)
				}
			}
		}(i)
	}

	wg.Wait()
	t.Log("混合负载测试通过 - 读写并发正常")
}

// TestMiniCCOW_Performance 基本性能测试
func TestMiniCCOW_Performance(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过性能测试（使用 -short 标志）")
	}

	t.Log("=== TestMiniCCOW_Performance ===")

	ccow := NewMiniCCOW()

	// 测试参数
	const goroutines = 100
	const iterations = 1000

	// 预热
	for i := 0; i < 100; i++ {
		key := string(rune(i))
		value := "value-" + key
		ccow.Write(key, value)
	}

	// 测试读性能
	t.Run("ConcurrentRead", func(t *testing.T) {
		var wg sync.WaitGroup
		start := time.Now()

		for range goroutines {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := range iterations {
					key := string(rune(j % 100))
					ccow.Read(key)
				}
			}()
		}

		wg.Wait()
		elapsed := time.Since(start)
		totalOps := goroutines * iterations
		opsPerSec := float64(totalOps) / elapsed.Seconds()

		t.Logf("并发读性能测试完成")
		t.Logf("  Goroutines: %d", goroutines)
		t.Logf("  每个goroutine迭代次数: %d", iterations)
		t.Logf("  总操作数: %d", totalOps)
		t.Logf("  耗时: %v", elapsed)
		t.Logf("  吞吐量: %.0f ops/s", opsPerSec)

		// 验证性能目标：≥ 1000 ops/s（原型最低要求）
		assert.Greater(t, opsPerSec, 1000.0, "原型性能应该达到 1000 ops/s")
	})

	// 测试写性能
	t.Run("ConcurrentWrite", func(t *testing.T) {
		ccow2 := NewMiniCCOW() // 使用新实例

		var wg sync.WaitGroup
		start := time.Now()

		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < iterations; j++ {
					key := string(rune(id*1000 + j))
					value := "value-" + string(rune(id))
					err := ccow2.Write(key, value)
					assert.NoError(t, err)
				}
			}(i)
		}

		wg.Wait()
		elapsed := time.Since(start)
		totalOps := goroutines * iterations
		opsPerSec := float64(totalOps) / elapsed.Seconds()

		t.Logf("并发写性能测试完成")
		t.Logf("  Goroutines: %d", goroutines)
		t.Logf("  每个goroutine迭代次数: %d", iterations)
		t.Logf("  总操作数: %d", totalOps)
		t.Logf("  耗时: %v", elapsed)
		t.Logf("  吞吐量: %.0f ops/s", opsPerSec)

		// 原型版本的性能目标较低，因为使用了 mutex
		// 正式版本将使用 PerCoreExecutor 单写线程
		t.Logf("  注: 正式实现将使用 PerCoreExecutor，性能会更高")
	})
}

// BenchmarkMiniCCOW_Read 基准测试 - 读操作
func BenchmarkMiniCCOW_Read(b *testing.B) {
	ccow := NewMiniCCOW()

	// 预填充数据
	for i := range 1000 {
		key := string(rune(i))
		value := "value-" + key
		ccow.Write(key, value)
	}

	b.ResetTimer()
	for i := range b.N {
		key := string(rune(i % 1000))
		ccow.Read(key)
	}
}

// BenchmarkMiniCCOW_Write 基准测试 - 写操作
func BenchmarkMiniCCOW_Write(b *testing.B) {
	b.StopTimer()

	ccow := NewMiniCCOW()
	key := "benchmark-key"
	value := "benchmark-value"

	// 预热
	for range 100 {
		ccow.Write(key, value)
	}

	b.StartTimer()
	for i := range b.N {
		key := string(rune(i))
		value := "value-" + key
		ccow.Write(key, value)
	}
}
