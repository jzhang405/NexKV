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
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
)

// TestTwoPCConcurrentTransactions 测试并发 2PC 事务
func TestTwoPCConcurrentTransactions(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	nodes := setupCluster(t, ctx, tmpDir, 3)
	defer teardownCluster(nodes)

	// 注册处理器
	var txCount int
	var txMu sync.Mutex

	for i := 1; i < 3; i++ {
		node := nodes[i]
		node.Protocol().RegisterHandler(MessageTypeSync, MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
			txMu.Lock()
			txCount++
			txMu.Unlock()

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
			return node.Protocol().SendMessage(ctx, from, response)
		}))
	}

	// 并发发送 3 个事务
	const numTx = 3
	for i := 0; i < numTx; i++ {
		go func(txIndex int) {
			txID := fmt.Sprintf("txn-concurrent-%d", txIndex)

			preparePayload := &TwoPCPreparePayload{
				TxID:    txID,
				Timeout: 10000,
				Operations: []Operation{
					{Type: "put", Key: fmt.Sprintf("k%d", txIndex), Value: []byte("v1")},
				},
			}

			msg := &Message{}
			msg.MustEncodePayload(preparePayload)

			peers := make([]peer.ID, 0, 2)
			for j := 1; j < 3; j++ {
				peers = append(peers, nodes[j].PeerID())
			}

			_ = nodes[0].Protocol().BroadcastMessage(ctx, peers, msg)
		}(i)
	}

	// 等待所有事务完成
	time.Sleep(2 * time.Second)

	// 验证至少收到部分事务
	txMu.Lock()
	finalTxCount := txCount
	txMu.Unlock()

	t.Logf("并发 2PC 事务测试：发送 %d 个事务，收到 %d 个响应", numTx, finalTxCount)
	assert.Greater(t, finalTxCount, 0, "应该至少收到部分事务")
}
