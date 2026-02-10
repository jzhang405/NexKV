// Package kvstore MessagePack 编解码器单元测试
package kvstore

import (
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMetadataCodec_EncodeDecode 测试编解码基本功能
func TestMetadataCodec_EncodeDecode(t *testing.T) {
	codec := DefaultCodec()

	t.Run("NodeInfo 编解码", func(t *testing.T) {
		original := &types.NodeInfo{
			NodeID: "node-001",
			HostID: "host-001",
			Role:   types.NodeRoleLeaf,
			Addr: types.NodeAddress{
				Host:    "127.0.0.1",
				TCPPort: 5001,
				UDPPort: 5002,
			},
			Status:        types.NodeStatusReady,
			Priority:      1,
			LastHeartbeat: time.Now(),
			Version:       1,
		}

		// 编码
		data, err := codec.Encode(original)
		require.NoError(t, err)
		assert.NotEmpty(t, data)

		// 解码
		decoded := &types.NodeInfo{}
		err = codec.Decode(data, decoded)
		require.NoError(t, err)

		// 验证
		assert.Equal(t, original.NodeID, decoded.NodeID)
		assert.Equal(t, original.HostID, decoded.HostID)
		assert.Equal(t, original.Role, decoded.Role)
		assert.Equal(t, original.Addr.Host, decoded.Addr.Host)
		assert.Equal(t, original.Addr.TCPPort, decoded.Addr.TCPPort)
		assert.Equal(t, original.Addr.UDPPort, decoded.Addr.UDPPort)
		assert.Equal(t, original.Status, decoded.Status)
		assert.Equal(t, original.Priority, decoded.Priority)
		assert.Equal(t, original.Version, decoded.Version)
	})

	t.Run("ClusterInfo 编解码", func(t *testing.T) {
		original := &types.ClusterInfo{
			ClusterID:      "cluster-001",
			ClusterName:    "test-cluster",
			ClusterVersion: "1.0.0",
			State:          types.ClusterStateRunning,
			RootNodeIDs:    []string{"node-001"},
			TreeDepth:      3,
			TotalNodes:     10,
			TotalShards:    5,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
			Version:        1,
		}

		// 编码
		data, err := codec.Encode(original)
		require.NoError(t, err)
		assert.NotEmpty(t, data)

		// 解码
		decoded := &types.ClusterInfo{}
		err = codec.Decode(data, decoded)
		require.NoError(t, err)

		// 验证
		assert.Equal(t, original.ClusterID, decoded.ClusterID)
		assert.Equal(t, original.ClusterName, decoded.ClusterName)
		assert.Equal(t, original.ClusterVersion, decoded.ClusterVersion)
		assert.Equal(t, original.State, decoded.State)
		assert.Equal(t, original.TotalNodes, decoded.TotalNodes)
		assert.Equal(t, original.TotalShards, decoded.TotalShards)
	})

	t.Run("TopologyInfo 编解码", func(t *testing.T) {
		original := &types.TopologyInfo{
			NodeID:      "node-001",
			ParentID:    "",
			ChildrenIDs: []string{"child-001", "child-002"},
			Level:       0,
			Version:     1,
		}

		// 编码
		data, err := codec.Encode(original)
		require.NoError(t, err)
		assert.NotEmpty(t, data)

		// 解码
		decoded := &types.TopologyInfo{}
		err = codec.Decode(data, decoded)
		require.NoError(t, err)

		// 验证
		assert.Equal(t, original.NodeID, decoded.NodeID)
		assert.Equal(t, original.ParentID, decoded.ParentID)
		assert.Equal(t, original.Level, decoded.Level)
		assert.Equal(t, original.Version, decoded.Version)
		assert.Equal(t, original.ChildrenIDs, decoded.ChildrenIDs)
	})
}

// TestMetadataCodec_EncodeNil 测试编码 nil 值
func TestMetadataCodec_EncodeNil(t *testing.T) {
	codec := DefaultCodec()

	data, err := codec.Encode(nil)
	assert.Error(t, err)
	assert.Nil(t, data)
}

// TestMetadataCodec_DecodeEmpty 测试解码空数据
func TestMetadataCodec_DecodeEmpty(t *testing.T) {
	codec := DefaultCodec()

	result := &types.NodeInfo{}
	err := codec.Decode([]byte{}, result)
	assert.Error(t, err)
}

// TestMetadataCodec_DecodeInvalid 测试解码无效数据
func TestMetadataCodec_DecodeInvalid(t *testing.T) {
	codec := DefaultCodec()

	result := &types.NodeInfo{}
	err := codec.Decode([]byte("invalid data"), result)
	assert.Error(t, err)
}

// TestMetadataCodec_DecodeNilTarget 测试解码到 nil 目标
func TestMetadataCodec_DecodeNilTarget(t *testing.T) {
	codec := DefaultCodec()

	nodeInfo := &types.NodeInfo{NodeID: "test"}
	data, _ := codec.Encode(nodeInfo)

	err := codec.Decode(data, nil)
	assert.Error(t, err)
}

// TestMetadataCodec_ComplexTypes 测试复杂数据类型
func TestMetadataCodec_ComplexTypes(t *testing.T) {
	codec := DefaultCodec()

	t.Run("嵌套结构", func(t *testing.T) {
		shardInfo := &types.ShardInfo{
			ShardID:      "shard-001",
			RangeStart:   "a",
			RangeEnd:     "z",
			State:        types.ShardStateActive,
			PrimaryNode:  "node-001",
			ReplicaNodes: []string{"node-002", "node-003"},
		}

		data, err := codec.Encode(shardInfo)
		require.NoError(t, err)

		decoded := &types.ShardInfo{}
		err = codec.Decode(data, decoded)
		require.NoError(t, err)

		assert.Equal(t, shardInfo.ShardID, decoded.ShardID)
		assert.Equal(t, shardInfo.ReplicaNodes, decoded.ReplicaNodes)
	})

	t.Run("时间戳", func(t *testing.T) {
		now := time.Now()
		nodeInfo := &types.NodeInfo{
			NodeID:        "node-001",
			LastHeartbeat: now,
		}

		data, err := codec.Encode(nodeInfo)
		require.NoError(t, err)

		decoded := &types.NodeInfo{}
		err = codec.Decode(data, decoded)
		require.NoError(t, err)

		// 验证时间戳（允许秒级误差）
		assert.WithinDuration(t, now, decoded.LastHeartbeat, time.Second)
	})
}

// TestBuildKeyAndParseKey_AlreadyVerified 测试键构建和解析（已在 metadata_sync_test.go 中验证）
// 这里添加更多边界情况测试
func TestBuildKeyAndParseKey_EdgeCases(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		key       string
	}{
		{"特殊字符键", NamespaceNode, "node-001:with:colons"},
		{"带前缀键", NamespaceNode, "/prefix/node-001"},
		{"带后缀键", NamespaceNode, "node-001/suffix"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 构建完整键
			fullKey := BuildKey(tt.namespace, tt.key)
			assert.NotEmpty(t, fullKey)

			// 解析键
			ns, key, ok := ParseKey(fullKey)
			assert.True(t, ok)
			assert.Equal(t, tt.namespace, ns)
			assert.Equal(t, tt.key, key)
		})
	}

	t.Run("空键", func(t *testing.T) {
		// 空键构建为仅命名空间，无法解析回原始键
		fullKey := BuildKey(NamespaceNode, "")
		assert.Equal(t, NamespaceNode, fullKey)

		// 解析失败，因为空键和命名空间长度相同
		ns, key, ok := ParseKey(fullKey)
		assert.False(t, ok)
		assert.Empty(t, ns)
		assert.Empty(t, key)
	})

	t.Run("无效格式解析", func(t *testing.T) {
		// 不带命名空间分隔符的键
		ns, key, ok := ParseKey("invalid_key")
		assert.False(t, ok)
		assert.Empty(t, ns)
		assert.Empty(t, key)

		// 空键
		ns, key, ok = ParseKey("")
		assert.False(t, ok)
		assert.Empty(t, ns)
		assert.Empty(t, key)
	})
}

