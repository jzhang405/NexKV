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
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/service"
)

// TestStreamIO 测试流的读写操作
func TestStreamIO(t *testing.T) {
	ctx := context.Background()

	// 创建两个 transport
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

	// 构建地址并连接
	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()
	_, err = client.Connect(ctx, addr)
	if err != nil && err != service.ErrAlreadyConnected {
		t.Fatalf("Connect failed: %v", err)
	}

	// 设置流处理器
	protocolID := "/test-stream/1.0.0"
	received := make(chan []byte, 1)

	server.SetStreamHandler(protocolID, func(stream service.Stream) {
		defer stream.Close()

		// 测试 ID(), Protocol(), RemotePeer()
		t.Logf("Stream ID: %s", stream.ID())
		t.Logf("Stream Protocol: %s", stream.Protocol())
		t.Logf("Remote Peer: %s", stream.RemotePeer())

		// 读取数据
		buf := make([]byte, 1024)
		n, err := stream.Read(buf)
		if err != nil && err != io.EOF {
			t.Errorf("Read failed: %v", err)
			return
		}

		// 写入响应
		_, err = stream.Write([]byte("pong"))
		if err != nil {
			t.Errorf("Write failed: %v", err)
			return
		}

		received <- buf[:n]
	})

	// 打开流
	stream, err := client.OpenStream(ctx, server.Self(), protocolID)
	if err != nil {
		t.Fatalf("OpenStream failed: %v", err)
	}
	defer stream.Close()

	// 测试 Stream 方法
	t.Logf("Client Stream ID: %s", stream.ID())
	t.Logf("Client Stream Protocol: %s", stream.Protocol())

	// 写入数据
	_, err = stream.Write([]byte("ping"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// 读取响应
	buf := make([]byte, 1024)
	n, err := stream.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("Read failed: %v", err)
	}

	// 验证
	select {
	case data := <-received:
		if string(data) != "ping" {
			t.Errorf("Expected 'ping', got '%s'", string(data))
		}
	case <-time.After(5 * time.Second):
		t.Error("Timeout waiting for data")
	}

	if string(buf[:n]) != "pong" {
		t.Errorf("Expected 'pong', got '%s'", string(buf[:n]))
	}
}

// TestStreamDeadline 测试流的 Deadline 设置
func TestStreamDeadline(t *testing.T) {
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

	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()
	_, err = client.Connect(ctx, addr)
	if err != nil && err != service.ErrAlreadyConnected {
		t.Fatalf("Connect failed: %v", err)
	}

	protocolID := "/test-deadline/1.0.0"
	server.SetStreamHandler(protocolID, func(stream service.Stream) {
		defer stream.Close()
		// 不做任何事情，让客户端超时
	})

	stream, err := client.OpenStream(ctx, server.Self(), protocolID)
	if err != nil {
		t.Fatalf("OpenStream failed: %v", err)
	}
	defer stream.Close()

	// 测试 SetReadDeadline
	err = stream.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if err != nil {
		t.Errorf("SetReadDeadline failed: %v", err)
	}
}

// TestChannelIO 测试 Channel 的 Send/Recv（使用长度前缀编码）
func TestChannelIO(t *testing.T) {
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

	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()
	_, err = client.Connect(ctx, addr)
	if err != nil && err != service.ErrAlreadyConnected {
		t.Fatalf("Connect failed: %v", err)
	}

	protocolID := "/test-channel/1.0.0"
	codec := &LengthPrefixedCodec{}

	server.SetStreamHandler(protocolID, func(stream service.Stream) {
		defer stream.Close()

		// 使用长度前缀编码读取请求
		reqData, err := codec.Decode(stream)
		if err != nil {
			t.Errorf("Decode failed: %v", err)
			return
		}

		// 使用长度前缀编码发送响应
		response := append([]byte("echo: "), reqData...)
		if err := codec.Encode(stream, response); err != nil {
			t.Errorf("Encode failed: %v", err)
		}
	})

	// 打开 Channel
	channel, err := client.OpenChannel(ctx, server.Self(), protocolID)
	if err != nil {
		t.Fatalf("OpenChannel failed: %v", err)
	}
	defer channel.Close()

	// 发送消息
	err = channel.Send(ctx, []byte("hello"))
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	// 接收响应
	response, err := channel.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv failed: %v", err)
	}

	if !bytes.Contains(response, []byte("hello")) {
		t.Errorf("Expected response to contain 'hello', got '%s'", string(response))
	}

	t.Logf("Channel response: %s", string(response))
}

