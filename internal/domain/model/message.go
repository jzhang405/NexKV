// Package model 定义领域模型
package model

import (
	"sync"
)

// MessageType 消息类型
type MessageType int

const (
	// MessageTypeRequest 请求消息
	MessageTypeRequest MessageType = iota
	// MessageTypeResponse 响应消息
	MessageTypeResponse
	// MessageTypeEvent 事件消息
	MessageTypeEvent
)

// String 返回消息类型的字符串表示
func (t MessageType) String() string {
	switch t {
	case MessageTypeRequest:
		return "request"
	case MessageTypeResponse:
		return "response"
	case MessageTypeEvent:
		return "event"
	default:
		return "unknown"
	}
}

// Extensions 可扩展 KV 接口
type Extensions interface {
	Set(key string, value any)
	Get(key string) (any, bool)
	GetString(key string) (string, bool)
	GetInt(key string) (int64, bool)
	GetBytes(key string) ([]byte, bool)
	Has(key string) bool
	All() map[string]any
}

// BaseExtensions 基础扩展实现
type BaseExtensions struct {
	mu   sync.RWMutex
	data map[string]any
}

// NewExtensions 创建新的扩展
func NewExtensions() *BaseExtensions {
	return &BaseExtensions{
		data: make(map[string]any),
	}
}

// Set 设置扩展字段
func (e *BaseExtensions) Set(key string, value any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.data[key] = value
}

// Get 获取扩展字段
func (e *BaseExtensions) Get(key string) (any, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	v, ok := e.data[key]
	return v, ok
}

// GetString 获取字符串类型扩展字段
func (e *BaseExtensions) GetString(key string) (string, bool) {
	v, ok := e.Get(key)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// GetInt 获取整数类型扩展字段
func (e *BaseExtensions) GetInt(key string) (int64, bool) {
	v, ok := e.Get(key)
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case int32:
		return int64(n), true
	case int16:
		return int64(n), true
	case int8:
		return int64(n), true
	case uint64:
		return int64(n), true
	case uint:
		return int64(n), true
	case uint32:
		return int64(n), true
	case uint16:
		return int64(n), true
	case uint8:
		return int64(n), true
	default:
		return 0, false
	}
}

// copyBytes 辅助函数：深拷贝字节切片
func copyBytes(src []byte) []byte {
	if src == nil {
		return nil
	}
	copied := make([]byte, len(src))
	copy(copied, src)
	return copied
}

// GetBytes 获取字节类型扩展字段（P1 修复：返回深拷贝）
func (e *BaseExtensions) GetBytes(key string) ([]byte, bool) {
	v, ok := e.Get(key)
	if !ok {
		return nil, false
	}
	b, ok := v.([]byte)
	if !ok {
		return nil, false
	}
	return copyBytes(b), true
}

// Has 检查扩展字段是否存在
func (e *BaseExtensions) Has(key string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	_, ok := e.data[key]
	return ok
}

// All 返回所有扩展字段（P1 修复：返回深拷贝）
func (e *BaseExtensions) All() map[string]any {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := make(map[string]any, len(e.data))
	for k, v := range e.data {
		// 对 []byte 类型进行深拷贝
		if val, ok := v.([]byte); ok {
			result[k] = copyBytes(val)
		} else {
			result[k] = v
		}
	}
	return result
}

// 确保实现 Extensions 接口
var _ Extensions = (*BaseExtensions)(nil)

// Message 消息接口（P0 修复：移除 SetPayload，保持不可变性）
type Message interface {
	// ID 返回消息 ID
	ID() string
	// Type 返回消息类型
	Type() MessageType
	// Source 返回发送方节点 ID
	Source() PeerID
	// Target 返回目标节点 ID
	Target() PeerID
	// Payload 返回消息内容
	Payload() []byte
	// Exts 返回可扩展 KV
	Exts() Extensions
}

// BaseMessage 基础消息实现（不可变设计）
type BaseMessage struct {
	id      string
	msgType MessageType
	source  PeerID
	target  PeerID
	payload []byte
	exts    Extensions
}

// NewMessage 创建新消息
func NewMessage(id string, msgType MessageType, source, target PeerID, payload []byte) *BaseMessage {
	// 创建 payload 的拷贝，保证不可变性
	payloadCopy := make([]byte, len(payload))
	copy(payloadCopy, payload)

	return &BaseMessage{
		id:      id,
		msgType: msgType,
		source:  source,
		target:  target,
		payload: payloadCopy,
		exts:    NewExtensions(),
	}
}

// ID 返回消息 ID
func (m *BaseMessage) ID() string {
	return m.id
}

// Type 返回消息类型
func (m *BaseMessage) Type() MessageType {
	return m.msgType
}

// Source 返回发送方节点 ID
func (m *BaseMessage) Source() PeerID {
	return m.source
}

// Target 返回目标节点 ID
func (m *BaseMessage) Target() PeerID {
	return m.target
}

// Payload 返回消息内容（P1 修复：返回深拷贝）
func (m *BaseMessage) Payload() []byte {
	return copyBytes(m.payload)
}

// Exts 返回可扩展 KV
func (m *BaseMessage) Exts() Extensions {
	return m.exts
}

// WithPayload 创建带有新 payload 的消息副本（不可变模式）
func (m *BaseMessage) WithPayload(payload []byte) *BaseMessage {
	return NewMessage(m.id, m.msgType, m.source, m.target, payload)
}

// 确保实现 Message 接口
var _ Message = (*BaseMessage)(nil)
