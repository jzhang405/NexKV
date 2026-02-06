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
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

// BenchmarkQuorumVs2PC_SmallCluster 小集群（3 节点）性能对比
func BenchmarkQuorumVs2PC_SmallCluster(b *testing.B) {
	ctx := context.Background()
	tmpDir := b.TempDir()

	// 创建 3 节点集群
	nodes := setupCluster(b, ctx, tmpDir, 3)
	defer teardownCluster(nodes)

	// 注册 Quorum 处理器
	for i := 1; i < 3; i++ {
		nodes[i].Protocol().RegisterHandler(MessageTypeQuorum, MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
			// 快速返回投票
			response := &Message{}
			voteResp := &QuorumPayload{
				Phase:      "vote",
				ProposalID: "bench-quorum",
				Key:        "bench-key",
				Voter:      nodes[i].PeerID().String(),
				Decision:   true,
			}
			response.MustEncodePayload(voteResp)
			return nodes[i].Protocol().SendMessage(ctx, from, response)
		}))
	}

	b.Run("Quorum", func(b *testing.B) {
		quorumPayload := &QuorumPayload{
			Phase:      "propose",
			ProposalID: "bench-quorum",
			Key:        "bench-key",
			Value:      make([]byte, 128),
		}

		msg := &Message{}
		msg.MustEncodePayload(quorumPayload)

		peers := make([]peer.ID, 0, 2)
		for i := 1; i < 3; i++ {
			peers = append(peers, nodes[i].PeerID())
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = nodes[0].Protocol().BroadcastMessage(ctx, peers, msg)
		}
	})

	// 注册 2PC 处理器
	for i := 1; i < 3; i++ {
		nodes[i].Protocol().RegisterHandler(MessageTypeSync, MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
			payload, err := msg.DecodePayload()
			if err != nil {
				return err
			}
			prepareMsg, ok := payload.(*TwoPCPreparePayload)
			if !ok {
				return nil
			}

			// 返回 ACK
			ackPayload := &TwoPCCommitPayload{
				TxID:   prepareMsg.TxID,
				Result: true,
			}
			response := &Message{}
			response.MustEncodePayload(ackPayload)
			return nodes[i].Protocol().SendMessage(ctx, from, response)
		}))
	}

	b.Run("2PC", func(b *testing.B) {
		preparePayload := &TwoPCPreparePayload{
			TxID:    "bench-2pc",
			Timeout: 10000,
			Operations: []Operation{
				{Type: "put", Key: "bench-key", Value: make([]byte, 128)},
			},
		}

		msg := &Message{}
		msg.MustEncodePayload(preparePayload)

		peers := make([]peer.ID, 0, 2)
		for i := 1; i < 3; i++ {
			peers = append(peers, nodes[i].PeerID())
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = nodes[0].Protocol().BroadcastMessage(ctx, peers, msg)
		}
	})
}

// BenchmarkQuorumVs2PC_MediumCluster 中等集群（5 节点）性能对比
func BenchmarkQuorumVs2PC_MediumCluster(b *testing.B) {
	ctx := context.Background()
	tmpDir := b.TempDir()

	// 创建 5 节点集群
	nodes := setupCluster(b, ctx, tmpDir, 5)
	defer teardownCluster(nodes)

	// 注册 Quorum 处理器（多数派 3/5）
	for i := 1; i < 5; i++ {
		nodeIdx := i
		nodes[i].Protocol().RegisterHandler(MessageTypeQuorum, MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
			response := &Message{}
			voteResp := &QuorumPayload{
				Phase:      "vote",
				ProposalID: "bench-quorum",
				Key:        "bench-key",
				Voter:      nodes[nodeIdx].PeerID().String(),
				Decision:   true,
			}
			response.MustEncodePayload(voteResp)
			return nodes[nodeIdx].Protocol().SendMessage(ctx, from, response)
		}))
	}

	b.Run("Quorum", func(b *testing.B) {
		quorumPayload := &QuorumPayload{
			Phase:      "propose",
			ProposalID: "bench-quorum",
			Key:        "bench-key",
			Value:      make([]byte, 128),
		}

		msg := &Message{}
		msg.MustEncodePayload(quorumPayload)

		peers := make([]peer.ID, 0, 4)
		for i := 1; i < 5; i++ {
			peers = append(peers, nodes[i].PeerID())
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = nodes[0].Protocol().BroadcastMessage(ctx, peers, msg)
		}
	})

	// 注册 2PC 处理器（全员应答 4/4）
	for i := 1; i < 5; i++ {
		nodeIdx := i
		nodes[i].Protocol().RegisterHandler(MessageTypeSync, MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
			payload, err := msg.DecodePayload()
			if err != nil {
				return err
			}
			prepareMsg, ok := payload.(*TwoPCPreparePayload)
			if !ok {
				return nil
			}

			ackPayload := &TwoPCCommitPayload{
				TxID:   prepareMsg.TxID,
				Result: true,
			}
			response := &Message{}
			response.MustEncodePayload(ackPayload)
			return nodes[nodeIdx].Protocol().SendMessage(ctx, from, response)
		}))
	}

	b.Run("2PC", func(b *testing.B) {
		preparePayload := &TwoPCPreparePayload{
			TxID:    "bench-2pc",
			Timeout: 10000,
			Operations: []Operation{
				{Type: "put", Key: "bench-key", Value: make([]byte, 128)},
			},
		}

		msg := &Message{}
		msg.MustEncodePayload(preparePayload)

		peers := make([]peer.ID, 0, 4)
		for i := 1; i < 5; i++ {
			peers = append(peers, nodes[i].PeerID())
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = nodes[0].Protocol().BroadcastMessage(ctx, peers, msg)
		}
	})
}

