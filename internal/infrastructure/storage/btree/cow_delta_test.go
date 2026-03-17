package btree

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestCOWDeltaRef_Basic 测试基本功能
func TestCOWDeltaRef_Basic(t *testing.T) {
	keys := [][]byte{[]byte("key1"), []byte("key2")}
	values := [][]byte{[]byte("val1"), []byte("val2")}

	ref := NewCOWDeltaRef(keys, values)

	// 初始状态
	assert.Equal(t, int32(0), ref.GetRefCount())
	assert.Equal(t, 0, ref.GetDeltaCount())

	// Retain 增加引用计数
	ref.Retain()
	assert.Equal(t, int32(1), ref.GetRefCount())

	ref.Retain()
	assert.Equal(t, int32(2), ref.GetRefCount())

	// Release 减少引用计数
	last := ref.Release()
	assert.False(t, last) // 还有 1 个引用
	assert.Equal(t, int32(1), ref.GetRefCount())

	last = ref.Release()
	assert.True(t, last) // 最后一个引用
	assert.Equal(t, int32(0), ref.GetRefCount())
}

// TestCOWDeltaRef_Deltas 测试增量链操作
func TestCOWDeltaRef_Deltas(t *testing.T) {
	keys := [][]byte{[]byte("key1"), []byte("key2")}
	values := [][]byte{[]byte("val1"), []byte("val2")}

	ref := NewCOWDeltaRef(keys, values)

	// 添加增量操作
	ref.AppendDelta(Delta{op: DeltaInsert, key: []byte("key3"), value: []byte("val3")})
	assert.Equal(t, 1, ref.GetDeltaCount())

	ref.AppendDelta(Delta{op: DeltaUpdate, key: []byte("key1"), value: []byte("newval1")})
	assert.Equal(t, 2, ref.GetDeltaCount())

	ref.AppendDelta(Delta{op: DeltaDelete, key: []byte("key2"), value: nil})
	assert.Equal(t, 3, ref.GetDeltaCount())

	// 获取增量快照
	deltas := ref.GetDeltas()
	assert.Equal(t, 3, len(deltas))
	assert.Equal(t, DeltaInsert, deltas[0].op)
	assert.Equal(t, DeltaUpdate, deltas[1].op)
	assert.Equal(t, DeltaDelete, deltas[2].op)
}

// TestCOWDeltaRef_ShouldMaterialize 测试物化判断
func TestCOWDeltaRef_ShouldMaterialize(t *testing.T) {
	keys := make([][]byte, 100)
	values := make([][]byte, 100)
	for i := 0; i < 100; i++ {
		keys[i] = []byte{byte(i)}
		values[i] = []byte{byte(i + 100)}
	}

	t.Run("数量超限", func(t *testing.T) {
		ref := NewCOWDeltaRef(keys, values)
		for i := 0; i < 11; i++ {
			ref.AppendDelta(Delta{op: DeltaInsert, key: []byte{byte(i)}, value: []byte{byte(i)}})
		}
		assert.True(t, ref.ShouldMaterialize(100, 1))
	})

	t.Run("比例超限", func(t *testing.T) {
		// 10 个键，3 个增量 = 30% > 20%
		smallKeys := keys[:10]
		smallValues := values[:10]
		ref := NewCOWDeltaRef(smallKeys, smallValues)
		for i := 0; i < 3; i++ {
			ref.AppendDelta(Delta{op: DeltaInsert, key: []byte{byte(i)}, value: []byte{byte(i)}})
		}
		assert.True(t, ref.ShouldMaterialize(10, 1))
	})

	t.Run("引用计数高", func(t *testing.T) {
		ref := NewCOWDeltaRef(keys, values)
		for i := 0; i < 5; i++ {
			ref.AppendDelta(Delta{op: DeltaInsert, key: []byte{byte(i)}, value: []byte{byte(i)}})
		}
		assert.True(t, ref.ShouldMaterialize(100, 11))
	})

	t.Run("不需要物化", func(t *testing.T) {
		ref := NewCOWDeltaRef(keys, values)
		for i := 0; i < 5; i++ {
			ref.AppendDelta(Delta{op: DeltaInsert, key: []byte{byte(i)}, value: []byte{byte(i)}})
		}
		assert.False(t, ref.ShouldMaterialize(100, 1))
	})
}

