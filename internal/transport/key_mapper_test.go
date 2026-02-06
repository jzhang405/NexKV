package transport

import (
	"sync"
	"testing"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNodeIDMapper_RegisterAndQuery 测试注册与查询
func TestNodeIDMapper_RegisterAndQuery(t *testing.T) {
	// Given: 创建映射器
	mapper := NewNodeIDMapper()

	// Given: 生成密钥对并计算 PeerID
	privKey, _, err := crypto.GenerateEd25519Key(nil)
	require.NoError(t, err)
	peerID, _ := peer.IDFromPrivateKey(privKey)

	// When: 注册映射
	nodeID := "node-1"
	mapper.Register(nodeID, peerID)

	// Then: 应该能够查询到
	retrievedPeerID, ok := mapper.GetPeerID(nodeID)
	require.True(t, ok)
	assert.Equal(t, peerID, retrievedPeerID)

	// And: 反向查询也应该成功
	retrievedNodeID, ok := mapper.GetNodeID(peerID)
	require.True(t, ok)
	assert.Equal(t, nodeID, retrievedNodeID)
}

// TestNodeIDMapper_ConcurrentAccess 测试并发访问
func TestNodeIDMapper_ConcurrentAccess(t *testing.T) {
	// Given: 创建映射器
	mapper := NewNodeIDMapper()

	// Given: 生成多个 PeerID
	peerIDs := make([]peer.ID, 10)
	for i := 0; i < 10; i++ {
		privKey, _, err := crypto.GenerateEd25519Key(nil)
		require.NoError(t, err)
		peerIDs[i], _ = peer.IDFromPrivateKey(privKey)
	}

	// When: 并发注册
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			nodeID := string(rune('a' + idx))
			mapper.Register(nodeID, peerIDs[idx])
		}(i)
	}
	wg.Wait()

	// Then: 所有映射都应该存在
	for i := 0; i < 10; i++ {
		nodeID := string(rune('a' + i))
		_, ok := mapper.GetPeerID(nodeID)
		assert.True(t, ok, "node_id %s should exist", nodeID)
	}
}

// TestNodeIDMapper_BidirectionalMapping 测试双向映射
func TestNodeIDMapper_BidirectionalMapping(t *testing.T) {
	// Given: 创建映射器
	mapper := NewNodeIDMapper()

	// Given: 生成密钥对
	privKey, _, err := crypto.GenerateEd25519Key(nil)
	require.NoError(t, err)
	peerID, _ := peer.IDFromPrivateKey(privKey)

	// When: 注册映射
	nodeID := "node-test"
	mapper.Register(nodeID, peerID)

	// Then: 正向查询（node_id -> peer_id）
	retrievedPeerID, ok := mapper.GetPeerID(nodeID)
	require.True(t, ok)
	assert.Equal(t, peerID, retrievedPeerID)

	// Then: 反向查询（peer_id -> node_id）
	retrievedNodeID, ok := mapper.GetNodeID(peerID)
	require.True(t, ok)
	assert.Equal(t, nodeID, retrievedNodeID)

	// And: 查询不存在的映射应该失败
	_, ok = mapper.GetPeerID("non-existent")
	assert.False(t, ok)

	emptyPeerID := peer.ID("")
	_, ok = mapper.GetNodeID(emptyPeerID)
	assert.False(t, ok)
}
