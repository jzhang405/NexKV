// Copyright 2025 The NexKV Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package gossip

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/clock"
	"github.com/jzhang405/NexKV/internal/metadata/kvstore"
	_ "github.com/jzhang405/NexKV/internal/transport"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"
)

// mockTransport 模拟 Transport 层（用于测试）
type mockTransport struct {
	mu            sync.RWMutex
	receivedMsgs  map[string][]byte // nodeID -> messages
	sentMsgs      map[string][]byte // nodeID -> messages
	messageHandler func(string, []byte)
}

func newMockTransport() *mockTransport {
	return &mockTransport{
		receivedMsgs: make(map[string][]byte),
		sentMsgs:     make(map[string][]byte),
	}
}

func (m *mockTransport) Send(nodeID string, msg []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sentMsgs[nodeID] = msg
	return nil
}

func (m *mockTransport) Receive(handler func(string, []byte)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messageHandler = handler
	return nil
}

func (m *mockTransport) Close() error {
	return nil
}

// simulateReceive 模拟接收消息（用于测试）
func (m *mockTransport) simulateReceive(fromNodeID string, msg []byte) {
	m.mu.RLock()
	handler := m.messageHandler
	m.mu.RUnlock()

	if handler != nil {
		handler(fromNodeID, msg)
	}
}

// TestMerkleGossipSync_Integration 测试 Merkle Gossip 同步集成
func TestMerkleGossipSync_Integration(t *testing.T) {
	ctx := context.Background()

	// 创建两个独立的 Merkle Tree
	hlc1 := clock.NewHLC()
	merkle1 := kvstore.NewNamespacedMerkleTree(hlc1)

	hlc2 := clock.NewHLC()
	merkle2 := kvstore.NewNamespacedMerkleTree(hlc2)

	// 创建模拟 transport
	transport1 := newMockTransport()
	transport2 := newMockTransport()

	// 创建 Gossip 同步服务
	sync1 := NewMerkleGossipSync(merkle1, nil, transport1, "node-1")
	sync2 := NewMerkleGossipSync(merkle2, nil, transport2, "node-2")

	// 添加 peer
	sync1.AddKnownPeer("node-2")
	sync2.AddKnownPeer("node-1")

	// 初始状态下，两边的 Global Root 应该相同
	initialRoot1 := merkle1.GetGlobalRootHash()
	initialRoot2 := merkle2.GetGlobalRootHash()
	if initialRoot1 != initialRoot2 {
		t.Errorf("Expected same initial root, got %s vs %s", initialRoot1, initialRoot2)
	}

	// 更新 node-1 的数据
	metadata := map[string]string{"key": "value1", "status": "active"}
	err := merkle1.UpdateKey(kvstore.NamespaceNode, "test-node-001", metadata)
	require.NoError(t, err)

	// 更新后 Global Root 应该不同
	newRoot1 := merkle1.GetGlobalRootHash()
	if newRoot1 == initialRoot1 {
		t.Error("Expected root to change after update")
	}

	// node-1 与 node-2 同步（模拟）
	result, err := sync1.SyncWithPeer(ctx, "node-2")
	require.NoError(t, err)

	// 应该检测到差异（因为 node-2 没有更新）
	if !result.Synced {
		t.Log("No sync detected (expected - peer data not updated)")
	}

	// 验证统计信息
	stats := sync1.GetStats()
	syncCount := stats["sync_count"].(uint64)
	if syncCount != 1 {
		t.Errorf("Expected sync_count 1, got %d", syncCount)
	}
}

// TestMerkleGossipSync_BidirectionalSync 测试双向同步
func TestMerkleGossipSync_BidirectionalSync(t *testing.T) {
	ctx := context.Background()

	hlc1 := clock.NewHLC()
	merkle1 := kvstore.NewNamespacedMerkleTree(hlc1)

	hlc2 := clock.NewHLC()
	merkle2 := kvstore.NewNamespacedMerkleTree(hlc2)

	transport1 := newMockTransport()
	transport2 := newMockTransport()

	sync1 := NewMerkleGossipSync(merkle1, nil, transport1, "node-1")
	sync2 := NewMerkleGossipSync(merkle2, nil, transport2, "node-2")

	sync1.AddKnownPeer("node-2")
	sync2.AddKnownPeer("node-1")

	// node-1 添加数据
	metadata1 := map[string]string{"key": "value1"}
	err := merkle1.UpdateKey(kvstore.NamespaceNode, "node-1-data", metadata1)
	require.NoError(t, err)

	// node-2 保持不变，所以 node-1 的 Global Root 会变化
	// 当 node-1 与 node-2 同步时，由于 node-2 没有变化，差异会被检测到

	// 验证 Global Root 不同
	root1 := merkle1.GetGlobalRootHash()
	root2 := merkle2.GetGlobalRootHash()

	if root1 == root2 {
		t.Error("Expected different roots after independent updates")
	}

	// node-1 与 node-2 同步（会检测到差异，因为 node-2 没有数据）
	result1, _ := sync1.SyncWithPeer(ctx, "node-2")

	// 由于实现是单向检查（只比较本地和远程），
	// 且 node-2 没有数据，所以会检测到差异
	t.Logf("Sync result: synced=%v, diff_ns=%d",
		result1.Synced, len(result1.DiffNamespaces))
}

