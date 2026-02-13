# 【PR全流程文档】Feature - Porcupine 分离测试策略

> **文档说明**：本文档包含「前置规划」部分，记录需求对齐和技术设计，PR 开发前必须通过架构师评审。

---

## 第一部分：前置部分（开工前必完成，架构师评审通过）

### 1. 基础信息（与分支/PR绑定）

| 项目 | 内容 |
|------|------|
| 工作类型 | 新功能开发（Feature） |
| PR编号 | PR-064 |
| 分支名称 | feature/porcupine-separated-test-strategy |
| 工作主题 | 分离 Gossip/Quorum 测试策略 |
| 负责人 | 🤖 核心开发 A |
| 分支创建日期 | 2026-02-13 |
| 计划开工日期 | 2026-02-13 |
| 计划CI通过日期 | 2026-02-17 |
| 关联需求单号 | PR-063 后续：Porcupine 线性一致性验证扩展 |
| 架构师评审状态 | ✅ 通过 |
| 预审批结果 | ✅ 通过 |

### 评审记录

#### 第一轮评审（架构师）

| 问题 | 优先级 | 评审意见 | 处理状态 |
|------|--------|---------|---------|
| P0-1 | HLC 时间戳格式描述有误 | 澄清位运算逻辑 | ✅ 已修复 |
| P0-2 | Merkle Root 获取接口未确认 | 定义 MerkleRootProvider 接口 | ✅ 已修复 |
| P1-1 | ConvergenceError 实现位置未明确 | 确认放在 gossip_checker.go | ✅ 已确认 |
| P1-2 | 可视化支持设计不完整 | 补充具体变更内容 | ✅ 已补充 |
| P1-3 | 缺少网络异常场景风险评估 | 添加 3 个场景 | ✅ 已添加 |
| P2-2 | 依赖关系描述有歧义 | 修正为 PR #64 | ✅ 已修正 |

#### 第二轮评审（架构师）

**结论**: ✅ **批准开始开发**

**开发注意事项**：
1. HLC import 路径使用 `github.com/jzhang405/NexKV/internal/clock`
2. `LogicalCounter()` 返回 `uint16`，需要类型转换为 `int64` 进行位运算
3. 建议优先完成测试用例编写，确保覆盖率 > 80%

### 2. 背景与目标（为什么干）

#### 2.1 背景

**业务场景**：
NexKV 采用分层一致性设计，不同协议保证不同的一致性级别：
- **Gossip 协议**：最终一致性，10 秒内收敛
- **Quorum 机制**：强一致性，多数派确认

**现有问题**：
- ❌ **Gossip/Quorum 模型混淆**：当前 NexKVModel 对所有操作都使用线性一致性验证，但 Gossip 只保证最终一致性
- ❌ **Gossip 测试误报**：Gossip 操作在严格线性一致性验证下会产生误报
- ❌ **缺乏收敛检测**：Gossip 测试使用固定 `time.Sleep(10s)`，不能保证收敛完成

**价值**：
- ✅ **消除误报**：分离测试策略，Gossip 使用收敛性测试，Quorum 使用线性一致性验证
- ✅ **精确检测**：基于 Merkle Tree 实现收敛检测，替代固定等待
- ✅ **可视化调试**：Porcupine 验证失败时生成交互式 HTML 报告

#### 2.2 核心目标（可量化、可验证）

1. **功能目标**：
   - 实现 GossipConvergenceChecker（基于 Merkle Tree）
   - 实现 QuorumLinearizabilityChecker
   - 实现 HLCTimestamp 适配器（48-bit PT + 16-bit C）
   - 实现 Porcupine 可视化支持
   - 更新现有测试用例，使用正确的测试策略

2. **性能目标**：
   - 收敛检测轮询间隔 < 100ms
   - 线性一致性验证 1000 ops < 100ms

3. **测试目标**：
   - Gossip 收敛测试 100% 通过
   - Quorum 线性化测试 100% 通过
   - 测试覆盖率 > 80%

### 3. 技术设计

#### 3.1 架构设计

```
┌─────────────────────────────────────────────────────────────────────┐
│                    分离测试策略架构                                   │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │ Gossip 操作测试                                               │   │
│  │ ├── GossipConvergenceChecker（基于 Merkle Tree）              │   │
│  │ │   ├── WaitForConvergence(ctx, timeout)                     │   │
│  │ │   └── 返回 ConvergenceError（含诊断信息）                    │   │
│  │ └── 验证最终一致性（所有节点 Merkle Root 一致）                │   │
│  └─────────────────────────────────────────────────────────────┘   │
│                                                                     │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │ Quorum 操作测试                                               │   │
│  │ ├── QuorumLinearizabilityChecker                             │   │
│  │ │   ├── 使用 Porcupine 线性一致性验证                          │   │
│  │ │   └── VerifyLinearizabilityWithVis()（可视化支持）          │   │
│  │ └── HLCTimestamp 适配器（48-bit PT + 16-bit C）               │   │
│  └─────────────────────────────────────────────────────────────┘   │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

#### 3.2 核心组件设计

**3.2.1 GossipConvergenceChecker（基于 Merkle Tree）**

**接口设计**（P0-2 修复）：

```go
// MerkleRootProvider Merkle Root 提供者接口
// TreeTopologyCoordinator 已实现此接口（GetMerkleRoot() string）
type MerkleRootProvider interface {
    GetMerkleRoot() string
}

