// Package transport TLV 协议帧测试
package transport

import (
	"encoding/binary"
	"hash/crc32"
	"testing"

	"github.com/jzhang405/NexKV/internal/metadata/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"
)

// TestDecodeFragmentExt_Success 测试正常解析分片扩展
func TestDecodeFragmentExt_Success(t *testing.T) {
	// 准备测试数据：使用 EncodeFragmentExt 编码，然后解码验证
	tests := []struct {
		name          string
		index         uint16
		total         uint16
		expectedIndex uint16
		expectedTotal uint16
	}{
		{
			name:          "首个分片（0/3）",
			index:         0,
			total:         3,
			expectedIndex: 0,
			expectedTotal: 3,
		},
		{
			name:          "中间分片（1/5）",
			index:         1,
			total:         5,
			expectedIndex: 1,
			expectedTotal: 5,
		},
		{
			name:          "最后分片（2/3）",
			index:         2,
			total:         3,
			expectedIndex: 2,
			expectedTotal: 3,
		},
		{
			name:          "单个分片（0/1，无实际分片）",
			index:         0,
			total:         1,
			expectedIndex: 0,
			expectedTotal: 1,
		},
		{
			name:          "大量分片（99/100）",
			index:         99,
			total:         100,
			expectedIndex: 99,
			expectedTotal: 100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 使用 EncodeFragmentExt 创建测试数据
			field := EncodeFragmentExt(tt.index, tt.total)

			// 测试 DecodeFragmentExt
			index, total, err := DecodeFragmentExt(field)

			// 验证结果
			assert.NoError(t, err, "解码应该成功")
			assert.Equal(t, tt.expectedIndex, index, "索引应该匹配")
			assert.Equal(t, tt.expectedTotal, total, "总数应该匹配")
		})
	}
}

// TestDecodeFragmentExt_InvalidData 测试无效数据
func TestDecodeFragmentExt_InvalidData(t *testing.T) {
	tests := []struct {
		name        string
		createField func() *ExtField
		checkError  func(t *testing.T, err error)
	}{
		{
			name: "空值",
			createField: func() *ExtField {
				return &ExtField{
					Type:  ExtFragment,
					Value: []byte{},
				}
			},
			checkError: func(t *testing.T, err error) {
				assert.Error(t, err, "空值应该返回错误")
				assert.Contains(t, err.Error(), "反序列化分片扩展失败", "错误信息应该包含提示")
			},
		},
		{
			name: "非 MessagePack 数据",
			createField: func() *ExtField {
				return &ExtField{
					Type:  ExtFragment,
					Value: []byte{0xFF, 0xFF, 0xFF, 0xFF}, // 无效的 msgpack 数据
				}
			},
			checkError: func(t *testing.T, err error) {
				assert.Error(t, err, "无效的 msgpack 数据应该返回错误")
				assert.Contains(t, err.Error(), "反序列化分片扩展失败", "错误信息应该包含提示")
			},
		},
		{
			name: "不完整的数据（只有 Index）",
			createField: func() *ExtField {
				// 手动构造只有 Index 的 msgpack 数据
				data := map[string]uint16{
					"idx": 1, // 只有 Index，缺少 Total
				}
				bytes, _ := msgpack.Marshal(data)
				return &ExtField{
					Type:  ExtFragment,
					Value: bytes,
				}
			},
			checkError: func(t *testing.T, err error) {
				// msgpack 反序列化不完整数据时，缺失字段会被设置为零值
				// 所以这种情况不会报错，但 Total 会是 0
				assert.NoError(t, err, "msgpack 允许缺失字段，会使用零值")
			},
		},
		{
			name: "类型不匹配（字符串而非整数）",
			createField: func() *ExtField {
				// 构造类型错误的数据
				data := map[string]interface{}{
					"idx": "not-a-number", // 字符串而非 uint16
					"tot": uint16(3),
				}
				bytes, _ := msgpack.Marshal(data)
				return &ExtField{
					Type:  ExtFragment,
					Value: bytes,
				}
			},
			checkError: func(t *testing.T, err error) {
				assert.Error(t, err, "类型不匹配应该返回错误")
				assert.Contains(t, err.Error(), "反序列化分片扩展失败", "错误信息应该包含提示")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			field := tt.createField()
			_, _, err := DecodeFragmentExt(field)
			tt.checkError(t, err)
		})
	}
}

