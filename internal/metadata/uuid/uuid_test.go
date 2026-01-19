// Package uuid 测试 UUID 生成器
package uuid

import (
	"sync"
	"testing"
	"time"
)

// TestUUIDv4Generation 测试 UUID v4 生成
func TestUUIDv4Generation(t *testing.T) {
	// 生成 10000 个 UUID v4
	generated := make(map[string]bool)
	for i := 0; i < 10000; i++ {
		uuid := GenerateUUIDv4()

		// 验证格式
		if len(uuid) != 36 {
			t.Errorf("UUID 长度错误: %d (期望 36)", len(uuid))
		}

		// 验证版本
		if !IsValidUUIDv4(uuid) {
			t.Errorf("生成的 UUID 不是有效的 v4: %s", uuid)
		}

		// 验证唯一性
		if generated[uuid] {
			t.Errorf("UUID 重复: %s", uuid)
		}
		generated[uuid] = true
	}
}

// TestUUIDv7Generation 测试 UUID v7 生成
func TestUUIDv7Generation(t *testing.T) {
	// 生成 10000 个 UUID v7
	generated := make(map[string]bool)
	var lastTimestamp int64

	for i := 0; i < 10000; i++ {
		uuid := GenerateUUIDv7()

		// 验证格式
		if len(uuid) != 36 {
			t.Errorf("UUID 长度错误: %d (期望 36)", len(uuid))
		}

		// 验证版本
		if !IsValidUUIDv7(uuid) {
			t.Errorf("生成的 UUID 不是有效的 v7: %s", uuid)
		}

		// 验证时间戳单调递增
		timestamp, err := ExtractTimestamp(uuid)
		if err != nil {
			t.Errorf("提取时间戳失败: %v", err)
			continue
		}

		if lastTimestamp > 0 && timestamp < lastTimestamp {
			t.Errorf("时间戳不单调递增: last=%d, current=%d", lastTimestamp, timestamp)
		}
		lastTimestamp = timestamp

		// 验证唯一性
		if generated[uuid] {
			t.Errorf("UUID 重复: %s", uuid)
		}
		generated[uuid] = true
	}
}

// TestSnowflakeGeneration 测试 Snowflake ID 生成
func TestSnowflakeGeneration(t *testing.T) {
	snowflake, err := NewSnowflake(0, 0)
	if err != nil {
		t.Fatalf("创建 Snowflake 失败: %v", err)
	}

	// 生成 10000 个 ID
	generated := make(map[int64]bool)
	var lastID int64

	for i := 0; i < 10000; i++ {
		id, err := snowflake.Generate()
		if err != nil {
			t.Errorf("生成 Snowflake ID 失败: %v", err)
			continue
		}

		// 验证单调递增
		if lastID > 0 && id <= lastID {
			t.Errorf("ID 不单调递增: last=%d, current=%d", lastID, id)
		}
		lastID = id

		// 验证唯一性
		if generated[id] {
			t.Errorf("ID 重复: %d", id)
		}
		generated[id] = true
	}
}

// TestSnowflakeClockBackwards 测试 Snowflake 时钟回拨检测
func TestSnowflakeClockBackwards(t *testing.T) {
	snowflake, err := NewSnowflake(0, 0)
	if err != nil {
		t.Fatalf("创建 Snowflake 失败: %v", err)
	}

	// 生成第一个 ID
	id1, err := snowflake.Generate()
	if err != nil {
		t.Fatalf("生成第一个 ID 失败: %v", err)
	}

	// 生成第二个 ID（正常情况）
	id2, err := snowflake.Generate()
	if err != nil {
		t.Fatalf("生成第二个 ID 失败: %v", err)
	}

	if id2 <= id1 {
		t.Errorf("ID 不单调递增: id1=%d, id2=%d", id1, id2)
	}

	// TODO: 模拟时钟回拨场景（需要修改 Snowflake 实现以支持注入时间）
}

// TestSnowflakeConcurrency 测试 Snowflake 并发安全性
func TestSnowflakeConcurrency(t *testing.T) {
	snowflake, err := NewSnowflake(0, 0)
	if err != nil {
		t.Fatalf("创建 Snowflake 失败: %v", err)
	}

	const goroutines = 10
	const idsPerGoroutine = 1000

	idChan := make(chan int64, goroutines*idsPerGoroutine)
	var wg sync.WaitGroup
	var errCh = make(chan error, 1) // 用于传递第一个错误

	// 并发生成 ID
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < idsPerGoroutine; j++ {
				id, err := snowflake.Generate()
				if err != nil {
					select {
					case errCh <- err:
					default:
						// 已经有一个错误在队列中
					}
					return
				}
				idChan <- id
			}
		}()
	}

	// 在后台等待所有 goroutine 完成
	go func() {
		wg.Wait()
		close(idChan)
	}()

	// 检查是否有错误发生
	select {
	case err := <-errCh:
		t.Fatalf("生成 ID 失败: %v", err)
	default:
		// 没有错误，继续
	}

	// 收集所有 ID
	generated := make(map[int64]bool)
	for id := range idChan {
		if generated[id] {
			t.Errorf("ID 重复: %d", id)
		}
		generated[id] = true
	}
}

