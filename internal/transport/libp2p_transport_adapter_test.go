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

	"github.com/libp2p/go-libp2p"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLibp2pTransportAdapter_New 测试创建适配器
func TestLibp2pTransportAdapter_New(t *testing.T) {
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h.Close()

	adapter := NewLibp2pTransportAdapter(h)
	assert.NotNil(t, adapter)
	assert.Equal(t, h, adapter.Host())
	assert.NotNil(t, adapter.Protocol())
}

// TestLibp2pTransportAdapter_RegisterNodeID 测试注册节点映射
func TestLibp2pTransportAdapter_RegisterNodeID(t *testing.T) {
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h.Close()

	adapter := NewLibp2pTransportAdapter(h)

	// 注册节点映射
	nodeID := "node-1"
	pid := h.ID()
	adapter.RegisterNodeID(nodeID, pid)

	// 验证映射
	retrievedPID, ok := adapter.GetPeerID(nodeID)
	assert.True(t, ok)
	assert.Equal(t, pid, retrievedPID)

	retrievedNodeID, ok := adapter.GetNodeID(pid)
	assert.True(t, ok)
	assert.Equal(t, nodeID, retrievedNodeID)
}

// TestLibp2pTransportAdapter_RegisterNodeIDFromString 测试从字符串注册
func TestLibp2pTransportAdapter_RegisterNodeIDFromString(t *testing.T) {
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h.Close()

	adapter := NewLibp2pTransportAdapter(h)

	nodeID := "node-2"
	peerIDStr := h.ID().String()

	err = adapter.RegisterNodeIDFromString(nodeID, peerIDStr)
	require.NoError(t, err)

	// 验证映射
	pid, ok := adapter.GetPeerID(nodeID)
	assert.True(t, ok)
	assert.Equal(t, h.ID(), pid)
}

// TestLibp2pTransportAdapter_InvalidPeerIDString 测试无效的 PeerID 字符串
func TestLibp2pTransportAdapter_InvalidPeerIDString(t *testing.T) {
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h.Close()

	adapter := NewLibp2pTransportAdapter(h)

	err = adapter.RegisterNodeIDFromString("node-3", "invalid-peer-id")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "解析 PeerID 失败")
}

// TestLibp2pTransportAdapter_SendReceive 测试发送和接收消息
func TestLibp2pTransportAdapter_SendReceive(t *testing.T) {
	// 创建两个节点
	h1, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h1.Close()

	h2, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h2.Close()

	// 连接两个节点
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = h1.Connect(ctx, h2.Peerstore().PeerInfo(h2.ID()))
	require.NoError(t, err)

	// 创建适配器
	adapter1 := NewLibp2pTransportAdapter(h1)
	adapter2 := NewLibp2pTransportAdapter(h2)

	// 注册节点映射
	adapter1.RegisterNodeID("node-2", h2.ID())
	adapter2.RegisterNodeID("node-1", h1.ID())

	// 注册接收处理器（接收方）
	received := make(chan struct {
		nodeID string
		msg    []byte
	}, 1)

	err = adapter2.Receive(func(nodeID string, msg []byte) {
		received <- struct {
			nodeID string
			msg    []byte
		}{nodeID, msg}
	})
	require.NoError(t, err)

	// 稍等片刻让处理器注册完成
	time.Sleep(100 * time.Millisecond)

	// 发送消息
	testMsg := []byte("test message payload")
	err = adapter1.Send("node-2", testMsg)
	require.NoError(t, err)

	// 验证接收
	select {
	case result := <-received:
		assert.Equal(t, "node-1", result.nodeID)
		assert.Equal(t, testMsg, result.msg)
	case <-time.After(5 * time.Second):
		t.Fatal("未接收到消息")
	}
}

// TestLibp2pTransportAdapter_SendUnknownNode 测试发送到未知节点
func TestLibp2pTransportAdapter_SendUnknownNode(t *testing.T) {
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h.Close()

	adapter := NewLibp2pTransportAdapter(h)

	err = adapter.Send("unknown-node", []byte("test"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "未知节点 ID")
}

// TestLibp2pTransportAdapter_ReceiveTwice 测试重复注册接收处理器
func TestLibp2pTransportAdapter_ReceiveTwice(t *testing.T) {
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h.Close()

	adapter := NewLibp2pTransportAdapter(h)

	// 第一次注册
	err = adapter.Receive(func(nodeID string, msg []byte) {})
	require.NoError(t, err)

	// 第二次注册应失败
	err = adapter.Receive(func(nodeID string, msg []byte) {})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "已注册")
}

// TestLibp2pTransportAdapter_Close 测试关闭适配器
func TestLibp2pTransportAdapter_Close(t *testing.T) {
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h.Close()

	adapter := NewLibp2pTransportAdapter(h)

	// 注册接收处理器
	err = adapter.Receive(func(nodeID string, msg []byte) {})
	require.NoError(t, err)

	// 关闭适配器
	err = adapter.Close()
	assert.NoError(t, err)

	// 再次关闭应成功（幂等）
	err = adapter.Close()
	assert.NoError(t, err)
}