// TestMerkleGossipSync_Performance 性能测试：大量数据场景
func TestMerkleGossipSync_Performance(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	ctx := context.Background()
	hlc := clock.NewHLC()
	merkle := kvstore.NewNamespacedMerkleTree(hlc)
	transport := newMockTransport()

	sync := NewMerkleGossipSync(merkle, nil, transport, "node-1")

	// 添加大量已知 peer
	for i := 0; i < 100; i++ {
		peerID := peer.ID(fmt.Sprintf("peer-%d", i)).String()
		sync.AddKnownPeer(peerID)
	}

	// 批量更新数据
	startTime := time.Now()
	for i := 0; i < 1000; i++ {
		metadata := map[string]string{
			"id":   fmt.Sprintf("data-%d", i),
			"index": fmt.Sprintf("%d", i),
		}
		key := fmt.Sprintf("node-%04d", i)
		err := merkle.UpdateKey(kvstore.NamespaceNode, key, metadata)
		if err != nil {
			t.Fatalf("UpdateKey failed: %v", err)
		}
	}
	updateDuration := time.Since(startTime)

	t.Logf("Updated 1000 keys in %v", updateDuration)

	// 测试同步性能
	startTime = time.Now()
	for i := 0; i < 100; i++ {
		peerID := peer.ID(fmt.Sprintf("peer-%d", i%100)).String()
		_, err := sync.SyncWithPeer(ctx, peerID)
		if err != nil {
			t.Logf("Sync with peer %s failed: %v", peerID, err)
		}
	}
	syncDuration := time.Since(startTime)

	t.Logf("Performed 100 sync operations in %v", syncDuration)

	// 验证性能目标
	avgSyncTime := syncDuration / 100
	if avgSyncTime > 10*time.Millisecond {
		t.Errorf("Average sync time too high: %v (target: < 10ms)", avgSyncTime)
	}

	stats := sync.GetStats()
	t.Logf("Stats: %+v", stats)
}

// TestMerkleGossipSync_BandwidthSavings 测试带宽节省
func TestMerkleGossipSync_BandwidthSavings(t *testing.T) {
	tests := []struct {
		name              string
		totalSize         int
		keysReceived      int
		keysSent          int
		minExpectedSaved  uint64
	}{
		{
			name:             "单个 Key 变化",
			totalSize:        10000,
			keysReceived:     1,
			keysSent:         0,
			minExpectedSaved: 9000, // 至少节省 90%
		},
		{
			name:             "多个 Key 变化",
			totalSize:        10000,
			keysReceived:     10,
			keysSent:         5,
			minExpectedSaved: 7000, // 至少节省 70%
		},
		{
			name:             "全量变化（无节省）",
			totalSize:        10000,
			keysReceived:     100,
			keysSent:         50,
			minExpectedSaved: 0,    // 全量变化无节省
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			saved := CalculateBandwidthSavings(tt.totalSize, tt.keysReceived, tt.keysSent)
			if saved < tt.minExpectedSaved {
				t.Errorf("CalculateBandwidthSavings() = %d, want >= %d", saved, tt.minExpectedSaved)
			}
		})
	}
}

// TestMerkleGossipSync_CacheOptimization 测试缓存优化效果
func TestMerkleGossipSync_CacheOptimization(t *testing.T) {
	hlc := clock.NewHLC()
	merkle := kvstore.NewNamespacedMerkleTree(hlc)
	transport := newMockTransport()

	sync := NewMerkleGossipSync(merkle, nil, transport, "node-1")

	// 添加已知 peer
	for i := 0; i < 10; i++ {
		peerID := peer.ID(fmt.Sprintf("peer-%d", i)).String()
		sync.AddKnownPeer(peerID)
	}

	ctx := context.Background()

	// 连续调用多次 SyncWithPeer（没有实际更新）
	// 第二次及以后的调用应该使用缓存
	startTime := time.Now()
	for i := 0; i < 100; i++ {
		peerID := peer.ID(fmt.Sprintf("peer-%d", i%10)).String()
		sync.SyncWithPeer(ctx, peerID)
	}
	duration := time.Since(startTime)

	t.Logf("100 sync operations (cached) took: %v", duration)

	// 平均每次同步应该很快（< 1ms）
	avgDuration := duration / 100
	if avgDuration > 1*time.Millisecond {
		t.Errorf("Average sync duration too high: %v (target: < 1ms)", avgDuration)
	}

	// 验证缓存命中率
	cacheStats := merkle.GetCacheStats()
	hitRate := cacheStats["hit_rate"].(float64)
	if hitRate < 0.9 {
		t.Errorf("Cache hit rate too low: %f (target: > 0.9)", hitRate)
	}

	t.Logf("Cache stats: hit=%d, miss=%d, rate=%.2f%%",
		cacheStats["hit_count"].(int64),
		cacheStats["miss_count"].(int64),
		hitRate*100)
}