// BenchmarkQuorumVs2PC_LargeCluster 大集群（10 节点）性能对比
func BenchmarkQuorumVs2PC_LargeCluster(b *testing.B) {
	ctx := context.Background()
	tmpDir := b.TempDir()

	// 创建 10 节点集群（仅测试部分节点以避免超时）
	nodes := make([]*P2PService, 10)
	for i := 0; i < 10; i++ {
		keyPath := filepath.Join(tmpDir, fmt.Sprintf("node%d.key", i))
		cfg := DefaultP2PServiceConfig(fmt.Sprintf("991%d", i), keyPath)

		service, err := NewP2PService(cfg)
		if err != nil {
			continue
		}

		err = service.Start(ctx)
		if err != nil {
			continue
		}

		nodes[i] = service
	}
	defer teardownCluster(nodes)

	// 建立部分连接（避免全连接开销）
	for i := 0; i < 5; i++ {
		for j := i + 1; j < 5; j++ {
			if nodes[j] != nil && nodes[i] != nil {
				peerInfo := nodes[j].GetPeerInfo()
				_ = nodes[i].ConnectToPeer(ctx, peerInfo)
			}
		}
	}

	time.Sleep(200 * time.Millisecond)

	// 仅使用前 5 个节点进行测试
	activeNodes := make([]*P2PService, 0, 5)
	for i := 0; i < 5; i++ {
		if nodes[i] != nil {
			activeNodes = append(activeNodes, nodes[i])
		}
	}

	if len(activeNodes) < 3 {
		b.Skip("需要至少 3 个节点")
		return
	}

	// 注册 Quorum 处理器
	for i := 1; i < len(activeNodes); i++ {
		nodeIdx := i
		activeNodes[i].Protocol().RegisterHandler(MessageTypeQuorum, MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
			response := &Message{}
			voteResp := &QuorumPayload{
				Phase:      "vote",
				ProposalID: "bench-quorum",
				Key:        "bench-key",
				Voter:      activeNodes[nodeIdx].PeerID().String(),
				Decision:   true,
			}
			response.MustEncodePayload(voteResp)
			return activeNodes[nodeIdx].Protocol().SendMessage(ctx, from, response)
		}))
	}

	b.Run("Quorum", func(b *testing.B) {
		quorumPayload := &QuorumPayload{
			Phase:      "propose",
			ProposalID: "bench-quorum",
			Key:        "bench-key",
			Value:      make([]byte, 128),
		}

		msg := &Message{}
		msg.MustEncodePayload(quorumPayload)

		peers := make([]peer.ID, 0, len(activeNodes)-1)
		for i := 1; i < len(activeNodes); i++ {
			peers = append(peers, activeNodes[i].PeerID())
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = activeNodes[0].Protocol().BroadcastMessage(ctx, peers, msg)
		}
	})

	// 注册 2PC 处理器
	for i := 1; i < len(activeNodes); i++ {
		nodeIdx := i
		activeNodes[i].Protocol().RegisterHandler(MessageTypeSync, MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
			payload, err := msg.DecodePayload()
			if err != nil {
				return err
			}
			prepareMsg, ok := payload.(*TwoPCPreparePayload)
			if !ok {
				return nil
			}

			ackPayload := &TwoPCCommitPayload{
				TxID:   prepareMsg.TxID,
				Result: true,
			}
			response := &Message{}
			response.MustEncodePayload(ackPayload)
			return activeNodes[nodeIdx].Protocol().SendMessage(ctx, from, response)
		}))
	}

	b.Run("2PC", func(b *testing.B) {
		preparePayload := &TwoPCPreparePayload{
			TxID:    "bench-2pc",
			Timeout: 10000,
			Operations: []Operation{
				{Type: "put", Key: "bench-key", Value: make([]byte, 128)},
			},
		}

		msg := &Message{}
		msg.MustEncodePayload(preparePayload)

		peers := make([]peer.ID, 0, len(activeNodes)-1)
		for i := 1; i < len(activeNodes); i++ {
			peers = append(peers, activeNodes[i].PeerID())
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = activeNodes[0].Protocol().BroadcastMessage(ctx, peers, msg)
		}
	})
}

