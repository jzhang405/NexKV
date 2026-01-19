// Package store WAL 批量写入实现
//
// 提供批量写入能力，减少 fsync 调用次数
// 改善写入吞吐量，特别是在高并发场景下
package store

import (
	"encoding/binary"
	"hash/crc32"
	"io"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/clock"
	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// ========================================
// 批量写入配置
// ========================================

const (
	// DefaultBatchSize 默认批量大小
	// 一次 fsync 可以包含多条 WAL 条目
	DefaultBatchSize = 100

	// MaxBatchSize 最大批量大小
	// 防止内存过度使用
	MaxBatchSize = 1000

	// DefaultBufferSize 默认缓冲区大小
	// 用于累积批量写入的数据
	DefaultBufferSize = 256 * 1024 // 256KB
)

// ========================================
// WALBatchWriter 批量写入器
// ========================================

// WALBatchWriter WAL 批量写入器
//
// 特性：
//   - 缓冲多条 WAL 条目
//   - 一次 fsync 完成批量写入
//   - 自动刷新机制（达到阈值或手动刷新）
//   - 线程安全
type WALBatchWriter struct {
	wal        WAL         // 底层 WAL 实现
	buffer     []*WALEntry // 条目缓冲区
	bufferSize int         // 缓冲区大小（字节）
	mu         sync.Mutex  // 保护并发写入
	batchSize  int         // 批量大小（条目数）
	closed     bool        // 是否已关闭
}

// NewWALBatchWriter 创建批量写入器
func NewWALBatchWriter(wal WAL, batchSize int) *WALBatchWriter {
	if batchSize <= 0 {
		batchSize = DefaultBatchSize
	}
	if batchSize > MaxBatchSize {
		batchSize = MaxBatchSize
	}

	return &WALBatchWriter{
		wal:        wal,
		buffer:     make([]*WALEntry, 0, batchSize),
		bufferSize: 0,
		batchSize:  batchSize,
	}
}

// Append 追加日志条目（缓冲模式）
//
// 行为：
//   - 将条目加入缓冲区
//   - 当缓冲区满时自动刷盘
//   - 返回实际刷盘的条目数
func (w *WALBatchWriter) Append(entry *WALEntry) (int, error) {
	if w.closed {
		return 0, types.NewClosedError("WALBatchWriter")
	}

	if entry == nil {
		return 0, types.NewStoreInvalidParameterError("entry 不能为空")
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// 加入缓冲区
	w.buffer = append(w.buffer, entry)

	// 计算缓冲区大小（估算）
	w.bufferSize += estimateEntrySize(entry)

	// 检查是否需要刷新
	needFlush := len(w.buffer) >= w.batchSize
	flushedCount := 0

	if needFlush {
		count, err := w.flush()
		if err != nil {
			return 0, err
		}
		flushedCount = count
	}

	return flushedCount, nil
}

// AppendBatch 批量追加日志条目
//
// 行为：
//   - 一次性追加多条条目
//   - 自动分批写入（如果超过批量大小）
//   - 返回实际写入的条目数
func (w *WALBatchWriter) AppendBatch(entries []*WALEntry) (int, error) {
	if w.closed {
		return 0, types.NewClosedError("WALBatchWriter")
	}

	if len(entries) == 0 {
		return 0, nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	totalFlushed := 0

	// 分批处理
	for _, entry := range entries {
		if entry == nil {
			continue
		}

		w.buffer = append(w.buffer, entry)
		w.bufferSize += estimateEntrySize(entry)

		// 达到批量大小，刷新
		if len(w.buffer) >= w.batchSize {
			count, err := w.flush()
			if err != nil {
				return totalFlushed, err
			}
			totalFlushed += count
		}
	}

	return totalFlushed, nil
}

// Flush 手动刷新缓冲区
//
// 强制将缓冲区中的所有条目写入磁盘
// 返回实际写入的条目数
func (w *WALBatchWriter) Flush() (int, error) {
	if w.closed {
		return 0, types.NewClosedError("WALBatchWriter")
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	return w.flush()
}

// flush 内部刷新方法（不加锁）
func (w *WALBatchWriter) flush() (int, error) {
	if len(w.buffer) == 0 {
		return 0, nil
	}

	// 批量写入
	if err := w.writeBatch(w.buffer); err != nil {
		return 0, err
	}

	count := len(w.buffer)

	// 清空缓冲区
	w.buffer = make([]*WALEntry, 0, w.batchSize)
	w.bufferSize = 0

	return count, nil
}

// writeBatch 批量写入底层 WAL
func (w *WALBatchWriter) writeBatch(entries []*WALEntry) error {
	// 使用底层 WAL 的 Append 方法逐条写入
	// TODO: 优化为单次 fsync 写入多条
	for _, entry := range entries {
		if err := w.wal.Append(entry); err != nil {
			return types.NewStoreWALError("批量写入", err)
		}
	}

	return nil
}

// BufferedCount 获取缓冲区中的条目数
func (w *WALBatchWriter) BufferedCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.buffer)
}

// BufferedSize 获取缓冲区大小（字节）
func (w *WALBatchWriter) BufferedSize() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.bufferSize
}

// Close 关闭批量写入器
//
// 注意：
//   - 会自动刷新剩余缓冲区
//   - 关闭后不能再写入
func (w *WALBatchWriter) Close() error {
	if w.closed {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// 刷新剩余条目
	if len(w.buffer) > 0 {
		if _, err := w.flush(); err != nil {
			return err
		}
	}

	w.closed = true
	return nil
}

// ========================================
// 辅助函数
// ========================================

// estimateEntrySize 估算条目大小
func estimateEntrySize(entry *WALEntry) int {
	size := 12 // Header 固定大小
	size += len(entry.Key)
	size += len(entry.Value)
	size += len(entry.OldValue)
	size += 10 // HLC 时间戳固定大小
	size += 4  // CRC32
	return size
}

// ========================================
// 批量读取器（用于 Recover 优化）
// ========================================

// WALBatchReader WAL 批量读取器
//
// 用于优化 Recover 性能：
//   - 批量读取 WAL 条目
//   - 减少系统调用次数
//   - 提供流式读取接口
type WALBatchReader struct {
	reader io.Reader
	buffer []byte
}

// NewWALBatchReader 创建批量读取器
func NewWALBatchReader(reader io.Reader, bufferSize int) *WALBatchReader {
	if bufferSize <= 0 {
		bufferSize = DefaultBufferSize
	}

	return &WALBatchReader{
		reader: reader,
		buffer: make([]byte, bufferSize),
	}
}

// ReadBatch 批量读取 WAL 条目
//
// 一次读取多条条目，减少 I/O 次数
// 返回读取的条目数和错误
func (r *WALBatchReader) ReadBatch(maxCount int) ([]*WALEntry, error) {
	var entries []*WALEntry

	for i := 0; i < maxCount; i++ {
		// 读取 Header (16 字节)
		header := make([]byte, walHeaderSize)
		if _, err := io.ReadFull(r.reader, header); err != nil {
			if err == io.EOF {
				break
			}
			return nil, types.NewInternalError("读取 WAL header 失败", err)
		}

		// 解析 Header
		// Header 格式: Magic(4) + Type(2) + KeyLen(4) + ValueLen(4) + OldValueLen(4) + TimestampLen(2) + CRC(4)
		magic := string(header[0:4])
		if magic != walMagic {
			return nil, types.NewInternalError("WAL 条目魔术字不匹配", nil)
		}

		typ := WALType(binary.BigEndian.Uint16(header[4:6]))
		keyLen := binary.BigEndian.Uint32(header[6:10])
		valueLen := binary.BigEndian.Uint32(header[10:14])
		oldValueLen := binary.BigEndian.Uint32(header[14:18])
		timestampSize := binary.BigEndian.Uint16(header[18:20])
		headerCRC := binary.BigEndian.Uint32(header[20:24])

		// 读取 Data
		dataSize := uint32(keyLen) + uint32(valueLen) + uint32(timestampSize) + oldValueLen
		data := make([]byte, dataSize)
		if _, err := io.ReadFull(r.reader, data); err != nil {
			if err == io.EOF {
				break
			}
			return nil, types.NewInternalError("读取 WAL data 失败", err)
		}

		// 验证校验和 (覆盖 Header[0:20] + Data)
		// 注意：不包含 header[20:24]（CRC 字段本身）
		computedChecksum := crc32.ChecksumIEEE(append(header[:20], data...))
		if computedChecksum != headerCRC {
			return nil, types.NewInternalError("WAL 条目校验和不匹配", nil)
		}

		// 解析 Entry
		entry, err := r.decodeEntry(typ, keyLen, valueLen, oldValueLen, timestampSize, data)
		if err != nil {
			return nil, types.NewInternalError("解码 WAL 条目失败", err)
		}

		entries = append(entries, entry)
	}

	return entries, nil
}

// decodeEntry 解码 WAL 条目
//
// 从 WAL 二进制格式解析 WALEntry
// 数据格式: key + value + oldvalue + timestamp
func (r *WALBatchReader) decodeEntry(typ WALType, keyLen, valueLen, oldValueLen uint32, timestampSize uint16, data []byte) (*WALEntry, error) {
	entry := &WALEntry{
		Type: typ,
	}

	offset := 0

	// 解析 Key（第一位）
	if keyLen > 0 {
		entry.Key = make([]byte, keyLen)
		copy(entry.Key, data[offset:offset+int(keyLen)])
	}
	offset += int(keyLen)

	// 解析 Value（第二位）
	if valueLen > 0 {
		entry.Value = make([]byte, valueLen)
		copy(entry.Value, data[offset:offset+int(valueLen)])
	}
	offset += int(valueLen)

	// 解析 OldValue（第三位）
	if oldValueLen > 0 {
		entry.OldValue = make([]byte, oldValueLen)
		copy(entry.OldValue, data[offset:offset+int(oldValueLen)])
	}
	offset += int(oldValueLen)

	// 解析时间戳（最后一位）
	timestampData := data[offset : offset+int(timestampSize)]
	entry.Timestamp = &clock.HLC{}
	if err := entry.Timestamp.UnmarshalBinary(timestampData); err != nil {
		return nil, err
	}

	return entry, nil
}

// ========================================
// 批量写入优化（未来扩展）
// ========================================

// WALGroupCommit 组提交管理器
//
// 将多个写入请求组合成一次 fsync
// 提高写入吞吐量，特别是在高并发场景
type WALGroupCommit struct {
	wal         WAL
	pending     []*commitRequest
	mu          sync.Mutex
	flushCh     chan struct{}
	batchSize   int
	batchWaitMs int // 批次等待时间（毫秒）
}

type commitRequest struct {
	entry  *WALEntry
	result chan error
}

// NewWALGroupCommit 创建组提交管理器
func NewWALGroupCommit(wal WAL, batchSize int) *WALGroupCommit {
	gc := &WALGroupCommit{
		wal:         wal,
		pending:     make([]*commitRequest, 0, batchSize),
		flushCh:     make(chan struct{}, 1),
		batchSize:   batchSize,
		batchWaitMs: 10, // 默认等待 10ms
	}

	// 启动后台 flush 协程
	go gc.flushLoop()

	return gc
}

// Commit 提交写入请求
func (g *WALGroupCommit) Commit(entry *WALEntry) error {
	resultCh := make(chan error, 1)

	g.mu.Lock()
	g.pending = append(g.pending, &commitRequest{
		entry:  entry,
		result: resultCh,
	})
	shouldFlush := len(g.pending) >= g.batchSize
	g.mu.Unlock()

	if shouldFlush {
		g.flushCh <- struct{}{}
	}

	return <-resultCh
}

// flush 执行批量刷新（后台协程）
//
// 执行流程：
//  1. 收集一批请求（最多等待 batchWaitMs）
//  2. 批量写入 WAL
//  3. 通知所有等待者
func (g *WALGroupCommit) flush() {
	g.mu.Lock()
	defer g.mu.Unlock()

	if len(g.pending) == 0 {
		return
	}

	// 收集当前批次
	batch := make([]*commitRequest, len(g.pending))
	copy(batch, g.pending)
	g.pending = g.pending[:0] // 清空 pending

	// 批量写入 WAL
	var firstErr error
	for _, req := range batch {
		if err := g.wal.Append(req.entry); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	// 通知所有等待者
	for _, req := range batch {
		if firstErr != nil {
			req.result <- firstErr
		} else {
			req.result <- nil
		}
		close(req.result)
	}
}

// flushLoop 后台 flush 协程
//
// 两种触发方式：
//  1. 达到 batchSize，通过 flushCh 触发立即 flush
//  2. 超过 batchWaitMs，定时器触发延迟 flush
func (g *WALGroupCommit) flushLoop() {
	timer := time.NewTimer(time.Duration(g.batchWaitMs) * time.Millisecond)
	defer timer.Stop()

	for {
		select {
		case <-g.flushCh:
			// 立即 flush（达到批量大小）
			if !timer.Stop() {
				<-timer.C
			}
			g.flush()
			timer.Reset(time.Duration(g.batchWaitMs) * time.Millisecond)

		case <-timer.C:
			// 延迟 flush（超时）
			g.flush()
			timer.Reset(time.Duration(g.batchWaitMs) * time.Millisecond)
		}
	}
}

// verifyBatchChecksums 验证批量条目的校验和
//
// 用于批量写入后的完整性验证
// 返回每个条目的校验和（Header + Data）
func verifyBatchChecksums(entries []*WALEntry) []uint32 {
	checksums := make([]uint32, len(entries))

	for i, entry := range entries {
		// 序列化 HLC 时间戳
		var timestampData []byte
		if entry.Timestamp != nil {
			var err error
			timestampData, err = entry.Timestamp.MarshalBinary()
			if err != nil {
				// 如果序列化失败，使用零值
				timestampData = make([]byte, 10)
			}
		} else {
			// 使用零值 HLC
			hlc := clock.NewHLC()
			var err error
			timestampData, err = hlc.MarshalBinary()
			if err != nil {
				timestampData = make([]byte, 10)
			}
		}

		// 获取 OldValue 长度
		oldValueLen := uint32(len(entry.OldValue))

		// 构建 Header (16 字节)
		header := make([]byte, walHeaderSize)
		binary.BigEndian.PutUint16(header[0:2], uint16(entry.Type))
		binary.BigEndian.PutUint32(header[2:6], uint32(len(entry.Key)))
		binary.BigEndian.PutUint32(header[6:10], uint32(len(entry.Value)))
		binary.BigEndian.PutUint16(header[10:12], uint16(len(timestampData)))
		binary.BigEndian.PutUint32(header[12:16], oldValueLen)

		// 构建 Data
		data := make([]byte, 0, walHeaderSize+len(timestampData)+len(entry.Key)+len(entry.Value)+len(entry.OldValue))
		data = append(data, header...)
		data = append(data, timestampData...)
		data = append(data, []byte(entry.Key)...)
		data = append(data, entry.Value...)
		data = append(data, entry.OldValue...)

		// 计算 CRC32 校验和
		checksums[i] = crc32.ChecksumIEEE(data)
	}

	return checksums
}
