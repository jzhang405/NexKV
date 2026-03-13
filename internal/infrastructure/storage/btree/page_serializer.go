// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sync"
)

// PageSerializer 页面序列化辅助工具
// 用于提取 LeafPage 和 InternalPage 的公共序列化逻辑
type PageSerializer struct {
	buf bytes.Buffer
}

// ✅ 对象池：复用 PageSerializer 对象
var pageSerializerPool = sync.Pool{
	New: func() any {
		return &PageSerializer{}
	},
}

// AcquirePageSerializer 从对象池获取序列化器
func AcquirePageSerializer() *PageSerializer {
	ps := pageSerializerPool.Get().(*PageSerializer)
	ps.buf.Reset()
	return ps
}

// ReleasePageSerializer 将序列化器放回对象池
func ReleasePageSerializer(ps *PageSerializer) {
	pageSerializerPool.Put(ps)
}

// NewPageSerializer 创建新的页面序列化器
func NewPageSerializer() *PageSerializer {
	return &PageSerializer{}
}

// WriteHeader 写入页面公共头部（pageID + version）
// ✅ 优化：使用栈上缓冲区，避免堆分配
func (ps *PageSerializer) WriteHeader(pageID uint64, version uint64) error {
	// 使用栈上缓冲区（16 字节）
	var buf [16]byte
	binary.LittleEndian.PutUint64(buf[0:8], pageID)
	binary.LittleEndian.PutUint64(buf[8:16], version)

	_, err := ps.buf.Write(buf[:])
	return err
}

// WriteKeyCount 写入键数量
// ✅ 优化：使用栈上缓冲区
func (ps *PageSerializer) WriteKeyCount(numKeys int) error {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], uint32(numKeys))
	_, err := ps.buf.Write(buf[:])
	return err
}

// WriteKey 写入单个键（带长度前缀）
// ✅ 优化：使用栈上缓冲区
func (ps *PageSerializer) WriteKey(key []byte) error {
	// 使用栈上缓冲区（2 字节长度 + 键数据）
	keyLen := uint16(len(key))
	var buf [2]byte
	binary.LittleEndian.PutUint16(buf[:], keyLen)

	if _, err := ps.buf.Write(buf[:]); err != nil {
		return err
	}

	if _, err := ps.buf.Write(key); err != nil {
		return err
	}

	return nil
}

// WriteKeyValue 写入键值对（带长度前缀）
// ✅ 优化：使用栈上缓冲区
func (ps *PageSerializer) WriteKeyValue(key, value []byte) error {
	// 写入键长度
	keyLen := uint16(len(key))
	var keyLenBuf [2]byte
	binary.LittleEndian.PutUint16(keyLenBuf[:], keyLen)
	if _, err := ps.buf.Write(keyLenBuf[:]); err != nil {
		return err
	}

	// 写入键数据
	if _, err := ps.buf.Write(key); err != nil {
		return err
	}

	// 写入值长度
	valueLen := uint16(len(value))
	var valueLenBuf [2]byte
	binary.LittleEndian.PutUint16(valueLenBuf[:], valueLen)
	if _, err := ps.buf.Write(valueLenBuf[:]); err != nil {
		return err
	}

	// 写入值数据
	if _, err := ps.buf.Write(value); err != nil {
		return err
	}

	return nil
}

// WriteChildCount 写入子节点数量
// ✅ 优化：使用栈上缓冲区
func (ps *PageSerializer) WriteChildCount(numChildren int) error {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], uint32(numChildren))
	_, err := ps.buf.Write(buf[:])
	return err
}

// WriteChildID 写入子节点 ID
// ✅ 优化：使用栈上缓冲区
func (ps *PageSerializer) WriteChildID(childID uint64) error {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], childID)
	_, err := ps.buf.Write(buf[:])
	return err
}

