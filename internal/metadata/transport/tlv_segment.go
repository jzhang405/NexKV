// Package transport UDP 分片与重组实现
//
// 当 UDP 消息超过 1400 字节时，自动进行应用层分片
//
// 分片机制：
// - Segmenter: 将大消息拆分成多个小片段（每个 <= 1400 字节）
// - Reassembler: 将多个片段重组为原始消息
//
// 分片扩展（TLV SegmentExt）：
// - Index: 当前片段索引（从 0 开始）
// - Total: 总片段数
package transport

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/types"
	"github.com/vmihailenco/msgpack/v5"
)

const (
	// ReassemblerTimeout 重组超时时间（30 秒）
	// 如果在 30 秒内没有收到所有片段，丢弃该消息
	ReassemblerTimeout = 30 * time.Second

	// ReassemblerMaxPending 重组器最大待重组消息数
	ReassemblerMaxPending = 1000

	// ReassemblerCleanupInterval 重组器清理间隔（1 分钟）
	ReassemblerCleanupInterval = 1 * time.Minute
)

// SegmentExt 分片扩展（MessagePack 序列化）
//
// 用于标识 UDP 分片的索引和总数
type SegmentExt struct {
	// Index 当前片段索引（从 0 开始）
	Index uint16 `msgpack:"idx"`

	// Total 总片段数
	Total uint16 `msgpack:"tot"`
}

// NewSegmentExt 创建新的分片扩展
func NewSegmentExt(index, total uint16) *SegmentExt {
	return &SegmentExt{
		Index: index,
		Total: total,
	}
}

// Serialize 序列化分片扩展（MessagePack）
func (s *SegmentExt) Serialize() ([]byte, error) {
	return msgpack.Marshal(s)
}

// DeserializeSegmentExt 反序列化分片扩展
func DeserializeSegmentExt(data []byte) (*SegmentExt, error) {
	ext := &SegmentExt{}
	if err := msgpack.Unmarshal(data, ext); err != nil {
		return nil, fmt.Errorf("反序列化分片扩展失败: %w", err)
	}
	return ext, nil
}

// String 返回分片扩展的字符串表示
func (s *SegmentExt) String() string {
	return fmt.Sprintf("SegmentExt{Index=%d, Total=%d}", s.Index, s.Total)
}

// Segmenter 分片器
//
// 将大消息拆分成多个 UDP 安全大小的片段
type Segmenter struct {
	// maxSegmentSize 最大片段大小（字节）
	maxSegmentSize int
}

// NewSegmenter 创建新的分片器
func NewSegmenter() *Segmenter {
	return &Segmenter{
		maxSegmentSize: UDPSafePayloadSize,
	}
}

// NewSegmenterWithSize 创建指定大小的分片器
func NewSegmenterWithSize(maxSize int) *Segmenter {
	return &Segmenter{
		maxSegmentSize: maxSize,
	}
}

// NeedSegment 判断消息是否需要分片
func (s *Segmenter) NeedSegment(dataSize int) bool {
	return dataSize > s.maxSegmentSize
}

// Segment 将消息分片
//
// 返回分片后的 TLV 消息列表
func (s *Segmenter) Segment(ctx context.Context, msg *TLVMessage) ([]*TLVMessage, error) {
	// 计算可用数据大小（除去 FixedHeader、ExtTotalLen、SegmentExt、CRC32）
	// FixedHeaderLen(16) + ExtTotalLen(2) + SegmentExt(约10) + CRCLen(4) = 32 字节
	overhead := FixedHeaderLen + 2 + 10 + CRCLen
	availableDataSize := s.maxSegmentSize - overhead

	if availableDataSize <= 0 {
		return nil, fmt.Errorf("maxSegmentSize 太小，无法分片")
	}

	dataSize := len(msg.Data)
	totalSegments := uint16(math.Ceil(float64(dataSize) / float64(availableDataSize)))

	if totalSegments > 1000 {
		return nil, fmt.Errorf("分片数过多: %d", totalSegments)
	}

	segments := make([]*TLVMessage, totalSegments)

	// 保存原始扩展头（非分片扩展）
	originalExtHeader := msg.VarExtHeader

	// 创建分片
	for i := uint16(0); i < totalSegments; i++ {
		// 计算当前片段的数据范围
		start := int(i) * availableDataSize
		end := start + availableDataSize
		if end > dataSize {
			end = dataSize
		}
		segmentData := msg.Data[start:end]

		// 创建新的扩展头（复制原始扩展头 + 添加分片扩展）
		newExtHeader := NewVarExtHeader()
		for _, field := range originalExtHeader.Fields {
			if field.Type != ExtSegment {
				// 复制非分片扩展字段
				newExtHeader.AddField(field)
			}
		}

		// 序列化分片扩展
		segmentExt := NewSegmentExt(i, totalSegments)
		segmentExtData, err := segmentExt.Serialize()
		if err != nil {
			return nil, fmt.Errorf("序列化分片扩展失败: %w", err)
		}

		// 添加分片扩展字段
		segmentField := &ExtField{
			Type:  ExtSegment,
			Value: segmentExtData,
		}
		newExtHeader.AddField(segmentField)

		// 创建分片消息
		segmentMsg := &TLVMessage{
			FixedHeader:  msg.FixedHeader,
			VarExtHeader: newExtHeader,
			Data:         segmentData,
		}

		segments[i] = segmentMsg
	}

	return segments, nil
}

