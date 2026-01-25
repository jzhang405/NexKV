// Package transport 测试工具函数
package transport

import (
	"sync/atomic"
)

// newDefaultMsgSeqGenerator 返回默认的消息序列号生成器（用于测试）
func newDefaultMsgSeqGenerator() func() uint64 {
	var seq uint64
	return func() uint64 {
		return atomic.AddUint64(&seq, 1)
	}
}
