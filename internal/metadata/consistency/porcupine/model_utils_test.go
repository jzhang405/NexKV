// Package porcupine 提供 Porcupine 线性一致性验证集成
// 本文件测试模型工具函数
package porcupine

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ==================== CloneNodeStores 测试 ====================

func TestCloneNodeStores(t *testing.T) {
	t.Run("normal clone", func(t *testing.T) {
		original := map[string]map[string]VersionedValue{
			"node1": {
				"key1": {Value: []byte("value1"), Version: 1},
			},
			"node2": {
				"key2": {Value: []byte("value2"), Version: 2},
			},
		}

		cloned := CloneNodeStores(original)

		// 修改克隆不影响原始
		cloned["node1"]["key1"] = VersionedValue{Value: []byte("modified"), Version: 99}
		assert.Equal(t, "value1", string(original["node1"]["key1"].Value))
		assert.Equal(t, uint64(1), original["node1"]["key1"].Version)
	})

	t.Run("nil input", func(t *testing.T) {
		result := CloneNodeStores(nil)
		assert.Nil(t, result)
	})

	t.Run("empty map", func(t *testing.T) {
		original := map[string]map[string]VersionedValue{}
		cloned := CloneNodeStores(original)
		assert.NotNil(t, cloned)
		assert.Empty(t, cloned)
	})
}

// ==================== NodeStoresEqual 测试 ====================

func TestNodeStoresEqual(t *testing.T) {
	t.Run("equal stores", func(t *testing.T) {
		s1 := map[string]map[string]VersionedValue{
			"node1": {"key1": {Value: []byte("value1"), Version: 1}},
		}
		s2 := map[string]map[string]VersionedValue{
			"node1": {"key1": {Value: []byte("value1"), Version: 1}},
		}
		assert.True(t, NodeStoresEqual(s1, s2))
	})

	t.Run("different values", func(t *testing.T) {
		s1 := map[string]map[string]VersionedValue{
			"node1": {"key1": {Value: []byte("value1"), Version: 1}},
		}
		s2 := map[string]map[string]VersionedValue{
			"node1": {"key1": {Value: []byte("value2"), Version: 1}},
		}
		assert.False(t, NodeStoresEqual(s1, s2))
	})

	t.Run("different nodes", func(t *testing.T) {
		s1 := map[string]map[string]VersionedValue{
			"node1": {"key1": {Value: []byte("value1"), Version: 1}},
		}
		s2 := map[string]map[string]VersionedValue{
			"node2": {"key1": {Value: []byte("value1"), Version: 1}},
		}
		assert.False(t, NodeStoresEqual(s1, s2))
	})

	t.Run("different length", func(t *testing.T) {
		s1 := map[string]map[string]VersionedValue{
			"node1": {},
		}
		s2 := map[string]map[string]VersionedValue{}
		assert.False(t, NodeStoresEqual(s1, s2))
	})
}

// ==================== CloneBoolMap 测试 ====================

func TestCloneBoolMap(t *testing.T) {
	t.Run("normal clone", func(t *testing.T) {
		original := map[string]bool{
			"node1": true,
			"node2": false,
		}

		cloned := CloneBoolMap(original)

		// 修改克隆不影响原始
		cloned["node1"] = false
		assert.True(t, original["node1"])
	})

	t.Run("nil input", func(t *testing.T) {
		result := CloneBoolMap(nil)
		assert.Nil(t, result)
	})
}

// ==================== BoolMapEqual 测试 ====================

func TestBoolMapEqual(t *testing.T) {
	t.Run("equal maps", func(t *testing.T) {
		m1 := map[string]bool{"node1": true, "node2": false}
		m2 := map[string]bool{"node1": true, "node2": false}
		assert.True(t, BoolMapEqual(m1, m2))
	})

	t.Run("different maps", func(t *testing.T) {
		m1 := map[string]bool{"node1": true}
		m2 := map[string]bool{"node1": false}
		assert.False(t, BoolMapEqual(m1, m2))
	})
}

// ==================== CloneStringSlice 测试 ====================

func TestCloneStringSlice(t *testing.T) {
	t.Run("normal clone", func(t *testing.T) {
		original := []string{"a", "b", "c"}
		cloned := CloneStringSlice(original)

		// 修改克隆不影响原始
		cloned[0] = "modified"
		assert.Equal(t, "a", original[0])
	})

	t.Run("nil input", func(t *testing.T) {
		result := CloneStringSlice(nil)
		assert.Nil(t, result)
	})

	t.Run("empty slice", func(t *testing.T) {
		original := []string{}
		cloned := CloneStringSlice(original)
		assert.NotNil(t, cloned)
		assert.Empty(t, cloned)
	})
}

// ==================== StringSliceEqual 测试 ====================

func TestStringSliceEqual(t *testing.T) {
	t.Run("equal slices", func(t *testing.T) {
		s1 := []string{"a", "b", "c"}
		s2 := []string{"a", "b", "c"}
		assert.True(t, StringSliceEqual(s1, s2))
	})

	t.Run("different slices", func(t *testing.T) {
		s1 := []string{"a", "b"}
		s2 := []string{"a", "c"}
		assert.False(t, StringSliceEqual(s1, s2))
	})

	t.Run("different length", func(t *testing.T) {
		s1 := []string{"a"}
		s2 := []string{"a", "b"}
		assert.False(t, StringSliceEqual(s1, s2))
	})
}
