// Package clock 测试 HLC 混合逻辑时钟
package clock

import (
	"sync"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// TestHLCProviderNow 测试 HLCProvider.Now() 生成单调递增时间戳
func TestHLCProviderNow(t *testing.T) {
	provider := NewHLCProvider()

	var lastHLC *model.HLC
	for range 10000 {
		now := provider.Now()

		if lastHLC != nil {
			// 验证单调递增
			if now.LessThan(lastHLC) || now.Equal(lastHLC) {
				t.Errorf("HLC 时间戳不单调递增: last=%v, now=%v", lastHLC, now)
			}
		}

		lastHLC = now
	}
}

// TestHLCProviderUpdate 测试 HLCProvider.Update() 算法
// HLC Update 核心算法：pt' = max(now, pt, eventTime, remoteHLC.pt)
// 如果 pt' == pt && pt' == remoteHLC.pt，则 c' = max(c, remoteHLC.c) + 1
// 否则 c' = 0
func TestHLCProviderUpdate(t *testing.T) {
	provider := NewHLCProvider()
	currentPT := provider.Current().PhysicalTime()

	t.Run("Update后时间戳单调递增", func(t *testing.T) {
		// 创建远程 HLC，时间戳比本地大
		remoteHLC := model.NewHLCWithTime(currentPT+100, 50)

		// 更新本地 HLC
		updated := provider.Update(currentPT+50, remoteHLC)

		// 验证更新后的时间戳不小于原时间戳
		current := provider.Current()
		if updated.LessThan(current) {
			t.Errorf("Update 违反单调性: updated=%v, current=%v", updated, current)
		}

		// 验证物理时间正确（应该取最大值）
		if updated.PhysicalTime() != remoteHLC.PhysicalTime() {
			t.Errorf("物理时间未取最大值: updated.pt=%d, remote.pt=%d", updated.PhysicalTime(), remoteHLC.PhysicalTime())
		}
	})

	t.Run("Update后再次Update保持单调性", func(t *testing.T) {
		// 第一次更新
		remote1 := model.NewHLCWithTime(provider.Current().PhysicalTime()+10, 5)
		updated1 := provider.Update(provider.Current().PhysicalTime()+5, remote1)

		// 第二次更新
		remote2 := model.NewHLCWithTime(updated1.PhysicalTime()+10, 10)
		updated2 := provider.Update(updated1.PhysicalTime()+5, remote2)

		// 验证单调递增
		if updated2.LessThan(updated1) || updated2.Equal(updated1) {
			t.Errorf("连续 Update 违反单调性: updated1=%v, updated2=%v", updated1, updated2)
		}
	})

	t.Run("处理时钟回拨", func(t *testing.T) {
		// 本地时间前进
		time.Sleep(2 * time.Millisecond)
		currentPT2 := provider.Current().PhysicalTime()

		// 远程 HLC 时间较早（模拟时钟回拨）
		earlyRemote := model.NewHLCWithTime(currentPT2-1000, 0)

		// 更新本地 HLC（应该忽略较早的远程时间）
		updated := provider.Update(currentPT2-500, earlyRemote)

		// 验证物理时间没有回退
		if updated.PhysicalTime() < currentPT2 {
			t.Errorf("Update 导致时间回退: updated.pt=%d, current.pt=%d", updated.PhysicalTime(), currentPT2)
		}
	})
}

// TestHLCClockBackwards 测试时间回拨防护
func TestHLCClockBackwards(t *testing.T) {
	provider := NewHLCProvider()

	// 先推进时间确保物理时间前进
	ts0 := provider.Now()
	time.Sleep(2 * time.Millisecond)

	// 获取当前时间戳
	ts1 := provider.Now()

	// 模拟时钟回拨：使用过去的时间更新
	pastTime := ts1.PhysicalTime() - 1000
	remoteHLC := model.NewHLCWithTime(pastTime, 0)

	// 更新时间戳（应该保持单调性）
	ts2 := provider.Update(pastTime, remoteHLC)

	// 验证物理时间没有回退
	// 注意：由于 Update 使用 max(now, pt, eventTime, remotePT)，物理时间会前进
	if ts2.PhysicalTime() < ts1.PhysicalTime() {
		t.Errorf("时钟回拨导致物理时间倒退: ts1=%v, ts2=%v", ts1, ts2)
	}

	// 验证当前状态的时间戳不小于 ts1
	current := provider.Current()
	if current.LessThan(ts1) {
		t.Errorf("时钟回拨防护失败: ts1=%v, current=%v", ts1, current)
	}

	_ = ts0 // 避免未使用变量警告
}

// TestHLCMarshalBinary 测试 HLC 序列化
func TestHLCMarshalBinary(t *testing.T) {
	hlc := model.NewHLCWithTime(1234567890123, 42)

	data, err := hlc.MarshalBinary()
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}

	// 验证数据长度（8 bytes pt + 2 bytes c = 10 bytes）
	if len(data) != 10 {
		t.Errorf("序列化数据长度错误: expected 10, got %d", len(data))
	}

	// 反序列化
	hlc2 := &model.HLC{}
	err = hlc2.UnmarshalBinary(data)
	if err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}

	// 验证反序列化后的值
	if !hlc.Equal(hlc2) {
		t.Errorf("反序列化后值不匹配: original=%v, deserialized=%v", hlc, hlc2)
	}
}

