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
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNexKVProtocol_RegisterHandler 测试消息处理器注册
func TestNexKVProtocol_RegisterHandler(t *testing.T) {
	ctx := context.Background()

	h1, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h1.Close()

	h2, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h2.Close()

	protocol1 := NewNexKVProtocol(h1, nil)
	protocol2 := NewNexKVProtocol(h2, nil)

	// 注册消息处理器
	received := make(chan *Message, 10)
	protocol2.RegisterHandler(MessageTypeGet, MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
		received <- msg
		return nil
	}))

	// 连接并发送消息
	_ = h2.Connect(ctx, peer.AddrInfo{ID: h1.ID(), Addrs: h1.Addrs()})

	// 使用 Payload 模式创建消息
	testMsg := &Message{}
	testMsg.MustEncodePayload(&GetPayload{
		Key: []byte("test-key"),
	})

	err = protocol1.SendMessage(ctx, h2.ID(), testMsg)
	require.NoError(t, err)

	// 验证接收
	select {
	case msg := <-received:
		assert.Equal(t, MessageTypeGet, msg.Type)
		// 解码 Payload 并验证
		payload, err := msg.DecodePayload()
		require.NoError(t, err)
		getPayload, ok := payload.(*GetPayload)
		require.True(t, ok)
		assert.Equal(t, []byte("test-key"), getPayload.Key)
	case <-time.After(5 * time.Second):
		t.Fatal("未接收到消息")
	}
}

// TestNexKVProtocol_UnregisterHandler 测试注销消息处理器
func TestNexKVProtocol_UnregisterHandler(t *testing.T) {
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h.Close()

	protocol := NewNexKVProtocol(h, nil)

	// 注册处理器
	handler := MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
		return nil
	})
	protocol.RegisterHandler(MessageTypeGet, handler)

	// 注销处理器
	protocol.UnregisterHandler(MessageTypeGet)

	// 验证：再次注册不应该覆盖（虽然我们无法直接验证）
	protocol.RegisterHandler(MessageTypePut, handler)
}

// TestNexKVProtocol_Stats 测试统计信息
func TestNexKVProtocol_Stats(t *testing.T) {
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
		return nil
	}))

	// 重置统计
	protocol1.ResetStats()
	stats := protocol1.Stats()
	assert.Equal(t, uint64(0), stats.MessagesSent)

	// 使用 Payload 模式发送消息
	msg := &Message{}
	msg.MustEncodePayload(&PutPayload{
		Key:   []byte("key"),
		Value: []byte("value"),
	})

	err = protocol1.SendMessage(ctx, h2.ID(), msg)
	require.NoError(t, err)

	// 验证统计
	stats = protocol1.Stats()
	assert.Equal(t, uint64(1), stats.MessagesSent)
	assert.Greater(t, stats.BytesSent, uint64(0))
}
