# PR-033 测试计划 - Host/Node 双层架构

> **文档类型**: 💡 测试计划
> **创建日期**: 2026-01-29
> **状态**: 📋 待评审
> **优先级**: P0（核心功能）
> **关联 PR**: [PR-033](https://github.com/jzhang405/NexKV/pull/33)

---

## 测试目标

验证 Host/Node 双层架构设计的正确性、稳定性和性能，确保：
1. **功能正确性**：Host 和 Node 结构、关联关系、端口分配符合设计
2. **并发安全**：多进程环境下端口分配无冲突
3. **故障恢复**：故障检测和转移机制工作正常
4. **性能达标**：O(1) 访问性能，故障转移延迟 < 5s

---

## 测试范围

### 功能测试
- Host 结构创建、验证、序列化
- Node 结构创建、验证、序列化
- Host-Node 关联关系
- 端口分配算法（MD5 + MVStore）
- 地址组装方法（GetTCPAddr/GetUDPAddr）

### 并发测试
- 多进程端口分配并发安全
- Host/Node 并发访问
- MVStore 并发读写

### 故障测试
- 心跳超时检测
- TCP/UDP 双重探测
- ParentStandby 提升机制
- 防脑裂延迟机制

### 性能测试
- Node 访问性能（O(1) 验证）
- 端口分配性能
- 故障转移延迟
- 元数据同步开销

### 场景测试
- localhost 场景（单机多角色）
- 分布式场景（多机部署）
- 故障注入（网络分区、节点宕机）

---

## 测试用例设计

### 1. 单元测试（Unit Tests）

#### 1.1 Host 结构测试

| 用例ID | 测试场景 | 输入 | 预期输出 | 优先级 |
|--------|----------|------|----------|--------|
| UT-HOST-001 | Host 创建 | `HostID="server-1", Hostname="192.168.1.100", Role=leaf_parent` | Host 结构正确创建 | P0 |
| UT-HOST-002 | HostRole 约束验证 - leaf_only | `Role=leaf_parent, LeafNodeID="", ParentNodeID="node-1"` | 返回错误（LeafNodeID 必备） | P0 |
| UT-HOST-003 | HostRole 约束验证 - leaf_parent | `Role=leaf_parent, LeafNodeID="n1", ParentNodeID="", ParentStandbyNodeID="n3"` | 返回错误（ParentStandby 必须为空） | P0 |
| UT-HOST-004 | Host MsgPack 序列化 | Host 结构 | 序列化后可反序列化 | P0 |
| UT-HOST-005 | Host 字段验证 | `Hostname=""` | 返回错误（Hostname 不能为空） | P1 |

#### 1.2 Node 结构测试

| 用例ID | 测试场景 | 输入 | 预期输出 | 优先级 |
|--------|----------|------|----------|--------|
| UT-NODE-001 | Node 创建 | `NodeID="node-leaf-1", HostID="server-1", Addr={TCP:9000, UDP:9001}` | Node 结构正确创建 | P0 |
| UT-NODE-002 | NodeAddress Validate - TCP 范围 | `TCPPort=1023` | 返回错误（必须 >= 1024） | P0 |
| UT-NODE-003 | NodeAddress Validate - UDP 规则 | `TCPPort=9000, UDPPort=9002` | 返回错误（UDP 必须 = TCP + 1） | P0 |
| UT-NODE-004 | NodeAddress Validate - 正常 | `TCPPort=9000, UDPPort=9001` | 返回 nil | P0 |
| UT-NODE-005 | GetTCPAddr - 有 Host | `Node.host={Hostname="192.168.1.100"}, Addr.TCPPort=9000` | 返回 "192.168.1.100:9000" | P0 |
| UT-NODE-006 | GetTCPAddr - 无 Host | `Node.host=nil, Addr.TCPPort=9000` | 返回 ":9000" | P0 |
| UT-NODE-007 | GetUDPAddr - 有 Host | `Node.host={Hostname="127.0.0.1"}, Addr.UDPPort=9001` | 返回 "127.0.0.1:9001" | P0 |
| UT-NODE-008 | Node MsgPack 序列化 | Node 结构 | 序列化后可反序列化 | P0 |
| UT-NODE-009 | SetHost/GetHost | 设置 Host 引用 | GetHost 返回正确的 Host | P0 |

#### 1.3 端口分配测试

| 用例ID | 测试场景 | 输入 | 预期输出 | 优先级 |
|--------|----------|------|----------|--------|
| UT-PORT-001 | 首次分配 | `hostID="localhost-1"` | 返回 TCP 端口 [9000, 32767]，UDP = TCP + 1 | P0 |
| UT-PORT-002 | 确定性分配 | `hostID="localhost-1"` (两次) | 两次返回相同端口 | P0 |
| UT-PORT-003 | 冲突重试 | `hostID="collision-test"` (强制冲突) | 自动重试，返回新端口 | P0 |
| UT-PORT-004 | 端口释放 | `hostID="localhost-1"` | 分配记录从 MVStore 删除 | P0 |
| UT-PORT-005 | MVStore 持久化 | 分配后重启进程 | 重启后仍能获取相同端口 | P0 |
| UT-PORT-006 | 并发分配 - 多进程 | 10 个进程同时分配不同 host_id | 所有端口无冲突 | P0 |

#### 1.4 故障检测测试

| 用例ID | 测试场景 | 输入 | 预期输出 | 优先级 |
|--------|----------|------|----------|--------|
| UT-FAIL-001 | 心跳超时 - 正常 | LastHeartbeat = now | IsFailed = false | P0 |
| UT-FAIL-002 | 心跳超时 - 超时 | LastHeartbeat = now - 31s | IsFailed = true | P0 |
| UT-FAIL-003 | TCP 探测成功 | 目标可达 | TCPReachable = true, RTT < 5s | P0 |
| UT-FAIL-004 | UDP 探测成功 | 目标可达 | UDPReachable = true | P0 |
| UT-FAIL-005 | 双重探测 - 单协议失败 | TCP 可达，UDP 不可达 | 仍判定为正常（容忍单协议失败） | P0 |
| UT-FAIL-006 | 双重探测 - 双协议失败 | TCP 不可达，UDP 不可达 | 判定为故障 | P0 |
| UT-FAIL-007 | 连续失败计数 | 连续 3 次探测失败 | 触发故障转移 | P0 |
| UT-FAIL-008 | 防脑裂延迟 | 连续失败后延迟 2s 再探测 | 延迟后再次探测确认 | P0 |

---

### 2. 集成测试（Integration Tests）

#### 2.1 HostManager 集成测试

| 用例ID | 测试场景 | 操作步骤 | 预期结果 | 优先级 |
|--------|----------|----------|----------|--------|
| IT-HM-001 | 添加 Host | AddHost(host) | Host 成功添加到 allHosts | P0 |
| IT-HM-002 | 获取 Host | GetHost(hostID) | 返回正确的 Host | P0 |
| IT-HM-003 | 移除 Host | RemoveHost(hostID) | Host 从 allHosts 删除 | P0 |
| IT-HM-004 | HostTopology | GetHostTopology() | 返回完整的 Host 拓扑 | P0 |
| IT-HM-005 | Host-Node 关联 | AddHost -> AddNode -> 验证关联 | Host.NodeID 与 Node.NodeID 一致 | P0 |

#### 2.2 TreeCoordinator 集成测试

| 用例ID | 测试场景 | 操作步骤 | 预期结果 | 优先级 |
|--------|----------|----------|----------|--------|
| IT-TC-001 | 双节点启动 | Start(LeafNode, ParentNode) | 两个节点成功启动 | P0 |
| IT-TC-002 | 心跳发送 | sendHeartbeat() | Leaf 向 Parent 发送心跳 | P0 |
| IT-TC-003 | localhost 场景 | 配置两个 localhost Host | 自动分配不同端口 | P0 |
| IT-TC-004 | 分布式场景 | 配置多个物理机 Host | 根据评分选择最优 Host | P0 |

#### 2.3 动态分配集成测试

| 用例ID | 测试场景 | 操作步骤 | 预期结果 | 优先级 |
|--------|----------|----------|----------|--------|
| IT-DA-001 | Parent 自动分配 | 集群启动时无 Parent | 自动选择最优 Host 创建 Parent | P0 |
| IT-DA-002 | ParentStandby 自动分配 | 启用 HA 模式 | 自动选择次优 Host 创建 ParentStandby | P0 |
| IT-DA-003 | localhost 评分 | 所有 Host hostname 相同 | 仅考虑负载均衡，忽略 CPU/内存 | P0 |
| IT-DA-004 | 分布式评分 | Host hostname 不同 | 综合考虑 CPU、内存、延迟、负载 | P0 |
| IT-DA-005 | 负载均衡触发 | Parent 负载过高 | 触发重新分配（可选功能） | P1 |

#### 2.4 故障转移集成测试

| 用例ID | 测试场景 | 操作步骤 | 预期结果 | 优先级 |
|--------|----------|----------|----------|--------|
| IT-FO-001 | ParentStandby 快速切换 | Parent 故障，有 ParentStandby | ParentStandby 提升为 Parent，切换 < 5s | P0 |
| IT-FO-002 | Parent 重新分配 | Parent 故障，无 ParentStandby | 触发动态分配算法，创建新 Parent | P0 |
| IT-FO-003 | localhost 场景切换 | 同一 Host 内故障 | 快速切换（< 1s，同进程内） | P0 |
| IT-FO-004 | 跨 Host 切换 | 不同 Host 间故障 | 跨进程切换，< 5s | P0 |
| IT-FO-005 | 拓扑变更广播 | 故障转移完成 | 通过 Gossip 广播到所有节点 | P0 |
| IT-FO-006 | 元数据同步 | ParentStandby 提升后 | 新 Parent 元数据同步到集群 | P0 |

---

### 3. 性能测试（Performance Tests）

| 用例ID | 测试场景 | 测试指标 | 目标值 | 优先级 |
|--------|----------|----------|--------|--------|
| PT-PERF-001 | Node 访问性能 | GetHost(hostID) 耗时 | < 1ms | P0 |
| PT-PERF-002 | 端口分配性能 | AllocTCPPort(hostID) 耗时 | < 10ms | P1 |
| PT-PERF-003 | MVStore 读写性能 | Get/Put 操作耗时 | < 5ms | P1 |
| PT-PERF-004 | 故障检测延迟 | Parent 故障到检测完成 | < 22s (优化后) | P0 |
| PT-PERF-005 | 故障转移延迟 | 检测到切换完成 | < 5s | P0 |
| PT-PERF-006 | 元数据同步性能 | 增量同步 100 条变更耗时 | < 100ms | P1 |
| PT-PERF-007 | 并发访问性能 | 100 个协程并发访问 Node | 无数据竞争，性能线性扩展 | P0 |

---

### 4. 混沌测试（Chaos Tests）

| 用例ID | 测试场景 | 故障注入 | 预期结果 | 优先级 |
|--------|----------|----------|----------|--------|
| CT-CHAOS-001 | 网络分区 | Parent 与集群网络隔离 | ParentStandby 提升，分区恢复后自动恢复 | P0 |
| CT-CHAOS-002 | 节点宕机 | Parent 进程 kill | ParentStandby 提升 | P0 |
| CT-CHAOS-003 | 端口冲突 | 强制分配相同端口 | 自动重试，分配新端口 | P0 |
| CT-CHAOS-004 | 并发启动 | 100 个进程同时启动 | 所有端口无冲突 | P1 |
| CT-CHAOS-005 | 心跳抖动 | 间歇性心跳丢失 | 不触发误判（容错 3 次） | P0 |
| CT-CHAOS-006 | MVStore 损坏 | 删除 MVStore 文件 | 进程崩溃或重建 MVStore | P1 |

---

### 5. 场景测试（Scenario Tests）

#### 5.1 localhost 场景（单机多角色）

| 用例ID | 测试场景 | 配置 | 预期结果 | 优先级 |
|--------|----------|------|----------|--------|
| ST-LOCAL-001 | 双 Host 部署 | 2 个 localhost Host，不同 host_id | 自动分配不同端口 | P0 |
| ST-LOCAL-002 | HostID 生成 | 自动生成 localhost-{index} | localhost-1, localhost-2 | P0 |
| ST-LOCAL-003 | 端口隔离 | 相同 hostname(127.0.0.1) | 不同 host_id 分配不同端口 | P0 |
| ST-LOCAL-004 | 同 Host 内切换 | Parent 故障，有 ParentStandby | < 1s 切换（同进程内） | P0 |
| ST-LOCAL-005 | 多角色验证 | leaf_parent_standby | 3 个 Node（Leaf, Parent, ParentStandby） | P0 |

#### 5.2 分布式场景（多机部署）

| 用例ID | 测试场景 | 配置 | 预期结果 | 优先级 |
|--------|----------|------|----------|--------|
| ST-DIST-001 | 多机部署 | 3 台物理机 | Parent 部署在最优机器 | P0 |
| ST-DIST-002 | 评分算法 | CPU/内存/延迟不同 | 选择综合评分最高的 Host | P0 |
| ST-DIST-003 | 跨 Host 故障转移 | Parent 宕机 | ParentStandby 跨机器提升 | P0 |
| ST-DIST-004 | 网络延迟 | Host 间延迟不同 | 延迟低的 Host 优先 | P0 |
| ST-DIST-005 | 资源均衡 | CPU/内存使用率不同 | 负载低的 Host 优先 | P0 |

---

## 测试环境

### localhost 场景
- **机器**：单机（macOS/Linux）
- **配置**：2-4 个 localhost Host
- **网络**：127.0.0.1 loopback

### 分布式场景
- **机器**：3-5 台物理机或虚拟机
- **配置**：每台机器 1 个 Host
- **网络**：真实网络（192.168.1.x）

---

## 测试执行顺序

### 阶段 1：单元测试（必须全部通过）
1. Host 结构测试
2. Node 结构测试
3. 端口分配测试
4. 故障检测测试

### 阶段 2：集成测试（必须全部通过）
1. HostManager 集成测试
2. TreeCoordinator 集成测试
3. 动态分配集成测试
4. 故障转移集成测试

### 阶段 3：性能测试（基准测试）
1. Node 访问性能测试
2. 端口分配性能测试
3. 故障转移延迟测试
4. 元数据同步性能测试

### 阶段 4：混沌测试（可选）
1. 网络分区测试
2. 节点宕机测试
3. 并发启动测试

### 阶段 5：场景测试（必须全部通过）
1. localhost 场景测试
2. 分布式场景测试

---

## 测试通过标准

### 功能正确性
- ✅ 所有单元测试通过（100%）
- ✅ 所有集成测试通过（100%）
- ✅ 所有场景测试通过（100%）

### 并发安全
- ✅ 并发测试无数据竞争（race detector 通过）
- ✅ 多进程端口分配无冲突

### 性能指标
- ✅ Node 访问性能 < 1ms
- ✅ 故障转移延迟 < 5s
- ✅ 故障检测延迟 < 22s（优化后）

### 覆盖率
- ✅ 代码覆盖率 > 80%
- ✅ 核心模块覆盖率 > 90%

---

## 测试工具

- **单元测试**：Go testing + testify
- **并发检测**：go run race detector
- **性能测试**：Go benchmark
- **覆盖率**：go test -cover
- **混沌测试**：手动故障注入 + 自动化脚本

---

## 风险与应对

| 风险 | 应对措施 |
|------|----------|
| 多进程测试环境搭建困难 | 使用 Docker 容器模拟多进程 |
| 网络分区测试不可控 | 使用 iptables 或 tc 模拟网络故障 |
| 性能测试结果不稳定 | 多次运行取平均值，排除环境干扰 |
| MVStore 并发冲突 | 添加详细的并发测试用例 |

---

**维护者**: 👤 架构师 + 🤖 AI 团队
**最后更新**: 2026-01-29
**状态**: 📋 待评审
