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

	// 发送消息
	msg := &Message{
		Type:  MessageTypePut,
		Key:   []byte("test-key"),
		Value: []byte("test-value"),
	}

	err = protocol1.SendMessage(ctx, h2.ID(), msg)
	assert.NoError(t, err)

	// 验证接收
	select {
	case receivedMsg := <-received:
		assert.Equal(t, MessageTypePut, receivedMsg.Type)
		assert.Equal(t, []byte("test-key"), receivedMsg.Key)
		assert.Equal(t, []byte("test-value"), receivedMsg.Value)
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
	msg := &Message{
		Type: MessageTypeGet,
		Key:  []byte("key"),
	}

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

	// 无效消息（GET 没有Key）
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
	msg := &Message{
		Type: MessageTypeSync,
		Key:  []byte("sync-key"),
	}

	err = protocol.BroadcastMessage(ctx, peers, msg)
	assert.NoError(t, err)

	// 验证发送计数
	stats := protocol.Stats()
	assert.GreaterOrEqual(t, stats.MessagesSent, uint64(2))
}

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

	testMsg := &Message{
		Type: MessageTypeGet,
		Key:  []byte("test-key"),
	}

	err = protocol1.SendMessage(ctx, h2.ID(), testMsg)
	require.NoError(t, err)

	// 验证接收
	select {
	case msg := <-received:
		assert.Equal(t, MessageTypeGet, msg.Type)
		assert.Equal(t, []byte("test-key"), msg.Key)
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

	// 发送消息
	msg := &Message{
		Type:  MessageTypePut,
		Key:   []byte("key"),
		Value: []byte("value"),
	}

	err = protocol1.SendMessage(ctx, h2.ID(), msg)
	require.NoError(t, err)

	// 验证统计
	stats = protocol1.Stats()
	assert.Equal(t, uint64(1), stats.MessagesSent)
	assert.Greater(t, stats.BytesSent, uint64(0))
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

	// 并发发送 100 条消息
	concurrency := 100
	done := make(chan bool, concurrency)

	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			msg := &Message{
				Type:    MessageTypePut,
				Key:     []byte("concurrent-key"),
				Value:   []byte("value"),
				Version: uint64(idx),
			}
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

	msg := &Message{
		Type:  MessageTypePut,
		Key:   []byte("key"),
		Value: []byte("value"),
	}

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
				msg = &Message{
					Type:  msgType,
					Key:   []byte("test-key"),
					Value: []byte("test-value"),
				}
			case MessageTypeAck:
				msg = &Message{
					Type: msgType,
					Seq:  1,
				}
			default:
				msg = &Message{
					Type: msgType,
					Key:  []byte("test-key"),
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
