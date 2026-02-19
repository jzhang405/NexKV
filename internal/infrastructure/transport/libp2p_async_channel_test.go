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

	proto := "/nexkv/test/async/1.0.0"

	// 服务端接受异步通道
	var serverCh service.AsyncChannel
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
		serverCh = NewLibp2pAsyncChannel(libp2pStream, DefaultAsyncChannelConfig())
	}()

	// 客户端打开异步通道
	clientCh, err := client.OpenAsyncChannel(context.Background(), server.Self(), proto)
	if err != nil {
		t.Fatalf("open async channel: %v", err)
	}

	wg.Wait()
	if serverCh == nil {
		t.Fatal("server channel not created")
	}
	defer serverCh.Close()
	defer clientCh.Close()

	// 客户端发送消息
	msg := []byte("hello async")
	select {
	case clientCh.SendChan() <- msg:
	case <-time.After(time.Second):
		t.Fatal("send timeout")
	}

	// 服务端接收消息
	select {
	case recv := <-serverCh.RecvChan():
		if recv.Err != nil {
			t.Fatalf("recv error: %v", recv.Err)
		}
		if string(recv.Msg) != string(msg) {
			t.Errorf("got %q, want %q", recv.Msg, msg)
		}
	case <-time.After(time.Second):
		t.Fatal("recv timeout")
	}

	// 服务端回复
	reply := []byte("reply async")
	select {
	case serverCh.SendChan() <- reply:
	case <-time.After(time.Second):
		t.Fatal("send reply timeout")
	}

	// 客户端接收回复
	select {
	case recv := <-clientCh.RecvChan():
		if recv.Err != nil {
			t.Fatalf("recv reply error: %v", recv.Err)
		}
		if string(recv.Msg) != string(reply) {
			t.Errorf("got %q, want %q", recv.Msg, reply)
		}
	case <-time.After(time.Second):
		t.Fatal("recv reply timeout")
	}
}

// TestAsyncChannel_Burst 测试突发消息
func TestAsyncChannel_Burst(t *testing.T) {
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

	proto := "/nexkv/test/burst/1.0.0"

	var serverCh service.AsyncChannel
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
		serverCh = NewLibp2pAsyncChannel(libp2pStream, DefaultAsyncChannelConfig())
	}()

	clientCh, err := client.OpenAsyncChannel(context.Background(), server.Self(), proto)
	if err != nil {
		t.Fatalf("open async channel: %v", err)
	}

	wg.Wait()
	if serverCh == nil {
		t.Fatal("server channel not created")
	}
	defer serverCh.Close()
	defer clientCh.Close()

	// 发送 100 条消息
	msgCount := 100
	for i := 0; i < msgCount; i++ {
		select {
		case clientCh.SendChan() <- []byte("msg"):
		case <-time.After(time.Second):
			t.Fatalf("send timeout at %d", i)
		}
	}

	// 接收 100 条消息
	received := 0
	timeout := time.After(5 * time.Second)
	for received < msgCount {
		select {
		case <-serverCh.RecvChan():
			received++
		case <-timeout:
			t.Fatalf("only received %d/%d messages", received, msgCount)
		}
	}

	if received != msgCount {
		t.Errorf("received %d, want %d", received, msgCount)
	}
}

// TestAsyncChannel_Close 测试关闭行为
func TestAsyncChannel_Close(t *testing.T) {
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

	proto := "/nexkv/test/close/1.0.0"

	var serverCh service.AsyncChannel
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
		serverCh = NewLibp2pAsyncChannel(libp2pStream, DefaultAsyncChannelConfig())
	}()

	clientCh, err := client.OpenAsyncChannel(context.Background(), server.Self(), proto)
	if err != nil {
		t.Fatalf("open async channel: %v", err)
	}

	wg.Wait()
	if serverCh == nil {
		t.Fatal("server channel not created")
	}

	// 客户端关闭
	if err := clientCh.Close(); err != nil {
		t.Errorf("close error: %v", err)
	}

	// 服务端应收到连接断开（收到错误或 recvCh 关闭）
	select {
	case msg, ok := <-serverCh.RecvChan():
		if ok && msg.Err == nil {
			t.Error("expected error or closed channel")
		}
		// 收到错误或 channel 关闭都是预期行为
	case <-time.After(5 * time.Second):
		t.Error("timeout waiting for recv channel close")
	}

	// 清理
	_ = serverCh.Close()
}

// TestAsyncChannel_Concurrent 测试并发读写
func TestAsyncChannel_Concurrent(t *testing.T) {
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

	proto := "/nexkv/test/concurrent/1.0.0"

	var serverCh service.AsyncChannel
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
		serverCh = NewLibp2pAsyncChannel(libp2pStream, DefaultAsyncChannelConfig())
	}()

	clientCh, err := client.OpenAsyncChannel(context.Background(), server.Self(), proto)
	if err != nil {
		t.Fatalf("open async channel: %v", err)
	}

	wg.Wait()
	if serverCh == nil {
		t.Fatal("server channel not created")
	}
	defer serverCh.Close()
	defer clientCh.Close()

	// 并发发送
	msgCount := 50
	var sendWg sync.WaitGroup
	for i := 0; i < msgCount; i++ {
		sendWg.Add(1)
		go func(idx int) {
			defer sendWg.Done()
			clientCh.SendChan() <- []byte("concurrent msg")
		}(i)
	}

	// 并发接收
	var recvWg sync.WaitGroup
	for i := 0; i < msgCount; i++ {
		recvWg.Add(1)
		go func() {
			defer recvWg.Done()
			<-serverCh.RecvChan()
		}()
	}

	sendWg.Wait()
	recvWg.Wait()
}
