# Porcupine 扩展验证预研报告

> **预研分支**: `spike/porcupine-extended-verification`
> **创建日期**: 2026-02-13
> **预研目标**: 分析 PR-063 后续工作的技术可行性和实施方案
> **文档版本**: V1.3

---

## 评审记录

### 第一轮评审（架构师）

| 序号 | 评审意见 | 处理状态 |
|------|---------|---------|
| 1 | 时钟使用 `internal/clock` 的 HLC | ✅ 已采纳 |
| 2 | 采用方案 A：分离测试策略 | ✅ 已确认 |

### 第二轮评审（架构师 + 分布式系统专家）

| 问题 | 优先级 | 评审意见 | 处理状态 |
|------|--------|---------|---------|
| HLC 集成方案缺陷 | P0 | 时间戳格式转换、nil 检查、溢出处理 | ✅ 已修复 |
| 2PC 工作量估算 | P0 | 7-10 天过于乐观 | ✅ 调整为 12-15 天 |
| Gossip 收敛检测 | P1 | 固定等待改为版本检测 | ✅ 已修复 |
| PR 依赖关系 | P1 | 补充依赖关系图 | ✅ 已添加 |
| 协调者故障场景 | P1 | 故障测试需补充 | ✅ 已添加 |

### 第三轮评审（架构师 + 分布式系统专家）

#### 架构师审查意见

| 问题 | 优先级 | 评审意见 | 处理状态 |
|------|--------|---------|---------|
| P0-1 | HLC.Update() nil 指针风险 | 需创建独立 bugfix PR | ✅ 已记录 |
| P0-2 | HLC 逻辑计数器溢出 | 65535→0 回绕 | ✅ 已记录 |
| P0-3 | TLA+ 规约计划未明确 | 需明确负责人和验收标准 | ✅ 已确认架构师负责 |
| P1-1 | HLC 时间戳 32-bit 精度不足 | 49 天限制 | ✅ 改为 48-bit |
| P1-2 | 收敛检测缺少诊断信息 | 需返回 ConvergenceError | ✅ 已添加 |
| P1-3 | Gossip failpoint 覆盖不完整 | 缺少 parse-error 等 | ✅ 已补充 |
| P1-4 | PR-065 工作量偏乐观 | 5 天 → 7-9 天 | ✅ 已调整 |
| P1-5 | 缺少 Porcupine 可视化 | 需添加 Visualize 支持 | ✅ 已添加 |

#### 分布式系统专家审查意见

| 问题 | 优先级 | 评审意见 | 处理状态 |
|------|--------|---------|---------|
| P0-1 | VersionVector 不存在 | 文档假设了不存在的接口 | ✅ 改用 Merkle Tree |
| P0-2 | HLC 时间戳 32-bit 精度不足 | 49 天回绕 | ✅ 改为 48-bit |
| P1-1 | 2PC 状态模型扩展不完整 | 需补充 TxState 结构 | ✅ 已补充 |
| P1-2 | failpoint 并行测试存在风险 | 不建议并行执行 | ✅ 已明确 |
| P1-3 | 协调者故障场景不完整 | 需补充 5 种故障时机 | ✅ 已添加 |

#### 用户决策确认

| 决策项 | 选择 | 说明 |
|--------|------|------|
| Gossip 收敛检测 | **方案 A：Merkle Tree** | 复用现有实现，无需新增版本向量 |
| HLC 时间戳格式 | **48-bit PT + 16-bit C** | 无 49 天限制，覆盖约 8900 年 |
| TLA+ 规约负责人 | **架构师/分布式系统 Agent** | 与 PR-064/065/066 并行 |

**评审结论**: ✅ 通过（V1.3 版本）

---

## 1. 预研背景

### 1.1 当前状态

PR-063 已完成 Porcupine 线性一致性验证框架的基础集成：

| 完成项 | 状态 |
|--------|------|
| NexKVModel 状态模型 | ✅ |
| HistoryRecorder 事件记录器 | ✅ |
| ConsistencyChecker 检查器 | ✅ |
| RecordingClient 记录客户端 | ✅ |
| 测试覆盖率 88.2% | ✅ |

### 1.2 待解决问题

根据架构师和分布式系统专家评审，存在以下问题：

| 问题 | 优先级 | 说明 |
|------|--------|------|
| **Gossip/Quorum 模型混淆** | P1 | 两种协议共享线性一致性模型，Gossip 操作可能产生误报 |
| **缺乏故障场景测试** | P1 | 节点故障、网络分区、协调者故障等场景未覆盖 |
| **2PC 验证未实现** | P2 | 跨分片事务的一致性验证 |
| **真实 E2E 测试** | P2 | 当前使用 mock，未在真实网络环境验证 |

---

## 2. 时钟方案：使用 HLC

### 2.1 现有 HLC 实现

NexKV 已有 `internal/clock/hlc.go` 实现了完整的 HLC（混合逻辑时钟）：

```go
// HLC 结构: 48-bit 物理时间 + 16-bit 逻辑计数
type HLC struct {
    pt int64  // 物理时间（毫秒）
    c  uint16 // 逻辑计数（0-65535）
}

// 核心方法
func (h *HLC) Now() *HLC                    // 获取当前时间
func (h *HLC) Update(eventTime, remoteHLC)  // 更新时间（核心算法）
func (h *HLC) Compare(other *HLC) int       // 比较
func (h *HLC) MarshalBinary() ([]byte, error) // 序列化
```

### 2.2 HLC 现有问题及修复（P0）

分布式系统专家评审发现 `internal/clock/hlc.go` 存在以下问题：

**问题 1: 缺少 nil 检查**

