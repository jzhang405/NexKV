// Package model 测试消息模型
package model

import (
	"testing"
)

// ==========================================
// MessageType 测试
// ==========================================

// TestMessageType_String 测试消息类型字符串表示
func TestMessageType_String(t *testing.T) {
	tests := []struct {
		name     string
		msgType  MessageType
		expected string
	}{
		{"Request", MessageTypeRequest, "request"},
		{"Response", MessageTypeResponse, "response"},
		{"Event", MessageTypeEvent, "event"},
		{"Unknown", MessageType(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.msgType.String(); got != tt.expected {
				t.Errorf("String() = %s, want %s", got, tt.expected)
			}
		})
	}
}

// ==========================================
// Extensions 测试
// ==========================================

// TestNewExtensions 测试创建扩展
func TestNewExtensions(t *testing.T) {
	exts := NewExtensions()

	if exts == nil {
		t.Fatal("NewExtensions should not return nil")
	}

	// 验证初始状态
	if exts.Has("key") {
		t.Error("New extensions should not have any keys")
	}
}

// TestExtensions_Set_Get 测试设置和获取
func TestExtensions_Set_Get(t *testing.T) {
	exts := NewExtensions()

	// 设置值
	exts.Set("name", "test")

	// 获取值
	val, ok := exts.Get("name")
	if !ok {
		t.Error("Get should return true for existing key")
	}
	if val != "test" {
		t.Errorf("Get = %v, want %v", val, "test")
	}

	// 获取不存在的键
	_, ok = exts.Get("nonexistent")
	if ok {
		t.Error("Get should return false for non-existing key")
	}
}

// TestExtensions_GetString 测试获取字符串
func TestExtensions_GetString(t *testing.T) {
	exts := NewExtensions()

	// 设置字符串
	exts.Set("str", "hello")

	// 获取字符串
	val, ok := exts.GetString("str")
	if !ok {
		t.Error("GetString should return true for string value")
	}
	if val != "hello" {
		t.Errorf("GetString = %s, want %s", val, "hello")
	}

	// 获取非字符串值
	exts.Set("num", 42)
	_, ok = exts.GetString("num")
	if ok {
		t.Error("GetString should return false for non-string value")
	}

	// 获取不存在的键
	_, ok = exts.GetString("nonexistent")
	if ok {
		t.Error("GetString should return false for non-existing key")
	}
}

// TestExtensions_GetInt 测试获取整数
func TestExtensions_GetInt(t *testing.T) {
	exts := NewExtensions()

	tests := []struct {
		name     string
		value    any
		expected int64
		ok       bool
	}{
		{"int64", int64(42), 42, true},
		{"int", int(42), 42, true},
		{"int32", int32(42), 42, true},
		{"int16", int16(42), 42, true},
		{"int8", int8(42), 42, true},
		{"uint64", uint64(42), 42, true},
		{"uint", uint(42), 42, true},
		{"uint32", uint32(42), 42, true},
		{"uint16", uint16(42), 42, true},
		{"uint8", uint8(42), 42, true},
		{"string", "42", 0, false},
		{"nonexistent", nil, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name != "nonexistent" {
				exts.Set(tt.name, tt.value)
			}

			val, ok := exts.GetInt(tt.name)
			if ok != tt.ok {
				t.Errorf("GetInt(%s) ok = %v, want %v", tt.name, ok, tt.ok)
				return
			}
			if val != tt.expected {
				t.Errorf("GetInt(%s) = %d, want %d", tt.name, val, tt.expected)
			}
		})
	}
}

// TestExtensions_GetBytes 测试获取字节切片
func TestExtensions_GetBytes(t *testing.T) {
	exts := NewExtensions()

	// 设置字节切片
	original := []byte{1, 2, 3}
	exts.Set("data", original)

	// 获取字节切片（应该返回深拷贝）
	val, ok := exts.GetBytes("data")
	if !ok {
		t.Error("GetBytes should return true for bytes value")
	}
	if string(val) != string(original) {
		t.Errorf("GetBytes = %v, want %v", val, original)
	}

	// 修改返回值不应该影响原值
	val[0] = 99
	modifiedVal, _ := exts.GetBytes("data")
	if modifiedVal[0] == 99 {
		t.Error("GetBytes should return a deep copy")
	}

	// 获取非字节切片值
	exts.Set("str", "hello")
	_, ok = exts.GetBytes("str")
	if ok {
		t.Error("GetBytes should return false for non-bytes value")
	}
}

// TestExtensions_Has 测试检查键是否存在
func TestExtensions_Has(t *testing.T) {
	exts := NewExtensions()

	// 不存在的键
	if exts.Has("key") {
		t.Error("Has should return false for non-existing key")
	}

	// 设置后
	exts.Set("key", "value")
	if !exts.Has("key") {
		t.Error("Has should return true after Set")
	}
}

