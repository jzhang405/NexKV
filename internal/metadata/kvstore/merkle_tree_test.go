package kvstore

import (
	"testing"

	"github.com/jzhang405/NexKV/internal/clock"
)

// TestNewNamespacedMerkleTree 测试创建 NamespacedMerkleTree
func TestNewNamespacedMerkleTree(t *testing.T) {
	hlc := clock.NewHLC()
	nmt := NewNamespacedMerkleTree(hlc)

	if nmt == nil {
		t.Fatal("NewNamespacedMerkleTree returned nil")
	}

	// 验证初始化了 9 个 Namespace
	if len(nmt.namespaces) != 9 {
		t.Errorf("expected 9 namespaces, got %d", len(nmt.namespaces))
	}

	// 验证所有 Namespace 都有初始 Root Hash
	for ns, tree := range nmt.namespaces {
		if tree.RootHash == "" {
			t.Errorf("namespace %s has empty RootHash", ns)
		}
		if tree.KeyHashes == nil {
			t.Errorf("namespace %s has nil KeyHashes", ns)
		}
	}
}

// TestGetGlobalRootHash 测试获取全局 Root Hash
func TestGetGlobalRootHash(t *testing.T) {
	hlc := clock.NewHLC()
	nmt := NewNamespacedMerkleTree(hlc)

	// 初始全局 Root Hash
	globalRoot := nmt.GetGlobalRootHash()
	if globalRoot == "" {
		t.Error("GetGlobalRootHash returned empty string")
	}

	// 多次调用应该返回相同的值（如果没有更新）
	globalRoot2 := nmt.GetGlobalRootHash()
	if globalRoot != globalRoot2 {
		t.Errorf("GetGlobalRootHash returned different values: %s != %s", globalRoot, globalRoot2)
	}
}

// TestGetNamespaceRootHash 测试获取 Namespace Root Hash
func TestGetNamespaceRootHash(t *testing.T) {
	hlc := clock.NewHLC()
	nmt := NewNamespacedMerkleTree(hlc)

	tests := []string{
		NamespaceCluster,
		NamespaceShard,
		NamespaceNode,
		NamespaceRole,
		NamespaceStatic,
		NamespaceTopo,
		NamespaceDynamic,
		NamespaceOp,
		NamespaceVersion,
	}

	for _, ns := range tests {
		rootHash, err := nmt.GetNamespaceRootHash(ns)
		if err != nil {
			t.Errorf("GetNamespaceRootHash(%s) returned error: %v", ns, err)
		}
		if rootHash == "" {
			t.Errorf("GetNamespaceRootHash(%s) returned empty string", ns)
		}
	}

	// 测试不存在的 Namespace
	_, err := nmt.GetNamespaceRootHash("invalid:namespace:")
	if err != ErrNamespaceNotFound {
		t.Errorf("expected ErrNamespaceNotFound, got: %v", err)
	}
}

// TestUpdateKey 测试更新 Key
func TestUpdateKey(t *testing.T) {
	hlc := clock.NewHLC()
	nmt := NewNamespacedMerkleTree(hlc)

	// 初始 Root Hash
	initialRoot, _ := nmt.GetNamespaceRootHash(NamespaceNode)

	// 更新一个 Key
	metadata := map[string]string{
		"node_id":  "node-001",
		"address":  "192.168.1.10:8080",
		"status":   "online",
		"role":     "leaf",
		"parent":   "parent-001",
	}
	err := nmt.UpdateKey(NamespaceNode, "node-001", metadata)
	if err != nil {
		t.Fatalf("UpdateKey failed: %v", err)
	}

	// 验证 Key Hash 存在
	keyHash, err := nmt.GetKeyHash(NamespaceNode, "node-001")
	if err != nil {
		t.Errorf("GetKeyHash failed: %v", err)
	}
	if keyHash == "" {
		t.Error("GetKeyHash returned empty string")
	}

	// 验证 Root Hash 已更新
	newRoot, _ := nmt.GetNamespaceRootHash(NamespaceNode)
	if newRoot == initialRoot {
		t.Error("RootHash should have changed after UpdateKey")
	}

	// 验证版本号已增加
	if nmt.GetVersion() == 0 {
		t.Error("Version should have increased after UpdateKey")
	}
}

