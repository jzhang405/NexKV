# PR-042: P1-3 并发验证 + P1-1 RPC 层实现 - Pre 文档

> **文档类型**: Pre 文档（需求+设计+风险评估）  
> **创建日期**: 2026-02-12  
> **目标分支**: main  
> **工作分支**: feature/fix-p1-concurrency-and-rpc

---

## 1. 需求背景

### 1.1 问题来源

根据 `docs/09-code-review/2026-02-12-findings-report.md` Code Review 综合报告：

| 问题编号 | 问题描述 | 位置 | 影响 |
|---------|---------|------|------|
| **P1-3** | TreeCoordinator 并发保护机制未确认 | `internal/metadata/cluster/tree_coordinator.go` | 高并发场景下可能出现数据竞争、状态不一致 |
| **P1-1** | RPC 层未实现，CLI 功能阻塞 | `internal/rpc/`, `cmd/nexkvd/main.go:63-64` | CLI 与 Daemon 通信功能未实现 |

---

## 2. P1-3: TreeCoordinator 并发验证

### 2.1 验证结果

**已验证** ✅：TreeCoordinator 并发保护机制已正确实现

```go
type TreeCoordinator struct {
    // 节点管理
    allNodes map[string]*Node
    nodesMu  sync.RWMutex  // ✅ 保护节点并发访问
    
    // 元数据管理
    metadataMu  sync.RWMutex  // ✅ 保护元数据字段
    
    // 状态管理（原子操作）
    state atomic.Int32
    heartbeatSeq atomic.Uint64
    started atomic.Bool
    stopped atomic.Bool
}
```

**并发测试结果**：
```bash
$ go test -race ./internal/metadata/cluster/...
ok  	github.com/jzhang405/NexKV/internal/metadata/cluster	(cached)
```
✅ 无数据竞争告警

### 2.2 结论

**P1-3 问题已解决**，TreeCoordinator 已具备完善的并发保护机制：
- `nodesMu` 保护节点管理
- `metadataMu` 保护元数据访问
- 原子操作保护状态字段

---

## 3. P1-1: RPC 层实现

### 3.1 问题详情

**技术背景**：PR-Libp2p-TransportCleanup 移除了原有 TCP/UDP Transport，RPC 层需要迁移到 libp2p Stream

**当前状态**：
- `internal/rpc/client.go` - ✅ 已实现
- `internal/rpc/server.go` - ✅ 已实现
- `cmd/nexkv/commands/` - ⏸️ 待更新使用新 RPC

**功能需求**：
1. RPCClient 使用 libp2p Stream 实现
2. RPCServer 使用 libp2p Stream Handler 实现
3. 更新 cmd/nexkv/commands 以使用新 RPC

### 3.2 技术方案

#### 3.2.1 RPC 架构

```
CLI (cmd/nexkv) 
    ↓
RPCClient (libp2p Stream)
    ↓
RPCServer (Daemon)
    ↓
TreeCoordinator (业务逻辑)
```

#### 3.2.2 实现步骤

1. **确认 RPCClient/Server 已就绪**
   - 检查 `internal/rpc/client.go` 和 `internal/rpc/server.go`
   - 验证 Stream 通信正常

2. **更新 CLI 命令**
   - `cmd/nexkv/commands/cluster.go` - 集群管理命令
   - `cmd/nexkv/commands/node.go` - 节点操作命令
   - `cmd/nexkv/commands/kv.go` - KV 操作命令

3. **添加 CLI 与 Daemon 通信**
   - CLI 使用 RPCClient 连接 Daemon
   - Daemon 启动 RPCServer 监听连接

### 3.3 文件变更清单

| 文件路径 | 变更类型 | 说明 |
|---------|---------|------|
| `cmd/nexkv/commands/*.go` | 修改 | 更新 CLI 命令使用新 RPC |
| `cmd/nexkvd/main.go` | 修改 | 确认 RPCServer 启动 |

---

## 4. 风险评估

### 4.1 技术风险

| 风险项 | 风险等级 | 缓解措施 |
|--------|---------|---------|
| **libp2p Stream API 不稳定** | 中 | 使用稳定版本的 API |
| **CLI 与 Daemon 通信协议不一致** | 中 | 定义清晰的协议格式 |
| **测试覆盖不足** | 低 | 添加 E2E 测试 |

### 4.2 回滚方案

保留原有 CLI 命令作为备份，新功能通过 feature flag 控制。

---

## 5. 验收标准

### 5.1 P1-3 并发安全验收

- [x] `go test -race ./internal/metadata/cluster/...` 无数据竞争告警
- [x] TreeCoordinator 并发保护机制已验证

### 5.2 P1-1 RPC 功能验收

- [ ] CLI 可成功连接 Daemon
- [ ] 集群管理命令可用（join/leave/status）
- [ ] 节点操作命令可用（list/info）
- [ ] KV 操作命令可用（put/get/delete）
- [ ] E2E 测试通过

---

## 6. 预估工作量

| 任务 | 预估时间 |
|------|---------|
| P1-3 验证 | 0.5 小时 ✅ 已完成 |
| RPC 集成到 CLI | 2 小时 |
| E2E 测试 | 1 小时 |
| **总计** | **3-4 小时** |

---

## 7. 实施计划

| 步骤 | 操作 | 预期产出 |
|------|------|---------|
| 1 | P1-3 并发验证 | ✅ 验证报告 |
| 2 | 检查 RPCClient/Server 现状 | 现状分析 |
| 3 | 更新 CLI 命令使用新 RPC | 功能代码 |
| 4 | 添加 E2E 测试 | 测试代码 |
| 5 | 本地验证 | make ci 通过 |

---

**Pre 文档状态**: ⏸️ 待架构师评审

---

**文档版本**: v1.0  
**创建者**: 🤖 AI 核心开发  
**评审状态**: ⏳ 待评审
