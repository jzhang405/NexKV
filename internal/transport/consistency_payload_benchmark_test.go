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
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"
)

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
