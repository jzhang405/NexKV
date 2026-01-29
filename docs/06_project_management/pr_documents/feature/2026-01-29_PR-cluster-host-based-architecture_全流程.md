# 【PR全流程文档】Feature - Cluster Host-Based Architecture Adjustment

> **文档说明**：本文档包含「前置规划」和「后置总结」两部分，记录从需求对齐到开发完成的全流程，一个PR对应一份全流程文档，归档后作为项目追溯依据。

---

## 第一部分：前置部分（开工前必完成，架构师评审通过）

### 1. 基础信息（与分支/PR绑定）

| 项目 | 内容 |
|------|------|
| 工作类型 | 架构调整（非功能开发） |
| PR编号 | PR-XXX（创建GitHub PR后补充完整） |
| 分支名称 | feature/cluster-host-based-architecture |
| 工作主题 | TreeCoordinator 从"节点=物理机器"模型调整为"物理机器层+逻辑节点层"的双层架构 |
| 负责人 | 架构师 + AI 团队 |
| 分支创建日期 | 2026-01-29 |
| 计划开工日期 | 待架构师评审后确定 |
| 计划CI通过日期 | 待定 |
| 关联需求单号 | 无（架构调整） |
| 架构师评审状态 | □ 待评审 □ 评审中 □ 评审通过 □ 需优化（循环记录） |
| 预审批结果 | □ 未通过 □ 已通过（架构师签字/备注：XXX 202X-XX-XX 同意开工） |

---

### 2. 背景与目标（为什么干）

#### 2.1 背景
- **业务场景**：NexKV 需要支持单机多角色部署（一台物理机器运行多个逻辑节点）和高可用（HA）场景（Parent Node 热备）
- **现有问题**：当前 TreeCoordinator 采用"节点=物理机器"的简化模型，每个节点对应一个物理 IP 和端口，无法支持：
  1. 单机多角色：一台机器无法运行多个逻辑节点（leaf + parent）
  2. HA 高可用：无法实现 Parent Node 的热备（standby）机制
  3. 灵活性不足：物理机器与逻辑节点强耦合，扩展受限
- **价值**：采用"物理机器层+逻辑节点层"的双层架构，实现：
  1. 单机多角色：支持一台物理机器运行多个逻辑节点
  2. HA 高可用：支持 Parent Node 的热备（standby）机制
  3. 灵活扩展：物理机器与逻辑节点解耦，支持动态扩缩容
  4. 清晰模型：Host 层管理物理机器，Node 层管理逻辑节点，职责明确

#### 2.2 核心目标（可量化、可验证）
1. **功能目标**：
   - 实现 Host 结构，包含 HostID（物理机器标识）、hostname（物理地址）、Role（部署模式）
   - 实现 Node 结构，包含 HostID（归属物理机器）、NodeAddress（网络地址）、Role（节点角色）
   - 定义 HostRole 枚举：`leaf_only`、`leaf_parent`、`leaf_parent_standby`
   - 定义 NodeRole 枚举：`Leaf`、`Parent`、`ParentStandby`
   - 支持 NodeAddress 类型，包含 IP、TCPPort、UDPPort，支持 IPFS 格式转换
2. **性能目标**：不涉及性能优化，仅架构调整
3. **可用性目标**：不涉及代码实现，仅文档和设计评审

#### 2.3 明确边界（不做什么，避免范围蔓延）
- **本次不支持**：
  - 代码实现（仅文档和设计评审）
  - 性能优化
  - 新增功能特性
  - 单元测试编写
- **本次不优化**：
  - 网络传输层优化
  - 元数据同步机制优化
  - 一致性协议调整

---

### 3. 实现方案（怎么干，核心设计）

#### 3.1 整体架构设计

```mermaid
flowchart TD
    subgraph HostLayer["物理机器层 (Host Layer)"]
        H1[Host-1<br/>host_id: server-1<br/>hostname: 192.168.1.100<br/>role: leaf_parent]
        H2[Host-2<br/>host_id: server-2<br/>hostname: 192.168.1.101<br/>role: leaf_parent_standby]
    end

    subgraph NodeLayer["逻辑节点层 (Node Layer)"]
        N1[Node-1<br/>host_id: server-1<br/>role: Leaf<br/>tcp: /ip4/192.168.1.100/tcp/5001<br/>udp: /ip4/192.168.1.100/udp/5002]
        N2[Node-2<br/>host_id: server-1<br/>role: Parent<br/>tcp: /ip4/192.168.1.100/tcp/6001<br/>udp: /ip4/192.168.1.100/udp/6002]
        N3[Node-3<br/>host_id: server-2<br/>role: Parent<br/>tcp: /ip4/192.168.1.101/tcp/6001<br/>udp: /ip4/192.168.1.101/udp/6002]
        N4[Node-4<br/>host_id: server-2<br/>role: ParentStandby<br/>tcp: /ip4/192.168.1.101/tcp/6001<br/>udp: /ip4/192.168.1.101/udp/6002]
    end

    H1 --> N1
    H1 --> N2
    H2 --> N3
    H2 --> N4

    style HostLayer fill:#e1f5ff,stroke:#01579b,stroke-width:2px
    style NodeLayer fill:#f3e5f5,stroke:#4a148c,stroke-width:2px
    ```

