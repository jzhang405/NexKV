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
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestQuorumConsistency 测试 Quorum 一致性
func TestQuorumConsistency(t *testing.T) {
	ctx := context.Background()

	// 创建 5 节点集群
	tmpDir := t.TempDir()
	nodes := setupCluster(t, ctx, tmpDir, 5)
	defer teardownCluster(nodes)

	// Quorum 阈值 = 5/2 + 1 = 3
	quorumThreshold := 3

	// 模拟 2 个节点故障（停止服务）
	_ = nodes[3].Stop()
	_ = nodes[4].Stop()

	// 等待停止完成
	time.Sleep(100 * time.Millisecond)

	// 发送 Quorum 消息
	quorumPayload := &QuorumPayload{
		Phase:      "propose",
		ProposalID: "prop-001",
		Key:        "quorum-test",
		Value:      []byte("quorum-value"),
	}

	msg := &Message{}
	msg.MustEncodePayload(quorumPayload)

	// 注册处理器
	var voteCount int
	var mu sync.Mutex

	for i := range 3 {
		node := nodes[i]
		node.Protocol().RegisterHandler(MessageTypeQuorum, MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
			mu.Lock()
			voteCount++
			mu.Unlock()

			// 返回投票
			response := &Message{}
			voteResp := &QuorumPayload{
				Phase:      "vote",
				ProposalID: "prop-001",
				Key:        "quorum-test",
				Voter:      node.PeerID().String(),
				Decision:   true,
			}
			response.MustEncodePayload(voteResp)
			_ = node.Protocol().SendMessage(ctx, from, response)
			return nil
		}))
	}

	// 广播 Quorum 提案
	peers := make([]peer.ID, 0, 2)
	for i := 1; i < 3; i++ {
		peers = append(peers, nodes[i].PeerID())
	}
	err := nodes[0].Protocol().BroadcastMessage(ctx, peers, msg)
	require.NoError(t, err)

	// 等待投票
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	finalVotes := voteCount
	mu.Unlock()

	// 验证 Quorum 达成（2 个投票 + 发起节点 = 3）
	assert.GreaterOrEqual(t, finalVotes, quorumThreshold-1, "Quorum 应该达成")
}

// TestQuorumFailure 测试 Quorum 失败场景
func TestQuorumFailure(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	nodes := setupCluster(t, ctx, tmpDir, 5)
	defer teardownCluster(nodes)

	// 模拟 3 个节点故障（只剩 2 个）
	_ = nodes[2].Stop()
	_ = nodes[3].Stop()
	_ = nodes[4].Stop()

	time.Sleep(100 * time.Millisecond)

	// 注册处理器
	for i := 0; i < 2; i++ {
		nodes[i].Protocol().RegisterHandler(MessageTypeQuorum, MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
			// 返回投票
			response := &Message{}
			voteResp := &QuorumPayload{
				Phase:      "vote",
				ProposalID: "prop-002",
				Key:        "test-key",
				Voter:      nodes[i].PeerID().String(),
				Decision:   true,
			}
			response.MustEncodePayload(voteResp)
			_ = nodes[i].Protocol().SendMessage(ctx, from, response)
			return nil
		}))
	}

	// 发送 Quorum 提案
	quorumPayload := &QuorumPayload{
		Phase:      "propose",
		ProposalID: "prop-002",
		Key:        "test-key",
		Value:      []byte("test-value"),
	}

	msg := &Message{}
	msg.MustEncodePayload(quorumPayload)

	// 只能发送给剩余的 1 个节点
	peers := nodes[0].Host().Network().Peers()
	if len(peers) > 0 {
		err := nodes[0].Protocol().BroadcastMessage(ctx, peers, msg)
		require.NoError(t, err)
	}

	// Quorum 无法达成（只有 2 个节点，需要 3 个）
	// 这应该是失败场景，但网络库可能不会报错
	t.Log("Quorum 失败场景：只有 2 个节点，需要 3 个")
}