// BenchmarkQuorumVs2PC_SmallPayload 小消息性能对比
func BenchmarkQuorumVs2PC_SmallPayload(b *testing.B) {
	ctx := context.Background()
	tmpDir := b.TempDir()

	nodes := setupCluster(b, ctx, tmpDir, 3)
	defer teardownCluster(nodes)

	// 注册处理器
	for i := 1; i < 3; i++ {
		nodes[i].Protocol().RegisterHandler(MessageTypeQuorum, MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
			response := &Message{}
			voteResp := &QuorumPayload{
				Phase:      "vote",
				ProposalID: "bench-quorum",
				Key:        "bench-key",
				Voter:      nodes[i].PeerID().String(),
				Decision:   true,
			}
			response.MustEncodePayload(voteResp)
			return nodes[i].Protocol().SendMessage(ctx, from, response)
		}))

		nodes[i].Protocol().RegisterHandler(MessageTypeSync, MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
			payload, err := msg.DecodePayload()
			if err != nil {
				return err
			}
			prepareMsg, ok := payload.(*TwoPCPreparePayload)
			if !ok {
				return nil
			}

			ackPayload := &TwoPCCommitPayload{
				TxID:   prepareMsg.TxID,
				Result: true,
			}
			response := &Message{}
			response.MustEncodePayload(ackPayload)
			return nodes[i].Protocol().SendMessage(ctx, from, response)
		}))
	}

	b.Run("Quorum", func(b *testing.B) {
		quorumPayload := &QuorumPayload{
			Phase:      "propose",
			ProposalID: "bench-quorum",
			Key:        "bench-key",
			Value:      make([]byte, 64), // 小消息
		}

		msg := &Message{}
		msg.MustEncodePayload(quorumPayload)

		peers := make([]peer.ID, 0, 2)
		for i := 1; i < 3; i++ {
			peers = append(peers, nodes[i].PeerID())
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = nodes[0].Protocol().BroadcastMessage(ctx, peers, msg)
		}
	})

	b.Run("2PC", func(b *testing.B) {
		preparePayload := &TwoPCPreparePayload{
			TxID:    "bench-2pc",
			Timeout: 10000,
			Operations: []Operation{
				{Type: "put", Key: "bench-key", Value: make([]byte, 64)}, // 小消息
			},
		}

		msg := &Message{}
		msg.MustEncodePayload(preparePayload)

		peers := make([]peer.ID, 0, 2)
		for i := 1; i < 3; i++ {
			peers = append(peers, nodes[i].PeerID())
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = nodes[0].Protocol().BroadcastMessage(ctx, peers, msg)
		}
	})
}

// BenchmarkQuorumVs2PC_LargePayload 大消息性能对比
func BenchmarkQuorumVs2PC_LargePayload(b *testing.B) {
	ctx := context.Background()
	tmpDir := b.TempDir()

	nodes := setupCluster(b, ctx, tmpDir, 3)
	defer teardownCluster(nodes)

	// 注册处理器
	for i := 1; i < 3; i++ {
		nodes[i].Protocol().RegisterHandler(MessageTypeQuorum, MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
			response := &Message{}
			voteResp := &QuorumPayload{
				Phase:      "vote",
				ProposalID: "bench-quorum",
				Key:        "bench-key",
				Voter:      nodes[i].PeerID().String(),
				Decision:   true,
			}
			response.MustEncodePayload(voteResp)
			return nodes[i].Protocol().SendMessage(ctx, from, response)
		}))

		nodes[i].Protocol().RegisterHandler(MessageTypeSync, MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
			payload, err := msg.DecodePayload()
			if err != nil {
				return err
			}
			prepareMsg, ok := payload.(*TwoPCPreparePayload)
			if !ok {
				return nil
			}

			ackPayload := &TwoPCCommitPayload{
				TxID:   prepareMsg.TxID,
				Result: true,
			}
			response := &Message{}
			response.MustEncodePayload(ackPayload)
			return nodes[i].Protocol().SendMessage(ctx, from, response)
		}))
	}

	b.Run("Quorum", func(b *testing.B) {
		quorumPayload := &QuorumPayload{
			Phase:      "propose",
			ProposalID: "bench-quorum",
			Key:        "bench-key",
			Value:      make([]byte, 4096), // 大消息
		}

		msg := &Message{}
		msg.MustEncodePayload(quorumPayload)

		peers := make([]peer.ID, 0, 2)
		for i := 1; i < 3; i++ {
			peers = append(peers, nodes[i].PeerID())
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = nodes[0].Protocol().BroadcastMessage(ctx, peers, msg)
		}
	})

	b.Run("2PC", func(b *testing.B) {
		preparePayload := &TwoPCPreparePayload{
			TxID:    "bench-2pc",
			Timeout: 10000,
			Operations: []Operation{
				{Type: "put", Key: "bench-key", Value: make([]byte, 4096)}, // 大消息
			},
		}

		msg := &Message{}
		msg.MustEncodePayload(preparePayload)

		peers := make([]peer.ID, 0, 2)
		for i := 1; i < 3; i++ {
			peers = append(peers, nodes[i].PeerID())
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = nodes[0].Protocol().BroadcastMessage(ctx, peers, msg)
		}
	})
}
