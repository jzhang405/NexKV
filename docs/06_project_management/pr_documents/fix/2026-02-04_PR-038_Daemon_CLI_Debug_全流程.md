# 【PR全流程文档】Fix - Daemon 与 CLI 调试

> **文档说明**：本文档包含「前置规划」和「后置总结」两部分，记录从Bug发现到修复完成的全流程，一个PR对应一份全流程文档，归档后作为项目追溯依据。

---

## 第一部分：前置部分（开工前必完成，架构师评审通过）

### 1. 基础信息（与分支/PR绑定）

| 项目 | 内容 |
|------|------|
| 工作类型 | Bug修复（Fix） |
| PR编号 | PR-038（创建GitHub PR后补充完整） |
| 分支名称 | fix/debug-daemon-cli |
| 工作主题 | 调试和修复 NexKV Daemon 与 CLI 交互问题 |
| 负责人 | 🤖 核心开发 + AI 团队 |
| 分支创建日期 | 2026-02-04 |
| 计划开工日期 | 2026-02-04 |
| 计划CI通过日期 | 2026-02-05 |
| 关联Bug单号 | 待发现（调试过程中识别） |
| 严重等级 | □ 致命 ☑ 严重 □ 一般 □ 较低 |
| 架构师评审状态 | □ 待评审 ☑ 评审中 □ 评审通过 □ 需优化 |
| 预审批结果 | ☑ 未通过 □ 已通过（架构师签字/备注：XXX 202X-XX-XX 同意修复） |

### 2. Bug描述（发生了什么）

#### 2.1 Bug现象
- **影响范围**：Daemon 启动、CLI 与 Daemon 的 RPC 通信
- **触发条件**：启动 Daemon 进程，使用 CLI 命令与 Daemon 交互
- **实际表现**：待调试（需要实际运行发现）
- **预期行为**：
  - Daemon 应该能够正常启动并监听在指定地址（默认 127.0.0.1:9211）
  - CLI 应该能够通过 RPC 与 Daemon 通信
  - 所有 CLI 命令（node add/remove/list/ping, cluster status/topology/info/health）应该正常工作

#### 2.2 影响评估
- **影响用户**：所有使用 NexKV 的用户
- **影响数据**：无（仅影响功能使用）
- **业务影响**：高（无法正常使用 CLI 管理 Daemon）
- **紧急程度**：高（阻塞基本功能使用）

### 3. 根因分析（为什么发生）

#### 3.1 问题定位
- **问题代码位置**：待定位（调试过程中发现）
- **问题函数**：待定位
- **问题逻辑**：待分析

#### 3.2 根本原因
- **直接原因**：待分析（可能的问题点）
  - Daemon 初始化失败（7步初始化过程中的某一步）
  - Transport 层绑定端口失败
  - RPC Server/Client 创建失败
  - TreeCoordinator 启动失败
- **深层原因**：待分析
- **相关代码**：
  - `cmd/nexkvd/main.go:223-401` - Daemon 7步初始化流程
  - `cmd/nexkv/commands/node.go` - CLI Node 命令
  - `cmd/nexkv/commands/cluster.go` - CLI Cluster 命令
  - `internal/metadata/transport/tcp_transport.go` - TCP Transport 实现
  - `internal/metadata/transport/rpc_server.go` - RPC Server 实现
  - `internal/metadata/transport/rpc_client.go` - RPC Client 实现

### 4. 修复方案（怎么修）

#### 4.1 测试环境配置
- **配置文件**：`configs/config.yaml`
- **运行模式**：dev（开发模式，详细日志）
- **测试节点**：host-1 / node-1
- **监听地址**：127.0.0.1:9211 (TCP), 127.0.0.1:9212 (UDP)

#### 4.2 修复策略
- **修复方式**：□ 直接修复 ☑ 重构 □ 其他
- **影响范围**：待确定（根据调试结果）
- **简化策略**（根据架构师评审意见）：
  - ✅ **去掉 MVStore**：node 管理只保留在 memory 中
  - ✅ **先验证基本功能**：daemon 启动、CLI 通信、node 管理
  - ⏳ **MVStore 后续完成**：待基本功能验证后再添加持久化

#### 4.3 构建流程

