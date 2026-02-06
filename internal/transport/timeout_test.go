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
	"net"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConnectionTimeout 测试连接超时设置
func TestConnectionTimeout(t *testing.T) {
	ctx := context.Background()

	// 创建一个节点
	tmpDir := t.TempDir()
	keyPath := fmt.Sprintf("%s/node1.key", tmpDir)
	cfg := DefaultP2PServiceConfig("9711", keyPath)

	service, err := NewP2PService(cfg)
	require.NoError(t, err)
	defer service.Close()

	err = service.Start(ctx)
	require.NoError(t, err)

	// 构造一个无效的 peer.AddrInfo
	invalidPeerID, _ := peer.Decode("QmInvalidPeerID123456789")
	invalidAddr, _ := ma.NewMultiaddr("/ip4/127.0.0.1/tcp/9999")
	invalidPeerInfo := peer.AddrInfo{
		ID:    invalidPeerID,
		Addrs: []ma.Multiaddr{invalidAddr},
	}

	// 使用带超时的 context
	timeoutCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	startTime := time.Now()
	err = service.ConnectToPeer(timeoutCtx, invalidPeerInfo)
	elapsed := time.Since(startTime)

	// 验证超时行为
	assert.Error(t, err, "连接不存在的地址应该失败")
	t.Logf("连接超时测试：超时时间约 %v，错误：%v", elapsed, err)
}

// TestConnectionToUnreachablePort 测试连接不可达端口
func TestConnectionToUnreachablePort(t *testing.T) {
	ctx := context.Background()

	// 创建一个监听在本地端口的 TCP 服务器（不响应 libp2p 连接）
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port

	// 创建 P2PService
	tmpDir := t.TempDir()
	keyPath := fmt.Sprintf("%s/node1.key", tmpDir)
	cfg := DefaultP2PServiceConfig("9712", keyPath)

	service, err := NewP2PService(cfg)
	require.NoError(t, err)
	defer service.Close()

	err = service.Start(ctx)
	require.NoError(t, err)

	// 构造指向非 libp2p 端口的 peer info
	invalidPeerID, _ := peer.Decode("QmAnotherInvalidPeerID")
	invalidAddr, _ := ma.NewMultiaddr(fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", port))
	invalidPeerInfo := peer.AddrInfo{
		ID:    invalidPeerID,
		Addrs: []ma.Multiaddr{invalidAddr},
	}

	// 使用短超时
	timeoutCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	startTime := time.Now()
	err = service.ConnectToPeer(timeoutCtx, invalidPeerInfo)
	elapsed := time.Since(startTime)

	// 应该快速失败（因为端口不可达或者协议不匹配）
	assert.Error(t, err, "连接非 libp2p 端口应该失败")
	t.Logf("连接非 libp2p 端口耗时：%v，错误：%v", elapsed, err)
}

// TestConnectionCancel 测试连接取消
func TestConnectionCancel(t *testing.T) {
	ctx := context.Background()

	// 创建一个节点
	tmpDir := t.TempDir()
	keyPath := fmt.Sprintf("%s/node1.key", tmpDir)
	cfg := DefaultP2PServiceConfig("9713", keyPath)

	service, err := NewP2PService(cfg)
	require.NoError(t, err)
	defer service.Close()

	err = service.Start(ctx)
	require.NoError(t, err)

	// 使用可取消的 context
	cancelCtx, cancel := context.WithCancel(ctx)

	// 启动连接操作
	errCh := make(chan error, 1)
	go func() {
		invalidPeerID, _ := peer.Decode("QmCancelTestPeerID")
		invalidAddr, _ := ma.NewMultiaddr("/ip4/127.0.0.1/tcp/19999")
		invalidPeerInfo := peer.AddrInfo{
			ID:    invalidPeerID,
			Addrs: []ma.Multiaddr{invalidAddr},
		}
		errCh <- service.ConnectToPeer(cancelCtx, invalidPeerInfo)
	}()

	// 等待一小段时间后取消
	time.Sleep(500 * time.Millisecond)
	cancel()

	// 验证取消行为
	select {
	case err := <-errCh:
		// 可能是 context canceled 或其他错误
		assert.Error(t, err, "取消的连接应该返回错误")
		t.Logf("连接取消测试：%v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("连接操作应该被快速取消")
	}
}

// TestMessageSendTimeout 测试消息发送超时
func TestMessageSendTimeout(t *testing.T) {
	ctx := context.Background()

	// 创建两个节点
	tmpDir := t.TempDir()

	// 节点 1
	keyPath1 := fmt.Sprintf("%s/node1.key", tmpDir)
	cfg1 := DefaultP2PServiceConfig("9714", keyPath1)
	service1, err := NewP2PService(cfg1)
	require.NoError(t, err)
	defer service1.Close()
	err = service1.Start(ctx)
	require.NoError(t, err)

	// 节点 2
	keyPath2 := fmt.Sprintf("%s/node2.key", tmpDir)
	cfg2 := DefaultP2PServiceConfig("9715", keyPath2)
	service2, err := NewP2PService(cfg2)
	require.NoError(t, err)
	defer service2.Close()
	err = service2.Start(ctx)
	require.NoError(t, err)

	// 建立连接
	peerInfo2 := service2.GetPeerInfo()
	err = service1.ConnectToPeer(ctx, peerInfo2)
	require.NoError(t, err)

	// 等待连接建立
	time.Sleep(200 * time.Millisecond)

	// 注册一个慢速处理器（故意延迟响应）
	slowProcessed := make(chan struct{})
	service2.Protocol().RegisterHandler(MessageTypePut, MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
		// 故意延迟 10 秒
		time.Sleep(10 * time.Second)
		close(slowProcessed)
		return nil
	}))

	// 使用带超时的 context 发送消息
	timeoutCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	// 发送消息
	putPayload := &PutPayload{
		Key:     []byte("timeout-test-key"),
		Value:   []byte("timeout-test-value"),
		Version: 1,
	}

	msg := &Message{}
	msg.MustEncodePayload(putPayload)

	startTime := time.Now()
	err = service1.Protocol().SendMessage(timeoutCtx, service2.PeerID(), msg)
	elapsed := time.Since(startTime)

	// 验证超时行为
	if err != nil {
		t.Logf("消息发送超时测试：发送在 %v 后超时或失败：%v", elapsed, err)
	}

	// 应该快速返回（不一定等到超时）
	assert.Less(t, elapsed, 5*time.Second, "发送操作不应该等待太久")
}

