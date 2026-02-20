package transport

import (
	"bytes"
	"io"
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
)

// TestMessagePackCodec_EncodeDecode 测试基本编解码
func TestMessagePackCodec_EncodeDecode(t *testing.T) {
	codec := NewMessagePackCodec()

	tests := []struct {
		name    string
		msg     model.Message
		wantErr bool
	}{
		{
			name:    "basic message",
			msg:     createTestMessage("msg-001", model.MessageTypeRequest, []byte("hello world")),
			wantErr: false,
		},
		{
			name:    "message with metadata",
			msg:     createTestMessage("msg-002", model.MessageTypeResponse, []byte(`{"key": "value"}`)),
			wantErr: false,
		},
		{
			name:    "empty payload",
			msg:     createTestMessage("msg-003", model.MessageTypeEvent, []byte{}),
			wantErr: false,
		},
		{
			name:    "nil message",
			msg:     nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 编码
			data, err := codec.Encode(tt.msg)
			if tt.wantErr {
				if err == nil {
					t.Error("Encode() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Encode() error = %v", err)
			}

			// 解码
			decoded, err := codec.Decode(data)
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}

			// 验证
			if decoded.ID() != tt.msg.ID() {
				t.Errorf("ID mismatch: got %v, want %v", decoded.ID(), tt.msg.ID())
			}
			if decoded.Type() != tt.msg.Type() {
				t.Errorf("Type mismatch: got %v, want %v", decoded.Type(), tt.msg.Type())
			}
			if string(decoded.Payload()) != string(tt.msg.Payload()) {
				t.Errorf("Payload mismatch: got %v, want %v", string(decoded.Payload()), string(tt.msg.Payload()))
			}
		})
	}
}

// TestMessagePackCodec_WithExtensions 测试带扩展字段的消息
func TestMessagePackCodec_WithExtensions(t *testing.T) {
	codec := NewMessagePackCodec()

	msg := createTestMessage("msg-ext-001", model.MessageTypeRequest, []byte("test"))
	msg.Exts().Set("request_id", "req-001")
	msg.Exts().Set("trace_id", "trace-001")

	// 编码
	data, err := codec.Encode(msg)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	// 解码
	decoded, err := codec.Decode(data)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	// 验证扩展字段
	reqID, ok := decoded.Exts().GetString("request_id")
	if !ok || reqID != "req-001" {
		t.Errorf("request_id mismatch: got %v, want req-001", reqID)
	}

	traceID, ok := decoded.Exts().GetString("trace_id")
	if !ok || traceID != "trace-001" {
		t.Errorf("trace_id mismatch: got %v, want trace-001", traceID)
	}
}

// TestMessagePackCodec_NameVersion 测试 Name 和 Version
func TestMessagePackCodec_NameVersion(t *testing.T) {
	codec := NewMessagePackCodec()

	if codec.Name() != "msgpack" {
		t.Errorf("Name() = %v, want msgpack", codec.Name())
	}

	if codec.Version() != "v1" {
		t.Errorf("Version() = %v, want v1", codec.Version())
	}
}

// TestMessagePackCodec_EmptyData 测试空数据解码
func TestMessagePackCodec_EmptyData(t *testing.T) {
	codec := NewMessagePackCodec()

	_, err := codec.Decode([]byte{})
	if err == nil {
		t.Error("Decode() with empty data should return error")
	}
}

// TestMessagePackCodec_InvalidData 测试无效数据解码
func TestMessagePackCodec_InvalidData(t *testing.T) {
	codec := NewMessagePackCodec()

	_, err := codec.Decode([]byte("invalid msgpack data"))
	if err == nil {
		t.Error("Decode() with invalid data should return error")
	}
}

// TestMessagePackStreamCodec_EncodeDecode 测试流式编解码
func TestMessagePackStreamCodec_EncodeDecode(t *testing.T) {
	codec := NewMessagePackStreamCodec()

	msg := createTestMessage("stream-001", model.MessageTypeRequest, []byte("stream data"))
	msg.Exts().Set("seq", "1")

	// 编码到 buffer
	var buf bytes.Buffer
	err := codec.EncodeToWriter(&buf, msg)
	if err != nil {
		t.Fatalf("EncodeToWriter() error = %v", err)
	}

	// 从 buffer 解码
	decoded, err := codec.DecodeFromReader(&buf)
	if err != nil {
		t.Fatalf("DecodeFromReader() error = %v", err)
	}

	// 验证
	if decoded.ID() != msg.ID() {
		t.Errorf("ID mismatch: got %v, want %v", decoded.ID(), msg.ID())
	}
	if string(decoded.Payload()) != string(msg.Payload()) {
		t.Errorf("Payload mismatch: got %v, want %v", string(decoded.Payload()), string(msg.Payload()))
	}
}

