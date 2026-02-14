// Package porcupine 提供 Porcupine 线性一致性验证集成
// 本文件提供增强模型的公共工具函数
package porcupine

import "reflect"

// ==================== 节点存储工具函数 ====================

// CloneNodeStores 深拷贝节点存储
// 用于状态克隆，避免共享状态
func CloneNodeStores(stores map[string]map[string]VersionedValue) map[string]map[string]VersionedValue {
	if stores == nil {
		return nil
	}

	newStores := make(map[string]map[string]VersionedValue)
	for nodeID, store := range stores {
		newStore := make(map[string]VersionedValue)
		for k, v := range store {
			newStore[k] = v
		}
		newStores[nodeID] = newStore
	}
	return newStores
}

// NodeStoresEqual 比较两个节点存储是否相等
// 用于状态比较函数
func NodeStoresEqual(s1, s2 map[string]map[string]VersionedValue) bool {
	if len(s1) != len(s2) {
		return false
	}
	for nodeID, store1 := range s1 {
		store2, exists := s2[nodeID]
		if !exists || !reflect.DeepEqual(store1, store2) {
			return false
		}
	}
	return true
}

// CloneBoolMap 深拷贝 bool 类型的 map
func CloneBoolMap(m map[string]bool) map[string]bool {
	if m == nil {
		return nil
	}

	newMap := make(map[string]bool)
	for k, v := range m {
		newMap[k] = v
	}
	return newMap
}

// BoolMapEqual 比较两个 bool map 是否相等
func BoolMapEqual(m1, m2 map[string]bool) bool {
	return reflect.DeepEqual(m1, m2)
}

// CloneStringSlice 拷贝字符串切片
func CloneStringSlice(s []string) []string {
	if s == nil {
		return nil
	}
	newSlice := make([]string, len(s))
	copy(newSlice, s)
	return newSlice
}

// StringSliceEqual 比较两个字符串切片是否相等
func StringSliceEqual(s1, s2 []string) bool {
	return reflect.DeepEqual(s1, s2)
}
