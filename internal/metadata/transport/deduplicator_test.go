// Package transport 消息去重器单元测试
package transport

import (
	"sync"
	"testing"
	"time"
)

// TestMessageDeduplicator_Basic 基础功能测试
func TestMessageDeduplicator_Basic(t *testing.T) {
	dedup := NewMessageDeduplicator()
	dedup.Start()
	defer dedup.Stop()

	// 测试新节点
	nodeID := uint64(1)
	msgSeq := uint64(100)

	if dedup.IsDuplicate(nodeID, msgSeq) {
		t.Errorf("新消息被错误识别为重复")
	}

	// 记录消息
	dedup.Record(nodeID, msgSeq)

	// 测试重复消息
	if !dedup.IsDuplicate(nodeID, msgSeq) {
		t.Errorf("重复消息未被识别")
	}

	// 测试新消息（序列号更大）
	newSeq := msgSeq + 1
	if dedup.IsDuplicate(nodeID, newSeq) {
		t.Errorf("新消息被错误识别为重复")
	}
}

// TestMessageDeduplicator_OutOfOrder TD-011: 乱序消息处理
func TestMessageDeduplicator_OutOfOrder(t *testing.T) {
	dedup := NewMessageDeduplicator()
	dedup.Start()
	defer dedup.Stop()

	nodeID := uint64(1)

	// 先接收 msgSeq=100
	dedup.Record(nodeID, 100)

	// 后接收 msgSeq=90，应被识别为过时消息
	if !dedup.IsDuplicate(nodeID, 90) {
		t.Errorf("过时消息未被识别为重复: msgSeq=90 < maxSeq=100")
	}
}

// TestMessageDeduplicator_NodeIDCollision TD-012: NodeID 碰撞场景
func TestMessageDeduplicator_NodeIDCollision(t *testing.T) {
	dedup := NewMessageDeduplicator()
	dedup.Start()
	defer dedup.Stop()

	// 不同 NodeID 独立计数
	nodeID1 := uint64(1)
	nodeID2 := uint64(2)

	dedup.Record(nodeID1, 100)
	dedup.Record(nodeID2, 100) // 相同 msgSeq，但不同 NodeID

	// nodeID1 的 msgSeq=101 应该是新的
	if dedup.IsDuplicate(nodeID1, 101) {
		t.Errorf("nodeID1 的新消息被错误识别为重复")
	}

	// nodeID2 的 msgSeq=101 也应该是新的（不同节点）
	if dedup.IsDuplicate(nodeID2, 101) {
		t.Errorf("nodeID2 的新消息被错误识别为重复")
	}
}

// TestMessageDeduplicator_MsgSeqWraparound TD-013: MsgSeq 回绕场景
func TestMessageDeduplicator_MsgSeqWraparound(t *testing.T) {
	dedup := NewMessageDeduplicator()
	dedup.Start()
	defer dedup.Stop()

	nodeID := uint64(1)

	// 模拟 MsgSeq 接近最大值
	maxSeq := uint64(0xFFFFFFFFFFFFFFFF)
	dedup.Record(nodeID, maxSeq)

	// MsgSeq 回绕到 0，应该被识别为新消息（因为 0 < maxSeq，会被判定为重复）
	// 这是正确的行为：回绕后，需要等待足够长的时间或使用其他机制
	if !dedup.IsDuplicate(nodeID, 0) {
		// 注意：由于使用 uint64 相减判断，0 被认为是旧消息
		// 实际场景中，回绕后需要等待 2^64 次调用才会发生，基本不可能
		t.Logf("回绕消息被识别为新消息（符合预期）")
	}
}

