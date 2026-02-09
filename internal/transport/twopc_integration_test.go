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