#### 3.2 关键设计点

1. **Host 结构定义**：
   ```go
   type Host struct {
       HostID       string              // 机器唯一标识（逻辑标识，如 "server-1", "test-host-1"）
       Hostname     string              // 物理机器地址（如 "192.168.1.100", "127.0.0.1"）
       Role          HostRole           // 机器部署模式: leaf_only, leaf_parent, leaf_parent_standby
       // ... 其他字段保持不变
   }

   type HostRole string
   const (
       HostRoleLeafOnly       HostRole = "leaf_only"           // 仅运行 Leaf 节点
       HostRoleLeafParent     HostRole = "leaf_parent"         // 同时运行 Leaf + Parent
       HostRoleLeafParentStandby HostRole = "leaf_parent_standby" // Leaf + Parent 热备
   )
   ```

2. **Node 结构定义**：
   ```go
   type NodeAddress struct {
       IPAddress string  // IP 地址
       TCPPort   int    // TCP 端口
       UDPPort   int    // UDP 端口
   }

   func (na *NodeAddress) TCPAddr() string {
       return fmt.Sprintf("/ip4/%s/tcp/%d", na.IPAddress, na.TCPPort)
   }

   func (na *NodeAddress) UDPAddr() string {
       return fmt.Sprintf("/ip4/%s/udp/%d", na.IPAddress, na.UDPPort)
   }

   type Node struct {
       HostID    string       // 归属物理机器 ID（引用 Host.HostID）
       Addr       NodeAddress  // 网络地址（强类型，包含 IP 和端口）
       Role       NodeRole     // 节点角色: Leaf, Parent, ParentStandby
       // ... 其他字段保持不变
   }

   type NodeRole int
   const (
       NodeRoleLeaf         NodeRole = iota // 叶子节点：负责数据存储
       NodeRoleParent                           // 父节点：负责数据转发和路由
       NodeRoleParentStandby                    // 父节点备节点：Parent Node 的热备（HA 模式）
   )
   ```

3. **NodeID 格式设计**：
   - **格式**：`node-{role}-{index}`（如 `node-leaf-1`、`node-parent-1`）
   - **前缀**：`node-`（明确是节点标识，而非机器标识）
   - **作用域**：全局唯一标识一个逻辑节点

4. **HostID 格式设计**：
   - **格式**：使用 hostname 前缀（如 `server-1`、`server-2`、`test-host-1`）
   - **类型**：string（便于配置文件读取和日志输出）
   - **作用域**：标识一台物理机器

5. **host_id 与 hostname 的分离**：
   - **host_id**：逻辑标识符，用于区分部署单元（如 `test-host-1`、`server-1`）
   - **hostname**：物理地址，用于实际网络通信（如 `127.0.0.1`、`192.168.1.100`）
   - **localhost 场景**：通过 `host_id` 逻辑区分不同"虚拟物理机"

6. **配置文件格式**：
   ```yaml
   hosts:
     - host_id: "server-1"       # 逻辑标识符
       hostname: "192.168.1.100"  # 物理地址
       role: "leaf_parent"         # 部署模式
     - host_id: "server-2"       # 逻辑标识符
       hostname: "192.168.1.101"  # 物理地址
       role: "leaf_parent_standby"  # HA 模式（热备）
   ```

7. **localhost 场景解决方案**：
   ```yaml
   hosts:
     - host_id: "test-host-1"      # 逻辑标识符 1
       hostname: "127.0.0.1"
       role: "leaf_parent"
     - host_id: "test-host-2"      # 逻辑标识符 2
       hostname: "127.0.0.1"
       role: "leaf_parent_standby"
   ```
   - 通过 `host_id` 逻辑区分，支持单机多角色测试

8. **核心机制**：
   - **Host 层**：管理物理机器信息（hostname、部署模式、节点列表指针）
   - **Node 层**：管理逻辑节点信息（网络地址、角色、归属 HostID）
   - **地址信息下沉**：Host 不再包含 IP 和端口，这些信息存储在 Node 层
   - **类型安全**：使用 `NodeAddress` 结构而非 `string`，避免类型错误

---

### 4. 风险评估与应对措施

