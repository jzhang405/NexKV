// Package model 定义领域模型
package model

// PeerID 节点标识符
type PeerID string

// PeerAddr 节点地址
type PeerAddr string

// String 返回 PeerID 的字符串表示
func (p PeerID) String() string {
	return string(p)
}

// String 返回 PeerAddr 的字符串表示
func (p PeerAddr) String() string {
	return string(p)
}
