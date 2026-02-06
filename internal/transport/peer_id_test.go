package transport

import (
	"testing"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPeerID_GenerateFromPublicKey 测试从公钥生成 PeerID
func TestPeerID_GenerateFromPublicKey(t *testing.T) {
	// Given: 生成密钥对
	privKey, _, err := crypto.GenerateEd25519Key(nil)
	require.NoError(t, err)
	pubKey := privKey.GetPublic()

	// When: 从公钥生成 PeerID
	peerID, err := GeneratePeerID(pubKey)

	// Then: 应成功生成
	require.NoError(t, err)
	assert.NotEmpty(t, peerID)
	// peer.ID 不能为空字符串
	assert.NotEqual(t, peer.ID(""), peerID)

	// And: PeerID 应该可以从私钥直接生成
	peerIDFromPriv, err := peer.IDFromPrivateKey(privKey)
	require.NoError(t, err)
	assert.Equal(t, peerID, peerIDFromPriv)
}

// TestPeerID_StringRepresentation 测试 PeerID 字符串表示
func TestPeerID_StringRepresentation(t *testing.T) {
	// Given: 生成密钥对
	privKey, _, err := crypto.GenerateEd25519Key(nil)
	require.NoError(t, err)
	peerID, _ := peer.IDFromPrivateKey(privKey)

	// When: 获取 PeerID 字符串表示
	peerIDStr := PeerIDString(peerID)

	// Then: 应该非空
	assert.NotEmpty(t, peerIDStr)

	// And: 应该能够解析回 PeerID
	parsedPeerID, err := peer.Decode(peerIDStr)
	require.NoError(t, err)
	assert.Equal(t, peerID, parsedPeerID)
}

// TestPeerID_Validation 测试 PeerID 验证
func TestPeerID_Validation(t *testing.T) {
	// Given: 生成密钥对
	privKey, _, err := crypto.GenerateEd25519Key(nil)
	require.NoError(t, err)
	peerID, _ := peer.IDFromPrivateKey(privKey)

	// When: 验证 PeerID
	// Then: 不应该是空的
	assert.NotEqual(t, peer.ID(""), peerID)

	// And: 应该能够获取字符串表示
	peerIDStr := peerID.String()
	assert.NotEmpty(t, peerIDStr)

	// And: PeerIDShort() 应该返回短格式
	peerIDShort := PeerIDShort(peerID)
	assert.NotEmpty(t, peerIDShort)
	assert.Less(t, len(peerIDShort), len(peerIDStr))
}
