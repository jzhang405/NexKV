package service

import (
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// TestTransport_PeerID 测试 PeerID 类型
func TestTransport_PeerID(t *testing.T) {
	peer := model.PeerID("test-node")
	if peer.String() != "test-node" {
		t.Errorf("Expected PeerID to be 'test-node', got %s", peer.String())
	}
}

// TestDefaultRPCConfig 测试默认 RPC 配置
func TestDefaultRPCConfig(t *testing.T) {
	config := DefaultRPCConfig()

	if config == nil {
		t.Fatal("DefaultRPCConfig should not return nil")
		return //nolint:govet // unreachable, satisfies staticcheck SA5011
	}

	// 验证默认值
	if config.CallTimeout != 30*time.Second {
		t.Errorf("CallTimeout: got %v, want %v", config.CallTimeout, 30*time.Second)
	}
	if config.BroadcastTimeout != 60*time.Second {
		t.Errorf("BroadcastTimeout: got %v, want %v", config.BroadcastTimeout, 60*time.Second)
	}
	if config.ConnectTimeout != 10*time.Second {
		t.Errorf("ConnectTimeout: got %v, want %v", config.ConnectTimeout, 10*time.Second)
	}
	if config.MaxRetries != 3 {
		t.Errorf("MaxRetries: got %d, want %d", config.MaxRetries, 3)
	}
	if config.MaxConcurrentCalls != 1000 {
		t.Errorf("MaxConcurrentCalls: got %d, want %d", config.MaxConcurrentCalls, 1000)
	}
	if config.RequestBufferSize != 256 {
		t.Errorf("RequestBufferSize: got %d, want %d", config.RequestBufferSize, 256)
	}
}
