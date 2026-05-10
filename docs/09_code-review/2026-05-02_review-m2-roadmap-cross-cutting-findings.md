# M2 存储引擎路线图 — 跨路线有效评审结论

> **评审日期**: 2026-05-02
> **评审范围**: `docs/07_spike/2026-02-21_spike_m2-storage-engine-roadmap.md`（Bf-Tree 路线，现已废弃）
> **适用场景**: 提取不受具体 BTree 实现路线（Bf-Tree vs Lealone BTree）影响的通用评审结论
> **当前实际路线**: Lealone BTree 移植 → `docs/07_spike/btree-refactor/`

---

## 背景

经过四专家（存储引擎、DDD 架构、Go 语言、分布式 KV）并行评审，Bf-Tree roadmap 文档综合评分约 **5.3/10**。

随后确认 **Bf-Tree 路线已废弃**，当前实际走 Lealone BTree 移植路线。原评审中大量 Bf-Tree 专属结论（Mini-Page、Delta Chain、3 级 Mini-Page 归属、Rust 基线对比等）直接作废。

本文档提取**不受路线变更影响**的通用结论，供当前 `btree-refactor` 系列文档参考和应用。

---

## 一、DDD 架构层面（跨路线有效）

### 1.1 接口数量与层级不一致

**级别**：致命（F5）

存储引擎层接口数量在三份"权威文档"中各不相同：
- 路线图 10 个（含 AsyncOperation）
- DDD Interface v3.1 定义 9 个（AsyncOperation 归入基础设施层）
- 两方定义冲突

AsyncOperation 是基础设施层的通用并发抽象，不应混入存储引擎领域接口清单。`btree-refactor` 文档中应明确区分领域接口 vs 基础设施工具。

### 1.2 KVStore 接口违反 ISP

**级别**：高风险（H1）

20+ 方法混合同步/异步/CRUD/批量/事务/资源管理，胖接口导致：
- 简单实现（如测试 MemoryStore）被迫实现全部异步存根
- `NewTx()` 返回 `LocalTx` 造成编译时耦合，不支持事务的实现仍需 stub

**建议**：拆分为 `KVStoreReader` / `KVStoreWriter` / `KVStoreAsync` 组合接口。

### 1.3 V4 管道接口跨层

**级别**：高风险（H2）

`TaskRunner` / `BaseTask[Result]` 属于基础设施层并发调度机制，不应出现在领域层的 `package storage` 中。

**建议**：
- `Task[Result]` 保留在领域层（描述"做什么"）
- `TaskRunner` / `BaseTask` 移入 `infrastructure/concurrency/`（描述"怎么做"）

### 1.4 BlockDevice 接口存在抽象泄漏

**级别**：中风险（M1）

`LocalStorage.Prefetch()`/`Defragment()`、`CloudStorage.MultipartUpload()`/`SetLifecycle()` 等方法是特定存储介质的实现细节，不应在领域接口层暴露。

**建议**：使用策略模式注入优化行为，或定义为独立的 `Optimizer`/`LifecycleManager` 可选接口。

---

## 二、Go 并发安全层面（跨路线有效）

### 2.1 AsyncOperation.Get() 数据竞争

**级别**：高风险（H3）

`Get()` 在 `<-op.done` 后直接读取 `op.result`/`op.err`，未持有互斥锁。`execute()` 中写入这些字段时持有锁，但 `done` channel 的关闭发生在 `mu.Unlock()` 之后（defer 顺序），没有 happen-before 保证。

多 goroutine 并发 `Get()` 时 race detector 会报错。

**建议**：使用 `sync.RWMutex` 保护读取，或使用 `atomic.Value` 包装 result/err。

### 2.2 页面级锁的 sync.Map 内存泄漏

**级别**：高风险（H4）

`getPageLock()` 使用 `sync.Map.LoadOrStore(pageID, &sync.RWMutex{})` 创建锁，节点分裂/合并后对应锁永久残留。大规模使用下（百万页面）内存泄漏严重。

**建议**：实现惰性清理策略 — 节点合并时从 `pageLocks` 中 delete 对应 key。

### 2.3 goroutine 生命周期管理缺失

**级别**：高风险（H5）

`notifyCallbacks()` 每个回调启动独立 goroutine 无超时控制。如果回调阻塞（写入已满 channel），goroutine 永久泄漏。

