// Package gossip 提供事件驱动 Gossip 测试
package gossip

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jzhang405/NexKV/internal/clock"
	"github.com/jzhang405/NexKV/internal/metadata/kvstore"
)

// ==================== 测试 ====================

// TestEventDrivenGossipSync_Create 测试创建事件驱动 Gossip
func TestEventDrivenGossipSync_Create(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)

	config := &EventDrivenConfig{
		MerkleTree:    merkleTree,
		MetadataKV:    nil, // 本地模式不需要
		Transport:     nil, // 本地模式
		LocalNodeID:   "node-001",
		PeerSelector:  NewRandomPeerSelector(),
		EventChanSize: 100,
		TickerDelay:   1 * time.Second,
	}

	sync := NewEventDrivenGossipSync(config)
	defer sync.Close()

	require.NotNil(t, sync)
	require.NotNil(t, sync.eventChan)
}

// TestEventDrivenGossipSync_OnWrite 测试写入事件触发
func TestEventDrivenGossipSync_OnWrite(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)

	config := &EventDrivenConfig{
		MerkleTree:    merkleTree,
		MetadataKV:    nil,
		Transport:     nil,
		LocalNodeID:   "node-001",
		PeerSelector:  NewRandomPeerSelector(),
		EventChanSize: 100,
		TickerDelay:   10 * time.Second, // 长间隔，依赖事件触发
	}

	sync := NewEventDrivenGossipSync(config)
	defer sync.Close()

	// 触发写入事件
	sync.OnWrite("meta:node:", "node-001")

	// 等待事件处理
	time.Sleep(100 * time.Millisecond)

	// 验证统计
	stats := sync.GetStats()
	require.Equal(t, uint64(1), stats["events_received"])
	require.Equal(t, uint64(1), stats["events_processed"])
}

// TestEventDrivenGossipSync_EventDrop 测试事件丢弃（通道满）
func TestEventDrivenGossipSync_EventDrop(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)

	config := &EventDrivenConfig{
		MerkleTree:    merkleTree,
		MetadataKV:    nil,
		Transport:     nil,
		LocalNodeID:   "node-001",
		PeerSelector:  NewRandomPeerSelector(),
		EventChanSize: 5, // 小通道，容易满
		TickerDelay:   10 * time.Second,
	}

	sync := NewEventDrivenGossipSync(config)
	defer sync.Close()

	// 发送大量事件，超过通道容量
	for i := 0; i < 20; i++ {
		sync.OnWrite("meta:node:", "node-001")
	}

	// 等待事件处理
	time.Sleep(200 * time.Millisecond)

	// 验证有事件被丢弃
	stats := sync.GetStats()
	require.True(t, stats["events_dropped"].(uint64) > 0, "应该有事件被丢弃")
}

// TestEventDrivenGossipSync_TimerTrigger 测试定时器触发
func TestEventDrivenGossipSync_TimerTrigger(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)

	config := &EventDrivenConfig{
		MerkleTree:    merkleTree,
		MetadataKV:    nil,
		Transport:     nil,
		LocalNodeID:   "node-001",
		PeerSelector:  NewRandomPeerSelector(),
		EventChanSize: 100,
		TickerDelay:   200 * time.Millisecond, // 短间隔
	}

	sync := NewEventDrivenGossipSync(config)
	defer sync.Close()

	// 等待定时器触发
	time.Sleep(500 * time.Millisecond)

	// 验证定时器触发
	stats := sync.GetStats()
	require.True(t, stats["timer_triggered"].(uint64) >= 1, "定时器应该触发")
}

// TestEventDrivenGossipSync_BatchTrigger 测试批量触发（积累 5 个事件后触发）
func TestEventDrivenGossipSync_BatchTrigger(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)

	config := &EventDrivenConfig{
		MerkleTree:    merkleTree,
		MetadataKV:    nil,
		Transport:     nil,
		LocalNodeID:   "node-001",
		PeerSelector:  NewRandomPeerSelector(),
		EventChanSize: 100,
		TickerDelay:   10 * time.Second, // 长间隔
	}

	sync := NewEventDrivenGossipSync(config)
	defer sync.Close()

	// 添加已知 peer（否则不会执行同步）
	sync.AddKnownPeer("node-002")

	// 发送 5 个不同 Namespace 的事件
	for i := 0; i < 5; i++ {
		sync.OnWrite(fmt.Sprintf("meta:ns%d:", i), "key-001")
	}

	// 等待事件处理和批量触发
	time.Sleep(200 * time.Millisecond)

	// 验证事件触发
	stats := sync.GetStats()
	require.Equal(t, uint64(5), stats["events_received"])
}

// TestEventDrivenGossipSync_GetStats 测试统计信息
func TestEventDrivenGossipSync_GetStats(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)

	config := &EventDrivenConfig{
		MerkleTree:    merkleTree,
		MetadataKV:    nil,
		Transport:     nil,
		LocalNodeID:   "node-001",
		PeerSelector:  NewRandomPeerSelector(),
		EventChanSize: 100,
		TickerDelay:   1 * time.Second,
	}

	sync := NewEventDrivenGossipSync(config)
	defer sync.Close()

	// 触发一些事件
	sync.OnWrite("meta:node:", "node-001")
	sync.OnNamespaceChange("meta:cluster:")
	sync.OnPeerJoin("node-002")

	// 等待事件处理
	time.Sleep(100 * time.Millisecond)

	// 获取统计
	stats := sync.GetStats()

	require.Equal(t, uint64(3), stats["events_received"])
	require.Equal(t, uint64(3), stats["events_processed"])
	require.Contains(t, stats, "event_chan_size")
	require.Contains(t, stats, "ticker_delay")
}