// GossipConvergenceChecker 收敛性检查器
type GossipConvergenceChecker struct {
    providers []MerkleRootProvider  // 节点的 Merkle Root 提供者
    timeout   time.Duration
    interval  time.Duration
}

// NewGossipConvergenceChecker 创建收敛检查器
// 参数: coordinators []*TreeTopologyCoordinator（已实现 MerkleRootProvider）
func NewGossipConvergenceChecker(coordinators []MerkleRootProvider, timeout, interval time.Duration) *GossipConvergenceChecker

// WaitForConvergence 等待所有节点收敛
func (c *GossipConvergenceChecker) WaitForConvergence(ctx context.Context) error

// isConverged 检查所有节点 Merkle Root 是否一致
func (c *GossipConvergenceChecker) isConverged() bool {
    if len(c.providers) == 0 {
        return true
    }
    baseRoot := c.providers[0].GetMerkleRoot()
    for _, p := range c.providers[1:] {
        if baseRoot != p.GetMerkleRoot() {
            return false
        }
    }
    return true
}
```

**ConvergenceError 诊断信息**（P1-1 确认：放在 gossip_checker.go）：

```go
// ConvergenceError 收敛失败诊断信息
type ConvergenceError struct {
    Timeout        time.Duration
    NodeRoots      map[string]string  // nodeID -> Merkle Root 快照
    DivergentNodes []string           // 未收敛的节点 ID
}

func (e *ConvergenceError) Error() string {
    return fmt.Sprintf("convergence timeout after %v, divergent nodes: %v",
        e.Timeout, e.DivergentNodes)
}
```

**3.2.2 QuorumLinearizabilityChecker**

```go
// QuorumLinearizabilityChecker Quorum 线性一致性检查器
type QuorumLinearizabilityChecker struct {
    checker   *ConsistencyChecker
    timestamp *HLCTimestamp
}

// VerifyLinearizabilityWithVis 带可视化的线性化验证
func (c *QuorumLinearizabilityChecker) VerifyLinearizabilityWithVis() (*CheckResult, string)
```

**3.2.3 HLCTimestamp 适配器**（P0-1 澄清）

**时间戳格式说明**：

```
int64 时间戳格式（64-bit）：
┌─────────────────────────────────────────────────────────────────────┐
│  高 48 位：物理时间（毫秒）  │  低 16 位：逻辑计数器              │
│  PhysicalTime() << 16       │  LogicalCounter()                  │
└─────────────────────────────────────────────────────────────────────┘

验证：
- 当前毫秒时间戳 ≈ 1.77 × 10^12（约 42 位）
- 左移 16 位后 ≈ 1.16 × 10^17，仍在 int64 范围内（9.22 × 10^18）
- 不会溢出
```

```go
// HLCTimestamp 适配 Porcupine 的时间戳接口
type HLCTimestamp struct {
    hlc *clock.HLC
}

// Now 返回 int64 时间戳
// 格式: 高 48 位 = 物理时间（毫秒），低 16 位 = 逻辑计数器
// 验证：当前时间戳左移 16 位后不会溢出 int64
func (t *HLCTimestamp) Now() int64 {
    hlc := t.hlc.Now()
    // 使用完整 int64 物理时间（毫秒级），左移 16 位
    // 加上 16 位逻辑计数器，总共 64 位
    return (hlc.PhysicalTime() << 16) | int64(hlc.LogicalCounter())
}
```

**3.2.4 Porcupine 可视化支持**（P1-2 补充）

**变更说明**：
- 现有 `checker.go` 已有 `generateReport` 方法，但仅返回 JSON
- **新增** `CheckWithVisualization` 方法，失败时生成交互式 HTML
- **新增** `VerifyLinearizabilityWithVis` 方法，集成到 `RecordingE2ETestScenario`

```go
// === checker.go 新增方法 ===

// CheckWithVisualization 带可视化的一致性检查
// 返回: (是否通过, 可视化文件路径)
func (c *ConsistencyChecker) CheckWithVisualization(history []porcupine.Operation) (bool, string) {
    result := porcupine.CheckOperations(c.model, history, c.timeout)
    if result.Ok {
        return true, ""
    }

    // 生成可视化 HTML 文件
    visPath := filepath.Join(os.TempDir(), fmt.Sprintf("porcupine-violation-%d.html", time.Now().Unix()))
    if err := porcupine.Visualize(c.model, history, visPath); err != nil {
        return false, fmt.Sprintf("visualization error: %v", err)
    }
    return false, visPath
}

// === scenario_adapter.go 新增方法 ===

