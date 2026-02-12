// Package cluster TreeCoordinator 元数据集成
//
// 集成 MetadataKV 到 TreeCoordinator，提供基于 KV 映射的元数据管理
package cluster

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jzhang405/NexKV/internal/clock"
	"github.com/jzhang405/NexKV/internal/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/api"
	"github.com/jzhang405/NexKV/internal/metadata/kvstore"
	"github.com/jzhang405/NexKV/internal/metadata/types"
	metadatarpc "github.com/jzhang405/NexKV/internal/rpc"
	store "github.com/jzhang405/NexKV/internal/wal"
	"github.com/vmihailenco/msgpack/v5"
)

// ========================================
// TreeCoordinator 元数据集成扩展
// ========================================

// setupMetadataStorage 设置并初始化元数据存储
//
// 创建 MVStore 并初始化 MetadataKV，在 TreeCoordinator 启动时调用
//
// 路径规则：
//   - 正式环境：{NEXKV_BASE_DIR}/{host_id}/metadata
//   - 降级路径：/var/tmp/nexkv/（当配置为空时，自动创建）
//
//nolint:unused // 阶段 3 集成时使用
func (tc *TreeCoordinator) setupMetadataStorage(dataDir string) error {
	// 处理空路径，使用降级路径
	if dataDir == "" {
		dataDir = "/var/tmp/nexkv"
		logging.Warn("数据目录未配置，使用降级路径: /var/tmp/nexkv/")
	}

	// 确保目录存在
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return fmt.Errorf("failed to create data directory %s: %w", dataDir, err)
	}

	// 创建 MVStore
	mvStore, err := store.NewMemoryMVStore(&store.MVStoreOptions{
		DataDir:       dataDir,
		WALDir:        dataDir,
		MemTableSize:  16 * 1024 * 1024, // 16MB 内存表
		FlushInterval: 300,              // 5 分钟（秒）
		EnableWAL:     true,
		WALSsyncSize:  4096,
	})
	if err != nil {
		return fmt.Errorf("failed to create MVStore: %w", err)
	}

	// 保存 MVStore 引用
	tc.mvStore = mvStore

	// 初始化元数据 KV
	if err := tc.initMetadataKV(mvStore); err != nil {
		// 初始化失败时关闭 MVStore
		_ = mvStore.Close()
		tc.mvStore = nil
		return err
	}

	logging.WithFields(map[string]any{
		"node_id":  tc.localNode.NodeID,
		"data_dir": dataDir,
	}).Info("元数据存储已初始化")

	return nil
}

// initMetadataKV 初始化元数据 KV 存储
//
// 在 TreeCoordinator 启动时调用，初始化 MetadataKV 和 MetadataAPI
//
//nolint:unused // 阶段 3 集成时使用
func (tc *TreeCoordinator) initMetadataKV(mvStore store.MVStore) error {
	tc.metadataMu.Lock()
	defer tc.metadataMu.Unlock()

	hlc := clock.NewHLC()
	codec := kvstore.NewMetadataCodec(kvstore.CompressionNone)

	metadataKV, err := kvstore.NewMetadataKV(mvStore, &kvstore.MetadataKVOptions{
		HLC:   hlc,
		Codec: codec,
		// 设置 Gossip 回调
		GossipCallback: tc.gossipMetadataChange,
		// 设置 Quorum 回调
		QuorumCallback: tc.quorumMetadataChange,
	})

	if err != nil {
		return fmt.Errorf("failed to create metadata KV store: %w", err)
	}

	metadataAPI := api.NewMetadataAPI(metadataKV)

	// 存储到 TreeCoordinator
	tc.metadataKV = metadataKV
	tc.metadataAPI = metadataAPI

	logging.WithField("node_id", tc.localNode.NodeID).Info("元数据 KV 存储已初始化")

	return nil
}

// closeMetadataKV 关闭元数据 KV 存储
//
//nolint:unused // 阶段 3 集成时使用
func (tc *TreeCoordinator) closeMetadataKV() error {
	tc.metadataMu.Lock()
	defer tc.metadataMu.Unlock()

	// 关闭元数据 KV
	var kvErr error
	if tc.metadataKV != nil {
		kvErr = tc.metadataKV.Close()
	}

	// 关闭 MVStore
	var storeErr error
	if tc.mvStore != nil {
		storeErr = tc.mvStore.Close()
		tc.mvStore = nil
	}

	// 返回第一个非空错误
	if kvErr != nil {
		return kvErr
	}
	return storeErr
}

