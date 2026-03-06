package bftree

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDeltaChain(t *testing.T) {
	dc := NewDeltaChain(8, 64)

	assert.NotNil(t, dc)
	assert.Equal(t, 0, dc.Len())
	assert.Equal(t, uint16(0), dc.Size())
	assert.True(t, dc.IsEmpty())
}

func TestDeltaChain_Append(t *testing.T) {
	dc := NewDeltaChain(8, 64)

	// 追加第一个 Delta
	err := dc.Append(DeltaOpInsert, []byte("key1"), []byte("value1"))
	require.NoError(t, err)
	assert.Equal(t, 1, dc.Len())
	assert.False(t, dc.IsEmpty())

	// 追加第二个 Delta
	err = dc.Append(DeltaOpInsert, []byte("key2"), []byte("value2"))
	require.NoError(t, err)
	assert.Equal(t, 2, dc.Len())
}

func TestDeltaChain_Append_Full(t *testing.T) {
	tests := []struct {
		name      string
		maxLength int
		maxSize   uint16
		append    int
	}{
		{"长度限制", 2, 1024, 3},
		{"大小限制", 100, 10, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dc := NewDeltaChain(tt.maxLength, tt.maxSize)

			// 追加到容量上限
			for i := 0; i < tt.append; i++ {
				err := dc.Append(DeltaOpInsert, []byte{byte(i)}, []byte("value"))
				if i < tt.maxLength && tt.maxSize >= uint16(i)*10 {
					require.NoError(t, err)
				}
			}

			// 下一个追加应该失败
			err := dc.Append(DeltaOpInsert, []byte("full"), []byte("value"))
			assert.Error(t, err)
			assert.ErrorIs(t, err, ErrDeltaFull)
		})
	}
}

func TestDeltaChain_ShouldCompact(t *testing.T) {
	tests := []struct {
		name      string
		maxLength int
		maxSize   uint16
		append    int
		want      bool
	}{
		{"未达到阈值", 10, 100, 3, false},
		{"长度触发", 8, 100, 8, true},
		{"大小触发", 10, 15, 3, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dc := NewDeltaChain(tt.maxLength, tt.maxSize)

			for i := 0; i < tt.append; i++ {
				_ = dc.Append(DeltaOpInsert, []byte{byte(i)}, []byte("value"))
			}

			assert.Equal(t, tt.want, dc.ShouldCompact())
		})
	}
}

func TestDeltaChain_CompactTo(t *testing.T) {
	dc := NewDeltaChain(8, 64)
	mp := NewMiniPage(L2)

	// 添加初始数据到 Mini-Page
	mp.slots = append(mp.slots, Slot{key: []byte("old"), value: []byte("old-value")})
	mp.slotMap["old"] = 0
	mp.dataSize = 15

	// 追加 Delta
	_ = dc.Append(DeltaOpInsert, []byte("key1"), []byte("value1"))
	_ = dc.Append(DeltaOpUpdate, []byte("old"), []byte("new-value"))
	_ = dc.Append(DeltaOpDelete, []byte("key1"), nil)

	// 合并
	err := dc.CompactTo(mp)
	require.NoError(t, err)

	// 验证 Delta Chain 已清空
	assert.Equal(t, 0, dc.Len())
	assert.True(t, dc.IsEmpty())

	// 验证 Mini-Page 状态
	assert.Equal(t, 1, len(mp.slots)) // 只剩 "old"（已更新）
	assert.Equal(t, []byte("new-value"), mp.slots[0].value)
}

func TestDeltaChain_Get(t *testing.T) {
	dc := NewDeltaChain(8, 64)

	// 空链查找
	value, found := dc.Get([]byte("key1"))
	assert.False(t, found)
	assert.Nil(t, value)

	// 追加 Delta
	_ = dc.Append(DeltaOpInsert, []byte("key1"), []byte("value1"))
	_ = dc.Append(DeltaOpUpdate, []byte("key1"), []byte("value2"))

	// 查找（应返回最新值）
	value, found = dc.Get([]byte("key1"))
	assert.True(t, found)
	assert.Equal(t, []byte("value2"), value)

	// 追加删除 Delta
	_ = dc.Append(DeltaOpDelete, []byte("key1"), nil)

	// 查找（应返回未找到）
	value, found = dc.Get([]byte("key1"))
	assert.False(t, found)
	assert.Nil(t, value)
}