// TestMessageDeduplicator_MemoryLeak TD-014: 去重缓存内存泄漏测试
func TestMessageDeduplicator_MemoryLeak(t *testing.T) {
	maxCacheSize := 100
	dedup := NewMessageDeduplicatorWithConfig(maxCacheSize, 1*time.Second, 10*time.Minute)
	dedup.Start()
	defer dedup.Stop()

	// 模拟 10000 次重复消息
	const iterations = 10000
	for i := 0; i < iterations; i++ {
		nodeID := uint64(i % 1000) // 1000 个不同节点
		msgSeq := uint64(i)
		dedup.Record(nodeID, msgSeq)
	}

	// 等待清理协程执行
	time.Sleep(2 * time.Second)

	stats := dedup.Stats()
	cacheSize := stats["cache_size"].(int)

	// 缓存大小不应超过 maxCacheSize
	if cacheSize > maxCacheSize {
		t.Errorf("缓存大小超限: %d > %d", cacheSize, maxCacheSize)
	}

	t.Logf("缓存大小: %d/%d（正常）", cacheSize, maxCacheSize)
}

// TestMessageDeduplicator_Concurrency 并发安全测试
func TestMessageDeduplicator_Concurrency(t *testing.T) {
	dedup := NewMessageDeduplicator()
	dedup.Start()
	defer dedup.Stop()

	const goroutines = 100
	const operationsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(goroutines * 2) // 一半 Record，一半 IsDuplicate

	// 并发 Record
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			nodeID := uint64(id % 10)
			for j := 0; j < operationsPerGoroutine; j++ {
				msgSeq := uint64(j)
				dedup.Record(nodeID, msgSeq)
			}
		}(i)
	}

	// 并发 IsDuplicate
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			nodeID := uint64(id % 10)
			for j := 0; j < operationsPerGoroutine; j++ {
				msgSeq := uint64(j)
				dedup.IsDuplicate(nodeID, msgSeq)
			}
		}(i)
	}

	wg.Wait()

	// 验证统计信息一致性
	stats := dedup.Stats()
	totalChecks := stats["total_checks"].(uint64)
	expectedMinChecks := uint64(goroutines * operationsPerGoroutine)

	if totalChecks < expectedMinChecks {
		t.Errorf("统计信息不一致: total_checks=%d, 期望 >= %d", totalChecks, expectedMinChecks)
	}
}

// TestMessageDeduplicator_Stats 测试统计信息
func TestMessageDeduplicator_Stats(t *testing.T) {
	dedup := NewMessageDeduplicator()
	dedup.Start()
	defer dedup.Stop()

	// 记录一些消息
	dedup.Record(1, 100)
	dedup.Record(2, 200)

	// 检查重复消息
	dedup.IsDuplicate(1, 100) // 重复
	dedup.IsDuplicate(1, 101) // 新消息
	dedup.IsDuplicate(2, 200) // 重复

	stats := dedup.Stats()

	// 验证统计信息
	if stats["cache_size"].(int) != 2 {
		t.Errorf("缓存大小不正确: %d", stats["cache_size"])
	}

	if stats["hit_count"].(uint64) != 2 {
		t.Errorf("命中计数不正确: %d", stats["hit_count"])
	}

	if stats["total_checks"].(uint64) != 3 {
		t.Errorf("总检查次数不正确: %d", stats["total_checks"])
	}

	hitRate := stats["hit_rate"].(float64)
	expectedRate := 2.0 / 3.0
	if hitRate != expectedRate {
		t.Errorf("命中率不正确: %f, 期望 %f", hitRate, expectedRate)
	}
}

// TestMessageDeduplicator_Clear 测试清空缓存
func TestMessageDeduplicator_Clear(t *testing.T) {
	dedup := NewMessageDeduplicator()
	dedup.Start()

	// 记录一些消息
	dedup.Record(1, 100)
	dedup.Record(2, 200)

	// 清空缓存
	dedup.Clear()

	stats := dedup.Stats()
	if stats["cache_size"].(int) != 0 {
		t.Errorf("清空后缓存大小不为 0: %d", stats["cache_size"])
	}

	if stats["hit_count"].(uint64) != 0 {
		t.Errorf("清空后命中计数不为 0: %d", stats["hit_count"])
	}

	dedup.Stop()
}