// registerMetadataRPCHandlers 注册元数据 RPC 处理器
//
// 在 TreeCoordinator 启动时调用，注册元数据同步相关的 RPC 方法
//
//nolint:unused // 阶段 3 集成时使用
func (tc *TreeCoordinator) registerMetadataRPCHandlers() error {
	if tc.rpcServer == nil {
		return fmt.Errorf("RPC server not initialized")
	}

	// 注册元数据同步处理器
	if err := tc.rpcServer.RegisterHandlerFunc("MetadataSync", tc.HandleMetadataSyncRequest); err != nil {
		return fmt.Errorf("failed to register MetadataSync handler: %w", err)
	}

	if err := tc.rpcServer.RegisterHandlerFunc("MetadataChangeNotify", tc.HandleMetadataChangeNotification); err != nil {
		return fmt.Errorf("failed to register MetadataChangeNotify handler: %w", err)
	}

	logging.WithField("node_id", tc.localNode.NodeID).Info("元数据 RPC 处理器已注册")

	return nil
}

// ========================================
// 节点元数据操作
// ========================================

// GetNodeInfo 获取节点信息（使用 MetadataKV）
func (tc *TreeCoordinator) GetNodeInfo(ctx context.Context, nodeID string) (*types.NodeInfo, error) {
	// P0 修复：移除双重状态管理，统一使用 MetadataKV
	// 如果 MetadataKV 未初始化，返回明确错误而非回退到内存状态
	tc.metadataMu.RLock()
	defer tc.metadataMu.RUnlock()

	if tc.metadataAPI == nil {
		return nil, fmt.Errorf("metadata API not initialized, please call setupMetadataStorage first")
	}

	return tc.metadataAPI.GetNodeInfo(ctx, nodeID)
}

// SetNodeInfo 设置节点信息（使用 MetadataKV）
func (tc *TreeCoordinator) SetNodeInfo(ctx context.Context, info *types.NodeInfo) error {
	// P0 修复：移除双重状态管理，统一使用 MetadataKV
	tc.metadataMu.RLock()
	defer tc.metadataMu.RUnlock()

	if tc.metadataAPI == nil {
		return fmt.Errorf("metadata API not initialized, please call setupMetadataStorage first")
	}

	return tc.metadataAPI.SetNodeInfo(ctx, info)
}

// UpdateNodeHeartbeat 更新节点心跳（使用 MetadataKV）
func (tc *TreeCoordinator) UpdateNodeHeartbeat(ctx context.Context, nodeID string, heartbeatTime time.Time) error {
	// P0 修复：移除双重状态管理，统一使用 MetadataKV
	tc.metadataMu.RLock()
	defer tc.metadataMu.RUnlock()

	if tc.metadataAPI == nil {
		return fmt.Errorf("metadata API not initialized, please call setupMetadataStorage first")
	}

	// 获取现有节点信息
	info, err := tc.metadataAPI.GetNodeInfo(ctx, nodeID)
	if err != nil {
		return fmt.Errorf("failed to get node info: %w", err)
	}

	// 更新心跳时间
	info.LastHeartbeat = heartbeatTime

	// 写回
	return tc.metadataAPI.SetNodeInfo(ctx, info)
}

// ========================================
// 角色元数据操作
// ========================================

// GetRoleInfo 获取角色信息（使用 MetadataKV）
func (tc *TreeCoordinator) GetRoleInfo(ctx context.Context, roleID string) (*types.RoleInfo, error) {
	tc.metadataMu.RLock()
	defer tc.metadataMu.RUnlock()

	if tc.metadataAPI == nil {
		return nil, fmt.Errorf("metadata API not initialized")
	}

	return tc.metadataAPI.GetRoleInfo(ctx, roleID)
}

// SetRoleInfo 设置角色信息（使用 MetadataKV）
func (tc *TreeCoordinator) SetRoleInfo(ctx context.Context, info *types.RoleInfo) error {
	tc.metadataMu.RLock()
	defer tc.metadataMu.RUnlock()

	if tc.metadataAPI == nil {
		return fmt.Errorf("metadata API not initialized")
	}

	return tc.metadataAPI.SetRoleInfo(ctx, info)
}

// AddNodeToRole 将节点添加到角色（Active 或 Standby）
func (tc *TreeCoordinator) AddNodeToRole(ctx context.Context, roleID, nodeID string, isStandby bool) error {
	tc.metadataMu.RLock()
	defer tc.metadataMu.RUnlock()

	if tc.metadataAPI == nil {
		return fmt.Errorf("metadata API not initialized")
	}

	// 获取现有角色信息
	info, err := tc.metadataAPI.GetRoleInfo(ctx, roleID)
	if err != nil {
		// 角色不存在，创建新角色
		info = &types.RoleInfo{
			RoleID:       roleID,
			ActiveNodes:  []string{},
			StandbyNodes: []string{},
			Version:      uint64(time.Now().UnixNano()),
		}
	}

	// 添加节点到对应列表
	if isStandby {
		// 检查是否已存在
		for _, n := range info.StandbyNodes {
			if n == nodeID {
				return nil // 已存在，无需添加
			}
		}
		info.StandbyNodes = append(info.StandbyNodes, nodeID)
	} else {
		// 检查是否已存在
		for _, n := range info.ActiveNodes {
			if n == nodeID {
				return nil // 已存在，无需添加
			}
		}
		info.ActiveNodes = append(info.ActiveNodes, nodeID)
	}

	// 更新版本号
	info.Version = uint64(time.Now().UnixNano())

	return tc.metadataAPI.SetRoleInfo(ctx, info)
}