// TestEncodeFragmentExt_DecodeRoundTrip 测试编码解码往返
func TestEncodeFragmentExt_DecodeRoundTrip(t *testing.T) {
	// 测试多种分片组合
	testCases := []struct {
		index uint16
		total uint16
	}{
		{0, 1},     // 单个分片
		{0, 2},     // 第一个分片
		{1, 2},     // 最后一个分片
		{0, 10},    // 第一个分片（10个分片）
		{5, 10},    // 中间分片（10个分片）
		{9, 10},    // 最后一个分片（10个分片）
		{0, 65535}, // 最大分片数
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			// 编码
			field := EncodeFragmentExt(tc.index, tc.total)

			// 解码
			index, total, err := DecodeFragmentExt(field)

			// 验证
			assert.NoError(t, err, "编码解码往返应该成功")
			assert.Equal(t, tc.index, index, "索引应该保持不变")
			assert.Equal(t, tc.total, total, "总数应该保持不变")
		})
	}
}

// TestDecodeFragmentExt_EdgeCases 测试边界情况
func TestDecodeFragmentExt_EdgeCases(t *testing.T) {
	t.Run("零值分片", func(t *testing.T) {
		field := EncodeFragmentExt(0, 0)
		index, total, err := DecodeFragmentExt(field)

		assert.NoError(t, err, "零值分片应该成功解码")
		assert.Equal(t, uint16(0), index, "索引应该为零")
		assert.Equal(t, uint16(0), total, "总数应该为零")
	})

	t.Run("最大uint16值", func(t *testing.T) {
		field := EncodeFragmentExt(65535, 65535)
		index, total, err := DecodeFragmentExt(field)

		assert.NoError(t, err, "最大值应该成功解码")
		assert.Equal(t, uint16(65535), index, "索引应该为最大值")
		assert.Equal(t, uint16(65535), total, "总数应该为最大值")
	})
}

// ========================================
// Priority 扩展字段测试（P2-PR-001）
// ========================================

// TestEncodePriorityExt_DecodePriorityExt 测试优先级扩展编码解码往返
func TestEncodePriorityExt_DecodePriorityExt(t *testing.T) {
	testCases := []struct {
		name     string
		priority types.Priority
	}{
		{"低优先级", types.PriorityLow},
		{"正常优先级", types.PriorityNormal},
		{"高优先级", types.PriorityHigh},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// 编码
			field := EncodePriorityExt(tc.priority)

			// 验证字段类型
			assert.Equal(t, ExtPriority, field.Type, "扩展字段类型应该为 ExtPriority")

			// 解码
			decodedPriority, err := DecodePriorityExt(field)

			// 验证
			assert.NoError(t, err, "解码应该成功")
			assert.Equal(t, tc.priority, decodedPriority, "优先级应该匹配")
		})
	}
}

// TestEncodePriorityExt_AllPriorityValues 测试所有优先级值
func TestEncodePriorityExt_AllPriorityValues(t *testing.T) {
	// 测试所有可能的优先级值（0-255）
	for i := 0; i < 256; i++ {
		t.Run("", func(t *testing.T) {
			priority := types.Priority(i)

			// 编码
			field := EncodePriorityExt(priority)

			// 解码
			decodedPriority, err := DecodePriorityExt(field)

			// 验证
			assert.NoError(t, err, "解码应该成功")
			assert.Equal(t, priority, decodedPriority, "优先级应该保持不变")
		})
	}
}

// TestDecodePriorityExt_InvalidData 测试无效数据解码
func TestDecodePriorityExt_InvalidData(t *testing.T) {
	testCases := []struct {
		name        string
		createField func() *ExtField
		checkError  func(t *testing.T, err error)
	}{
		{
			name: "空值",
			createField: func() *ExtField {
				return &ExtField{
					Type:  ExtPriority,
					Value: []byte{},
				}
			},
			checkError: func(t *testing.T, err error) {
				assert.Error(t, err, "空值应该返回错误")
				assert.Contains(t, err.Error(), "反序列化优先级扩展失败", "错误信息应该包含提示")
			},
		},
		{
			name: "非 MessagePack 数据",
			createField: func() *ExtField {
				return &ExtField{
					Type:  ExtPriority,
					Value: []byte{0xFF, 0xFF, 0xFF, 0xFF}, // 无效的 msgpack 数据
				}
			},
			checkError: func(t *testing.T, err error) {
				assert.Error(t, err, "无效的 msgpack 数据应该返回错误")
				assert.Contains(t, err.Error(), "反序列化优先级扩展失败", "错误信息应该包含提示")
			},
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			field := tt.createField()
			_, err := DecodePriorityExt(field)
			tt.checkError(t, err)
		})
	}
}