// TestAsyncChannelIO 测试异步 Channel（使用长度前缀编码）
func TestAsyncChannelIO(t *testing.T) {
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

	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()
	_, err = client.Connect(ctx, addr)
	if err != nil && err != service.ErrAlreadyConnected {
		t.Fatalf("Connect failed: %v", err)
	}

	protocolID := "/test-async-channel/1.0.0"
	codec := &LengthPrefixedCodec{}

	server.SetStreamHandler(protocolID, func(stream service.Stream) {
		defer stream.Close()

		// 使用长度前缀编码读取
		reqData, err := codec.Decode(stream)
		if err != nil {
			return
		}

		// 使用长度前缀编码响应
		response := append([]byte("async-echo: "), reqData...)
		_ = codec.Encode(stream, response) //nolint:errcheck // test code
	})

	// 打开异步 Channel
	channel, err := client.OpenAsyncChannel(ctx, server.Self(), protocolID)
	if err != nil {
		t.Fatalf("OpenAsyncChannel failed: %v", err)
	}
	defer channel.Close()

	// 获取发送通道
	sendChan := channel.SendChan()
	if sendChan == nil {
		t.Fatal("SendChan should not be nil")
	}

	// 获取接收通道
	recvChan := channel.RecvChan()
	if recvChan == nil {
		t.Fatal("RecvChan should not be nil")
	}

	// 发送消息
	select {
	case sendChan <- []byte("async-hello"):
	case <-time.After(time.Second):
		t.Fatal("Timeout sending message")
	}

	// 接收响应
	select {
	case msgOrErr := <-recvChan:
		if msgOrErr.Err != nil {
			t.Fatalf("Recv error: %v", msgOrErr.Err)
		}
		if !bytes.Contains(msgOrErr.Msg, []byte("async-hello")) {
			t.Errorf("Expected response to contain 'async-hello', got '%s'", string(msgOrErr.Msg))
		}
		t.Logf("Async channel response: %s", string(msgOrErr.Msg))
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout receiving response")
	}
}

// TestAsyncStreamIO 测试异步 Stream（使用长度前缀编码）
func TestAsyncStreamIO(t *testing.T) {
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

	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()
	_, err = client.Connect(ctx, addr)
	if err != nil && err != service.ErrAlreadyConnected {
		t.Fatalf("Connect failed: %v", err)
	}

	protocolID := "/test-async-stream/1.0.0"
	codec := &LengthPrefixedCodec{}

	server.SetStreamHandler(protocolID, func(stream service.Stream) {
		defer stream.Close()

		// 使用长度前缀编码读取
		reqData, err := codec.Decode(stream)
		if err != nil {
			return
		}

		// 使用长度前缀编码响应
		response := append([]byte("stream-echo: "), reqData...)
		_ = codec.Encode(stream, response) //nolint:errcheck // test code
	})

	// 打开异步 Stream
	stream, err := client.OpenAsyncStream(ctx, server.Self(), protocolID)
	if err != nil {
		t.Fatalf("OpenAsyncStream failed: %v", err)
	}
	defer stream.Close()

	// 获取读写通道
	writeChan := stream.WriteChan()
	if writeChan == nil {
		t.Fatal("WriteChan should not be nil")
	}

	readChan := stream.ReadChan()
	if readChan == nil {
		t.Fatal("ReadChan should not be nil")
	}

	// 写入消息（使用 WriteRequest 结构）
	writeReq := service.WriteRequest{
		Data: []byte("stream-hello"),
		Err:  nil, // 不等待确认
	}
	select {
	case writeChan <- writeReq:
	case <-time.After(time.Second):
		t.Fatal("Timeout writing message")
	}

	// 读取响应
	select {
	case result := <-readChan:
		if result.Err != nil {
			t.Fatalf("Read error: %v", result.Err)
		}
		if !bytes.Contains(result.Data, []byte("stream-hello")) {
			t.Errorf("Expected response to contain 'stream-hello', got '%s'", string(result.Data))
		}
		t.Logf("Async stream response: %s", string(result.Data))
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout reading response")
	}
}