// ========================================
// 拓扑元数据操作
// ========================================

// GetTopologyInfo 获取拓扑信息（使用 MetadataKV）
func (tc *TreeCoordinator) GetTopologyInfo(ctx context.Context, nodeID string) (*types.TopologyInfo, error) {
	tc.metadataMu.RLock()
	defer tc.metadataMu.RUnlock()

	if tc.metadataAPI == nil {
		// 元数据 API 未初始化，从内存中的节点获取
		tc.nodesMu.RLock()
		node, ok := tc.allNodes[nodeID]
		tc.nodesMu.RUnlock()

		if !ok {
			return nil, fmt.Errorf("node not found: %s", nodeID)
		}

		// 转换为 TopologyInfo
		return &types.TopologyInfo{
			NodeID:      node.NodeID,
			ParentID:    node.ParentID,
			ChildrenIDs: node.ChildrenIDs,
			Level:       node.Level,
			Version:     uint64(time.Now().UnixNano()),
		}, nil
	}

	return tc.metadataAPI.GetTopologyInfo(ctx, nodeID)
}

// SetTopologyInfo 设置拓扑信息（使用 MetadataKV）
func (tc *TreeCoordinator) SetTopologyInfo(ctx context.Context, info *types.TopologyInfo) error {
	tc.metadataMu.RLock()
	defer tc.metadataMu.RUnlock()

	if tc.metadataAPI == nil {
		// 元数据 API 未初始化，更新内存中的节点
		tc.nodesMu.Lock()
		defer tc.nodesMu.Unlock()

		node, ok := tc.allNodes[info.NodeID]
		if !ok {
			return fmt.Errorf("node not found: %s", info.NodeID)
		}

		node.ParentID = info.ParentID
		node.ChildrenIDs = info.ChildrenIDs
		node.Level = info.Level

		return nil
	}

	return tc.metadataAPI.SetTopologyInfo(ctx, info)
}

// ========================================
// Gossip 同步集成
// ========================================

// gossipMetadataChange Gossip 元数据变更
//
// 当元数据发生变更时，通过 Gossip 协议异步扩散到其他节点
//
//nolint:unused // 阶段 3 集成时使用
func (tc *TreeCoordinator) gossipMetadataChange(ns, key string, version uint64) {
	// 根据命名空间确定同步策略
	level := kvstore.GetConsistencyLevel(ns)

	if level == kvstore.ConsistencyEventual {
		// 最终一致：通过 Gossip 异步扩散
		tc.gossipNodeMetadata(ns, key, version)
	}
	// 强一致：通过 Quorum 确认（在 quorumMetadataChange 中处理）
}

// gossipNodeMetadata Gossip 节点元数据变更
//
// 构造并发送元数据变更通知到随机选择的节点
//
// P0 修复：添加信号量控制，防止无限制创建 goroutine
//
//nolint:unused // 阶段 3 集成时使用
func (tc *TreeCoordinator) gossipNodeMetadata(ns, key string, version uint64) {
	// 检查 RPC 客户端是否可用
	if tc.rpcClient == nil {
		logging.WithFields(map[string]any{
			"namespace": ns,
			"key":       key,
			"version":   version,
		}).Debug("⚠️ RPC 客户端未初始化，跳过元数据 Gossip")
		return
	}

	// 构造元数据变更通知
	notification := metadatarpc.NewMetadataChangeNotification(ns, key, "put", version)

	// 序列化通知
	reqBody, err := msgpack.Marshal(notification)
	if err != nil {
		logging.WithFields(map[string]any{
			"namespace": ns,
			"key":       key,
			"error":     err,
		}).Error("序列化元数据变更通知失败")
		return
	}

	// 获取所有就绪节点（不包括本地节点）
	targetNodes := tc.getRandomReadyNodes(3) // 随机选择 3 个节点

	// P0 修复：使用信号量限制并发 goroutine 数量
	for _, targetNode := range targetNodes {
		// 获取信号量（阻塞直到有可用 slot）
		tc.gossipSemaphore <- struct{}{}

		// 异步发送通知
		go func(node *Node) {
			defer func() { <-tc.gossipSemaphore }() // 释放信号量
			tc.sendMetadataChangeNotification(node, reqBody)
		}(targetNode)
	}

	logging.WithFields(map[string]any{
		"namespace":    ns,
		"key":          key,
		"version":      version,
		"target_count": len(targetNodes),
	}).Debug("元数据变更已通过 Gossip 扩散")
}

