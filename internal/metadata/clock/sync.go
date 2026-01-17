// Package clock 提供 Gossip 时钟同步实现
package clock

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
)

// gossipClockSync Gossip 时钟同步实现
type gossipClockSync struct {
	hlc    *HLC
	config *ClockSyncConfig

	// 节点列表
	peers []string

	// 停止信号
	stopCh chan struct{}
	wg     sync.WaitGroup

	// 同步状态统计
	syncCount   int64
	syncSuccess int64
	syncFailed  int64
	maxDrift    int64
}

// NewClockSync 创建时钟同步服务
func NewClockSync(hlc *HLC, config *ClockSyncConfig, peers []string) ClockSync {
	if config == nil {
		config = DefaultClockSyncConfig()
	}

	return &gossipClockSync{
		hlc:    hlc,
		config: config,
		peers:  peers,
		stopCh: make(chan struct{}),
	}
}

// Start 启动时钟同步服务
func (g *gossipClockSync) Start() error {
	logging.WithField("sync_interval", g.config.SyncInterval).
		WithField("peers", len(g.peers)).
		Info("启动时钟同步服务")

	// 启动同步循环
	g.wg.Add(1)
	go g.syncLoop()

	return nil
}

// Stop 停止时钟同步服务
func (g *gossipClockSync) Stop() error {
	logging.Info("停止时钟同步服务")

	close(g.stopCh)
	g.wg.Wait()

	// 打印统计信息
	logging.WithFields(map[string]any{
		"sync_count":   g.syncCount,
		"sync_success": g.syncSuccess,
		"sync_failed":  g.syncFailed,
		"max_drift_ms": g.maxDrift,
	}).Info("时钟同步统计")

	return nil
}

// SyncWithPeer 与指定节点同步时钟
func (g *gossipClockSync) SyncWithPeer(ctx context.Context, addr string) error {
	// TODO: 实现 RPC 调用获取远程节点的 HLC
	// 当前为简化实现，使用本地 HLC 更新

	// 模拟远程 HLC（实际应该通过网络获取）
	remoteHLC := g.hlc.Now()

	// 计算时间漂移
	drift := absInt64(g.hlc.PhysicalTime() - remoteHLC.PhysicalTime())

	// 更新本地 HLC
	updatedHLC := g.hlc.Update(time.Now().UnixMilli(), remoteHLC)

	// 更新统计
	g.syncCount++
	if drift <= g.config.MaxDrift {
		g.syncSuccess++
	} else {
		g.syncFailed++
		logging.WithField("peer", addr).
			WithField("drift_ms", drift).
			Warn("时钟漂移超过阈值")
	}

	if drift > g.maxDrift {
		g.maxDrift = drift
	}

	logging.WithFields(map[string]any{
		"peer":       addr,
		"local_pt":   g.hlc.PhysicalTime(),
		"remote_pt":  remoteHLC.PhysicalTime(),
		"drift_ms":   drift,
		"updated_pt": updatedHLC.PhysicalTime(),
		"updated_c":  updatedHLC.LogicalCounter(),
	}).Debug("时钟同步完成")

	// 检测时间回拨
	if updatedHLC.PhysicalTime() < g.hlc.PhysicalTime() {
		logging.WithFields(map[string]any{
			"peer":       addr,
			"current_pt": g.hlc.PhysicalTime(),
			"updated_pt": updatedHLC.PhysicalTime(),
		}).Warn("检测到时间回拨，已自动修正")
	}

	return nil
}

// syncLoop 时钟同步循环
func (g *gossipClockSync) syncLoop() {
	defer g.wg.Done()

	ticker := time.NewTicker(g.config.SyncInterval)
	defer ticker.Stop()

	// 首次启动时立即同步一次
	g.syncWithRandomPeers()

	for {
		select {
		case <-ticker.C:
			g.syncWithRandomPeers()
		case <-g.stopCh:
			return
		}
	}
}

// syncWithRandomPeers 随机选择节点进行同步
func (g *gossipClockSync) syncWithRandomPeers() {
	if len(g.peers) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), g.config.Timeout)
	defer cancel()

	// 随机选择 2 个节点进行同步（Gossip 协议）
	// TODO: 实现更智能的节点选择策略
	selectedPeers := g.selectRandomPeers(2)

	for _, peer := range selectedPeers {
		if err := g.SyncWithPeer(ctx, peer); err != nil {
			logging.WithField("peer", peer).
				WithError(err).
				Error("时钟同步失败")
		}
	}
}

// selectRandomPeers 随机选择节点
func (g *gossipClockSync) selectRandomPeers(count int) []string {
	if len(g.peers) <= count {
		return g.peers
	}

	// 简化实现：返回前 count 个节点
	// TODO: 实现真正的随机选择
	return g.peers[:count]
}

// GetStats 获取同步统计信息
func (g *gossipClockSync) GetStats() map[string]any {
	return map[string]any{
		"sync_count":   g.syncCount,
		"sync_success": g.syncSuccess,
		"sync_failed":  g.syncFailed,
		"max_drift_ms": g.maxDrift,
		"success_rate": float64(g.syncSuccess) / float64(g.syncCount) * 100,
	}
}

// absInt64 返回 int64 的绝对值
func absInt64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// VerifyClockIntegrity 验证时钟完整性（用于测试和监控）
func VerifyClockIntegrity(hlc *HLC) error {
	if hlc == nil {
		return fmt.Errorf("HLC is nil")
	}

	// 检查物理时间是否合理
	pt := hlc.PhysicalTime()
	now := time.Now().UnixMilli()

	// 允许前后 1 天的误差（处理时钟漂移）
	if pt < now-24*3600*1000 || pt > now+24*3600*1000 {
		return fmt.Errorf("物理时间超出合理范围: pt=%d, now=%d", pt, now)
	}

	// 检查逻辑计数
	c := hlc.LogicalCounter()
	if c > MaxLogicalCounter {
		return fmt.Errorf("逻辑计数溢出: c=%d", c)
	}

	return nil
}