// TestFrame_WithPriority_Chain 测试 WithPriority 链式调用
func TestFrame_WithPriority_Chain(t *testing.T) {
	testCases := []struct {
		name     string
		priority types.Priority
	}{
		{"低优先级", types.PriorityLow},
		{"正常优先级", types.PriorityNormal},
		{"高优先级", types.PriorityHigh},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			frame := NewFrame(12345, 67890, types.MessageTypePut, uint16(types.CodecTypeMessagePack), 0, []byte("test data"))

			// 使用 WithPriority 添加优先级扩展
			frame.WithPriority(tc.priority)

			// 验证扩展字段已添加
			assert.Equal(t, 1, len(frame.VarExtHeader.Fields), "应该有一个扩展字段")

			priorityField := frame.VarExtHeader.GetField(ExtPriority)
			assert.NotNil(t, priorityField, "应该找到优先级扩展字段")

			decodedPriority, err := DecodePriorityExt(priorityField)
			assert.NoError(t, err)
			assert.Equal(t, tc.priority, decodedPriority, "优先级应该匹配")
		})
	}
}

// TestFrame_WithPriority_MultipleExtensions 测试 WithPriority 与其他扩展字段组合
func TestFrame_WithPriority_MultipleExtensions(t *testing.T) {
	frame := NewFrame(12345, 67890, types.MessageTypePut, uint16(types.CodecTypeMessagePack), 0, []byte("test data"))

	// 链式调用添加多个扩展字段
	frame.WithFragment(1, 5).
		WithPriority(types.PriorityHigh).
		WithCompress(1) // 假设有 WithCompress 方法

	// 验证所有扩展字段都已添加
	assert.Equal(t, 3, len(frame.VarExtHeader.Fields), "应该有三个扩展字段")

	// 验证优先级扩展字段
	priorityField := frame.VarExtHeader.GetField(ExtPriority)
	assert.NotNil(t, priorityField, "应该找到优先级扩展字段")

	decodedPriority, err := DecodePriorityExt(priorityField)
	assert.NoError(t, err)
	assert.Equal(t, types.PriorityHigh, decodedPriority, "优先级应该是 High")
}

// TestFrame_WithPriority_RoundTrip 测试 WithPriority 序列化往返
func TestFrame_WithPriority_RoundTrip(t *testing.T) {
	testCases := []struct {
		name     string
		priority types.Priority
	}{
		{"低优先级往返", types.PriorityLow},
		{"正常优先级往返", types.PriorityNormal},
		{"高优先级往返", types.PriorityHigh},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// 创建带优先级的帧
			frame := NewFrame(12345, 67890, types.MessageTypePut, uint16(types.CodecTypeMessagePack), 0, []byte("test data"))
			frame.WithPriority(tc.priority).Finalize()

			// 序列化
			frameData, err := frame.Marshal()
			assert.NoError(t, err, "序列化应该成功")

			// 反序列化
			decodedFrame := &Frame{}
			err = decodedFrame.Unmarshal(frameData)
			assert.NoError(t, err, "反序列化应该成功")

			// 验证优先级扩展字段
			priorityField := decodedFrame.VarExtHeader.GetField(ExtPriority)
			assert.NotNil(t, priorityField, "应该找到优先级扩展字段")

			decodedPriority, err := DecodePriorityExt(priorityField)
			assert.NoError(t, err)
			assert.Equal(t, tc.priority, decodedPriority, "优先级应该保持不变")
		})
	}
}