// sendMetadataChangeNotification 发送元数据变更通知到指定节点
//
//nolint:unused // 阶段 3 集成时使用
func (tc *TreeCoordinator) sendMetadataChangeNotification(targetNode *Node, reqBody []byte) {
	// 检查 RPC 客户端是否可用
	if tc.rpcClient == nil {
		return
	}

	// 将目标节点地址转换为 peer.ID
	targetPeerID := tc.addrToPeerID(targetNode.Addr.TCPAddr())

	// 发送 RPC 请求
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := tc.rpcClient.Call(ctx, targetPeerID, "MetadataChangeNotify", reqBody)
	if err != nil {
		logging.WithFields(map[string]any{
			"target_node": targetNode.NodeID,
			"error":       err,
		}).Debug("发送元数据变更通知失败")
	}
}

// quorumMetadataChange Quorum 元数据变更确认
//
// 当强一致元数据发生变更时，等待 Quorum 确认
//
//nolint:unused // 阶段 3 集成时使用
func (tc *TreeCoordinator) quorumMetadataChange(ns, key string, version uint64) {
	// 获取集群配置
	tc.nodesMu.RLock()
	totalNodes := len(tc.allNodes)
	tc.nodesMu.RUnlock()

	quorumThreshold := totalNodes/2 + 1 // 简单多数派

	logging.WithFields(map[string]any{
		"namespace":        ns,
		"key":              key,
		"version":          version,
		"quorum_threshold": quorumThreshold,
		"total_nodes":      totalNodes,
	}).Debug("等待 Quorum 确认")

	// 构造元数据变更通知
	notification := metadatarpc.NewMetadataChangeNotification(ns, key, "put", version)
	reqBody, err := msgpack.Marshal(notification)
	if err != nil {
		logging.WithField("error", err).Error("序列化元数据变更通知失败")
		return
	}

	// 获取所有节点
	tc.nodesMu.RLock()
	allNodes := make([]*Node, 0, len(tc.allNodes))
	for _, node := range tc.allNodes {
		// 跳过本地节点
		if node.NodeID != tc.localNode.NodeID && node.Status == NodeStatusReady {
			allNodes = append(allNodes, node)
		}
	}
	tc.nodesMu.RUnlock()

	if len(allNodes) == 0 {
		logging.WithField("namespace", ns).Debug("无其他节点需要 Quorum 确认")
		return
	}

	// 使用 channel 收集确认响应
	ackCh := make(chan struct{}, len(allNodes))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 并发发送确认请求
	for _, node := range allNodes {
		go func(n *Node) {
			defer func() { ackCh <- struct{}{} }()

			if tc.rpcClient == nil {
				return
			}

			targetPeerID := tc.addrToPeerID(n.Addr.TCPAddr())
			_, err := tc.rpcClient.Call(ctx, targetPeerID, "MetadataChangeNotify", reqBody)
			if err != nil {
				logging.WithFields(map[string]any{
					"target_node": n.NodeID,
					"error":       err,
				}).Debug("Quorum 确认请求失败")
				return
			}

			logging.WithFields(map[string]any{
				"target_node": n.NodeID,
				"namespace":   ns,
				"key":         key,
			}).Debug("Quorum 确认成功")
		}(node)
	}

	// 等待多数派确认
	acks := 0
	for i := 0; i < len(allNodes); i++ {
		select {
		case <-ackCh:
			acks++
			if acks >= quorumThreshold {
				logging.WithFields(map[string]any{
					"namespace": ns,
					"key":       key,
					"version":   version,
					"acks":      acks,
					"threshold": quorumThreshold,
				}).Info("Quorum 确认达成")
				return
			}
		case <-ctx.Done():
			logging.WithFields(map[string]any{
				"namespace": ns,
				"key":       key,
				"acks":      acks,
				"threshold": quorumThreshold,
				"error":     "timeout",
			}).Warn("Quorum 确认超时")
			return
		}
	}
}

// ========================================
// RPC 处理器（元数据同步）
// ========================================