// TestGetKeyHash 测试获取 Key Hash
func TestGetKeyHash(t *testing.T) {
	hlc := clock.NewHLC()
	nmt := NewNamespacedMerkleTree(hlc)

	// 添加一个 Key
	metadata := map[string]string{"key": "value"}
	err := nmt.UpdateKey(NamespaceNode, "test-key", metadata)
	if err != nil {
		t.Fatalf("UpdateKey failed: %v", err)
	}

	// 获取存在的 Key
	hash, err := nmt.GetKeyHash(NamespaceNode, "test-key")
	if err != nil {
		t.Errorf("GetKeyHash failed: %v", err)
	}
	if hash == "" {
		t.Error("GetKeyHash returned empty string")
	}

	// 获取不存在的 Key
	_, err = nmt.GetKeyHash(NamespaceNode, "non-existent")
	if err != ErrKeyNotFound {
		t.Errorf("expected ErrKeyNotFound, got: %v", err)
	}

	// 获取不存在的 Namespace 的 Key
	_, err = nmt.GetKeyHash("invalid:ns:", "test-key")
	if err != ErrNamespaceNotFound {
		t.Errorf("expected ErrNamespaceNotFound, got: %v", err)
	}
}

// TestDeleteKey 测试删除 Key
func TestDeleteKey(t *testing.T) {
	hlc := clock.NewHLC()
	nmt := NewNamespacedMerkleTree(hlc)

	// 添加一个 Key
	metadata := map[string]string{"key": "value"}
	err := nmt.UpdateKey(NamespaceNode, "test-key", metadata)
	if err != nil {
		t.Fatalf("UpdateKey failed: %v", err)
	}

	// 验证 Key 存在
	_, err = nmt.GetKeyHash(NamespaceNode, "test-key")
	if err != nil {
		t.Fatalf("GetKeyHash failed: %v", err)
	}

	// 删除 Key
	err = nmt.DeleteKey(NamespaceNode, "test-key")
	if err != nil {
		t.Fatalf("DeleteKey failed: %v", err)
	}

	// 验证 Key 已删除
	_, err = nmt.GetKeyHash(NamespaceNode, "test-key")
	if err != ErrKeyNotFound {
		t.Errorf("expected ErrKeyNotFound after DeleteKey, got: %v", err)
	}
}

// TestGetAllNamespaceRootHashes 测试获取所有 Namespace Root Hash
func TestGetAllNamespaceRootHashes(t *testing.T) {
	hlc := clock.NewHLC()
	nmt := NewNamespacedMerkleTree(hlc)

	allHashes := nmt.GetAllNamespaceRootHashes()

	if len(allHashes) != 9 {
		t.Errorf("expected 9 namespace hashes, got %d", len(allHashes))
	}

	for ns, hash := range allHashes {
		if hash == "" {
			t.Errorf("namespace %s has empty RootHash", ns)
		}
	}
}

// TestUpdateKeyFromBytes 测试从字节数组更新 Key
func TestUpdateKeyFromBytes(t *testing.T) {
	hlc := clock.NewHLC()
	nmt := NewNamespacedMerkleTree(hlc)

	data := []byte(`{"node_id":"node-001","status":"online"}`)
	err := nmt.UpdateKeyFromBytes(NamespaceNode, "node-001", data)
	if err != nil {
		t.Fatalf("UpdateKeyFromBytes failed: %v", err)
	}

	// 验证 Key Hash 存在
	hash, err := nmt.GetKeyHash(NamespaceNode, "node-001")
	if err != nil {
		t.Errorf("GetKeyHash failed: %v", err)
	}
	if hash == "" {
		t.Error("GetKeyHash returned empty string")
	}
}

