// Package transport TLV 协议单元测试
package transport

import (
	"bytes"
	"testing"

	"github.com/jzhang405/NexKV/internal/metadata/types"
	"github.com/stretchr/testify/assert"
)

// TestFixedHeader 测试固定头序列化/反序列化
func TestFixedHeader(t *testing.T) {
	nodeID := uint64(12345)
	msgID := uint16(100)
	codecID := uint16(1)

	// 创建固定头
	header := NewFixedHeader(nodeID, msgID, codecID)

	// 序列化
	data := header.Serialize()
	assert.Len(t, data, FixedHeaderLen, "固定头长度应为 16 字节")

	// 反序列化
	header2, err := DeserializeFixedHeader(data)
	assert.NoError(t, err, "反序列化应成功")
	assert.Equal(t, header.NodeID, header2.NodeID, "NodeID 应匹配")
	assert.Equal(t, header.MsgID, header2.MsgID, "MsgID 应匹配")
	assert.Equal(t, header.CodecID, header2.CodecID, "CodecID 应匹配")
}

// TestFixedHeaderInvalidMagic 测试无效魔术字
func TestFixedHeaderInvalidMagic(t *testing.T) {
	// 创建无效魔术字的数据
	data := make([]byte, FixedHeaderLen)
	data[0] = 0x00
	data[1] = 0x00
	data[2] = 0x00
	data[3] = 0x00

	// 反序列化应失败
	_, err := DeserializeFixedHeader(data)
	assert.Error(t, err, "无效魔术字应返回错误")
}

// TestVarExtHeader 测试变长扩展头
func TestVarExtHeader(t *testing.T) {
	// 创建扩展字段
	compressExt := &ExtField{
		Type:  ExtCompress,
		Value: []byte{0x01, 0x02},
	}

	priorityExt := &ExtField{
		Type:  ExtPriority,
		Value: []byte{0x05},
	}

	// 创建变长扩展头
	header := NewVarExtHeader(compressExt, priorityExt)

	// 序列化
	data := header.Serialize()
	assert.True(t, len(data) > 2, "扩展头长度应大于 2 字节")

	// 反序列化
	header2, err := DeserializeVarExtHeader(data)
	assert.NoError(t, err, "反序列化应成功")
	assert.Len(t, header2.Fields, 2, "应有 2 个扩展字段")

	// 验证字段类型
	compressField := header2.GetField(ExtCompress)
	assert.NotNil(t, compressField, "应找到压缩扩展字段")
	assert.Equal(t, compressExt.Value, compressField.Value, "压缩扩展值应匹配")

	priorityField := header2.GetField(ExtPriority)
	assert.NotNil(t, priorityField, "应找到优先级扩展字段")
	assert.Equal(t, priorityExt.Value, priorityField.Value, "优先级扩展值应匹配")
}

// TestExtField 测试扩展字段
func TestExtField(t *testing.T) {
	field := &ExtField{
		Type:  ExtCompress,
		Value: []byte{0x01, 0x02, 0x03},
	}

	// 序列化
	data := field.Serialize()
	assert.Len(t, data, 4+3, "扩展字段长度应为 4 + len(Value)")

	// 反序列化
	field2, err := DeserializeExtField(data)
	assert.NoError(t, err, "反序列化应成功")
	assert.Equal(t, field.Type, field2.Type, "字段类型应匹配")
	assert.Equal(t, field.Value, field2.Value, "字段值应匹配")
}

// TestTLVMessage 测试 TLV 消息
func TestTLVMessage(t *testing.T) {
	nodeID := uint64(12345)
	msgID := uint16(100)
	codecID := uint16(1)
	data := []byte("test data")

	// 创建 TLV 消息
	msg := NewTLVMessage(nodeID, msgID, codecID, data)

	// 验证固定头
	assert.Equal(t, nodeID, msg.FixedHeader.NodeID)
	assert.Equal(t, msgID, msg.FixedHeader.MsgID)
	assert.Equal(t, codecID, msg.FixedHeader.CodecID)

	// 验证数据
	assert.Equal(t, data, msg.Data)

	// 序列化
	msgData, err := msg.Marshal()
	assert.NoError(t, err, "序列化应成功")
	assert.True(t, len(msgData) > FixedHeaderLen+CRCLen, "消息长度应大于固定头 + CRC")

	// 反序列化
	msg2 := &TLVMessage{}
	err = msg2.Unmarshal(msgData)
	assert.NoError(t, err, "反序列化应成功")

	// 验证内容
	assert.Equal(t, msg.FixedHeader.NodeID, msg2.FixedHeader.NodeID)
	assert.Equal(t, msg.FixedHeader.MsgID, msg2.FixedHeader.MsgID)
	assert.Equal(t, msg.FixedHeader.CodecID, msg2.FixedHeader.CodecID)
	assert.Equal(t, msg.Data, msg2.Data)
}

