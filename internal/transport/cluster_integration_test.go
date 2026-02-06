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

package transport

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClusterIntegration 测试 Cluster 层集成
func TestClusterIntegration(t *testing.T) {
	ctx := context.Background()

	// 创建 3 个 P2PService 模拟集群节点
	tmpDir := t.TempDir()
	nodes := make([]*P2PService, 3)

	for i := 0; i < 3; i++ {
		keyPath := filepath.Join(tmpDir, "node"+string(rune('1'+i))+".key")
		cfg := DefaultP2PServiceConfig("931"+string(rune('0'+i)), keyPath)

		service, err := NewP2PService(cfg)
		require.NoError(t, err)
		defer service.Close()

		err = service.Start(ctx)
		require.NoError(t, err)

		nodes[i] = service
	}

	// 验证节点启动
	for i, node := range nodes {
		assert.True(t, node.IsStarted(), "节点 %d 应该已启动", i)
		assert.NotEmpty(t, node.PeerID().String())
	}

	// 连接节点：node0 -> node1, node0 -> node2
	peerInfo1 := nodes[1].GetPeerInfo()
	err := nodes[0].ConnectToPeer(ctx, peerInfo1)
	require.NoError(t, err)

	peerInfo2 := nodes[2].GetPeerInfo()
	err = nodes[0].ConnectToPeer(ctx, peerInfo2)
	require.NoError(t, err)

	// 等待连接建立
	time.Sleep(200 * time.Millisecond)

	// 验证连接
	peers := nodes[0].Host().Network().Peers()
	assert.Contains(t, peers, nodes[1].PeerID())
	assert.Contains(t, peers, nodes[2].PeerID())

	// 验证 Gossip 消息
	t.Run("GossipMessage", func(t *testing.T) {
		// 注册 Gossip 消息处理器
		received := make(chan *Message, 10)

		for i := 1; i < 3; i++ {
			nodes[i].Protocol().RegisterHandler(MessageTypeGossip, MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
				received <- msg
				return nil
			}))
		}

		// 发送 Gossip 消息
		gossipPayload := &GossipPayload{
			Digest: map[string]uint64{
				"key1": 1,
				"key2": 2,
			},
			VersionDelta: 2,
			FullSync:     false,
		}

		msg := &Message{
			From: nodes[0].PeerID().String(),
		}
		msg.MustEncodePayload(gossipPayload)

		err = nodes[0].Protocol().BroadcastMessage(ctx, []peer.ID{nodes[1].PeerID(), nodes[2].PeerID()}, msg)
		require.NoError(t, err)

		// 等待接收（使用超时避免死锁）
		receivedCount := 0
		timeout := time.After(5 * time.Second)

		for {
			select {
			case <-received:
				receivedCount++
				if receivedCount >= 2 {
					return // 收到足够消息，退出子测试
				}
			case <-timeout:
				assert.GreaterOrEqual(t, receivedCount, 1, "应该至少收到 1 个 Gossip 响应")
				return
			}
		}
	})

	// 验证 Quorum 投票
	t.Run("QuorumVoting", func(t *testing.T) {
		quorumThreshold := 2 // 需要至少 2 个节点确认

		// 注册 Quorum 消息处理器
		votes := make(chan *Message, 10)

		for i := 1; i < 3; i++ {
			nodes[i].Protocol().RegisterHandler(MessageTypeQuorum, MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
				// 发送投票响应
				response := &Message{
					Type: MessageTypeAck,
				}
				votePayload := &QuorumPayload{
					Phase:      "vote",
					ProposalID: "prop-001",
					Key:        "test-shard",
					Voter:      nodes[i].PeerID().String(),
					Decision:   true,
				}
				response.MustEncodePayload(votePayload)
				_ = nodes[i].Protocol().SendMessage(ctx, from, response)

				// 通知投票已发送
				votes <- response
				return nil
			}))
		}

		// 发起 Quorum 提案
		proposePayload := &QuorumPayload{
			Phase:      "propose",
			ProposalID: "prop-001",
			Key:        "test-shard",
			Value:      []byte("shard-metadata"),
		}

		msg := &Message{
			From: nodes[0].PeerID().String(),
		}
		msg.MustEncodePayload(proposePayload)

		err = nodes[0].Protocol().BroadcastMessage(ctx, []peer.ID{nodes[1].PeerID(), nodes[2].PeerID()}, msg)
		require.NoError(t, err)

		// 等待投票（使用超时）
		voteCount := 0
		timeout := time.After(5 * time.Second)

		for {
			select {
			case <-votes:
				voteCount++
				if voteCount >= quorumThreshold-1 {
					return // 收到足够投票
				}
			case <-timeout:
				assert.GreaterOrEqual(t, voteCount, quorumThreshold-1, "应该收到至少 %d 个投票", quorumThreshold-1)
				return
			}
		}
	})
}