// HandleMetadataSyncRequest 处理元数据同步请求
//
//nolint:unused // 阶段 3 集成时使用
func (tc *TreeCoordinator) HandleMetadataSyncRequest(ctx context.Context, req []byte) ([]byte, error) {
	// 解析请求
	var syncReq metadatarpc.MetadataSyncRequest
	if err := msgpack.Unmarshal(req, &syncReq); err != nil {
		return nil, metadatarpc.NewRPCError(metadatarpc.ErrCodeBadRequest, "invalid metadata sync request")
	}

	logging.WithFields(map[string]any{
		"namespace": syncReq.Namespace,
		"key_count": len(syncReq.Keys),
		"version":   syncReq.Version,
	}).Debug("收到元数据同步请求")

	// 构造响应
	syncResp := &metadatarpc.MetadataSyncResponse{
		Namespace: syncReq.Namespace,
		Metadata:  make(map[string][]byte),
		Version:   syncReq.Version,
		Timestamp: time.Now().UnixNano(),
	}

	tc.metadataMu.RLock()
	defer tc.metadataMu.RUnlock()

	if tc.metadataKV == nil {
		// 元数据 KV 未初始化，返回空响应
		return msgpack.Marshal(syncResp)
	}

	// 批量获取请求的元数据（原始字节）
	metadataMap, err := tc.metadataKV.BatchGetRaw(ctx, syncReq.Namespace, syncReq.Keys)
	if err != nil {
		logging.WithField("error", err).Warn("批量获取元数据失败")
		// 继续处理，返回已获取的部分数据
	}

	syncResp.Metadata = metadataMap

	return msgpack.Marshal(syncResp)
}

// HandleMetadataChangeNotification 处理元数据变更通知
//
//nolint:unused // 阶段 3 集成时使用
func (tc *TreeCoordinator) HandleMetadataChangeNotification(ctx context.Context, req []byte) ([]byte, error) {
	// 解析通知
	var notification metadatarpc.MetadataChangeNotification
	if err := msgpack.Unmarshal(req, &notification); err != nil {
		return nil, metadatarpc.NewRPCError(metadatarpc.ErrCodeBadRequest, "invalid metadata change notification")
	}

	logging.WithFields(map[string]any{
		"namespace": notification.Namespace,
		"key":       notification.Key,
		"operation": notification.Operation,
		"version":   notification.Version,
	}).Debug("收到元数据变更通知")

	// 处理变更通知（异步拉取最新元数据）
	go tc.fetchMetadataAfterNotification(notification.Namespace, notification.Key)

	// 返回成功响应
	resp := &metadatarpc.MetadataSyncResponse{
		Namespace: notification.Namespace,
		Metadata:  make(map[string][]byte),
		Version:   notification.Version,
		Timestamp: time.Now().UnixNano(),
	}

	return msgpack.Marshal(resp)
}

// fetchMetadataAfterNotification 收到变更通知后，从随机节点拉取最新元数据
func (tc *TreeCoordinator) fetchMetadataAfterNotification(ns, key string) {
	// 检查 RPC 客户端是否可用
	if tc.rpcClient == nil {
		return
	}

	// 随机选择一个节点拉取元数据
	targetNodes := tc.getRandomReadyNodes(1)
	if len(targetNodes) == 0 {
		logging.WithField("namespace", ns).Debug("无可用节点拉取元数据")
		return
	}

	targetNode := targetNodes[0]

	// 构造同步请求
	syncReq := metadatarpc.NewMetadataSyncRequest(ns, []string{key}, 0)
	reqBody, err := msgpack.Marshal(syncReq)
	if err != nil {
		logging.WithField("error", err).Error("序列化元数据同步请求失败")
		return
	}

	// 发送同步请求
	targetPeerID := tc.addrToPeerID(targetNode.Addr.TCPAddr())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	respBody, err := tc.rpcClient.Call(ctx, targetPeerID, "MetadataSync", reqBody)
	if err != nil {
		logging.WithFields(map[string]any{
			"target_node": targetNode.NodeID,
			"namespace":   ns,
			"key":         key,
			"error":       err,
		}).Debug("拉取元数据失败")
		return
	}

	// 解析响应
	var syncResp metadatarpc.MetadataSyncResponse
	if err := msgpack.Unmarshal(respBody, &syncResp); err != nil {
		logging.WithField("error", err).Error("解析元数据同步响应失败")
		return
	}

	// 更新本地元数据（使用原始字节存储）
	tc.metadataMu.Lock()
	defer tc.metadataMu.Unlock()

	if tc.metadataKV != nil {
		updatedCount := 0
		for key, data := range syncResp.Metadata {
			// 使用 PutRaw 直接存储原始字节
			if err := tc.metadataKV.PutRaw(context.Background(), ns, key, data); err != nil {
				logging.WithFields(map[string]any{
					"namespace": ns,
					"key":       key,
					"error":     err,
				}).Warn("存储同步元数据失败")
				continue
			}
			updatedCount++
		}

		if updatedCount > 0 {
			logging.WithFields(map[string]any{
				"namespace":     ns,
				"updated_count": updatedCount,
			}).Debug("元数据同步完成")
		}
	}
}

// ========================================
// 元数据迁移辅助方法
// ========================================

