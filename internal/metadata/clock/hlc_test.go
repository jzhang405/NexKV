// Package clock 测试 HLC 混合逻辑时钟
package clock

import (
	"sync"
	"testing"
	"time"
)

// TestHLCNow 测试 HLC.Now() 生成单调递增时间戳
func TestHLCNow(t *testing.T) {
	hlc := NewHLC()

	var lastHLC *HLC
	for i := 0; i < 10000; i++ {
		now := hlc.Now()

		if lastHLC != nil {
			// 验证单调递增
			if now.LessThan(lastHLC) || now.Equal(lastHLC) {
				t.Errorf("HLC 时间戳不单调递增: last=%v, now=%v", lastHLC, now)
			}
		}

		lastHLC = now
	}
}

// TestHLCUpdate 测试 HLC.Update() 算法
func TestHLCUpdate(t *testing.T) {
	hlc := NewHLC()

	// 模拟远程 HLC（物理时间相同，逻辑计数更大）
	remoteHLC := &HLC{
		pt: hlc.PhysicalTime(),
		c:  100,
	}

	// 更新本地 HLC
	updated := hlc.Update(time.Now().UnixMilli(), remoteHLC)

	// 验证逻辑计数增加
	if updated.LogicalCounter() <= remoteHLC.c {
		t.Errorf("逻辑计数未正确增加: remote.c=%d, updated.c=%d", remoteHLC.c, updated.LogicalCounter())
	}
}

// TestHLCClockBackwards 测试时间回拨防护
func TestHLCClockBackwards(t *testing.T) {
	hlc := NewHLC()

	// 获取当前时间
	now1 := hlc.Now()
	pt1 := now1.PhysicalTime()

	// 模拟时钟回拨（设置一个更早的时间）
	time.Sleep(10 * time.Millisecond)

	// 强制设置一个较早的物理时间（模拟时钟回拨）
	earlyHLC := &HLC{
		pt: pt1 - 1000, // 回拨 1 秒
		c:  0,
	}

	// 更新本地 HLC
	updated := hlc.Update(time.Now().UnixMilli(), earlyHLC)

	// 验证物理时间没有回退
	if updated.PhysicalTime() < pt1 {
		t.Errorf("物理时间回退: pt1=%d, updated.pt=%d", pt1, updated.PhysicalTime())
	}

	// 验证逻辑计数正确处理
	now2 := hlc.Now()
	if now2.PhysicalTime() < pt1 {
		t.Errorf("Now() 返回回退的时间: pt1=%d, now2.pt=%d", pt1, now2.PhysicalTime())
	}
}

// TestHLCComparison 测试 HLC 比较操作
func TestHLCComparison(t *testing.T) {
	hlc1 := NewHLC()
	hlc2 := NewHLC()

	// hlc1 应该小于或等于 hlc2
	if hlc1.LessThan(hlc2) {
		t.Log("hlc1 < hlc2 (正常)")
	} else if hlc1.Equal(hlc2) {
		t.Log("hlc1 == hlc2 (同一毫秒生成)")
	} else if hlc1.GreaterThan(hlc2) {
		t.Error("hlc1 > hlc2 (不应该发生)")
	}

	// 测试 Compare 方法
	result := hlc1.Compare(hlc2)
	if result != -1 && result != 0 {
		t.Errorf("Compare 返回错误值: %d", result)
	}
}

// TestHLCConcurrent 测试并发安全性
func TestHLCConcurrent(t *testing.T) {
	hlc := NewHLC()

	const goroutines = 100
	const operationsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()

			for j := 0; j < operationsPerGoroutine; j++ {
				// 并发调用 Now()
				_ = hlc.Now()

				// 并发调用 Update()
				remote := &HLC{
					pt: time.Now().UnixMilli(),
					c:  uint16(j),
				}
				_ = hlc.Update(time.Now().UnixMilli(), remote)
			}
		}()
	}

	wg.Wait()

	// 验证最终 HLC 仍然有效
	final := hlc.Now()
	if final.IsAtMaxValue() {
		t.Error("HLC 已达到最大值")
	}

	if final.PhysicalTime() == 0 {
		t.Error("物理时间为 0")
	}
}

// TestHLCLimit 测试逻辑计数器溢出
func TestHLCLimit(t *testing.T) {
	hlc := NewHLC()

	// 固定物理时间
	fixedPT := time.Now().UnixMilli()

	// 快速增加逻辑计数
	for i := 0; i < 70000; i++ { // 超过 65535
		hlc.Update(fixedPT, &HLC{pt: fixedPT, c: uint16(i)})
	}

	// 检查逻辑计数是否正确回绕
	now := hlc.Now()
	if now.LogicalCounter() == 0 {
		// 如果物理时间前进，逻辑计数会重置为 0（正常）
		t.Log("逻辑计数已重置（物理时间前进）")
	}
}

// TestHLCMaxValue 测试最大值检测
func TestHLCMaxValue(t *testing.T) {
	// 创建一个接近最大值的 HLC
	maxHLC := &HLC{
		pt: (1 << 48) - 1, // 48-bit 最大值
		c:  MaxLogicalCounter,
	}

	if !maxHLC.IsAtMaxValue() {
		t.Error("IsAtMaxValue() 应该返回 true")
	}

	// 普通值不应该达到最大值
	normalHLC := NewHLC()
	if normalHLC.IsAtMaxValue() {
		t.Error("普通 HLC 不应该达到最大值")
	}
}

// TestHLCToTime 测试 HLC 转换为 time.Time
func TestHLCToTime(t *testing.T) {
	hlc := NewHLC()

	// 生成 HLC 时间戳
	ts := hlc.Now()

	// 转换为 time.Time
	tm := ts.ToTime()

	// 验证转换正确
	if tm.IsZero() {
		t.Error("转换后的时间为零值")
	}

	// 验证时间合理（应该在当前时间前后 1 秒内）
	now := time.Now()
	diff := now.Sub(tm).Abs()
	if diff > time.Second {
		t.Errorf("转换后的时间偏差过大: %v", diff)
	}
}

// BenchmarkHLCNow 性能基准测试: HLC.Now()
func BenchmarkHLCNow(b *testing.B) {
	hlc := NewHLC()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = hlc.Now()
	}
}

// BenchmarkHLCUpdate 性能基准测试: HLC.Update()
func BenchmarkHLCUpdate(b *testing.B) {
	hlc := NewHLC()
	remote := &HLC{
		pt: time.Now().UnixMilli(),
		c:  100,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = hlc.Update(time.Now().UnixMilli(), remote)
	}
}

// BenchmarkHLCLessThan 性能基准测试: HLC.LessThan()
func BenchmarkHLCLessThan(b *testing.B) {
	hlc1 := NewHLC()
	hlc2 := hlc1.Now()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = hlc2.LessThan(hlc1)
	}
}

// BenchmarkHLCConcurrent 并发性能基准测试
func BenchmarkHLCConcurrent(b *testing.B) {
	hlc := NewHLC()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = hlc.Now()
		}
	})
}
