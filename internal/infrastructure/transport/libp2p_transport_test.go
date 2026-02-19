package transport

import (
	"context"
	stderrors "errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/pkg/errors"
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
	if err != nil && !stderrors.Is(err, errors.ErrAlreadyConnected) {
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
	if !stderrors.Is(err, errors.ErrPeerIDInvalid) {
		t.Errorf("should reject empty peer ID, got %v", err)
	}

	// 测试超长 PeerID
	longPeerID := model.PeerID(strings.Repeat("a", 200))
	err = transport.Disconnect(longPeerID)
	if !stderrors.Is(err, errors.ErrPeerIDInvalid) {
		t.Errorf("should reject oversized peer ID, got %v", err)
	}

	// 测试空地址
	_, err = transport.Connect(context.Background(), "")
	if !stderrors.Is(err, errors.ErrAddrInvalid) {
		t.Errorf("should reject empty address, got %v", err)
	}

	// 测试超长地址
	longAddr := strings.Repeat("/ip4/127.0.0.1", 200)
	_, err = transport.Connect(context.Background(), longAddr)
	if !stderrors.Is(err, errors.ErrAddrTooLong) {
		t.Errorf("should reject oversized address, got %v", err)
	}
}
