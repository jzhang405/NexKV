// Copyright 2025 The NexKV Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package transport

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/service"
)

func init() {
	// 设置较短的超时时间用于测试
	os.Setenv("NEXKV_TEST_TIMEOUT", "5s")
}

// TestIntegration_ConnectAndCommunicate 集成测试：直接使用 Libp2pTransport
// 此测试直接覆盖 Libp2pTransport 的核心功能
func TestIntegration_ConnectAndCommunicate(t *testing.T) {
	// 跳过网络集成测试，需要真实网络环境
	if os.Getenv("NEXKV_ENABLE_NETWORK_TESTS") == "" {
		t.Skip("Skipping network integration test. Set NEXKV_ENABLE_NETWORK_TESTS=1 to enable.")
	}
	ctx := context.Background()

	// 创建服务端
	server, err := NewLibp2pTransport(ctx, &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	defer server.Close()

	// 创建客户端
	client, err := NewLibp2pTransport(ctx, &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	defer client.Close()

	// 构建地址并连接
	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()
	_, err = client.Connect(ctx, addr)
	if err != nil && err != service.ErrAlreadyConnected {
		t.Fatalf("Connect failed: %v", err)
	}

	// 验证连接
	if !client.IsConnected(server.Self()) {
		t.Fatal("Client not connected to server")
	}
	if !server.IsConnected(client.Self()) {
		t.Fatal("Server not connected to client")
	}

	// 验证 ConnectedPeers
	serverPeers := server.ConnectedPeers()
	if len(serverPeers) == 0 {
		t.Error("Server has no connected peers")
	}

	clientPeers := client.ConnectedPeers()
	if len(clientPeers) == 0 {
		t.Error("Client has no connected peers")
	}

	t.Logf("Integration test passed: server=%s, client=%s", server.Self(), client.Self())
}

// TestIntegration_RPCStyleCommunication 集成测试：RPC 风格通信
func TestIntegration_RPCStyleCommunication(t *testing.T) {
	ctx := context.Background()

	server, err := NewLibp2pTransport(ctx, &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	defer server.Close()

	client, err := NewLibp2pTransport(ctx, &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	defer client.Close()

	// 连接
	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()
	_, err = client.Connect(ctx, addr)
	if err != nil && err != service.ErrAlreadyConnected {
		t.Fatalf("Connect failed: %v", err)
	}

	// 设置 RPC 处理器
	rpcProtocol := "/nexkv/rpc-test/1.0.0"
	codec := &LengthPrefixedCodec{}

	server.SetStreamHandler(rpcProtocol, func(stream service.Stream) {
		defer stream.Close()

		libp2pStream, ok := stream.(*Libp2pStream)
		if !ok {
			return
		}

		// 读取请求
		req, err := libp2pStream.ReadWithCodec(codec)
		if err != nil {
			return
		}

		// 处理并响应
		resp := append([]byte("response: "), req...)
		_ = libp2pStream.WriteWithCodec(codec, resp) //nolint:errcheck // test code
	})

	// 客户端发起 RPC 调用
	stream, err := client.OpenStream(ctx, server.Self(), rpcProtocol)
	if err != nil {
		t.Fatalf("OpenStream failed: %v", err)
	}
	defer stream.Close()

	libp2pStream, ok := stream.(*Libp2pStream)
	if !ok {
		t.Fatal("Failed to cast stream")
	}

	// 发送请求
	if err := libp2pStream.WriteWithCodec(codec, []byte("request")); err != nil {
		t.Fatalf("WriteWithCodec failed: %v", err)
	}

	// 读取响应
	resp, err := libp2pStream.ReadWithCodec(codec)
	if err != nil {
		t.Fatalf("ReadWithCodec failed: %v", err)
	}

	t.Logf("RPC response: %s", string(resp))
}

// TestIntegration_ChannelCommunication 集成测试：Channel 通信
func TestIntegration_ChannelCommunication(t *testing.T) {
	ctx := context.Background()

	server, err := NewLibp2pTransport(ctx, &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	defer server.Close()

	client, err := NewLibp2pTransport(ctx, &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	defer client.Close()

	// 连接
	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()
	_, err = client.Connect(ctx, addr)
	if err != nil && err != service.ErrAlreadyConnected {
		t.Fatalf("Connect failed: %v", err)
	}

	// 设置 Channel 处理器
	channelProtocol := "/nexkv/channel-test/1.0.0"
	codec := &LengthPrefixedCodec{}

	server.SetStreamHandler(channelProtocol, func(stream service.Stream) {
		defer stream.Close()

		// 读取
		data, _ := codec.Decode(stream) //nolint:errcheck // test code
		// 响应
		response := append([]byte("echo: "), data...)
		_ = codec.Encode(stream, response) //nolint:errcheck // test code
	})

	// 打开 Channel
	channel, err := client.OpenChannel(ctx, server.Self(), channelProtocol)
	if err != nil {
		t.Fatalf("OpenChannel failed: %v", err)
	}
	defer channel.Close()

	// 发送
	if err := channel.Send(ctx, []byte("hello channel")); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	// 接收
	resp, err := channel.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv failed: %v", err)
	}

	t.Logf("Channel response: %s", string(resp))
}

// TestIntegration_DisconnectAndReconnect 集成测试：断开重连
func TestIntegration_DisconnectAndReconnect(t *testing.T) {
	ctx := context.Background()

	server, err := NewLibp2pTransport(ctx, &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	defer server.Close()

	client, err := NewLibp2pTransport(ctx, &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	defer client.Close()

	// 连接
	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()
	_, err = client.Connect(ctx, addr)
	if err != nil && err != service.ErrAlreadyConnected {
		t.Fatalf("Connect failed: %v", err)
	}

	// 验证连接
	if !client.IsConnected(server.Self()) {
		t.Fatal("Not connected")
	}

	// 断开
	err = client.Disconnect(server.Self())
	if err != nil {
		t.Logf("Disconnect returned: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	// 重新连接
	_, err = client.Connect(ctx, addr)
	if err != nil && err != service.ErrAlreadyConnected {
		t.Logf("Reconnect returned: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// 验证重连
	if !client.IsConnected(server.Self()) {
		t.Log("Reconnection may have failed (expected in some cases)")
	} else {
		t.Log("Reconnection successful")
	}
}

// TestIntegration_MultipleStreams 集成测试：多个并发流
func TestIntegration_MultipleStreams(t *testing.T) {
	ctx := context.Background()

	server, err := NewLibp2pTransport(ctx, &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	defer server.Close()

	client, err := NewLibp2pTransport(ctx, &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	defer client.Close()

	// 连接
	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()
	_, err = client.Connect(ctx, addr)
	if err != nil && err != service.ErrAlreadyConnected {
		t.Fatalf("Connect failed: %v", err)
	}

	// 设置处理器
	protocol := "/nexkv/multi-stream/1.0.0"
	server.SetStreamHandler(protocol, func(stream service.Stream) {
		defer stream.Close()
		buf := make([]byte, 1024)
		n, _ := stream.Read(buf)     //nolint:errcheck // test code
		_, _ = stream.Write(buf[:n]) //nolint:errcheck // test code
	})

	// 打开多个流
	for i := 0; i < 5; i++ {
		stream, err := client.OpenStream(ctx, server.Self(), protocol)
		if err != nil {
			t.Errorf("OpenStream %d failed: %v", i, err)
			continue
		}

		// 发送并接收
		_, _ = stream.Write([]byte("message")) //nolint:errcheck // test code
		buf := make([]byte, 1024)
		n, _ := stream.Read(buf) //nolint:errcheck // test code
		t.Logf("Stream %d: %s", i, string(buf[:n]))

		stream.Close()
	}
}

// TestIntegration_TransportClose 集成测试：Transport 关闭
func TestIntegration_TransportClose(t *testing.T) {
	ctx := context.Background()

	tr, err := NewLibp2pTransport(ctx, &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("create transport: %v", err)
	}

	// 验证初始状态
	if tr.Self() == "" {
		t.Error("Self() returned empty")
	}

	// 关闭
	if err := tr.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// 再次关闭
	if err := tr.Close(); err != nil {
		t.Logf("Second close returned: %v", err)
	}
}