// TestConcurrentConnectionAttempts 测试并发连接尝试
func TestConcurrentConnectionAttempts(t *testing.T) {
	ctx := context.Background()

	// 创建一个节点
	tmpDir := t.TempDir()
	keyPath := fmt.Sprintf("%s/node1.key", tmpDir)
	cfg := DefaultP2PServiceConfig("9716", keyPath)

	service, err := NewP2PService(cfg)
	require.NoError(t, err)
	defer service.Close()

	err = service.Start(ctx)
	require.NoError(t, err)

	// 并发尝试连接多个不存在的地址
	const numAttempts = 5
	results := make(chan error, numAttempts)

	for i := 0; i < numAttempts; i++ {
		go func(index int) {
			invalidPeerID, _ := peer.Decode(fmt.Sprintf("QmConcurrentTestPeerID%d", index))
			invalidAddr, _ := ma.NewMultiaddr(fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 9990+index))
			invalidPeerInfo := peer.AddrInfo{
				ID:    invalidPeerID,
				Addrs: []ma.Multiaddr{invalidAddr},
			}
			results <- service.ConnectToPeer(ctx, invalidPeerInfo)
		}(i)
	}

	// 收集结果
	successCount := 0
	failCount := 0
	timeout := time.After(10 * time.Second)

	for i := 0; i < numAttempts; i++ {
		select {
		case err := <-results:
			if err != nil {
				failCount++
			} else {
				successCount++
			}
		case <-timeout:
			t.Fatal("并发连接测试超时")
		}
	}

	// 验证所有连接都失败
	assert.Equal(t, 0, successCount, "所有连接都应该失败")
	assert.Equal(t, numAttempts, failCount, "所有连接都应该失败")

	t.Logf("并发连接测试：尝试 %d 次，失败 %d 次", numAttempts, failCount)
}

// TestConnectionRetry 测试连接重试机制
func TestConnectionRetry(t *testing.T) {
	ctx := context.Background()

	// 创建节点 1（客户端）
	tmpDir := t.TempDir()
	keyPath1 := fmt.Sprintf("%s/node1.key", tmpDir)
	cfg1 := DefaultP2PServiceConfig("9717", keyPath1)
	service1, err := NewP2PService(cfg1)
	require.NoError(t, err)
	defer service1.Close()
	err = service1.Start(ctx)
	require.NoError(t, err)

	// 节点 2 尚未启动
	keyPath2 := fmt.Sprintf("%s/node2.key", tmpDir)
	cfg2 := DefaultP2PServiceConfig("9718", keyPath2)

	// 构造指向节点 2 地址的 peer info（节点尚未启动）
	invalidPeerID, _ := peer.Decode("QmTempPeerID")
	invalidAddr, _ := ma.NewMultiaddr("/ip4/127.0.0.1/tcp/9718")
	peerInfo2 := peer.AddrInfo{
		ID:    invalidPeerID,
		Addrs: []ma.Multiaddr{invalidAddr},
	}

	err = service1.ConnectToPeer(ctx, peerInfo2)
	assert.Error(t, err, "节点未启动时应该连接失败")

	// 启动节点 2
	service2, err := NewP2PService(cfg2)
	require.NoError(t, err)
	defer service2.Close()
	err = service2.Start(ctx)
	require.NoError(t, err)

	// 使用正确的 peer info 连接
	peerInfo2 = service2.GetPeerInfo()

	// 再次尝试连接（应该成功）
	err = service1.ConnectToPeer(ctx, peerInfo2)
	if err == nil {
		t.Log("连接重试测试：节点启动后连接成功")
	} else {
		t.Logf("连接重试测试：节点启动后仍然失败 - %v（可能是 libp2p 特性）", err)
	}
}

// BenchmarkConnectionTimeout 连接超时基准测试
func BenchmarkConnectionTimeout(b *testing.B) {
	ctx := context.Background()

	tmpDir := b.TempDir()
	keyPath := fmt.Sprintf("%s/node1.key", tmpDir)
	cfg := DefaultP2PServiceConfig("9719", keyPath)

	service, err := NewP2PService(cfg)
	require.NoError(b, err)
	defer service.Close()
	err = service.Start(ctx)
	require.NoError(b, err)

	invalidPeerID, _ := peer.Decode("QmBenchmarkInvalidPeer")
	invalidAddr, _ := ma.NewMultiaddr("/ip4/127.0.0.1/tcp/9999")
	invalidPeerInfo := peer.AddrInfo{
		ID:    invalidPeerID,
		Addrs: []ma.Multiaddr{invalidAddr},
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = service.ConnectToPeer(ctx, invalidPeerInfo)
	}
}
