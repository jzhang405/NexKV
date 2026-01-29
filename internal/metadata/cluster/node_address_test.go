// Package cluster 的 PR-033 扩展功能单元测试
//
// 测试覆盖 PR-033 新增的方法和功能：
// - NodeAddress.Validate() - 端口验证
// - NodeAddress.GetTCPAddr() - TCP 地址组装
// - NodeAddress.GetUDPAddr() - UDP 地址组装
// - NewNodeAddress() - 工厂方法
// - Node.Validate() - 节点验证
// - Node 辅助方法 - IsLeaf, IsParent, IsParentStandby, IsOnline
package cluster

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"
)

// ============================================================================
// NodeAddress 扩展测试 (UT-NODE-002 ~ UT-NODE-004)
// ============================================================================

// Test_NodeAddress_Validate_TCPRange UT-NODE-002: NodeAddress Validate - TCP 范围
func Test_NodeAddress_Validate_TCPRange(t *testing.T) {
	tests := []struct {
		name    string
		addr    NodeAddress
		wantErr bool
		errMsg  string
	}{
		{
			name: "错误 - TCP 端口过小（1023）",
			addr: NodeAddress{
				Host:    "192.168.1.100",
				TCPPort: 1023,
				UDPPort: 1024,
			},
			wantErr: true,
			errMsg:  "TCPPort must be in range",
		},
		{
			name: "错误 - TCP 端口过大（65535）",
			addr: NodeAddress{
				Host:    "192.168.1.100",
				TCPPort: 65535,
				UDPPort: 0,
			},
			wantErr: true,
			errMsg:  "TCPPort must be in range",
		},
		{
			name: "正常 - TCP 端口边界值（1024）",
			addr: NodeAddress{
				Host:    "192.168.1.100",
				TCPPort: 1024,
				UDPPort: 0,
			},
			wantErr: false,
		},
		{
			name: "正常 - TCP 端口边界值（65534）",
			addr: NodeAddress{
				Host:    "192.168.1.100",
				TCPPort: 65534,
				UDPPort: 0,
			},
			wantErr: false,
		},
		{
			name: "正常 - TCP 端口标准值（9000）",
			addr: NodeAddress{
				Host:    "192.168.1.100",
				TCPPort: 9000,
				UDPPort: 0,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.addr.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Test_NodeAddress_Validate_UDPRule UT-NODE-003: NodeAddress Validate - UDP 规则
func Test_NodeAddress_Validate_UDPRule(t *testing.T) {
	tests := []struct {
		name    string
		addr    NodeAddress
		wantErr bool
		errMsg  string
	}{
		{
			name: "错误 - UDP != TCP + 1",
			addr: NodeAddress{
				Host:    "192.168.1.100",
				TCPPort: 9000,
				UDPPort: 9002, // 应该是 9001
			},
			wantErr: true,
			errMsg:  "UDPPort must equal TCPPort + 1",
		},
		{
			name: "错误 - UDP 端口过小",
			addr: NodeAddress{
				Host:    "192.168.1.100",
				TCPPort: 9000,
				UDPPort: 1023,
			},
			wantErr: true,
			errMsg:  "UDPPort must be in range",
		},
		{
			name: "正常 - UDP = TCP + 1",
			addr: NodeAddress{
				Host:    "192.168.1.100",
				TCPPort: 9000,
				UDPPort: 9001,
			},
			wantErr: false,
		},
		{
			name: "正常 - 仅设置 TCP 端口",
			addr: NodeAddress{
				Host:    "192.168.1.100",
				TCPPort: 9000,
				UDPPort: 0, // 未设置
			},
			wantErr: false,
		},
		{
			name: "正常 - 仅设置 UDP 端口",
			addr: NodeAddress{
				Host:    "192.168.1.100",
				TCPPort: 0, // 未设置
				UDPPort: 9001,
			},
			wantErr: false,
		},
		{
			name: "错误 - 两个端口都未设置",
			addr: NodeAddress{
				Host:    "192.168.1.100",
				TCPPort: 0,
				UDPPort: 0,
			},
			wantErr: true,
			errMsg:  "at least one port",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.addr.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Test_NodeAddress_Validate_Normal UT-NODE-004: NodeAddress Validate - 正常
func Test_NodeAddress_Validate_Normal(t *testing.T) {
	addr := NodeAddress{
		Host:    "192.168.1.100",
		TCPPort: 9000,
		UDPPort: 9001,
	}

	err := addr.Validate()
	assert.NoError(t, err)
}

// Test_NodeAddress_GetTCPAddr UT-NODE-005: GetTCPAddr - 有 Host
func Test_NodeAddress_GetTCPAddr_WithHost(t *testing.T) {
	addr := NodeAddress{
		Host:    "192.168.1.100",
		TCPPort: 9000,
		UDPPort: 9001,
	}

	tcpAddr := addr.GetTCPAddr()
	assert.Equal(t, "192.168.1.100:9000", tcpAddr)
}

// Test_NodeAddress_GetTCPAddr_WithoutHost UT-NODE-006: GetTCPAddr - 无 Host
func Test_NodeAddress_GetTCPAddr_WithoutHost(t *testing.T) {
	addr := NodeAddress{
		Host:    "",
		TCPPort: 9000,
		UDPPort: 0,
	}

	tcpAddr := addr.GetTCPAddr()
	assert.Equal(t, ":9000", tcpAddr)
}

// Test_NodeAddress_GetUDPAddr_WithHost UT-NODE-007: GetUDPAddr - 有 Host
func Test_NodeAddress_GetUDPAddr_WithHost(t *testing.T) {
	addr := NodeAddress{
		Host:    "127.0.0.1",
		TCPPort: 9000,
		UDPPort: 9001,
	}

	udpAddr := addr.GetUDPAddr()
	assert.Equal(t, "127.0.0.1:9001", udpAddr)
}

// Test_NodeAddress_GetUDPAddr_WithoutHost UT-NODE-006: GetUDPAddr - 无 Host
func Test_NodeAddress_GetUDPAddr_WithoutHost(t *testing.T) {
	addr := NodeAddress{
		Host:    "",
		TCPPort: 0,
		UDPPort: 9001,
	}

	udpAddr := addr.GetUDPAddr()
	assert.Equal(t, ":9001", udpAddr)
}

// ============================================================================
// Node 扩展测试
// ============================================================================

// Test_Node_GetTCPAddr 测试 Node.GetTCPAddr()
func Test_Node_GetTCPAddr(t *testing.T) {
	node := &Node{
		NodeID: "node-leaf-1",
		HostID: "server-1",
		Addr: NodeAddress{
			Host:    "192.168.1.100",
			TCPPort: 9000,
			UDPPort: 9001,
		},
		Role: Leaf,
	}

	tcpAddr := node.GetTCPAddr()
	assert.Equal(t, "192.168.1.100:9000", tcpAddr)
}

// Test_Node_GetUDPAddr 测试 Node.GetUDPAddr()
func Test_Node_GetUDPAddr(t *testing.T) {
	node := &Node{
		NodeID: "node-leaf-1",
		HostID: "server-1",
		Addr: NodeAddress{
			Host:    "192.168.1.100",
			TCPPort: 9000,
			UDPPort: 9001,
		},
		Role: Leaf,
	}

	udpAddr := node.GetUDPAddr()
	assert.Equal(t, "192.168.1.100:9001", udpAddr)
}

// Test_Node_Validate 测试 Node.Validate()
func Test_Node_Validate(t *testing.T) {
	tests := []struct {
		name    string
		node    *Node
		wantErr bool
		errMsg  string
	}{
		{
			name: "正常 - 完整 Node",
			node: &Node{
				NodeID: "node-leaf-1",
				HostID: "server-1",
				Addr: NodeAddress{
					Host:    "192.168.1.100",
					TCPPort: 9000,
					UDPPort: 9001,
				},
				Role: Leaf,
			},
			wantErr: false,
		},
		{
			name: "错误 - NodeID 为空",
			node: &Node{
				NodeID: "",
				HostID: "server-1",
				Addr: NodeAddress{
					Host:    "192.168.1.100",
					TCPPort: 9000,
					UDPPort: 9001,
				},
				Role: Leaf,
			},
			wantErr: true,
			errMsg:  "NodeID is required",
		},
		{
			name: "错误 - HostID 为空",
			node: &Node{
				NodeID: "node-leaf-1",
				HostID: "",
				Addr: NodeAddress{
					Host:    "192.168.1.100",
					TCPPort: 9000,
					UDPPort: 9001,
				},
				Role: Leaf,
			},
			wantErr: true,
			errMsg:  "HostID is required",
		},
		{
			name: "错误 - 无效的 Addr",
			node: &Node{
				NodeID: "node-leaf-1",
				HostID: "server-1",
				Addr: NodeAddress{
					Host:    "192.168.1.100",
					TCPPort: 1023, // 无效端口
					UDPPort: 0,
				},
				Role: Leaf,
			},
			wantErr: true,
			errMsg:  "invalid Addr",
		},
		{
			name: "错误 - 无效的 NodeRole",
			node: &Node{
				NodeID: "node-leaf-1",
				HostID: "server-1",
				Addr: NodeAddress{
					Host:    "192.168.1.100",
					TCPPort: 9000,
					UDPPort: 9001,
				},
				Role: NodeRole(99), // 无效角色
			},
			wantErr: true,
			errMsg:  "invalid NodeRole",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.node.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Test_Node_RoleHelpers 测试 Node 角色辅助方法
func Test_Node_RoleHelpers(t *testing.T) {
	leafNode := &Node{Role: Leaf}
	parentNode := &Node{Role: Parent}
	standbyNode := &Node{Role: ParentStandby}

	assert.True(t, leafNode.IsLeaf())
	assert.False(t, leafNode.IsParent())
	assert.False(t, leafNode.IsParentStandby())

	assert.False(t, parentNode.IsLeaf())
	assert.True(t, parentNode.IsParent())
	assert.False(t, parentNode.IsParentStandby())

	assert.False(t, standbyNode.IsLeaf())
	assert.False(t, standbyNode.IsParent())
	assert.True(t, standbyNode.IsParentStandby())
}

// Test_Node_StatusHelpers 测试 Node 状态辅助方法
func Test_Node_StatusHelpers(t *testing.T) {
	onlineNode := &Node{Status: NodeStatusReady}
	initNode := &Node{Status: NodeStatusInit}
	offlineNode := &Node{Status: NodeStatusFailed}

	assert.True(t, onlineNode.IsOnline())
	assert.True(t, initNode.IsOnline())
	assert.False(t, offlineNode.IsOnline())
}

// ============================================================================
// NewNodeAddress 工厂方法测试
// ============================================================================

// Test_NewNodeAddress 测试 NewNodeAddress 工厂方法
func Test_NewNodeAddress(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		tcpPort int
		wantErr bool
		wantTCP int
		wantUDP int
		errMsg  string
	}{
		{
			name:    "正常 - 标准端口",
			host:    "192.168.1.100",
			tcpPort: 9000,
			wantErr: false,
			wantTCP: 9000,
			wantUDP: 9001,
		},
		{
			name:    "正常 - 最小端口",
			host:    "192.168.1.100",
			tcpPort: 1024,
			wantErr: false,
			wantTCP: 1024,
			wantUDP: 1025,
		},
		{
			name:    "正常 - 最大端口",
			host:    "192.168.1.100",
			tcpPort: 65534,
			wantErr: false,
			wantTCP: 65534,
			wantUDP: 65535,
		},
		{
			name:    "正常 - 空 Host",
			host:    "",
			tcpPort: 9000,
			wantErr: false,
			wantTCP: 9000,
			wantUDP: 9001,
		},
		{
			name:    "错误 - 端口过小",
			host:    "192.168.1.100",
			tcpPort: 1023,
			wantErr: true,
			errMsg:  "TCPPort must be in range",
		},
		{
			name:    "错误 - 端口过大",
			host:    "192.168.1.100",
			tcpPort: 65535,
			wantErr: true,
			errMsg:  "TCPPort must be in range",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr, err := NewNodeAddress(tt.host, tt.tcpPort)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.host, addr.Host)
				assert.Equal(t, tt.wantTCP, addr.TCPPort)
				assert.Equal(t, tt.wantUDP, addr.UDPPort)
			}
		})
	}
}

// Test_NewNodeAddress_AutoUDP 测试 UDP 自动设置为 TCP + 1
func Test_NewNodeAddress_AutoUDP(t *testing.T) {
	addr, err := NewNodeAddress("192.168.1.100", 9000)
	require.NoError(t, err)

	assert.Equal(t, 9000, addr.TCPPort)
	assert.Equal(t, 9001, addr.UDPPort)
}

// ============================================================================
// MsgPack 序列化测试 (UT-NODE-008)
// ============================================================================

// Test_Node_MsgPack UT-NODE-008: Node MsgPack 序列化
func Test_Node_MsgPack(t *testing.T) {
	original := &Node{
		NodeID: "node-leaf-1",
		HostID: "server-1",
		Role:   Leaf,
		Addr: NodeAddress{
			Host:    "192.168.1.100",
			TCPPort: 9000,
			UDPPort: 9001,
		},
		ParentID:    "",
		ChildrenIDs: []string{},
		Level:       0,
		Status:      NodeStatusReady,
		Priority:    0,
		Metadata:    make(map[string]string),
	}

	// 序列化
	data, err := msgpack.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// 反序列化
	decoded := &Node{}
	err = msgpack.Unmarshal(data, decoded)
	require.NoError(t, err)

	// 验证字段
	assert.Equal(t, original.NodeID, decoded.NodeID)
	assert.Equal(t, original.HostID, decoded.HostID)
	assert.Equal(t, original.Addr.Host, decoded.Addr.Host)
	assert.Equal(t, original.Addr.TCPPort, decoded.Addr.TCPPort)
	assert.Equal(t, original.Addr.UDPPort, decoded.Addr.UDPPort)
	assert.Equal(t, original.Role, decoded.Role)
	assert.Equal(t, original.Status, decoded.Status)
}

// Test_NodeAddress_MsgPack 测试 NodeAddress MsgPack 序列化
func Test_NodeAddress_MsgPack(t *testing.T) {
	original := &NodeAddress{
		Host:    "192.168.1.100",
		TCPPort: 9000,
		UDPPort: 9001,
	}

	// 序列化
	data, err := msgpack.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// 反序列化
	decoded := &NodeAddress{}
	err = msgpack.Unmarshal(data, decoded)
	require.NoError(t, err)

	// 验证字段
	assert.Equal(t, original.Host, decoded.Host)
	assert.Equal(t, original.TCPPort, decoded.TCPPort)
	assert.Equal(t, original.UDPPort, decoded.UDPPort)
}

// ============================================================================
// HostStatus 测试
// ============================================================================

// Test_HostStatus_String 测试 HostStatus.String() 方法
func Test_HostStatus_String(t *testing.T) {
	tests := []struct {
		status   HostStatus
		expected string
	}{
		{HostStatusOffline, "Offline"},
		{HostStatusOnline, "Online"},
		{HostStatusDegraded, "Degraded"},
		{HostStatus(99), "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := tt.status.String()
			assert.Equal(t, tt.expected, result)
		})
	}
}
