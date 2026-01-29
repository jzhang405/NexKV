# 【PR全流程文档】Feature - Cluster Host-Based Architecture Adjustment

> **文档说明**：本文档包含「前置规划」和「后置总结」两部分，记录从需求对齐到开发完成的全流程，一个PR对应一份全流程文档，归档后作为项目追溯依据。

---

## 第一部分：前置部分（开工前必完成，架构师评审通过）

### 1. 基础信息（与分支/PR绑定）

| 项目 | 内容 |
|------|------|
| 工作类型 | 架构调整（非功能开发） |
| PR编号 | PR-033（https://github.com/jzhang405/NexKV/pull/33） |
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
   - 实现 Host 结构，包含 HostID（物理机器标识）、Hostname（物理地址）、Role（部署模式）
   - 实现 Node 结构，包含 HostID（归属物理机器）、Addr（端口信息）、Role（节点角色）
   - Host 通过 NodeID 字符串关联 Node（LeafNodeID、ParentNodeID、ParentStandbyNodeID）
   - NodeAddress 只包含端口（TCPPort、UDPPort），IP 地址从 Host.Hostname 获取
   - 定义 HostRole 枚举：`leaf_only`、`leaf_parent`、`leaf_parent_standby`
   - 定义 NodeRole 枚举：`Leaf`、`Parent`、`ParentStandby`
   - 支持完整实现：Host/Node 结构 + 动态分配算法 + 故障转移机制
2. **localhost 场景支持**：
   - 自动生成 host_id：`localhost-{序号}`（可读性强）
   - 自动分配端口：基于 host_id 哈希计算（避免冲突）
3. **故障检测机制**：
   - 心跳超时 + 主动探测（双重验证，避免误判）
4. **性能目标**：不涉及性能优化，仅架构调整
5. **可用性目标**：完整代码实现 + 单元测试 + 集成测试

#### 2.3 明确边界（不做什么，避免范围蔓延）
- **本次不支持**：
  - 性能优化（仅架构调整）
  - 新增功能特性（仅双层架构重构）
- **本次不优化**：
  - 网络传输层优化
  - 元数据同步机制优化
  - 一致性协议调整