**步骤 1：创建 bin 目录**
```bash
mkdir -p bin
```

**步骤 2：构建 Daemon**
```bash
go build -o bin/nexkvd ./cmd/nexkvd
```

**步骤 3：构建 CLI**
```bash
go build -o bin/nexkv ./cmd/nexkv
```

**构建验证**：
```bash
ls -lh bin/
# 预期输出：
# -rwxr-xr-x  1 user  staff   1.2M Feb  4 10:00 nexkv
# -rwxr-xr-x  1 user  staff   2.5M Feb  4 10:00 nexkvd
```

---

#### 4.4 测试策略与预期输出

##### 测试 1：Daemon 启动

**命令**：
```bash
./bin/nexkvd --config configs/config.yaml --host-id host-1 --node-id node-1 --mode dev
```

**预期输出**：
```
[INFO] NexKV Daemon v0.1.0 starting...
[INFO] Configuration loaded from: configs/config.yaml
[INFO]
[INFO] ===== Configuration Summary =====
[INFO] Cluster: nexkv-cluster
[INFO) Base Directory: /Users/zhangcz/.nexkv
[INFO] Host ID: host-1
[INFO] Node ID: node-1
[INFO]
[INFO] ===== Step 1/7: Creating Transports =====
[INFO] TCP Transport: binding to /ip4/127.0.0.1/tcp/9211
[INFO] UDP Transport: binding to /ip4/127.0.0.1/udp/9212
[INFO] ✓ Transports created successfully
[INFO]
[INFO] ===== Step 2/7: Creating RPC Client =====
[INFO] RPC Client created
[INFO] ✓ RPC Client created successfully
[INFO]
[INFO] ===== Step 3/7: Creating RPC Server =====
[INFO] Registering RPC handlers...
[INFO]   - NodeJoinHandler
[INFO]   - NodeLeaveHandler
[INFO]   - ClusterStatusHandler
[INFO]   - NodePingHandler
[INFO] ✓ RPC Server created successfully
[INFO]
[INFO] ===== Step 4/7: Starting RPC Server =====
[INFO] RPC Server listening on 127.0.0.1:9211
[INFO] ✓ RPC Server started successfully
[INFO]
[INFO] ===== Step 5/7: Starting RPC Client =====
[INFO] RPC Client connecting to seed node: /ip4/127.0.0.1/tcp/9211
[INFO] ✓ RPC Client started successfully
[INFO]
[INFO] ===== Step 6/7: Creating TreeCoordinator =====
[INFO] TreeCoordinator created (mode: memory)
[INFO] ✓ TreeCoordinator created successfully
[INFO]
[INFO] ===== Step 7/7: Starting TreeCoordinator =====
[INFO] Starting as ROOT node
[INFO] Node registered: node-1 (role: root)
[INFO] ✓ TreeCoordinator started successfully
[INFO]
[INFO] ===== Daemon Started Successfully =====
[INFO] Listening on: 127.0.0.1:9211 (TCP)
[INFO] Node ID: node-1
[INFO] Role: ROOT
[INFO] Mode: dev
[INFO]
[INFO] Press Ctrl+C to stop
```

**可能的问题**：
- ❌ 端口被占用：`bind: address already in use`
- ❌ 配置文件错误：`failed to load configuration`
- ❌ RPC Server 启动失败：`failed to start RPC server`

---

##### 测试 2：CLI - node list（空节点列表）

**命令**：
```bash
./bin/nexkv node list
```

**预期输出**（启动后第一次查询）：
```
┌────────┬────────────────┬─────────────┬───────────┬────────────┐
│ NodeID │ Address        │ Role        │ Status    │ Parent     │
├────────┼────────────────┼─────────────┼───────────┼────────────┤
│ node-1 │ 127.0.0.1:9211 │ ROOT        │ ALIVE     │ -          │
└────────┴────────────────┴─────────────┴───────────┴────────────┘

Total: 1 nodes
```

**可能的问题**：
- ❌ RPC 连接失败：`failed to connect to daemon`
- ❌ 响应超时：`request timeout`

---

##### 测试 3：CLI - node status

**命令**：
```bash
./bin/nexkv node status node-1
```