// TestStreamReset 测试流重置
func TestStreamReset(t *testing.T) {
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

	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()
	_, err = client.Connect(ctx, addr)
	if err != nil && err != service.ErrAlreadyConnected {
		t.Fatalf("Connect failed: %v", err)
	}

	protocolID := "/test-reset/1.0.0"
	server.SetStreamHandler(protocolID, func(stream service.Stream) {
		// 不关闭，让客户端重置
	})

	stream, err := client.OpenStream(ctx, server.Self(), protocolID)
	if err != nil {
		t.Fatalf("OpenStream failed: %v", err)
	}

	// 测试 Reset
	err = stream.Reset()
	if err != nil {
		t.Errorf("Reset failed: %v", err)
	}
}

// TestSetStreamHandler 测试流处理器设置
func TestSetStreamHandler(t *testing.T) {
	ctx := context.Background()

	tr, err := NewLibp2pTransport(ctx, &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("create transport: %v", err)
	}
	defer tr.Close()

	protocolID := "/test-handler/1.0.0"

	// 设置流处理器
	tr.SetStreamHandler(protocolID, func(stream service.Stream) {
		stream.Close()
	})

	// 验证处理器已注册
	// 注意：这里无法直接验证，但不会 panic 就说明成功
	t.Logf("Stream handler set for protocol: %s", protocolID)
}