```go
// 原代码 - 危险！
func (h *HLC) Update(eventTime int64, remoteHLC *HLC) *HLC {
    newPT := maxInt64(now, h.pt, eventTime, remoteHLC.pt)  // remoteHLC 可能为 nil
}

// 修复后
func (h *HLC) Update(eventTime int64, remoteHLC *HLC) *HLC {
    remotePT := int64(0)
    remoteC := uint16(0)
    if remoteHLC != nil {
        remotePT = remoteHLC.pt
        remoteC = remoteHLC.c
    }
    newPT := maxInt64(now, h.pt, eventTime, remotePT)
    // ...
}
```

**问题 2: 逻辑计数器溢出未处理**

```go
// 原代码 - 可能溢出！
h.c = maxUint16(h.c, remoteC) + 1  // 65535 + 1 = 0

// 修复后
if newPT == h.pt && newPT == remotePT {
    newC := maxUint16(h.c, remoteC) + 1
    if newC == 0 { // 溢出检测
        newPT++    // 推进物理时间
        newC = 0
    }
    h.c = newC
}
```

### 2.3 HLC 与 Porcupine 集成方案

**时间戳格式设计**（V1.3 更新）：

使用 48-bit 物理时间 + 16-bit 逻辑计数，避免 49 天限制：

```go
import "github.com/jzhang405/NexKV/internal/clock"

// HLCTimestamp 适配 Porcupine 的时间戳接口
// V1.3 更新：使用 48-bit PT 避免时间戳回绕问题
type HLCTimestamp struct {
    hlc *clock.HLC
}

func NewHLCTimestamp() *HLCTimestamp {
    return &HLCTimestamp{
        hlc: clock.NewHLC(),
    }
}

// Now 返回 int64 时间戳
// 格式: PT (48-bit) | C (16-bit)
// PT 使用完整 48-bit（约 8900 年毫秒），C 用于同毫秒事件排序
func (t *HLCTimestamp) Now() int64 {
    hlc := t.hlc.Now()
    // 不截断物理时间，使用低 16 位逻辑计数
    // 48-bit PT + 16-bit C = 64-bit，正好填满 int64
    return (hlc.PhysicalTime() << 16) | int64(hlc.LogicalCounter())
}

// Compare 比较两个时间戳
func (t *HLCTimestamp) Compare(other int64) int {
    now := t.Now()
    if now < other {
        return -1
    } else if now > other {
        return 1
    }
    return 0
}
```

**格式说明**：

| 字段 | 位宽 | 范围 | 说明 |
|------|------|------|------|
| PT（物理时间） | 48-bit | 约 8900 年 | 毫秒级 Unix 时间戳 |
| C（逻辑计数） | 16-bit | 0-65535 | 同毫秒内的事件排序 |

**对比原方案**：

| 方案 | PT 位宽 | 有效时间 | 问题 |
|------|---------|---------|------|
| ~~原方案~~ | ~~32-bit~~ | ~~约 49 天~~ | ~~时间戳周期回绕~~ |
| **新方案** | **48-bit** | **约 8900 年** | **无回绕问题** |

### 2.4 HLC 优势

| 特性 | LogicalTimestamp | HLC |
|------|-----------------|-----|
| 物理时间关联 | ❌ 无 | ✅ 有 |
| 跨节点同步 | ⚠️ clientID 区分 | ✅ Update 算法 |
| 时钟回拨处理 | ❌ 无 | ✅ 自动处理 |
| 序列化支持 | ⚠️ 自定义 | ✅ 原生支持 |
| 溢出处理 | ❌ 无 | ✅ 需修复 |
| 分布式友好 | ⭐⭐⭐ | ⭐⭐⭐⭐⭐ |

---

## 3. 分层一致性验证方案

### 3.1 问题分析

NexKV 采用**分层一致性**设计：

```
┌─────────────────────────────────────────────────┐
│ Layer 3: 2PC 协议（强一致）                     │
│   - 全员 commit/rollback                        │
│   - 线性一致性验证适用 ✅                       │
├─────────────────────────────────────────────────┤
│ Layer 2: Quorum 机制（增强最终一致）            │
│   - 多数派确认                                  │
│   - 线性一致性验证适用 ✅                       │
├─────────────────────────────────────────────────┤
│ Layer 1: Gossip 协议（最终一致）                │
│   - 10s 内收敛                                  │
│   - 线性一致性验证不适用 ❌                     │
└─────────────────────────────────────────────────┘
```

**核心问题**：当前 NexKVModel 对所有操作都使用线性一致性验证，但 Gossip 协议只保证最终一致性。

### 3.2 解决方案

**方案 A：分离测试策略（架构师确认 ✅）**

```go
// Quorum 操作：使用 Porcupine 线性一致性验证
func TestQuorumLinearizability(t *testing.T) {
    client.QuorumPut(ctx, "ns", "key", value)
    client.QuorumGet(ctx, "ns", "key")
    result := scenario.VerifyLinearizability()
    require.True(t, result.Ok)
}

// Gossip 操作：使用收敛性测试
func TestGossipConvergence(t *testing.T) {
    // 执行 Gossip 操作
    client.Put(ctx, "ns", "key", value)

    // 等待收敛（使用版本向量检测，而非固定等待）
    err := waitForConvergence(nodes, 10*time.Second)
    require.NoError(t, err)

    // 验证所有节点最终一致
    for _, node := range nodes {
        val := node.Get(ctx, "ns", "key")
        require.Equal(t, value, val)
    }
}
```

### 3.3 Gossip 收敛性检测（V1.3 更新）

**问题**：原方案使用固定 `time.Sleep(10s)`，不能保证收敛完成。

