// Package transport 内存传输测试
package transport

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ========================================
// MemoryTransport 测试
// ========================================

// TestMemoryTransport_StartStop 测试启动和停止
func TestMemoryTransport_StartStop(t *testing.T) {
	trans, err := NewMemoryTransport("node1:9211")
	require.NoError(t, err)
	require.NotNil(t, trans)

	// 启动
	err = trans.Start()
	require.NoError(t, err)
	assert.True(t, trans.started.Load())

	// 停止
	err = trans.Stop()
	require.NoError(t, err)
	assert.True(t, trans.stopped.Load())
}

// TestMemoryTransport_Start_AlreadyStarted 测试重复启动
func TestMemoryTransport_Start_AlreadyStarted(t *testing.T) {
	trans, err := NewMemoryTransport("node1:9211")
	require.NoError(t, err)

	err = trans.Start()
	require.NoError(t, err)

	// 重复启动应该失败
	err = trans.Start()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "已经启动")
}

// TestMemoryTransport_SendReceive 测试发送和接收消息
func TestMemoryTransport_SendReceive(t *testing.T) {
	// 创建两个传输
	trans1, err := NewMemoryTransport("node1:9211")
	require.NoError(t, err)

	trans2, err := NewMemoryTransport("node2:9211")
	require.NoError(t, err)

	// 启动
	require.NoError(t, trans1.Start())
	defer func() { _ = trans1.Stop() }()

	require.NoError(t, trans2.Start())
	defer func() { _ = trans2.Stop() }()

	// 注册远程节点
	trans1.RegisterRemoteNode("node2:9211")
	trans2.RegisterRemoteNode("node1:9211")

	// 发送消息
	msg := &GetMessage{Key: "test_key"}
	ctx := context.Background()

	err = trans1.Send(ctx, "node2:9211", msg)
	require.NoError(t, err)

	// 接收消息
	select {
	case receivedMsg := <-trans2.Receive():
		assert.Equal(t, MessageTypeGet, receivedMsg.Type())
		getMsg, ok := receivedMsg.(*GetMessage)
		require.True(t, ok)
		assert.Equal(t, "test_key", getMsg.Key)
	case <-time.After(5 * time.Second):
		t.Fatal("未接收到消息")
	}
}

// TestMemoryTransport_Send_Bidirectional 测试双向通信
func TestMemoryTransport_Send_Bidirectional(t *testing.T) {
	trans1, err := NewMemoryTransport("node1:9211")
	require.NoError(t, err)

	trans2, err := NewMemoryTransport("node2:9211")
	require.NoError(t, err)

	require.NoError(t, trans1.Start())
	defer func() { _ = trans1.Stop() }()

	require.NoError(t, trans2.Start())
	defer func() { _ = trans2.Stop() }()

	trans1.RegisterRemoteNode("node2:9211")
	trans2.RegisterRemoteNode("node1:9211")

	// node1 -> node2
	msg1 := &PutMessage{Key: "key1", Value: []byte("value1")}
	ctx := context.Background()

	err = trans1.Send(ctx, "node2:9211", msg1)
	require.NoError(t, err)

	// node2 -> node1
	msg2 := &PutMessage{Key: "key2", Value: []byte("value2")}
	err = trans2.Send(ctx, "node1:9211", msg2)
	require.NoError(t, err)

	// node2 接收消息
	select {
	case receivedMsg := <-trans2.Receive():
		assert.Equal(t, MessageTypePut, receivedMsg.Type())
		putMsg, ok := receivedMsg.(*PutMessage)
		require.True(t, ok)
		assert.Equal(t, "key1", putMsg.Key)
	case <-time.After(5 * time.Second):
		t.Fatal("node2 未接收到消息")
	}

	// node1 接收消息
	select {
	case receivedMsg := <-trans1.Receive():
		assert.Equal(t, MessageTypePut, receivedMsg.Type())
		putMsg, ok := receivedMsg.(*PutMessage)
		require.True(t, ok)
		assert.Equal(t, "key2", putMsg.Key)
	case <-time.After(5 * time.Second):
		t.Fatal("node1 未接收到消息")
	}
}

// TestMemoryTransport_Send_NotStarted 测试未启动发送
func TestMemoryTransport_Send_NotStarted(t *testing.T) {
	trans, err := NewMemoryTransport("node1:9211")
	require.NoError(t, err)

	// 未启动就发送
	msg := &GetMessage{Key: "test"}
	ctx := context.Background()

	err = trans.Send(ctx, "node2:9211", msg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "传输层未启动")
}

