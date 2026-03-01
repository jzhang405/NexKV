package transport

import (
	"context"
	stderrors "errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	pkgerrors "github.com/jzhang405/NexKV/pkg/errors"
)

// TestNewLibp2pTransport 测试创建 Transport
func TestNewLibp2pTransport(t *testing.T) {
	ctx := context.Background()
	cfg := &Config{
		ListenAddr:      "/ip4/127.0.0.1/tcp/0",
		EnableDiscovery: false,
	}

	transport, err := NewLibp2pTransport(ctx, cfg)
	if err != nil {
		t.Fatalf("NewLibp2pTransport failed: %v", err)
	}
	defer transport.Close()

	if transport.Self() == "" {
		t.Error("Self() should return non-empty peer ID")
	}
}

// TestTransportSelf 测试 Self 方法
func TestTransportSelf(t *testing.T) {
	transport, err := NewLibp2pTransport(context.Background(), &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("NewLibp2pTransport failed: %v", err)
	}
	defer transport.Close()

	self := transport.Self()
	if self == "" {
		t.Error("Self() should return non-empty peer ID")
	}
}

// TestTransportConnectDisconnect 测试连接和断开
func TestTransportConnectDisconnect(t *testing.T) {
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

	// 构建地址
	addr := server.host.Addrs()[0].String() + "/p2p/" + server.Self().String()

	// 连接
	peerID, err := client.Connect(context.Background(), addr)
	if err != nil && !stderrors.Is(err, pkgerrors.ErrAlreadyConnected) {
		t.Fatalf("Connect failed: %v", err)
	}

	if peerID != server.Self() {
		t.Errorf("Connect returned wrong peer ID: got %s, want %s", peerID, server.Self())
	}

	// 检查连接状态
	if !client.IsConnected(server.Self()) {
		t.Error("IsConnected should return true after Connect")
	}

	// 断开
	err = client.Disconnect(server.Self())
	if err != nil {
		t.Fatalf("Disconnect failed: %v", err)
	}

	// 等待断开
	time.Sleep(100 * time.Millisecond)

	if client.IsConnected(server.Self()) {
		t.Error("IsConnected should return false after Disconnect")
	}
}

// TestTransportConcurrentClose 测试并发关闭
func TestTransportConcurrentClose(t *testing.T) {
	transport, err := NewLibp2pTransport(context.Background(), &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("NewLibp2pTransport failed: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			transport.Close() // 不应 panic
		}()
	}
	wg.Wait()
}

// TestInputValidation 测试输入验证
func TestInputValidation(t *testing.T) {
	transport, err := NewLibp2pTransport(context.Background(), &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("NewLibp2pTransport failed: %v", err)
	}
	defer transport.Close()

	// 测试空 PeerID
	err = transport.Disconnect("")
	if !stderrors.Is(err, pkgerrors.ErrPeerIDInvalid) {
		t.Errorf("should reject empty peer ID, got %v", err)
	}

	// 测试超长 PeerID
	longPeerID := model.PeerID(strings.Repeat("a", 200))
	err = transport.Disconnect(longPeerID)
	if !stderrors.Is(err, pkgerrors.ErrPeerIDInvalid) {
		t.Errorf("should reject oversized peer ID, got %v", err)
	}

	// 测试空地址
	_, err = transport.Connect(context.Background(), "")
	if !stderrors.Is(err, pkgerrors.ErrAddrInvalid) {
		t.Errorf("should reject empty address, got %v", err)
	}

	// 测试超长地址
	longAddr := strings.Repeat("/ip4/127.0.0.1", 200)
	_, err = transport.Connect(context.Background(), longAddr)
	if !stderrors.Is(err, pkgerrors.ErrAddrTooLong) {
		t.Errorf("should reject oversized address, got %v", err)
	}
}

// ==========================================
// Libp2pStream 方法测试（使用实际连接）
// ==========================================