// TestFrame_WithPriority_UsageExamples 使用示例
func TestFrame_WithPriority_UsageExamples(t *testing.T) {
	t.Run("示例1: 创建高优先级消息", func(t *testing.T) {
		// 场景：发送高优先级的元数据操作请求
		frame := NewFrame(12345, // NodeID
			67890,                              // MsgSeq
			types.MessageTypePut,               // MsgType
			uint16(types.CodecTypeMessagePack), 0, // CodecID
			[]byte("metadata data"), // Data
		)

		// 设置为高优先级，确保此消息优先处理
		frame.WithPriority(types.PriorityHigh).Finalize()

		// 验证
		priorityField := frame.VarExtHeader.GetField(ExtPriority)
		require.NotNil(t, priorityField)
		priority, _ := DecodePriorityExt(priorityField)
		assert.Equal(t, types.PriorityHigh, priority)
	})

	t.Run("示例2: 创建带分片和高优先级的消息", func(t *testing.T) {
		// 场景：大数据消息需要分片，同时设置为高优先级
		frame := NewFrame(12345, 67890, types.MessageTypePut, uint16(types.CodecTypeMessagePack), 0, make([]byte, 2000)) // 大数据需要分片

		// 链式调用：添加分片信息和优先级
		frame.WithFragment(0, 3). // 第一个分片，共 3 个分片
						WithPriority(types.PriorityHigh).
						Finalize()

		// 验证扩展字段
		assert.Equal(t, 2, len(frame.VarExtHeader.Fields))

		fragmentField := frame.VarExtHeader.GetField(ExtFragment)
		require.NotNil(t, fragmentField)
		index, total, _ := DecodeFragmentExt(fragmentField)
		assert.Equal(t, uint16(0), index)
		assert.Equal(t, uint16(3), total)

		priorityField := frame.VarExtHeader.GetField(ExtPriority)
		require.NotNil(t, priorityField)
		priority, _ := DecodePriorityExt(priorityField)
		assert.Equal(t, types.PriorityHigh, priority)
	})

	t.Run("示例3: 批量创建不同优先级的消息", func(t *testing.T) {
		// 场景：批量发送消息时，为不同类型的消息设置不同优先级
		createMessage := func(priority types.Priority) *Frame {
			return NewFrame(12345, 67890, types.MessageTypeGet, uint16(types.CodecTypeMessagePack), 0, []byte("key")).WithPriority(priority).Finalize()
		}

		// 创建三个不同优先级的消息
		lowPriorityMsg := createMessage(types.PriorityLow)
		normalPriorityMsg := createMessage(types.PriorityNormal)
		highPriorityMsg := createMessage(types.PriorityHigh)

		// 验证优先级
		lowField := lowPriorityMsg.VarExtHeader.GetField(ExtPriority)
		lowPriority, _ := DecodePriorityExt(lowField)
		assert.Equal(t, types.PriorityLow, lowPriority)

		normalField := normalPriorityMsg.VarExtHeader.GetField(ExtPriority)
		normalPriority, _ := DecodePriorityExt(normalField)
		assert.Equal(t, types.PriorityNormal, normalPriority)

		highField := highPriorityMsg.VarExtHeader.GetField(ExtPriority)
		highPriority, _ := DecodePriorityExt(highField)
		assert.Equal(t, types.PriorityHigh, highPriority)
	})
}

// ========================================
// TLV 协议集成测试（完整序列化/反序列化）
// ========================================

// TestFrame_FullRoundTrip 完整帧序列化/反序列化测试
func TestFrame_FullRoundTrip(t *testing.T) {
	tests := []struct {
		name      string
		nodeID    uint64
		msgSeq    uint64
		msgType   MessageType
		codecID   uint16
		data      []byte
		extFields []*ExtField
	}{
		{
			name:    "基础帧（无扩展字段）",
			nodeID:  12345,
			msgSeq:  67890,
			msgType: types.MessageTypePut,
			codecID: uint16(types.CodecTypeMessagePack),
			data:    []byte("test data"),
		},
		{
			name:    "带分片扩展的帧",
			nodeID:  99999,
			msgSeq:  88888,
			msgType: types.MessageTypeGet,
			codecID: uint16(types.CodecTypeMessagePack),
			data:    make([]byte, 1000),
			extFields: []*ExtField{
				EncodeFragmentExt(0, 3),
			},
		},
		{
			name:    "带多个扩展字段的帧",
			nodeID:  11111,
			msgSeq:  22222,
			msgType: types.MessageTypeDelete,
			codecID: uint16(types.CodecTypeJSON),
			data:    []byte("delete test"),
			extFields: []*ExtField{
				EncodeFragmentExt(1, 5),
				{
					Type:  ExtPriority,
					Value: []byte{byte(types.PriorityHigh)},
				},
			},
		},
		{
			name:    "大数据帧",
			nodeID:  54321,
			msgSeq:  98765,
			msgType: types.MessageTypePut,
			codecID: uint16(types.CodecTypeMessagePack),
			data:    make([]byte, 10000),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 创建帧
			frame := NewFrame(tt.nodeID, tt.msgSeq, tt.msgType, tt.codecID, 0, tt.data)

			// 添加扩展字段
			frame.VarExtHeader.Fields = append(frame.VarExtHeader.Fields, tt.extFields...)

			// 序列化
			frameData, err := frame.Marshal()
			assert.NoError(t, err, "序列化应该成功")
			assert.NotNil(t, frameData, "序列化数据不应为空")

			// 反序列化
			decodedFrame := &Frame{}
			err = decodedFrame.Unmarshal(frameData)
			assert.NoError(t, err, "反序列化应该成功")

			// 验证 FixedHeader
			assert.Equal(t, tt.nodeID, decodedFrame.FixedHeader.NodeID, "NodeID 应该匹配")
			assert.Equal(t, tt.msgSeq, decodedFrame.FixedHeader.MsgSeq, "MsgSeq 应该匹配")
			assert.Equal(t, tt.msgType, decodedFrame.FixedHeader.MsgType, "MsgType 应该匹配")
			assert.Equal(t, tt.codecID, decodedFrame.FixedHeader.CodecID, "CodecID 应该匹配")

			// 验证 Data
			assert.Equal(t, tt.data, decodedFrame.Data, "Data 应该匹配")

			// 验证扩展字段
			assert.Equal(t, len(tt.extFields), len(decodedFrame.VarExtHeader.Fields), "扩展字段数量应该匹配")
			for i, ext := range tt.extFields {
				decodedExt := decodedFrame.VarExtHeader.Fields[i]
				assert.Equal(t, ext.Type, decodedExt.Type, "扩展字段类型应该匹配")
				assert.Equal(t, ext.Value, decodedExt.Value, "扩展字段值应该匹配")
			}
		})
	}
}

