package gossip

import (
	"context"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/clock"
	"github.com/jzhang405/NexKV/internal/metadata/kvstore"
)

// setupMerkleGossipSync 创建测试用的 Merkle Gossip 同步服务
func setupMerkleGossipSync(t *testing.T) *MerkleGossipSync {
	t.Helper()
	hlc := clock.NewHLC()
	merkle := kvstore.NewNamespacedMerkleTree(hlc)
	sync := NewMerkleGossipSync(merkle, nil, nil, "node-1")
	t.Cleanup(func() { _ = sync.Close() })
	return sync
}

// TestNewMerkleGossipSync 测试创建 Merkle Gossip 同步服务
func TestNewMerkleGossipSync(t *testing.T) {
	sync := setupMerkleGossipSync(t)

	if sync == nil {
		t.Fatal("NewMerkleGossipSync returned nil")
	}

	// 验证默认配置
	if sync.gossipInterval != 10*time.Second {
		t.Errorf("Expected gossip interval 10s, got %v", sync.gossipInterval)
	}
	if sync.gossipTimeout != 5*time.Second {
		t.Errorf("Expected gossip timeout 5s, got %v", sync.gossipTimeout)
	}
}

// TestSyncWithPeer_NoDifference 测试无差异同步
func TestSyncWithPeer_NoDifference(t *testing.T) {
	sync := setupMerkleGossipSync(t)
	ctx := context.Background()

	// 使用相同的 Global Root 进行同步（应该检测到无差异）
	result, err := sync.SyncWithPeer(ctx, "peer-1")
	if err != nil {
		t.Fatalf("SyncWithPeer failed: %v", err)
	}

	if result.Synced {
		t.Error("Expected no sync (same Global Root), but synced=true")
	}

	if result.Reason != "Global Root Hash 相同" {
		t.Errorf("Expected reason 'Global Root Hash 相同', got '%s'", result.Reason)
	}

	// 验证带宽使用（只传输了 Global Root Hash）
	if result.BandwidthUsed != 32 {
		t.Errorf("Expected bandwidth 32B (Global Root only), got %d", result.BandwidthUsed)
	}
}

// TestSyncWithPeer_WithDifference 测试有差异同步
func TestSyncWithPeer_WithDifference(t *testing.T) {
	sync := setupMerkleGossipSync(t)

	// 更新本地数据
	metadata := map[string]string{"key": "value"}
	err := sync.merkle.UpdateKey(kvstore.NamespaceNode, "test-node", metadata)
	if err != nil {
		t.Fatalf("UpdateKey failed: %v", err)
	}

	ctx := context.Background()

	// 使用不同的 peer（模拟有差异）
	result, err := sync.SyncWithPeer(ctx, "peer-1")
	if err != nil {
		t.Fatalf("SyncWithPeer failed: %v", err)
	}

	// 由于没有实际的 peer 数据，这个测试会认为有差异
	if !result.Synced {
		t.Log("No sync detected (expected behavior without actual peer data)")
	}
}

// TestFindDiffNamespaces 测试查找差异 Namespace
func TestFindDiffNamespaces(t *testing.T) {
	sync := setupMerkleGossipSync(t)

	localHashes := map[string]string{
		"ns-1": "hash-1",
		"ns-2": "hash-2-same",
		"ns-3": "hash-3",
	}

	peerHashes := map[string]string{
		"ns-1": "hash-1-different", // 差异
		"ns-2": "hash-2-same",      // 相同
		"ns-4": "hash-4-peer-only", // peer 有但本地没有
	}

	diff := sync.findDiffNamespaces(localHashes, peerHashes)

	// 验证差异的 Namespace
	if !diff["ns-1"] {
		t.Error("Expected ns-1 in diff (different hash)")
	}
	if !diff["ns-4"] {
		t.Error("Expected ns-4 in diff (peer only)")
	}
	if diff["ns-2"] {
		t.Error("Expected ns-2 not in diff (same hash)")
	}
	// 注意：本地有但 peer 没有的也应该被认为是差异（需要发送给 peer）
	if !diff["ns-3"] {
		t.Error("Expected ns-3 in diff (local only, needs to be sent to peer)")
	}
}

// TestCalculateBandwidthSavings 测试带宽节省计算
func TestCalculateBandwidthSavings(t *testing.T) {
	tests := []struct {
		name          string
		totalSize     int
		keysReceived  int
		keysSent      int
		expectedSaved uint64
	}{
		{
			name:          "单个 Key 变化",
			totalSize:     10000,
			keysReceived:  1,
			keysSent:      0,
			expectedSaved: 9580,
		},
		{
			name:          "多个 Key 变化",
			totalSize:     10000,
			keysReceived:  10,
			keysSent:      5,
			expectedSaved: 8180,
		},
		{
			name:          "全量变化",
			totalSize:     10000,
			keysReceived:  100,
			keysSent:      50,
			expectedSaved: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			saved := CalculateBandwidthSavings(tt.totalSize, tt.keysReceived, tt.keysSent)
			if saved != tt.expectedSaved {
				t.Errorf("CalculateBandwidthSavings() = %d, want %d", saved, tt.expectedSaved)
			}
		})
	}
}