// TestExtensions_All 测试获取所有扩展
func TestExtensions_All(t *testing.T) {
	exts := NewExtensions()

	// 设置多个值
	exts.Set("str", "hello")
	exts.Set("num", 42)
	originalBytes := []byte{1, 2, 3}
	exts.Set("data", originalBytes)

	// 获取所有
	all := exts.All()
	if len(all) != 3 {
		t.Errorf("All() returned %d items, want 3", len(all))
	}

	// 验证字符串值
	if all["str"] != "hello" {
		t.Errorf("All()[\"str\"] = %v, want \"hello\"", all["str"])
	}

	// 验证字节切片是深拷贝
	if val, ok := all["data"].([]byte); ok {
		val[0] = 99
	}

	// 再次获取应该仍然是原始值
	all2 := exts.All()
	if val2, ok := all2["data"].([]byte); ok {
		if val2[0] != 1 {
			t.Error("All() should return deep copies, original should not be modified")
		}
	}

	// 修改返回的 map 不应该影响原 Extensions
	all["str"] = "modified"
	val, _ := exts.GetString("str")
	if val == "modified" {
		t.Error("Modifying returned map should not affect Extensions")
	}
}

// ==========================================
// Message 测试
// ==========================================

// TestNewMessage 测试创建消息
func TestNewMessage(t *testing.T) {
	payload := []byte("test payload")
	msg := NewMessage("msg-001", MessageTypeRequest, "node-1", "node-2", payload)

	if msg.ID() != "msg-001" {
		t.Errorf("ID() = %s, want %s", msg.ID(), "msg-001")
	}
	if msg.Type() != MessageTypeRequest {
		t.Errorf("Type() = %v, want %v", msg.Type(), MessageTypeRequest)
	}
	if msg.Source() != "node-1" {
		t.Errorf("Source() = %s, want %s", msg.Source(), "node-1")
	}
	if msg.Target() != "node-2" {
		t.Errorf("Target() = %s, want %s", msg.Target(), "node-2")
	}
	if string(msg.Payload()) != string(payload) {
		t.Errorf("Payload() = %s, want %s", string(msg.Payload()), string(payload))
	}
}

// TestNewMessage_PayloadCopy 测试 payload 是深拷贝
func TestNewMessage_PayloadCopy(t *testing.T) {
	originalPayload := []byte{1, 2, 3}
	msg := NewMessage("msg-001", MessageTypeRequest, "node-1", "node-2", originalPayload)

	// 修改原始 payload
	originalPayload[0] = 99

	// 消息的 payload 应该不受影响
	if msg.Payload()[0] != 1 {
		t.Error("Message payload should be a deep copy")
	}
}

// TestMessage_Exts 测试获取扩展
func TestMessage_Exts(t *testing.T) {
	msg := NewMessage("msg-001", MessageTypeRequest, "node-1", "node-2", nil)

	// Exts 不应该是 nil
	if msg.Exts() == nil {
		t.Error("Exts() should not return nil")
	}

	// 可以使用扩展
	msg.Exts().Set("key", "value")
	val, ok := msg.Exts().Get("key")
	if !ok || val != "value" {
		t.Error("Should be able to use Exts()")
	}
}

// TestMessage_WithPayload 测试创建带新 payload 的消息副本
func TestMessage_WithPayload(t *testing.T) {
	originalPayload := []byte{1, 2, 3}
	msg := NewMessage("msg-001", MessageTypeRequest, "node-1", "node-2", originalPayload)

	// 修改原始 payload（不应该影响已创建的消息）
	originalPayload[0] = 99

	// 验证原消息的 payload 不受影响（NewMessage 时已拷贝）
	if msg.Payload()[0] != 1 {
		t.Errorf("Original message payload should be unchanged, got %d", msg.Payload()[0])
	}

	// 创建新消息
	newPayload := []byte{4, 5, 6}
	newMsg := msg.WithPayload(newPayload)

	// 验证新消息的 payload
	if string(newMsg.Payload()) != string(newPayload) {
		t.Errorf("WithPayload() payload = %v, want %v", newMsg.Payload(), newPayload)
	}

	// 原消息的 payload 应该保持不变
	if string(msg.Payload()) != string([]byte{1, 2, 3}) {
		t.Errorf("Original message payload should be unchanged, got %v", msg.Payload())
	}

	// 原消息和新消息应该有不同的 payload 切片
	if &msg.Payload()[0] == &newMsg.Payload()[0] {
		t.Error("WithPayload() should create a new payload slice")
	}
}

// TestMessage_Payload_ReturnsCopy 测试 Payload 返回深拷贝
func TestMessage_Payload_ReturnsCopy(t *testing.T) {
	originalPayload := []byte{1, 2, 3}
	msg := NewMessage("msg-001", MessageTypeRequest, "node-1", "node-2", originalPayload)

	// 获取 payload
	p1 := msg.Payload()
	p2 := msg.Payload()

	// 修改 p1
	p1[0] = 99

	// p2 不应该受影响
	if p2[0] != 1 {
		t.Error("Payload() should return a deep copy each time")
	}

	// 原始 payload 也不应该受影响
	if msg.Payload()[0] != 1 {
		t.Error("Original payload should not be affected")
	}
}
