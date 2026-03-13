// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"encoding/binary"
	"io"
)

// binaryWrite 辅助函数：写入字节数组到 Writer
func binaryWrite(w io.Writer, data []byte) error {
	_, err := w.Write(data)
	return err
}

// uint64ToBytes 将 uint64 转换为字节数组（小端序）
func uint64ToBytes(v uint64) []byte {
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, v)
	return buf
}

// uint32ToBytes 将 uint32 转换为字节数组（小端序）
func uint32ToBytes(v uint32) []byte {
	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, v)
	return buf
}

// uint16ToBytes 将 uint16 转换为字节数组（小端序）
func uint16ToBytes(v uint16) []byte {
	buf := make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, v)
	return buf
}

// readBytes 从 Reader 读取指定字节数
func readBytes(r io.Reader, n int) ([]byte, error) {
	buf := make([]byte, n)
	_, err := io.ReadFull(r, buf)
	if err != nil {
		return nil, err
	}
	return buf, nil
}

// bytesToUint64 将字节数组转换为 uint64（小端序）
func bytesToUint64(buf []byte) uint64 {
	return binary.LittleEndian.Uint64(buf)
}

// bytesToUint32 将字节数组转换为 uint32（小端序）
func bytesToUint32(buf []byte) uint32 {
	return binary.LittleEndian.Uint32(buf)
}

// bytesToUint16 将字节数组转换为 uint16（小端序）
func bytesToUint16(buf []byte) uint16 {
	return binary.LittleEndian.Uint16(buf)
}