// TestMessagePackStreamCodec_MultipleMessages 测试多条消息流式编解码
func TestMessagePackStreamCodec_MultipleMessages(t *testing.T) {
	codec := NewMessagePackStreamCodec()

	messages := []*model.BaseMessage{
		createTestMessage("msg1", model.MessageTypeRequest, []byte("data1")),
		createTestMessage("msg2", model.MessageTypeResponse, []byte("data2")),
		createTestMessage("msg3", model.MessageTypeEvent, []byte("data3")),
	}

	// 编码所有消息
	var buf bytes.Buffer
	for _, msg := range messages {
		err := codec.EncodeToWriter(&buf, msg)
		if err != nil {
			t.Fatalf("EncodeToWriter() error = %v", err)
		}
	}

	// 解码所有消息
	for i, expected := range messages {
		decoded, err := codec.DecodeFromReader(&buf)
		if err != nil {
			t.Fatalf("DecodeFromReader() message %d error = %v", i, err)
		}

		if decoded.ID() != expected.ID() {
			t.Errorf("Message %d ID mismatch: got %v, want %v", i, decoded.ID(), expected.ID())
		}
	}

	// 再读取应该返回 EOF
	_, err := codec.DecodeFromReader(&buf)
	if err != io.EOF {
		t.Errorf("Expected EOF, got %v", err)
	}
}

// TestMessagePackStreamCodec_NilMessage 测试 nil 消息
func TestMessagePackStreamCodec_NilMessage(t *testing.T) {
	codec := NewMessagePackStreamCodec()

	var buf bytes.Buffer
	err := codec.EncodeToWriter(&buf, nil)
	if err == nil {
		t.Error("EncodeToWriter() with nil message should return error")
	}
}

// TestMessagePackStreamCodec_LargeMessage 测试大消息（5MB，在 10MB 限制内）
func TestMessagePackStreamCodec_LargeMessage(t *testing.T) {
	// P1 修复：使用自定义限制创建编解码器（允许 10MB 消息）
	codec := NewMessagePackStreamCodecWithLimit(10 * 1024 * 1024)

	// 创建 5MB 的消息（在限制内）
	largePayload := make([]byte, 5*1024*1024)
	for i := range largePayload {
		largePayload[i] = byte(i % 256)
	}

	msg := createTestMessage("large-001", model.MessageTypeRequest, largePayload)

	var buf bytes.Buffer
	err := codec.EncodeToWriter(&buf, msg)
	if err != nil {
		t.Fatalf("EncodeToWriter() error = %v", err)
	}

	decoded, err := codec.DecodeFromReader(&buf)
	if err != nil {
		t.Fatalf("DecodeFromReader() error = %v", err)
	}

	if len(decoded.Payload()) != len(largePayload) {
		t.Errorf("Payload length mismatch: got %d, want %d", len(decoded.Payload()), len(largePayload))
	}
}

// TestMessagePackStreamCodec_TooLargeMessage 测试超大消息（超过限制）
func TestMessagePackStreamCodec_TooLargeMessage(t *testing.T) {
	// 手动构造一个超大消息的长度前缀
	var buf bytes.Buffer

	// 写入一个 101MB 的长度前缀（超过默认 10MB 限制）
	length := uint32(101 * 1024 * 1024)
	buf.WriteByte(byte(length >> 24))
	buf.WriteByte(byte(length >> 16))
	buf.WriteByte(byte(length >> 8))
	buf.WriteByte(byte(length))

	codec := NewMessagePackStreamCodec()
	_, err := codec.DecodeFromReader(&buf)

	// 应该返回消息过大的错误
	if err == nil {
		t.Error("DecodeFromReader() should return error for too large message")
	}
}

// TestMessagePackStreamCodec_CustomMaxSize 测试自定义消息大小限制
func TestMessagePackStreamCodec_CustomMaxSize(t *testing.T) {
	// 创建一个 1MB 限制的编解码器
	codec := NewMessagePackStreamCodecWithLimit(1024 * 1024) // 1MB

	if codec.MaxMessageSize() != 1024*1024 {
		t.Errorf("MaxMessageSize() = %d, want %d", codec.MaxMessageSize(), 1024*1024)
	}

	// 创建一个超过限制的消息
	msg := createTestMessage("large-001", model.MessageTypeRequest, make([]byte, 2*1024*1024))

	var buf bytes.Buffer
	err := codec.EncodeToWriter(&buf, msg)
	if err != nil {
		t.Fatalf("EncodeToWriter() error = %v", err)
	}

	// 解码应该失败（消息超过 1MB 限制）
	_, err = codec.DecodeFromReader(&buf)
	if err == nil {
		t.Error("DecodeFromReader() should return error for message exceeding limit")
	}
}

// TestMessagePackStreamCodec_DefaultMaxSize 测试默认消息大小限制
func TestMessagePackStreamCodec_DefaultMaxSize(t *testing.T) {
	codec := NewMessagePackStreamCodec()

	if codec.MaxMessageSize() != DefaultMaxMessageSize {
		t.Errorf("MaxMessageSize() = %d, want %d", codec.MaxMessageSize(), DefaultMaxMessageSize)
	}
}