// TestTLVMessageCRC32 测试 CRC32 校验
func TestTLVMessageCRC32(t *testing.T) {
	// 创建有效的 TLV 消息
	msg := NewTLVMessage(12345, 100, 1, []byte("test data"))
	data, err := msg.Marshal()
	assert.NoError(t, err)

	// 反序列化应成功（CRC32 校验通过）
	msg2 := &TLVMessage{}
	err = msg2.Unmarshal(data)
	assert.NoError(t, err)

	// 篡改数据，CRC32 校验应失败
	tamperedData := make([]byte, len(data))
	copy(tamperedData, data)
	tamperedData[len(data)-2]++ // 修改倒数第二个字节

	msg3 := &TLVMessage{}
	err = msg3.Unmarshal(tamperedData)
	assert.Error(t, err, "篡改数据后 CRC32 校验应失败")
}

// TestSegmentExt 测试分片扩展
func TestSegmentExt(t *testing.T) {
	segment := NewSegmentExt(2, 5)

	// 序列化
	data, err := segment.Serialize()
	assert.NoError(t, err, "序列化应成功")
	assert.True(t, len(data) > 0, "序列化数据不应为空")

	// 反序列化
	segment2, err := DeserializeSegmentExt(data)
	assert.NoError(t, err, "反序列化应成功")
	assert.Equal(t, segment.Index, segment2.Index, "索引应匹配")
	assert.Equal(t, segment.Total, segment2.Total, "总数应匹配")
}

// TestSegmenter 测试分片器
func TestSegmenter(t *testing.T) {
	segmenter := NewSegmenter()

	// 测试小消息（不需要分片）
	smallData := make([]byte, 1000)
	assert.False(t, segmenter.NeedSegment(len(smallData)), "小消息不需要分片")

	// 测试大消息（需要分片）
	largeData := make([]byte, 2000)
	assert.True(t, segmenter.NeedSegment(len(largeData)), "大消息需要分片")
}

// TestSegmenterSegment 测试分片功能
func TestSegmenterSegment(t *testing.T) {
	segmenter := NewSegmenter()

	// 创建大消息（需要分片）
	data := make([]byte, 20000)
	for i := range data {
		data[i] = byte(i % 256)
	}

	msg := NewTLVMessage(12345, 100, 1, data)

	// 分片
	segments, err := segmenter.Segment(nil, msg)
	assert.NoError(t, err, "分片应成功")
	assert.True(t, len(segments) > 1, "应产生多个分片")

	// 验证每个分片都包含分片扩展
	for i, segment := range segments {
		segmentField := segment.VarExtHeader.GetField(ExtSegment)
		assert.NotNil(t, segmentField, "分片应包含分片扩展字段")

		segmentExt, err := DeserializeSegmentExt(segmentField.Value)
		assert.NoError(t, err, "反序列化分片扩展应成功")
		assert.Equal(t, uint16(i), segmentExt.Index, "分片索引应匹配")
		assert.Equal(t, uint16(len(segments)), segmentExt.Total, "总分片数应匹配")
	}
}

// TestReassembler 测试重组器
func TestReassembler(t *testing.T) {
	reassembler := NewReassembler()
	defer reassembler.Close()

	// 创建测试分片
	totalSegments := uint16(5)
	data := make([]byte, 1000)
	for i := range data {
		data[i] = byte(i % 256)
	}

	// 创建分片消息
	segments := make([]*TLVMessage, totalSegments)
	for i := uint16(0); i < totalSegments; i++ {
		// 创建分片数据
		segmentData := make([]byte, len(data)/int(totalSegments))
		copy(segmentData, data[int(i)*len(segmentData):int(i+1)*len(segmentData)])

		// 创建分片扩展
		segmentExt := NewSegmentExt(i, totalSegments)
		segmentExtData, _ := segmentExt.Serialize()

		// 创建扩展字段
		segmentField := &ExtField{
			Type:  ExtSegment,
			Value: segmentExtData,
		}

		// 创建分片消息
		segments[i] = NewTLVMessage(12345, 100, 1, segmentData)
		segments[i].VarExtHeader.AddField(segmentField)
	}

	// 添加分片（打乱顺序）
	order := []int{2, 0, 4, 1, 3}
	for _, idx := range order {
		reassembledData, _, complete, err := reassembler.AddFragment(segments[idx])
		assert.NoError(t, err, "添加分片应成功")

		if idx == 3 { // 第 4 个分片（最后一个）
			assert.True(t, complete, "最后一个分片后应完成重组")
			assert.NotNil(t, reassembledData, "重组后应有数据")
		} else {
			assert.False(t, complete, "未完成前应返回 false")
		}
	}

	// 验证统计信息
	stats := reassembler.Stats()
	assert.Equal(t, 0, stats["pending_count"], "重组完成后应无待重组消息")
}

