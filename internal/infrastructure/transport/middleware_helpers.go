// Package transport 实现传输层基础设施
package transport

import (
	"sync"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// cleanupSyncMap 清理 sync.Map 中不在有效节点列表中的资源
// 用于统一处理限流器和熔断器的资源清理逻辑
func cleanupSyncMap(m *sync.Map, validPeers []model.PeerID) {
	validSet := make(map[model.PeerID]bool, len(validPeers))
	for _, peer := range validPeers {
		validSet[peer] = true
	}

	m.Range(func(key, value any) bool {
		peer := key.(model.PeerID)
		if !validSet[peer] {
			m.Delete(peer)
		}
		return true
	})
}

// countSyncMap 统计 sync.Map 中的元素数量
// 用于统一处理限流器和熔断器的资源计数逻辑
func countSyncMap(m *sync.Map) int {
	count := 0
	m.Range(func(key, value any) bool {
		count++
		return true
	})
	return count
}

// copyExts 复制消息扩展信息（跳过压缩标记）
// 用于压缩中间件中统一处理扩展信息复制
func copyExts(src, dst model.Message) {
	for k, v := range src.Exts().All() {
		if k != "compression" {
			dst.Exts().Set(k, v)
		}
	}
}