// tlvPartialMessage TLV 部分消息
//
// 用于重组过程中的消息片段缓存
type tlvPartialMessage struct {
	nodeID    uint64
	msgID     uint16
	total     uint16
	received  uint16
	fragments map[uint16][]byte // Index -> Data
	firstTime time.Time
	extHeader *VarExtHeader     // 保存扩展头（不含分片扩展）
}

// AddFragment 添加片段
func (p *tlvPartialMessage) AddFragment(index uint16, data []byte, extHeader *VarExtHeader) {
	p.fragments[index] = data
	p.received++

	// 保存扩展头（使用第一个片段的扩展头）
	if p.extHeader == nil {
		// 移除分片扩展，保存其他扩展字段
		newExtHeader := NewVarExtHeader()
		for _, field := range extHeader.Fields {
			if field.Type != ExtSegment {
				newExtHeader.AddField(field)
			}
		}
		p.extHeader = newExtHeader
	}
}

// IsComplete 检查是否所有片段都已收到
func (p *tlvPartialMessage) IsComplete() bool {
	return p.received == p.total
}

// Reassemble 重组消息
func (p *tlvPartialMessage) Reassemble() ([]byte, *VarExtHeader, error) {
	if !p.IsComplete() {
		return nil, nil, fmt.Errorf("消息不完整: 收到 %d/%d", p.received, p.total)
	}

	// 按顺序拼接片段
	totalSize := 0
	for i := uint16(0); i < p.total; i++ {
		totalSize += len(p.fragments[i])
	}

	data := make([]byte, totalSize)
	offset := 0
	for i := uint16(0); i < p.total; i++ {
		copy(data[offset:], p.fragments[i])
		offset += len(p.fragments[i])
	}

	return data, p.extHeader, nil
}

// IsExpired 检查是否超时
func (p *tlvPartialMessage) IsExpired(timeout time.Duration) bool	{
	return time.Since(p.firstTime) > timeout
}

// Reassembler 重组器
//
// 将多个 UDP 片段重组为原始消息
type Reassembler struct {
	mu              sync.RWMutex
	pendingMessages map[string]*tlvPartialMessage // key: "nodeID_msgID"
	timeout         time.Duration
	cleanupTicker   *time.Ticker
	done            chan struct{}
}

// NewReassembler 创建新的重组器
func NewReassembler() *Reassembler {
	r := &Reassembler{
		pendingMessages: make(map[string]*tlvPartialMessage),
		timeout:         ReassemblerTimeout,
		cleanupTicker:   time.NewTicker(ReassemblerCleanupInterval),
		done:            make(chan struct{}),
	}

	// 启动清理协程
	go r.cleanupLoop()

	return r
}

// cleanupLoop 清理超时的部分消息
func (r *Reassembler) cleanupLoop() {
	for {
		select {
		case <-r.cleanupTicker.C:
			r.cleanupExpiredMessages()
		case <-r.done:
			r.cleanupTicker.Stop()
			return
		}
	}
}