func TestDeltaChain_CheckExists(t *testing.T) {
	dc := NewDeltaChain(8, 64)

	// 不存在
	exists, deleted := dc.CheckExists([]byte("key1"))
	assert.False(t, exists)
	assert.False(t, deleted)

	// 插入
	_ = dc.Append(DeltaOpInsert, []byte("key1"), []byte("value1"))
	exists, deleted = dc.CheckExists([]byte("key1"))
	assert.True(t, exists)
	assert.False(t, deleted)

	// 删除
	_ = dc.Append(DeltaOpDelete, []byte("key1"), nil)
	exists, deleted = dc.CheckExists([]byte("key1"))
	assert.True(t, exists)
	assert.True(t, deleted)
}

func TestDeltaChain_Clear(t *testing.T) {
	dc := NewDeltaChain(8, 64)

	// 追加 Delta
	_ = dc.Append(DeltaOpInsert, []byte("key1"), []byte("value1"))
	_ = dc.Append(DeltaOpInsert, []byte("key2"), []byte("value2"))
	assert.Equal(t, 2, dc.Len())

	// 清空
	dc.Clear()
	assert.Equal(t, 0, dc.Len())
	assert.True(t, dc.IsEmpty())
	assert.Equal(t, uint16(0), dc.Size())
}

func TestDeltaChain_Len_Size(t *testing.T) {
	dc := NewDeltaChain(8, 64)

	assert.Equal(t, 0, dc.Len())
	assert.Equal(t, uint16(0), dc.Size())

	_ = dc.Append(DeltaOpInsert, []byte("key1"), []byte("value1"))
	assert.Equal(t, 1, dc.Len())
	assert.Equal(t, uint16(10), dc.Size()) // 3 + 7

	_ = dc.Append(DeltaOpInsert, []byte("key2"), []byte("value2"))
	assert.Equal(t, 2, dc.Len())
	assert.Equal(t, uint16(20), dc.Size())
}

func TestDeltaChain_IsEmpty(t *testing.T) {
	dc := NewDeltaChain(8, 64)

	assert.True(t, dc.IsEmpty())

	_ = dc.Append(DeltaOpInsert, []byte("key1"), []byte("value1"))
	assert.False(t, dc.IsEmpty())

	dc.Clear()
	assert.True(t, dc.IsEmpty())
}

func TestDeltaChain_Entries(t *testing.T) {
	dc := NewDeltaChain(8, 64)

	_ = dc.Append(DeltaOpInsert, []byte("key1"), []byte("value1"))
	_ = dc.Append(DeltaOpInsert, []byte("key2"), []byte("value2"))

	entries := dc.Entries()
	assert.Equal(t, 2, len(entries))

	// 修改返回值不应影响原数据
	entries[0].key[0] = 'X'

	entries2 := dc.Entries()
	assert.Equal(t, []byte("key1"), entries2[0].key)
}

func TestDeltaChain_Clone(t *testing.T) {
	dc := NewDeltaChain(8, 64)

	_ = dc.Append(DeltaOpInsert, []byte("key1"), []byte("value1"))
	_ = dc.Append(DeltaOpDelete, []byte("key2"), nil)

	clone := dc.Clone()

	// 验证克隆内容
	assert.Equal(t, dc.Len(), clone.Len())
	assert.Equal(t, dc.Size(), clone.Size())

	// 修改原数据不应影响克隆
	_ = dc.Append(DeltaOpInsert, []byte("key3"), []byte("value3"))
	assert.Equal(t, 3, dc.Len())
	assert.Equal(t, 2, clone.Len())
}

func TestDeltaChain_Merge(t *testing.T) {
	dc1 := NewDeltaChain(8, 64)
	dc2 := NewDeltaChain(8, 64)

	_ = dc1.Append(DeltaOpInsert, []byte("key1"), []byte("value1"))
	_ = dc2.Append(DeltaOpInsert, []byte("key2"), []byte("value2"))

	err := dc1.Merge(dc2)
	require.NoError(t, err)

	assert.Equal(t, 2, dc1.Len())
	assert.Equal(t, 1, dc2.Len()) // dc2 不变
}

