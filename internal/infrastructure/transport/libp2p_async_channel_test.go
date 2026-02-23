package transport

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/service"
)

// TestAsyncChannel_Basic 测试基本收发
func TestAsyncChannel_Basic(t *testing.T) {
	// 创建两个节点
	server, err := NewLibp2pTransport(context.Background(), &Config{
		EnableDiscovery: false,
	})
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	defer server.Close()

	client, err := NewLibp2pTransport(context.Background(), &Config{
		EnableDiscovery: false,
	})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	defer client.Close()

	// 连接
	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()
	if _, err := client.Connect(context.Background(), addr); err != nil {
		t.Fatalf("connect: %v", err)
	}

	proto := "/test/channel/1.0.0"
	msg := []byte("hello async channel")

	var serverCh service.AsyncChannel
	var wg sync.WaitGroup
	wg.Add(1)

	// 服务端处理
	server.SetStreamHandler(proto, func(stream service.Stream) {
		defer wg.Done()
		libp2pStream, ok := stream.(*Libp2pStream)
		if !ok {
			t.Errorf("unexpected stream type: %T", stream)
			return
		}
		serverCh = NewLibp2pAsyncChannel(nil, libp2pStream, DefaultAsyncChannelConfig())
	})

	// 客户端打开 channel
	clientCh, err := client.OpenAsyncChannel(context.Background(), server.Self(), proto)
	if err != nil {
		t.Fatalf("open async channel: %v", err)
	}

	// 发送消息
	select {
	case clientCh.SendChan() <- msg:
	case <-time.After(time.Second):
		t.Fatal("send timeout")
	}

	// 等待服务端接收
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for server")
	}

	if serverCh == nil {
		t.Fatal("server channel not created")
	}

	// 服务端读取消息
	select {
	case result := <-serverCh.RecvChan():
		if result.Err != nil {
			t.Fatalf("recv error: %v", result.Err)
		}
		if string(result.Msg) != string(msg) {
			t.Errorf("got %q, want %q", result.Msg, msg)
		}
	case <-time.After(time.Second):
		t.Fatal("recv timeout")
	}
}

// TestAsyncChannel_Concurrent 测试并发收发
func TestAsyncChannel_Concurrent(t *testing.T) {
	server, err := NewLibp2pTransport(context.Background(), &Config{
		EnableDiscovery: false,
	})
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	defer server.Close()

	client, err := NewLibp2pTransport(context.Background(), &Config{
		EnableDiscovery: false,
	})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	defer client.Close()

	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()
	if _, err := client.Connect(context.Background(), addr); err != nil {
		t.Fatalf("connect: %v", err)
	}

	proto := "/test/channel/concurrent/1.0.0"
	msgCount := 100

	var serverCh service.AsyncChannel
	var wg sync.WaitGroup
	wg.Add(1)

	server.SetStreamHandler(proto, func(stream service.Stream) {
		defer wg.Done()
		libp2pStream, ok := stream.(*Libp2pStream)
		if !ok {
			t.Errorf("unexpected stream type: %T", stream)
			return
		}
		serverCh = NewLibp2pAsyncChannel(nil, libp2pStream, DefaultAsyncChannelConfig())
	})

	clientCh, err := client.OpenAsyncChannel(context.Background(), server.Self(), proto)
	if err != nil {
		t.Fatalf("open async channel: %v", err)
	}

	// 并发发送
	var sendWg sync.WaitGroup
	for i := 0; i < msgCount; i++ {
		sendWg.Add(1)
		go func(idx int) {
			defer sendWg.Done()
			msg := []byte("message")
			select {
			case clientCh.SendChan() <- msg:
			case <-time.After(time.Second):
				t.Errorf("send %d timeout", idx)
			}
		}(i)
	}

	sendWg.Wait()

	// 等待服务端
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for server")
	}

	if serverCh == nil {
		t.Fatal("server channel not created")
	}
}

// TestAsyncChannel_Close 测试关闭
func TestAsyncChannel_Close(t *testing.T) {
	server, err := NewLibp2pTransport(context.Background(), &Config{
		EnableDiscovery: false,
	})
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	defer server.Close()

	client, err := NewLibp2pTransport(context.Background(), &Config{
		EnableDiscovery: false,
	})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	defer client.Close()

	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()
	if _, err := client.Connect(context.Background(), addr); err != nil {
		t.Fatalf("connect: %v", err)
	}

	proto := "/test/channel/close/1.0.0"

	var wg sync.WaitGroup
	wg.Add(1)

	server.SetStreamHandler(proto, func(stream service.Stream) {
		defer wg.Done()
		libp2pStream, ok := stream.(*Libp2pStream)
		if !ok {
			t.Errorf("unexpected stream type: %T", stream)
			return
		}
		_ = NewLibp2pAsyncChannel(nil, libp2pStream, DefaultAsyncChannelConfig())
	})

	clientCh, err := client.OpenAsyncChannel(context.Background(), server.Self(), proto)
	if err != nil {
		t.Fatalf("open async channel: %v", err)
	}

	// 关闭
	if err := clientCh.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// TestAsyncChannel_Buffer 测试缓冲区配置
func TestAsyncChannel_Buffer(t *testing.T) {
	cfg := &AsyncChannelConfig{
		SendBufferSize: 512,
		RecvBufferSize: 512,
	}
	cfg.validate()

	// validate() 会确保值不小于最小值 (256)
	if cfg.SendBufferSize < 256 {
		t.Errorf("send buffer size = %d, want >= 256", cfg.SendBufferSize)
	}
	if cfg.RecvBufferSize < 256 {
		t.Errorf("recv buffer size = %d, want >= 256", cfg.RecvBufferSize)
	}
}