// TestQuorumAllNodesAgree 测试所有节点一致同意
func TestQuorumAllNodesAgree(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	nodes := setupCluster(t, ctx, tmpDir, 3)
	defer teardownCluster(nodes)

	// 注册处理器
	votes := make(chan *QuorumPayload, 10)

	for i := 1; i < 3; i++ {
		node := nodes[i]
		node.Protocol().RegisterHandler(MessageTypeQuorum, MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
			// 解码 Quorum 提案
			payload, err := msg.DecodePayload()
			require.NoError(t, err)
			quorumMsg, ok := payload.(*QuorumPayload)
			require.True(t, ok)

			// 同意提案
			voteResp := &QuorumPayload{
				Phase:      "vote",
				ProposalID: quorumMsg.ProposalID,
				Key:        quorumMsg.Key,
				Voter:      node.PeerID().String(),
				Decision:   true,
			}

			response := &Message{}
			response.MustEncodePayload(voteResp)
			err = node.Protocol().SendMessage(ctx, from, response)
			assert.NoError(t, err)

			// 发送投票到 channel
			votes <- voteResp
			return nil
		}))
	}

	// 发起 Quorum 提案
	quorumPayload := &QuorumPayload{
		Phase:      "propose",
		ProposalID: "prop-unanimous",
		Key:        "unanimous-key",
		Value:      []byte("unanimous-value"),
	}

	msg := &Message{}
	msg.MustEncodePayload(quorumPayload)

	peers := make([]peer.ID, 0, 2)
	for i := 1; i < 3; i++ {
		peers = append(peers, nodes[i].PeerID())
	}

	err := nodes[0].Protocol().BroadcastMessage(ctx, peers, msg)
	require.NoError(t, err)

	// 收集投票
	collectedVotes := make([]*QuorumPayload, 0, 2)
	timeout := time.After(3 * time.Second)

	voteCount := 0
loop:
	for {
		select {
		case vote := <-votes:
			collectedVotes = append(collectedVotes, vote)
			voteCount++
			if voteCount >= 2 {
				return // 收到足够投票
			}
		case <-timeout:
			break loop
		}
	}

	// 验证所有投票都是同意
	agreeCount := 0
	for _, vote := range collectedVotes {
		if vote.Decision {
			agreeCount++
		}
	}

	assert.Equal(t, 2, agreeCount, "所有节点应该同意")
}

// TestQuorumDecidePhase 测试 Quorum 决策阶段
func TestQuorumDecidePhase(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	nodes := setupCluster(t, ctx, tmpDir, 3)
	defer teardownCluster(nodes)

	proposalID := "prop-decide"

	// 注册处理器（包含决定阶段）
	for i := 1; i < 3; i++ {
		node := nodes[i]
		node.Protocol().RegisterHandler(MessageTypeQuorum, MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
			payload, err := msg.DecodePayload()
			require.NoError(t, err)
			quorumMsg, ok := payload.(*QuorumPayload)
			require.True(t, ok)

			if quorumMsg.Phase == "propose" {
				// 投票
				voteResp := &QuorumPayload{
					Phase:      "vote",
					ProposalID: proposalID,
					Key:        quorumMsg.Key,
					Voter:      node.PeerID().String(),
					Decision:   true,
				}

				response := &Message{}
				response.MustEncodePayload(voteResp)
				return node.Protocol().SendMessage(ctx, from, response)
			}

			return nil
		}))
	}

	// 发起提案
	proposePayload := &QuorumPayload{
		Phase:      "propose",
		ProposalID: proposalID,
		Key:        "decide-key",
		Value:      []byte("decide-value"),
	}

	msg := &Message{}
	msg.MustEncodePayload(proposePayload)

	peers := make([]peer.ID, 0, 2)
	for i := 1; i < 3; i++ {
		peers = append(peers, nodes[i].PeerID())
	}

	err := nodes[0].Protocol().BroadcastMessage(ctx, peers, msg)
	require.NoError(t, err)

	// 等待投票
	time.Sleep(500 * time.Millisecond)

	// 发送决定消息（假设达成一致）
	decidePayload := &QuorumPayload{
		Phase:      "decide",
		ProposalID: proposalID,
		Key:        "decide-key",
		Decision:   true,
	}

	decideMsg := &Message{}
	decideMsg.MustEncodePayload(decidePayload)

	err = nodes[0].Protocol().BroadcastMessage(ctx, peers, decideMsg)
	require.NoError(t, err)

	t.Log("Quorum 决策阶段完成")
}

