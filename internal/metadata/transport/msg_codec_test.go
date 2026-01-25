// Package transport 编解码器测试
package transport

import (
	"testing"

	"github.com/jzhang405/NexKV/internal/metadata/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ========================================
// Codec 接口方法测试（Name、Type、DecodeInto）
// ========================================

// TestCodec_Name 测试所有编解码器的名称
func TestCodec_Name(t *testing.T) {
	testCases := []struct {
		name         string
		codec        Codec
		expectedName string
	}{
		{"MessagePack", NewMessagePackCodec(), "msgpack"},
		{"JSON", NewJSONCodec(), "json"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expectedName, tc.codec.Name())
		})
	}
}

// TestCodec_Type 测试所有编解码器的类型
func TestCodec_Type(t *testing.T) {
	testCases := []struct {
		name         string
		codec        Codec
		expectedType types.CodecType
	}{
		{"MessagePack", NewMessagePackCodec(), types.CodecTypeMessagePack},
		{"JSON", NewJSONCodec(), types.CodecTypeJSON},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expectedType, tc.codec.Type())
		})
	}
}

// TestCodec_DecodeInto_Success 测试所有编解码器 DecodeInto 成功场景
func TestCodec_DecodeInto_Success(t *testing.T) {
	testCases := []struct {
		name  string
		codec Codec
	}{
		{"MessagePack", NewMessagePackCodec()},
		{"JSON", NewJSONCodec()},
	}

	originalMsg := &PutMessage{
		BaseMessage: BaseMessage{MessageType: types.MessageTypePut},
		Key:         "test-key",
		Value:       []byte("test-value"),
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// 编码
			data, err := tc.codec.Encode(originalMsg)
			require.NoError(t, err)

			// 解码到新实例
			decodedMsg := &PutMessage{}
			err = tc.codec.DecodeInto(data, decodedMsg)
			require.NoError(t, err)

			// 验证
			assert.Equal(t, originalMsg.Key, decodedMsg.Key)
			assert.Equal(t, originalMsg.Value, decodedMsg.Value)
		})
	}
}

// TestCodec_DecodeInto_EmptyData 测试所有编解码器 DecodeInto 空数据
func TestCodec_DecodeInto_EmptyData(t *testing.T) {
	testCases := []struct {
		name  string
		codec Codec
	}{
		{"MessagePack", NewMessagePackCodec()},
		{"JSON", NewJSONCodec()},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			msg := &PutMessage{}
			err := tc.codec.DecodeInto([]byte{}, msg)
			assert.Error(t, err)
			if cerr, ok := err.(*types.Error); ok {
				assert.Equal(t, types.ErrCodecInvalidData, cerr.Code)
			}
		})
	}
}

// TestCodec_DecodeInto_NilMsg 测试所有编解码器 DecodeInto nil 消息
func TestCodec_DecodeInto_NilMsg(t *testing.T) {
	testCases := []struct {
		name  string
		codec Codec
	}{
		{"MessagePack", NewMessagePackCodec()},
		{"JSON", NewJSONCodec()},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.codec.DecodeInto([]byte("data"), nil)
			assert.Error(t, err)
			if cerr, ok := err.(*types.Error); ok {
				assert.Equal(t, types.ErrCodecInvalidMessage, cerr.Code)
			}
		})
	}
}

// TestCodec_DecodeInto_InvalidData 测试所有编解码器 DecodeInto 无效数据
func TestCodec_DecodeInto_InvalidData(t *testing.T) {
	testCases := []struct {
		name        string
		codec       Codec
		invalidData []byte
	}{
		{"MessagePack", NewMessagePackCodec(), []byte("invalid msgpack data")},
		{"JSON", NewJSONCodec(), []byte("invalid json data")},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			msg := &PutMessage{}
			err := tc.codec.DecodeInto(tc.invalidData, msg)
			assert.Error(t, err)
			if cerr, ok := err.(*types.Error); ok {
				assert.Equal(t, types.ErrCodecDecodeFailed, cerr.Code)
			}
		})
	}
}

// TestCodec_DecodeInto_RoundTrip 测试所有编解码器的 DecodeInto 往返
func TestCodec_DecodeInto_RoundTrip(t *testing.T) {
	testCases := []struct {
		name  string
		codec Codec
	}{
		{"MessagePack", NewMessagePackCodec()},
		{"JSON", NewJSONCodec()},
	}

	testMsg := &PutMessage{
		BaseMessage: BaseMessage{MessageType: types.MessageTypePut},
		Key:         "test-roundtrip",
		Value:       []byte("test-value-data"),
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// 编码
			data, err := tc.codec.Encode(testMsg)
			require.NoError(t, err, "编码应该成功")

			// DecodeInto
			decodedMsg := &PutMessage{}
			err = tc.codec.DecodeInto(data, decodedMsg)
			require.NoError(t, err, "DecodeInto 应该成功")

			// 验证
			assert.Equal(t, testMsg.Key, decodedMsg.Key)
			assert.Equal(t, testMsg.Value, decodedMsg.Value)
		})
	}
}

// TestCodec_Encode_NilMessage 测试所有编解码器的 nil 消息处理
func TestCodec_Encode_NilMessage(t *testing.T) {
	testCases := []struct {
		name  string
		codec Codec
	}{
		{"MessagePack", NewMessagePackCodec()},
		{"JSON", NewJSONCodec()},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.codec.Encode(nil)
			assert.Error(t, err)
			if cerr, ok := err.(*types.Error); ok {
				assert.Equal(t, types.ErrCodecInvalidMessage, cerr.Code)
			}
		})
	}
}