**方案选择**（架构师确认）：
- ~~方案 A：基于版本向量~~ - 需要新增 VersionVector 实现
- **方案 B（采用）：基于 Merkle Tree** - 复用现有 `internal/metadata/gossip/merkle_sync.go`

**实现**：基于 Merkle Root 的收敛检测：

```go
// GossipConvergenceChecker 收敛性检查器（基于 Merkle Tree）
type GossipConvergenceChecker struct {
    nodes    []*Node
    timeout  time.Duration
    interval time.Duration
}

// WaitForConvergence 等待所有节点收敛
func (c *GossipConvergenceChecker) WaitForConvergence(ctx context.Context) error {
    deadline := time.Now().Add(c.timeout)
    for time.Now().Before(deadline) {
        if c.isConverged() {
            return nil
        }
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-time.After(c.interval):
        }
    }
    // V1.3 新增：返回详细诊断信息
    return c.buildConvergenceError()
}

// isConverged 检查所有节点 Merkle Root 是否一致
func (c *GossipConvergenceChecker) isConverged() bool {
    if len(c.nodes) == 0 {
        return true
    }

    // 使用 Merkle Root 作为一致性标记（复用现有实现）
    baseRoot := c.nodes[0].GetMerkleRoot()

    for _, node := range c.nodes[1:] {
        root := node.GetMerkleRoot()
        if !bytes.Equal(baseRoot, root) {
            return false
        }
    }
    return true
}

// V1.3 新增：收敛失败时提供诊断信息
type ConvergenceError struct {
    Timeout        time.Duration
    NodeRoots      map[string][]byte  // 各节点的 Merkle Root 快照
    DivergentNodes []string           // 未收敛的节点
}

func (e *ConvergenceError) Error() string {
    return fmt.Sprintf("convergence timeout after %v, divergent nodes: %v",
        e.Timeout, e.DivergentNodes)
}

func (c *GossipConvergenceChecker) buildConvergenceError() *ConvergenceError {
    err := &ConvergenceError{
        Timeout:        c.timeout,
        NodeRoots:      make(map[string][]byte),
        DivergentNodes: []string{},
    }

    if len(c.nodes) == 0 {
        return err
    }

    baseRoot := c.nodes[0].GetMerkleRoot()
    err.NodeRoots[c.nodes[0].ID()] = baseRoot

    for _, node := range c.nodes[1:] {
        root := node.GetMerkleRoot()
        err.NodeRoots[node.ID()] = root
        if !bytes.Equal(baseRoot, root) {
            err.DivergentNodes = append(err.DivergentNodes, node.ID())
        }
    }

    return err
}
```

**优势**：
- ✅ 复用现有 `merkle_sync.go` 实现，无需新增代码
- ✅ Merkle Root 已经是节点一致性标记
- ✅ 失败时提供详细诊断信息（V1.3 新增）

### 3.4 Quorum 一致性语义澄清（P1）

| 操作 | 一致性保证 | 验证方法 |
|------|-----------|---------|
| QuorumPut | 多数派确认写入成功 | 线性一致性验证 |
| QuorumGet | 读取多数派最新值 | 线性一致性验证 |
| 普通 Put | Gossip 异步扩散 | 收敛性测试 |
| 普通 Get | 可能读到旧值 | 最终一致性测试 |

---

## 4. 故障注入测试方案（采用 pingcap/failpoint）

> **技术选型**: 采用工业级 `pingcap/failpoint` 框架（TiDB/TiKV 验证）
> **参考文档**: `thoughts/pingcap-failpoint代码级故障注入完全指南.md`

### 4.1 为什么选择 pingcap/failpoint

| 对比项 | 自定义 FaultInjector | pingcap/failpoint |
|--------|---------------------|-------------------|
| **成熟度** | 概念设计 | 工业级（TiDB/TiKV 生产验证） |
| **代码形式** | 自定义 API | 合法 Go 代码（非注释） |
| **编译检查** | 运行时 | ✅ 编译时检查 |
| **并行测试** | 需设计 | ✅ `InjectContext` + `WithHook` |
| **CI 集成** | 需自建 | ✅ `-toolexec` 无缝集成 |
| **开发工作量** | 7-9 天 | 3-5 天 |

### 4.2 需求分析

需要验证以下故障场景的一致性：

| 故障类型 | 场景描述 | 验证目标 | failpoint 名称 |
|---------|---------|---------|----------------|
| **磁盘故障** | 写入失败、磁盘满 | 错误处理正确 | `storage/disk-full`, `storage/write-error` |
| **协调者故障** | 2PC 协调者 crash | 事务正确恢复 | `2pc/coordinator-failure` |
| **网络分区** | 脑裂场景 | Quorum 操作正确性 | `network/partition` |
| **网络延迟** | 高延迟环境 | 时间戳排序正确性 | `network/latency` |
| **消息丢失** | 部分消息丢失 | 重试机制正确性 | `network/drop` |

### 4.3 NexKV Failpoint 规划（V1.3 更新）

