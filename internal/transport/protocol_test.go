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
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNexKVProtocol_New 测试创建协议处理器
func TestNexKVProtocol_New(t *testing.T) {
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h.Close()

	codec := NewMessagePackCodec()
	protocol := NewNexKVProtocol(h, codec)

	assert.NotNil(t, protocol)
	assert.Equal(t, h, protocol.Host())
	assert.Equal(t, h.ID(), protocol.PeerID())
}

// TestNexKVProtocol_NewNilCodec 测试使用 nil codec 创建
func TestNexKVProtocol_NewNilCodec(t *testing.T) {
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h.Close()

	protocol := NewNexKVProtocol(h, nil)
	assert.NotNil(t, protocol)
}

// TestNexKVProtocol_SendMessage_Success 测试消息发送成功
func TestNexKVProtocol_SendMessage_Success(t *testing.T) {
	ctx := context.Background()

	// 创建两个 libp2p 节点
	h1, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h1.Close()

	h2, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h2.Close()

	// 连接两个节点
	err = h1.Connect(ctx, peer.AddrInfo{ID: h2.ID(), Addrs: h2.Addrs()})
	require.NoError(t, err)

	// 创建协议处理器
	codec := NewMessagePackCodec()
	protocol1 := NewNexKVProtocol(h1, codec)
	protocol2 := NewNexKVProtocol(h2, codec)

	// 注册消息处理器（接收方）
	received := make(chan *Message, 10)
	protocol2.RegisterHandler(MessageTypePut, MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
		received <- msg
		return nil
	}))

	// 发送消息（使用 Payload 模式）
	msg := &Message{}
	msg.MustEncodePayload(&PutPayload{
		Key:   []byte("test-key"),
		Value: []byte("test-value"),
	})

	err = protocol1.SendMessage(ctx, h2.ID(), msg)
	assert.NoError(t, err)

	// 验证接收
	select {
	case receivedMsg := <-received:
		assert.Equal(t, MessageTypePut, receivedMsg.Type)
		// 解码 Payload 并验证
		payload, err := receivedMsg.DecodePayload()
		require.NoError(t, err)
		putPayload, ok := payload.(*PutPayload)
		require.True(t, ok)
		assert.Equal(t, []byte("test-key"), putPayload.Key)
		assert.Equal(t, []byte("test-value"), putPayload.Value)
	case <-time.After(5 * time.Second):
		t.Fatal("未接收到消息")
	}

	// 验证统计
	stats1 := protocol1.Stats()
	assert.Greater(t, stats1.MessagesSent, uint64(0))
}

// TestNexKVProtocol_SendMessage_UnknownPeer 测试发送给未知 peer
func TestNexKVProtocol_SendMessage_UnknownPeer(t *testing.T) {
	ctx := context.Background()

	h1, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h1.Close()

	protocol := NewNexKVProtocol(h1, nil)

	// 使用 Payload 模式创建消息
	msg := &Message{}
	msg.MustEncodePayload(&GetPayload{
		Key: []byte("key"),
	})

	// 生成一个随机的 peer ID
	unknownPeerID, _ := peer.Decode("QmInvalidPeerID123456789")

	err = protocol.SendMessage(ctx, unknownPeerID, msg)
	assert.Error(t, err)
}

// TestNexKVProtocol_SendMessage_InvalidMessage 测试发送无效消息
func TestNexKVProtocol_SendMessage_InvalidMessage(t *testing.T) {
	ctx := context.Background()

	h1, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h1.Close()

	h2, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h2.Close()

	protocol := NewNexKVProtocol(h1, nil)

	// 无效消息（GET 没有Payload）
	msg := &Message{
		Type: MessageTypeGet,
	}

	err = protocol.SendMessage(ctx, h2.ID(), msg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "无效消息")
}

// TestNexKVProtocol_BroadcastMessage 测试广播消息
func TestNexKVProtocol_BroadcastMessage(t *testing.T) {
	ctx := context.Background()

	// 创建 3 个节点
	hosts := make([]host.Host, 3)
	for i := range hosts {
		h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
		require.NoError(t, err)
		defer h.Close()
		hosts[i] = h
	}

	// 连接网络拓扑: h1 -> h2, h1 -> h3
	err := hosts[0].Connect(ctx, peer.AddrInfo{ID: hosts[1].ID(), Addrs: hosts[1].Addrs()})
	require.NoError(t, err)
	err = hosts[0].Connect(ctx, peer.AddrInfo{ID: hosts[2].ID(), Addrs: hosts[2].Addrs()})
	require.NoError(t, err)

	// 创建协议处理器
	protocol := NewNexKVProtocol(hosts[0], nil)

	// 注册接收方处理器
	for i := 1; i < 3; i++ {
		p := NewNexKVProtocol(hosts[i], nil)
		p.RegisterHandler(MessageTypeSync, MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
			return nil
		}))
	}

	peers := []peer.ID{hosts[1].ID(), hosts[2].ID()}

	// 使用 Payload 模式创建消息
	msg := &Message{}
	msg.MustEncodePayload(&TwoPCPreparePayload{
		TxID: "test-sync-tx",
	})

	err = protocol.BroadcastMessage(ctx, peers, msg)
	assert.NoError(t, err)

	// 验证发送计数
	stats := protocol.Stats()
	assert.GreaterOrEqual(t, stats.MessagesSent, uint64(2))
}

// TestNexKVProtocol_Close 测试关闭协议处理器
func TestNexKVProtocol_Close(t *testing.T) {
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h.Close()

	protocol := NewNexKVProtocol(h, nil)

	// 注册处理器
	protocol.RegisterHandler(MessageTypeGet, MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
		return nil
	}))

	// 关闭协议
	err = protocol.Close()
	assert.NoError(t, err)
}
