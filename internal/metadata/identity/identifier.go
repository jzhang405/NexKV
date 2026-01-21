// Package identity 提供标识符生成功能
//
// 核心功能:
//   - NodeID 生成（FNV-1a 64-bit 哈希）
//   - MsgSeq 生成器（原子递增）
package identity

import (
	"hash/fnv"
	"net"
	"strconv"
	"sync/atomic"
	"time"
)

// GenerateNodeIDFromPorts 根据主机和端口生成节点 ID
//
// 参数:
//   - host: 主机地址（IP 或域名）
//   - tcpPort: TCP 端口（0 表示未启用）
//   - udpPort: UDP 端口（0 表示未启用）
//
// 返回:
//   - uint64: 节点 ID（FNV-1a 64-bit 哈希）
func GenerateNodeIDFromPorts(host string, tcpPort, udpPort int) uint64 {
	listenAddr := net.JoinHostPort(host, strconv.Itoa(tcpPort)) + ":" + strconv.Itoa(udpPort)
	h := fnv.New64a()
	h.Write([]byte(listenAddr))
	return h.Sum64()
}

// MsgSeqGenerator 消息序列号生成器（原子递增）
type MsgSeqGenerator struct {
	counter uint64
}

// NewMsgSeqGenerator 创建消息序列号生成器
//
// 使用当前时间戳（微秒级）作为初始值，确保单调递增
//
// 返回:
//   - *MsgSeqGenerator: 序列号生成器实例
func NewMsgSeqGenerator() *MsgSeqGenerator {
	return &MsgSeqGenerator{
		counter: uint64(time.Now().UnixMicro()),
	}
}

// Next 生成下一个序列号（原子递增）
func (g *MsgSeqGenerator) Next() uint64 {
	return atomic.AddUint64(&g.counter, 1)
}

// Current 获取当前序列号（不递增）
func (g *MsgSeqGenerator) Current() uint64 {
	return atomic.LoadUint64(&g.counter)
}