- **本次包含**：
  - ✅ Host/Node 结构定义
  - ✅ HostManager 基础功能
  - ✅ 动态分配算法实现
  - ✅ 故障转移机制实现
  - ✅ 单元测试和集成测试

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
       HostID       string              // 机器唯一标识（逻辑标识，如 "server-1", "localhost-1"）
       Hostname     string              // 物理机器地址（如 "192.168.1.100", "127.0.0.1"）
       Role          HostRole           // 机器部署模式: leaf_only, leaf_parent, leaf_parent_standby

       // 关联的 Node ID（通过 NodeID 字符串关联）
       LeafNodeID          string        // 必备：叶子节点 ID
       ParentNodeID        string        // 可选：父节点 ID
       ParentStandbyNodeID string        // 可选：父备份节点 ID

       // ... 其他字段（状态、心跳、元数据等）
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
   // NodeAddress 只包含端口信息（IP 从 Host.Hostname 获取）
   type NodeAddress struct {
       TCPPort int    // TCP 端口
       UDPPort int    // UDP 端口
   }

   type Node struct {
       NodeID    string       // 节点唯一标识（如 "node-leaf-1", "node-parent-1"）
       HostID    string       // 归属物理机器 ID（引用 Host.HostID）
       Addr       NodeAddress  // 端口信息（IP 从 Host.Hostname 获取）
       Role       NodeRole     // 节点角色: Leaf, Parent, ParentStandby

       // 完整网络地址 = Host.Hostname + Node.Addr
       // TCP: Host.Hostname:Node.Addr.TCPPort
       // UDP: Host.Hostname:Node.Addr.UDPPort

       // ... 其他字段（ParentID、ChildrenIDs、Level、Status 等）
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

9. **Parent/ParentStandby 动态分配算法**：

    ```mermaid
    flowchart TD
        A[集群启动] --> B{是否存在 Parent 节点?}
        B -->|否| C[触发动态分配]
        B -->|是| D[跳过分配<br/>使用现有配置]

        C --> E[扫描所有 Host]
        E --> F[筛选可用 Host<br/>leaf_parent 或 leaf_parent_standby]
        F --> G[评估候选 Host]
        G --> H[选择最优 Host 作为 Parent]
        H --> I[在选中的 Host 上创建 Parent 节点]

        I --> J{是否需要 HA?}
        J -->|是| K[选择次优 Host 作为 ParentStandby]
        J -->|否| L[不创建 ParentStandby]

        K --> M[在选中的 Host 上创建 ParentStandby 节点]
        L --> N[分配完成]
        M --> N

        style C fill:#fff4e6,stroke:#e65100,stroke-width:2px
        style G fill:#e1f5ff,stroke:#01579b,stroke-width:2px
        style M fill:#c8e6c9,stroke:#2e7d32,stroke-width:2px
    ```

    **9.1 分配原则**：

    | 分配项 | 说明 | 策略 |
    |--------|------|------|
    | **Leaf 节点** | 静态配置，直接在 Host 上启动 | 不参与动态分配 |
    | **Parent 节点** | 动态分配，选择最优 Host | 基于资源、负载、网络延迟 |
    | **ParentStandby 节点** | 动态分配，作为 Parent 热备 | 选择次优 Host，确保快速切换 |

    **9.2 动态分配触发时机**：

    ```go
    type AllocationTrigger int
    const (
        AllocationTriggerStartup      AllocationTrigger = iota // 集群启动时
        AllocationTriggerNodeFailure                      // Parent 节点故障时
        AllocationTriggerRebalance                     // 负载不均时（可选）
        AllocationTriggerScaleIn                         // 缩容时
        AllocationTriggerScaleOut                        // 扩容时
    )
    ```

     **9.3 分配算法**：

    **9.3.1 localhost 场景特殊处理**：

    ```go
    // 判断是否为 localhost 场景
    func isLocalhostScenario(hosts []Host) bool {
        if len(hosts) == 0 {
            return false
        }

        // 检查所有 Host 的 hostname 是否相同
        firstHostname := hosts[0].Hostname
        for _, host := range hosts {
            if host.Hostname != firstHostname {
                return false // 存在不同机器，非 localhost 场景
            }
        }

        // 所有 hostname 相同，判断是否为 localhost（127.0.0.1 或 0.0.0.0）
        return isLocalhostAddress(firstHostname)
    }

    func isLocalhostAddress(hostname string) bool {
        return hostname == "127.0.0.1" ||
               hostname == "0.0.0.0" ||
               strings.HasPrefix(hostname, "localhost")
    }

    // localhost 场景下的 Host 评分
    func evaluateHostsForLocalhost(hosts []Host) []HostScore {
        var scores []HostScore

        for _, host := range hosts {
            score := calculateHostScoreForLocalhost(host)
            scores = append(scores, score)
        }

        return scores
    }

    func calculateHostScoreForLocalhost(host Host) HostScore {
        // localhost 场景特点：
        // 1. 网络延迟为 0（同一机器）
        // 2. 评分主要基于已有节点数（负载均衡）
        // 3. 忽略 CPU 和内存（因为是同一进程内的不同角色）

        // 评分原则：已有节点数越少越好（负载均衡）
        loadScore := 1.0 / float64(host.ExistingNodes+1)

        return HostScore{
            HostID:         host.HostID,
            CPUUsage:      0, // localhost 下不计算
            MemUsage:      0, // localhost 下不计算
            NetworkLatency: 0, // localhost 延迟为 0
            ExistingNodes: host.ExistingNodes,
            Score:          loadScore, // 仅考虑负载均衡
        }
    }

    // 分配决策（支持 localhost 场景）
    func AllocateParent(availableHosts []Host) (primary Host, standby Host) {
        // 检查是否为 localhost 场景
        if isLocalhostScenario(availableHosts) {
            return allocateParentForLocalhost(availableHosts)
        }

        // 分布式场景：使用完整评分算法
        return allocateParentForDistributed(availableHosts)
    }

    // localhost 场景分配逻辑
    func allocateParentForLocalhost(hosts []Host) (primary Host, standby Host) {
        scores := evaluateHostsForLocalhost(hosts)

        if len(scores) == 0 {
            return Host{}, Host{}
        }

        // 按已有节点数排序（少的优先）
        sort.Slice(scores, func(i, j int) bool {
            return scores[i].ExistingNodes < scores[j].ExistingNodes
        })

        // 选择节点最少的 Host 作为 Parent
        primary := scores[0]

        var standby Host
        if len(scores) > 1 {
            standby = scores[1]
        }

        return primary, standby
    }

    // 分布式场景分配逻辑
    func allocateParentForDistributed(hosts []Host) (primary Host, standby Host) {
        scores := evaluateHosts(hosts)

        // 排序：评分最高的优先
        sort.Slice(scores, func(i, j int) bool {
            return scores[i].Score > scores[j].Score
        })

        if len(scores) == 0 {
            return Host{}, Host{} // 无可用 Host
        }

        // 最高分作为 Parent
        primary := scores[0]

        // 次高分作为 ParentStandby（如果需要 HA）
        var standby Host
        if len(scores) > 1 {
            standby = scores[1]
        }

        return primary, standby
    }

    // 评分函数（加权综合评估）
    func evaluateHosts(hosts []Host) []HostScore {
        var scores []HostScore

        for _, host := range hosts {
            score := calculateHostScore(host)
            scores = append(scores, score)
        }

        return scores
    }

    func calculateHostScore(host Host) HostScore {
        // 权重配置（可调整）
        cpuWeight := 0.3
        memWeight := 0.3
        latencyWeight := 0.3
        loadWeight := 0.1

        // 归一化评分（越高越好）
        cpuScore := 1.0 - host.CPUUsage      // CPU 使用率越低越好
        memScore := 1.0 - host.MemUsage      // 内存使用率越低越好
        latencyScore := 1000.0 / (host.NetworkLatency + 1) // 延迟越低越好
        loadScore := 1.0 / float64(host.ExistingNodes+1) // 负载越低越好

        totalScore := cpuScore*cpuWeight +
                     memScore*memWeight +
                     latencyScore*latencyWeight +
                     loadScore*loadWeight

        return HostScore{
            HostID:         host.HostID,
            CPUUsage:      host.CPUUsage,
            MemUsage:      host.MemUsage,
            NetworkLatency: host.NetworkLatency,
            ExistingNodes: host.ExistingNodes,
            Score:          totalScore,
        }
    }
    ```

     **9.4 分配流程**：

    **步骤 1：判断部署场景**
    - 检查所有 Host 的 hostname 是否相同
    - **localhost 场景**：所有 hostname 为 `127.0.0.1` 或 `0.0.0.0` 或 `localhost`
    - **分布式场景**：Host hostname 存在差异（如 `192.168.1.100`、`192.168.1.101`）

    **步骤 2：localhost 场景分配流程**
    - 收集所有角色为 `leaf_parent` 或 `leaf_parent_standby` 的 Host
    - 按已有节点数排序（节点数少的 Host 优先）
    - 选择节点最少的 Host 作为 Parent 所在机器
    - 在该 Host 上创建 Parent Node（role = Parent）
    - 如果需要 HA，选择次优 Host 创建 ParentStandby
    - 通过 `host_id` 区分不同"虚拟物理机"

    **步骤 3：分布式场景分配流程**
    - 收集所有角色为 `leaf_parent` 或 `leaf_parent_standby` 的 Host
    - 收集每个 Host 的资源指标（CPU、内存、网络延迟、已有节点数）
    - 使用评分函数计算综合得分（权重：CPU 30%、内存 30%、延迟 30%、负载 10%）
    - 选择评分最高的 Host 作为 Parent 节点所在机器
    - 在该 Host 上创建 Parent Node（role = Parent）
    - 如果配置要求 HA，选择评分次高的 Host
    - 在该 Host 上创建 ParentStandby Node（role = ParentStandby）
    - ParentStandby 处于热备状态，实时同步 Parent 的数据

    **步骤 4：更新集群拓扑**
    - 将新分配的 Parent 和 ParentStandby 节点注册到 TreeCoordinator
    - 通过 Gossip 协议同步节点信息到整个集群
    - 重新构建树形拓扑结构

    **步骤 5：localhost 场景验证**
    - 检查 `host_id` 唯一性：确保通过 `test-host-1`、`test-host-2` 等逻辑区分
    - 检查端口冲突：同一 localhost 不同虚拟机使用不同端口（TCP/UDP）
    - 检查角色分配：确保每个 Host 上有明确的角色（Leaf/Parent/ParentStandby）

    **9.5 故障转移机制**：

    ```mermaid
    flowchart TD
        A[Parent 节点故障] --> B[TreeCoordinator 检测到故障]
        B --> C{是否存在 ParentStandby?}

        C -->|是| D[触发自动故障转移]
        C -->|否| E[触发重新分配]

        D --> F[将 ParentStandby 提升为 Parent]
        F --> G[更新节点 Role: ParentStandby -> Parent]
        G --> H[更新集群拓扑]
        H --> I[通知所有 Leaf 节点]
        I --> J[故障转移完成]

        E --> K[执行动态分配算法]
        K --> L[选择新的 Host 创建 Parent]
        L --> M[选择新的 Host 创建 ParentStandby]
        M --> N[注册到 TreeCoordinator]
        N --> O[通过 Gossip 同步]

        style D fill:#c8e6c9,stroke:#2e7d32,stroke-width:2px
        style F fill:#e1f5ff,stroke:#01579b,stroke-width:2px
        style K fill:#fff4e6,stroke:#e65100,stroke-width:2px
    ```

     **故障转移流程**：

     | 阶段 | 操作 | 说明 |
     |------|------|------|
     | **检测** | TreeCoordinator 通过心跳或 Gossip 检测 Parent 故障 | 超时阈值：30s 无心跳判定故障 |
     | **决策** | 检查是否存在可用的 ParentStandby | 有 ParentStandby → 快速切换；无 → 重新分配 |
     | **切换** | 将 ParentStandby 提升为 Parent，Role 更新为 `Parent` | 切换时间 < 5s |
     | **通知** | 通过 Gossip 协议向所有 Leaf 节点广播拓扑变更 | 确保最终一致性 |

     **9.5.1 localhost 场景故障转移**：

     ```mermaid
     flowchart TD
         A[Parent 故障检测<br/>localhost 场景] --> B{判断场景类型}
         B -->|同一 Host 内故障| C[同一虚拟机内切换<br/>host_id: test-host-1]
         B -->|不同 Host 间故障| D[跨虚拟机切换<br/>host_id: test-host-1 -> test-host-2]

         C --> E[在 test-host-1 上<br/>检查 ParentStandby]
         D --> F[在 test-host-2 上<br/>检查可用性]

         E --> G{有 ParentStandby?}
         F --> H{test-host-2 可用?}

         G -->|是| I[快速切换<br/>ParentStandby -> Parent]
         G -->|否| J[在同一 Host 内<br/>重新分配]

         H --> K[更新节点 Role]
         J --> K

         I --> L[通知 Leaf 节点]
         K --> L

         H -->|是| M[test-host-2 可用]
         H -->|否| N[test-host-2 不可用<br/>触发跨 Host 分配]

         M --> O{有 ParentStandby?}
         N --> P[选择新 Host<br/>创建 Parent+ParentStandby]

         O -->|是| Q[使用 ParentStandby]
         O -->|否| P

         Q --> L
         P --> L

         style C fill:#e1f5ff,stroke:#01579b,stroke-width:2px
         style D fill:#fff4e6,stroke:#e65100,stroke-width:2px
         style P fill:#c8e6c9,stroke:#2e7d32,stroke-width:2px
     ```

     **localhost 故障转移策略**：

     | 故障类型 | 判断条件 | 处理方式 | 切换时间 |
     |----------|----------|---------|---------|
     | **同一 Host 内故障** | Parent 和 ParentStandby 在同一 `host_id` | 检查是否有 ParentStandby，有则快速切换 | < 1s（同进程内） |
     | **跨 Host 故障** | Parent 所在 Host 完全不可用 | 检查其他 Host 可用性，选择新 Host 重新分配 | < 5s（跨进程通信） |
     | **所有 Host 不可用** | 所有 `localhost` Host 都故障 | 触发告警，等待手动恢复或重启 | N/A |

     **localhost 故障转移注意事项**：

     1. **host_id 隔离**：通过 `test-host-1`、`test-host-2` 逻辑区分不同"虚拟物理机"
     2. **端口隔离**：同一 localhost 上的不同 Host 使用不同端口（TCP/UDP 避免冲突）
     3. **进程级切换**：同一 Host 内的 ParentStandby 切换到 Parent 实际是同进程内角色变更
     4. **跨进程通信**：不同 Host 之间的通信通过 `127.0.0.1:port`，需正确处理超时和重试

    **9.6 负载均衡触发（可选）**：

    ```go
    type RebalanceConfig struct {
        Enabled           bool     // 是否启用
        CheckInterval     time.Duration // 检查间隔（如 5 分钟）
        LoadImbalanceThreshold float64 // 负载不均阈值（如 0.3）
    }

    func checkRebalance(cluster *Cluster) {
        if !cluster.RebalanceConfig.Enabled {
            return
        }

        parentLoad := cluster.CalculateParentLoad()
        averageLoad := cluster.CalculateAverageLoad()

        if parentLoad/averageLoad > 1.0+cluster.RebalanceConfig.LoadImbalanceThreshold {
            // Parent 负载过高，触发重新分配
            newHosts := selectOptimalHosts(cluster)
            reallocateParents(newHosts)
        }
    }
    ```

    **9.7 动态分配的优势**：

    1. **资源优化**：根据 Host 的实际资源使用情况选择最优部署位置
    2. **故障自愈**：Parent 故障时自动触发分配，无需人工干预
    3. **负载均衡**：避免单点过载，分散压力到多个 Host
    4. **灵活扩展**：支持动态扩缩容，自动调整拓扑

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

2. **实现顺序（自底向上）**

   **阶段 1：基础结构定义**
   - [ ] 第1步：定义 Host 结构体（`internal/metadata/cluster/host.go`）
     - HostID、Hostname、Role 字段
     - LeafNodeID、ParentNodeID、ParentStandbyNodeID 字段
   - [ ] 第2步：定义 Node 结构体（`internal/metadata/cluster/node.go`）
     - NodeID、HostID、Addr（NodeAddress）、Role 字段
   - [ ] 第3步：定义 NodeAddress 结构体（只包含端口）
   - [ ] 第4步：定义 HostRole 和 NodeRole 枚举
   - [ ] 第5步：编写基础结构单元测试

   **阶段 2：HostManager 实现**
   - [ ] 第6步：创建 HostManager（`internal/metadata/cluster/host_manager.go`）
   - [ ] 第7步：实现 AddHost（添加物理机器）
   - [ ] 第8步：实现 GetHost（获取机器信息）
   - [ ] 第9步：实现 RemoveHost（移除物理机器）
   - [ ] 第10步：实现 GetHostTopology（获取机器拓扑）
   - [ ] 第11步：编写 HostManager 单元测试

   **阶段 3：TreeCoordinator 调整**
   - [ ] 第12步：调整 TreeCoordinator 结构（`internal/metadata/cluster/tree_coordinator.go`）
   - [ ] 第13步：添加 allHosts map 和 localHost 字段
   - [ ] 第14步：调整 NewTreeCoordinator 构造函数
   - [ ] 第15步：调整 Start 方法支持双节点启动
   - [ ] 第16步：调整 sendHeartbeat 方法
   - [ ] 第17步：编写集成测试

   **阶段 4：动态分配算法实现**
   - [ ] 第18步：实现端口分配函数（基于 host_id 哈希）
   - [ ] 第19步：实现 localhost host_id 生成（localhost-{序号}）
   - [ ] 第20步：实现场景判断（localhost vs 分布式）
   - [ ] 第21步：实现 localhost 场景分配逻辑
   - [ ] 第22步：实现分布式场景分配逻辑（评分算法）
   - [ ] 第23步：编写动态分配算法测试

   **阶段 5：故障转移机制实现**
   - [ ] 第24步：实现故障检测（心跳超时 + 主动探测）
   - [ ] 第25步：实现 ParentStandby 提升逻辑
   - [ ] 第26步：实现拓扑变更 Gossip 广播
   - [ ] 第27步：编写故障转移测试

   **阶段 6：质量保证**
   - [ ] 第28步：使用 code-simplifier 优化代码
   - [ ] 第29步：运行本地验证：`make build → make lint → make test → make clean`
   - [ ] 第30步：确保测试覆盖率 > 80%

   **阶段 7：文档同步**
   - [ ] 第31步：编写 Post 文档总结实现情况
   - [ ] 第32步：更新相关设计文档
   - [ ] 第33步：架构师评审 Post 文档
   - [ ] 第34步：推送到 GitHub，等待 CI 通过

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
