// Package model 定义领域模型
package model

// PeerID 节点标识符
type PeerID string

// String 返回 PeerID 的字符串表示
func (p PeerID) String() string {
	return string(p)
}