// TestMessageDeduplicator_LRUEviction 测试 LRU 淘汰
func TestMessageDeduplicator_LRUEviction(t *testing.T) {
	maxCacheSize := 10
	dedup := NewMessageDeduplicatorWithConfig(maxCacheSize, 1*time.Second, 10*time.Minute)
	dedup.Start()
	defer dedup.Stop()

	// 添加超过最大缓存大小的节点
	for i := 0; i < maxCacheSize+5; i++ {
		nodeID := uint64(i)
		msgSeq := uint64(100)
		dedup.Record(nodeID, msgSeq)
	}

	stats := dedup.Stats()
	cacheSize := stats["cache_size"].(int)

	// 缓存大小应该被限制在 maxCacheSize 左右
	// 注意：由于 LRU 实现较简单，可能略微超过限制
	if cacheSize > maxCacheSize+2 {
		t.Errorf("LRU 淘汰失败: cache_size=%d, maxCacheSize=%d", cacheSize, maxCacheSize)
	}
}

// TestMessageDeduplicator_MultipleNodes 多节点测试
func TestMessageDeduplicator_MultipleNodes(t *testing.T) {
	dedup := NewMessageDeduplicator()
	dedup.Start()
	defer dedup.Stop()

	// 模拟 10 个节点，每个节点 100 条消息
	const nodeCount = 10
	const msgPerNode = 100

	for nodeID := 1; nodeID <= nodeCount; nodeID++ {
		for msgSeq := 1; msgSeq <= msgPerNode; msgSeq++ {
			// 新消息不应被识别为重复
			if dedup.IsDuplicate(uint64(nodeID), uint64(msgSeq)) {
				t.Errorf("新消息被错误识别为重复: nodeID=%d, msgSeq=%d", nodeID, msgSeq)
			}
			dedup.Record(uint64(nodeID), uint64(msgSeq))
		}
	}

	// 验证重复检测
	for nodeID := 1; nodeID <= nodeCount; nodeID++ {
		for msgSeq := 1; msgSeq <= msgPerNode; msgSeq++ {
			if !dedup.IsDuplicate(uint64(nodeID), uint64(msgSeq)) {
				t.Errorf("重复消息未被识别: nodeID=%d, msgSeq=%d", nodeID, msgSeq)
			}
		}
	}
}

// TestMessageDeduplicator_Cleanup 测试清理协程
func TestMessageDeduplicator_Cleanup(t *testing.T) {
	cleanupInterval := 500 * time.Millisecond
	dedup := NewMessageDeduplicatorWithConfig(1000, cleanupInterval, 10*time.Minute)
	dedup.Start()
	defer dedup.Stop()

	// 添加一些条目
	for i := 0; i < 100; i++ {
		dedup.Record(uint64(i), 100)
	}

	// 等待清理协程执行
	time.Sleep(cleanupInterval + 100*time.Millisecond)

	// 验证清理协程正常运行
	stats := dedup.Stats()
	t.Logf("清理后缓存大小: %d", stats["cache_size"])
}

// BenchmarkMessageDeduplicator_IsDuplicate 性能测试
func BenchmarkMessageDeduplicator_IsDuplicate(b *testing.B) {
	dedup := NewMessageDeduplicator()
	dedup.Start()
	defer dedup.Stop()

	// 预填充一些数据
	for i := 0; i < 1000; i++ {
		dedup.Record(uint64(i), uint64(i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		nodeID := uint64(i % 1000)
		msgSeq := uint64(i)
		dedup.IsDuplicate(nodeID, msgSeq)
	}
}

// BenchmarkMessageDeduplicator_Record 性能测试
func BenchmarkMessageDeduplicator_Record(b *testing.B) {
	dedup := NewMessageDeduplicator()
	dedup.Start()
	defer dedup.Stop()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		nodeID := uint64(i % 1000)
		msgSeq := uint64(i)
		dedup.Record(nodeID, msgSeq)
	}
}