```
┌─────────────────────────────────────────────────────────────────────┐
│                    NexKV Failpoint 规划                              │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  Layer 0: Storage (internal/storage)                               │
│  ├── storage/disk-full         - 磁盘满                             │
│  ├── storage/write-error       - 写入错误                           │
│  ├── storage/read-corruption   - 读取数据损坏                       │
│  └── storage/sync-error        - fsync 错误                         │
│                                                                     │
│  Layer 1: Consistency (internal/metadata/consistency)              │
│  ├── 2pc/prepare-timeout       - 2PC prepare 超时                   │
│  ├── 2pc/commit-error          - 2PC commit 错误                    │
│  ├── 2pc/coordinator-failure   - 协调者故障                         │
│  ├── quorum/not-reached        - Quorum 未达成                      │
│  └── merkle/mismatch           - Merkle Tree 校验失败               │
│                                                                     │
│  Layer 2: Network (internal/transport)                             │
│  ├── network/drop              - 消息丢失                           │
│  ├── network/latency           - 网络延迟                           │
│  ├── network/partition         - 网络分区                           │
│  └── network/corruption        - 数据损坏                           │
│                                                                     │
│  Layer 3: Gossip (internal/metadata/gossip) [V1.3 扩展]            │
│  ├── gossip/message-drop       - Gossip 消息丢失                    │
│  ├── gossip/delay              - Gossip 传播延迟                    │
│  ├── gossip/parse-error        - 消息解析错误（V1.3 新增）          │
│  ├── gossip/out-of-order       - 消息顺序乱序（V1.3 新增）          │
│  └── gossip/duplicate          - 消息重复（V1.3 新增）              │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### 4.4 Storage Layer 实现

```go
// internal/storage/disk.go
package storage

import (
    "errors"
    "github.com/pingcap/failpoint"
)

var (
    ErrDiskFull    = errors.New("disk full")
    ErrWriteError  = errors.New("write error")
    ErrSyncError   = errors.New("sync error")
)

// Write 写入数据到磁盘
func (s *Storage) Write(key string, value []byte) error {
    // 故障注入点：磁盘满
    failpoint.Inject("storage/disk-full", func() {
        failpoint.Return(ErrDiskFull)
    })

    // 故障注入点：写入错误
    failpoint.Inject("storage/write-error", func() {
        failpoint.Return(ErrWriteError)
    })

    return s.writeToDisk(key, value)
}

// Sync 强制同步到磁盘
func (s *Storage) Sync() error {
    failpoint.Inject("storage/sync-error", func() {
        failpoint.Return(ErrSyncError)
    })

    return s.file.Sync()
}
```

### 4.5 Consistency Layer 实现（支持并行测试）

```go
// internal/metadata/consistency/twopc_coordinator.go
package consistency

import (
    "context"
    "time"

    "github.com/pingcap/failpoint"
)

// TwoPhaseCommit 执行两阶段提交
func (c *Coordinator) TwoPhaseCommit(ctx context.Context, tx *Transaction) error {
    // 使用 InjectContext 支持并行测试隔离
    failpoint.InjectContext(ctx, "2pc/prepare-timeout", func() {
        time.Sleep(30 * time.Second)
        failpoint.Return(ErrPrepareTimeout)
    })

    failpoint.InjectContext(ctx, "2pc/coordinator-failure", func() {
        // 模拟协调者故障
        failpoint.Return(ErrCoordinatorFailure)
    })

    // Phase 1: Prepare
    if err := c.prepare(ctx, tx); err != nil {
        return c.rollback(ctx, tx)
    }

    // Phase 2: Commit
    failpoint.InjectContext(ctx, "2pc/commit-error", func() {
        failpoint.Return(ErrCommitFailed)
    })

    return c.commit(ctx, tx)
}

// CheckQuorum 检查是否达成 Quorum
func (c *Coordinator) CheckQuorum(shard *Shard) (bool, error) {
    failpoint.Inject("quorum/not-reached", func() {
        failpoint.Return(false, ErrQuorumNotReached)
    })

    responses := c.broadcastCheck(shard)
    return len(responses) >= shard.Quorum, nil
}
```

### 4.6 Network Layer 实现

```go
// internal/transport/transport.go
package transport

import (
    "context"
    "time"

    "github.com/pingcap/failpoint"
)

// SendMessage 发送网络消息
func (t *Transport) SendMessage(ctx context.Context, msg *Message) error {
    // 故障注入：消息丢失
    failpoint.InjectContext(ctx, "network/drop", func(val failpoint.Value) {
        dropRate := val.(float64)
        if rand.Float64() < dropRate {
            failpoint.Return(nil) // 静默丢弃
        }
    })

    // 故障注入：网络延迟
    failpoint.InjectContext(ctx, "network/latency", func(val failpoint.Value) {
        latency := time.Duration(val.(int)) * time.Millisecond
        time.Sleep(latency)
    })

    // 故障注入：网络分区
    failpoint.InjectContext(ctx, "network/partition", func(val failpoint.Value) {
        partitionedNodes := val.([]string)
        for _, node := range partitionedNodes {
            if msg.Target == node {
                failpoint.Return(ErrNetworkPartition)
            }
        }
    })

    return t.doSend(ctx, msg)
}
```

### 4.7 测试用例

```go
// internal/metadata/consistency/porcupine/failpoint_test.go
package porcupine_test

import (
    "context"
    "testing"

    "github.com/pingcap/failpoint"
    "github.com/stretchr/testify/require"
)

// 测试：磁盘满场景
func TestStorageWrite_DiskFull(t *testing.T) {
    s := storage.NewTestStorage(t)
    defer s.Close()

    // 激活 failpoint
    require.NoError(t, failpoint.Enable(
        "github.com/jzhang405/NexKV/internal/storage/storage/disk-full",
        "return(true)",
    ))
    defer failpoint.Disable("github.com/jzhang405/NexKV/internal/storage/storage/disk-full")

    err := s.Write("key1", []byte("value1"))
    require.ErrorIs(t, err, storage.ErrDiskFull)
}