// migrateNodeFromLegacy 从旧格式迁移节点数据到 MetadataKV
//
//nolint:unused // 阶段 3 集成时使用
func (tc *TreeCoordinator) migrateNodeFromLegacy(ctx context.Context, node *Node) error {
	tc.metadataMu.RLock()
	defer tc.metadataMu.RUnlock()

	if tc.metadataAPI == nil {
		return fmt.Errorf("metadata API not initialized")
	}

	// 将旧格式 Node 转换为新格式 NodeInfo
	nodeInfo := &types.NodeInfo{
		NodeID: node.NodeID,
		HostID: node.HostID,
		Role:   convertNodeRole(node.Role),
		Addr: types.NodeAddress{
			Host:    node.Addr.Host,
			TCPPort: node.Addr.TCPPort,
		},
		ParentID:      node.ParentID,
		Level:         node.Level,
		Status:        convertNodeStatus(node.Status),
		Priority:      node.Priority,
		LastHeartbeat: node.LastHeartbeat,
		Version:       uint64(time.Now().UnixNano()),
	}

	// 写入 MetadataKV
	return tc.metadataAPI.SetNodeInfo(ctx, nodeInfo)
}

// convertNodeRole 将 int 类型的 NodeRole 转换为 string 类型
func convertNodeRole(role NodeRole) types.NodeRole {
	switch role {
	case Leaf:
		return types.NodeRoleLeaf
	case Parent:
		return types.NodeRoleParent
	case ParentStandby:
		return types.NodeRoleParentStandby
	default:
		return types.NodeRoleLeaf // 默认为叶子节点
	}
}

// convertNodeStatus 将 int 类型的 NodeStatus 转换为 string 类型
func convertNodeStatus(status NodeStatus) types.NodeStatus {
	switch status {
	case NodeStatusInit:
		return types.NodeStatusInit
	case NodeStatusReady:
		return types.NodeStatusReady
	case NodeStatusJoining:
		return types.NodeStatusJoining
	case NodeStatusLeaving:
		return types.NodeStatusLeaving
	case NodeStatusFailed:
		return types.NodeStatusFailed
	default:
		return types.NodeStatusInit // 默认为初始化状态
	}
}

// migrateTopologyFromLegacy 从旧格式迁移拓扑数据到 MetadataKV
//
//nolint:unused // 阶段 3 集成时使用
func (tc *TreeCoordinator) migrateTopologyFromLegacy(ctx context.Context, node *Node) error {
	tc.metadataMu.RLock()
	defer tc.metadataMu.RUnlock()

	if tc.metadataAPI == nil {
		return fmt.Errorf("metadata API not initialized")
	}

	// 将旧格式拓扑关系转换为新格式 TopologyInfo
	topoInfo := &types.TopologyInfo{
		NodeID:      node.NodeID,
		ParentID:    node.ParentID,
		ChildrenIDs: node.ChildrenIDs,
		Level:       node.Level,
		Version:     uint64(time.Now().UnixNano()),
	}

	// 写入 MetadataKV
	return tc.metadataAPI.SetTopologyInfo(ctx, topoInfo)
}

// ========================================
// 辅助方法
// ========================================

// nodeToNodeInfo 将 Node 转换为 NodeInfo
func (tc *TreeCoordinator) nodeToNodeInfo(node *Node) *types.NodeInfo {
	return &types.NodeInfo{
		NodeID: node.NodeID,
		HostID: node.HostID,
		Role:   convertNodeRole(node.Role),
		Addr: types.NodeAddress{
			Host:    node.Addr.Host,
			TCPPort: node.Addr.TCPPort,
		},
		ParentID:      node.ParentID,
		Level:         node.Level,
		Status:        convertNodeStatus(node.Status),
		Priority:      node.Priority,
		LastHeartbeat: node.LastHeartbeat,
		Version:       uint64(time.Now().UnixNano()),
	}
}

// getRandomReadyNodes 随机获取指定数量的就绪节点
func (tc *TreeCoordinator) getRandomReadyNodes(count int) []*Node {
	tc.nodesMu.RLock()
	defer tc.nodesMu.RUnlock()

	// 收集所有就绪节点
	var readyNodes []*Node
	for _, node := range tc.allNodes {
		// 跳过本地节点
		if node.NodeID == tc.localNode.NodeID {
			continue
		}
		// 只选择就绪状态的节点
		if node.Status == NodeStatusReady {
			readyNodes = append(readyNodes, node)
		}
	}

	// 如果节点数量不足，返回全部
	if len(readyNodes) <= count {
		return readyNodes
	}

	// 随机选择指定数量的节点
	// 这里简化处理，实际可以使用 Fisher-Yates 洗牌算法
	result := make([]*Node, 0, count)
	indices := make(map[int]struct{})

	for len(result) < count {
		idx := time.Now().UnixNano() % int64(len(readyNodes))
		if _, exists := indices[int(idx)]; !exists {
			indices[int(idx)] = struct{}{}
			result = append(result, readyNodes[int(idx)])
		}
	}

	return result
}

