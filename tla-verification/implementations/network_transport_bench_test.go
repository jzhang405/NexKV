package implementations

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"

	pb "github.com/jzhang405/NexKV/proto"
)

// BenchmarkNetworkTransportClientCreation 测试客户端创建性能（优化后）
func BenchmarkNetworkTransportClientCreation(b *testing.B) {
	peers := map[string]string{
		"n1": "localhost:5001",
		"n2": "localhost:5002",
	}

	nt, err := CreateNetworkTransport("n1", peers)
	if err != nil {
		b.Fatalf("Failed to create network transport: %v", err)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// 重置客户端缓存以测试创建性能
		nt.mu.Lock()
		nt.clients = make(map[string]pb.MetadataTransportClient)
		nt.connPool = make(map[string]*grpc.ClientConn)
		nt.mu.Unlock()
		b.StartTimer()

		_, err := nt.getOrCreateClient("n2")
		if err != nil {
			// 连接失败是正常的（目标节点不存在），但我们测试的是创建速度
			b.Logf("Client creation error (expected): %v", err)
		}
	}
}

// BenchmarkNetworkTransportSendWithCachedClient 测试使用缓存客户端的发送性能
func BenchmarkNetworkTransportSendWithCachedClient(b *testing.B) {
	peers := map[string]string{
		"n1": "localhost:6001",
		"n2": "localhost:6002",
	}

	nt, err := CreateNetworkTransport("n1", peers)
	if err != nil {
		b.Fatalf("Failed to create network transport: %v", err)
	}

	// 预先创建客户端（连接会异步建立）
	nt.mu.Lock()
	nt.clients = make(map[string]pb.MetadataTransportClient)
	nt.connPool = make(map[string]*grpc.ClientConn)
	nt.mu.Unlock()

	msg := Message{
		Type:      Heartbeat,
		From:      "n1",
		To:        "n2",
		Timestamp: time.Now().UnixNano(),
		Payload:   make([]byte, 1024),
		Context:   context.Background(),
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// 第一次会创建客户端，后续使用缓存
		_ = nt.Send("n2", msg)
	}
}


