// Package rpc RPC 层性能基准测试
package rpc

import (
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// BenchmarkBroadcastProgress_Creation 测试 BroadcastProgress 创建性能
// 这是 sync.Pool 优化的目标
func BenchmarkBroadcastProgress_Creation(b *testing.B) {
	peers := make([]model.PeerID, 10)
	for i := range peers {
		peers[i] = model.PeerID("node-" + string(rune('0'+i)))
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = NewBroadcastProgress("benchmark-task", peers)
	}
}

// BenchmarkBroadcastProgress_ConcurrentCreate 并发创建测试
// 模拟多个 goroutine 同时创建 BroadcastProgress
func BenchmarkBroadcastProgress_ConcurrentCreate(b *testing.B) {
	peers := make([]model.PeerID, 10)
	for i := range peers {
		peers[i] = model.PeerID("node-" + string(rune('0'+i)))
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = NewBroadcastProgress("parallel-task", peers)
		}
	})
}

// BenchmarkBroadcastProgress_Stats 测试统计查询性能
func BenchmarkBroadcastProgress_Stats(b *testing.B) {
	peers := make([]model.PeerID, 10)
	for i := range peers {
		peers[i] = model.PeerID("node-" + string(rune('0'+i)))
	}
	tracker := NewBroadcastProgress("stats-task", peers)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		tracker.Stats()
	}
}

// BenchmarkBroadcastProgress_HighFrequency 高频场景模拟
// 模拟 Raft 心跳等高频广播场景
func BenchmarkBroadcastProgress_HighFrequency(b *testing.B) {
	peers := make([]model.PeerID, 5) // Raft 通常 3-5 节点

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		tracker := NewBroadcastProgress("heartbeat", peers)
		// 模拟快速使用后丢弃
		tracker.Stats()
		// tracker 会在这里被 GC
	}
}

// BenchmarkBroadcastProgress_PoolCreation 使用对象池的创建性能测试
// 与 BenchmarkBroadcastProgress_Creation 对比，展示 sync.Pool 的优化效果
func BenchmarkBroadcastProgress_PoolCreation(b *testing.B) {
	peers := make([]model.PeerID, 10)
	for i := range peers {
		peers[i] = model.PeerID("node-" + string(rune('0'+i)))
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		bp := acquireBroadcastProgress("benchmark-task", peers)
		// 模拟使用
		bp.Stats()
		// 归还到池中
		releaseBroadcastProgress(bp)
	}
}

// BenchmarkBroadcastProgress_PoolConcurrentCreate 使用对象池的并发创建测试
// 与 BenchmarkBroadcastProgress_ConcurrentCreate 对比
func BenchmarkBroadcastProgress_PoolConcurrentCreate(b *testing.B) {
	peers := make([]model.PeerID, 10)
	for i := range peers {
		peers[i] = model.PeerID("node-" + string(rune('0'+i)))
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			bp := acquireBroadcastProgress("parallel-task", peers)
			bp.Stats()
			releaseBroadcastProgress(bp)
		}
	})
}

// BenchmarkBroadcastProgress_PoolHighFrequency 使用对象池的高频场景测试
// 与 BenchmarkBroadcastProgress_HighFrequency 对比，这是 sync.Pool 优化的主要目标场景
func BenchmarkBroadcastProgress_PoolHighFrequency(b *testing.B) {
	peers := make([]model.PeerID, 5) // Raft 通常 3-5 节点

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		bp := acquireBroadcastProgress("heartbeat", peers)
		bp.Stats()
		releaseBroadcastProgress(bp)
	}
}

// BenchmarkBroadcastProgress_Comparison 对比测试
// 展示直接分配 vs 对象池的性能差异
func BenchmarkBroadcastProgress_Comparison(b *testing.B) {
	peers := make([]model.PeerID, 5)

	b.Run("DirectAllocation", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			bp := NewBroadcastProgress("heartbeat", peers)
			bp.Stats()
			// bp 在这里被 GC
		}
	})

	b.Run("ObjectPool", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			bp := acquireBroadcastProgress("heartbeat", peers)
			bp.Stats()
			releaseBroadcastProgress(bp)
		}
	})
}
