// Package clock 测试时钟应用服务
package clock

import (
	"sync"
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/stretchr/testify/assert"
)

// ==========================================
// ClockServiceImpl 测试
// ==========================================

func TestNewClockService(t *testing.T) {
	provider := NewHLCProvider()
	service := NewClockService(provider)

	assert.NotNil(t, service)
	assert.Equal(t, provider, service.GetProvider())
}

func TestClockService_GetProvider(t *testing.T) {
	provider := NewHLCProvider()
	service := NewClockService(provider)

	assert.Equal(t, provider, service.GetProvider())
}

func TestClockService_CompareTimestamps(t *testing.T) {
	provider := NewHLCProvider()
	service := NewClockService(provider)

	t1 := model.NewHLCWithTime(1000, 0)
	t2 := model.NewHLCWithTime(2000, 0)
	t3 := model.NewHLCWithTime(1000, 1)

	// t1 < t2
	assert.Equal(t, -1, service.CompareTimestamps(t1, t2))

	// t2 > t1
	assert.Equal(t, 1, service.CompareTimestamps(t2, t1))

	// t1 < t3 (相同物理时间，逻辑计数决定)
	assert.Equal(t, -1, service.CompareTimestamps(t1, t3))

	// t3 > t1
	assert.Equal(t, 1, service.CompareTimestamps(t3, t1))

	// 相等
	assert.Equal(t, 0, service.CompareTimestamps(t1, t1))
}

func TestClockService_IsConcurrent(t *testing.T) {
	provider := NewHLCProvider()
	service := NewClockService(provider)

	t1 := model.NewHLCWithTime(1000, 0)
	t2 := model.NewHLCWithTime(1000, 0) // 完全相同
	t3 := model.NewHLCWithTime(1000, 1) // 逻辑计数不同
	t4 := model.NewHLCWithTime(2000, 0) // 物理时间不同

	// 完全相同的时间戳认为是并发的
	assert.True(t, service.IsConcurrent(t1, t2))

	// 逻辑计数不同，不是并发
	assert.False(t, service.IsConcurrent(t1, t3))

	// 物理时间不同，不是并发
	assert.False(t, service.IsConcurrent(t1, t4))

	// nil 处理
	assert.False(t, service.IsConcurrent(nil, t1))
	assert.False(t, service.IsConcurrent(t1, nil))
	assert.False(t, service.IsConcurrent(nil, nil))
}

func TestClockService_MaxTimestamp(t *testing.T) {
	provider := NewHLCProvider()
	service := NewClockService(provider)

	t1 := model.NewHLCWithTime(1000, 0)
	t2 := model.NewHLCWithTime(2000, 0)
	t3 := model.NewHLCWithTime(1000, 1)

	// t2 > t1
	max := service.MaxTimestamp(t1, t2)
	assert.Equal(t, t2.PhysicalTime(), max.PhysicalTime())
	assert.Equal(t, t2.LogicalCounter(), max.LogicalCounter())

	// t3 > t1
	max = service.MaxTimestamp(t1, t3)
	assert.Equal(t, t3.PhysicalTime(), max.PhysicalTime())
	assert.Equal(t, t3.LogicalCounter(), max.LogicalCounter())

	// nil 处理
	max = service.MaxTimestamp(nil, t1)
	assert.Equal(t, t1, max)

	max = service.MaxTimestamp(t1, nil)
	assert.Equal(t, t1, max)

	max = service.MaxTimestamp(nil, nil)
	assert.Nil(t, max)
}

// ==========================================
// HLCProvider 测试
// ==========================================

func TestNewHLCProvider(t *testing.T) {
	provider := NewHLCProvider()

	assert.NotNil(t, provider)
	// 初始化时间应该是最近的时间戳
	hlc := provider.Current()
	assert.Greater(t, hlc.PhysicalTime(), int64(0))
	assert.Equal(t, uint16(0), hlc.LogicalCounter())
}