// 测试：2PC 协调者故障（使用 WithHook 隔离）
func Test2PC_CoordinatorFailure(t *testing.T) {
    // 创建带 hook 的 context，只激活特定 failpoint
    ctx := failpoint.WithHook(context.Background(), func(ctx context.Context, fpname string) bool {
        return fpname == "2pc/coordinator-failure"
    })

    require.NoError(t, failpoint.Enable(
        "github.com/jzhang405/NexKV/internal/metadata/consistency/2pc/coordinator-failure",
        "return(true)",
    ))
    defer failpoint.Disable("github.com/jzhang405/NexKV/internal/metadata/consistency/2pc/coordinator-failure")

    coord := NewTestCoordinator(t)
    tx := NewTestTransaction(t, []string{"node-1", "node-2", "node-3"})

    err := coord.TwoPhaseCommit(ctx, tx)
    require.ErrorIs(t, err, consistency.ErrCoordinatorFailure)
}

// 测试：并行测试（不同 failpoint 隔离）
// V1.3 更新：架构师建议故障注入测试不建议并行执行
func TestParallelFailpoints(t *testing.T) {
    scenarios := []struct {
        name       string
        failpoints map[string]bool
        expectErr  bool
    }{
        {"normal", map[string]bool{}, false},
        {"disk-full", map[string]bool{"storage/disk-full": true}, true},
        {"network-partition", map[string]bool{"network/partition": true}, true},
    }

    for _, sc := range scenarios {
        sc := sc  // 显式捕获循环变量
        t.Run(sc.name, func(t *testing.T) {
            // V1.3 注意：故障注入测试不建议使用 t.Parallel()
            // 原因：failpoint.Enable() 是全局的，并行测试可能相互干扰
            // 如需并行，请确保使用 WithHook 隔离且不使用全局 Enable

            ctx := failpoint.WithHook(context.Background(), func(ctx context.Context, fpname string) bool {
                enabled, ok := sc.failpoints[fpname]
                return ok && enabled
            })

            err := runStorageTest(ctx)
            if sc.expectErr {
                require.Error(t, err)
            } else {
                require.NoError(t, err)
            }
        })
    }
}

// V1.3 新增：推荐使用串行标签隔离故障注入测试
//go:build !race
// +build !race

func TestFailpointScenarios_Serial(t *testing.T) {
    // 故障注入测试强制串行执行
    // 使用 go test -tags=!race 运行
    // ...
}
```

### 4.8 Makefile 集成

```makefile
# Makefile with failpoint support

FAILPOINT_TOOLEXEC := $(shell pwd)/tools/failpoint-toolexec
GOCACHE_FAILPOINT := /tmp/nexkv-failpoint-cache

# 初始化 failpoint 工具
failpoint-setup:
	@echo "Setting up failpoint tools..."
	@if [ ! -f $(FAILPOINT_TOOLEXEC) ]; then \
		go install github.com/pingcap/failpoint/failpoint-toolexec@latest && \
		mkdir -p tools && \
		cp $(shell which failpoint-toolexec) $(FAILPOINT_TOOLEXEC); \
	fi

# 运行所有测试（包含 failpoint）
test-failpoint: failpoint-setup
	@echo "Running tests with failpoint support..."
	GOCACHE=$(GOCACHE_FAILPOINT) \
	go test -toolexec $(FAILPOINT_TOOLEXEC) -v -race ./...

# 运行故障注入测试
test-chaos: failpoint-setup
	@echo "Running chaos tests..."
	@for fp in storage/disk-full storage/write-error network/partition; do \
		echo "Testing failpoint: $$fp"; \
		GOCACHE=$(GOCACHE_FAILPOINT) \
		GO_FAILPOINTS="github.com/jzhang405/NexKV/internal/$$fp=50%return(true)" \
		go test -toolexec $(FAILPOINT_TOOLEXEC) -v ./... || exit 1; \
	done
```

### 4.9 实现难度评估（V1.3 更新）

| 组件 | 难度 | 工作量 | 说明 |
|------|------|--------|------|
| failpoint 工具集成 | ⭐ | 0.5 天 | `go install` + Makefile |
| Storage 层 failpoint | ⭐⭐ | 1 天 | 4 个故障点 |
| Consistency 层 failpoint | ⭐⭐⭐ | 1.5 天 | 5 个故障点 + context 支持 |
| Network 层 failpoint | ⭐⭐⭐ | 1 天 | 4 个故障点 |
| Gossip 层 failpoint | ⭐⭐ | 1 天 | 5 个故障点（V1.3 扩展） |
| 测试用例编写 | ⭐⭐ | 1.5 天 | 表格驱动 + 串行测试 |
| E2ETestScenario 集成 | ⭐⭐ | 0.5 天 | 与现有框架集成 |
| **总计** | - | **7 天** | V1.3 调整（原 5 天偏乐观） |

**架构师建议**：如需进一步拆分，可分为：
- PR-065a：failpoint 基础设施集成（2 天）
- PR-065b：Storage/Network 层 failpoint 实现（3 天）
- PR-065c：Consistency/Gossip 层 failpoint + 测试用例（2-3 天）

---

## 5. 2PC 跨分片事务验证

### 5.1 需求分析

NexKV 的 2PC 协议特点：
- 发起节点兼任协调者
- 砍掉 Prepare 阶段（直接预提交）
- Gossip 同步事务状态
- 故障自动补偿

**现有实现复杂度**（评审发现）：
- 5 种事务状态
- Merkle Tree 协同
- Pending 操作暂存机制
- 超时与重试逻辑

### 5.2 技术方案

**扩展操作类型**：

```go
const (
    // 现有操作
    OpGet OpType = iota
    OpPut
    OpDelete
    OpQuorumGet
    OpQuorumPut

    // 2PC 操作
    Op2PCBegin      // 开始事务
    Op2PCPrepare    // 预提交（单分片）
    Op2PCCommit     // 提交
    Op2PCRollback   // 回滚
)

