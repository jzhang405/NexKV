# 【PR全流程文档】Feature - BTree 读写性能调优（WAL 关闭）

> **文档说明**：本文档包含「前置规划」和「后置总结」两部分，记录从需求对齐到开发完成的全流程，一个PR对应一份全流程文档，归档后作为项目追溯依据。

---

## 第一部分：前置部分（开工前必完成，架构师评审通过）

### 1. 基础信息（与分支/PR绑定）

| 项目 | 内容 |
|------|------|
| 工作类型 | 性能优化（Feature - Perf Tuning） |
| PR编号 | PR-XXX（创建GitHub PR后补充完整） |
| 分支名称 | feature/btree-perf-tuning |
| 工作主题 | BTree 存储引擎读写路径性能分析与优化（WAL 关闭，纯存储引擎模式） |
| 负责人 | jzhang405 |
| 分支创建日期 | 2026-05-15 |
| 计划开工日期 | 2026-05-16 |
| 计划CI通过日期 | 2026-05-23 |
| 关联需求单号 | BTree v1 性能达标（Phase 7 前置条件） |
| 架构师评审状态 | □ 待评审 □ 评审中 □ 评审通过 □ 需优化（循环记录） |
| 预审批结果 | □ 未通过 □ 已通过（架构师签字/备注：XXX 202X-XX-XX 同意开工） |

### 2. 背景与目标（为什么干）

#### 2.1 背景

**业务场景**：NexKV 的 BTree 存储引擎已完成 Phase 1-6.5 的核心功能开发（COW 语义、CAS 乐观锁、Split/Merge、Tombstone Compaction），功能正确性已通过 80%+ 测试覆盖率验证。**本次 PR 聚焦于 WAL 关闭场景下的纯存储引擎性能**，先优化核心读写路径，排除 WAL I/O 的干扰变量。WAL 开启场景的优化作为下一个独立 PR。当前阶段需要将性能提升至生产可用水平。

**现有问题**（综合 Pre 分析 + Code Review 发现）：

| 优先级 | 问题 | 位置 | 表现 | 影响 |
|--------|------|------|------|------|
| P0 | O(n²) Update 回退 | `leaf_page.go:142-163` | 值增长时逐条 InsertLeafEntry，2000 条 → ~200 万次 byte move | 大 value 更新延迟爆炸 |
| P0 | 缺少延迟直方图 | `metrics.go` | 仅有计数型 metrics，无 P50/P95/P99 | 无法定位性能热点 |
| P0 | 缺少性能基线 | `benchmark_test.go` | 现有 benchmark 仅 100 key 回避分裂 | 无法量化优化效果 |
| P1 | COW 写放大 | `leaf_page.go` | 每次写复制 4KB 全页，1 条 entry 变更 = 128x 写放大 | 写吞吐受限 |
| P1 | Release() defer 开销 | `page_ref.go:109-136` | 每次 Release 支付 ~80ns defer + debug.PrintStack | 读写热路径持续浪费 |
| P1 | Parent CAS 重做 COW | `operations.go:32-99` | 自旋每次重做 InsertChild(Alloc+4KB memcpy)，100 次 × 2 协程 = 800KB | 分裂竞争 CPU 浪费 |
| P1 | RemoveChild wasted memcpy | `node_page.go:163-206` | 先 copy 4096 字节全页再 InitIndexPage wipe | 无效内存拷贝 |
| P1 | 分裂传播等待 | `operations.go:32-99` | MaxParentCASSpins=100，每迭代全页 COW | 写延迟毛刺 |
| P2 | CAS 重试风暴 | `constants.go:39` | MaxCASRetries=200，50us/次 = 最差 10ms | 延迟抖动 |
| P2 | Split double-COW 孤儿页 | `operations.go:711-748` | 目标半页 BulkInit 后立即释放重分配 | 浪费 Alloc+Free |
| P2 | ChildrenCache 重复分配 | `operations.go:629-690` | CAS 失败时每次分配新 slice | GC 压力 |
| P2 | Separator key 全量拷贝 | `operations.go:571-609` | 126 子节点分配 ~250 个 key | 内部分裂 GC 压力 |
| P2 | Gosched 跨核无效 | `operations.go:139-146` | runtime.Gosched() 仅同 P 内让出 | 分裂 backoff 低效 |

**价值**：
- 建立性能基线和回归检测能力
- 识别并消除读写路径中的热点
- 将 BTree 吞吐提升至接近硬件上限
- 为 Phase 7（生产化）提供性能达标证明

#### 2.2 核心目标（按 3 个 Phase 推进，每 Phase 独立可验证）

**Phase 1: 性能基线建立**