**预期输出**：
```
Node: node-1
├─ Address: 127.0.0.1:9211
├─ Role: ROOT
├─ Status: ALIVE
├─ Parent: (none)
├─ Children: 0
├─ Uptime: 5s
└─ Version: 0.1.0
```

---

##### 测试 4：CLI - cluster status

**命令**：
```bash
./bin/nexkv cluster status
```

**预期输出**：
```
Cluster: nexkv-cluster
├─ Total Nodes: 1
├─ Active Nodes: 1
├─ Root Node: node-1
├─ Tree Depth: 1
├─ Tree Status: STABLE
└─ Mode: dev (memory storage)
```

---

##### 测试 5：CLI - cluster topology

**命令**：
```bash
./bin/nexkv cluster topology --format tree
```

**预期输出**：
```
nexkv-cluster (1 node)
└─ node-1 [ROOT, ALIVE]
```

---

##### 测试 6：CLI - node add（添加第二个节点）

**命令**：
```bash
# 在另一个终端启动第二个 daemon
./bin/nexkvd --config configs/config.yaml --host-id host-2 --node-id node-2 --mode dev

# 在第一个终端执行
./bin/nexkv node add node-2 --addr /ip4/127.0.0.1/tcp/9213
```

**预期输出**：
```
[INFO] Adding node: node-2
[INFO] Sending NodeJoin request to daemon...
✓ Node added successfully

Node: node-2
├─ Address: /ip4/127.0.0.1/tcp/9213
├─ Role: CHILD
├─ Parent: node-1
└─ Status: JOINING
```

**node list 预期输出**（添加后）：
```
┌────────┬────────────────┬─────────────┬───────────┬────────────┐
│ NodeID │ Address        │ Role        │ Status    │ Parent     │
├────────┼────────────────┼─────────────┼───────────┼────────────┤
│ node-1 │ 127.0.0.1:9211 │ ROOT        │ ALIVE     │ -          │
│ node-2 │ 127.0.0.1:9213 │ CHILD       │ ALIVE     │ node-1     │
└────────┴────────────────┴─────────────┴───────────┴────────────┘

Total: 2 nodes
```

---

##### 测试 7：CLI - node ping

**命令**：
```bash
./bin/nexkv node ping node-2
```

**预期输出**：
```
PING node-2 (127.0.0.1:9213)
├─ RTT: 2.3ms
├─ Status: ALIVE
└─ Response: pong
```

---

##### 测试 8：CLI - cluster health

**命令**：
```bash
./bin/nexkv cluster health
```

**预期输出**：
```
Cluster Health Check
├─ Overall Status: HEALTHY
├─ Total Nodes: 2
├─ Healthy Nodes: 2
├─ Unhealthy Nodes: 0
├─ Tree Depth: 2
└─ Recommendations: No issues detected
```

---

#### 4.4 实际输出对比模板

| 测试项 | 预期结果 | 实际结果 | 状态 | 备注 |
|--------|---------|---------|------|------|
| Daemon 启动 | 成功，7步初始化完成 | 待测试 | ⏳ | - |
| node list | 显示 node-1 | 待测试 | ⏳ | - |
| node status | 显示 node-1 详情 | 待测试 | ⏳ | - |
| cluster status | 1节点，STABLE | 待测试 | ⏳ | - |
| cluster topology | 树形结构 | 待测试 | ⏳ | - |
| node add | 成功添加 node-2 | 待测试 | ⏳ | - |
| node ping | RTT < 10ms | 待测试 | ⏳ | - |
| cluster health | HEALTHY | 待测试 | ⏳ | - |

#### 4.2 修复设计

```mermaid
flowchart TD
    subgraph 调试流程
        A[构建 Daemon 和 CLI] --> B[启动 Daemon]
        B --> C{Daemon 启动成功?}
        C -->|失败| D[分析启动日志<br/>定位失败点]
        C -->|成功| E[测试 CLI 命令]
        E --> F{命令执行成功?}
        F -->|失败| G[分析 RPC 通信<br/>定位失败点]
        F -->|成功| H[功能验证通过]
        D --> I[修复代码]
        G --> I
        I --> A
    end
```