**建议**：回调执行加入 `time.After` + select 超时；QA 流程集成 `go.uber.org/goleak`。

### 2.4 BaseTask.Run() 类型断言 panic 风险

**级别**：中风险（M3）

`any(b).(Task[Result])` 非安全类型断言，如果具体类型嵌入 `BaseTask` 但未实现 `Execute`，运行时 panic。

**建议**：使用 `runner, ok := any(b).(Task[Result])` 安全断言。

---

## 三、分布式 KV 层面（跨路线有效）

### 3.1 分布式原语完全缺失

**级别**：致命（F7）

当前接口和数据结构没有为分布式演进预留扩展点：
- 没有 ShardID 参数/上下文
- WALEntry 没有 Term 字段（Raft 集成会冲突）
- 没有 StateMachine 接口（WAL 回放的标准模式）
- 没有 Prepare/Commit/Abort 的原子提交协议支持

**建议**：即使 MVP 不做分布式，也要在关键数据结构中预留字段（如 WALEntry 中增加 `Term uint64`，`KVStore` 方法中增加 `ShardID` 可选参数）。

### 3.2 单机到分布式演进路径空白

**级别**：致命（F9）

当前设计本质上是"优秀的单机存储引擎"，与"分布式 KV"存在架构鸿沟。没有阶段性演进规划，未来从单机到分布式将面临大规模重构。

**建议**：创建 `spike/distributed-evolution-path.md` 专项文档，定义 Stage 1-3 的交付物和评审门禁。

### 3.3 Cloud/Distributed Storage 周期不现实

**级别**：致命（F8）

无论哪个路线，2 周实现 4 个云适配器（S3 + Azure + Ceph + MinIO）都不现实。SDK 认证、重试策略、错误映射、幂等性保障均未讨论。

**建议**：MVP 阶段仅做接口定义 + Mock 实现，真实云存储推向 Phase 3+。

### 3.4 分布式场景性能指标缺失

**级别**：高风险（HIGH-03）

当前性能目标只定义了单机指标，缺少：
- P99/P999 延迟（分布式 SLO 的关键指标）
- 网络开销预算
- 跨节点 Scan 的复杂度分析

---

## 四、测试与质量保证（跨路线有效）

### 4.1 goroutine 泄漏检测缺失

QA 章节（第 523-565 行）列出了单元测试、竞态检测、模糊测试、基准测试、压力测试，但未包含 goroutine 泄漏检测。

**建议**：集成 `go.uber.org/goleak` 到 CI 流水线。

### 4.2 故障注入测试覆盖不足

当前测试策略缺少：
- IO 故障注入（磁盘满、网络分区、S3 不可用）
- 确定性模拟测试（并发时序系统性覆盖）
- 长稳测试（72 小时+ 连续运行，观察内存泄漏/WAL 增长）

### 4.3 WAL 生命周期管理未规划

WAL GC/轮转策略（何时截断、何时触发 Snapshot、Snap 后安全窗口）缺失。

**建议**：参考 `docs/07_spike/btree-refactor/2026-04-17-phase3-wal-gc-spike.md` 已有设计。

---

## 五、建议优先级

| 优先级 | 结论编号 | 行动 |
|--------|---------|------|
| **P0** | 2.1 | 修复 AsyncOperation.Get() 数据竞争 |
| **P0** | 3.1 | 关键数据结构预留分布式字段（Term, ShardID） |
| **P0** | 1.2 | 拆分 KVStore 胖接口 |
| **P1** | 1.3 | 将 TaskRunner/BaseTask 移入基础设施层 |
| **P1** | 2.2 | sync.Map 页面锁清理机制 |
| **P1** | 2.3 | 回调 goroutine 超时 + goleak 集成 |
| **P1** | 1.1 | 统一接口数量与层级定义 |
| **P2** | 3.2 | 创建分布式演进路径专项文档 |
| **P2** | 3.3 | Cloud/Distributed Storage 降级为 Mock |
| **P2** | 4.2 | 故障注入 + 长稳测试 |

---

**文档版本**: v1.0
**创建日期**: 2026-05-02
**关联文档**: `docs/07_spike/2026-02-21_spike_m2-storage-engine-roadmap.md`（已废弃，Bf-Tree 路线）