// TestFrame_CRC32Validation 测试 CRC32 校验
func TestFrame_CRC32Validation(t *testing.T) {
	t.Run("正常CRC32校验", func(t *testing.T) {
		frame := NewFrame(12345, 67890, types.MessageTypePut, uint16(types.CodecTypeMessagePack), 0, []byte("test data"))

		// 序列化（会自动计算CRC32）
		frameData, err := frame.Marshal()
		assert.NoError(t, err)

		// 修改数据（破坏CRC32）
		// FixedHeader 是 40 字节，从偏移量 40 开始是 VarExtHeader + Data + CRC32
		// 修改 VarExtHeader 或 Data 部分会破坏 CRC32
		if len(frameData) > 40 {
			frameData[40] ^= 0xFF // 翻转 VarExtHeader 或 Data 的第一个字节

			// 反序列化应该失败（CRC32校验失败）
			decodedFrame := &Frame{}
			err = decodedFrame.Unmarshal(frameData)
			assert.Error(t, err, "CRC32校验应该失败")
		}
	})

	t.Run("截断数据CRC32校验", func(t *testing.T) {
		frame := NewFrame(12345, 67890, types.MessageTypePut, uint16(types.CodecTypeMessagePack), 0, []byte("test data"))

		frameData, err := frame.Marshal()
		assert.NoError(t, err)

		// 截断数据
		truncatedData := frameData[:len(frameData)-5]

		// 反序列化应该失败（数据不完整）
		decodedFrame := &Frame{}
		err = decodedFrame.Unmarshal(truncatedData)
		assert.Error(t, err, "截断数据应该返回错误")
	})
}

