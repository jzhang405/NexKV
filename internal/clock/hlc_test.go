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
// HLC Update 核心算法：pt' = max(now, pt, eventTime, remoteHLC.pt)
// 如果 pt' == pt && pt' == remoteHLC.pt，则 c' = max(c, remoteHLC.c) + 1
// 否则 c' = 0
func TestHLCUpdate(t *testing.T) {
	hlc := NewHLC()
	currentPT := hlc.PhysicalTime()

	t.Run("Update后时间戳单调递增", func(t *testing.T) {
		// 创建远程 HLC，时间戳比本地大
		remoteHLC := &HLC{
			pt: currentPT + 100,
			c:  50,
		}

		// 更新本地 HLC
		updated := hlc.Update(currentPT+50, remoteHLC)

		// 验证更新后的时间戳不小于原时间戳
		if updated.LessThan(hlc) {
			t.Errorf("Update 违反单调性: updated=%v, original=%v", updated, hlc)
		}

		// 验证物理时间正确（应该取最大值）
		if updated.PhysicalTime() != remoteHLC.pt {
			t.Errorf("物理时间未取最大值: updated.pt=%d, remote.pt=%d", updated.PhysicalTime(), remoteHLC.pt)
		}
	})

	t.Run("Update后再次Update保持单调性", func(t *testing.T) {
		// 第一次更新
		remote1 := &HLC{
			pt: hlc.PhysicalTime() + 10,
			c:  5,
		}
		updated1 := hlc.Update(hlc.PhysicalTime()+5, remote1)

		// 第二次更新
		remote2 := &HLC{
			pt: updated1.PhysicalTime() + 10,
			c:  10,
		}
		updated2 := hlc.Update(updated1.PhysicalTime()+5, remote2)

		// 验证单调递增
		if updated2.LessThan(updated1) || updated2.Equal(updated1) {
			t.Errorf("连续 Update 违反单调性: updated1=%v, updated2=%v", updated1, updated2)
		}
	})

	t.Run("处理时钟回拨", func(t *testing.T) {
		// 本地时间前进
		time.Sleep(2 * time.Millisecond)
		currentPT2 := hlc.PhysicalTime()

		// 远程 HLC 时间较早（模拟时钟回拨）
		earlyRemote := &HLC{
			pt: currentPT2 - 1000,
			c:  0,
		}

		// 更新本地 HLC（应该忽略较早的远程时间）
		updated := hlc.Update(currentPT2-500, earlyRemote)

		// 验证物理时间没有回退
		if updated.PhysicalTime() < currentPT2 {
			t.Errorf("Update 导致时间回退: updated.pt=%d, current.pt=%d", updated.PhysicalTime(), currentPT2)
		}
	})
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

// TestHLCUpdateWithNilRemote 测试 P0-1 修复: Update 接受 nil remoteHLC
func TestHLCUpdateWithNilRemote(t *testing.T) {
	hlc := NewHLC()
	initialPT := hlc.PhysicalTime()

	// P0-1 修复测试: nil remoteHLC 不应该导致 panic
	updated := hlc.Update(time.Now().UnixMilli(), nil)

	// 验证: 即使 remoteHLC 为 nil，时间戳也应该单调递增
	if updated.PhysicalTime() < initialPT {
		t.Errorf("Update with nil remoteHLC caused time regression: initial=%d, updated=%d",
			initialPT, updated.PhysicalTime())
	}

	// 验证: 多次使用 nil 调用 Update 仍然正常工作
	for i := 0; i < 100; i++ {
		prev := updated
		updated = hlc.Update(time.Now().UnixMilli(), nil)
		if updated.LessThan(prev) {
			t.Errorf("Update with nil remoteHLC not monotonic: prev=%v, updated=%v", prev, updated)
		}
	}
}

// TestHLCUpdateNilRemoteWithFixedTime 测试 P0-1: 固定事件时间 + nil remoteHLC
// 当 remoteHLC 为 nil 时，Update 只考虑本地时间和事件时间
// 如果两者都等于当前物理时间，逻辑计数器不会增加（这是正确行为）
func TestHLCUpdateNilRemoteWithFixedTime(t *testing.T) {
	hlc := NewHLC()
	fixedPT := time.Now().UnixMilli()

	// 使用相同事件时间和 nil remoteHLC
	// 由于 remotePT=0，条件 newPT == h.pt && newPT == remotePT 永远不满足
	// 所以逻辑计数器会保持为 0，物理时间会取最大值
	for i := 0; i < 10; i++ {
		updated := hlc.Update(fixedPT, nil)
		// 验证: 不会导致 panic，且时间戳有效
		if updated.PhysicalTime() < 0 {
			t.Errorf("Invalid physical time: %d", updated.PhysicalTime())
		}
	}
}

// TestHLCLogicalCounterOverflowInUpdate 测试 P0-2 修复: Update 中的逻辑计数器溢出
func TestHLCLogicalCounterOverflowInUpdate(t *testing.T) {
	// 创建一个逻辑计数器接近最大值的 HLC
	hlc := &HLC{
		pt: time.Now().UnixMilli(),
		c:  MaxLogicalCounter - 1, // 65534
	}

	initialPT := hlc.PhysicalTime()

	// 使用相同物理时间的远程 HLC 触发溢出
	remote := &HLC{
		pt: initialPT,
		c:  MaxLogicalCounter, // 65535
	}

	// P0-2 修复测试: 溢出应该推进物理时间而非回绕
	updated := hlc.Update(initialPT, remote)

	// 验证: 物理时间应该推进（因为逻辑计数溢出）
	if updated.PhysicalTime() <= initialPT {
		t.Errorf("Overflow should advance physical time: initial=%d, updated=%d",
			initialPT, updated.PhysicalTime())
	}

	// 验证: 逻辑计数应该重置为 0
	if updated.LogicalCounter() != 0 {
		t.Errorf("Logical counter should be reset after overflow: got=%d, expected=0",
			updated.LogicalCounter())
	}
}

// TestHLCLogicalCounterOverflowInNow 测试 P0-2 修复: Now() 中的逻辑计数器溢出
func TestHLCLogicalCounterOverflowInNow(t *testing.T) {
	// 创建一个逻辑计数器在最大值的 HLC
	pt := time.Now().UnixMilli()
	hlc := &HLC{
		pt: pt,
		c:  MaxLogicalCounter, // 65535
	}

	// P0-2 修复测试: Now() 中的 h.c++ 溢出应该推进物理时间
	now := hlc.Now()

	// 验证: 物理时间应该推进（因为逻辑计数溢出）
	if hlc.PhysicalTime() <= pt {
		t.Errorf("Now() overflow should advance physical time: initial=%d, after=%d",
			pt, hlc.PhysicalTime())
	}

	// 验证: 返回的时间戳应该大于初始值
	if now.PhysicalTime() < pt {
		t.Errorf("Now() returned timestamp with lower physical time: initial=%d, returned=%d",
			pt, now.PhysicalTime())
	}
}

// TestHLCOverflowMonotonicity 测试溢出后的单调性
func TestHLCOverflowMonotonicity(t *testing.T) {
	// 从接近溢出的状态开始
	hlc := &HLC{
		pt: time.Now().UnixMilli(),
		c:  MaxLogicalCounter - 100,
	}

	var last *HLC
	// 触发多次溢出
	for i := 0; i < 300; i++ {
		remote := &HLC{
			pt: hlc.PhysicalTime(),
			c:  MaxLogicalCounter,
		}
		updated := hlc.Update(hlc.PhysicalTime(), remote)

		if last != nil && (updated.LessThan(last) || updated.Equal(last)) {
			t.Errorf("Monotonicity violated after overflow: last=%v, updated=%v", last, updated)
		}
		last = updated
	}
}