// TestClusterNodeJoinLeave 测试节点加入和离开
func TestClusterNodeJoinLeave(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	// 创建初始节点
	keyPath1 := filepath.Join(tmpDir, "node1.key")
	cfg1 := DefaultP2PServiceConfig("9321", keyPath1)
	node1, err := NewP2PService(cfg1)
	require.NoError(t, err)
	defer node1.Close()
	err = node1.Start(ctx)
	require.NoError(t, err)

	// 新节点加入
	keyPath2 := filepath.Join(tmpDir, "node2.key")
	cfg2 := DefaultP2PServiceConfig("9322", keyPath2)
	node2, err := NewP2PService(cfg2)
	require.NoError(t, err)

	err = node2.Start(ctx)
	require.NoError(t, err)

	// 新节点连接到集群
	peerInfo1 := node1.GetPeerInfo()
	err = node2.ConnectToPeer(ctx, peerInfo1)
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	// 验证节点相互发现
	peers1 := node1.Host().Network().Peers()
	peers2 := node2.Host().Network().Peers()

	assert.Contains(t, peers1, node2.PeerID())
	assert.Contains(t, peers2, node1.PeerID())

	// 节点离开
	node2.Close()

	// 等待连接清理
	time.Sleep(200 * time.Millisecond)

	// 验证连接已断开
	peers1 = node1.Host().Network().Peers()
	assert.NotContains(t, peers1, node2.PeerID())
}

// TestClusterConcurrentOperations 测试集群并发操作
func TestClusterConcurrentOperations(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	// 创建 2 节点
	keyPath1 := filepath.Join(tmpDir, "node1.key")
	cfg1 := DefaultP2PServiceConfig("9331", keyPath1)
	node1, err := NewP2PService(cfg1)
	require.NoError(t, err)
	defer node1.Close()

	keyPath2 := filepath.Join(tmpDir, "node2.key")
	cfg2 := DefaultP2PServiceConfig("9332", keyPath2)
	node2, err := NewP2PService(cfg2)
	require.NoError(t, err)
	defer node2.Close()

	err = node1.Start(ctx)
	require.NoError(t, err)
	err = node2.Start(ctx)
	require.NoError(t, err)

	// 建立连接
	peerInfo2 := node2.GetPeerInfo()
	err = node1.ConnectToPeer(ctx, peerInfo2)
	require.NoError(t, err)

	// 注册处理器（使用 atomic 保护 receivedCount 避免竞态条件）
	var receivedCount int64
	node2.Protocol().RegisterHandler(MessageTypePut, MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
		atomic.AddInt64(&receivedCount, 1)
		return nil
	}))

	// 发送多条消息
	const messageCount = 10
	for i := 0; i < messageCount; i++ {
		putPayload := &PutPayload{
			Key:     []byte("key"),
			Value:   []byte("value"),
			Version: uint64(i),
		}

		msg := &Message{}
		msg.MustEncodePayload(putPayload)

		err = node1.Protocol().SendMessage(ctx, node2.PeerID(), msg)
		require.NoError(t, err)
	}

	// 等待处理完成
	time.Sleep(500 * time.Millisecond)

	// 验证至少收到部分消息（使用 atomic.LoadInt64 避免竞态条件）
	finalCount := atomic.LoadInt64(&receivedCount)
	t.Logf("发送 %d 条消息，收到 %d 条响应", messageCount, finalCount)
	assert.Greater(t, finalCount, int64(0), "应该至少收到部分消息")
}

