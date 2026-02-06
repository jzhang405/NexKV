// Copyright 2025 The NexKV Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package transport

import (
	"errors"

	"github.com/vmihailenco/msgpack/v5"
)

// ==================== Payload 结构定义 ====================

// PutPayload KV PUT 操作专属 Payload
type PutPayload struct {
	Key     []byte `msgpack:"key"`             // 从 Message.Key 移入
	Value   []byte `msgpack:"value,omitempty"` // 从 Message.Value 移入
	Version uint64 `msgpack:"version"`         // 从 Message.Version 移入
	Sync    bool   `msgpack:"sync"`            // 是否同步写入
}

// GetPayload KV GET 操作专属 Payload
type GetPayload struct {
	Key         []byte `msgpack:"key"`          // 从 Message.Key 移入
	WithVersion bool   `msgpack:"with_version"` // 是否返回版本号
}

// DeletePayload KV DELETE 操作专属 Payload
type DeletePayload struct {
	Key    []byte `msgpack:"key"`    // 从 Message.Key 移入
	Verify bool   `msgpack:"verify"` // 是否验证删除成功
}

// GossipPayload Gossip 协议专属 Payload
type GossipPayload struct {
	Digest       map[string]uint64 `msgpack:"digest"`        // key -> version
	VersionDelta uint64            `msgpack:"version_delta"` // 版本增量
	FullSync     bool              `msgpack:"full_sync"`     // 是否全量同步
}

// QuorumPayload Quorum 协议专属 Payload
type QuorumPayload struct {
	Phase      string `msgpack:"phase"`              // "propose", "vote", "decide"
	ProposalID string `msgpack:"proposal_id"`        // 提案ID
	Key        string `msgpack:"key"`                // 操作的键
	Value      []byte `msgpack:"value,omitempty"`    // 操作的值
	Voter      string `msgpack:"voter,omitempty"`    // 投票节点
	Decision   bool   `msgpack:"decision,omitempty"` // 决策结果
}

// Operation 2PC 操作定义
type Operation struct {
	Type  string `msgpack:"type"`            // "put", "delete"
	Key   string `msgpack:"key"`             // 操作的键
	Value []byte `msgpack:"value,omitempty"` // 操作的值
}

// TwoPCPreparePayload 2PC 准备阶段专属 Payload
type TwoPCPreparePayload struct {
	TxID        string      `msgpack:"tx_id"`       // 事务ID
	Operations  []Operation `msgpack:"operations"`  // 操作列表
	Timeout     int64       `msgpack:"timeout"`     // 超时时间（毫秒）
	Coordinator string      `msgpack:"coordinator"` // 协调节点
}

// TwoPCCommitPayload 2PC 提交阶段专属 Payload
type TwoPCCommitPayload struct {
	TxID   string `msgpack:"tx_id"`  // 事务ID
	Result bool   `msgpack:"result"` // 提交结果（成功/失败）
}

// TwoPCRollbackPayload 2PC 回滚阶段专属 Payload
type TwoPCRollbackPayload struct {
	TxID   string `msgpack:"tx_id"`  // 事务ID
	Reason string `msgpack:"reason"` // 回滚原因
}

// ClusterPayload 集群管理专属 Payload
type ClusterPayload struct {
	Action   string            `msgpack:"action"`   // "join", "leave", "status"
	NodeID   string            `msgpack:"node_id"`  // 节点ID
	Metadata map[string]string `msgpack:"metadata"` // 元数据
}

// ==================== Payload 类型映射 ====================

// PayloadTypeFactory Payload 类型工厂函数（用于创建新实例）
type PayloadTypeFactory func() interface{}

// payloadTypeFactories 维护 MessageType 与 Payload 工厂函数的映射
var payloadTypeFactories = map[MessageType]PayloadTypeFactory{
	MessageTypePut:     func() interface{} { return &PutPayload{} },
	MessageTypeGet:     func() interface{} { return &GetPayload{} },
	MessageTypeDelete:  func() interface{} { return &DeletePayload{} },
	MessageTypeGossip:  func() interface{} { return &GossipPayload{} },
	MessageTypeQuorum:  func() interface{} { return &QuorumPayload{} },
	MessageTypeSync:    func() interface{} { return &TwoPCPreparePayload{} },
	MessageTypeAck:     func() interface{} { return &TwoPCCommitPayload{} },
	MessageTypeNack:    func() interface{} { return &TwoPCRollbackPayload{} },
	MessageTypeCluster: func() interface{} { return &ClusterPayload{} },
}

// ==================== Payload 编解码方法 ====================

// EncodePayload 将结构化 Payload 序列化为 []byte
// 自动绑定 Message.Type 为对应 MessageType
func (m *Message) EncodePayload(payload interface{}) error {
	// 1. 校验 Payload 类型，绑定对应的 MessageType
	var msgType MessageType
	switch payload.(type) {
	case *PutPayload:
		msgType = MessageTypePut
	case *GetPayload:
		msgType = MessageTypeGet
	case *DeletePayload:
		msgType = MessageTypeDelete
	case *GossipPayload:
		msgType = MessageTypeGossip
	case *QuorumPayload:
		msgType = MessageTypeQuorum
	case *TwoPCPreparePayload:
		msgType = MessageTypeSync
	case *TwoPCCommitPayload:
		msgType = MessageTypeAck
	case *TwoPCRollbackPayload:
		msgType = MessageTypeNack
	case *ClusterPayload:
		msgType = MessageTypeCluster
	default:
		return errors.New("unsupported payload type")
	}
	m.Type = msgType

	// 2. MessagePack 序列化 Payload 为 []byte
	data, err := msgpack.Marshal(payload)
	if err != nil {
		return errors.New("msgpack marshal failed: " + err.Error())
	}
	m.Payload = data
	return nil
}

// DecodePayload 将 Message.Payload 反序列化为对应结构化 Payload
// 根据 Message.Type 自动匹配 Payload 类型，保证类型安全
func (m *Message) DecodePayload() (interface{}, error) {
	// 1. 检查 MessageType 是否合法
	factory, ok := payloadTypeFactories[m.Type]
	if !ok {
		return nil, errors.New("unsupported message type: " + m.Type.String())
	}

	// 2. 使用工厂函数创建新的 Payload 实例（避免状态污染）
	payload := factory()

	// 3. MessagePack 反序列化到结构化 Payload
	if err := msgpack.Unmarshal(m.Payload, payload); err != nil {
		return nil, errors.New("msgpack unmarshal failed: " + err.Error())
	}

	return payload, nil
}

// ==================== 辅助方法 ====================

// MustEncodePayload 编码 Payload，失败时 panic（仅用于测试）
func (m *Message) MustEncodePayload(payload interface{}) {
	if err := m.EncodePayload(payload); err != nil {
		panic(err)
	}
}

// MustDecodePayload 解码 Payload，失败时 panic（仅用于测试）
func (m *Message) MustDecodePayload() interface{} {
	payload, err := m.DecodePayload()
	if err != nil {
		panic(err)
	}
	return payload
}