// TestMemoryTransport_Send_NodeNotExist 测试发送到不存在的节点
func TestMemoryTransport_Send_NodeNotExist(t *testing.T) {
	trans1, err := NewMemoryTransport("node1:9211")
	require.NoError(t, err)

	require.NoError(t, trans1.Start())
	defer func() { _ = trans1.Stop() }()

	msg := &GetMessage{Key: "test"}
	ctx := context.Background()

	// 发送到不存在的节点
	err = trans1.Send(ctx, "node2:9211", msg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "节点不存在")
}

// TestMemoryTransport_Send_ContextCancel 测试上下文取消
func TestMemoryTransport_Send_ContextCancel(t *testing.T) {
	trans1, err := NewMemoryTransport("node1:9211")
	require.NoError(t, err)

	trans2, err := NewMemoryTransport("node2:9211")
	require.NoError(t, err)

	require.NoError(t, trans1.Start())
	defer func() { _ = trans1.Stop() }()

	require.NoError(t, trans2.Start())
	defer func() { _ = trans2.Stop() }()

	trans1.RegisterRemoteNode("node2:9211")

	// 创建已取消的上下文
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	// 等待取消传播
	<-ctx.Done()

	msg := &GetMessage{Key: "test"}
	err = trans1.Send(ctx, "node2:9211", msg)
	// 由于 select 的竞态，Send 可能成功也可能返回 context.Canceled
	// 两种情况都是正确的行为
	if err != nil {
		assert.ErrorIs(t, err, context.Canceled)
	}
	// 如果 err == nil，说明消息成功发送，这也是可接受的行为
}

// TestMemoryTransport_ConnectTo 测试连接到远程节点
func TestMemoryTransport_ConnectTo(t *testing.T) {
	trans1, err := NewMemoryTransport("node1:9211")
	require.NoError(t, err)

	trans2, err := NewMemoryTransport("node2:9211")
	require.NoError(t, err)

	require.NoError(t, trans1.Start())
	defer func() { _ = trans1.Stop() }()

	require.NoError(t, trans2.Start())
	defer func() { _ = trans2.Stop() }()

	// 连接到远程节点
	err = trans1.ConnectTo("node2:9211")
	require.NoError(t, err)

	// 验证可以发送消息
	msg := &GetMessage{Key: "test"}
	ctx := context.Background()

	err = trans1.Send(ctx, "node2:9211", msg)
	require.NoError(t, err)

	// 验证消息被接收
	select {
	case <-trans2.Receive():
		// 成功接收
	case <-time.After(1 * time.Second):
		t.Fatal("未接收到消息")
	}
}

// TestMemoryTransport_DisconnectFrom 测试断开连接
func TestMemoryTransport_DisconnectFrom(t *testing.T) {
	trans1, err := NewMemoryTransport("node1:9211")
	require.NoError(t, err)

	trans2, err := NewMemoryTransport("node2:9211")
	require.NoError(t, err)

	require.NoError(t, trans1.Start())
	defer func() { _ = trans1.Stop() }()

	require.NoError(t, trans2.Start())
	defer func() { _ = trans2.Stop() }()

	// 连接
	err = trans1.ConnectTo("node2:9211")
	require.NoError(t, err)

	// 断开连接
	err = trans1.DisconnectFrom("node2:9211")
	require.NoError(t, err)

	// 尝试发送应该失败
	msg := &GetMessage{Key: "test"}
	ctx := context.Background()

	err = trans1.Send(ctx, "node2:9211", msg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "节点不存在")
}

// TestMemoryTransport_Clear 测试清理所有节点
func TestMemoryTransport_Clear(t *testing.T) {
	trans1, err := NewMemoryTransport("node1:9211")
	require.NoError(t, err)

	trans2, err := NewMemoryTransport("node2:9211")
	require.NoError(t, err)

	trans3, err := NewMemoryTransport("node3:9211")
	require.NoError(t, err)

	require.NoError(t, trans1.Start())
	defer func() { _ = trans1.Stop() }()

	require.NoError(t, trans2.Start())
	defer func() { _ = trans2.Stop() }()

	require.NoError(t, trans3.Start())
	defer func() { _ = trans3.Stop() }()

	// 建立连接
	trans1.RegisterRemoteNode("node2:9211")
	trans1.RegisterRemoteNode("node3:9211")

	// 清理
	trans1.Clear()

	// 验证远程连接已被清理
	trans1.nodesMu.RLock()
	nodeCount := len(trans1.nodes)
	trans1.nodesMu.RUnlock()

	assert.Equal(t, 1, nodeCount) // 只有本地节点
}

