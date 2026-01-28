// Package identity 提供标识符生成功能
//
// 核心功能:
//   - NodeID 生成（FNV-1a 64-bit 哈希）
//   - MsgSeq 生成器（原子递增）
package identity

import (
	"fmt"
	"hash/fnv"
	"net"
	"os"
	"strconv"
	"sync/atomic"
	"time"
)

// GenerateNodeIDFromPorts 根据主机和端口生成节点 ID
//
// 参数:
//   - tcpPort: TCP 端口（0 表示未启用，范围 1-65535）
//   - udpPort: UDP 端口（0 表示未启用，范围 1-65535）
//
// 返回:
//   - uint64: 节点 ID（FNV-1a 64-bit 哈希）
//   - error: 端口验证失败时返回错误
//
// 错误条件:
//   - 两个端口都为 0
//   - 端口超出有效范围 (1-65535)
func GenerateNodeIDFromPorts(tcpPort, udpPort int) (uint64, error) {
	// 验证端口参数
	if tcpPort == 0 && udpPort == 0 {
		return 0, fmt.Errorf("至少需要启用一个端口（TCP 或 UDP）")
	}

	// 验证端口范围（有效端口：1-65535）
	if tcpPort < 0 || tcpPort > 65535 {
		return 0, fmt.Errorf("TCP 端口无效: %d（有效范围: 1-65535）", tcpPort)
	}
	if udpPort < 0 || udpPort > 65535 {
		return 0, fmt.Errorf("UDP 端口无效: %d（有效范围: 1-65535）", udpPort)
	}

	host, err := os.Hostname()
	if err != nil {
		return 0, err
	}
	listenAddr := net.JoinHostPort(host, strconv.Itoa(tcpPort)) + ":" + strconv.Itoa(udpPort)
	h := fnv.New64a()
	h.Write([]byte(listenAddr))
	return h.Sum64(), nil
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
