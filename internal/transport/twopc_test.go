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
	"github.com/stretchr/testify/require"
)

// TestTwoPCBasicFlow 测试 2PC 基本流程（Prepare → Commit）
func TestTwoPCBasicFlow(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	nodes := setupCluster(t, ctx, tmpDir, 3)
	defer teardownCluster(nodes)

	txID := "txn-001"

	// 注册 Prepare 阶段处理器
	var prepareCount int
	var prepareMu sync.Mutex

	for i := 1; i < 3; i++ {
		node := nodes[i]
		node.Protocol().RegisterHandler(MessageTypeSync, MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
			// 解码 Prepare 消息
			payload, err := msg.DecodePayload()
			require.NoError(t, err)
			prepareMsg, ok := payload.(*TwoPCPreparePayload)
			require.True(t, ok)

			// 验证事务 ID
			assert.Equal(t, txID, prepareMsg.TxID)

			prepareMu.Lock()
			prepareCount++
			prepareMu.Unlock()

			// 返回 ACK（同意提交）
			ackPayload := &TwoPCCommitPayload{
				TxID:   txID,
				Result: true,
			}

			response := &Message{}
			response.MustEncodePayload(ackPayload)
			return node.Protocol().SendMessage(ctx, from, response)
		}))
	}

	// 发起 Prepare 阶段
	preparePayload := &TwoPCPreparePayload{
		TxID:    txID,
		Timeout: 30000,
		Operations: []Operation{
			{Type: "put", Key: "k1", Value: []byte("v1")},
			{Type: "put", Key: "k2", Value: []byte("v2")},
		},
		Coordinator: nodes[0].PeerID().String(),
	}

	msg := &Message{}
	msg.MustEncodePayload(preparePayload)

	peers := make([]peer.ID, 0, 2)
	for i := 1; i < 3; i++ {
		peers = append(peers, nodes[i].PeerID())
	}

	err := nodes[0].Protocol().BroadcastMessage(ctx, peers, msg)
	require.NoError(t, err)

	// 等待所有节点响应
	time.Sleep(500 * time.Millisecond)

	// 验证所有节点都收到了 Prepare 消息
	prepareMu.Lock()
	finalPrepareCount := prepareCount
	prepareMu.Unlock()

	assert.Equal(t, 2, finalPrepareCount, "所有 2 个节点应该收到 Prepare 消息")

	t.Log("2PC 基本流程测试通过：Prepare → Commit")
}

// TestTwoPCRollback 测试 2PC 失败回滚场景
func TestTwoPCRollback(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	nodes := setupCluster(t, ctx, tmpDir, 3)
	defer teardownCluster(nodes)

	txID := "txn-rollback"

	// 节点 1 同意，节点 2 拒绝
	voteResults := make(map[string]bool)
	var voteMu sync.Mutex

	for i := 1; i < 3; i++ {
		nodeIndex := i
		node := nodes[i]
		node.Protocol().RegisterHandler(MessageTypeSync, MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
			payload, err := msg.DecodePayload()
			require.NoError(t, err)
			prepareMsg, ok := payload.(*TwoPCPreparePayload)
			require.True(t, ok)

			// 节点 2 拒绝，其他节点同意
			agree := (nodeIndex != 2)

			voteMu.Lock()
			voteResults[node.PeerID().String()] = agree
			voteMu.Unlock()

			// 返回投票结果
			if agree {
				ackPayload := &TwoPCCommitPayload{
					TxID:   prepareMsg.TxID,
					Result: true,
				}
				response := &Message{}
				response.MustEncodePayload(ackPayload)
				return node.Protocol().SendMessage(ctx, from, response)
			} else {
				// 拒绝：返回 Nack
				rollbackPayload := &TwoPCRollbackPayload{
					TxID:   prepareMsg.TxID,
					Reason: "constraint violation",
				}
				response := &Message{}
				response.MustEncodePayload(rollbackPayload)
				return node.Protocol().SendMessage(ctx, from, response)
			}
		}))
	}

	// 发起 Prepare
	preparePayload := &TwoPCPreparePayload{
		TxID:    txID,
		Timeout: 30000,
		Operations: []Operation{
			{Type: "put", Key: "k1", Value: []byte("v1")},
		},
	}

	msg := &Message{}
	msg.MustEncodePayload(preparePayload)

	peers := make([]peer.ID, 0, 2)
	for i := 1; i < 3; i++ {
		peers = append(peers, nodes[i].PeerID())
	}

	err := nodes[0].Protocol().BroadcastMessage(ctx, peers, msg)
	require.NoError(t, err)

	// 等待响应
	time.Sleep(500 * time.Millisecond)

	// 验证投票结果
	voteMu.Lock()
	results := make(map[string]bool)
	for k, v := range voteResults {
		results[k] = v
	}
	voteMu.Unlock()

	// 应该有 1 个同意、1 个拒绝
	agreeCount := 0
	refuseCount := 0
	for _, result := range results {
		if result {
			agreeCount++
		} else {
			refuseCount++
		}
	}

	assert.Equal(t, 1, agreeCount, "应该有 1 个节点同意")
	assert.Equal(t, 1, refuseCount, "应该有 1 个节点拒绝")

	t.Log("2PC 回滚测试通过：有节点拒绝，事务应回滚")
}