1. **Latency Histogram**：`metrics.go` 新增 P50/P95/P99 延迟直方图，采样策略 1/64 避免热路径回退
2. **Benchmark 补齐**：覆盖不同 key/value size（8B-4KB）、并发度（1-16）、数据量（1K-1M keys）、读写比例（0:100 ~ 100:0）
3. **基准报告**：产出当前 BTree 的完整性能画像（吞吐 + 延迟 + CAS 冲突率 + 页分配/释放速率）

**Phase 2: 低风险快速收益（不改变核心算法）**

1. **Release() 去 defer**（`page_ref.go`）：使用 `Add(-1)` 返回值替代 defer + Load，消除 ~80ns/调用开销
2. **RemoveChild 去 wasted memcpy**（`node_page.go`）：跳过初始 4KB copy，直接 InitIndexPage
3. **O(n²) → O(n) Update**（`leaf_page.go`）：值增长回退路径改为批量拷贝替代逐条 InsertLeafEntry
4. **参数调优**（`constants.go`）：MaxCASRetries 200→128，MaxParentCASSpins 100→32
5. **目标**：写吞吐 +20-30%，读延迟 -10-15%

**Phase 3: 深度优化（每个优化项独立 benchmark 验证）**

1. **Parent CAS 自适应退避**（`operations.go`）：前 10 次 spin，之后 exponential backoff（1us→1ms）
2. **Split 跳过孤儿页**（`operations.go`）：目标半页不预 BulkInit，直接构造最终页
3. **updateChildrenCache 预分配**（`operations.go`）：CAS 成功后再分配新 slice
4. **Splitting backoff 指数退避**（`operations.go`）：替代固定 Gosched 阈值
5. **目标**：8 线程并发写线性扩展系数 > 0.7，写 P99 < 1ms

#### 2.3 明确边界（不做什么，避免范围蔓延）

- **本次不支持**：存储格式变更（不改变 on-disk format）
- **本次不支持**：WAL 性能优化（WAL 已独立为 Phase 3，不在本次范围）
- **本次不涉及**：WAL 开启场景的性能优化（**WAL 开启优化为下一个独立 PR**，本 PR 所有 benchmark 和优化均在 WAL 关闭条件下进行）
- **本次不支持**：网络层/RPC 层性能优化
- **本次不优化**：Compaction 策略调整（Phase 6.5 已完成）
- **本次不涉及**：MVCC 事务引擎优化（MVCC 性能调优独立进行）

### 3. 实现方案（3 个 Phase 渐进推进）

#### 3.1 Phase 1: 性能基线建立

**目标**：补齐观测能力，产出完整性能画像。

**3.1.1 Latency Histogram**（`metrics.go`）

```go
// LatencyHistogram 使用对数分桶记录延迟分布
// 桶边界：1us, 2us, 4us, 8us, ... 1s (64 个桶)
type LatencyHistogram struct {
    buckets [64]atomic.Int64
    count   atomic.Int64
    sum     atomic.Int64 // 微秒级总延迟
}
// 采样策略：每 64 次操作记录 1 次，避免热路径回退
// 方法：Record(d time.Duration), Snapshot() HistogramSnapshot
// 默认关闭，WithLatencyMetrics(true) 启用
```

**3.1.2 Benchmark 补齐**（`benchmark_test.go`）

| 维度 | 取值 |
|------|------|
| Key size | 8B, 64B, 256B, 1KB |
| Value size | 16B, 128B, 1KB, 4KB |
| 并发度 | 1, 2, 4, 8, 16 goroutines |
| 数据规模 | 1K, 10K, 100K, 1M keys |
| 读写比例 | 100R:0W, 80R:20W, 50R:50W, 0R:100W |
| Split 场景 | sequential keys, random keys, zipfian distribution |

**3.1.3 WithLatencyMetrics Option**（`options.go`）
- 新增 `WithLatencyMetrics(enabled bool) BTreeOption`

#### 3.2 Phase 2: 低风险快速收益

**目标**：不改变核心算法，消除明显浪费。

**3.2.1 Release() 去 defer**（`page_ref.go:109-136`）

```
当前：
  defer func() { recover(); debug.PrintStack(); ... }()
  v := r.refCount.Load()  // 冗余 atomic load
  r.refCount.Add(-1)

改为：
  v := r.refCount.Add(-1) // 直接使用返回值
  if v < 0 { panic(...) }
  if v == 0 && r.freeFunc != nil && r.pageID != model.InvalidPageID {
      r.freeFunc(r.pageID)
  }
收益：消除 ~80ns/调用的 defer + debug.PrintStack + 冗余 Load
```

**3.2.2 RemoveChild 去 wasted memcpy**（`node_page.go:163-206`）