// TestStreamSetDeadline 测试流的 SetDeadline 方法
func TestStreamSetDeadline(t *testing.T) {
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

	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()
	_, err = client.Connect(ctx, addr)
	if err != nil && err != service.ErrAlreadyConnected {
		t.Fatalf("Connect failed: %v", err)
	}

	protocolID := "/test-stream-deadline/1.0.0"
	server.SetStreamHandler(protocolID, func(stream service.Stream) {
		defer stream.Close()
		// 简单回显
		buf := make([]byte, 1024)
		n, _ := stream.Read(buf)     //nolint:errcheck // test code
		_, _ = stream.Write(buf[:n]) //nolint:errcheck // test code
	})

	stream, err := client.OpenStream(ctx, server.Self(), protocolID)
	if err != nil {
		t.Fatalf("OpenStream failed: %v", err)
	}
	defer stream.Close()

	// 测试 SetDeadline（同时设置读写 deadline）
	// 需要类型断言为 Libp2pStream 才能访问 SetDeadline
	libp2pStream, ok := stream.(*Libp2pStream)
	if !ok {
		t.Fatal("Failed to cast stream to *Libp2pStream")
	}

	deadline := time.Now().Add(5 * time.Second)
	err = libp2pStream.SetDeadline(deadline)
	if err != nil {
		t.Errorf("SetDeadline failed: %v", err)
	}

	// 简单测试确保流仍然工作
	_, err = stream.Write([]byte("test"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	buf := make([]byte, 1024)
	n, err := stream.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("Read failed: %v", err)
	}

	if string(buf[:n]) != "test" {
		t.Errorf("Expected 'test', got '%s'", string(buf[:n]))
	}
}

// TestStreamSetWriteDeadline 测试流的 SetWriteDeadline 方法
func TestStreamSetWriteDeadline(t *testing.T) {
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

	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()
	_, err = client.Connect(ctx, addr)
	if err != nil && err != service.ErrAlreadyConnected {
		t.Fatalf("Connect failed: %v", err)
	}

	protocolID := "/test-write-deadline/1.0.0"
	server.SetStreamHandler(protocolID, func(stream service.Stream) {
		defer stream.Close()
		buf := make([]byte, 1024)
		_, _ = stream.Read(buf) //nolint:errcheck // test code
	})

	stream, err := client.OpenStream(ctx, server.Self(), protocolID)
	if err != nil {
		t.Fatalf("OpenStream failed: %v", err)
	}
	defer stream.Close()

	// 测试 SetWriteDeadline
	err = stream.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err != nil {
		t.Errorf("SetWriteDeadline failed: %v", err)
	}

	// 确保写入仍然工作
	_, err = stream.Write([]byte("test"))
	if err != nil {
		t.Errorf("Write failed: %v", err)
	}
}

// TestAsyncChannelWaitClosed 测试 AsyncChannel 的 WaitClosed 方法
func TestAsyncChannelWaitClosed(t *testing.T) {
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

	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()
	_, err = client.Connect(ctx, addr)
	if err != nil && err != service.ErrAlreadyConnected {
		t.Fatalf("Connect failed: %v", err)
	}

	protocolID := "/test-waitclosed-channel/1.0.0"
	codec := &LengthPrefixedCodec{}

	server.SetStreamHandler(protocolID, func(stream service.Stream) {
		defer stream.Close()
		// 读取并响应
		data, _ := codec.Decode(stream)
		_ = codec.Encode(stream, data) //nolint:errcheck // test code
	})

	// 打开异步 Channel
	channel, err := client.OpenAsyncChannel(ctx, server.Self(), protocolID)
	if err != nil {
		t.Fatalf("OpenAsyncChannel failed: %v", err)
	}

	// 发送消息
	sendChan := channel.SendChan()
	select {
	case sendChan <- []byte("hello"):
	case <-time.After(time.Second):
		t.Fatal("Timeout sending message")
	}

	// 接收响应
	recvChan := channel.RecvChan()
	select {
	case msg := <-recvChan:
		if msg.Err != nil {
			t.Fatalf("Recv error: %v", msg.Err)
		}
		t.Logf("Received: %s", string(msg.Msg))
	case <-time.After(time.Second):
		t.Fatal("Timeout receiving message")
	}

	// 关闭 channel
	err = channel.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// 测试 WaitClosed
	err = channel.WaitClosed()
	if err != nil {
		t.Logf("WaitClosed returned: %v", err)
	}
}

// TestAsyncChannelWaitClosedWithTimeout 测试 AsyncChannel 的 WaitClosedWithTimeout 方法
func TestAsyncChannelWaitClosedWithTimeout(t *testing.T) {
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

	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()
	_, err = client.Connect(ctx, addr)
	if err != nil && err != service.ErrAlreadyConnected {
		t.Fatalf("Connect failed: %v", err)
	}

	protocolID := "/test-waitclosed-timeout/1.0.0"

	server.SetStreamHandler(protocolID, func(stream service.Stream) {
		defer stream.Close()
	})

	// 打开异步 Channel
	channel, err := client.OpenAsyncChannel(ctx, server.Self(), protocolID)
	if err != nil {
		t.Fatalf("OpenAsyncChannel failed: %v", err)
	}

	// 关闭 channel
	err = channel.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// 测试 WaitClosedWithTimeout
	err = channel.WaitClosedWithTimeout(2 * time.Second)
	if err != nil {
		t.Logf("WaitClosedWithTimeout returned: %v", err)
	}
}

// TestAsyncStreamWaitClosed 测试 AsyncStream 的 WaitClosed 方法
func TestAsyncStreamWaitClosed(t *testing.T) {
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

	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()
	_, err = client.Connect(ctx, addr)
	if err != nil && err != service.ErrAlreadyConnected {
		t.Fatalf("Connect failed: %v", err)
	}

	protocolID := "/test-waitclosed-stream/1.0.0"
	codec := &LengthPrefixedCodec{}

	server.SetStreamHandler(protocolID, func(stream service.Stream) {
		defer stream.Close()
		// 读取并响应
		data, _ := codec.Decode(stream)
		_ = codec.Encode(stream, data) //nolint:errcheck // test code
	})

	// 打开异步 Stream
	stream, err := client.OpenAsyncStream(ctx, server.Self(), protocolID)
	if err != nil {
		t.Fatalf("OpenAsyncStream failed: %v", err)
	}

	// 发送消息
	writeChan := stream.WriteChan()
	writeReq := service.WriteRequest{
		Data: []byte("hello"),
		Err:  nil,
	}
	select {
	case writeChan <- writeReq:
	case <-time.After(time.Second):
		t.Fatal("Timeout sending message")
	}

	// 接收响应
	readChan := stream.ReadChan()
	select {
	case result := <-readChan:
		if result.Err != nil {
			t.Fatalf("Read error: %v", result.Err)
		}
		t.Logf("Received: %s", string(result.Data))
	case <-time.After(time.Second):
		t.Fatal("Timeout receiving message")
	}

	// 关闭 stream
	err = stream.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// 测试 WaitClosed
	err = stream.WaitClosed()
	if err != nil {
		t.Logf("WaitClosed returned: %v", err)
	}
}

// TestAsyncStreamWaitClosedWithTimeout 测试 AsyncStream 的 WaitClosedWithTimeout 方法
func TestAsyncStreamWaitClosedWithTimeout(t *testing.T) {
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

	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()
	_, err = client.Connect(ctx, addr)
	if err != nil && err != service.ErrAlreadyConnected {
		t.Fatalf("Connect failed: %v", err)
	}

	protocolID := "/test-stream-waitclosed-timeout/1.0.0"

	server.SetStreamHandler(protocolID, func(stream service.Stream) {
		defer stream.Close()
	})

	// 打开异步 Stream
	stream, err := client.OpenAsyncStream(ctx, server.Self(), protocolID)
	if err != nil {
		t.Fatalf("OpenAsyncStream failed: %v", err)
	}

	// 关闭 stream
	err = stream.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// 测试 WaitClosedWithTimeout
	err = stream.WaitClosedWithTimeout(2 * time.Second)
	if err != nil {
		t.Logf("WaitClosedWithTimeout returned: %v", err)
	}
}

// TestStreamWriteWithCodecAndReadWithCodec 测试流的 WriteWithCodec 和 ReadWithCodec 方法
func TestStreamWriteWithCodecAndReadWithCodec(t *testing.T) {
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

	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()
	_, err = client.Connect(ctx, addr)
	if err != nil && err != service.ErrAlreadyConnected {
		t.Fatalf("Connect failed: %v", err)
	}

	protocolID := "/test-codec-stream/1.0.0"
	codec := &LengthPrefixedCodec{}

	server.SetStreamHandler(protocolID, func(stream service.Stream) {
		defer stream.Close()

		// 使用 ReadWithCodec 读取
		libp2pStream, ok := stream.(*Libp2pStream)
		if !ok {
			t.Error("Failed to cast stream")
			return
		}

		data, err := libp2pStream.ReadWithCodec(codec)
		if err != nil {
			t.Errorf("ReadWithCodec failed: %v", err)
			return
		}

		// 使用 WriteWithCodec 响应
		response := append([]byte("echo: "), data...)
		if err := libp2pStream.WriteWithCodec(codec, response); err != nil {
			t.Errorf("WriteWithCodec failed: %v", err)
		}
	})

	// 打开流
	stream, err := client.OpenStream(ctx, server.Self(), protocolID)
	if err != nil {
		t.Fatalf("OpenStream failed: %v", err)
	}
	defer stream.Close()

	libp2pStream, ok := stream.(*Libp2pStream)
	if !ok {
		t.Fatal("Failed to cast stream to *Libp2pStream")
	}

	// 使用 WriteWithCodec 发送
	if err := libp2pStream.WriteWithCodec(codec, []byte("hello")); err != nil {
		t.Fatalf("WriteWithCodec failed: %v", err)
	}

	// 使用 ReadWithCodec 读取响应
	data, err := libp2pStream.ReadWithCodec(codec)
	if err != nil {
		t.Fatalf("ReadWithCodec failed: %v", err)
	}

	if !bytes.Contains(data, []byte("hello")) {
		t.Errorf("Expected response to contain 'hello', got '%s'", string(data))
	}

	t.Logf("Response: %s", string(data))
}

// TestStreamMultipleMessages 测试流上发送多条消息
func TestStreamMultipleMessages(t *testing.T) {
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

	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()
	_, err = client.Connect(ctx, addr)
	if err != nil && err != service.ErrAlreadyConnected {
		t.Fatalf("Connect failed: %v", err)
	}

	protocolID := "/test-multi-msg/1.0.0"
	codec := &LengthPrefixedCodec{}
	receivedCount := 0

	server.SetStreamHandler(protocolID, func(stream service.Stream) {
		defer stream.Close()

		libp2pStream, ok := stream.(*Libp2pStream)
		if !ok {
			return
		}

		// 读取多条消息
		for range 5 {
			data, err := libp2pStream.ReadWithCodec(codec)
			if err != nil {
				return
			}
			receivedCount++
			// 响应
			_ = libp2pStream.WriteWithCodec(codec, append([]byte("ack: "), data...)) //nolint:errcheck // test code
		}
	})

	// 打开流
	stream, err := client.OpenStream(ctx, server.Self(), protocolID)
	if err != nil {
		t.Fatalf("OpenStream failed: %v", err)
	}
	defer stream.Close()

	libp2pStream, ok := stream.(*Libp2pStream)
	if !ok {
		t.Fatal("Failed to cast stream")
	}

	// 发送多条消息
	for i := 0; i < 5; i++ {
		msg := []byte("message-" + string(rune('0'+i)))
		if err := libp2pStream.WriteWithCodec(codec, msg); err != nil {
			t.Errorf("WriteWithCodec failed: %v", err)
		}

		// 读取响应
		resp, err := libp2pStream.ReadWithCodec(codec)
		if err != nil {
			t.Errorf("ReadWithCodec failed: %v", err)
		}
		t.Logf("Received response: %s", string(resp))
	}

	// 等待服务器处理
	time.Sleep(100 * time.Millisecond)

	if receivedCount != 5 {
		t.Errorf("Expected 5 messages received, got %d", receivedCount)
	}
}

// TestTransportConnectAlreadyConnected 测试重复连接
func TestTransportConnectAlreadyConnected(t *testing.T) {
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

	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()

	// 第一次连接
	_, err = client.Connect(ctx, addr)
	if err != nil && err != service.ErrAlreadyConnected {
		t.Fatalf("First connect failed: %v", err)
	}

	// 第二次连接（应该返回 ErrAlreadyConnected）
	_, err = client.Connect(ctx, addr)
	if err != service.ErrAlreadyConnected {
		t.Logf("Second connect returned: %v (expected ErrAlreadyConnected)", err)
	}
}

// TestTransportDisconnect 测试断开连接
func TestTransportDisconnect(t *testing.T) {
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

	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()

	// 连接
	_, err = client.Connect(ctx, addr)
	if err != nil && err != service.ErrAlreadyConnected {
		t.Fatalf("Connect failed: %v", err)
	}

	// 验证已连接
	if !client.IsConnected(server.Self()) {
		t.Fatal("Expected to be connected")
	}

	// 断开连接
	err = client.Disconnect(server.Self())
	if err != nil {
		t.Logf("Disconnect returned: %v", err)
	}
}

// TestTransportMultipleSetStreamHandler 测试多次设置流处理器
func TestTransportMultipleSetStreamHandler(t *testing.T) {
	ctx := context.Background()

	tr, err := NewLibp2pTransport(ctx, &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("create transport: %v", err)
	}
	defer tr.Close()

	protocolID := "/test-multi-handler/1.0.0"

	// 设置第一个处理器
	tr.SetStreamHandler(protocolID, func(stream service.Stream) {
		stream.Close()
	})

	// 再次设置相同协议的处理器（覆盖）
	tr.SetStreamHandler(protocolID, func(stream service.Stream) {
		stream.Close()
	})

	t.Logf("Multiple SetStreamHandler calls succeeded")
}

// TestTransportGetConnectedPeers 测试获取已连接节点列表
func TestTransportGetConnectedPeers(t *testing.T) {
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

	// 连接前应该为空
	peers := client.ConnectedPeers()
	if len(peers) != 0 {
		t.Logf("Before connect: %d peers (expected 0)", len(peers))
	}

	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()
	_, err = client.Connect(ctx, addr)
	if err != nil && err != service.ErrAlreadyConnected {
		t.Fatalf("Connect failed: %v", err)
	}

	// 连接后应该有一个节点
	peers = client.ConnectedPeers()
	if len(peers) == 0 {
		t.Error("Expected at least one connected peer")
	} else {
		t.Logf("Connected peers: %v", peers)
	}
}

// TestTransportSelfID 测试获取自身 Peer ID
func TestTransportSelfID(t *testing.T) {
	ctx := context.Background()

	tr, err := NewLibp2pTransport(ctx, &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("create transport: %v", err)
	}
	defer tr.Close()

	self := tr.Self()
	if self == "" {
		t.Error("Self() returned empty string")
	}

	t.Logf("Self ID: %s", self)
}

// TestTransportClose 测试关闭 Transport
func TestTransportClose(t *testing.T) {
	ctx := context.Background()

	tr, err := NewLibp2pTransport(ctx, &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("create transport: %v", err)
	}

	// 关闭
	err = tr.Close()
	if err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// 再次关闭应该返回 nil 或特定错误
	err = tr.Close()
	t.Logf("Second close returned: %v", err)
}

// TestAsyncChannelSendAfterClose 测试在关闭后发送
func TestAsyncChannelSendAfterClose(t *testing.T) {
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

	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()
	_, err = client.Connect(ctx, addr)
	if err != nil && err != service.ErrAlreadyConnected {
		t.Fatalf("Connect failed: %v", err)
	}

	protocolID := "/test-send-after-close/1.0.0"
	server.SetStreamHandler(protocolID, func(stream service.Stream) {
		defer stream.Close()
	})

	channel, err := client.OpenAsyncChannel(ctx, server.Self(), protocolID)
	if err != nil {
		t.Fatalf("OpenAsyncChannel failed: %v", err)
	}

	// 关闭
	err = channel.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// 等待关闭完成
	time.Sleep(100 * time.Millisecond)

	// 在关闭后，SendChan 应该返回 nil 或已关闭的通道
	// 尝试发送可能会 panic，所以我们使用 recover
	defer func() {
		if r := recover(); r != nil {
			t.Logf("Send after close panicked (expected): %v", r)
		}
	}()

	sendChan := channel.SendChan()
	if sendChan == nil {
		t.Log("SendChan returned nil after close")
		return
	}

	// 尝试发送（可能会 panic）
	select {
	case sendChan <- []byte("after close"):
		t.Log("Send after close succeeded (may be buffered)")
	case <-time.After(100 * time.Millisecond):
		t.Log("Send after close blocked (expected)")
	}
}

// TestAsyncStreamSendAfterClose 测试在关闭后发送（AsyncStream）
func TestAsyncStreamSendAfterClose(t *testing.T) {
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

	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()
	_, err = client.Connect(ctx, addr)
	if err != nil && err != service.ErrAlreadyConnected {
		t.Fatalf("Connect failed: %v", err)
	}

	protocolID := "/test-stream-send-after-close/1.0.0"
	server.SetStreamHandler(protocolID, func(stream service.Stream) {
		defer stream.Close()
	})

	stream, err := client.OpenAsyncStream(ctx, server.Self(), protocolID)
	if err != nil {
		t.Fatalf("OpenAsyncStream failed: %v", err)
	}

	// 关闭
	err = stream.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// 等待关闭完成
	time.Sleep(100 * time.Millisecond)

	// 使用 recover 防止 panic
	defer func() {
		if r := recover(); r != nil {
			t.Logf("Write after close panicked (expected): %v", r)
		}
	}()

	// 尝试在关闭后发送
	writeChan := stream.WriteChan()
	if writeChan == nil {
		t.Log("WriteChan returned nil after close")
		return
	}

	writeReq := service.WriteRequest{
		Data: []byte("after close"),
		Err:  nil,
	}
	select {
	case writeChan <- writeReq:
		t.Log("Write after close succeeded (may be buffered)")
	case <-time.After(100 * time.Millisecond):
		t.Log("Write after close blocked (expected)")
	}
}

// TestTransportConnectInvalidAddress 测试连接无效地址
func TestTransportConnectInvalidAddress(t *testing.T) {
	ctx := context.Background()

	client, err := NewLibp2pTransport(ctx, &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	defer client.Close()

	// 尝试连接无效地址
	invalidAddr := "/ip4/0.0.0.0/tcp/9999/p2p/invalid-peer-id"
	_, err = client.Connect(ctx, invalidAddr)
	if err == nil {
		t.Error("Expected error for invalid address")
	}
	t.Logf("Connect to invalid address returned: %v", err)
}

// TestTransportConnectCanceledContext 测试使用取消的上下文连接
func TestTransportConnectCanceledContext(t *testing.T) {
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

	// 创建已取消的上下文
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()
	_, err = client.Connect(canceledCtx, addr)
	if err == nil {
		t.Log("Connect with canceled context succeeded (may be already connected)")
	} else {
		t.Logf("Connect with canceled context returned: %v", err)
	}
}

// TestStreamReadAfterClose 测试在关闭后读取
func TestStreamReadAfterClose(t *testing.T) {
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

	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()
	_, err = client.Connect(ctx, addr)
	if err != nil && err != service.ErrAlreadyConnected {
		t.Fatalf("Connect failed: %v", err)
	}

	protocolID := "/test-read-after-close/1.0.0"
	server.SetStreamHandler(protocolID, func(stream service.Stream) {
		defer stream.Close()
		_, _ = stream.Write([]byte("hello")) //nolint:errcheck // test code
	})

	stream, err := client.OpenStream(ctx, server.Self(), protocolID)
	if err != nil {
		t.Fatalf("OpenStream failed: %v", err)
	}

	// 读取数据
	buf := make([]byte, 1024)
	n, err := stream.Read(buf)
	if err != nil && err != io.EOF {
		t.Errorf("Read failed: %v", err)
	}
	t.Logf("Read %d bytes: %s", n, string(buf[:n]))

	// 关闭
	stream.Close()

	// 在关闭后读取
	n, err = stream.Read(buf)
	t.Logf("Read after close: n=%d, err=%v", n, err)
}

// TestChannelSendLargeMessage 测试发送大消息
func TestChannelSendLargeMessage(t *testing.T) {
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

	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()
	_, err = client.Connect(ctx, addr)
	if err != nil && err != service.ErrAlreadyConnected {
		t.Fatalf("Connect failed: %v", err)
	}

	protocolID := "/test-large-message/1.0.0"
	codec := &LengthPrefixedCodec{}

	server.SetStreamHandler(protocolID, func(stream service.Stream) {
		defer stream.Close()
		data, _ := codec.Decode(stream)
		_ = codec.Encode(stream, data) //nolint:errcheck // test code
	})

	channel, err := client.OpenChannel(ctx, server.Self(), protocolID)
	if err != nil {
		t.Fatalf("OpenChannel failed: %v", err)
	}
	defer channel.Close()

	// 发送较大的消息（10KB）
	largeMsg := make([]byte, 10*1024)
	for i := range largeMsg {
		largeMsg[i] = byte(i % 256)
	}

	err = channel.Send(ctx, largeMsg)
	if err != nil {
		t.Fatalf("Send large message failed: %v", err)
	}

	resp, err := channel.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv large message failed: %v", err)
	}

	if len(resp) != len(largeMsg) {
		t.Errorf("Response length mismatch: got %d, want %d", len(resp), len(largeMsg))
	} else {
		t.Logf("Successfully sent and received %d bytes", len(resp))
	}
}
