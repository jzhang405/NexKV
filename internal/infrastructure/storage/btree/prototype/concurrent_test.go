package prototype

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test_ConcurrentRead_1000Goroutines 并发测试：1000 goroutines 并发读取
// 验证高并发场景下的读取性能和正确性
// 成功标准：吞吐 >8M ops/sec，无数据竞争
func Test_ConcurrentRead_1000Goroutines(t *testing.T) {
	ref := NewPageReferenceWithPage(NewPage(1))

	const goroutines = 1000
	const readsPerGoroutine = 10000

	var wg sync.WaitGroup
	start := time.Now()

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < readsPerGoroutine; j++ {
				page := ref.GetPage()
				require.NotNil(t, page)
				assert.Equal(t, 1, page.ID)
			}
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(start)

	// 计算吞吐
	totalOps := goroutines * readsPerGoroutine
	throughput := float64(totalOps) / elapsed.Seconds()

	t.Logf("总操作数: %d, 耗时: %v, 吞吐: %.2f M ops/sec", totalOps, elapsed, throughput/1e6)
	assert.Greater(t, throughput, 8.0e6, "吞吐应 >8M ops/sec")
}

// Test_ConcurrentReadWrite_NoRace 并发测试：读写无数据竞争
// 使用 race detector 验证并发安全性
// 运行：go test -race -v -run=Test_ConcurrentReadWrite_NoRace
func Test_ConcurrentReadWrite_NoRace(t *testing.T) {
	ref := NewPageReferenceWithPage(NewPage(1))

	const goroutines = 100
	const opsPerGoroutine = 1000

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				if id%2 == 0 {
					// 读
					page := ref.GetPage()
					assert.NotNil(t, page)
				} else {
					// 写（CAS）
					newPage := NewPage(j + 10)
					ref.UpdatePage(newPage)
				}
			}
		}(i)
	}

	wg.Wait()
	// 如果有 race condition，go test -race 会检测到
}

// Test_CAS_Atomicity 并发测试：CAS 操作原子性
// 验证在并发场景下，只有一个 goroutine 能成功 CAS
func Test_CAS_Atomicity(t *testing.T) {
	ref := NewPageReferenceWithPage(NewPage(1))

	const goroutines = 100
	var successCount atomic.Int64
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			oldInfo := ref.GetPageInfo()
			newInfo := NewPageInfo(NewPage(id + 10))
			if ref.ReplacePage(oldInfo, newInfo) {
				successCount.Add(1)
			}
		}(i)
	}

	wg.Wait()
	// 只有一个 goroutine 应该成功（第一个 CAS 的）
	assert.Equal(t, int64(1), successCount.Load(), "只有一个 goroutine 应该 CAS 成功")
}

// Test_ConcurrentUpdatePage 并发测试：并发更新页面
// 验证 UpdatePage 方法的并发安全性
func Test_ConcurrentUpdatePage(t *testing.T) {
	ref := NewPageReferenceWithPage(NewPage(1))

	const goroutines = 100
	const updatesPerGoroutine = 100

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < updatesPerGoroutine; j++ {
				newPage := NewPage(id*updatesPerGoroutine + j + 10)
				ref.UpdatePage(newPage)
			}
		}(i)
	}

	wg.Wait()

	// 验证最终状态
	page := ref.GetPage()
	require.NotNil(t, page)
	t.Logf("最终页面 ID: %d", page.ID)
}

// Test_MarkDirty_Concurrent 并发测试：并发标记脏页
func Test_MarkDirty_Concurrent(t *testing.T) {
	ref := NewPageReferenceWithPage(NewPage(1))

	const goroutines = 100

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ref.MarkDirty()
		}(i)
	}

	wg.Wait()

	// 验证脏页标记
	info := ref.GetPageInfo()
	assert.True(t, info.IsDirty(), "页面应该被标记为脏页")
}

// Test_PageReference_StressTest 压力测试：长时间运行
// 模拟真实场景的长时间并发访问
func Test_PageReference_StressTest(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过压力测试（短模式）")
	}

	ref := NewPageReferenceWithPage(NewPage(1))
	const duration = 5 * time.Second
	stopCh := make(chan struct{})

	var readOps atomic.Int64
	var writeOps atomic.Int64

	// 启动多个 goroutine
	for i := 0; i < 10; i++ {
		go func(id int) {
			for {
				select {
				case <-stopCh:
					return
				default:
					if id%3 == 0 {
						// 写操作
						newPage := NewPage(int(writeOps.Add(1)))
						ref.UpdatePage(newPage)
					} else {
						// 读操作
						_ = ref.GetPage()
						readOps.Add(1)
					}
				}
			}
		}(i)
	}

	// 运行指定时间
	time.Sleep(duration)
	close(stopCh)

	// 输出统计
	totalReads := readOps.Load()
	totalWrites := writeOps.Load()
	totalOps := totalReads + totalWrites
	throughput := float64(totalOps) / duration.Seconds()

	t.Logf("压力测试结果（%v）:", duration)
	t.Logf("  读操作: %d", totalReads)
	t.Logf("  写操作: %d", totalWrites)
	t.Logf("  总操作: %d", totalOps)
	t.Logf("  吞吐: %.2f M ops/sec", throughput/1e6)

	// 验证最终状态
	page := ref.GetPage()
	require.NotNil(t, page)
	t.Logf("  最终页面 ID: %d", page.ID)
}

// Test_ConcurrentClone 并发测试：并发克隆 PageInfo
func Test_ConcurrentClone(t *testing.T) {
	page := NewPage(1)
	page.Keys = [][]byte{[]byte("key1"), []byte("key2")}
	page.Values = [][]byte{[]byte("value1"), []byte("value2")}
	info := NewPageInfo(page)

	const goroutines = 100
	const clonesPerGoroutine = 100

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < clonesPerGoroutine; j++ {
				cloned := info.Clone()
				assert.NotNil(t, cloned)
				assert.Equal(t, page.ID, cloned.page.ID)
				assert.NotSame(t, info, cloned, "Clone 应该创建新对象")
				assert.NotSame(t, info.page, cloned.page, "Clone 应该创建新的 Page 对象")
			}
		}(i)
	}

	wg.Wait()
}

// Example_PageReference_BasicUsage PageReference 基本使用示例
func Example_PageReference_BasicUsage() {
	// 创建 PageReference
	ref := NewPageReferenceWithPage(NewPage(1))

	// 读取页面
	page := ref.GetPage()
	fmt.Println(page.ID)

	// 更新页面
	newPage := NewPage(2)
	ref.UpdatePage(newPage)

	// 读取更新后的页面
	page = ref.GetPage()
	fmt.Println(page.ID)

	// Output:
	// 1
	// 2
}

// Example_PageReference_CAS PageReference CAS 使用示例
func Example_PageReference_CAS() {
	ref := NewPageReferenceWithPage(NewPage(1))

	oldInfo := ref.GetPageInfo()
	newInfo := NewPageInfo(NewPage(2))

	// CAS 更新
	swapped := ref.ReplacePage(oldInfo, newInfo)
	fmt.Println("CAS 成功:", swapped)

	page := ref.GetPage()
	fmt.Println(page.ID)

	// Output:
	// CAS 成功: true
	// 2
}
