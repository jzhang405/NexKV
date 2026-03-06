// Package wal provides Write-Ahead Logging (WAL) for crash recovery.
package wal

import (
	"encoding/binary"
	"hash/crc32"
	"time"
)

// LSN 日志序列号（Log Sequence Number），唯一标识每条 WAL 记录
type LSN uint64

const (
	// LSNInvalid 无效 LSN
	LSNInvalid LSN = 0
)

// WALType WAL 日志类型
type WALType uint8

const (
	// WALTypeInsert 插入操作
	WALTypeInsert WALType = iota
	// WALTypeUpdate 更新操作
	WALTypeUpdate
	// WALTypeDelete 删除操作
	WALTypeDelete
	// WALTypeCommit 事务提交
	WALTypeCommit
	// WALTypeRollback 事务回滚
	WALTypeRollback
	// WALTypeCheckpoint 检查点
	WALTypeCheckpoint
)

// String 返回 WALType 的字符串表示
func (wt WALType) String() string {
	switch wt {
	case WALTypeInsert:
		return "Insert"
	case WALTypeUpdate:
		return "Update"
	case WALTypeDelete:
		return "Delete"
	case WALTypeCommit:
		return "Commit"
	case WALTypeRollback:
		return "Rollback"
	case WALTypeCheckpoint:
		return "Checkpoint"
	default:
		return "Unknown"
	}
}

// WALEntry WAL 日志条目
type WALEntry struct {
	// LSN 日志序列号（在 Append 时分配）
	LSN LSN
	// TxID 事务ID（0 = 非事务操作）
	TxID uint64
	// Timestamp Unix 时间戳（微秒）
	Timestamp int64
	// Type 日志类型
	Type WALType
	// Key 键
	Key []byte
	// Value 值
	Value []byte
	// PrevLSN 前一条日志的 LSN（用于事务链）
	PrevLSN LSN
	// CRC32 校验和
	CRC uint32
}

// NewWALEntry 创建新的 WAL 日志条目
func NewWALEntry(entryType WALType, txID uint64, key, value []byte, prevLSN LSN) *WALEntry {
	now := time.Now().UnixMicro()
	return &WALEntry{
		TxID:      txID,
		Timestamp: now,
		Type:      entryType,
		Key:       key,
		Value:     value,
		PrevLSN:   prevLSN,
	}
}

// Marshal 序列化 WAL 日志条目
// 格式：[CRC:4][LSN:8][Type:1][TxID:8][Timestamp:8][PrevLSN:8][KeyLen:4][ValueLen:4][Key:N][Value:M]
func (e *WALEntry) Marshal() ([]byte, error) {
	// 计算总长度
	keyLen := len(e.Key)
	valueLen := len(e.Value)
	totalLen := 4 + 8 + 1 + 8 + 8 + 8 + 4 + 4 + keyLen + valueLen // CRC + LSN + Type + TxID + Timestamp + PrevLSN + KeyLen + ValueLen + Key + Value

	buf := make([]byte, totalLen)
	offset := 0

	// CRC（预留，最后计算）
	offset += 4

	// LSN
	binary.BigEndian.PutUint64(buf[offset:], uint64(e.LSN))
	offset += 8

	// Type
	buf[offset] = byte(e.Type)
	offset++

	// TxID
	binary.BigEndian.PutUint64(buf[offset:], e.TxID)
	offset += 8

	// Timestamp
	binary.BigEndian.PutUint64(buf[offset:], uint64(e.Timestamp))
	offset += 8

	// PrevLSN
	binary.BigEndian.PutUint64(buf[offset:], uint64(e.PrevLSN))
	offset += 8

	// KeyLen
	binary.BigEndian.PutUint32(buf[offset:], uint32(keyLen))
	offset += 4

	// ValueLen
	binary.BigEndian.PutUint32(buf[offset:], uint32(valueLen))
	offset += 4

	// Key
	if keyLen > 0 {
		copy(buf[offset:], e.Key)
		offset += keyLen
	}

	// Value
	if valueLen > 0 {
		copy(buf[offset:], e.Value)
	}

	// 计算并写入 CRC（不包括 CRC 字段本身）
	e.CRC = crc32.ChecksumIEEE(buf[4:])
	binary.BigEndian.PutUint32(buf[0:], e.CRC)

	return buf, nil
}

// Unmarshal 反序列化 WAL 日志条目
func (e *WALEntry) Unmarshal(data []byte) error {
	if len(data) < 4+8+1+8+8+8+4+4 {
		return ErrWALEntryCorrupted
	}

	offset := 0

	// CRC
	crc := binary.BigEndian.Uint32(data[offset:])
	offset += 4

	// 验证 CRC
	actualCRC := crc32.ChecksumIEEE(data[4:])
	if actualCRC != crc {
		return ErrWALChecksumMismatch
	}

	// LSN
	e.LSN = LSN(binary.BigEndian.Uint64(data[offset:]))
	offset += 8

	// Type
	e.Type = WALType(data[offset])
	offset++

	// TxID
	e.TxID = binary.BigEndian.Uint64(data[offset:])
	offset += 8

	// Timestamp
	e.Timestamp = int64(binary.BigEndian.Uint64(data[offset:]))
	offset += 8

	// PrevLSN
	e.PrevLSN = LSN(binary.BigEndian.Uint64(data[offset:]))
	offset += 8

	// KeyLen
	keyLen := int(binary.BigEndian.Uint32(data[offset:]))
	offset += 4

	// ValueLen
	valueLen := int(binary.BigEndian.Uint32(data[offset:]))
	offset += 4

	// 检查数据长度
	if len(data) < offset+keyLen+valueLen {
		return ErrWALEntryCorrupted
	}

	// Key
	if keyLen > 0 {
		e.Key = make([]byte, keyLen)
		copy(e.Key, data[offset:offset+keyLen])
		offset += keyLen
	}

	// Value
	if valueLen > 0 {
		e.Value = make([]byte, valueLen)
		copy(e.Value, data[offset:offset+valueLen])
	}

	// 保存 CRC
	e.CRC = crc

	return nil
}

// WALConfig WAL 配置
type WALConfig struct {
	// Dir WAL 目录
	Dir string
	// SegmentSize 分段大小（字节），默认 64MB
	SegmentSize int64
	// SyncPolicy 同步策略
	SyncPolicy SyncPolicy
}

// SyncPolicy 同步策略
type SyncPolicy int

const (
	// SyncPolicyEveryWrite 每次写入都同步
	SyncPolicyEveryWrite SyncPolicy = iota
	// SyncPolicyEverySecond 每秒同步
	SyncPolicyEverySecond
	// SyncPolicyBatch 批量同步
	SyncPolicyBatch
)

// DefaultWALConfig 返回默认 WAL 配置
func DefaultWALConfig() *WALConfig {
	return &WALConfig{
		SegmentSize: 64 * 1024 * 1024, // 64MB
		SyncPolicy:  SyncPolicyEveryWrite,
	}
}

// Validate 验证配置
func (c *WALConfig) Validate() error {
	if c.Dir == "" {
		return ErrInvalidWALConfig
	}
	if c.SegmentSize < 1024*1024 { // 最小 1MB
		return ErrInvalidWALConfig
	}
	return nil
}

// WALEntryIterator WAL 日志迭代器
type WALEntryIterator interface {
	// Next 返回下一条日志
	Next() (*WALEntry, error)
	// Close 关闭迭代器
	Close() error
}