// cleanupExpiredMessages 清理超时的部分消息
func (r *Reassembler) cleanupExpiredMessages() {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	expiredCount := 0

	for key, pm := range r.pendingMessages {
		if pm.IsExpired(r.timeout) {
			delete(r.pendingMessages, key)
			expiredCount++
		}
	}

	if expiredCount > 0 {
		// 可以添加日志记录
		_ = now
		_ = expiredCount
	}
}

// AddFragment 添加消息片段
//
// 如果消息完整，返回重组后的数据
func (r *Reassembler) AddFragment(msg *TLVMessage) ([]byte, *VarExtHeader, bool, error) {
	// 提取分片扩展
	segmentField := msg.VarExtHeader.GetField(ExtSegment)
	if segmentField == nil {
		// 不是分片消息
		return nil, nil, false, fmt.Errorf("消息不包含分片扩展")
	}

	segmentExt, err := DeserializeSegmentExt(segmentField.Value)
	if err != nil {
		return nil, nil, false, fmt.Errorf("解析分片扩展失败: %w", err)
	}

	// 验证分片索引
	if segmentExt.Index >= segmentExt.Total {
		return nil, nil, false, fmt.Errorf("无效的分片索引: %d >= %d", segmentExt.Index, segmentExt.Total)
	}

	// 生成消息 key
	key := r.generateKey(msg.FixedHeader.NodeID, msg.FixedHeader.MsgID)

	r.mu.Lock()
	defer r.mu.Unlock()

	// 查找或创建部分消息
	pm, exists := r.pendingMessages[key]
	if !exists {
		// 检查待重组消息数量
		if len(r.pendingMessages) >= ReassemblerMaxPending {
			return nil, nil, false, types.NewOpErr(types.ErrCodeTransport, "Reassembler",
				fmt.Sprintf("待重组消息数超限: %d", ReassemblerMaxPending), nil)
		}

		// 创建新的部分消息
		pm = &tlvPartialMessage{
			nodeID:    msg.FixedHeader.NodeID,
			msgID:     msg.FixedHeader.MsgID,
			total:     segmentExt.Total,
			received:  0,
			fragments: make(map[uint16][]byte),
			firstTime: time.Now(),
		}
		r.pendingMessages[key] = pm
	}

	// 验证总片段数是否匹配
	if pm.total != segmentExt.Total {
		return nil, nil, false, fmt.Errorf("分片总数不匹配: 期望 %d，实际 %d", pm.total, segmentExt.Total)
	}

	// 检查是否重复接收
	if _, exists := pm.fragments[segmentExt.Index]; exists {
		// 重复片段，忽略
		return nil, nil, false, nil
	}

	// 添加片段
	pm.AddFragment(segmentExt.Index, msg.Data, msg.VarExtHeader)

	// 检查是否完整
	if pm.IsComplete() {
		// 重组消息
		data, extHeader, err := pm.Reassemble()
		if err != nil {
			// 重组失败，删除部分消息
			delete(r.pendingMessages, key)
			return nil, nil, false, fmt.Errorf("重组消息失败: %w", err)
		}

		// 重组成功，删除部分消息
		delete(r.pendingMessages, key)

		return data, extHeader, true, nil
	}

	// 消息不完整
	return nil, nil, false, nil
}

// generateKey 生成消息 key
func (r *Reassembler) generateKey(nodeID uint64, msgID uint16) string {
	return fmt.Sprintf("%d_%d", nodeID, msgID)
}

// Stats 返回重组器统计信息
func (r *Reassembler) Stats() map[string]interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()

	pendingCount := len(r.pendingMessages)
	totalFragments := 0
	incompleteCount := 0

	for _, pm := range r.pendingMessages {
		totalFragments += int(pm.received)
		if !pm.IsComplete() {
			incompleteCount++
		}
	}

	return map[string]interface{}{
		"pending_count":    pendingCount,
		"total_fragments":  totalFragments,
		"incomplete_count": incompleteCount,
	}
}

// Close 关闭重组器
func (r *Reassembler) Close() error {
	close(r.done)

	r.mu.Lock()
	defer r.mu.Unlock()

	// 清空所有待重组消息
	r.pendingMessages = make(map[string]*tlvPartialMessage)

	return nil
}

// ShouldReassemble 判断消息是否需要重组
//
// 通过检查是否包含分片扩展来判断
func ShouldReassemble(msg *TLVMessage) bool {
	segmentField := msg.VarExtHeader.GetField(ExtSegment)
	return segmentField != nil
}
