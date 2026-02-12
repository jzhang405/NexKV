# 阶段 1.2：接口边界分析

> NexKV 接口设计评估

**创建时间**：2026-02-12
**分析范围**：`internal/` 核心接口

---

## 接口清单

### 1. metadata/api.Provider ⭐ 核心

**文件位置**：`internal/metadata/api/interface.go`

**方法数量**：13 个

| 方法 | 说明 | 参数 |
|------|------|------|
| GetNodeInfo | 获取节点信息 | nodeID |
| SetNodeInfo | 设置节点信息 | *NodeInfo |
| ListNodes | 列出所有节点 | - |
| DeleteNode | 删除节点 | nodeID |
| GetRoleInfo | 获取角色信息 | roleID |
| SetRoleInfo | 设置角色信息 | *RoleInfo |
| ListRoles | 列出所有角色 | - |
| GetTopologyInfo | 获取拓扑信息 | nodeID |
| SetTopologyInfo | 设置拓扑信息 | *TopologyInfo |
| GetShardInfo | 获取分片信息 | shardID |
| SetShardInfo | 设置分片信息 | *ShardInfo |

**评估**：

| 维度 | 评估 | 说明 |
|------|------|------|
| 职责单一 | ✅ 是 | 专注于元数据 CRUD |
| 方法数量 | ✅ 合理 | 13 个方法，按功能分组 |
| 接口命名 | ✅ 清晰 | Provider 准确表达意图 |
| 文档完整性 | ✅ 良好 | 每个方法都有注释 |

---

### 2. kvstore.Store

**文件位置**：`internal/metadata/kvstore/interface.go`

**方法数量**：10 个

| 方法 | 说明 | 返回值 |
|------|------|--------|
| Put | 写入键值对 | error |
| Get | 获取键值 | error |
| Delete | 删除键 | error |
| Exists | 检查键存在 | (bool, error) |
| ListPrefix | 列出前缀键 | ([]string, error) |
| Close | 关闭存储 | error |
| GetRaw | 获取原始字节 | ([]byte, error) |
| PutRaw | 写入原始字节 | error |
| BatchGetRaw | 批量获取 | (map[string][]byte, error) |

**评估**：

| 维度 | 评估 | 说明 |
|------|------|------|
| 职责单一 | ✅ 是 | 专注于 KV 存储 |
| 方法数量 | ✅ 合理 | 10 个方法，简洁 |
| 原子操作 | ✅ 支持 | BatchGetRaw 支持批量 |
| 文档完整性 | ✅ 良好 | 有详细注释 |

---

### 3. store.MVStore

**文件位置**：`internal/wal/mvstore.go`

**方法数量**：13 个

| 方法 | 说明 | MVCC 相关 |
|------|------|-----------|
| Put | 写入键值对 | ✅ 自动生成版本 |
| Get | 获取最新版本 | ✅ |
| GetVersion | 获取指定版本 | ✅ HLC 时间戳查询 |
| Delete | 删除键 | ✅ 墓碑标记 |
| Exists | 检查键存在 | - |
| List | 列出所有键 | - |
| ListPrefix | 列出前缀键 | - |
| GetVersionCount | 获取版本数量 | ✅ |
| GetAllVersions | 获取所有版本 | ✅ |
| Flush | 刷盘 | - |
| CreateSnapshot | 创建快照 | ✅ |
| RestoreFromSnapshot | 恢复快照 | ✅ |
| Close | 关闭存储 | - |

**评估**：

| 维度 | 评估 | 说明 |
|------|------|------|
| 职责单一 | ✅ 是 | MVCC 存储 |
| 方法数量 | ✅ 合理 | 13 个方法 |
| MVCC 支持 | ✅ 完整 | 版本管理完善 |
| 文档完整性 | ✅ 优秀 | 每个方法都有详细说明 |

---

### 4. Transport 接口

**文件位置**：`internal/transport/libp2p_transport_adapter.go`

**方法数量**：（需进一步确认完整定义）

**评估**：

| 维度 | 评估 | 说明 |
|------|------|------|
| 待分析 | ⏳ | 需要完整代码才能评估 |

---

### 5. ConsistencyCoordinator 接口

**文件位置**：`internal/metadata/consistency/coordinator.go`

**方法数量**：（需进一步确认完整定义）

**评估**：

| 维度 | 评估 | 说明 |
|------|------|------|
| 待分析 | ⏳ | 需要完整代码才能评估 |

---

## 接口设计原则检查

### ISP（接口隔离原则）

| 接口 | 评估 | 说明 |
|--------|------|------|
| Provider | ✅ 通过 | 方法按功能分组，没有冗余 |
| Store (kvstore) | ✅ 通过 | 接口精简，职责单一 |
| MVStore | ✅ 通过 | 版本管理接口清晰 |

### SRP（单一职责原则）

| 接口 | 评估 | 说明 |
|--------|------|------|
| Provider | ✅ 通过 | 只负责元数据访问 |
| Store (kvstore) | ✅ 通过 | 只负责 KV 操作 |
| MVStore | ✅ 通过 | 只负责多版本存储 |

---

## 接口命名规范检查

| 接口 | 命名 | 评估 |
|--------|--------|------|
| Provider | ✅ 清晰 | 表达"提供元数据"的意图 |
| Store (kvstore) | ✅ 清晰 | 表达"存储"的职责 |
| MVStore | ✅ 清晰 | 表达"多版本存储"的特性 |

---

## 观察与发现

### ✅ 设计优点

1. **依赖倒置**：接口由提供者定义，不是使用者
2. **职责清晰**：每个接口都有明确的职责范围
3. **命名规范**：接口命名清晰表达意图

### ⚠️ 潜在问题

1. **接口过多**：
   - `kvstore.Store` 和 `store.MVStore` 功能有重叠
   - 需要确认是否可以合并或明确边界

2. **Raw 方法**：
   - `GetRaw/PutRaw/BatchGetRaw` 的用途需要明确
   - 用于网络传输，但应该属于独立的协议层

### 📌 需要进一步追踪

1. Transport 接口的完整定义
2. ConsistencyCoordinator 的方法列表
3. Raw 方法的使用场景

---

**下一步**：→ [阶段 1.3：职责越界检查](phase1_boundary_violations.md)