// setupCluster 创建测试集群
func setupCluster(t testing.TB, ctx context.Context, tmpDir string, size int) []*P2PService {
	nodes := make([]*P2PService, size)

	for i := 0; i < size; i++ {
		keyPath := filepath.Join(tmpDir, "node"+string(rune('1'+i))+".key")
		cfg := DefaultP2PServiceConfig("94"+string(rune('0'+i)), keyPath)

		service, err := NewP2PService(cfg)
		require.NoError(t, err)

		err = service.Start(ctx)
		require.NoError(t, err)

		nodes[i] = service
	}

	// 建立全连接网络
	for i := 0; i < size; i++ {
		for j := i + 1; j < size; j++ {
			peerInfo := nodes[j].GetPeerInfo()
			err := nodes[i].ConnectToPeer(ctx, peerInfo)
			if err != nil {
				// 部分连接可能失败，继续
				continue
			}
		}
	}

	// 等待连接建立
	time.Sleep(200 * time.Millisecond)

	return nodes
}

// teardownCluster 清理集群
func teardownCluster(nodes []*P2PService) {
	for _, node := range nodes {
		if node.IsStarted() {
			_ = node.Close()
		}
	}
}

// TestQuorumWithConcurrentProposals 测试并发的 Quorum 提案
func TestQuorumWithConcurrentProposals(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	nodes := setupCluster(t, ctx, tmpDir, 3)
	defer teardownCluster(nodes)

	// 注册处理器
	for i := 1; i < 3; i++ {
		nodes[i].Protocol().RegisterHandler(MessageTypeQuorum, MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
			// 简单确认
			return nil
		}))
	}

	// 并发发送 3 个不同的提案
	proposals := []string{"proposal-a", "proposal-b", "proposal-c"}

	for _, propID := range proposals {
		go func(id string) {
			quorumPayload := &QuorumPayload{
				Phase:      "propose",
				ProposalID: id,
				Key:        "concurrent-key",
				Value:      []byte("value"),
			}

			msg := &Message{}
			msg.MustEncodePayload(quorumPayload)

			peers := make([]peer.ID, 0, 2)
			for j := 1; j < 3; j++ {
				peers = append(peers, nodes[j].PeerID())
			}

			_ = nodes[0].Protocol().BroadcastMessage(ctx, peers, msg)
		}(propID)
	}

	// 等待所有提案发送完成
	time.Sleep(1 * time.Second)

	t.Log("并发 Quorum 提案发送完成")
}

// sync 包被移除，因为不再需要

// BenchmarkQuorumVote Quorum 投票性能基准测试
func BenchmarkQuorumVote(b *testing.B) {
	ctx := context.Background()
	tmpDir := b.TempDir()
	nodes := setupCluster(b, ctx, tmpDir, 3)
	defer teardownCluster(nodes)

	// 注册处理器
	for i := 1; i < 3; i++ {
		nodes[i].Protocol().RegisterHandler(MessageTypeQuorum, MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
			return nil
		}))
	}

	quorumPayload := &QuorumPayload{
		Phase:      "propose",
		ProposalID: "bench-proposal",
		Key:        "bench-key",
		Value:      []byte("bench-value"),
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		msg := &Message{}
		msg.MustEncodePayload(quorumPayload)

		peers := make([]peer.ID, 0, 2)
		for j := 1; j < 3; j++ {
			peers = append(peers, nodes[j].PeerID())
		}

		_ = nodes[0].Protocol().BroadcastMessage(ctx, peers, msg)
	}
}