// ========================================
// 增强的 Quorum 确认（带重试机制）
// ========================================

// RetryPolicy 重试策略配置
type RetryPolicy struct {
	// MaxAttempts 最大重试次数
	MaxAttempts int
	// InitialBackoff 初始退避时间
	InitialBackoff time.Duration
	// MaxBackoff 最大退避时间
	MaxBackoff time.Duration
	// BackoffFactor 退避因子（每次重试后乘以这个值）
	BackoffFactor float64
}

// DefaultRetryPolicy 返回默认重试策略
func DefaultRetryPolicy() *RetryPolicy {
	return &RetryPolicy{
		MaxAttempts:    3,
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     5 * time.Second,
		BackoffFactor:  2.0,
	}
}

// QuorumResult Quorum 确认结果
type QuorumResult struct {
	// Success 是否达成 Quorum
	Success bool
	// Acks 确认数量
	Acks int
	// Threshold Quorum 阈值
	Threshold int
	// Attempts 尝试次数
	Attempts int
	// LastError 最后一次错误
	LastError error
}

// quorumMetadataChangeWithRetry 带重试机制的 Quorum 元数据变更确认
//
// P1 修复：添加重试机制，提高元数据同步可靠性
//
//nolint:unused // 阶段 3 集成时使用
func (tc *TreeCoordinator) quorumMetadataChangeWithRetry(ns, key string, version uint64, policy *RetryPolicy) *QuorumResult {
	if policy == nil {
		policy = DefaultRetryPolicy()
	}

	var lastErr error
	backoff := policy.InitialBackoff

	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		result := tc.quorumMetadataChangeOnce(ns, key, version)
		result.Attempts = attempt

		if result.Success {
			logging.WithFields(map[string]any{
				"namespace": ns,
				"key":       key,
				"version":   version,
				"attempts":  attempt,
				"acks":      result.Acks,
				"threshold": result.Threshold,
			}).Info("Quorum 确认成功（带重试）")
			return result
		}

		// 最后一次尝试失败
		if attempt == policy.MaxAttempts {
			lastErr = fmt.Errorf("quorum 确认失败: %d/%d 确认，尝试 %d 次",
				result.Acks, result.Threshold, attempt)
			logging.WithFields(map[string]any{
				"namespace": ns,
				"key":       key,
				"version":   version,
				"acks":      result.Acks,
				"threshold": result.Threshold,
				"attempts":  attempt,
			}).Warn("Quorum 确认最终失败")
			result.LastError = lastErr
			return result
		}

		// 等待后重试
		logging.WithFields(map[string]any{
			"namespace":  ns,
			"key":        key,
			"attempt":    attempt,
			"next_retry": attempt + 1,
			"backoff":    backoff,
		}).Debug("Quorum 确认未达成，等待后重试")

		time.Sleep(backoff)

		// 计算下一次退避时间（指数退避）
		backoff = time.Duration(float64(backoff) * policy.BackoffFactor)
		if backoff > policy.MaxBackoff {
			backoff = policy.MaxBackoff
		}
	}

	return &QuorumResult{
		Success:   false,
		Attempts:  policy.MaxAttempts,
		LastError: lastErr,
	}
}

// ========================================
// 增强的 Gossip 扩散（带重试机制）
// ========================================