| 风险点 | 影响等级 | 应对措施 |
|--------|-----------|----------|
| HostID 与 NodeID 混淆 | 中 | 明确命名规范：HostID 用 hostname 前缀（server-*），NodeID 用 node-* 前缀 |
| 地址类型转换错误 | 中 | 提供 `TCPAddr()` 和 `UDPAddr()` 方法，统一 IPFS 格式输出 |
| localhost 场景配置错误 | 中 | 文档明确 localhost 场景的配置示例，通过 host_id 逻辑区分 |
| Role 类型定义不清晰 | 低 | 枚举定义文档化，配置文件添加注释说明 |
| NodeAddress 字段缺失（IP、Port） | 中 | 提供 `ParseNodeAddress` 函数，支持 "IP:Port" 和 "IP:TCPPort:UDPPort" 格式解析 |

---

### 5. 架构师评审记录（循环优化，直至通过）

| 评审轮次 | 评审日期 | 评审人（架构师） | 核心评审意见 | 优化措施（含AI辅助修改） | 优化结果 |
|----------|----------|------------------|--------------|--------------------------|----------|
| 第1轮 | 待定 | 待定 | 待评审 | 待定 | 待定 |

---

### 6. 预审批确认

> **架构师签字/备注**：XXX 202X-XX-XX 该架构调整方案可行，模型清晰，风险可控，同意继续推进设计完善。

---

### 7. 下一步行动（从文档讨论到 Coding）

> **核心原则**：Pre 文档评审通过后，按照本设计文档进行代码实现，严格遵循以下流程：

1. **进入开发阶段**
   - 在 feature 分支上实现设计文档中定义的数据结构和接口
   - 优先实现 Host 和 Node 核心结构
   - 遵循编码规范（`docs/03_development/01_编码规范文档.md`）

2. **实现顺序**
   - 第1步：实现 Host 和 Node 基础结构
   - 第2步：实现 HostRole 和 NodeRole 枚举
   - 第3步：实现 NodeAddress 结构及其方法
   - 第4步：适配 TreeCoordinator 使用新的 Host/Node 模型
   - 第5步：编写单元测试验证新模型

3. **质量保证**
   - 代码编写完成后，使用 code-simplifier 进行代码优化
   - 运行完整的本地验证流程：`make build → make lint → make test → make clean`
   - 确保 LSP 诊断无错误

4. **文档同步**
   - 开发完成后，编写 Post 文档总结实现情况
   - Post 文档通过架构师评审后，才能推送到 GitHub

---

## 第二部分：流程节点记录（开发/CI过程追溯）

### 1. 开发过程记录

| 节点 | 完成日期 | 具体内容 | 交付物 |
|------|----------|----------|--------|
| 启动开发 | 待定 | 待定 | 待定 |
| 本地测试 | 待定 | 待定 | 待定 |
| Post文档编写 | 待定 | 待定 | 待定 |
| 架构师Post批准 | 待定 | 待定 | 待定 |
| 提交GitHub | 待定 | 待定 | 待定 |

---

### 2. CI流程记录（修复Bug直至通过）

| CI轮次 | 触发时间 | 结果 | 问题详情 | 修复措施 | 修复结果 |
|--------|----------|------|----------|----------|----------|
| 第1轮 | 待定 | 待定 | 待定 | 待定 | 待定 |

---

### 3. 合并记录

| 合并时间 | 合并方式 | 审批人 | 备注 |
|----------|----------|--------|------|
| 待定 | 待定 | 待定 | 待定 |

---

## 第三部分：后置部分（CI通过后编写，总结/成果/ToDo）

### 1. 核心成果总结（开发了啥，结果怎样）

#### 1.1 功能成果
- **已完成**：待定
- **与Pre文档差异**：待定

#### 1.2 性能/数据成果
- **性能数据**：不涉及
- **测试成果**：不涉及

#### 1.3 代码/文档交付物

| 类型 | 具体内容 | 链接/路径 |
|------|----------|-----------|
| 代码变更 | 待定 | 待定 |
| 文档更新 | 待定 | 待定 |

---

### 2. 未完成项与ToDo清单（有哪些没干，后续规划）

#### 2.1 本次PR未完成项
- **未支持**：待定
- **遗留问题**：待定

#### 2.2 ToDo清单（优先级排序）

| 优先级 | 任务内容 | 预估工期 | 关联PR/需求 | 备注 |
|--------|----------|----------|-------------|------|
| 待定 | 待定 | 待定 | 待定 | 待定 |

---

### 3. 下一步工作建议（建议干啥）

1. **优先推进**：待定
2. **监控要点**：待定
3. **运维补充**：待定
4. **后续规划**：待定
5. **反馈收集**：待定

---

## 文档归档信息

| 项目 | 内容 |
|------|------|
| 文档最终版本 | v1.0 |
| 归档日期 | 202X-XX-XX |
| 归档路径 | `docs/06_project_management/pr_documents/feature/2026-01-29_PR-XXX_cluster-host-based-architecture_全流程.md` |
| 后续维护人 | 架构师 + AI 团队 |