```
当前：
  Alloc → copy(4096 bytes) → InitIndexPage (wipe) → 逐条 InsertEntry

改为：
  Alloc → InitIndexPage (wipe) → 批量拷贝剩余 entries
收益：消除 4KB 无效 memcpy
```

**3.2.3 O(n²) → O(n) Update**（`leaf_page.go:142-163`）

```
当前：OverwriteLeafValue 失败 → Free + Alloc + 逐条 InsertLeafEntry (O(n²))
改为：OverwriteLeafValue 失败 → Free + Alloc + 批量 copy + 定位插入 (O(n))
```

**3.2.4 参数调优**（`constants.go`）

```
MaxCASRetries:     200 → 128
MaxParentCASSpins: 100 → 32
```
理由：128 次重试已足够应对正常竞争；32 次 parent spin 后升级为 backoff 比继续 spin 更有效。

#### 3.3 Phase 3: 深度优化

**目标**：每个优化项独立 benchmark 验证效果，可单独 revert。

**3.3.1 Parent CAS 自适应退避**（`operations.go:32-99`）
- 前 10 次迭代：保持当前 spin 模式（低延迟）
- 10-32 次：exponential backoff（1us, 2us, 4us, ... 1ms）
- 超过 32 次：返回 ErrCASConflict，让 writeOperation 外层重试

**3.3.2 Split 跳过孤儿页**（`operations.go:711-748`）
- 目标半页（需插入新 key 的那半）：跳过 BulkInit，直接构造含新 key 的最终页
- 收益：节省 1 Alloc + 1 Free per split

**3.3.3 updateChildrenCache 预分配**（`operations.go:629-690`）
- CAS 成功后分配新 slice，失败时复用引用
- 减少 GC 压力

**3.3.4 Splitting backoff 指数退避**（`operations.go:139-146`）
- 替代固定 Gosched 阈值
- 前 16 次：spin（低延迟）
- 16-64 次：time.Sleep(1us → 1ms)（跨核有效）
- 超过 64 次：外层重试

#### 3.4 代码变更清单

```
internal/infrastructure/storage/btree/
  Phase 1:
    metrics.go          # 新增 LatencyHistogram + 采样逻辑
    benchmark_test.go   # 补齐 6 类 benchmark（key/value size, concurrency, scale, ratio, distribution）
    options.go          # 新增 WithLatencyMetrics(enabled bool)
  Phase 2:
    page_ref.go         # Release() 去 defer + 去冗余 Load
    node_page.go        # RemoveChild 去 wasted 4KB memcpy
    leaf_page.go        # O(n²) → O(n) Update 回退路径
    constants.go        # MaxCASRetries 200→128, MaxParentCASSpins 100→32
  Phase 3:
    operations.go       # Parent CAS 自适应退避 + Split 跳孤儿页 + updateChildrenCache 预分配 + Splitting backoff 指数退避
```

#### 3.5 测试策略

1. **正确性**：每个 Phase 后 `go test -race -count=10 ./internal/infrastructure/storage/btree/...`
2. **性能回归**：Phase 1 的 benchmark 作为 baseline，后续 Phase 对比 before/after
3. **竞态**：`-race` 全程开启
4. **覆盖率**：保持 80%+

### 4. 风险评估与应对措施

| 风险点 | 影响等级 | 应对措施 |
|--------|----------|----------|
| O(n²) Update 修复引入正确性 bug | 高 | 已有完整测试套件 + `-race -count=10` 验证，使用批量拷贝（简单的 memcpy 语义） |
| Release() 去 defer 引入 double-free | 中 | 使用 `Add(-1)` 返回值直接判断，无需额外 Load；免费函数仅 pageID≠InvalidPageID 触发 |
| 自适应退避导致活锁 | 中 | 保留固定上限（MaxCASRetries 作为 hard limit），退避策略仅影响重试间隔 |
| 性能优化反而降低吞吐 | 中 | 每个优化项独立 benchmark，可单独 revert |
| Phase 2 优化在特定场景下退化 | 低 | 参数调优有回调空间，MaxCASRetries 128→200 仅一行改回 |
| latency metrics 引入性能回退 | 低 | 采样策略（1/64），默认关闭，仅 benchmark 时启用 |
| 优化后功能测试 flaky | 中 | `-count=10` 稳定性验证 + CI race detector |

### 5. 性能目标

#### 5.1 Phase 1 产出：完整性能基线（Apple M2, 8 核, WAL 关闭）

**纯读 QPS**：

| Benchmark | ns/op | 单线程 QPS | 8核合并 QPS |
|-----------|-------|-----------|------------|
| Get (100 key, 单线程) | 191.8 ns | **5.21M reads/s** | — |
| Get (100 key, 并行) | 148.8 ns | 6.72M/核 | **~53.8M reads/s** |
| Get (5K tree, 单线程) | 222.7 ns | **4.49M reads/s** | — |
| Get (5K tree, 并行) | 133.7 ns | 7.48M/核 | **~59.8M reads/s** |