// TestEventDrivenGossipSync_Close 测试关闭
func TestEventDrivenGossipSync_Close(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)

	config := &EventDrivenConfig{
		MerkleTree:    merkleTree,
		MetadataKV:    nil,
		Transport:     nil,
		LocalNodeID:   "node-001",
		PeerSelector:  NewRandomPeerSelector(),
		EventChanSize: 100,
		TickerDelay:   1 * time.Second,
	}

	sync := NewEventDrivenGossipSync(config)

	// 关闭
	err := sync.Close()
	require.NoError(t, err)

	// 关闭后发送事件不应该 panic
	sync.OnWrite("meta:node:", "node-001")
}

// TestEventDrivenGossipSync_ForceSync 测试强制同步
func TestEventDrivenGossipSync_ForceSync(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)

	config := &EventDrivenConfig{
		MerkleTree:    merkleTree,
		MetadataKV:    nil,
		Transport:     nil,
		LocalNodeID:   "node-001",
		PeerSelector:  NewRandomPeerSelector(),
		EventChanSize: 100,
		TickerDelay:   10 * time.Second,
	}

	sync := NewEventDrivenGossipSync(config)
	defer sync.Close()

	// 强制同步
	sync.ForceSync()

	// 验证触发
	stats := sync.GetStats()
	require.Equal(t, uint64(1), stats["events_triggered"])
}

// TestEventDrivenGossipSync_PeerManagement 测试 Peer 管理
func TestEventDrivenGossipSync_PeerManagement(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)

	config := &EventDrivenConfig{
		MerkleTree:    merkleTree,
		MetadataKV:    nil,
		Transport:     nil,
		LocalNodeID:   "node-001",
		PeerSelector:  NewRandomPeerSelector(),
		EventChanSize: 100,
		TickerDelay:   10 * time.Second,
	}

	sync := NewEventDrivenGossipSync(config)
	defer sync.Close()

	// 添加 Peer
	sync.AddKnownPeer("node-002")
	sync.AddKnownPeer("node-003")

	// 验证 Peer 数量
	stats := sync.GetStats()
	require.Equal(t, 2, stats["peer_count"])

	// 移除 Peer
	sync.RemoveKnownPeer("node-002")

	stats = sync.GetStats()
	require.Equal(t, 1, stats["peer_count"])
}

// TestEventDrivenGossipSync_ConcurrentWrites 测试并发写入
func TestEventDrivenGossipSync_ConcurrentWrites(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)

	config := &EventDrivenConfig{
		MerkleTree:    merkleTree,
		MetadataKV:    nil,
		Transport:     nil,
		LocalNodeID:   "node-001",
		PeerSelector:  NewRandomPeerSelector(),
		EventChanSize: 1000,
		TickerDelay:   10 * time.Second,
	}

	gsync := NewEventDrivenGossipSync(config)
	defer gsync.Close()

	// 并发发送事件
	done := make(chan bool, 100)
	for i := 0; i < 100; i++ {
		go func(idx int) {
			gsync.OnWrite("meta:node:", fmt.Sprintf("node-%d", idx))
			done <- true
		}(i)
	}

	// 等待所有 goroutine 完成
	for i := 0; i < 100; i++ {
		<-done
	}

	// 等待事件处理
	time.Sleep(200 * time.Millisecond)

	// 验证事件被接收
	stats := gsync.GetStats()
	require.Equal(t, uint64(100), stats["events_received"])
}

// ==================== Benchmark ====================

// BenchmarkEventDrivenGossipSync_OnWrite 基准测试写入事件
func BenchmarkEventDrivenGossipSync_OnWrite(b *testing.B) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)

	config := &EventDrivenConfig{
		MerkleTree:    merkleTree,
		MetadataKV:    nil,
		Transport:     nil,
		LocalNodeID:   "node-001",
		PeerSelector:  NewRandomPeerSelector(),
		EventChanSize: 10000,
		TickerDelay:   10 * time.Second,
	}

	sync := NewEventDrivenGossipSync(config)
	defer sync.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sync.OnWrite("meta:node:", "node-001")
	}
}

// BenchmarkEventDrivenGossipSync_ConcurrentOnWrite 基准测试并发写入事件
func BenchmarkEventDrivenGossipSync_ConcurrentOnWrite(b *testing.B) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)

	config := &EventDrivenConfig{
		MerkleTree:    merkleTree,
		MetadataKV:    nil,
		Transport:     nil,
		LocalNodeID:   "node-001",
		PeerSelector:  NewRandomPeerSelector(),
		EventChanSize: 10000,
		TickerDelay:   10 * time.Second,
	}

	sync := NewEventDrivenGossipSync(config)
	defer sync.Close()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			sync.OnWrite("meta:node:", "node-001")
		}
	})
}
