// Package types 元数据类型定义
package types

// TopologyInfo 拓扑元数据
//
// 存储节点的拓扑关系信息
type TopologyInfo struct {
	// NodeID 节点 ID
	NodeID string `msgpack:"node_id"`

	// ParentID 父节点 ID（根节点为空）
	ParentID string `msgpack:"parent_id"`

	// ChildrenIDs 子节点 ID 列表
	ChildrenIDs []string `msgpack:"children_ids"`

	// Level 节点层级（根节点为 0）
	Level int `msgpack:"level"`

	// Version MVCC 版本号
	Version uint64 `msgpack:"version"`
}

// IsRoot 判断是否为根节点
func (t *TopologyInfo) IsRoot() bool {
	return t.ParentID == ""
}

// IsLeaf 判断是否为叶子节点
func (t *TopologyInfo) IsLeaf() bool {
	return len(t.ChildrenIDs) == 0
}

// AddChild 添加子节点
func (t *TopologyInfo) AddChild(childID string) {
	for _, id := range t.ChildrenIDs {
		if id == childID {
			return // 已存在
		}
	}
	t.ChildrenIDs = append(t.ChildrenIDs, childID)
}

// RemoveChild 移除子节点
func (t *TopologyInfo) RemoveChild(childID string) {
	for i, id := range t.ChildrenIDs {
		if id == childID {
			t.ChildrenIDs = append(t.ChildrenIDs[:i], t.ChildrenIDs[i+1:]...)
			return
		}
	}
}