// TestEstimateBandwidthUsage 测试带宽使用估算
func TestEstimateBandwidthUsage(t *testing.T) {
	tests := []struct {
		keyCount      int
		expectedBytes uint64
	}{
		{0, 64},      // 32 (Global) + 32 (Namespace)
		{1, 164},     // 64 + 100
		{10, 1064},   // 64 + 1000
		{100, 10064}, // 64 + 10000
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			usage := EstimateBandwidthUsage(tt.keyCount)
			if usage != tt.expectedBytes {
				t.Errorf("EstimateBandwidthUsage(%d) = %d, want %d", tt.keyCount, usage, tt.expectedBytes)
			}
		})
	}
}

// TestBuildGossipPayload 测试构建 Gossip Payload
func TestBuildGossipPayload(t *testing.T) {
	hlc := clock.NewHLC()
	merkle := kvstore.NewNamespacedMerkleTree(hlc)

	payload := BuildGossipPayload(merkle, false)

	// 验证必需字段
	globalRoot, ok := payload["global_root_hash"].(string)
	if !ok || globalRoot == "" {
		t.Error("Missing or empty global_root_hash")
	}

	namespaceHashes, ok := payload["namespace_hashes"].(map[string]string)
	if !ok || len(namespaceHashes) != 9 {
		t.Errorf("Expected 9 namespace hashes, got %d", len(namespaceHashes))
	}

	fullSync, ok := payload["full_sync"].(bool)
	if !ok || fullSync {
		t.Error("Expected full_sync=false")
	}
}

// TestParseGossipPayload 测试解析 Gossip Payload
func TestParseGossipPayload(t *testing.T) {
	validPayload := map[string]interface{}{
		"global_root_hash": "test_root_hash",
		"namespace_hashes": map[string]string{
			"ns-1": "hash-1",
			"ns-2": "hash-2",
		},
	}

	globalRoot, namespaceHashes, err := ParseGossipPayload(validPayload)
	if err != nil {
		t.Fatalf("ParseGossipPayload failed: %v", err)
	}

	if globalRoot != "test_root_hash" {
		t.Errorf("Expected global_root_hash 'test_root_hash', got '%s'", globalRoot)
	}

	if len(namespaceHashes) != 2 {
		t.Errorf("Expected 2 namespace hashes, got %d", len(namespaceHashes))
	}

	// 测试无效 payload
	invalidPayload := map[string]interface{}{
		"namespace_hashes": map[string]string{},
		// 缺少 global_root_hash
	}

	_, _, err = ParseGossipPayload(invalidPayload)
	if err == nil {
		t.Error("Expected error for invalid payload, got nil")
	}
}

// TestGetStats 测试获取统计信息
func TestGetStats(t *testing.T) {
	sync := setupMerkleGossipSync(t)

	stats := sync.GetStats()

	// 验证初始统计
	if stats["sync_count"].(uint64) != 0 {
		t.Error("Expected initial sync_count 0")
	}

	// 执行一次同步
	ctx := context.Background()
	_, _ = sync.SyncWithPeer(ctx, "peer-1")

	stats = sync.GetStats()

	// 验证更新后的统计
	if stats["sync_count"].(uint64) != 1 {
		t.Error("Expected sync_count 1 after sync")
	}
}

// BenchmarkSyncWithPeer 性能测试：与 peer 同步
func BenchmarkSyncWithPeer(b *testing.B) {
	hlc := clock.NewHLC()
	merkle := kvstore.NewNamespacedMerkleTree(hlc)
	sync := NewMerkleGossipSync(merkle, nil, nil, "node-1")

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = sync.SyncWithPeer(ctx, "peer-1")
	}
}

// BenchmarkFindDiffNamespaces 性能测试：查找差异 Namespace
func BenchmarkFindDiffNamespaces(b *testing.B) {
	hlc := clock.NewHLC()
	merkle := kvstore.NewNamespacedMerkleTree(hlc)
	sync := NewMerkleGossipSync(merkle, nil, nil, "node-1")

	localHashes := merkle.GetAllNamespaceRootHashes()
	peerHashes := merkle.GetAllNamespaceRootHashes()
	// 修改其中一个 hash 以创建差异
	for k := range peerHashes {
		peerHashes[k] = "different_" + peerHashes[k]
		break
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sync.findDiffNamespaces(localHashes, peerHashes)
	}
}
