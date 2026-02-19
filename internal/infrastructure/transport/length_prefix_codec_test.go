package transport

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// TestLengthPrefixCodec_Framing 测试长度前缀编解码
func TestLengthPrefixCodec_Framing(t *testing.T) {
	codec := &LengthPrefixedCodec{}

	// 模拟 TCP 流：多个消息连续到达（粘包）
	var buf bytes.Buffer

	messages := [][]byte{
		[]byte("hello"),
		[]byte("world"),
		[]byte("test message with more data"),
		[]byte("x"), // 最小消息
	}

	// 写入所有消息到同一个 buffer（模拟粘包）
	for _, msg := range messages {
		if err := codec.Encode(&buf, msg); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}

	// 逐一读取，验证边界正确
	for i, expected := range messages {
		actual, err := codec.Decode(&buf)
		if err != nil {
			t.Fatalf("decode %d: %v", i, err)
		}
		if !bytes.Equal(actual, expected) {
			t.Errorf("message %d: got %q, want %q", i, actual, expected)
		}
	}
}

// TestLengthPrefixCodec_MessageTooLarge 测试消息过大
func TestLengthPrefixCodec_MessageTooLarge(t *testing.T) {
	codec := &LengthPrefixedCodec{}
	var buf bytes.Buffer

	// 尝试发送超过限制的消息
	largeMsg := make([]byte, MaxMessageSize+1)
	err := codec.Encode(&buf, largeMsg)
	if err == nil {
		t.Error("should reject oversized message")
	}
}

// TestLengthPrefixCodec_DoSAttack 测试 DoS 攻击防护
func TestLengthPrefixCodec_DoSAttack(t *testing.T) {
	codec := &LengthPrefixedCodec{}

	// 发送超大长度前缀（模拟 DoS 攻击）
	buf := bytes.NewBuffer(nil)
	if err := binary.Write(buf, binary.BigEndian, uint32(0xFFFFFFFF)); err != nil {
		t.Fatalf("binary.Write failed: %v", err)
	} // 4GB

	// 应立即返回错误，不应尝试分配 4GB 内存
	_, err := codec.Decode(buf)
	if err == nil {
		t.Error("should reject oversized length prefix")
	}
}

// TestLengthPrefixCodec_ZeroLength 测试零长度消息
func TestLengthPrefixCodec_ZeroLength(t *testing.T) {
	codec := &LengthPrefixedCodec{}

	buf := bytes.NewBuffer(nil)
	if err := binary.Write(buf, binary.BigEndian, uint32(0)); err != nil {
		t.Fatalf("binary.Write failed: %v", err)
	}

	_, err := codec.Decode(buf)
	if err == nil {
		t.Error("should reject zero length message")
	}
}