// TestHLCCompare 测试 HLC 比较
func TestHLCCompare(t *testing.T) {
	tests := []struct {
		name     string
		h1       *model.HLC
		h2       *model.HLC
		expected int
	}{
		{
			name:     "h1 < h2 (物理时间)",
			h1:       model.NewHLCWithTime(100, 0),
			h2:       model.NewHLCWithTime(200, 0),
			expected: -1,
		},
		{
			name:     "h1 > h2 (物理时间)",
			h1:       model.NewHLCWithTime(200, 0),
			h2:       model.NewHLCWithTime(100, 0),
			expected: 1,
		},
		{
			name:     "h1 < h2 (逻辑计数)",
			h1:       model.NewHLCWithTime(100, 0),
			h2:       model.NewHLCWithTime(100, 1),
			expected: -1,
		},
		{
			name:     "h1 > h2 (逻辑计数)",
			h1:       model.NewHLCWithTime(100, 1),
			h2:       model.NewHLCWithTime(100, 0),
			expected: 1,
		},
		{
			name:     "h1 == h2",
			h1:       model.NewHLCWithTime(100, 50),
			h2:       model.NewHLCWithTime(100, 50),
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.h1.Compare(tt.h2)
			if result != tt.expected {
				t.Errorf("Compare() = %d, want %d", result, tt.expected)
			}
		})
	}
}

// TestHLCConcurrency 测试并发安全性
func TestHLCConcurrency(t *testing.T) {
	provider := NewHLCProvider()

	const numGoroutines = 100
	const numIterations = 1000

	var wg sync.WaitGroup
	timestamps := make(chan *model.HLC, numGoroutines*numIterations)

	for range numGoroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var prev *model.HLC
			for range numIterations {
				ts := provider.Now()
				timestamps <- ts
				// 验证每个 goroutine 内部的时间戳单调递增
				if prev != nil && (ts.LessThan(prev) || ts.Equal(prev)) {
					t.Errorf("goroutine 内时间戳不单调递增: prev=%v, curr=%v", prev, ts)
				}
				prev = ts
			}
		}()
	}

	wg.Wait()
	close(timestamps)

	// 收集所有时间戳
	var allTimestamps []*model.HLC
	for ts := range timestamps {
		allTimestamps = append(allTimestamps, ts)
	}

	// 验证所有时间戳都是唯一的（按物理时间+逻辑计数排序后检查）
	seen := make(map[string]bool)
	for _, ts := range allTimestamps {
		key := ts.String()
		if seen[key] {
			t.Errorf("发现重复的时间戳: %v", ts)
		}
		seen[key] = true
	}

	// 验证总数量正确
	expectedCount := numGoroutines * numIterations
	if len(allTimestamps) != expectedCount {
		t.Errorf("时间戳数量不匹配: expected %d, got %d", expectedCount, len(allTimestamps))
	}
}

// TestHLCProviderCurrent 测试 Current 方法
func TestHLCProviderCurrent(t *testing.T) {
	provider := NewHLCProvider()

	// 获取当前状态
	current1 := provider.Current()

	// 推进时间
	time.Sleep(2 * time.Millisecond)
	ts := provider.Now()

	// 再次获取当前状态
	current2 := provider.Current()

	// Current 应该返回当前内部状态
	if current2.LessThan(current1) {
		t.Errorf("Current() 返回的时间戳倒退")
	}

	// Now 推进后，Current 应该反映新状态
	if current2.LessThan(ts) {
		t.Errorf("Now() 后 Current() 未更新")
	}
}

// TestHLCProviderWithNilRemote 测试 remoteHLC 为 nil 的情况
func TestHLCProviderWithNilRemote(t *testing.T) {
	provider := NewHLCProvider()

	// 先推进时间，确保有足够的逻辑计数空间
	ts0 := provider.Now()
	time.Sleep(2 * time.Millisecond)

	// Update 时传入 nil remoteHLC
	ts1 := provider.Now()
	ts2 := provider.Update(ts1.PhysicalTime(), nil)

	// 验证时间戳单调递增（ts2 应该 > ts1，因为逻辑计数会增加）
	if ts2.LessThan(ts1) {
		t.Errorf("Update with nil remoteHLC 违反单调性: ts1=%v, ts2=%v", ts1, ts2)
	}

	// 验证物理时间没有倒退
	if ts2.PhysicalTime() < ts1.PhysicalTime() {
		t.Errorf("Update with nil remoteHLC 导致物理时间倒退")
	}

	_ = ts0 // 避免未使用变量警告
}