// V1.3 新增：2PC 扩展状态模型（分布式系统专家建议）
type TxState struct {
    TxID        string            // 事务 ID
    Status      TxStatus          // pending/prepared/committed/aborted
    Participants []string         // 参与者分片列表
    Writes      map[string]string // 写操作集合
    Coordinator string            // 协调者节点 ID
    DecisionTS  int64             // 决策时间戳（用于 Gossip 同步）
}

type TxStatus int

const (
    TxStatusPending TxStatus = iota
    TxStatusPrepared
    TxStatusCommitted
    TxStatusAborted
    TxStatusUnknown  // 协调者故障后的不确定状态
)

// 扩展的 Porcupine 状态模型
type ExtendedState struct {
    KVState  map[string]string  // 原有 KV 状态
    TxStates map[string]*TxState // txID -> TxState（V1.3 新增）
}
```

**V1.3 新增：协调者故障测试场景**（分布式系统专家建议）

| 故障时机 | 预期行为 | 验证方法 |
|---------|---------|---------|
| **Prepare 阶段前** | 事务不开始，客户端收到错误 | 检查 KV 状态不变 |
| **Prepare 阶段中** | 部分参与者已 prepare，需要超时回滚 | 检查最终一致性 |
| **Commit 阶段前** | 所有参与者已 prepare，需要恢复协调者 | 检查事务最终提交 |
| **Commit 阶段中** | 部分参与者已 commit，需要继续传播 | 检查所有参与者最终一致 |
| **Gossip 同步前** | 事务决策未传播到其他节点 | 检查 Gossip 后一致性 |

### 5.3 挑战分析

| 挑战 | 说明 | 解决方案 |
|------|------|---------|
| **状态爆炸** | 2PC 涉及多分片，状态空间巨大 | 限制事务规模、使用抽象 |
| **超时场景** | Prepare/Commit 超时 | 建模超时为显式状态 |
| **Gossip 同步** | 事务状态通过 Gossip 传播 | 扩展状态模型 |
| **故障恢复** | 协调者故障后恢复 | 幂等操作设计 |
| **现有复杂度** | 已有 5 种状态 + Merkle Tree | 先完成 TLA+ 规约 |

### 5.4 Porcupine 可视化支持（V1.3 新增）

**架构师建议**：Porcupine 验证失败时生成交互式 HTML 报告，便于调试：

```go
// internal/metadata/consistency/porcupine/checker.go

import (
    "os"
    "path/filepath"
    "github.com/anishathalye/porcupine"
)

// CheckWithVisualization 带可视化的一致性检查
func (c *ConsistencyChecker) CheckWithVisualization(history []porcupine.Operation) (bool, string) {
    result := porcupine.CheckOperations(c.model, history, c.timeout)

    if result.Ok {
        return true, ""
    }

    // 生成可视化文件
    visPath := filepath.Join(os.TempDir(), fmt.Sprintf("porcupine-violation-%d.html", time.Now().Unix()))
    err := porcupine.Visualize(c.model, history, visPath)
    if err != nil {
        return false, fmt.Sprintf("linearizability check failed, visualization error: %v", err)
    }

    return false, fmt.Sprintf("linearizability check failed, visualization: %s", visPath)
}

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
        Error: fmt.Sprintf("Linearizability violation detected. Visualization: %s", visPath),
    }, visPath
}
```

**使用示例**：

```go
func TestWithVisualization(t *testing.T) {
    // ... 执行操作 ...

    result, visPath := scenario.VerifyLinearizabilityWithVis()
    if !result.Ok {
        t.Logf("Visualization file: %s", visPath)
        // 在 CI 中上传为 artifact
    }
    require.True(t, result.Ok, result.Error)
}
```

| 阶段 | 原估算 | 修正估算 | 说明 |
|------|--------|---------|------|
| TLA+ 规约 | 0 天 | 3-5 天 | **前置条件**，必须先完成 |
| 操作类型扩展 | 1 天 | 2 天 | 需要考虑 Gossip 同步 |
| 状态模型扩展 | 2 天 | 3-4 天 | 状态空间更复杂 |
| 测试用例编写 | 3 天 | 4-5 天 | 需要覆盖协调者故障 |
| **总计** | **7-10 天** | **12-15 天** | |

**建议**：2PC 验证应结合 **TLA+** 进行设计验证，Porcupine 用于运行时验证。

---

## 6. 真实网络 E2E 测试

### 6.1 需求分析

当前测试使用 mock KV，需要在真实网络环境验证：
- 真实的网络延迟
- 真实的并发场景
- 真实的故障场景

### 6.2 技术方案

```go
// 真实集群测试场景
type RealClusterScenario struct {
    *RecordingE2ETestScenario
    Nodes     []*RealNode
    Transport *TCPTransport
    Config    TestConfig
}