func TestHLCProvider_Now(t *testing.T) {
	provider := NewHLCProvider()

	// 第一次调用
	t1 := provider.Now()
	assert.NotNil(t, t1)
	assert.Greater(t, t1.PhysicalTime(), int64(0))

	// 连续调用应该递增逻辑计数
	t2 := provider.Now()
	if t2.PhysicalTime() == t1.PhysicalTime() {
		// 物理时间相同，逻辑计数应该递增
		assert.Equal(t, t1.LogicalCounter()+1, t2.LogicalCounter())
	}

	// 多次调用
	t3 := provider.Now()
	if t3.PhysicalTime() == t2.PhysicalTime() {
		assert.Equal(t, t2.LogicalCounter()+1, t3.LogicalCounter())
	}
}

func TestHLCProvider_Now_Overflow(t *testing.T) {
	provider := NewHLCProvider().(*HLCProvider)
	provider.pt = 1000
	provider.c = 65535 // 接近最大值

	// 下一次调用应该触发溢出，推进物理时间
	hlc := provider.Now()
	// 物理时间应该推进或逻辑计数重置
	if hlc.PhysicalTime() == 1000 {
		assert.Equal(t, uint16(0), hlc.LogicalCounter())
	}
}

func TestHLCProvider_Update(t *testing.T) {
	tests := []struct {
		name       string
		eventTime  int64
		remoteHLC  *model.HLC
		wantPTInc  bool
		wantCReset bool
	}{
		{
			name:       "newer event time",
			eventTime:  9999999999999,
			remoteHLC:  model.NewHLCWithTime(1000, 0),
			wantPTInc:  true,
			wantCReset: true,
		},
		{
			name:       "same physical time",
			eventTime:  1000,
			remoteHLC:  model.NewHLCWithTime(1000, 5),
			wantPTInc:  false,
			wantCReset: false,
		},
		{
			name:       "nil remote HLC",
			eventTime:  1000,
			remoteHLC:  nil,
			wantPTInc:  false,
			wantCReset: true,
		},
		{
			name:       "remote HLC with newer physical time",
			eventTime:  1000,
			remoteHLC:  model.NewHLCWithTime(5000, 0),
			wantPTInc:  true,
			wantCReset: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := NewHLCProvider().(*HLCProvider)
			provider.pt = 1000 // 设置初始状态
			provider.c = 5

			oldPT := provider.pt
			_ = provider.c // 未使用警告消除

			result := provider.Update(tt.eventTime, tt.remoteHLC)

			assert.NotNil(t, result)
			if tt.wantPTInc {
				assert.GreaterOrEqual(t, provider.pt, oldPT)
			}
			if tt.wantCReset && provider.pt > oldPT {
				assert.Equal(t, uint16(0), provider.c)
			}
		})
	}
}

func TestHLCProvider_Update_LogicalCounterIncrement(t *testing.T) {
	provider := NewHLCProvider().(*HLCProvider)

	// 获取当前时间作为基准
	currentHLC := provider.Now()
	provider.pt = currentHLC.PhysicalTime()
	provider.c = 5

	// 相同物理时间，逻辑计数应该递增
	remoteHLC := model.NewHLCWithTime(provider.pt, 3)
	result := provider.Update(provider.pt, remoteHLC)

	// 验证逻辑计数递增
	assert.Equal(t, uint16(6), result.LogicalCounter()) // max(5, 3) + 1 = 6
}

func TestHLCProvider_Update_OverflowHandling(t *testing.T) {
	provider := NewHLCProvider().(*HLCProvider)

	// 获取当前时间作为基准
	currentHLC := provider.Now()
	provider.pt = currentHLC.PhysicalTime()
	provider.c = 65535 // 最大 uint16 值

	// 相同物理时间，逻辑计数溢出
	// 传递一个大于等于当前时间的 eventTime，确保使用 provider.pt
	remoteHLC := model.NewHLCWithTime(provider.pt, 65535)
	result := provider.Update(provider.pt+10000, remoteHLC) // 使用未来的时间确保不会被当前时间覆盖

	// 应该重置逻辑计数（关键验证点）
	assert.Equal(t, uint16(0), result.LogicalCounter())

	// 物理时间应该至少是 provider.pt（可能因为 time.Now() 而更大）
	assert.GreaterOrEqual(t, result.PhysicalTime(), provider.pt)
}