// TestMessagePackCodec_RoundTripConsistency 测试编解码一致性
func TestMessagePackCodec_RoundTripConsistency(t *testing.T) {
	codec := NewMessagePackCodec()

	original := createTestMessage("consistency-001", model.MessageTypeRequest, []byte("test payload with special chars: \x00\x01\x02"))
	original.Exts().Set("key1", "value1")
	original.Exts().Set("key2", "value2")

	// 多次编解码
	current := original
	for i := 0; i < 10; i++ {
		data, err := codec.Encode(current)
		if err != nil {
			t.Fatalf("Encode() iteration %d error = %v", i, err)
		}

		decoded, err := codec.Decode(data)
		if err != nil {
			t.Fatalf("Decode() iteration %d error = %v", i, err)
		}

		// 使用解码结果作为下一次的输入
		current = decoded.(*model.BaseMessage)
	}

	// 验证最终结果
	if current.ID() != original.ID() {
		t.Errorf("ID mismatch after round trips: got %v, want %v", current.ID(), original.ID())
	}
}

// BenchmarkMessagePackCodec_Encode 编码性能测试（1KB 消息）
func BenchmarkMessagePackCodec_Encode(b *testing.B) {
	codec := NewMessagePackCodec()
	msg := createTestMessage("bench-001", model.MessageTypeRequest, make([]byte, 1024))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = codec.Encode(msg)
	}
}

// BenchmarkMessagePackCodec_Decode 解码性能测试（1KB 消息）
func BenchmarkMessagePackCodec_Decode(b *testing.B) {
	codec := NewMessagePackCodec()
	msg := createTestMessage("bench-001", model.MessageTypeRequest, make([]byte, 1024))
	data, _ := codec.Encode(msg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = codec.Decode(data)
	}
}

// BenchmarkMessagePackCodec_RoundTrip 往返性能测试（1KB 消息）
func BenchmarkMessagePackCodec_RoundTrip(b *testing.B) {
	codec := NewMessagePackCodec()
	msg := createTestMessage("bench-001", model.MessageTypeRequest, make([]byte, 1024))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		data, _ := codec.Encode(msg)
		_, _ = codec.Decode(data)
	}
}

// BenchmarkMessagePackStreamCodec_EncodeToWriter 流式编码性能测试（1KB 消息）
func BenchmarkMessagePackStreamCodec_EncodeToWriter(b *testing.B) {
	codec := NewMessagePackStreamCodec()
	msg := createTestMessage("bench-001", model.MessageTypeRequest, make([]byte, 1024))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var buf bytes.Buffer
		_ = codec.EncodeToWriter(&buf, msg)
	}
}

// BenchmarkMessagePackStreamCodec_DecodeFromReader 流式解码性能测试（1KB 消息）
func BenchmarkMessagePackStreamCodec_DecodeFromReader(b *testing.B) {
	codec := NewMessagePackStreamCodec()
	msg := createTestMessage("bench-001", model.MessageTypeRequest, make([]byte, 1024))

	var buf bytes.Buffer
	_ = codec.EncodeToWriter(&buf, msg)
	data := buf.Bytes()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reader := bytes.NewReader(data)
		_, _ = codec.DecodeFromReader(reader)
	}
}

// BenchmarkMessagePackCodec_Encode_10KB 10KB 消息编码性能测试
func BenchmarkMessagePackCodec_Encode_10KB(b *testing.B) {
	codec := NewMessagePackCodec()
	msg := createTestMessage("bench-10kb", model.MessageTypeRequest, make([]byte, 10*1024))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = codec.Encode(msg)
	}
}

// BenchmarkMessagePackCodec_Decode_10KB 10KB 消息解码性能测试
func BenchmarkMessagePackCodec_Decode_10KB(b *testing.B) {
	codec := NewMessagePackCodec()
	msg := createTestMessage("bench-10kb", model.MessageTypeRequest, make([]byte, 10*1024))
	data, _ := codec.Encode(msg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = codec.Decode(data)
	}
}

// TestMessagePackCodec_ImplementsInterface 验证接口实现
func TestMessagePackCodec_ImplementsInterface(t *testing.T) {
	var _ service.Codec = NewMessagePackCodec()
	var _ service.StreamCodec = NewMessagePackStreamCodec()
}

// TestEncodeToBuffer 测试编码到 buffer
func TestEncodeToBuffer(t *testing.T) {
	codec := NewMessagePackCodec()
	msg := createTestMessage("buffer-001", model.MessageTypeRequest, []byte("test data"))

	buf, err := EncodeToBuffer(codec, msg)
	if err != nil {
		t.Fatalf("EncodeToBuffer() error = %v", err)
	}

	if buf == nil {
		t.Fatal("EncodeToBuffer() returned nil buffer")
	}

	if buf.Len() == 0 {
		t.Error("EncodeToBuffer() returned empty buffer")
	}

	// 验证可以解码
	decoded, err := codec.Decode(buf.Bytes())
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	if decoded.ID() != msg.ID() {
		t.Errorf("Decoded ID = %v, want %v", decoded.ID(), msg.ID())
	}
}

// TestEncodeToBuffer_NilMessage 测试编码 nil 消息
func TestEncodeToBuffer_NilMessage(t *testing.T) {
	codec := NewMessagePackCodec()

	_, err := EncodeToBuffer(codec, nil)
	if err == nil {
		t.Error("EncodeToBuffer() with nil message should return error")
	}
}