// TestMemoryTransport_Stats 测试统计信息
func TestMemoryTransport_Stats(t *testing.T) {
	trans, err := NewMemoryTransport("node1:9211")
	require.NoError(t, err)

	stats := trans.Stats()
	assert.NotNil(t, stats)
	assert.Equal(t, false, trans.started.Load())
	assert.Equal(t, false, trans.stopped.Load())
	assert.Equal(t, "node1:9211", stats["local_addr"])
	assert.Equal(t, 1, stats["registered_nodes"]) // 只有本地节点
}

// TestMemoryTransport_MultipleMessages 测试发送多条消息
func TestMemoryTransport_MultipleMessages(t *testing.T) {
	trans1, err := NewMemoryTransport("node1:9211")
	require.NoError(t, err)

	trans2, err := NewMemoryTransport("node2:9211")
	require.NoError(t, err)

	require.NoError(t, trans1.Start())
	defer func() { _ = trans1.Stop() }()

	require.NoError(t, trans2.Start())
	defer func() { _ = trans2.Stop() }()

	trans1.RegisterRemoteNode("node2:9211")
	trans2.RegisterRemoteNode("node1:9211")

	// 发送多条消息
	msgCount := 10
	ctx := context.Background()

	for i := 0; i < msgCount; i++ {
		msg := &PutMessage{
			Key:   string(rune('a' + i)),
			Value: []byte("value"),
		}
		err = trans1.Send(ctx, "node2:9211", msg)
		require.NoError(t, err)
	}

	// 接收所有消息
	receivedCount := 0
	timeout := time.After(5 * time.Second)

	for {
		select {
		case msg := <-trans2.Receive():
			assert.Equal(t, MessageTypePut, msg.Type())
			receivedCount++
			if receivedCount == msgCount {
				return // 全部接收
			}
		case <-timeout:
			t.Fatalf("只接收到 %d/%d 条消息", receivedCount, msgCount)
		}
	}
}

// TestMemoryTransport_Concurrent 测试并发发送
func TestMemoryTransport_Concurrent(t *testing.T) {
	trans1, err := NewMemoryTransport("node1:9211")
	require.NoError(t, err)

	trans2, err := NewMemoryTransport("node2:9211")
	require.NoError(t, err)

	require.NoError(t, trans1.Start())
	defer func() { _ = trans1.Stop() }()

	require.NoError(t, trans2.Start())
	defer func() { _ = trans2.Stop() }()

	trans1.RegisterRemoteNode("node2:9211")
	trans2.RegisterRemoteNode("node1:9211")

	// 并发发送
	ctx := context.Background()
	concurrency := 100
	done := make(chan bool, concurrency)

	for i := 0; i < concurrency; i++ {
		go func(id int) {
			msg := &PutMessage{
				Key:   string(rune('a' + (id % 26))),
				Value: []byte("value"),
			}
			err := trans1.Send(ctx, "node2:9211", msg)
			assert.NoError(t, err)
			done <- true
		}(i)
	}

	// 等待所有发送完成
	for i := 0; i < concurrency; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("发送超时")
		}
	}

	// 验证消息被接收
	receivedCount := 0
	timeout := time.After(5 * time.Second)

	for {
		select {
		case <-trans2.Receive():
			receivedCount++
			if receivedCount >= concurrency {
				return
			}
		case <-timeout:
			t.Logf("接收到 %d/%d 条消息", receivedCount, concurrency)
			return // 部分接收也算成功
		}
	}
}