// TestFrame_AllExtensionFields 测试所有扩展字段类型
func TestFrame_AllExtensionFields(t *testing.T) {
	t.Run("分片扩展字段", func(t *testing.T) {
		frame := NewFrame(1, 1, types.MessageTypePut, uint16(types.CodecTypeMessagePack), 0, []byte("data"))
		frame.VarExtHeader.Fields = append(frame.VarExtHeader.Fields, EncodeFragmentExt(2, 5))

		frameData, err := frame.Marshal()
		assert.NoError(t, err)

		decodedFrame := &Frame{}
		err = decodedFrame.Unmarshal(frameData)
		assert.NoError(t, err)

		// 验证分片扩展字段
		fragmentField := decodedFrame.VarExtHeader.GetField(ExtFragment)
		assert.NotNil(t, fragmentField, "应该找到分片扩展字段")

		index, total, err := DecodeFragmentExt(fragmentField)
		assert.NoError(t, err)
		assert.Equal(t, uint16(2), index, "分片索引应该匹配")
		assert.Equal(t, uint16(5), total, "总分片数应该匹配")
	})

	t.Run("优先级扩展字段", func(t *testing.T) {
		frame := NewFrame(1, 1, types.MessageTypePut, uint16(types.CodecTypeMessagePack), 0, []byte("data"))
		frame.VarExtHeader.Fields = append(frame.VarExtHeader.Fields, &ExtField{
			Type:  ExtPriority,
			Value: []byte{byte(types.PriorityHigh)},
		})

		frameData, err := frame.Marshal()
		assert.NoError(t, err)

		decodedFrame := &Frame{}
		err = decodedFrame.Unmarshal(frameData)
		assert.NoError(t, err)

		// 验证优先级扩展字段
		priorityField := decodedFrame.VarExtHeader.GetField(ExtPriority)
		assert.NotNil(t, priorityField, "应该找到优先级扩展字段")
		assert.Equal(t, byte(types.PriorityHigh), priorityField.Value[0], "优先级应该匹配")
	})

	t.Run("压缩扩展字段", func(t *testing.T) {
		frame := NewFrame(1, 1, types.MessageTypePut, uint16(types.CodecTypeMessagePack), 0, []byte("data"))
		frame.VarExtHeader.Fields = append(frame.VarExtHeader.Fields, &ExtField{
			Type:  ExtCompress,
			Value: []byte{1}, // 使用压缩算法 1
		})

		frameData, err := frame.Marshal()
		assert.NoError(t, err)

		decodedFrame := &Frame{}
		err = decodedFrame.Unmarshal(frameData)
		assert.NoError(t, err)

		// 验证压缩扩展字段
		compressField := decodedFrame.VarExtHeader.GetField(ExtCompress)
		assert.NotNil(t, compressField, "应该找到压缩扩展字段")
		assert.Equal(t, byte(1), compressField.Value[0], "压缩算法应该匹配")
	})

	t.Run("加密扩展字段", func(t *testing.T) {
		frame := NewFrame(1, 1, types.MessageTypePut, uint16(types.CodecTypeMessagePack), 0, []byte("data"))
		frame.VarExtHeader.Fields = append(frame.VarExtHeader.Fields, &ExtField{
			Type:  ExtEncrypt,
			Value: []byte{2}, // 使用加密算法 2
		})

		frameData, err := frame.Marshal()
		assert.NoError(t, err)

		decodedFrame := &Frame{}
		err = decodedFrame.Unmarshal(frameData)
		assert.NoError(t, err)

		// 验证加密扩展字段
		encryptField := decodedFrame.VarExtHeader.GetField(ExtEncrypt)
		assert.NotNil(t, encryptField, "应该找到加密扩展字段")
		assert.Equal(t, byte(2), encryptField.Value[0], "加密算法应该匹配")
	})
}

// TestFrame_WithFragment_Chain 测试 WithFragment 链式调用
func TestFrame_WithFragment_Chain(t *testing.T) {
	frame := NewFrame(12345, 67890, types.MessageTypePut, uint16(types.CodecTypeMessagePack), 0, []byte("test data"))

	// 使用 WithFragment 添加分片扩展
	frame.WithFragment(1, 10)

	// 验证扩展字段已添加
	assert.Equal(t, 1, len(frame.VarExtHeader.Fields), "应该有一个扩展字段")

	fragmentField := frame.VarExtHeader.GetField(ExtFragment)
	assert.NotNil(t, fragmentField, "应该找到分片扩展字段")

	index, total, err := DecodeFragmentExt(fragmentField)
	assert.NoError(t, err)
	assert.Equal(t, uint16(1), index, "分片索引应该是1")
	assert.Equal(t, uint16(10), total, "总分片数应该是10")
}