func TestHLCProvider_Current(t *testing.T) {
	provider := NewHLCProvider()

	// Current 应该不推进时间
	t1 := provider.Current()
	t2 := provider.Current()

	assert.Equal(t, t1.PhysicalTime(), t2.PhysicalTime())
	assert.Equal(t, t1.LogicalCounter(), t2.LogicalCounter())
}

func TestHLCProvider_ConcurrentNow(t *testing.T) {
	provider := NewHLCProvider()

	const goroutines = 100
	const iterations = 10

	var wg sync.WaitGroup
	results := make([]*model.HLC, 0, goroutines*iterations)
	var mu sync.Mutex

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				hlc := provider.Now()
				mu.Lock()
				results = append(results, hlc)
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	// 验证所有时间戳都是唯一的
	unique := make(map[string]bool)
	for _, hlc := range results {
		key := hlc.String()
		if unique[key] {
			t.Errorf("Duplicate HLC timestamp: %s", key)
		}
		unique[key] = true
	}

	assert.Equal(t, goroutines*iterations, len(unique))
}

func TestMaxInt64(t *testing.T) {
	tests := []struct {
		name     string
		values   []int64
		expected int64
	}{
		{"single value", []int64{5}, 5},
		{"multiple values", []int64{1, 5, 3, 9, 2}, 9},
		{"negative values", []int64{-1, -5, -3}, -1},
		{"mixed values", []int64{-1, 0, 5, 3}, 5},
		{"empty slice", []int64{}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := maxInt64(tt.values...)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMaxUint16(t *testing.T) {
	tests := []struct {
		name     string
		a        uint16
		b        uint16
		expected uint16
	}{
		{"a greater", 100, 50, 100},
		{"b greater", 50, 100, 100},
		{"equal", 50, 50, 50},
		{"max value", 65535, 0, 65535},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := maxUint16(tt.a, tt.b)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// ==========================================
// 集成测试
// ==========================================

func TestClockService_Integration(t *testing.T) {
	provider := NewHLCProvider()
	service := NewClockService(provider)

	// 获取多个时间戳
	timestamps := make([]*model.HLC, 10)
	for i := 0; i < 10; i++ {
		timestamps[i] = provider.Now()
	}

	// 验证时间戳单调递增
	for i := 1; i < 10; i++ {
		assert.GreaterOrEqual(t, service.CompareTimestamps(timestamps[i], timestamps[i-1]), 0)
	}

	// 找到最大时间戳
	max := service.MaxTimestamp(timestamps[0], timestamps[9])
	assert.NotNil(t, max)
}

func TestHLCProvider_Update_RealWorld(t *testing.T) {
	provider := NewHLCProvider()

	// 模拟接收远程事件
	localTime := provider.Now()
	remoteTime := model.NewHLCWithTime(localTime.PhysicalTime()+100, 0)

	// 更新本地时钟
	updated := provider.Update(localTime.PhysicalTime(), remoteTime)

	assert.GreaterOrEqual(t, updated.PhysicalTime(), remoteTime.PhysicalTime())
}

func TestHLCProvider_Stress(t *testing.T) {
	provider := NewHLCProvider()

	const count = 10000
	lastHLC := provider.Now()

	for i := 0; i < count; i++ {
		hlc := provider.Now()
		// 验证单调性
		if hlc.PhysicalTime() < lastHLC.PhysicalTime() {
			t.Errorf("Physical time went backwards: %d < %d", hlc.PhysicalTime(), lastHLC.PhysicalTime())
		}
		if hlc.PhysicalTime() == lastHLC.PhysicalTime() && hlc.LogicalCounter() < lastHLC.LogicalCounter() {
			t.Errorf("Logical counter went backwards: %d < %d", hlc.LogicalCounter(), lastHLC.LogicalCounter())
		}
		lastHLC = hlc
	}
}