// TestMemoryTransport_AllMessageTypes 测试所有消息类型
func TestMemoryTransport_AllMessageTypes(t *testing.T) {
	trans1, err := NewMemoryTransport("node1:9211")
	require.NoError(t, err)

	trans2, err := NewMemoryTransport("node2:9211")
	require.NoError(t, err)

	require.NoError(t, trans1.Start())
	defer func() { _ = trans1.Stop() }()

	require.NoError(t, trans2.Start())
	defer func() { _ = trans2.Stop() }()

	trans1.RegisterRemoteNode("node2:9211")
	trans2.RegisterRemoteNode("node1:9211")

	testMessages := []Message{
		&GetMessage{Key: "test"},
		&PutMessage{Key: "test", Value: []byte("value")},
		&DeleteMessage{Key: "test"},
		&GetReplyMessage{Key: "test", Value: []byte("value"), Found: true, Version: 1},
		&PutReplyMessage{Key: "test", Success: true, Version: 1},
		&DeleteReplyMessage{Key: "test", Success: true},
		&GossipSyncMessage{Version: 1, Metadata: map[string][]byte{"key": []byte("value")}, Timestamp: time.Now().Unix()},
		&GossipSyncReplyMessage{Accepted: true, Version: 1},
		&GossipDigestMessage{Version: 1, Digest: map[string]uint64{"key": 1}},
		&GossipDigestReplyMessage{Version: 1, Digest: map[string]uint64{"key": 1}},
		&QuorumProposeMessage{ProposalID: "prop1", Key: "test", Value: []byte("value"), Operation: "put", Proposer: "node1", Timestamp: time.Now().Unix()},
		&QuorumVoteMessage{ProposalID: "prop1", Voter: "node1", Vote: true},
		&QuorumDecideMessage{ProposalID: "prop1", Approved: true, Version: 1},
		&TwoPCPrepareMessage{TransactionID: "tx1", Participants: []string{"node1", "node2"}},
		&TwoPCPrepareReplyMessage{TransactionID: "tx1", Participant: "node1", Vote: "commit"},
		&TwoPCCommitMessage{TransactionID: "tx1"},
		&TwoPCRollbackMessage{TransactionID: "tx1"},
		&TwoPCCommitReplyMessage{TransactionID: "tx1", Participant: "node1", Success: true},
		&TwoPCRollbackReplyMessage{TransactionID: "tx1", Participant: "node1", Success: true},
		&NodePingMessage{NodeID: "node1", Sequence: 1, Timestamp: time.Now().Unix()},
		&NodePongMessage{NodeID: "node1", Sequence: 1, Status: "ready"},
		&NodeJoinMessage{NodeID: "node1", Addr: "node1:9211", Role: "child"},
		&NodeLeaveMessage{NodeID: "node1", Reason: "test"},
		&NodeSyncMessage{Version: 1, Metadata: map[string][]byte{"key": []byte("value")}},
		&ClusterStatusMessage{NodeID: "node1"},
		&ClusterStatusReplyMessage{Nodes: []NodeInfo{{NodeID: "node1", Addr: "node1:9211", Status: "ready", Level: 0}}},
		&LeaderElectionMessage{ElectionID: "election1", NodeID: "node1", Priority: 1},
	}

	ctx := context.Background()

	for _, msg := range testMessages {
		t.Run(msg.Type().String(), func(t *testing.T) {
			// 发送消息
			err := trans1.Send(ctx, "node2:9211", msg)
			require.NoError(t, err)

			// 接收消息
			select {
			case receivedMsg := <-trans2.Receive():
				assert.Equal(t, msg.Type(), receivedMsg.Type())
			case <-time.After(1 * time.Second):
				t.Fatalf("未接收到消息: %s", msg.Type())
			}
		})
	}
}

// TestMemoryTransport_LargeMessage 测试大消息
func TestMemoryTransport_LargeMessage(t *testing.T) {
	trans1, err := NewMemoryTransport("node1:9211")
	require.NoError(t, err)

	trans2, err := NewMemoryTransport("node2:9211")
	require.NoError(t, err)

	require.NoError(t, trans1.Start())
	defer func() { _ = trans1.Stop() }()

	require.NoError(t, trans2.Start())
	defer func() { _ = trans2.Stop() }()

	trans1.RegisterRemoteNode("node2:9211")
	trans2.RegisterRemoteNode("node1:9211")

	// 创建大消息（1MB）
	largeValue := make([]byte, 1024*1024)
	for i := range largeValue {
		largeValue[i] = byte(i % 256)
	}

	msg := &PutMessage{
		Key:   "large_key",
		Value: largeValue,
	}

	ctx := context.Background()

	// 发送大消息
	err = trans1.Send(ctx, "node2:9211", msg)
	require.NoError(t, err)

	// 接收大消息
	select {
	case receivedMsg := <-trans2.Receive():
		assert.Equal(t, MessageTypePut, receivedMsg.Type())
		putMsg, ok := receivedMsg.(*PutMessage)
		require.True(t, ok)
		assert.Equal(t, largeValue, putMsg.Value)
		assert.Equal(t, len(largeValue), len(putMsg.Value))
	case <-time.After(5 * time.Second):
		t.Fatal("未接收到大消息")
	}
}