// TestFrame_EdgeCases 测试边界情况
func TestFrame_EdgeCases(t *testing.T) {
	t.Run("空数据帧", func(t *testing.T) {
		frame := NewFrame(1, 1, types.MessageTypePut, uint16(types.CodecTypeMessagePack), FlagsOneWayRequest, []byte{})

		frameData, err := frame.Marshal()
		assert.NoError(t, err)

		decodedFrame := &Frame{}
		err = decodedFrame.Unmarshal(frameData)
		assert.NoError(t, err)
		assert.Empty(t, decodedFrame.Data, "数据应该为空") // 使用 Empty 而不是 Equal
	})

	t.Run("最大NodeID和MsgSeq", func(t *testing.T) {
		frame := NewFrame(0xFFFFFFFFFFFFFFFF, 0xFFFFFFFFFFFFFFFF, types.MessageTypePut, uint16(types.CodecTypeMessagePack), 0, []byte("test"))

		frameData, err := frame.Marshal()
		assert.NoError(t, err)

		decodedFrame := &Frame{}
		err = decodedFrame.Unmarshal(frameData)
		assert.NoError(t, err)
		assert.Equal(t, uint64(0xFFFFFFFFFFFFFFFF), decodedFrame.FixedHeader.NodeID)
		assert.Equal(t, uint64(0xFFFFFFFFFFFFFFFF), decodedFrame.FixedHeader.MsgSeq)
	})

	t.Run("大量扩展字段", func(t *testing.T) {
		frame := NewFrame(1, 1, types.MessageTypePut, uint16(types.CodecTypeMessagePack), 0, []byte("test"))

		// 添加10个扩展字段
		for i := 0; i < 10; i++ {
			frame.VarExtHeader.Fields = append(frame.VarExtHeader.Fields, &ExtField{
				Type:  ExtFieldType(i),
				Value: []byte{byte(i)},
			})
		}

		frameData, err := frame.Marshal()
		assert.NoError(t, err)

		decodedFrame := &Frame{}
		err = decodedFrame.Unmarshal(frameData)
		assert.NoError(t, err)
		assert.Equal(t, 10, len(decodedFrame.VarExtHeader.Fields), "应该有10个扩展字段")
	})
}

// ========================================
// CRC32 校验范围验证测试（P2-FR-005）
// ========================================

// TestFrame_ValidateCRC32 测试 ValidateCRC32 方法
func TestFrame_ValidateCRC32(t *testing.T) {
	t.Run("正常CRC32校验通过", func(t *testing.T) {
		frame := NewFrame(12345, 67890, types.MessageTypePut, uint16(types.CodecTypeMessagePack), 0, []byte("test data"))

		// Finalize 会自动计算 CRC32
		frame.Finalize()

		// ValidateCRC32 应该通过
		err := frame.ValidateCRC32()
		assert.NoError(t, err, "CRC32 校验应该通过")
	})

	t.Run("CRC32校验失败", func(t *testing.T) {
		frame := NewFrame(12345, 67890, types.MessageTypePut, uint16(types.CodecTypeMessagePack), 0, []byte("test data"))

		// Finalize 会自动计算 CRC32
		frame.Finalize()

		// 修改 CRC32 值以模拟校验失败
		originalCRC := frame.CRC32
		frame.CRC32 = originalCRC ^ 0xFFFFFFFF

		// ValidateCRC32 应该失败
		err := frame.ValidateCRC32()
		assert.Error(t, err, "CRC32 校验应该失败")
		if cerr, ok := err.(*types.Error); ok {
			assert.Equal(t, types.ErrCodeFrameChecksum, cerr.Code)
		}
	})

	t.Run("修改数据后CRC32校验失败", func(t *testing.T) {
		frame := NewFrame(12345, 67890, types.MessageTypePut, uint16(types.CodecTypeMessagePack), 0, []byte("test data"))

		// Finalize 会自动计算 CRC32
		frame.Finalize()

		// 修改数据（不重新计算 CRC32）
		frame.Data = []byte("modified data")

		// ValidateCRC32 应该失败
		err := frame.ValidateCRC32()
		assert.Error(t, err, "数据修改后 CRC32 校验应该失败")
	})
}