#### 4.3 代码变更
- **修改文件**：待确定（根据调试结果）
- **新增代码**：待定
- **删除代码**：待定

### 5. 回滚方案（如何回退）

| 回滚触发条件 | 回滚步骤 | 验证方法 |
|-------------|----------|---------|
| 修复引入新问题 | git revert commit | 重新测试基本功能 |
| 影响其他功能 | 回滚到修复前版本 | 执行回归测试 |

### 6. 风险评估与应对措施

| 风险点 | 影响等级（高/中/低） | 应对措施 |
|--------|----------------------|----------|
| Daemon 无法启动 | 高 | 检查初始化流程，添加详细日志 |
| RPC 通信失败 | 高 | 检查 Transport 层实现和消息序列化 |
| 端口占用 | 中 | 添加端口冲突检测和友好提示 |
| 配置文件错误 | 中 | 添加配置验证和错误提示 |

### 7. 架构师评审记录

| 评审轮次 | 评审日期 | 评审人（架构师） | 核心评审意见 | 优化措施 | 优化结果 |
|----------|----------|------------------|--------------|----------|----------|
| 第1轮 | 待评审 | 👤 架构师 | 待评审 | 待优化 | 待完成 |

### 8. 预审批确认
> **架构师签字/备注**：待评审

---

## 第二部分：流程节点记录（修复/CI过程追溯）

### 1. 修复过程记录

| 节点 | 完成日期 | 具体内容 | 交付物 |
|------|----------|----------|--------|
| 启动修复 | 待定 | 待定 | 待定 |
| 本地验证 | 待定 | 待定 | 待定 |
| Post文档编写 | 待定 | 待定 | 待定 |
| 架构师Post批准 | 待定 | 待定 | 待定 |
| 提交GitHub | 待定 | 待定 | 待定 |

### 2. CI流程记录

| CI轮次 | 触发时间 | 结果 | 问题详情 | 修复措施 | 修复结果 |
|--------|----------|------|----------|----------|----------|
| 第1轮 | 待定 | 待定 | 待定 | 待定 | 待定 |

### 3. 合并记录

| 合并时间 | 合并方式 | 审批人 | 备注 |
|----------|----------|--------|------|
| 待定 | 待定 | 👤 架构师 | 待定 |

---

## 第三部分：后置部分（CI通过后编写，总结/验证）

> **说明**：此部分在 CI 通过后编写，当前为 Pre 文档阶段，待开发完成后补充。

### 1. 修复成果总结

#### 1.1 修复完成情况
- **已修复**：待补充
- **修复方式**：待补充
- **与Pre文档差异**：待补充

#### 1.2 验证成果
- **单元测试**：待补充
- **回归测试**：待补充
- **性能验证**：待补充

#### 1.3 代码/文档交付物

| 类型 | 具体内容 | 链接/路径 |
|------|----------|-----------|
| 代码变更 | 待补充 | 待补充 |
| 测试用例 | 待补充 | 待补充 |

### 2. 遗留问题与预防措施

#### 2.1 遗留问题
- **未完全修复**：待补充
- **已知限制**：待补充

#### 2.2 预防措施（同类Bug预防）

| 措施类型 | 具体内容 | 负责人 | 完成时间 |
|---------|----------|--------|---------|
| 代码审查 | 待补充 | 待定 | 待定 |
| 测试增强 | 待补充 | 待定 | 待定 |
| 文档更新 | 待补充 | 待定 | 待定 |
| 监控告警 | 待补充 | 待定 | 待定 |

### 3. 后续建议

1. **监控要点**：待补充
2. **后续优化**：待补充
3. **知识沉淀**：待补充
4. **相关Bug排查**：待补充

---

## 文档归档信息

| 项目 | 内容 |
|------|------|
| 文档最终版本 | V0.1（Pre 文档） |
| 归档日期 | 2026-02-04 |
| 归档路径 | `docs/06_project_management/pr_documents/fix/2026-02-04_PR-038_Daemon_CLI_Debug_全流程.md` |
| 后续维护人 | 🤖 核心开发 + 👤 架构师 |

---

**维护者**: 🤖 AI 团队
**最后更新**: 2026-02-04
**状态**: 📋 Pre 文档 - 待架构师评审
