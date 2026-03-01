// Package transport 测试 Libp2pChannel
package transport

import (
	"context"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/service"
)

// ==========================================
// Libp2pChannel 测试
// ==========================================

// TestDefaultChannelConfig 测试默认配置
func TestDefaultChannelConfig(t *testing.T) {
	cfg := DefaultChannelConfig()

	if cfg.ReadTimeout != 5*time.Second {
		t.Errorf("ReadTimeout = %v, want %v", cfg.ReadTimeout, 5*time.Second)
	}
	if cfg.WriteTimeout != 5*time.Second {
		t.Errorf("WriteTimeout = %v, want %v", cfg.WriteTimeout, 5*time.Second)
	}
}

// TestLibp2pChannel_Integration_SendRecv 测试实际连接的发送和接收
func TestLibp2pChannel_Integration_SendRecv(t *testing.T) {
	// 创建两个 transport
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
	_, err = client.Connect(context.Background(), addr)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// 注册协议处理器
	proto := "/test-channel/1.0.0"
	receivedChan := make(chan []byte, 1)
	server.SetStreamHandler(proto, func(stream service.Stream) {
		defer stream.Close()

		// 使用 Channel 接收
		channel := NewLibp2pChannel(stream.(*Libp2pStream), nil)
		data, err := channel.Recv(context.Background())
		if err == nil {
			receivedChan <- data
		}
	})

	// 客户端打开流
	stream, err := client.OpenStream(context.Background(), server.Self(), proto)
	if err != nil {
		t.Fatalf("OpenStream failed: %v", err)
	}
	defer stream.Close()

	// 使用 Channel 发送
	channel := NewLibp2pChannel(stream.(*Libp2pStream), nil)
	testMsg := []byte("Hello from client!")

	err = channel.Send(context.Background(), testMsg)
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	// 等待接收
	select {
	case data := <-receivedChan:
		if string(data) != string(testMsg) {
			t.Errorf("Received = %s, want %s", string(data), string(testMsg))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for message")
	}
}

// TestLibp2pChannel_Close_Idempotent 测试 Close 的幂等性
func TestLibp2pChannel_Close_Idempotent(t *testing.T) {
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
	_, err = client.Connect(context.Background(), addr)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// 打开流
	proto := "/test-channel-close/1.0.0"
	server.SetStreamHandler(proto, func(stream service.Stream) {
		stream.Close()
	})

	stream, err := client.OpenStream(context.Background(), server.Self(), proto)
	if err != nil {
		t.Fatalf("OpenStream failed: %v", err)
	}

	channel := NewLibp2pChannel(stream.(*Libp2pStream), nil)

	// 多次关闭应该不 panic
	channel.Close()
	channel.Close()
	channel.Close()
}

// TestLibp2pChannel_Close_Concurrent 测试并发关闭
func TestLibp2pChannel_Close_Concurrent(t *testing.T) {
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
	_, err = client.Connect(context.Background(), addr)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// 打开流
	proto := "/test-channel-concurrent-close/1.0.0"
	server.SetStreamHandler(proto, func(stream service.Stream) {
		stream.Close()
	})

	stream, err := client.OpenStream(context.Background(), server.Self(), proto)
	if err != nil {
		t.Fatalf("OpenStream failed: %v", err)
	}

	channel := NewLibp2pChannel(stream.(*Libp2pStream), nil)

	// 并发关闭
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			channel.Close()
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

// TestLibp2pChannel_Send_Closed 测试向已关闭的 channel 发送
func TestLibp2pChannel_Send_Closed(t *testing.T) {
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
	_, err = client.Connect(context.Background(), addr)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	proto := "/test-channel-send-closed/1.0.0"
	server.SetStreamHandler(proto, func(stream service.Stream) {
		stream.Close()
	})

	stream, err := client.OpenStream(context.Background(), server.Self(), proto)
	if err != nil {
		t.Fatalf("OpenStream failed: %v", err)
	}

	channel := NewLibp2pChannel(stream.(*Libp2pStream), nil)
	channel.Close()

	// 向已关闭的 channel 发送应该返回错误
	err = channel.Send(context.Background(), []byte("test"))
	if err == nil {
		t.Error("Send() on closed channel should return error")
	}
}

// TestLibp2pChannel_Recv_Closed 测试从已关闭的 channel 接收
func TestLibp2pChannel_Recv_Closed(t *testing.T) {
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
	_, err = client.Connect(context.Background(), addr)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	proto := "/test-channel-recv-closed/1.0.0"
	server.SetStreamHandler(proto, func(stream service.Stream) {
		stream.Close()
	})

	stream, err := client.OpenStream(context.Background(), server.Self(), proto)
	if err != nil {
		t.Fatalf("OpenStream failed: %v", err)
	}

	channel := NewLibp2pChannel(stream.(*Libp2pStream), nil)
	channel.Close()

	// 从已关闭的 channel 接收应该返回错误
	_, err = channel.Recv(context.Background())
	if err == nil {
		t.Error("Recv() on closed channel should return error")
	}
}

// TestLibp2pChannel_Send_CanceledContext 测试使用已取消的 context 发送
func TestLibp2pChannel_Send_CanceledContext(t *testing.T) {
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
	_, err = client.Connect(context.Background(), addr)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	proto := "/test-channel-send-canceled/1.0.0"
	server.SetStreamHandler(proto, func(stream service.Stream) {
		stream.Close()
	})

	stream, err := client.OpenStream(context.Background(), server.Self(), proto)
	if err != nil {
		t.Fatalf("OpenStream failed: %v", err)
	}

	channel := NewLibp2pChannel(stream.(*Libp2pStream), nil)

	// 使用已取消的 context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = channel.Send(ctx, []byte("test"))
	if err == nil {
		t.Error("Send() with canceled context should return error")
	}
}

// TestLibp2pChannel_Recv_CanceledContext 测试使用已取消的 context 接收
func TestLibp2pChannel_Recv_CanceledContext(t *testing.T) {
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
	_, err = client.Connect(context.Background(), addr)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	proto := "/test-channel-recv-canceled/1.0.0"
	server.SetStreamHandler(proto, func(stream service.Stream) {
		stream.Close()
	})

	stream, err := client.OpenStream(context.Background(), server.Self(), proto)
	if err != nil {
		t.Fatalf("OpenStream failed: %v", err)
	}

	channel := NewLibp2pChannel(stream.(*Libp2pStream), nil)

	// 使用已取消的 context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = channel.Recv(ctx)
	if err == nil {
		t.Error("Recv() with canceled context should return error")
	}
}

// TestLibp2pChannel_Send_Recv_Multiple 测试多次发送和接收
func TestLibp2pChannel_Send_Recv_Multiple(t *testing.T) {
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
	_, err = client.Connect(context.Background(), addr)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	proto := "/test-channel-multiple/1.0.0"
	receivedChan := make(chan []byte, 10)
	server.SetStreamHandler(proto, func(stream service.Stream) {
		defer stream.Close()

		channel := NewLibp2pChannel(stream.(*Libp2pStream), nil)
		for i := 0; i < 5; i++ {
			data, err := channel.Recv(context.Background())
			if err == nil {
				receivedChan <- data
			}
		}
	})

	stream, err := client.OpenStream(context.Background(), server.Self(), proto)
	if err != nil {
		t.Fatalf("OpenStream failed: %v", err)
	}
	defer stream.Close()

	channel := NewLibp2pChannel(stream.(*Libp2pStream), nil)

	// 发送多条消息
	messages := [][]byte{
		[]byte("message 1"),
		[]byte("message 2"),
		[]byte("message 3"),
		[]byte("message 4"),
		[]byte("message 5"),
	}

	for _, msg := range messages {
		err = channel.Send(context.Background(), msg)
		if err != nil {
			t.Fatalf("Send failed: %v", err)
		}
	}

	// 接收并验证
	for i := 0; i < 5; i++ {
		select {
		case data := <-receivedChan:
			if string(data) != string(messages[i]) {
				t.Errorf("Received = %s, want %s", string(data), string(messages[i]))
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("Timeout waiting for message %d", i)
		}
	}
}