// TestFrame_GetCRCScope 测试 GetCRCScope 方法
func TestFrame_GetCRCScope(t *testing.T) {
	t.Run("无扩展头帧的CRC范围", func(t *testing.T) {
		frame := NewFrame(12345, 67890, types.MessageTypePut, uint16(types.CodecTypeMessagePack), 0, []byte("test data"))

		crcScope := frame.GetCRCScope()

		// CRC 范围应该包含：VarExtHeader（空） + Data
		expectedSize := 0 + len("test data") // VarExtHeader (空) + Data
		assert.Equal(t, expectedSize, len(crcScope), "CRC 范围大小应该匹配")

		// CRC 范围的数据应该与原始数据匹配
		expectedData := []byte("test data")
		assert.Equal(t, expectedData, crcScope, "CRC 范围数据应该包含原始数据")
	})

	t.Run("带扩展头帧的CRC范围", func(t *testing.T) {
		frame := NewFrame(12345, 67890, types.MessageTypePut, uint16(types.CodecTypeMessagePack), 0, []byte("test data"))
		frame.VarExtHeader.Fields = append(frame.VarExtHeader.Fields, EncodeFragmentExt(1, 3))

		crcScope := frame.GetCRCScope()

		// CRC 范围应该包含：VarExtHeader + Data
		// VarExtHeader: ExtCount(2) + [Type(2) + Length(2) + Value(N)]...
		expectedMinSize := len("test data") + 2 // 至少包含 Data + ExtCount
		assert.GreaterOrEqual(t, len(crcScope), expectedMinSize, "CRC 范围应该包含扩展头和数据")

		// 验证 Data 在 CRC 范围内
		dataFound := false
		for i := 0; i <= len(crcScope)-len(frame.Data); i++ {
			if string(crcScope[i:i+len(frame.Data)]) == string(frame.Data) {
				dataFound = true
				break
			}
		}
		assert.True(t, dataFound, "Data 应该在 CRC 范围内")
	})

	t.Run("CRC范围不包含FixedHeader", func(t *testing.T) {
		frame := NewFrame(12345, 67890, types.MessageTypePut, uint16(types.CodecTypeMessagePack), 0, []byte("test data"))

		crcScope := frame.GetCRCScope()
		fixedHeaderData := frame.FixedHeader.Serialize()

		// CRC 范围不应该包含 FixedHeader
		assert.NotContains(t, string(crcScope), string(fixedHeaderData[:4]), "CRC 范围不应该包含 Magic")
	})

	t.Run("CRC范围不包含CRC32字段本身", func(t *testing.T) {
		frame := NewFrame(12345, 67890, types.MessageTypePut, uint16(types.CodecTypeMessagePack), 0, []byte("test data"))
		frame.Finalize()

		crcScope := frame.GetCRCScope()

		// CRC32 的字节表示不应该在 CRC 范围内
		crcBytes := make([]byte, 4)
		binary.BigEndian.PutUint32(crcBytes, frame.CRC32)

		// 检查 CRC32 字节不在 CRC 范围内（简单检查：不匹配连续 4 字节）
		crc32FoundInScope := false
		for i := 0; i <= len(crcScope)-4; i++ {
			if crcScope[i] == crcBytes[0] && crcScope[i+1] == crcBytes[1] &&
				crcScope[i+2] == crcBytes[2] && crcScope[i+3] == crcBytes[3] {
				crc32FoundInScope = true
				break
			}
		}
		assert.False(t, crc32FoundInScope, "CRC 范围不应该包含 CRC32 字段本身")
	})
}

// TestFrame_CRC32ScopeConsistency 测试 CRC32 范围一致性
func TestFrame_CRC32ScopeConsistency(t *testing.T) {
	testCases := []struct {
		name      string
		extFields []*ExtField
		data      []byte
	}{
		{
			name:      "无扩展头无数据",
			extFields: []*ExtField{},
			data:      []byte{},
		},
		{
			name:      "无扩展头有数据",
			extFields: []*ExtField{},
			data:      []byte("hello world"),
		},
		{
			name: "有扩展头无数据",
			extFields: []*ExtField{
				EncodeFragmentExt(0, 1),
			},
			data: []byte{},
		},
		{
			name: "有扩展头有数据",
			extFields: []*ExtField{
				EncodeFragmentExt(1, 5),
				{
					Type:  ExtPriority,
					Value: []byte{byte(types.PriorityHigh)},
				},
			},
			data: []byte("test data with extension"),
		},
		{
			name: "大数据帧",
			extFields: []*ExtField{
				EncodeFragmentExt(0, 10),
			},
			data: make([]byte, 10000),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			frame := NewFrame(12345, 67890, types.MessageTypePut, uint16(types.CodecTypeMessagePack), 0, tc.data)
			frame.VarExtHeader.Fields = append(frame.VarExtHeader.Fields, tc.extFields...)

			// Finalize 会计算 CRC32
			frame.Finalize()

			// 获取 CRC 范围
			crcScope := frame.GetCRCScope()

			// 手动计算 CRC32
			calculatedCRC := crc32.ChecksumIEEE(crcScope)

			// 应该与帧的 CRC32 匹配
			assert.Equal(t, frame.CRC32, calculatedCRC, "手动计算的 CRC32 应该与帧的 CRC32 匹配")

			// ValidateCRC32 应该通过
			err := frame.ValidateCRC32()
			assert.NoError(t, err, "ValidateCRC32 应该通过")
		})
	}
}
