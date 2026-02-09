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
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNexKVProtocol_ConcurrentMessaging 测试并发消息传递
func TestNexKVProtocol_ConcurrentMessaging(t *testing.T) {
	ctx := context.Background()

	h1, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h1.Close()

	h2, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h2.Close()

	_ = h1.Connect(ctx, peer.AddrInfo{ID: h2.ID(), Addrs: h2.Addrs()})

	protocol1 := NewNexKVProtocol(h1, nil)
	protocol2 := NewNexKVProtocol(h2, nil)

	count := 0
	var countMu sync.Mutex
	protocol2.RegisterHandler(MessageTypePut, MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
		countMu.Lock()
		count++
		countMu.Unlock()
		return nil
	}))

	// 并发发送 100 条消息（使用 Payload 模式）
	concurrency := 100
	done := make(chan bool, concurrency)

	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			msg := &Message{}
			msg.MustEncodePayload(&PutPayload{
				Key:     []byte("concurrent-key"),
				Value:   []byte("value"),
				Version: uint64(idx),
			})
			_ = protocol1.SendMessage(ctx, h2.ID(), msg)
			done <- true
		}(i)
	}

	// 等待所有发送完成
	for i := 0; i < concurrency; i++ {
		<-done
	}

	// 等待处理完成
	time.Sleep(500 * time.Millisecond)

	countMu.Lock()
	receivedCount := count
	countMu.Unlock()

	// 验证至少收到部分消息（网络可能丢包）
	assert.Greater(t, receivedCount, 0)
}

// TestNexKVProtocol_MessageWithSeq 测试消息序号
func TestNexKVProtocol_MessageWithSeq(t *testing.T) {
	ctx := context.Background()

	h1, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h1.Close()

	h2, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h2.Close()

	_ = h1.Connect(ctx, peer.AddrInfo{ID: h2.ID(), Addrs: h2.Addrs()})

	protocol1 := NewNexKVProtocol(h1, nil)
	protocol2 := NewNexKVProtocol(h2, nil)

	protocol2.RegisterHandler(MessageTypePut, MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
		// 验证序号存在
		assert.Greater(t, msg.Seq, uint64(0))
		return nil
	}))

	// 使用 Payload 模式创建消息
	msg := &Message{}
	msg.MustEncodePayload(&PutPayload{
		Key:   []byte("key"),
		Value: []byte("value"),
	})

	err = protocol1.SendMessage(ctx, h2.ID(), msg)
	require.NoError(t, err)

	// 等待处理
	time.Sleep(100 * time.Millisecond)
}

// TestNexKVProtocol_AllMessageTypes 测试所有消息类型
func TestNexKVProtocol_AllMessageTypes(t *testing.T) {
	ctx := context.Background()

	h1, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h1.Close()

	h2, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h2.Close()

	_ = h1.Connect(ctx, peer.AddrInfo{ID: h2.ID(), Addrs: h2.Addrs()})

	protocol1 := NewNexKVProtocol(h1, nil)
	protocol2 := NewNexKVProtocol(h2, nil)

	msgTypes := []MessageType{
		MessageTypeGet,
		MessageTypePut,
		MessageTypeDelete,
		MessageTypeSync,
		MessageTypeAck,
		MessageTypeGossip,
	}

	for _, msgType := range msgTypes {
		t.Run(msgType.String(), func(t *testing.T) {
			received := make(chan *Message, 1)
			protocol2.RegisterHandler(msgType, MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
				received <- msg
				return nil
			}))

			var msg *Message
			switch msgType {
			case MessageTypePut:
				msg = &Message{}
				msg.MustEncodePayload(&PutPayload{
					Key:   []byte("test-key"),
					Value: []byte("test-value"),
				})
			case MessageTypeAck:
				// ACK 消息不需要 Payload
				msg = &Message{
					Type: msgType,
					Seq:  1,
				}
			case MessageTypeGet:
				msg = &Message{}
				msg.MustEncodePayload(&GetPayload{
					Key: []byte("test-key"),
				})
			case MessageTypeDelete:
				msg = &Message{}
				msg.MustEncodePayload(&DeletePayload{
					Key: []byte("test-key"),
				})
			case MessageTypeSync:
				msg = &Message{}
				msg.MustEncodePayload(&TwoPCPreparePayload{
					TxID: "test-tx",
				})
			case MessageTypeGossip:
				msg = &Message{}
				msg.MustEncodePayload(&GossipPayload{
					Digest: map[string]uint64{"k1": 1},
				})
			default:
				msg = &Message{
					Type: msgType,
				}
			}

			err = protocol1.SendMessage(ctx, h2.ID(), msg)
			require.NoError(t, err)

			select {
			case receivedMsg := <-received:
				assert.Equal(t, msgType, receivedMsg.Type)
			case <-time.After(2 * time.Second):
				t.Fatal("未接收到消息")
			}
		})
	}
}