func TestDeltaChain_Merge_Full(t *testing.T) {
	dc1 := NewDeltaChain(2, 64) // maxLength=2
	dc2 := NewDeltaChain(2, 64)

	_ = dc1.Append(DeltaOpInsert, []byte("key1"), []byte("value1"))
	_ = dc1.Append(DeltaOpInsert, []byte("key1b"), []byte("value1b")) // dc1 已满 (2/2)
	_ = dc2.Append(DeltaOpInsert, []byte("key2"), []byte("value2"))

	// dc1 已满，合并应该失败
	err := dc1.Merge(dc2)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrDeltaFull)
}

func TestDeltaChain_ConcurrentAppend(t *testing.T) {
	dc := NewDeltaChain(100, 10240)

	const goroutines = 10
	const appendsPerGoroutine = 10

	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < appendsPerGoroutine; j++ {
				key := []byte{byte(id), byte(j)}
				value := []byte("value")
				_ = dc.Append(DeltaOpInsert, key, value)
			}
		}(i)
	}

	wg.Wait()

	// 验证最终状态
	assert.Equal(t, goroutines*appendsPerGoroutine, dc.Len())
}

func TestDeltaChain_ConcurrentRead(t *testing.T) {
	dc := NewDeltaChain(100, 10240)

	// 预填充数据
	for i := 0; i < 100; i++ {
		_ = dc.Append(DeltaOpInsert, []byte{byte(i)}, []byte("value"))
	}

	const goroutines = 10
	const readsPerGoroutine = 100

	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < readsPerGoroutine; j++ {
				_, _ = dc.Get([]byte{byte(j)})
			}
		}()
	}

	wg.Wait()

	// 验证数据完整性
	value, found := dc.Get([]byte{50})
	assert.True(t, found)
	assert.Equal(t, []byte("value"), value)
}

func TestDeltaChain_ConcurrentReadWrite(t *testing.T) {
	dc := NewDeltaChain(100, 10240)

	const goroutines = 10
	const opsPerGoroutine = 50

	var wg sync.WaitGroup

	// 写入
	for i := 0; i < goroutines/2; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				key := []byte{byte(id), byte(j)}
				_ = dc.Append(DeltaOpInsert, key, []byte("value"))
			}
		}(i)
	}

	// 读取
	for i := goroutines / 2; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				_, _ = dc.Get([]byte{byte(j)})
			}
		}()
	}

	wg.Wait()
}

// 基准测试
func BenchmarkDeltaChain_Append(b *testing.B) {
	dc := NewDeltaChain(10000, 60000)
	key := []byte("key")
	value := []byte("value")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = dc.Append(DeltaOpInsert, key, value)
	}
}

func BenchmarkDeltaChain_Get(b *testing.B) {
	dc := NewDeltaChain(10000, 60000)

	// 预填充 100 个 Delta
	for i := 0; i < 100; i++ {
		_ = dc.Append(DeltaOpInsert, []byte{byte(i)}, []byte("value"))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = dc.Get([]byte{50})
	}
}

func BenchmarkDeltaChain_CompactTo(b *testing.B) {
	mp := NewMiniPage(L3)

	// 预填充 Mini-Page
	for i := 0; i < 50; i++ {
		key := []byte{byte(i)}
		value := []byte("value")
		mp.slots = append(mp.slots, Slot{key: key, value: value})
		mp.slotMap[string(key)] = i
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		dc := NewDeltaChain(100, 10240)
		for j := 0; j < 10; j++ {
			_ = dc.Append(DeltaOpInsert, []byte{byte(j)}, []byte("value"))
		}
		b.StartTimer()

		_ = dc.CompactTo(mp)
	}
}

// TestDeltaChain_CompactTo_Coverage 提升 CompactTo 覆盖率
func TestDeltaChain_CompactTo_Coverage(t *testing.T) {
	deltaChain := NewDeltaChain(10, 256)

	// 添加一些 Delta
	for i := 0; i < 5; i++ {
		key := []byte{byte(i)}
		value := []byte("value")
		err := deltaChain.Append(DeltaOpInsert, key, value)
		require.NoError(t, err)
	}

	// 创建目标 Mini-Page
	target := NewMiniPage(L1)

	// Compact 到目标
	err := deltaChain.CompactTo(target)
	require.NoError(t, err)

	// 验证数据已合并
	assert.Equal(t, 0, deltaChain.Len())
}