// TestTLVCodec 测试 TLV 编解码器
func TestTLVCodec(t *testing.T) {
	codec := NewTLVCodec(types.CodecTypeMessagePack, 12345)

	// 创建测试消息
	testMsg := &testMessage{
		MsgType: MessageType(100),
		Data:    "test data",
	}

	// 编码
	tlvMsg, err := codec.Encode(testMsg)
	assert.NoError(t, err, "编码应成功")
	assert.NotNil(t, tlvMsg, "TLV 消息不应为空")
	assert.Equal(t, uint64(12345), tlvMsg.FixedHeader.NodeID, "NodeID 应匹配")
	assert.Equal(t, uint16(types.CodecTypeMessagePack), tlvMsg.FixedHeader.CodecID, "CodecID 应匹配")
}

// TestTLVCodecExt 测试 TLV 扩展编解码器
func TestTLVCodecExt(t *testing.T) {
	extCodec := NewTLVCodecExt()

	// 测试压缩扩展
	compressField, err := extCodec.EncodeCompressExt(1)
	assert.NoError(t, err, "编码压缩扩展应成功")
	assert.Equal(t, ExtCompress, compressField.Type, "字段类型应为压缩扩展")

	compressID, err := extCodec.DecodeCompressExt(compressField)
	assert.NoError(t, err, "解码压缩扩展应成功")
	assert.Equal(t, uint16(1), compressID, "压缩 ID 应匹配")

	// 测试优先级扩展
	priorityField, err := extCodec.EncodePriorityExt(5)
	assert.NoError(t, err, "编码优先级扩展应成功")
	assert.Equal(t, ExtPriority, priorityField.Type, "字段类型应为优先级扩展")

	priority, err := extCodec.DecodePriorityExt(priorityField)
	assert.NoError(t, err, "解码优先级扩展应成功")
	assert.Equal(t, uint8(5), priority, "优先级应匹配")

	// 测试加密扩展
	encryptField, err := extCodec.EncodeEncryptExt(2, []byte("nonce"), "v1")
	assert.NoError(t, err, "编码加密扩展应成功")
	assert.Equal(t, ExtEncrypt, encryptField.Type, "字段类型应为加密扩展")

	encryptID, nonce, version, err := extCodec.DecodeEncryptExt(encryptField)
	assert.NoError(t, err, "解码加密扩展应成功")
	assert.Equal(t, uint16(2), encryptID, "加密 ID 应匹配")
	assert.Equal(t, []byte("nonce"), nonce, "Nonce 应匹配")
	assert.Equal(t, "v1", version, "版本应匹配")
}

// TestTLVMessageReaderWriter 测试 TLV 消息读写器
func TestTLVMessageReaderWriter(t *testing.T) {
	// 创建测试消息
	msg := NewTLVMessage(12345, 100, 1, []byte("test data"))

	// 序列化
	data, err := msg.Marshal()
	assert.NoError(t, err, "序列化应成功")

	// 创建读写器
	buf := bytes.NewBuffer(data)
	reader := NewTLVMessageReader(buf)

	// 读取消息
	msg2, err := reader.ReadMessage()
	assert.NoError(t, err, "读取消息应成功")
	assert.Equal(t, msg.FixedHeader.NodeID, msg2.FixedHeader.NodeID)
	assert.Equal(t, msg.FixedHeader.MsgID, msg2.FixedHeader.MsgID)
	assert.Equal(t, msg.Data, msg2.Data)
}

// testMessage 测试消息类型
type testMessage struct {
	MsgType MessageType
	Data    string
}

// Type 实现 Message 接口
func (m *testMessage) Type() MessageType {
	return m.MsgType
}