type TestConfig struct {
    NodeCount       int
    NetworkLatency  time.Duration
    FailureRate     float64
}
```

### 6.3 热点 Key 竞争测试（P2 新增）

```go
// 测试：热点 Key 竞争场景
func TestRealCluster_HotKeyContention(t *testing.T) {
    scenario := NewRealClusterScenario(TestConfig{
        NodeCount: 5,
    })
    defer scenario.Stop()
    scenario.Start()

    // 100 个客户端竞争写入同一个 key
    var wg sync.WaitGroup
    hotKey := "hot-key"

    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func(clientID int) {
            defer wg.Done()
            client := scenario.GetClient(clientID % 5)
            value := []byte(fmt.Sprintf("value-from-client-%d", clientID))
            client.QuorumPut(ctx, "ns", hotKey, value)
        }(i)
    }
    wg.Wait()

    // 验证最终一致性（所有节点最终应该有相同的值）
    time.Sleep(1 * time.Second)
    firstVal, _ := scenario.Nodes[0].Get(ctx, "ns", hotKey)
    for _, node := range scenario.Nodes[1:] {
        val, _ := node.Get(ctx, "ns", hotKey)
        require.Equal(t, firstVal, val, "All nodes should have the same final value")
    }
}
```

---

## 7. 实施计划

### 7.1 PR 依赖关系图（V1.3 更新）

```
Phase 0: 前置修复（V1.3 新增）
┌─────────────────────────────────────────────────────────┐
│ HLC Bugfix PR                                           │
│ ├── 修复 nil 检查问题                                   │
│ ├── 修复逻辑计数器溢出问题                              │
│ ├── 工作量: 0.5 天                                      │
│ └── 负责人: 核心开发 A                                  │
└─────────────────────────────────────────────────────────┘
                           │
                           ▼
Phase 1: 基础设施
┌─────────────────────────────────────────────────────────┐
│ PR-064: 分离 Gossip/Quorum 测试策略                     │
│ ├── 添加 GossipConvergenceChecker（基于 Merkle Tree）   │
│ ├── 添加 QuorumLinearizabilityChecker                   │
│ ├── 集成 HLC 时间戳（48-bit PT + 16-bit C）             │
│ ├── 添加 Porcupine 可视化支持（V1.3 新增）              │
│ ├── 依赖: HLC Bugfix PR                                 │
│ ├── 工作量: 3-4 天（V1.3 调整）                         │
│ └── 风险: Merkle Root 获取接口可能需要适配              │
└─────────────────────────────────────────────────────────┘
                           │
                           ▼
Phase 2: 故障注入
┌─────────────────────────────────────────────────────────┐
│ PR-065: 故障注入测试框架                                │
│ ├── failpoint 工具集成                                  │
│ ├── Storage/Network/Consistency/Gossip 层 failpoint     │
│ ├── 测试用例编写（串行执行）                            │
│ ├── 依赖: PR-064（需要分离的测试策略）                  │
│ ├── 工作量: 7 天（V1.3 调整，原 5 天）                  │
│ └── 风险: 并行测试隔离复杂度                            │
└─────────────────────────────────────────────────────────┘
                           │
                           ▼
Phase 3: 真实环境测试
┌─────────────────────────────────────────────────────────┐
│ PR-066: 真实网络 E2E 测试                               │
│ ├── RealClusterScenario                                 │
│ ├── 热点 Key 竞争测试（使用收敛检测替代硬编码等待）     │
│ ├── CI 集成                                             │
│ ├── 依赖: PR-065（需要故障注入支持）                    │
│ ├── 工作量: 4-5 天                                      │
│ └── 风险: CI 环境稳定性                                 │
└─────────────────────────────────────────────────────────┘
                           │
                           ▼
Phase 4: 2PC 验证（阻塞项：TLA+ 规约）
┌─────────────────────────────────────────────────────────┐
│ PR-067: 2PC 验证                                        │
│ ├── 前置条件: TLA+ 规约完成（3-5 天）                   │
│ │   └── 负责人: 架构师/分布式系统 Agent（V1.3 确认）    │
│ ├── 扩展操作类型 + 状态模型                             │
│ ├── 协调者故障测试（5 种场景）                          │
│ ├── 依赖: PR-065 + TLA+ 规约                            │
│ ├── 工作量: 12-15 天                                    │
│ └── 风险: TLA+ 规约发现设计缺陷                         │
└─────────────────────────────────────────────────────────┘
```

### 7.2 优先级排序（V1.3 更新）

| 优先级 | 任务 | 工作量 | 价值 | 建议 |
|--------|------|--------|------|------|
| **P0** | HLC Bugfix PR | 0.5 天 | 高 | 立即实施 |
| **P1** | 分离 Gossip/Quorum 测试策略 | 3-4 天 | 高 | 立即实施 |
| **P1** | 故障注入测试框架 | 7 天 | 高 | 本迭代 |
| **P1** | TLA+ 规约（与 PR-064/065 并行） | 3-5 天 | 高 | 架构师负责 |
| **P2** | 真实网络 E2E 测试 | 4-5 天 | 中 | 下迭代 |
| **P2** | 2PC 验证 | 12-15 天 | 中 | 需要设计先行 |

### 7.3 建议 PR 分拆（V1.3 更新）

```
HLC Bugfix PR（0.5 天）- 前置条件
├── 修复 internal/clock/hlc.go nil 检查
├── 修复逻辑计数器溢出
└── 添加单元测试

PR-064: 分离 Gossip/Quorum 测试策略（3-4 天）
├── 添加 GossipConvergenceChecker（基于 Merkle Tree）
├── 添加 QuorumLinearizabilityChecker
├── 集成 HLC 时间戳（48-bit PT + 16-bit C）
├── 添加 Porcupine 可视化支持
└── 更新测试用例

PR-065: 故障注入测试框架（7 天）
├── failpoint 工具集成
├── Storage 层 failpoint（4 个）
├── Network 层 failpoint（4 个）
├── Consistency 层 failpoint（5 个）
├── Gossip 层 failpoint（5 个，V1.3 扩展）
├── E2ETestScenario 集成
└── 测试用例（串行执行）

PR-066: 真实网络 E2E 测试（4-5 天）
├── RealClusterScenario
├── 热点 Key 竞争测试
├── CI 集成
└── 性能基准