// TestGetNamespaceVersion 测试获取 Namespace 版本号
func TestGetNamespaceVersion(t *testing.T) {
	hlc := clock.NewHLC()
	nmt := NewNamespacedMerkleTree(hlc)

	// 初始版本号应该是 0
	version, err := nmt.GetNamespaceVersion(NamespaceNode)
	if err != nil {
		t.Errorf("GetNamespaceVersion failed: %v", err)
	}
	if version != 0 {
		t.Errorf("expected initial version 0, got %d", version)
	}

	// 更新 Key 后版本号应该增加
	metadata := map[string]string{"key": "value"}
	nmt.UpdateKey(NamespaceNode, "test-key", metadata)

	version, err = nmt.GetNamespaceVersion(NamespaceNode)
	if err != nil {
		t.Errorf("GetNamespaceVersion failed: %v", err)
	}
	if version != 1 {
		t.Errorf("expected version 1 after update, got %d", version)
	}
}

// TestIncrementEpoch 测试增加 Epoch
func TestIncrementEpoch(t *testing.T) {
	hlc := clock.NewHLC()
	nmt := NewNamespacedMerkleTree(hlc)

	// 初始 Epoch 应该是 0
	if nmt.GetEpoch() != 0 {
		t.Errorf("expected initial epoch 0, got %d", nmt.GetEpoch())
	}

	// 增加 Epoch
	nmt.IncrementEpoch()
	if nmt.GetEpoch() != 1 {
		t.Errorf("expected epoch 1 after IncrementEpoch, got %d", nmt.GetEpoch())
	}

	nmt.IncrementEpoch()
	if nmt.GetEpoch() != 2 {
		t.Errorf("expected epoch 2 after second IncrementEpoch, got %d", nmt.GetEpoch())
	}
}

// TestConcurrentAccess 测试并发访问
func TestConcurrentAccess(t *testing.T) {
	hlc := clock.NewHLC()
	nmt := NewNamespacedMerkleTree(hlc)

	// 并发读写
	done := make(chan bool)

	// 启动多个 goroutine 进行读操作
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				nmt.GetGlobalRootHash()
				nmt.GetAllNamespaceRootHashes()
			}
			done <- true
		}()
	}

	// 启动多个 goroutine 进行写操作
	for i := 0; i < 5; i++ {
		go func(idx int) {
			metadata := map[string]string{"key": "value"}
			for j := 0; j < 50; j++ {
				key := "node-" + string(rune(idx))
				nmt.UpdateKey(NamespaceNode, key, metadata)
			}
			done <- true
		}(i)
	}

	// 等待所有 goroutine 完成
	for i := 0; i < 15; i++ {
		<-done
	}

	// 验证状态一致性
	version := nmt.GetVersion()
	if version == 0 {
		t.Error("Version should have increased after concurrent updates")
	}
}

// BenchmarkGetGlobalRootHash 性能测试：获取全局 Root Hash
func BenchmarkGetGlobalRootHash(b *testing.B) {
	hlc := clock.NewHLC()
	nmt := NewNamespacedMerkleTree(hlc)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		nmt.GetGlobalRootHash()
	}
}

// BenchmarkUpdateKey 性能测试：更新 Key
func BenchmarkUpdateKey(b *testing.B) {
	hlc := clock.NewHLC()
	nmt := NewNamespacedMerkleTree(hlc)
	metadata := map[string]string{
		"node_id": "node-001",
		"address": "192.168.1.10:8080",
		"status":  "online",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := "node-" + string(rune(i%1000))
		nmt.UpdateKey(NamespaceNode, key, metadata)
	}
}

// BenchmarkGetKeyHash 性能测试：获取 Key Hash
func BenchmarkGetKeyHash(b *testing.B) {
	hlc := clock.NewHLC()
	nmt := NewNamespacedMerkleTree(hlc)

	// 预先添加一些 Key
	metadata := map[string]string{"key": "value"}
	for i := 0; i < 1000; i++ {
		key := "node-" + string(rune(i))
		nmt.UpdateKey(NamespaceNode, key, metadata)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := "node-" + string(rune(i%1000))
		nmt.GetKeyHash(NamespaceNode, key)
	}
}