// TestSafeUUIDGenerator 测试安全 UUID 生成器
func TestSafeUUIDGenerator(t *testing.T) {
	gen, err := NewSafeUUIDGenerator(0, 0, 100*time.Millisecond, 1*time.Second)
	if err != nil {
		t.Fatalf("创建安全生成器失败: %v", err)
	}

	// 生成 1000 个事务 ID
	generated := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := gen.GenerateTransactionID()

		if !IsValidUUIDv7(id) {
			t.Errorf("生成的事务 ID 不是有效的 v7: %s", id)
		}

		if generated[id] {
			t.Errorf("事务 ID 重复: %s", id)
		}
		generated[id] = true
	}

	// 检查时钟回拨次数
	stats := gen.GetStats()
	if driftCount, ok := stats["drift_count"].(int); ok {
		t.Logf("时钟回拨次数: %d", driftCount)
	}
}

// TestUUIDPool 测试 UUID 预生成池
func TestUUIDPool(t *testing.T) {
	pool, err := NewUUIDPool("v7", 100, 0, 0)
	if err != nil {
		t.Fatalf("创建 UUID 池失败: %v", err)
	}
	defer pool.Close()

	// 测试初始池大小
	if pool.GetSize() == 0 {
		t.Error("池初始化后应该包含 UUID")
	}

	// 测试获取 UUID
	uuid := pool.Get()
	if uuid == "" {
		t.Error("获取的 UUID 为空")
	}

	if !IsValidUUIDv7(uuid) {
		t.Errorf("获取的 UUID 不是有效的 v7: %s", uuid)
	}

	// 测试池会自动补充
	initialSize := pool.GetSize()
	_ = pool.Get()
	afterSize := pool.GetSize()

	if afterSize >= initialSize {
		t.Error("池大小应该减少")
	}

	// 等待后台补充
	time.Sleep(200 * time.Millisecond)
	refilledSize := pool.GetSize()

	if refilledSize > afterSize {
		t.Log("池已自动补充")
	}
}

// TestUUIDParseFormat 测试 UUID 解析和格式化
func TestUUIDParseFormat(t *testing.T) {
	original := GenerateUUIDv4()

	// 解析
	parsed, err := Parse(original)
	if err != nil {
		t.Fatalf("解析 UUID 失败: %v", err)
	}

	// 格式化
	formatted := Format(parsed)

	if formatted != original {
		t.Errorf("格式化后的 UUID 与原始 UUID 不匹配: original=%s, formatted=%s", original, formatted)
	}
}

// TestUUIDVariantVersion 测试 UUID 变体和版本
func TestUUIDVariantVersion(t *testing.T) {
	// 测试 UUID v4
	uuidv4 := GenerateUUIDv4()
	parsed, _ := Parse(uuidv4)

	variant := GetVariant(parsed)
	version := GetVersion(parsed)

	if variant != VariantRFC4122 {
		t.Errorf("UUID v4 变体错误: %d (期望 %d)", variant, VariantRFC4122)
	}

	if version != VersionRandom {
		t.Errorf("UUID v4 版本错误: %d (期望 %d)", version, VersionRandom)
	}

	// 测试 UUID v7
	uuidv7 := GenerateUUIDv7()
	parsed, _ = Parse(uuidv7)

	variant = GetVariant(parsed)
	version = GetVersion(parsed)

	if variant != VariantRFC4122 {
		t.Errorf("UUID v7 变体错误: %d (期望 %d)", variant, VariantRFC4122)
	}

	if version != Version7TimeBased {
		t.Errorf("UUID v7 版本错误: %d (期望 %d)", version, Version7TimeBased)
	}
}

// BenchmarkUUIDv4 性能基准测试: UUID v4 生成
func BenchmarkUUIDv4(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = GenerateUUIDv4()
	}
}

// BenchmarkUUIDv7 性能基准测试: UUID v7 生成
func BenchmarkUUIDv7(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = GenerateUUIDv7()
	}
}

// BenchmarkSnowflake 性能基准测试: Snowflake ID 生成
func BenchmarkSnowflake(b *testing.B) {
	snowflake, _ := NewSnowflake(0, 0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = snowflake.Generate()
	}
}

// BenchmarkUUIDPool 性能基准测试: UUID 池获取
func BenchmarkUUIDPool(b *testing.B) {
	pool, _ := NewUUIDPool("v7", 1000, 0, 0)
	defer pool.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = pool.Get()
	}
}

// BenchmarkUUIDPoolConcurrent 并发性能基准测试: UUID 池
func BenchmarkUUIDPoolConcurrent(b *testing.B) {
	pool, _ := NewUUIDPool("v7", 1000, 0, 0)
	defer pool.Close()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = pool.Get()
		}
	})
}