// Finalize 完成序列化，生成固定大小的页面数据
func (ps *PageSerializer) Finalize() ([]byte, error) {
	// 1. 获取实际内容长度
	contentData := ps.buf.Bytes()
	contentLength := len(contentData)

	// 2. 创建最终的序列化结果（4 字节长度 + 内容 + 填充）
	result := make([]byte, PageSize)

	// 写入实际长度（前 4 字节）
	binary.BigEndian.PutUint32(result[0:4], uint32(contentLength))

	// 复制内容
	copy(result[4:4+contentLength], contentData)

	// 剩余部分已经自动填充为 0x00（Go 的 make 默认初始化）

	return result, nil
}

// Reset 重置序列化器（用于复用）
func (ps *PageSerializer) Reset() {
	ps.buf.Reset()
}

// ===== PageDeserializer 页面反序列化辅助工具 =====

// PageDeserializer 页面反序列化辅助工具
type PageDeserializer struct {
	data []byte
	pos  int
}

// NewPageDeserializer 创建新的页面反序列化器
func NewPageDeserializer(data []byte) (*PageDeserializer, error) {
	if len(data) != PageSize {
		return nil, fmt.Errorf("invalid data size: expected %d bytes, got %d", PageSize, len(data))
	}

	// 读取实际内容长度（前 4 字节）
	contentLength := binary.BigEndian.Uint32(data[0:4])

	return &PageDeserializer{
		data: data[4 : 4+contentLength], // 跳过长度字段，只保留实际内容
		pos:  0,
	}, nil
}

// readBytes 读取指定字节数
func (pd *PageDeserializer) readBytes(n int) ([]byte, error) {
	if pd.pos+n > len(pd.data) {
		return nil, fmt.Errorf("unexpected EOF: expected %d bytes, got %d", n, len(pd.data)-pd.pos)
	}
	result := pd.data[pd.pos : pd.pos+n]
	pd.pos += n
	return result, nil
}

// ReadHeader 读取页面公共头部（pageID + version）
func (pd *PageDeserializer) ReadHeader() (pageID uint64, version uint64, err error) {
	// 读取 pageID (8 bytes)
	pageIDBytes, err := pd.readBytes(8)
	if err != nil {
		return 0, 0, err
	}
	pageID = bytesToUint64(pageIDBytes)

	// 读取 version (8 bytes)
	versionBytes, err := pd.readBytes(8)
	if err != nil {
		return 0, 0, err
	}
	version = bytesToUint64(versionBytes)

	return pageID, version, nil
}

// ReadKeyCount 读取键数量
func (pd *PageDeserializer) ReadKeyCount() (int, error) {
	countBytes, err := pd.readBytes(4)
	if err != nil {
		return 0, err
	}
	return int(binary.BigEndian.Uint32(countBytes)), nil
}

// ReadKey 读取单个键（带长度前缀）
func (pd *PageDeserializer) ReadKey() ([]byte, error) {
	// 读取键长度 (2 bytes)
	lenBytes, err := pd.readBytes(2)
	if err != nil {
		return nil, err
	}
	keyLen := binary.BigEndian.Uint16(lenBytes)

	// 读取键数据
	key, err := pd.readBytes(int(keyLen))
	if err != nil {
		return nil, err
	}

	return key, nil
}

// ReadKeyValue 读取键值对（带长度前缀）
func (pd *PageDeserializer) ReadKeyValue() (key, value []byte, err error) {
	// 读取键
	key, err = pd.ReadKey()
	if err != nil {
		return nil, nil, err
	}

	// 读取值长度 (2 bytes)
	lenBytes, err := pd.readBytes(2)
	if err != nil {
		return nil, nil, err
	}
	valueLen := binary.BigEndian.Uint16(lenBytes)

	// 读取值数据
	value, err = pd.readBytes(int(valueLen))
	if err != nil {
		return nil, nil, err
	}

	return key, value, nil
}

// ReadChildCount 读取子节点数量
func (pd *PageDeserializer) ReadChildCount() (int, error) {
	countBytes, err := pd.readBytes(4)
	if err != nil {
		return 0, err
	}
	return int(binary.BigEndian.Uint32(countBytes)), nil
}

// ReadChildID 读取子节点 ID
func (pd *PageDeserializer) ReadChildID() (uint64, error) {
	idBytes, err := pd.readBytes(8)
	if err != nil {
		return 0, err
	}
	return bytesToUint64(idBytes), nil
}