PR-067: 2PC 验证（12-15 天，含 TLA+）
├── TLA+ 规约编写（前置条件，架构师负责）
├── 扩展操作类型
├── 扩展状态模型
└── 2PC 测试用例（5 种协调者故障场景）
```

---

## 8. 风险评估（V1.3 更新）

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| HLC nil 检查导致 panic | 高 | 100% | 创建独立 bugfix PR |
| HLC 溢出导致时间戳回退 | 高 | 30% | 已添加溢出检测和推进物理时间 |
| Gossip 测试误报 | 中 | 20% | 分离测试策略 + Merkle Root 检测 |
| 故障注入影响 CI 稳定性 | 中 | 40% | 串行执行 + 清理机制 |
| 2PC 状态爆炸 | 高 | 60% | 先完成 TLA+ 规约 |
| TLA+ 规约发现设计缺陷 | 中 | 40% | 早期发现，及时修正 |
| 真实测试环境不稳定 | 中 | 30% | 添加重试机制 |
| Porcupine 验证性能 | 中 | 40% | 限制 history 长度（1000 ops） |

---

## 9. 结论与建议

### 9.1 架构师确认的方案（V1.3 更新）

| 决策项 | 确认方案 |
|--------|---------|
| **时钟方案** | 使用 `internal/clock` 的 HLC（48-bit PT + 16-bit C） |
| **测试策略** | 方案 A：分离测试策略 |
| **Gossip 收敛检测** | 方案 A：基于 Merkle Tree（复用现有实现） |
| **故障注入** | pingcap/failpoint 框架 |
| **TLA+ 规约负责人** | 架构师/分布式系统 Agent |

### 9.2 实施计划

1. **立即实施**：创建 HLC bugfix PR（0.5 天）
2. **立即实施**：分离 Gossip/Quorum 测试策略（PR-064，3-4 天）
3. **并行启动**：TLA+ 规约编写（3-5 天，架构师负责）
4. **本迭代**：故障注入测试框架（PR-065，7 天）
5. **下迭代**：真实网络 E2E 测试（PR-066，4-5 天）
6. **待 TLA+ 完成**：2PC 验证（PR-067，12-15 天）

### 9.3 技术决策（V1.3 确认）

- ✅ 采用**分离测试策略**而非扩展模型（架构师确认）
- ✅ 时钟使用 **`internal/clock` HLC**，时间戳格式 48-bit PT + 16-bit C
- ✅ Gossip 收敛检测使用 **Merkle Tree**（复用现有实现）
- ✅ 故障注入使用 **pingcap/failpoint** 框架
- ✅ 2PC 验证结合 **TLA+** 进行设计验证
- ✅ 补充协调者故障测试场景（5 种时机）
- ✅ 添加 Porcupine 可视化支持
- ✅ 故障注入测试**不建议并行执行**

---

## 10. 附录：TLA+ 规约说明（V1.3 新增）

### 10.1 什么是 TLA+

**TLA+（Temporal Logic of Actions）** 是由 Leslie Lamport 发明的形式化规约语言，用于：

1. **设计验证**：在实现前验证分布式算法的正确性
2. **并发分析**：发现竞态条件和死锁
3. **不变式证明**：数学证明系统满足某些性质

### 10.2 为什么 PR-067 需要 TLA+

**2PC 协议复杂度分析**：

| 复杂度来源 | 说明 |
|-----------|------|
| 5 种事务状态 | pending/prepared/committed/aborted/unknown |
| 协调者故障 | 5 种故障时机需要处理 |
| Gossip 同步 | 事务状态异步传播 |
| Merkle Tree 协同 | 数据一致性标记 |
| 幂等重试 | 故障恢复需要幂等设计 |

**TLA+ 规约目标**：

```
---- MODULE NexKV2PC ----
EXTENDS Naturals, Sequences

VARIABLES txState, kvState, coordinator

(* 2PC 状态转换 *)
Init == /\ txState = "pending"
        /\ kvState = [k \in Keys |-> None]

Prepare == /\ txState = "pending"
           /\ coordinator # None
           /\ txState' = "prepared"

Commit == /\ txState = "prepared"
          /\ coordinator # None
          /\ txState' = "committed"

(* 不变式：原子性保证 *)
Atomicity == \/ txState = "pending"
             \/ txState = "prepared"
             \/ (txState = "committed" /\ AllShardsCommitted)
             \/ (txState = "aborted" /\ AllShardsAborted)
====
```

### 10.3 TLA+ 与 Porcupine 的协作

| 工具 | 用途 | 时机 |
|------|------|------|
| **TLA+** | 设计验证（模型检查） | 开发前 |
| **Porcupine** | 运行时验证（线性一致性） | 开发后 |

```
设计阶段 → TLA+ 规约 → 模型检查 → 发现设计缺陷
                                    ↓
                              修正设计
                                    ↓
开发阶段 → Porcupine 测试 → 线性一致性验证 → 发现实现缺陷
```

### 10.4 TLA+ 规约验收标准

PR-067 的 TLA+ 规约需满足：

| 验收项 | 说明 |
|--------|------|
| **原子性** | 事务要么全部提交，要么全部回滚 |
| **持久性** | 已提交事务不可撤销 |
| **故障恢复** | 协调者故障后系统能正确恢复 |
| **无死锁** | 任何状态下都有进展可能 |
| **可执行** | 规约可在 TLC 模型检查器中运行 |

---

## 文档信息

| 项目 | 内容 |
|------|------|
| 预研状态 | ✅ 完成，三轮评审通过 |
| 文档版本 | V1.3 |
| 创建日期 | 2026-02-13 |
| 最后更新 | 2026-02-13 |
| 下一步 | 创建 HLC bugfix PR |
| 维护人 | 🤖 核心开发 A |