**纯写 QPS**：

| Benchmark | 每 key 耗时 | 单线程 QPS |
|-----------|-----------|-----------|
| Preload 1000 keys | 1,027 ns/key | **~973K writes/s** |
| Preload 5000 keys | 1,019 ns/key | **~981K writes/s** |

**混合读写 (80R:20W, 并行)**：

| Benchmark | ns/op | 每核 QPS | 8核合并 QPS |
|-----------|-------|---------|------------|
| Mixed R/W Large | 187.4 ns | 5.34M/核 | **~42.7M ops/s** |

**读延迟**：

| 指标 | 值 |
|------|-----|
| Read Avg | 0.3-0.4 us |
| Read P99 | ~1 us |

> **注**：纯写 benchmark 受 COW 页面泄漏（预存 bug）限制无法长时间稳定运行，写 QPS 数据来自 Preload 批量测试。WAL 开启场景的 QPS 待下一个 PR 测量。

#### 5.2 Phase 2 目标（低风险快速收益）

| 优化项 | 预期改善 |
|--------|---------|
| Release() 去 defer | 每次 Release 节省 ~80ns |
| RemoveChild wasted memcpy | 节省 4KB memcpy/次 |
| O(n²) → O(n) Update | 大 value 更新延迟降低 100x+ |
| 参数调优 | 减少极端延迟毛刺 |

#### 5.3 Phase 3 目标

| 指标 | 目标 |
|------|------|
| 8 线程并发写线性扩展系数 | > 0.7 |
| 写 P99 延迟 | < 1ms |
| CAS 冲突率 | < 10% |

> **注**：精确目标值待 Phase 1 基线建立后设定。

### 6. 架构师评审记录（循环优化，直至通过）

| 评审轮次 | 评审日期 | 评审人（架构师） | 核心评审意见 | 优化措施（含AI辅助修改） | 优化结果 |
|----------|----------|------------------|--------------|--------------------------|----------|
| 第1轮 | 2026-XX-XX | XXX | | | |

### 7. 预审批确认
> **架构师签字/备注**：XXX 202X-XX-XX 该Feature方案可行，风险可控，同意启动开发，需严格按照文档落地，确保CI通过后提交Post总结。

---

## 第二部分：流程节点记录（开发/CI过程追溯）

### 1. 开发过程记录

| 节点 | 完成日期 | 具体内容 | 交付物 |
|------|----------|----------|--------|
| 启动开发 | 202X-XX-XX | | |
| 本地测试 | 202X-XX-XX | | |
| Post文档编写 | 202X-XX-XX | | |
| 架构师Post批准 | 202X-XX-XX | | |
| 提交GitHub | 202X-XX-XX | | |

### 2. CI流程记录（修复Bug直至通过）

| CI轮次 | 触发时间 | 结果 | 问题详情 | 修复措施 | 修复结果 |
|--------|----------|------|----------|----------|----------|
| 第1轮 | 202X-XX-XX | | | | |

### 3. 合并记录

| 合并时间 | 合并方式 | 审批人 | 备注 |
|----------|----------|--------|------|
| 202X-XX-XX | Squash Merge / Merge Commit | | |

---

## 第三部分：后置部分（CI通过后编写，总结/成果/ToDo）

### 1. 核心成果总结（开发了啥，结果怎样）

#### 1.1 功能成果
- **已完成**：
- **与Pre文档差异**：

#### 1.2 性能/数据成果
- **性能数据**：
- **测试成果**：

#### 1.3 代码/文档交付物

| 类型 | 具体内容 | 链接/路径 |
|------|----------|-----------|
| 代码变更 | | |
| 文档更新 | | |

### 2. 未完成项与ToDo清单（有哪些没干，后续规划）

#### 2.1 本次PR未完成项
- **未支持**：
- **遗留问题**：

#### 2.2 ToDo清单（优先级排序）

| 优先级 | 任务内容 | 预估工期 | 关联PR/需求 | 备注 |
|--------|----------|----------|-------------|------|
| | | | | |

### 3. 下一步工作建议
1. **优先推进**：
2. **监控要点**：
3. **运维补充**：
4. **后续规划**：
5. **反馈收集**：

---

## 文档归档信息

| 项目 | 内容 |
|------|------|
| 文档最终版本 | V1.0 |
| 归档日期 | 202X-XX-XX |
| 归档路径 | `docs/06_PM/feature/2026-05-15_PR-btree-read-write-perf-tuning_全流程.md` |
| 后续维护人 | jzhang405 |