// VerifyLinearizabilityWithVis 带可视化的线性化验证
func (s *RecordingE2ETestScenario) VerifyLinearizabilityWithVis() (*CheckResult, string) {
    var allOps []porcupine.Operation
    for _, recorder := range s.Recorders {
        allOps = append(allOps, recorder.GetOperations()...)
    }

    if len(allOps) == 0 {
        return &CheckResult{Ok: true}, ""
    }

    ok, visPath := s.Checker.CheckWithVisualization(allOps)
    if ok {
        return &CheckResult{Ok: true}, ""
    }

    return &CheckResult{
        Ok:    false,
        Error: fmt.Sprintf("Linearizability violation. Visualization: %s", visPath),
    }, visPath
}
```

**CI 集成**：
- 测试失败时，HTML 文件上传为 GitHub Actions artifact
- 使用 `actions/upload-artifact@v4` 上传 `/tmp/porcupine-*.html`

#### 3.3 文件变更计划

| 文件路径 | 操作 | 说明 |
|---------|------|------|
| `internal/metadata/consistency/porcupine/hlc_timestamp.go` | 新增 | HLC 时间戳适配器 |
| `internal/metadata/consistency/porcupine/hlc_timestamp_test.go` | 新增 | HLC 时间戳测试 |
| `internal/metadata/consistency/porcupine/gossip_checker.go` | 新增 | Gossip 收敛检查器 |
| `internal/metadata/consistency/porcupine/gossip_checker_test.go` | 新增 | 收敛检查器测试 |
| `internal/metadata/consistency/porcupine/quorum_checker.go` | 新增 | Quorum 线性化检查器 |
| `internal/metadata/consistency/porcupine/quorum_checker_test.go` | 新增 | 线性化检查器测试 |
| `internal/metadata/consistency/porcupine/checker.go` | 修改 | 添加可视化支持 |
| `internal/metadata/consistency/porcupine/scenario_adapter.go` | 修改 | 集成新检查器 |
| `internal/metadata/consistency/porcupine/linearizability_test.go` | 修改 | 更新测试用例 |

### 4. 实施计划

| 里程碑 | 任务 | 预计时间 | 交付物 |
|--------|------|---------|--------|
| M1 | HLCTimestamp 适配器 | 0.5 天 | hlc_timestamp.go + 测试 |
| M2 | GossipConvergenceChecker | 1 天 | gossip_checker.go + 测试 |
| M3 | QuorumLinearizabilityChecker + 可视化 | 1 天 | quorum_checker.go + checker.go 更新 |
| M4 | 测试用例更新 + 集成 | 1 天 | linearizability_test.go 更新 |
| M5 | CI 验证 + 文档 | 0.5 天 | Post 文档 |

**总计**：3-4 天

### 5. 风险评估（P1-3 补充）

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| Merkle Root 获取接口不兼容 | 低 | 10% | 已确认 TreeTopologyCoordinator.GetMerkleRoot() 存在 |
| HLC 时间戳溢出 | 低 | 5% | PR #64 已修复 nil 检查和溢出问题 |
| Porcupine 可视化在 CI 中无法展示 | 低 | 30% | 上传 HTML 文件为 artifact |
| 测试覆盖率不达标 | 中 | 20% | 优先编写测试用例 |
| **Gossip 消息丢失导致无法收敛** | 中 | 30% | 设置合理超时，返回 ConvergenceError 诊断信息 |
| **网络分区期间收敛检测** | 低 | 20% | 超时后返回错误，测试可跳过或重试 |
| **节点动态加入/退出** | 低 | 15% | 当前测试场景不涉及动态成员变更 |

### 6. 依赖关系（P2-2 修复）

```
PR-064（本 PR：分离 Gossip/Quorum 测试策略）
├── 依赖: PR #64 HLC Bugfix ✅ 已合并到 main
└── 阻塞: PR-065（故障注入测试框架，需要本 PR 的测试策略分离）
```

### 7. 验收标准

| 验收项 | 标准 |
|--------|------|
| 功能完整性 | 所有组件实现完成 |
| 测试通过率 | 100% |
| 测试覆盖率 | > 80% |
| Lint 检查 | 0 issues |
| 竞态检测 | 无竞态 |
| CI 通过 | 所有平台（ubuntu/macos/windows）× Go 版本（1.21/1.22/1.23） |

---

## 第二部分：流程节点记录（开发/CI过程追溯）

### 1. 开发过程记录

| 节点 | 完成日期 | 具体内容 | 交付物 |
|------|----------|----------|--------|
| Pre 文档编写 | 2026-02-13 | 本文档 | Pre 文档 |
| 架构师评审 | - | - | - |

### 2. 提交记录

（开发过程中填写）

---

## 文档信息

| 项目 | 内容 |
|------|------|
| 文档版本 | V1.0-Pre |
| 创建日期 | 2026-02-13 |
| 最后更新 | 2026-02-13 |
| 归档路径 | `docs/06_PM/feature/2026-02-13_PR-064_Porcupine-Separated-Test-Strategy_Pre.md` |
| 维护人 | 🤖 核心开发 A |