// gossipMetadataChangeWithRetry 带重试机制的 Gossip 元数据变更扩散
//
// P1 修复：添加重试机制，提高元数据扩散可靠性
//
//nolint:unused // 阶段 3 集成时使用
func (tc *TreeCoordinator) gossipMetadataChangeWithRetry(ns, key string, version uint64, policy *RetryPolicy) {
	if policy == nil {
		policy = DefaultRetryPolicy()
	}

	// 获取就绪节点数量以确定需要的确认数
	targetNodes := tc.getRandomReadyNodes(3)
	if len(targetNodes) == 0 {
		return
	}

	// 构造元数据变更通知
	notification := metadatarpc.NewMetadataChangeNotification(ns, key, "put", version)
	reqBody, err := msgpack.Marshal(notification)
	if err != nil {
		logging.WithField("error", err).Error("序列化元数据变更通知失败")
		return
	}

	// 尝试发送到多个节点（Gossip 不需要全部成功，只要一部分成功即可）
	successCount := 0
	minSuccess := (len(targetNodes) + 1) / 2 // 至少一半成功

	backoff := policy.InitialBackoff
	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		attemptSuccess := 0

		for _, targetNode := range targetNodes {
			if tc.sendMetadataSyncOnce(targetNode, reqBody) {
				attemptSuccess++
			}
		}

		successCount += attemptSuccess
		if successCount >= minSuccess {
			logging.WithFields(map[string]any{
				"namespace":     ns,
				"key":           key,
				"version":       version,
				"attempts":      attempt,
				"success_count": successCount,
				"min_success":   minSuccess,
			}).Debug("Gossip 扩散成功（带重试）")
			return
		}

		// 最后一次尝试失败
		if attempt == policy.MaxAttempts {
			logging.WithFields(map[string]any{
				"namespace":     ns,
				"key":           key,
				"version":       version,
				"success_count": successCount,
				"min_success":   minSuccess,
				"attempts":      attempt,
			}).Warn("Gossip 扩散未达到最小成功数")
			return
		}

		// 等待后重试
		logging.WithFields(map[string]any{
			"namespace":  ns,
			"key":        key,
			"attempt":    attempt,
			"next_retry": attempt + 1,
			"backoff":    backoff,
		}).Debug("Gossip 扩散未达到最小成功数，等待后重试")

		time.Sleep(backoff)

		// 计算下一次退避时间
		backoff = time.Duration(float64(backoff) * policy.BackoffFactor)
		if backoff > policy.MaxBackoff {
			backoff = policy.MaxBackoff
		}
	}
}

// sendMetadataSyncOnce 单次元数据同步发送尝试
//
//nolint:unused // 预留给未来使用
func (tc *TreeCoordinator) sendMetadataSyncOnce(targetNode *Node, reqBody []byte) bool {
	if tc.rpcClient == nil {
		return false
	}

	// 使用信号量控制并发
	tc.gossipSemaphore <- struct{}{}
	defer func() { <-tc.gossipSemaphore }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	targetPeerID := tc.addrToPeerID(targetNode.Addr.TCPAddr())
	_, err := tc.rpcClient.Call(ctx, targetPeerID, "MetadataChangeNotify", reqBody)
	if err != nil {
		logging.WithFields(map[string]any{
			"target_node": targetNode.NodeID,
			"error":       err,
		}).Debug("发送元数据变更通知失败")
		return false
	}

	return true
}

// quorumMetadataChangeOnce 单次 Quorum 确认尝试
//
//nolint:unused // 预留给未来使用
func (tc *TreeCoordinator) quorumMetadataChangeOnce(ns, key string, version uint64) *QuorumResult {
	// 获取集群配置
	tc.nodesMu.RLock()
	totalNodes := len(tc.allNodes)
	tc.nodesMu.RUnlock()

	quorumThreshold := totalNodes/2 + 1 // 简单多数派

	// 构造元数据变更通知
	notification := metadatarpc.NewMetadataChangeNotification(ns, key, "put", version)
	reqBody, err := msgpack.Marshal(notification)
	if err != nil {
		return &QuorumResult{
			Success:   false,
			Threshold: quorumThreshold,
			LastError: fmt.Errorf("序列化失败: %w", err),
		}
	}

	// 获取所有节点
	tc.nodesMu.RLock()
	allNodes := make([]*Node, 0, len(tc.allNodes))
	for _, node := range tc.allNodes {
		// 跳过本地节点
		if node.NodeID != tc.localNode.NodeID && node.Status == NodeStatusReady {
			allNodes = append(allNodes, node)
		}
	}
	tc.nodesMu.RUnlock()

	if len(allNodes) == 0 {
		// 单节点集群，认为成功
		return &QuorumResult{
			Success:   true,
			Acks:      1, // 本地节点
			Threshold: 1,
		}
	}

	// 使用 channel 收集确认响应
	ackCh := make(chan struct{}, len(allNodes))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 并发发送确认请求
	for _, node := range allNodes {
		go func(n *Node) {
			defer func() { ackCh <- struct{}{} }()

			if tc.rpcClient == nil {
				return
			}

			targetPeerID := tc.addrToPeerID(n.Addr.TCPAddr())
			_, err := tc.rpcClient.Call(ctx, targetPeerID, "MetadataChangeNotify", reqBody)
			if err != nil {
				return
			}
		}(node)
	}

	// 等待多数派确认
	acks := 0
	for i := 0; i < len(allNodes); i++ {
		select {
		case <-ackCh:
			acks++
			if acks >= quorumThreshold {
				return &QuorumResult{
					Success:   true,
					Acks:      acks,
					Threshold: quorumThreshold,
				}
			}
		case <-ctx.Done():
			// 超时，返回当前结果
			return &QuorumResult{
				Success:   false,
				Acks:      acks,
				Threshold: quorumThreshold,
				LastError: context.DeadlineExceeded,
			}
		}
	}

	return &QuorumResult{
		Success:   false,
		Acks:      acks,
		Threshold: quorumThreshold,
	}
}
