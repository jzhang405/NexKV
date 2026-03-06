// Package rpc

// SourceIDStrategy 定义 SourceID 亲和性策略
type SourceIDStrategy int

const (
	 // SourceStrategyNetwork 默认：无亲和性
  SourceStrategyNetwork SourceIDStrategy = iota
  // SourceStrategyShard 按分片亲和
  SourceStrategyShard
  // SourceStrategyClient 按客户端亲和
  SourceStrategyClient
  // SourceStrategyRaft 按 Raft 节点亲和
  SourceStrategyRaft
)

// GetSourceID 根据消息类型和策略选择合适的 SourceID
func GetSourceID(strategy SourceIDStrategy, msg model.Message, peer model.PeerID) model.SourceID {
  switch strategy {
  case SourceStrategyShard:
    // 尝试从 Extensions 中获取 ShardID
    if shardID := getShardIDFromExtensions(msg.Exts()); shardID != "" {
      return model.NewSourceShard(shardID)
    }
    return model.SourceNetwork

  case SourceStrategyClient:
    // 尝试从 Extensions 中获取 ClientID
    if clientID := getClientIDFromExtensions(msg.Exts()); clientID != "" {
      return model.NewSourceClient(clientID)
    }
    return model.SourceNetwork
  case SourceStrategyRaft:
    // 按 Raft 节点亲和
    return model.NewSourceNode(peer.String())
  default:
    return model.SourceNetwork
  }
}
}

// getShardIDFromExtensions 从扩展中获取 ShardID
func getShardIDFromExtensions(exts model.Extensions) string {
  if val, ok := exts.Get("shard_id"); ok {
    if shardID, ok := val.(string); ok {
      return shardID
    }
  }
  return ""
}
)
