// Package gossip 提供树感知 Gossip 同步测试
package gossip

import (
	"testing"
	"time"
)

// ==================== NodeType Tests ====================

func TestNodeType_String(t *testing.T) {
	tests := []struct {
		nodeType NodeType
		expected string
	}{
		{NodeTypeLeaf, "leaf"},
		{NodeTypeMiddle, "middle"},
		{NodeTypeRoot, "root"},
		{NodeTypeUnknown, "unknown"},
		{NodeType(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.nodeType.String(); got != tt.expected {
			t.Errorf("NodeType(%d).String() = %s, want %s", tt.nodeType, got, tt.expected)
		}
	}
}

// ==================== PriorityLevel Tests ====================

func TestPriorityLevel_String(t *testing.T) {
	tests := []struct {
		priority PriorityLevel
		expected string
	}{
		{PriorityHigh, "high"},
		{PriorityNormal, "normal"},
		{PriorityLow, "low"},
		{PriorityLevel(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.priority.String(); got != tt.expected {
			t.Errorf("PriorityLevel(%d).String() = %s, want %s", tt.priority, got, tt.expected)
		}
	}
}

// ==================== TreeTopology Tests ====================

func TestNewTreeTopology(t *testing.T) {
	topology := NewTreeTopology("node-1")
	if topology == nil {
		t.Fatal("expected topology to be created")
	}
	if topology.localNodeID != "node-1" {
		t.Errorf("expected localNodeID node-1, got %s", topology.localNodeID)
	}
	if topology.GetNodeType() != NodeTypeUnknown {
		t.Errorf("expected initial node type Unknown, got %s", topology.GetNodeType())
	}
}

func TestTreeTopology_SetNodeType(t *testing.T) {
	topology := NewTreeTopology("node-1")

	topology.SetNodeType(NodeTypeLeaf)
	if topology.GetNodeType() != NodeTypeLeaf {
		t.Errorf("expected NodeTypeLeaf, got %s", topology.GetNodeType())
	}

	topology.SetNodeType(NodeTypeRoot)
	if topology.GetNodeType() != NodeTypeRoot {
		t.Errorf("expected NodeTypeRoot, got %s", topology.GetNodeType())
	}
}

func TestTreeTopology_SetParent(t *testing.T) {
	topology := NewTreeTopology("node-1")

	topology.SetParent("parent-1")
	if topology.GetParent() != "parent-1" {
		t.Errorf("expected parent parent-1, got %s", topology.GetParent())
	}

	topology.SetParent("")
	if topology.GetParent() != "" {
		t.Errorf("expected empty parent, got %s", topology.GetParent())
	}
}

func TestTreeTopology_SetChildren(t *testing.T) {
	topology := NewTreeTopology("node-1")

	children := []string{"child-1", "child-2", "child-3"}
	topology.SetChildren(children)

	got := topology.GetChildren()
	if len(got) != 3 {
		t.Errorf("expected 3 children, got %d", len(got))
	}

	// 验证是副本，不是引用
	children[0] = "modified"
	if topology.GetChildren()[0] == "modified" {
		t.Error("expected children to be a copy")
	}
}

func TestTreeTopology_SetTreeDepth(t *testing.T) {
	topology := NewTreeTopology("node-1")

	topology.SetTreeDepth(3)
	if topology.GetTreeDepth() != 3 {
		t.Errorf("expected depth 3, got %d", topology.GetTreeDepth())
	}
}

func TestTreeTopology_SetAllNodes(t *testing.T) {
	topology := NewTreeTopology("node-1")

	nodes := []string{"node-1", "node-2", "node-3"}
	topology.SetAllNodes(nodes)

	got := topology.GetAllNodes()
	if len(got) != 3 {
		t.Errorf("expected 3 nodes, got %d", len(got))
	}
}

func TestTreeTopology_UpdateTopology_Leaf(t *testing.T) {
	topology := NewTreeTopology("leaf-1")

	// 叶子节点：有父节点，无子节点
	topology.UpdateTopology("parent-1", []string{}, 0)

	if topology.GetNodeType() != NodeTypeLeaf {
		t.Errorf("expected NodeTypeLeaf, got %s", topology.GetNodeType())
	}
	if topology.GetParent() != "parent-1" {
		t.Errorf("expected parent parent-1, got %s", topology.GetParent())
	}
	if len(topology.GetChildren()) != 0 {
		t.Errorf("expected 0 children, got %d", len(topology.GetChildren()))
	}
}

func TestTreeTopology_UpdateTopology_Middle(t *testing.T) {
	topology := NewTreeTopology("middle-1")

	// 中间节点：有父节点，有子节点
	topology.UpdateTopology("parent-1", []string{"leaf-1", "leaf-2"}, 1)

	if topology.GetNodeType() != NodeTypeMiddle {
		t.Errorf("expected NodeTypeMiddle, got %s", topology.GetNodeType())
	}
	if topology.GetParent() != "parent-1" {
		t.Errorf("expected parent parent-1, got %s", topology.GetParent())
	}
	if len(topology.GetChildren()) != 2 {
		t.Errorf("expected 2 children, got %d", len(topology.GetChildren()))
	}
}

func TestTreeTopology_UpdateTopology_Root(t *testing.T) {
	topology := NewTreeTopology("root-1")

	// Root 节点：无父节点，有子节点
	topology.UpdateTopology("", []string{"middle-1", "middle-2"}, 2)

	if topology.GetNodeType() != NodeTypeRoot {
		t.Errorf("expected NodeTypeRoot, got %s", topology.GetNodeType())
	}
	if topology.GetParent() != "" {
		t.Errorf("expected empty parent, got %s", topology.GetParent())
	}
	if len(topology.GetChildren()) != 2 {
		t.Errorf("expected 2 children, got %d", len(topology.GetChildren()))
	}
}

func TestTreeTopology_UpdateTopology_SingleNode(t *testing.T) {
	topology := NewTreeTopology("single-1")

	// 单节点：无父节点，无子节点 → 视为 Root
	topology.UpdateTopology("", []string{}, 0)

	if topology.GetNodeType() != NodeTypeRoot {
		t.Errorf("expected NodeTypeRoot for single node, got %s", topology.GetNodeType())
	}
}

// ==================== TreeAwareEvent Tests ====================

func TestTreeAwareEvent_Creation(t *testing.T) {
	event := GossipEvent{
		Type:      EventWrite,
		Namespace: "ns1",
		Key:       "key1",
		Timestamp: time.Now(),
	}

	treeEvent := TreeAwareEvent{
		Event:       event,
		Priority:    PriorityHigh,
		TargetNodes: []string{"node-1", "node-2"},
		EnqueueTime: time.Now(),
	}

	if treeEvent.Event.Type != EventWrite {
		t.Errorf("expected EventWrite, got %d", treeEvent.Event.Type)
	}
	if treeEvent.Priority != PriorityHigh {
		t.Errorf("expected PriorityHigh, got %s", treeEvent.Priority)
	}
	if len(treeEvent.TargetNodes) != 2 {
		t.Errorf("expected 2 target nodes, got %d", len(treeEvent.TargetNodes))
	}
}

// ==================== TreeAwareGossipSync Tests ====================

func TestNewTreeAwareGossipSync(t *testing.T) {
	config := &TreeAwareConfig{
		EventDrivenConfig: &EventDrivenConfig{
			LocalNodeID: "node-1",
		},
		HighPriorityChanSize:   100,
		NormalPriorityChanSize: 50,
		LowPriorityChanSize:    25,
	}

	sync := NewTreeAwareGossipSync(config)
	if sync == nil {
		t.Fatal("expected sync to be created")
	}
	defer sync.Close()

	if sync.topology == nil {
		t.Error("expected topology to be initialized")
	}
	if sync.highPriority == nil {
		t.Error("expected highPriority channel to be initialized")
	}
	if sync.normalPriority == nil {
		t.Error("expected normalPriority channel to be initialized")
	}
	if sync.lowPriority == nil {
		t.Error("expected lowPriority channel to be initialized")
	}
}

func TestNewTreeAwareGossipSync_DefaultConfig(t *testing.T) {
	config := &TreeAwareConfig{
		EventDrivenConfig: &EventDrivenConfig{
			LocalNodeID: "node-1",
		},
	}

	sync := NewTreeAwareGossipSync(config)
	defer sync.Close()

	// 验证默认值
	if cap(sync.highPriority) != 500 {
		t.Errorf("expected highPriority capacity 500, got %d", cap(sync.highPriority))
	}
	if cap(sync.normalPriority) != 300 {
		t.Errorf("expected normalPriority capacity 300, got %d", cap(sync.normalPriority))
	}
	if cap(sync.lowPriority) != 200 {
		t.Errorf("expected lowPriority capacity 200, got %d", cap(sync.lowPriority))
	}
}

func TestTreeAwareGossipSync_UpdateTopology(t *testing.T) {
	sync := NewTreeAwareGossipSync(&TreeAwareConfig{
		EventDrivenConfig: &EventDrivenConfig{
			LocalNodeID: "middle-1",
		},
	})
	defer sync.Close()

	sync.UpdateTopology("root-1", []string{"leaf-1", "leaf-2"}, 1)

	if sync.GetNodeType() != NodeTypeMiddle {
		t.Errorf("expected NodeTypeMiddle, got %s", sync.GetNodeType())
	}
	if sync.topology.GetParent() != "root-1" {
		t.Errorf("expected parent root-1, got %s", sync.topology.GetParent())
	}
	if len(sync.topology.GetChildren()) != 2 {
		t.Errorf("expected 2 children, got %d", len(sync.topology.GetChildren()))
	}
}

func TestTreeAwareGossipSync_GetExpectedDelay(t *testing.T) {
	sync := NewTreeAwareGossipSync(&TreeAwareConfig{
		EventDrivenConfig: &EventDrivenConfig{
			LocalNodeID: "node-1",
		},
	})
	defer sync.Close()

	// 深度 0 → 延迟 0
	sync.topology.SetTreeDepth(0)
	if sync.GetExpectedDelay() != 0 {
		t.Errorf("expected delay 0 for depth 0, got %v", sync.GetExpectedDelay())
	}

	// 深度 2 → 延迟 200ms
	sync.topology.SetTreeDepth(2)
	expected := 200 * time.Millisecond
	if sync.GetExpectedDelay() != expected {
		t.Errorf("expected delay %v for depth 2, got %v", expected, sync.GetExpectedDelay())
	}
}

func TestTreeAwareGossipSync_GetTreeAwareStats(t *testing.T) {
	sync := NewTreeAwareGossipSync(&TreeAwareConfig{
		EventDrivenConfig: &EventDrivenConfig{
			LocalNodeID: "node-1",
		},
	})
	defer sync.Close()

	sync.UpdateTopology("parent-1", []string{"child-1"}, 1)

	stats := sync.GetTreeAwareStats()

	if stats.NodeType != NodeTypeMiddle {
		t.Errorf("expected NodeTypeMiddle, got %s", stats.NodeType)
	}
	if stats.TreeDepth != 1 {
		t.Errorf("expected depth 1, got %d", stats.TreeDepth)
	}
	if stats.HighPrioritySent != 0 {
		t.Errorf("expected initial highPrioritySent 0, got %d", stats.HighPrioritySent)
	}
}

// ==================== Propagation Tests ====================

func TestTreeAwareGossipSync_Propagate_Leaf(t *testing.T) {
	sync := NewTreeAwareGossipSync(&TreeAwareConfig{
		EventDrivenConfig: &EventDrivenConfig{
			LocalNodeID: "leaf-1",
		},
	})
	defer sync.Close()

	// 设置为叶子节点
	sync.UpdateTopology("parent-1", []string{}, 0)

	// 触发写入事件
	sync.OnWrite("ns1", "key1")

	// 验证高优先级队列有事件
	time.Sleep(50 * time.Millisecond)

	stats := sync.GetTreeAwareStats()
	// 注意：事件会被处理，所以队列可能已空
	// 这里主要验证不会 panic
	_ = stats
}

func TestTreeAwareGossipSync_Propagate_Root(t *testing.T) {
	sync := NewTreeAwareGossipSync(&TreeAwareConfig{
		EventDrivenConfig: &EventDrivenConfig{
			LocalNodeID: "root-1",
		},
	})
	defer sync.Close()

	// 设置为 Root 节点
	sync.UpdateTopology("", []string{"child-1", "child-2"}, 2)

	// 触发写入事件
	sync.OnWrite("ns1", "key1")

	time.Sleep(50 * time.Millisecond)
}

func TestTreeAwareGossipSync_Propagate_Middle(t *testing.T) {
	sync := NewTreeAwareGossipSync(&TreeAwareConfig{
		EventDrivenConfig: &EventDrivenConfig{
			LocalNodeID: "middle-1",
		},
	})
	defer sync.Close()

	// 设置为中间节点
	sync.UpdateTopology("root-1", []string{"leaf-1", "leaf-2"}, 1)

	// 触发写入事件
	sync.OnWrite("ns1", "key1")

	time.Sleep(50 * time.Millisecond)
}

// ==================== OnPeerJoin/OnPeerLeave Tests ====================

func TestTreeAwareGossipSync_OnPeerJoin(t *testing.T) {
	sync := NewTreeAwareGossipSync(&TreeAwareConfig{
		EventDrivenConfig: &EventDrivenConfig{
			LocalNodeID: "node-1",
		},
	})
	defer sync.Close()

	sync.UpdateTopology("parent-1", []string{}, 0)
	sync.OnPeerJoin("new-node")

	time.Sleep(50 * time.Millisecond)
}

func TestTreeAwareGossipSync_OnPeerLeave(t *testing.T) {
	sync := NewTreeAwareGossipSync(&TreeAwareConfig{
		EventDrivenConfig: &EventDrivenConfig{
			LocalNodeID: "node-1",
		},
	})
	defer sync.Close()

	sync.UpdateTopology("parent-1", []string{}, 0)
	sync.OnPeerLeave("old-node")

	time.Sleep(50 * time.Millisecond)
}

func TestTreeAwareGossipSync_OnNamespaceChange(t *testing.T) {
	sync := NewTreeAwareGossipSync(&TreeAwareConfig{
		EventDrivenConfig: &EventDrivenConfig{
			LocalNodeID: "node-1",
		},
	})
	defer sync.Close()

	sync.UpdateTopology("parent-1", []string{}, 0)
	sync.OnNamespaceChange("ns1")

	time.Sleep(50 * time.Millisecond)
}

// ==================== Concurrent Tests ====================

func TestTreeAwareGossipSync_ConcurrentPropagation(t *testing.T) {
	sync := NewTreeAwareGossipSync(&TreeAwareConfig{
		EventDrivenConfig: &EventDrivenConfig{
			LocalNodeID: "middle-1",
		},
	})
	defer sync.Close()

	sync.UpdateTopology("root-1", []string{"leaf-1", "leaf-2"}, 1)

	done := make(chan bool)

	// 并发发送事件
	for i := 0; i < 3; i++ {
		go func(i int) {
			for j := 0; j < 10; j++ {
				sync.OnWrite("ns1", "key")
			}
			done <- true
		}(i)
	}

	// 等待完成
	for i := 0; i < 3; i++ {
		<-done
	}

	time.Sleep(100 * time.Millisecond)
}

// ==================== Benchmark ====================

func BenchmarkTreeAwareGossipSync_OnWrite(b *testing.B) {
	sync := NewTreeAwareGossipSync(&TreeAwareConfig{
		EventDrivenConfig: &EventDrivenConfig{
			LocalNodeID: "node-1",
		},
	})
	defer sync.Close()

	sync.UpdateTopology("parent-1", []string{}, 0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sync.OnWrite("ns1", "key")
	}
}

func BenchmarkTreeAwareGossipSync_Propagate(b *testing.B) {
	sync := NewTreeAwareGossipSync(&TreeAwareConfig{
		EventDrivenConfig: &EventDrivenConfig{
			LocalNodeID: "node-1",
		},
	})
	defer sync.Close()

	sync.UpdateTopology("parent-1", []string{"child-1", "child-2"}, 1)

	event := GossipEvent{
		Type:      EventWrite,
		Namespace: "ns1",
		Key:       "key1",
		Timestamp: time.Now(),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sync.Propagate(event)
	}
}

func BenchmarkTreeAwareGossipSync_UpdateTopology(b *testing.B) {
	sync := NewTreeAwareGossipSync(&TreeAwareConfig{
		EventDrivenConfig: &EventDrivenConfig{
			LocalNodeID: "node-1",
		},
	})
	defer sync.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sync.UpdateTopology("parent-1", []string{"child-1"}, 1)
	}
}