// TestLibp2pStream_Methods 测试 Libp2pStream 的方法
func TestLibp2pStream_Methods(t *testing.T) {
	// 创建两个 transport 并建立连接
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
	if err != nil && !stderrors.Is(err, pkgerrors.ErrAlreadyConnected) {
		t.Fatalf("Connect failed: %v", err)
	}

	// 注册协议处理器
	proto := "/test-stream-methods/1.0.0"
	server.SetStreamHandler(proto, func(stream service.Stream) {
		// 简单的 echo 处理器
		defer stream.Close()
		buf := make([]byte, 1024)
		n, _ := stream.Read(buf)     //nolint:errcheck // test code
		_, _ = stream.Write(buf[:n]) //nolint:errcheck // test code
	})

	// 打开流
	stream, err := client.OpenStream(context.Background(), server.Self(), proto)
	if err != nil {
		t.Fatalf("OpenStream failed: %v", err)
	}
	defer stream.Close()

	libp2pStream, ok := stream.(*Libp2pStream)
	if !ok {
		t.Fatal("Stream is not *Libp2pStream type")
	}

	// 测试 ID 方法
	streamID := libp2pStream.ID()
	if streamID == "" {
		t.Error("ID() should not return empty string")
	}

	// 测试 Protocol 方法
	if libp2pStream.Protocol() != proto {
		t.Errorf("Protocol() = %s, want %s", libp2pStream.Protocol(), proto)
	}

	// 测试 RemotePeer 方法
	remotePeer := libp2pStream.RemotePeer()
	if remotePeer != server.Self() {
		t.Errorf("RemotePeer() = %s, want %s", remotePeer, server.Self())
	}

	// 测试 SetDeadline 方法
	deadline := time.Now().Add(5 * time.Second)
	err = libp2pStream.SetDeadline(deadline)
	if err != nil {
		t.Errorf("SetDeadline() returned error: %v", err)
	}
}

// TestLibp2pStream_Reset 测试 Reset 方法
func TestLibp2pStream_Reset(t *testing.T) {
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
	if err != nil && !stderrors.Is(err, pkgerrors.ErrAlreadyConnected) {
		t.Fatalf("Connect failed: %v", err)
	}

	proto := "/test-stream-reset/1.0.0"
	server.SetStreamHandler(proto, func(stream service.Stream) {
		defer stream.Close()
	})

	stream, err := client.OpenStream(context.Background(), server.Self(), proto)
	if err != nil {
		t.Fatalf("OpenStream failed: %v", err)
	}

	libp2pStream, ok := stream.(*Libp2pStream)
	if !ok {
		t.Fatal("Stream is not *Libp2pStream type")
	}

	// Reset 应该不 panic
	err = libp2pStream.Reset()
	if err != nil {
		t.Errorf("Reset() returned error: %v", err)
	}
}

// TestLibp2pStream_WriteReadWithCodec 测试 WriteWithCodec 和 ReadWithCodec
func TestLibp2pStream_WriteReadWithCodec(t *testing.T) {
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
	if err != nil && !stderrors.Is(err, pkgerrors.ErrAlreadyConnected) {
		t.Fatalf("Connect failed: %v", err)
	}

	proto := "/test-stream-codec/1.0.0"
	receivedChan := make(chan []byte, 1)
	server.SetStreamHandler(proto, func(stream service.Stream) {
		defer stream.Close()

		libp2pStream := stream.(*Libp2pStream)
		codec := &LengthPrefixedCodec{}
		data, err := libp2pStream.ReadWithCodec(codec)
		if err == nil {
			receivedChan <- data
		}
	})

	stream, err := client.OpenStream(context.Background(), server.Self(), proto)
	if err != nil {
		t.Fatalf("OpenStream failed: %v", err)
	}
	defer stream.Close()

	libp2pStream := stream.(*Libp2pStream)
	codec := &LengthPrefixedCodec{}
	testMsg := []byte("test message with codec")

	// 使用 WriteWithCodec 发送
	err = libp2pStream.WriteWithCodec(codec, testMsg)
	if err != nil {
		t.Fatalf("WriteWithCodec() error = %v", err)
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
