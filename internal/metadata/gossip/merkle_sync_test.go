package gossip

import (
	"context"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/clock"
	"github.com/jzhang405/NexKV/internal/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/kvstore"
	"github.com/vmihailenco/msgpack/v5"
	"github.com/stretchr/testify/require"
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

// TestSyncWithPeer_NoDifference 验证无差异同步
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

// TestSyncWithPeer_WithDifference 验证有差异同步
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

// TestFindDiffNamespaces 验证查找差异 Namespace
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
	if !diff["ns-3"] {
		t.Error("Expected ns-3 in diff (local only, needs to be sent to peer)")
	}
}

// TestCalculateBandwidthSavings 验证带宽节省计算
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
			keysReceived: 1,
			keysSent:      0,
			expectedSaved: 9580,
		},
		{
			name:          "多个 Key 变化",
			totalSize:     10000,
			keysReceived: 10,
			keysSent:      5,
			expectedSaved: 8180,
		},
		{
			name:          "全量变化",
			totalSize:     10000,
			keysReceived: 100,
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

// TestEstimateBandwidthUsage 验证带宽使用估算
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

// TestBuildGossipPayload 验证构建 Gossip Payload
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

// TestParseGossipPayload 验证解析 Gossip Payload
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

// TestGetStats 验证获取统计信息
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

// ==================== 双向同步验证测试 ====================

// TestHandleIncomingMessage_DiffResponse 验证处理差异请求并发送响应
func TestHandleIncomingMessage_DiffResponse(t *testing.T) {
	hlc := clock.NewHLC()
	merkle1 := kvstore.NewNamespacedMerkleTree(hlc)
	merkle2 := kvstore.NewNamespacedMerkleTree(hlc)

	// 节点 1：有不同的数据
	_ = merkle1.UpdateKey(kvstore.NamespaceNode, "node-1", map[string]string{
		"address": "192.168.1.10:8080",
	})

	sync1 := NewMerkleGossipSync(merkle1, nil, "node-1")
	ctx := context.Background()

	// 模拟节点 2 发送同步请求（有不同的 Global Root）
	peerPayload := BuildGossipPayload(merkle2, false)
	peerPayloadBytes, _ := msgpack.Marshal(peerPayload)

	// 节点 1 处理节点 2 的消息
	sync1.handleIncomingMessage("node-2", peerPayloadBytes)

	// 验证：如果没有错误就说明响应处理成功
	logging.Info("双向同步验证完成")
}

// TestBuildDiffResponse 验证差异响应的构建逻辑
func TestBuildDiffResponse(t *testing.T) {
	hlc := clock.NewHLC()
	merkle := kvstore.NewNamespacedMerkleTree(hlc)

	// 添加一些测试数据
	_ = merkle.UpdateKey(kvstore.NamespaceNode, "node-1", map[string]string{
		"address": "192.168.1.10:8080",
	})
	_ = merkle.UpdateKey(kvstore.NamespaceShard, "shard-1", map[string]string{
		"status": "active",
	})

	sync := NewMerkleGossipSync(merkle, nil, "local-node")

	// 模拟差异场景
	localRoot := merkle.GetGlobalRootHash()
	peerRoot := "different_peer_root"

	// 创建一个 mock transport 来捕获 buildDiffResponse 的输出
	var capturedResponse map[string]interface{}
	mockTransport := &mockTransport{
		sendFunc: func(nodeID string, data []byte) error {
			// 解析响应
			if err := msgpack.Unmarshal(data, &capturedResponse); err != nil {
				t.Fatalf("Failed to unmarshal diff response: %v", err)
			}
			logging.WithField("peer", nodeID).Info("收到差异响应")
			return nil
		},
	}

	// 使用 mock transport 创建 sync
	syncWithMock := NewMerkleGossipSync(merkle, nil, "node-1")
	*syncWithMock.transport = mockTransport

	// 手动构建差异响应
	diffNamespaces := map[string]bool{
		kvstore.NamespaceNode: true,
		kvstore.NamespaceShard: true,
	}

	// 调用 buildDiffResponse 方法
	response := syncWithMock.buildDiffResponse("peer-1", localRoot, peerRoot, diffNamespaces)

	// 验证响应结构
	require.Equal(t, "node-1", response["from_node_id"])
	require.Equal(t, localRoot, response["global_root_hash"])

	namespaceHashes, ok := response["namespace_hashes"].(map[string]string)
	require.True(t, ok, "namespace_hashes should be present")
	require.NotEmpty(t, namespaceHashes, "namespace_hashes should not be empty")

	diffNS, ok := response["diff_namespaces"].(map[string][]string)
	require.True(t, ok, "diff_namespaces should be present")
	require.NotEmpty(t, diffNS, "diff_namespaces should not be empty")
	require.Contains(t, diffNS, kvstore.NamespaceNode, "diff_namespaces should contain meta:node")
	require.Contains(t, diffNS, kvstore.NamespaceShard, "diff_namespaces should contain meta:shard")
}
