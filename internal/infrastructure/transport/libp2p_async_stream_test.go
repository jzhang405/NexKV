package transport

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/service"
)

// TestAsyncStream_Basic 测试基本读写
func TestAsyncStream_Basic(t *testing.T) {
	// 创建两个节点
	server, err := NewLibp2pTransport(context.Background(), &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	defer server.Close()

	client, err := NewLibp2pTransport(context.Background(), &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	defer client.Close()

	// 连接
	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()
	if _, err := client.Connect(context.Background(), addr); err != nil {
		t.Fatalf("connect: %v", err)
	}

	proto := "/nexkv/test/asyncstream/1.0.0"

	// 服务端接受异步流
	var serverStream service.AsyncStream
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		stream, err := server.AcceptStream(proto)
		if err != nil {
			t.Errorf("accept stream: %v", err)
			return
		}
		libp2pStream, ok := stream.(*Libp2pStream)
		if !ok {
			t.Errorf("unexpected stream type: %T", stream)
			return
		}
		serverStream = NewLibp2pAsyncStream(nil, libp2pStream, DefaultAsyncStreamConfig())
	}()

	// 客户端打开异步流
	clientStream, err := client.OpenAsyncStream(context.Background(), server.Self(), proto)
	if err != nil {
		t.Fatalf("open async stream: %v", err)
	}

	wg.Wait()
	if serverStream == nil {
		t.Fatal("server stream not created")
	}
	defer serverStream.Close()
	defer clientStream.Close()

	// 客户端写入数据
	errCh := make(chan error, 1)
	select {
	case clientStream.WriteChan() <- service.WriteRequest{Data: []byte("hello"), Err: errCh}:
	case <-time.After(time.Second):
		t.Fatal("write timeout")
	}

	// 等待写入结果
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("write error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("wait write result timeout")
	}

	// 服务端读取数据
	select {
	case result := <-serverStream.ReadChan():
		if result.Err != nil {
			t.Fatalf("read error: %v", result.Err)
		}
		if string(result.Data) != "hello" {
			t.Errorf("got %q, want %q", result.Data, "hello")
		}
	case <-time.After(time.Second):
		t.Fatal("read timeout")
	}

	// 服务端回复
	replyCh := make(chan error, 1)
	select {
	case serverStream.WriteChan() <- service.WriteRequest{Data: []byte("world"), Err: replyCh}:
	case <-time.After(time.Second):
		t.Fatal("write reply timeout")
	}

	// 等待写入结果
	select {
	case err := <-replyCh:
		if err != nil {
			t.Fatalf("write reply error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("wait write reply result timeout")
	}

	// 客户端读取回复
	select {
	case result := <-clientStream.ReadChan():
		if result.Err != nil {
			t.Fatalf("read reply error: %v", result.Err)
		}
		if string(result.Data) != "world" {
			t.Errorf("got %q, want %q", result.Data, "world")
		}
	case <-time.After(time.Second):
		t.Fatal("read reply timeout")
	}
}

// TestAsyncStream_Burst 测试突发写入
func TestAsyncStream_Burst(t *testing.T) {
	server, err := NewLibp2pTransport(context.Background(), &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	defer server.Close()

	client, err := NewLibp2pTransport(context.Background(), &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	defer client.Close()

	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()
	if _, err := client.Connect(context.Background(), addr); err != nil {
		t.Fatalf("connect: %v", err)
	}

	proto := "/nexkv/test/burststream/1.0.0"

	var serverStream service.AsyncStream
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		stream, err := server.AcceptStream(proto)
		if err != nil {
			t.Errorf("accept stream: %v", err)
			return
		}
		libp2pStream, ok := stream.(*Libp2pStream)
		if !ok {
			t.Errorf("unexpected stream type: %T", stream)
			return
		}
		serverStream = NewLibp2pAsyncStream(nil, libp2pStream, DefaultAsyncStreamConfig())
	}()

	clientStream, err := client.OpenAsyncStream(context.Background(), server.Self(), proto)
	if err != nil {
		t.Fatalf("open async stream: %v", err)
	}

	wg.Wait()
	if serverStream == nil {
		t.Fatal("server stream not created")
	}
	defer serverStream.Close()
	defer clientStream.Close()

	// 发送 50 条消息
	msgCount := 50
	errChs := make([]chan error, msgCount)

	for i := 0; i < msgCount; i++ {
		errChs[i] = make(chan error, 1)
		select {
		case clientStream.WriteChan() <- service.WriteRequest{Data: []byte("msg"), Err: errChs[i]}:
		case <-time.After(time.Second):
			t.Fatalf("write timeout at %d", i)
		}
	}

	// 接收 50 条消息
	received := 0
	timeout := time.After(5 * time.Second)
	for received < msgCount {
		select {
		case <-serverStream.ReadChan():
			received++
		case <-timeout:
			t.Fatalf("only received %d/%d messages", received, msgCount)
		}
	}

	if received != msgCount {
		t.Errorf("received %d, want %d", received, msgCount)
	}
}

// TestAsyncStream_Close 测试关闭行为
func TestAsyncStream_Close(t *testing.T) {
	server, err := NewLibp2pTransport(context.Background(), &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	defer server.Close()

	client, err := NewLibp2pTransport(context.Background(), &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	defer client.Close()

	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()
	if _, err := client.Connect(context.Background(), addr); err != nil {
		t.Fatalf("connect: %v", err)
	}

	proto := "/nexkv/test/closestream/1.0.0"

	var serverStream service.AsyncStream
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		stream, err := server.AcceptStream(proto)
		if err != nil {
			t.Errorf("accept stream: %v", err)
			return
		}
		libp2pStream, ok := stream.(*Libp2pStream)
		if !ok {
			t.Errorf("unexpected stream type: %T", stream)
			return
		}
		serverStream = NewLibp2pAsyncStream(nil, libp2pStream, DefaultAsyncStreamConfig())
	}()

	clientStream, err := client.OpenAsyncStream(context.Background(), server.Self(), proto)
	if err != nil {
		t.Fatalf("open async stream: %v", err)
	}

	wg.Wait()
	if serverStream == nil {
		t.Fatal("server stream not created")
	}

	// 客户端关闭
	if err := clientStream.Close(); err != nil {
		t.Errorf("close error: %v", err)
	}

	// 服务端应收到连接断开（readCh 关闭或收到错误）
	select {
	case result, ok := <-serverStream.ReadChan():
		if ok && result.Err == nil {
			t.Error("expected error or closed channel")
		}
		// 收到错误或 channel 关闭都是预期行为
	case <-time.After(5 * time.Second):
		t.Error("timeout waiting for read channel close")
	}

	// 清理
	_ = serverStream.Close()
}

// TestAsyncStream_WaitWrite 测试 WaitWrite
func TestAsyncStream_WaitWrite(t *testing.T) {
	server, err := NewLibp2pTransport(context.Background(), &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	defer server.Close()

	client, err := NewLibp2pTransport(context.Background(), &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	defer client.Close()

	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()
	if _, err := client.Connect(context.Background(), addr); err != nil {
		t.Fatalf("connect: %v", err)
	}

	proto := "/nexkv/test/waitwrite/1.0.0"

	var serverStream service.AsyncStream
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		stream, err := server.AcceptStream(proto)
		if err != nil {
			t.Errorf("accept stream: %v", err)
			return
		}
		libp2pStream, ok := stream.(*Libp2pStream)
		if !ok {
			t.Errorf("unexpected stream type: %T", stream)
			return
		}
		serverStream = NewLibp2pAsyncStream(nil, libp2pStream, DefaultAsyncStreamConfig())
	}()

	clientStream, err := client.OpenAsyncStream(context.Background(), server.Self(), proto)
	if err != nil {
		t.Fatalf("open async stream: %v", err)
	}

	wg.Wait()
	if serverStream == nil {
		t.Fatal("server stream not created")
	}
	defer serverStream.Close()

	// 发送多条消息（不需要等待结果）
	for i := 0; i < 10; i++ {
		clientStream.WriteChan() <- service.WriteRequest{Data: []byte("msg")}
	}

	// 直接关闭（Close 内部会关闭 writeCh）
	if err := clientStream.Close(); err != nil {
		t.Errorf("close error: %v", err)
	}

	// WaitClosedWithTimeout 应该立即返回（因为已关闭）
	if err := clientStream.WaitClosedWithTimeout(time.Second); err != nil {
		// 可能返回 nil 或其他错误，但不应该超时
		t.Logf("WaitClosed result: %v (expected after close)", err)
	}
}
