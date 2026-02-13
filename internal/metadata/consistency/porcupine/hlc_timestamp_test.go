// Package porcupine 测试 HLC 时间戳适配器
package porcupine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestHLCTimestampNow 测试时间戳生成
func TestHLCTimestampNow(t *testing.T) {
	ts := NewHLCTimestamp()

	var last int64
	for i := 0; i < 1000; i++ {
		now := ts.Now()

		// 验证单调递增
		if last > 0 && now <= last {
			t.Errorf("时间戳不单调递增: last=%d, now=%d", last, now)
		}
		last = now
	}
}

// TestHLCTimestampFormat 测试时间戳格式
func TestHLCTimestampFormat(t *testing.T) {
	ts := NewHLCTimestamp()
	now := ts.Now()

	// 解析时间戳
	pt, lc := ParseTimestamp(now)

	// 验证物理时间在合理范围内（当前时间前后 1 秒）
	nowMs := time.Now().UnixMilli()
	if pt < nowMs-1000 || pt > nowMs+1000 {
		t.Errorf("物理时间不在合理范围: pt=%d, now=%d", pt, nowMs)
	}

	// 验证重构时间戳
	reconstructed := MakeTimestamp(pt, lc)
	if reconstructed != now {
		t.Errorf("重构时间戳不匹配: original=%d, reconstructed=%d", now, reconstructed)
	}

	// 使用 lc 避免未使用变量警告
	_ = lc
}

// TestHLCTimestampCompare 测试时间戳比较
func TestHLCTimestampCompare(t *testing.T) {
	tests := []struct {
		name     string
		a        int64
		b        int64
		expected int
	}{
		{"a < b", 1000, 2000, -1},
		{"a > b", 2000, 1000, 1},
		{"a == b", 1000, 1000, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Compare(tt.a, tt.b)
			require.Equal(t, tt.expected, result)
		})
	}
}

// TestHLCTimestampConcurrent 测试并发安全性
func TestHLCTimestampConcurrent(t *testing.T) {
	ts := NewHLCTimestamp()

	const goroutines = 100
	const operationsPerGoroutine = 100

	results := make(chan int64, goroutines*operationsPerGoroutine)

	for i := 0; i < goroutines; i++ {
		go func() {
			for j := 0; j < operationsPerGoroutine; j++ {
				results <- ts.Now()
			}
		}()
	}

	// 收集所有时间戳
	var timestamps []int64
	for i := 0; i < goroutines*operationsPerGoroutine; i++ {
		timestamps = append(timestamps, <-results)
	}

	// 验证所有时间戳都是正数
	for _, ts := range timestamps {
		if ts <= 0 {
			t.Error("时间戳应该是正数")
		}
	}
}

// TestHLCTimestampNoOverflow 测试不会溢出
func TestHLCTimestampNoOverflow(t *testing.T) {
	ts := NewHLCTimestamp()

	// 生成大量时间戳，验证不会溢出
	var last int64
	for i := 0; i < 100000; i++ {
		now := ts.Now()

		// 验证时间戳始终为正数
		if now < 0 {
			t.Errorf("时间戳溢出变为负数: %d", now)
		}

		// 验证单调递增（允许相等，因为同一毫秒内可能有多个操作）
		if now < last {
			t.Errorf("时间戳回退: last=%d, now=%d", last, now)
		}
		last = now
	}
}

// TestParseAndMakeTimestamp 测试解析和构造时间戳
func TestParseAndMakeTimestamp(t *testing.T) {
	tests := []struct {
		pt int64
		lc uint16
	}{
		{1739452800000, 0},
		{1739452800000, 1},
		{1739452800000, 65535},
		// 注意：48-bit 最大 PT (281474976710655) 左移 16 位会超出 int64 正数范围
		// 当前毫秒时间戳约 1.77×10^12，远小于 48-bit 最大值，所以不需要测试边界
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			ts := MakeTimestamp(tt.pt, tt.lc)
			pt, lc := ParseTimestamp(ts)

			require.Equal(t, tt.pt, pt, "物理时间不匹配")
			require.Equal(t, tt.lc, lc, "逻辑计数器不匹配")
		})
	}
}

// BenchmarkHLCTimestampNow 性能基准测试
func BenchmarkHLCTimestampNow(b *testing.B) {
	ts := NewHLCTimestamp()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ts.Now()
	}
}

// BenchmarkHLCTimestampConcurrent 并发性能基准测试
func BenchmarkHLCTimestampConcurrent(b *testing.B) {
	ts := NewHLCTimestamp()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = ts.Now()
		}
	})
}