// TestClusterMessageWithPayload 测试使用结构化 Payload 的集群消息
func TestClusterMessageWithPayload(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	// 创建 2 个节点
	keyPath1 := filepath.Join(tmpDir, "node1.key")
	cfg1 := DefaultP2PServiceConfig("9341", keyPath1)
	node1, err := NewP2PService(cfg1)
	require.NoError(t, err)
	defer node1.Close()

	keyPath2 := filepath.Join(tmpDir, "node2.key")
	cfg2 := DefaultP2PServiceConfig("9342", keyPath2)
	node2, err := NewP2PService(cfg2)
	require.NoError(t, err)
	defer node2.Close()

	err = node1.Start(ctx)
	require.NoError(t, err)
	err = node2.Start(ctx)
	require.NoError(t, err)

	// 连接节点
	peerInfo2 := node2.GetPeerInfo()
	err = node1.ConnectToPeer(ctx, peerInfo2)
	require.NoError(t, err)

	// 注册处理器
	received := make(chan *PutPayload, 1)
	node2.Protocol().RegisterHandler(MessageTypePut, MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
		payload, err := msg.DecodePayload()
		require.NoError(t, err)
		putPayload, ok := payload.(*PutPayload)
		require.True(t, ok)
		received <- putPayload
		return nil
	}))

	// 发送带结构化 Payload 的消息
	putPayload := &PutPayload{
		Key:     []byte("cluster-key"),
		Value:   []byte("cluster-value"),
		Version: 100,
		Sync:    true,
	}

	msg := &Message{}
	msg.MustEncodePayload(putPayload)

	err = node1.Protocol().SendMessage(ctx, node2.PeerID(), msg)
	require.NoError(t, err)

	// 验证接收
	select {
	case recvPayload := <-received:
		assert.Equal(t, []byte("cluster-key"), recvPayload.Key)
		assert.Equal(t, []byte("cluster-value"), recvPayload.Value)
		assert.Equal(t, uint64(100), recvPayload.Version)
		assert.True(t, recvPayload.Sync)
	case <-time.After(5 * time.Second):
		t.Fatal("未接收到集群消息")
	}
}

// BenchmarkClusterMessageBroadcast 消息广播基准测试
func BenchmarkClusterMessageBroadcast(b *testing.B) {
	ctx := context.Background()
	tmpDir := b.TempDir()

	// 创建 2 个节点
	keyPath1 := filepath.Join(tmpDir, "node1.key")
	cfg1 := DefaultP2PServiceConfig("9351", keyPath1)
	node1, err := NewP2PService(cfg1)
	require.NoError(b, err)
	defer node1.Close()

	keyPath2 := filepath.Join(tmpDir, "node2.key")
	cfg2 := DefaultP2PServiceConfig("9352", keyPath2)
	node2, err := NewP2PService(cfg2)
	require.NoError(b, err)
	defer node2.Close()

	err = node1.Start(ctx)
	require.NoError(b, err)
	err = node2.Start(ctx)
	require.NoError(b, err)

	// 建立连接
	peerInfo2 := node2.GetPeerInfo()
	err = node1.ConnectToPeer(ctx, peerInfo2)
	if err != nil {
		b.Skip("无法建立连接")
		return
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		putPayload := &PutPayload{
			Key:     []byte("bench-key"),
			Value:   make([]byte, 128),
			Version: uint64(i),
			Sync:    true,
		}

		msg := &Message{}
		msg.MustEncodePayload(putPayload)

		_ = node1.Protocol().SendMessage(ctx, node2.PeerID(), msg)
	}
}