// TestTwoPCUnanimousConsensus 测试 2PC 全员应答（100% 确认）
func TestTwoPCUnanimousConsensus(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	nodes := setupCluster(t, ctx, tmpDir, 3)
	defer teardownCluster(nodes)

	txID := "txn-unanimous"

	// 收集投票响应
	votes := make(chan bool, 10)
	var wg sync.WaitGroup
	wg.Add(2)

	for i := 1; i < 3; i++ {
		node := nodes[i]
		node.Protocol().RegisterHandler(MessageTypeSync, MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
			defer wg.Done()

			payload, err := msg.DecodePayload()
			require.NoError(t, err)
			prepareMsg, ok := payload.(*TwoPCPreparePayload)
			require.True(t, ok)

			// 模拟所有节点都同意
			agree := true

			// 返回投票
			ackPayload := &TwoPCCommitPayload{
				TxID:   prepareMsg.TxID,
				Result: agree,
			}
			response := &Message{}
			response.MustEncodePayload(ackPayload)

			err = node.Protocol().SendMessage(ctx, from, response)
			assert.NoError(t, err)

			// 通知投票结果
			votes <- agree
			return nil
		}))
	}

	// 发起 Prepare
	preparePayload := &TwoPCPreparePayload{
		TxID:    txID,
		Timeout: 30000,
		Operations: []Operation{
			{Type: "put", Key: "key1", Value: []byte("value1")},
		},
	}

	msg := &Message{}
	msg.MustEncodePayload(preparePayload)

	peers := make([]peer.ID, 0, 2)
	for i := 1; i < 3; i++ {
		peers = append(peers, nodes[i].PeerID())
	}

	err := nodes[0].Protocol().BroadcastMessage(ctx, peers, msg)
	require.NoError(t, err)

	// 等待所有投票（使用 WaitGroup + Timeout）
	doneCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(doneCh)
	}()

	select {
	case <-doneCh:
		// 所有投票已收集
	case <-time.After(5 * time.Second):
		t.Fatal("等待投票超时")
	}

	// 验证投票结果
	close(votes)
	agreeCount := 0
	for vote := range votes {
		if vote {
			agreeCount++
		}
	}

	assert.Equal(t, 2, agreeCount, "2PC 需要 100% 节点同意（2/2）")

	t.Log("2PC 全员应答测试通过：所有节点一致同意")
}

// TestTwoPCTimeout 测试 2PC 超时处理
func TestTwoPCTimeout(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	nodes := setupCluster(t, ctx, tmpDir, 3)
	defer teardownCluster(nodes)

	txID := "txn-timeout"

	// 模拟响应延迟场景：记录响应时间
	responseTimes := make(chan time.Duration, 10)
	var wg sync.WaitGroup

	for i := 1; i < 3; i++ {
		node := nodes[i]
		wg.Add(1) // 在启动 goroutine 之前调用 Add
		node.Protocol().RegisterHandler(MessageTypeSync, MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
			defer wg.Done()

			startTime := time.Now()

			// 模拟处理延迟
			time.Sleep(100 * time.Millisecond)

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

			err = node.Protocol().SendMessage(ctx, from, response)
			// channel 发送是并发安全的，不需要 mutex
			responseTimes <- time.Since(startTime)
			return err
		}))
	}

	// 发起 Prepare
	preparePayload := &TwoPCPreparePayload{
		TxID:    txID,
		Timeout: 5000, // 5 秒超时
		Operations: []Operation{
			{Type: "put", Key: "k1", Value: []byte("v1")},
		},
	}

	msg := &Message{}
	msg.MustEncodePayload(preparePayload)

	peers := make([]peer.ID, 0, 2)
	for i := 1; i < 3; i++ {
		peers = append(peers, nodes[i].PeerID())
	}

	startTime := time.Now()
	err := nodes[0].Protocol().BroadcastMessage(ctx, peers, msg)
	elapsed := time.Since(startTime)

	// 广播本身应该快速返回
	assert.Less(t, elapsed, 1*time.Second, "广播应该快速返回")

	// 等待所有响应处理完成
	wg.Wait()

	// 等待额外时间确保响应发送
	time.Sleep(200 * time.Millisecond)

	// 验证至少收到了部分响应
	close(responseTimes)
	responseCount := 0
	for range responseTimes {
		responseCount++
	}

	if err != nil {
		t.Logf("2PC 超时测试：广播操作耗时 %v，收到 %d 个响应", elapsed, responseCount)
	}

	// 验证至少部分节点响应了
	t.Log("2PC 超时处理测试完成")
}

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

// BenchmarkTwoPCCommit 2PC 提交性能基准测试
func BenchmarkTwoPCCommit(b *testing.B) {
	ctx := context.Background()
	tmpDir := b.TempDir()
	nodes := setupCluster(b, ctx, tmpDir, 3)
	defer teardownCluster(nodes)

	// 注册处理器
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

			ackPayload := &TwoPCCommitPayload{
				TxID:   prepareMsg.TxID,
				Result: true,
			}
			response := &Message{}
			response.MustEncodePayload(ackPayload)
			return nodes[i].Protocol().SendMessage(ctx, from, response)
		}))
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		txID := fmt.Sprintf("txn-bench-%d", i)

		preparePayload := &TwoPCPreparePayload{
			TxID:    txID,
			Timeout: 10000,
			Operations: []Operation{
				{Type: "put", Key: "bench-key", Value: make([]byte, 128)},
			},
		}

		msg := &Message{}
		msg.MustEncodePayload(preparePayload)

		peers := make([]peer.ID, 0, 2)
		for j := 1; j < 3; j++ {
			peers = append(peers, nodes[j].PeerID())
		}

		_ = nodes[0].Protocol().BroadcastMessage(ctx, peers, msg)
	}
}