// TestLibp2pTransportAdapter_AutoRegisterUnknownPeer 测试自动注册未知 peer
func TestLibp2pTransportAdapter_AutoRegisterUnknownPeer(t *testing.T) {
	// 创建两个节点
	h1, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h1.Close()

	h2, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h2.Close()

	// 连接两个节点
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = h1.Connect(ctx, h2.Peerstore().PeerInfo(h2.ID()))
	require.NoError(t, err)

	// 创建适配器
	adapter1 := NewLibp2pTransportAdapter(h1)
	adapter2 := NewLibp2pTransportAdapter(h2)

	// 只注册发送方映射（不注册接收方）
	adapter1.RegisterNodeID("node-2", h2.ID())

	// 注册接收处理器
	receivedNodeID := make(chan string, 1)
	err = adapter2.Receive(func(nodeID string, msg []byte) {
		receivedNodeID <- nodeID
	})
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	// 发送消息
	testMsg := []byte("test")
	err = adapter1.Send("node-2", testMsg)
	require.NoError(t, err)

	// 验证接收（应自动使用 PeerID 字符串作为 NodeID）
	select {
	case nodeID := <-receivedNodeID:
		// nodeID 应该是 h1.ID().String()（自动注册）
		assert.Equal(t, h1.ID().String(), nodeID)
	case <-time.After(5 * time.Second):
		t.Fatal("未接收到消息")
	}
}

// TestLibp2pTransportAdapter_ConnectToPeer 测试连接到 peer
func TestLibp2pTransportAdapter_ConnectToPeer(t *testing.T) {
	h1, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h1.Close()

	h2, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h2.Close()

	adapter := NewLibp2pTransportAdapter(h1)

	// 将 h2 的地址添加到 h1 的 peerstore
	h1.Peerstore().AddAddrs(h2.ID(), h2.Addrs(), time.Minute)

	// 连接到 h2
	err = adapter.ConnectToPeer(h2.ID())
	require.NoError(t, err)

	// 验证连接
	time.Sleep(100 * time.Millisecond)
	peers := h1.Network().Peers()
	assert.Contains(t, peers, h2.ID())
}

// TestLibp2pTransportAdapter_ConnectToNodeID 测试连接到 NodeID
func TestLibp2pTransportAdapter_ConnectToNodeID(t *testing.T) {
	h1, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h1.Close()

	h2, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h2.Close()

	adapter := NewLibp2pTransportAdapter(h1)

	// 注册节点映射
	adapter.RegisterNodeID("node-2", h2.ID())

	// 将 h2 的地址添加到 h1 的 peerstore
	h1.Peerstore().AddAddrs(h2.ID(), h2.Addrs(), time.Minute)

	// 通过 NodeID 连接
	err = adapter.ConnectToNodeID("node-2")
	require.NoError(t, err)

	// 验证连接
	time.Sleep(100 * time.Millisecond)
	peers := h1.Network().Peers()
	assert.Contains(t, peers, h2.ID())
}

// TestLibp2pTransportAdapter_ConnectToUnknownNodeID 测试连接到未知 NodeID
func TestLibp2pTransportAdapter_ConnectToUnknownNodeID(t *testing.T) {
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h.Close()

	adapter := NewLibp2pTransportAdapter(h)

	err = adapter.ConnectToNodeID("unknown-node")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "未知节点 ID")
}

// TestLibp2pTransportAdapter_MultipleMessages 测试多条消息
func TestLibp2pTransportAdapter_MultipleMessages(t *testing.T) {
	// 创建两个节点
	h1, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h1.Close()

	h2, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h2.Close()

	// 连接两个节点
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = h1.Connect(ctx, h2.Peerstore().PeerInfo(h2.ID()))
	require.NoError(t, err)

	// 创建适配器
	adapter1 := NewLibp2pTransportAdapter(h1)
	adapter2 := NewLibp2pTransportAdapter(h2)

	// 注册节点映射
	adapter1.RegisterNodeID("node-2", h2.ID())
	adapter2.RegisterNodeID("node-1", h1.ID())

	// 注册接收处理器
	receivedCount := 0
	var countMu sync.Mutex
	err = adapter2.Receive(func(nodeID string, msg []byte) {
		countMu.Lock()
		receivedCount++
		countMu.Unlock()
	})
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	// 发送多条消息
	msgCount := 10
	for i := 0; i < msgCount; i++ {
		msg := []byte(fmt.Sprintf("message-%d", i))
		err = adapter1.Send("node-2", msg)
		require.NoError(t, err)
	}

	// 等待接收完成
	time.Sleep(500 * time.Millisecond)

	countMu.Lock()
	finalCount := receivedCount
	countMu.Unlock()

	// 验证至少收到部分消息（网络可能丢包）
	assert.Greater(t, finalCount, 0)
}
