package implementations

import (
	"fmt"
	"os"
	"testing"
)

// TransportTestCase Transport 测试用例
type TransportTestCase struct {
	Name      string
	Transport Transport
	NodeType  string // "quorum" or "twophasecommit"
}

// RunTestsWithAllTransports 在所有 Transport 类型下运行测试
//
// 使用方法：
//
//	func TestTC001_MyTest(t *testing.T) {
//	    RunTestsWithAllTransports(t, "quorum", func(t *testing.T, transport Transport) {
//	        // 原有的测试逻辑
//	        nodes := createNodesWithTransport(transport, ...)
//	        // ... 测试代码
//	    })
//	}
func RunTestsWithAllTransports(t *testing.T, nodeType string, testFunc func(*testing.T, Transport)) {
	transportTypes := []struct {
		name   string
		create func(nodeIDs []string) []Transport
	}{
		{
			name: "Null",
			create: createNullTransports,
		},
		{
			name: "Memory",
			create: createMemoryTransports,
		},
		{
			name: "GRPC",
			create: createNetworkTransports,
		},
	}

	for _, tt := range transportTypes {
		t.Run(tt.name, func(t *testing.T) {
			// 创建 Transport
			nodeIDs := []string{"n1", "n2", "n3"}
			transports := tt.create(nodeIDs)

			// 确保测试后清理
			defer func() {
				for _, transport := range transports {
					_ = transport.Stop()
				}
			}()

			// 启动所有 Transport
			for _, transport := range transports {
				if err := transport.Start(); err != nil {
					t.Fatalf("Failed to start transport: %v", err)
				}
			}

			// 运行测试函数
			testFunc(t, transports[0]) // 传入第一个 Transport
		})
	}
}

// SkipIfTransportType 跳过指定的 Transport 类型
//
// 使用方法：
//
//	if SkipIfTransportType(t, "Network") {
//	    return
//	}
func SkipIfTransportType(t *testing.T, transportType string) bool {
	currentType := os.Getenv("TRANSPORT_TYPE")
	if currentType == transportType {
		t.Skipf("Skipping for transport type: %s", transportType)
		return true
	}
	return false
}

// createNullTransports 创建 Null Transport 列表
func createNullTransports(nodeIDs []string) []Transport {
	transports := make([]Transport, len(nodeIDs))

	peers := make(map[string]string)
	for _, id := range nodeIDs {
		peers[id] = "direct://" + id
	}

	// 第一遍：创建所有 transport
	for i, id := range nodeIDs {
		dst := NewNullTransport(id, peers)
		transports[i] = dst
	}

	// 第二遍：创建所有节点并注册到每个 transport
	nodes := make([]*Node, len(nodeIDs))
	for i, id := range nodeIDs {
		nodes[i] = NewNode(id)
	}

	// 将所有节点注册到每个 transport
	for i := range transports {
		dst := transports[i].(*NullTransport)
		for _, node := range nodes {
			dst.RegisterNode(node)
		}
	}

	return transports
}

// createMemoryTransports 创建 Memory Transport 列表
func createMemoryTransports(nodeIDs []string) []Transport {
	transports := make([]Transport, len(nodeIDs))

	// 创建临时目录
	tempDir, err := os.MkdirTemp("", "nexkv-test-*")
	if err != nil {
		panic(fmt.Sprintf("Failed to create temp dir: %v", err))
	}
	defer os.RemoveAll(tempDir)

	cluster := NewCluster(nodeIDs, tempDir)

	for i, id := range nodeIDs {
		mt, err := CreateMemoryTransport(id, cluster)
		if err != nil {
			panic(fmt.Sprintf("Failed to create memory transport: %v", err))
		}
		transports[i] = mt
	}

	return transports
}

// createNetworkTransports 创建 Network Transport 列表
func createNetworkTransports(nodeIDs []string) []Transport {
	transports := make([]Transport, len(nodeIDs))

	peers := make(map[string]string)
	for i, id := range nodeIDs {
		// 使用不同的端口避免冲突
		port := 7000 + i
		peers[id] = fmt.Sprintf("localhost:%d", port)
	}

	for i, id := range nodeIDs {
		gt, err := CreateGRPCTransport(id, peers)
		if err != nil {
			panic(fmt.Sprintf("Failed to create gRPC transport: %v", err))
		}
		transports[i] = gt
	}

	return transports
}

// CreateNodesWithTransport 使用 Transport 创建节点（辅助函数）
func CreateNodesWithTransport(nodeType string, nodeIDs []string, transport Transport) interface{} {
	switch nodeType {
	case "quorum":
		return createQuorumNodesWithTransport(nodeIDs, transport)

	case "twophasecommit":
		return createTwoPhaseCommitNodesWithTransport(nodeIDs, transport)

	default:
		panic(fmt.Sprintf("Unknown node type: %s", nodeType))
	}
}

// createQuorumNodesWithTransport 创建使用 Transport 的 Quorum 节点
func createQuorumNodesWithTransport(nodeIDs []string, transport Transport) []*Node {
	nodes := make([]*Node, len(nodeIDs))

	// NullTransport 特殊处理
	// 节点已经在 createNullTransports 中创建并注册
	if dst, ok := transport.(*NullTransport); ok {
		// 从 transport 的 nodes 映射中获取已注册的节点
		dst.mu.RLock()
		for i, id := range nodeIDs {
			if node, exists := dst.nodes[id]; exists {
				nodes[i] = node
			} else {
				dst.mu.RUnlock()
				panic(fmt.Sprintf("Node %s not found in NullTransport", id))
			}
		}
		dst.mu.RUnlock()
		return nodes
	}

	// 其他 Transport 类型（Memory, Network）
	// 为每个节点创建适配器
	for i, id := range nodeIDs {
		node := NewNode(id)
		adapter := NewTransportNodeAdapter(node, transport)
		if err := adapter.Start(); err != nil {
			panic(fmt.Sprintf("Failed to start adapter for node %s: %v", id, err))
		}
		nodes[i] = node
	}

	return nodes
}

// createTwoPhaseCommitNodesWithTransport 创建使用 Transport 的 2PC 节点
func createTwoPhaseCommitNodesWithTransport(nodeIDs []string, transport Transport) []*TwoPhaseCommitNode {
	nodes := make([]*TwoPhaseCommitNode, len(nodeIDs))

	for i, id := range nodeIDs {
		node := NewTwoPhaseCommitNode(id)

		// 创建适配器连接 Transport
		adapter := NewTransportNodeAdapter(node, transport)
		if err := adapter.Start(); err != nil {
			panic(fmt.Sprintf("Failed to start adapter for node %s: %v", id, err))
		}

		nodes[i] = node
	}

	return nodes
}