// TestCOWDeltaRef_CompactDeltas 测试增量链压缩
func TestCOWDeltaRef_CompactDeltas(t *testing.T) {
	keys := [][]byte{[]byte("key1"), []byte("key2")}
	values := [][]byte{[]byte("val1"), []byte("val2")}

	ref := NewCOWDeltaRef(keys, values)

	// 添加多个对同一个 key 的操作
	ref.AppendDelta(Delta{op: DeltaInsert, key: []byte("key3"), value: []byte("val3")})
	ref.AppendDelta(Delta{op: DeltaUpdate, key: []byte("key3"), value: []byte("val3-updated")})
	ref.AppendDelta(Delta{op: DeltaUpdate, key: []byte("key3"), value: []byte("val3-final")})

	assert.Equal(t, 3, ref.GetDeltaCount())

	ref.CompactDeltas()

	// 压缩后只保留最新的操作
	assert.Equal(t, 1, ref.GetDeltaCount())
	deltas := ref.GetDeltas()
	assert.Equal(t, []byte("val3-final"), deltas[0].value)
}

// TestCOWDeltaRef_Version 测试版本号
func TestCOWDeltaRef_Version(t *testing.T) {
	keys := [][]byte{[]byte("key1")}
	values := [][]byte{[]byte("val1")}

	ref := NewCOWDeltaRef(keys, values)
	assert.Equal(t, uint64(0), ref.GetVersion())

	ref.AppendDelta(Delta{op: DeltaInsert, key: []byte("key2"), value: []byte("val2")})
	assert.Equal(t, uint64(1), ref.GetVersion())

	ref.AppendDelta(Delta{op: DeltaUpdate, key: []byte("key1"), value: []byte("newval1")})
	assert.Equal(t, uint64(2), ref.GetVersion())
}

// TestCOWDeltaRef_Concurrent 测试并发安全性
func TestCOWDeltaRef_Concurrent(t *testing.T) {
	keys := make([][]byte, 100)
	values := make([][]byte, 100)
	for i := 0; i < 100; i++ {
		keys[i] = []byte{byte(i)}
		values[i] = []byte{byte(i + 100)}
	}

	ref := NewCOWDeltaRef(keys, values)
	ref.Retain() // 初始引用

	var wg sync.WaitGroup
	const goroutines = 10
	const opsPerGoroutine = 100

	// 并发写入
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				key := []byte{byte(idx*opsPerGoroutine + j)}
				ref.AppendDelta(Delta{op: DeltaInsert, key: key, value: key})
			}
		}(i)
	}

	// 并发读取
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				_ = ref.GetDeltaCount()
				_ = ref.GetDeltas()
				_ = ref.GetVersion()
			}
		}()
	}

	// 并发引用计数操作
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				ref.Retain()
				_ = ref.GetRefCount()
			}
		}()
	}

	wg.Wait()

	// 验证结果
	assert.Equal(t, goroutines*opsPerGoroutine, ref.GetDeltaCount())
	assert.Equal(t, int32(1+goroutines*10), ref.GetRefCount())

	// 清理引用
	for i := 0; i < goroutines*10; i++ {
		ref.Release()
	}
	assert.Equal(t, int32(1), ref.GetRefCount())
}

// TestCOWDeltaRef_SharedData 测试共享数据
func TestCOWDeltaRef_SharedData(t *testing.T) {
	keys := [][]byte{[]byte("key1"), []byte("key2")}
	values := [][]byte{[]byte("val1"), []byte("val2")}

	_ = NewCOWDeltaRef(keys, values) // 创建 COWDeltaRef 共享 keys/values

	// 注意：在实际使用中，应该确保原始数据不被修改
	// 因为 keys/values 是共享引用，任何修改都会影响所有使用它的 COWDeltaRef
}