// TestValidateNamespace 测试命名空间验证
func TestValidateNamespace(t *testing.T) {
	validNamespaces := []string{
		NamespaceCluster,
		NamespaceNode,
		NamespaceRole,
		NamespaceTopo,
		NamespaceShard,
		NamespaceStatic,
		NamespaceDynamic,
		NamespaceOp,
		NamespaceVersion,
	}

	for _, ns := range validNamespaces {
		t.Run(ns, func(t *testing.T) {
			assert.True(t, ValidateNamespace(ns))
		})
	}

	t.Run("无效命名空间", func(t *testing.T) {
		assert.False(t, ValidateNamespace(""))
		assert.False(t, ValidateNamespace("invalid"))
		assert.False(t, ValidateNamespace("meta:invalid"))
		assert.False(t, ValidateNamespace(":node:"))
	})
}

// TestNewMetadataError 测试错误创建
func TestNewMetadataError(t *testing.T) {
	t.Run("完整错误", func(t *testing.T) {
		err := NewMetadataError("test-ns", "test-key", ErrCodeEncodingFailed, "encoding failed", nil)
		assert.NotNil(t, err)

		assert.Equal(t, "test-ns", err.Namespace())
		assert.Equal(t, "test-key", err.Key())
		assert.Equal(t, ErrCodeEncodingFailed, err.Code())
		assert.Contains(t, err.Error(), "encoding failed")
	})

	t.Run("嵌套错误", func(t *testing.T) {
		innerErr := assert.AnError
		err := NewMetadataError("test-ns", "test-key", ErrCodeKeyNotFound, "key not found", innerErr)

		// 验证内部错误
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "key not found")
	})

	t.Run("错误方法", func(t *testing.T) {
		err := NewMetadataError("meta:node:", "node-001", ErrCodeKeyNotFound, "not found", nil)

		assert.Equal(t, "meta:node:", err.Namespace())
		assert.Equal(t, "node-001", err.Key())
		assert.Equal(t, ErrCodeKeyNotFound, err.Code())
		assert.NotEmpty(t, err.Error())
	})
}

// TestMetadataErrorCodes 测试错误码常量
func TestMetadataErrorCodes(t *testing.T) {
	codes := []ErrorCode{
		ErrCodeInvalidNamespace,
		ErrCodeEmptyKey,
		ErrCodeKeyNotFound,
		ErrCodeVersionNotFound,
		ErrCodeEncodingFailed,
		ErrCodeDecodingFailed,
		ErrCodeStoreClosed,
		ErrCodeStoreNotInitialized,
	}

	for _, code := range codes {
		assert.NotEmpty(t, code.String())
		// String() 应该返回错误码本身的值
		assert.Equal(t, string(code), code.String())
	}
}
