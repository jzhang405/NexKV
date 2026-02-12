// Package kvstore 提供元数据管理的 KV 存储封装
//
// 核心功能：
//   - 命名空间隔离：9 个预定义命名空间，避免键冲突
//   - 强类型接口：类型安全的元数据访问
//   - MVCC 版本控制：复用 MVStore 的多版本能力
//   - MessagePack 序列化：高效的二进制序列化
package kvstore

const (
	// NamespaceCluster 集群级别元数据
	// 用于存储：
	//   - 集群配置（ClusterID、ClusterName、ClusterVersion）
	//   - 集群状态（State、TotalNodes、TotalShards）
	//   - 一致性配置（QuorumThreshold、GossipInterval）
	// 键格式：meta:cluster:{cluster_id}
	NamespaceCluster = "meta:cluster:"

	// NamespaceNode 节点元数据
	// 用于存储：
	//   - 节点基本信息（NodeID、HostID、Role）
	//   - 节点地址（Addr、TCPPort）
	//   - 节点状态（Status、Level、Priority）
	// 键格式：meta:node:{node_id}
	NamespaceNode = "meta:node:"

	// NamespaceRole 角色元数据（包含 Standby 管理）
	// 用于存储：
	//   - 角色定义（RoleID、RoleType）
	//   - 活跃节点列表（ActiveNodes）
	//   - 备用节点列表（StandbyNodes）
	//   - 故障转移历史（FailoverHistory）
	// 键格式：meta:role:{role_id}
	NamespaceRole = "meta:role:"

	// NamespaceTopo 拓扑元数据
	// 用于存储：
	//   - 节点拓扑关系（NodeID、ParentID、ChildrenIDs）
	//   - 树形层级信息（Level）
	//   - 拓扑版本控制（Version）
	// 键格式：meta:topo:{node_id}
	NamespaceTopo = "meta:topo:"

	// NamespaceShard 分片元数据
	// 用于存储：
	//   - 分片配置（ShardID、RangeStart、RangeEnd）
	//   - 副本分布（ReplicaNodes）
	//   - 分片状态（State、Version）
	// 键格式：meta:shard:{shard_id}
	NamespaceShard = "meta:shard:"

	// NamespaceStatic 静态配置元数据
	// 用于存储：
	//   - 集群静态配置（MaxChildren、MaxLevel）
	//   - 网络配置（HeartbeatInterval、HeartbeatTimeout）
	//   - 功能开关（AutoDiscovery、EnableSelfHealing）
	// 键格式：meta:static:{config_key}
	NamespaceStatic = "meta:static:"

	// NamespaceDynamic 动态状态元数据
	// 用于存储：
	//   - 节点动态状态（CPUUsage、MemUsage）
	//   - 负载信息（ExistingNodes、Throughput）
	//   - 健康状态（HealthScore）
	// 键格式：meta:dynamic:{node_id}:{metric_key}
	NamespaceDynamic = "meta:dynamic:"

	// NamespaceOp 操作记录元数据
	// 用于存储：
	//   - 操作日志（OpID、OpType、StartTime）
	//   - 操作状态（Status、Progress）
	//   - 操作结果（Result、Error）
	// 键格式：meta:op:{op_id}
	NamespaceOp = "meta:op:"

	// NamespaceVersion 版本控制元数据
	// 用于存储：
	//   - MVCC 版本信息（Key、Version、Timestamp）
	//   - 数据版本历史（VersionHistory）
	//   - 冲突解决记录（ConflictResolution）
	// 键格式：meta:version:{key}:{version}
	NamespaceVersion = "meta:version:"
)

// NamespaceInfo 命名空间信息
type NamespaceInfo struct {
	// Prefix 命名空间前缀
	Prefix string

	// Description 命名空间描述
	Description string

	// ExampleKey 示例键格式
	ExampleKey string
}

// GetAllNamespaces 返回所有命名空间信息
func GetAllNamespaces() map[string]NamespaceInfo {
	return map[string]NamespaceInfo{
		NamespaceCluster: {
			Prefix:      NamespaceCluster,
			Description: "集群级别元数据",
			ExampleKey:  "meta:cluster:cluster-001",
		},
		NamespaceNode: {
			Prefix:      NamespaceNode,
			Description: "节点元数据",
			ExampleKey:  "meta:node:node-001",
		},
		NamespaceRole: {
			Prefix:      NamespaceRole,
			Description: "角色元数据（含 Standby）",
			ExampleKey:  "meta:role:role-parent-001",
		},
		NamespaceTopo: {
			Prefix:      NamespaceTopo,
			Description: "拓扑元数据",
			ExampleKey:  "meta:topo:node-001",
		},
		NamespaceShard: {
			Prefix:      NamespaceShard,
			Description: "分片元数据",
			ExampleKey:  "meta:shard:shard-001",
		},
		NamespaceStatic: {
			Prefix:      NamespaceStatic,
			Description: "静态配置元数据",
			ExampleKey:  "meta:static:max_children",
		},
		NamespaceDynamic: {
			Prefix:      NamespaceDynamic,
			Description: "动态状态元数据",
			ExampleKey:  "meta:dynamic:node-001:cpu_usage",
		},
		NamespaceOp: {
			Prefix:      NamespaceOp,
			Description: "操作记录元数据",
			ExampleKey:  "meta:op:op-20250209-001",
		},
		NamespaceVersion: {
			Prefix:      NamespaceVersion,
			Description: "版本控制元数据",
			ExampleKey:  "meta:version:node-001:1234567890",
		},
	}
}

// ValidateNamespace 验证命名空间是否有效
func ValidateNamespace(ns string) bool {
	switch ns {
	case NamespaceCluster,
		NamespaceNode,
		NamespaceRole,
		NamespaceTopo,
		NamespaceShard,
		NamespaceStatic,
		NamespaceDynamic,
		NamespaceOp,
		NamespaceVersion:
		return true
	default:
		return false
	}
}

// BuildKey 构建完整的键（命名空间 + 用户键）
func BuildKey(namespace, key string) string {
	return namespace + key
}

// ParseKey 解析完整的键，返回命名空间和用户键
func ParseKey(fullKey string) (namespace, key string, ok bool) {
	for _, ns := range []string{
		NamespaceCluster,
		NamespaceNode,
		NamespaceRole,
		NamespaceTopo,
		NamespaceShard,
		NamespaceStatic,
		NamespaceDynamic,
		NamespaceOp,
		NamespaceVersion,
	} {
		if len(fullKey) > len(ns) && fullKey[:len(ns)] == ns {
			return ns, fullKey[len(ns):], true
		}
	}
	return "", "", false
}
