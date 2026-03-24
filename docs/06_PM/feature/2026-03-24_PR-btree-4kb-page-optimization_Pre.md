# 【PR全流程文档】Feature - BTree 4KB 页面大小优化

> **文档说明**：本文档包含「前置规划」和「后置总结」两部分，记录从需求对齐到开发完成的全流程，一个PR对应一份全流程文档，归档后作为项目追溯依据。

---

## 第一部分：前置部分（开工前必完成，架构师评审通过）

### 1. 基础信息（与分支/PR绑定）

| 项目 | 内容 |
|------|------|
| 工作类型 | 性能优化（Optimization） |
| PR编号 | PR-XXX（创建GitHub PR后补充完整） |
| 分支名称 | feature/btree-4kb-page-optimization |
| 工作主题 | BTree 4KB 页面大小优化 - 减少内存复制开销，提升缓存命中率 |
| 负责人 | jzhang405 |
| 分支创建日期 | 2026-03-24 |
| 计划开工日期 | 2026-03-24 |
| 计划CI通过日期 | 2026-03-26 |
| 关联需求单号 | 内部性能优化需求（BTree存储引擎） |
| 架构师评审状态 | □ 待评审 □ 评审中 □ 评审通过 □ 需优化（循环记录） |
| 预审批结果 | □ 未通过 □ 已通过（架构师签字/备注：XXX 202X-XX-XX 同意开工） |

### 2. 背景与目标（为什么干）

#### 2.1 背景

- **业务场景**：NexKV BTree 存储引擎在高并发写密集场景下存在内存复制开销大、缓存命中率低的问题

- **当前基准性能**（2026-03-24 实测，GOGC=500，builtin 模式）：
  | 并发度 | 吞吐量 | 延迟 | 扩展比 |
  |--------|--------|------|--------|
  | 1 线程 | 557K ops/sec | 1.80 μs | 1.0x |
  | 2 线程 | 1,014K ops/sec | 0.99 μs | 1.82x |
  | 4 线程 | 1,449K ops/sec | 0.69 μs | 2.60x |
  | 8 线程 | 1,648K ops/sec | 0.61 μs | 2.96x |

  **测试命令**：`GOGC=500 go run cmd/btree_perf_scheduler/main.go -threads 1,2,4,8 -count 50000 -mode builtin`

- **对比数据**（纯内存模式，来源：`docs/10_benchmark/2026-03-17_baseline/`）：
  - 单线程：484K ops/sec
  - 8 线程：530K ops/sec
  - **说明**：纯内存模式不使用 TaskScheduler，作为保守基线参考

- **现有问题**：
  1. **内存复制开销**：每次修改都克隆整个 4KB 页面，即使只修改 1 个键值对（~88 bytes）
  2. **缓存未命中**：4KB 页面远大于 L1 Cache 行（64 bytes），导致缓存未命中率增加
  3. **Delta Chain 频繁物化**：写密集场景下频繁触发 ShouldMaterialize
  4. **并发竞争**：每次写入都创建新对象（LeafPage 结构体分配），增加 GC 压力
- **价值**：
  - **理论收益**：
    - P0 优化（原地修改）：8 线程性能 +30% (~700K ops/sec)
    - P2 优化（CCOW 路径复制）：8 线程性能 +122% (~1.2M ops/sec)
    - M 方案（内存池 mmap）：8 线程性能 +200-400% (~1.5-2.5M ops/sec）

**基准数据验证**（2026-03-24）：
```bash
# 运行基准测试
GOGC=500 go run cmd/btree_perf_scheduler/main.go -threads 1,2,4,8 -count 50000 -mode builtin

# 当前实测结果（GOGC=500）
# 1 线程：557K ops/sec (1.80 μs)
# 2 线程：1,014K ops/sec (0.99 μs)
# 4 线程：1,449K ops/sec (0.69 μs)
# 8 线程：1,648K ops/sec (0.61 μs)
```

#### 2.2 核心目标（可量化、可验证）

本次 PR 聚焦 **P0 优化**（原地修改），后续 PR 逐步推进 P2 和 M 方案。

1. **功能目标**：
   - 优化 `LeafPage.Clone()` 逻辑，避免不必要的对象分配
   - 提高 Delta Chain 物化阈值，减少频繁物化
   - 实现批量 Delta 处理

2. **性能目标**（P0 优化，估算值，实际需基准测试验证）：
   - **基于 builtin 模式当前基准**（1,648K ops/sec @ 8线程）：
     - 单线程：~600K ops/sec（+8%，从当前 557K 提升）
     - 8 线程：~2.1M ops/sec（+30%，从当前 1,648K 提升）
     - 内存分配：减少 ~20%（避免不必要的 LeafPage 对象分配）
   - **保守目标**（如收益 <10%）：
     - 如 8 线程提升 <10%，调整为优先推进 P2 或 M 方案

3. **可用性目标**：
   - 并发正确性完整测试覆盖
   - 无数据竞争
   - 测试覆盖率保持 >80%

#### 2.3 明确边界（不做什么，避免范围蔓延）

- **本次不支持**（P0 阶段）：
  - ❌ M 方案：内存池 mmap（工期 12-15 天，仅限内存优化，不涉及文件持久化）
  - ❌ P2 方案：CCOW 路径复制（工期 5-10 天，后续 PR）
  - ❌ 页面大小调整（保持 4KB，不调整）

- **本次不优化**（留待后续）：
  - BTree Get/Del 操作优化
  - 路径搜索缓存
  - Leaf 分裂优化

### 3. 实现方案（怎么干，核心设计）

#### 3.1 整体流程设计

```mermaid
flowchart TD
    A[BTree.Set 调用] --> B{获取 LeafPage}
    B --> C{cowDelta 是否存在?}
    C -- 否 --> D[创建 COWDeltaRef]
    D --> E[检查 ShouldMaterialize]
    C -- 是 --> E
    E -- 需要物化 --> F[执行标准 Clone]
    E -- 不需要物化 --> G[增加引用计数]
    G --> H[返回共享 Delta Chain 的新 LeafPage]
    F --> I[深拷贝 keys/values]
    I --> H
    H --> J[Insert 添加 Delta]
    J --> K[Leaf-Level CAS]
    K --> L[返回成功/重试]
```

#### 3.2 关键设计点

1. **接口定义**：
   ```go
   // LeafPage.Clone 当前实现（已包含 Delta Chain 优化）
   // 代码位置：internal/infrastructure/storage/btree/leaf_page.go:414-447
   func (p *LeafPage) Clone(config ...*COWDeltaRefConfig) *LeafPage

   // COWDeltaRef.ShouldMaterialize 物化阈值判断
   // 优化目标：提高阈值，减少频繁物化
   // 代码位置：internal/infrastructure/storage/btree/cow_delta_ref.go:179-211
   func (ref *COWDeltaRef) ShouldMaterialize(baseSize int, refCount int32, memPressure ...bool) bool

   // COWDeltaRefConfig 配置结构
   type COWDeltaRefConfig struct {
       MaxDeltas               int     // Delta 链长度阈值（默认 10）
       DeltaRatio              float64 // 比例阈值（默认 0.2）
       HotPageThreshold        int64   // 热数据阈值
       MemoryPressureThreshold float64 // 内存压力阈值
   }
   ```

2. **核心机制**：

   **当前 Clone 实现分析**（实际代码）：
   ```go
   func (p *LeafPage) Clone(config ...*COWDeltaRefConfig) *LeafPage {
       // 情况1：已有 COW 引用，共享 Delta Chain
       if p.cowDelta != nil {
           p.cowDelta.Retain()
           return &LeafPage{
               pageID:   p.pageID,
               version:  p.version + 1,
               cowDelta: p.cowDelta,  // ← 共享，零拷贝
               keys:     p.cowDelta.GetSharedKeys(),
               values:   p.cowDelta.GetSharedValues(),
               pageLock: nil, // 性能优化：延迟创建页面锁
           }
       }

       // 情况2：创建新的 COW 引用（共享 keys/values）
       var cowRef *COWDeltaRef
       if len(config) > 0 && config[0] != nil {
           cowRef = NewCOWDeltaRefWithConfig(p.keys, p.values, config[0])
       } else {
           cowRef = NewCOWDeltaRef(p.keys, p.values)
       }

       return &LeafPage{
           pageID:   p.pageID,
           version:  p.version + 1,
           cowDelta: cowRef,
           keys:     cowRef.GetSharedKeys(),
           values:   cowRef.GetSharedValues(),
           pageLock: nil, // 性能优化：延迟创建页面锁
       }
   }
   ```

   **P0 优化策略**：
   - ✅ **已有 Delta Chain 优化**：当前代码已实现共享底层数据
   - ⚠️ **问题**：每次 Clone 仍创建新的 LeafPage 对象（~40 ns 开销）
   - 🎯 **优化方向**：
     1. 提高 `ShouldMaterialize` 阈值（减少物化频率）
     2. 优化 LeafPage 结构体分配（使用 sync.Pool）
     3. 批量 Delta 处理（减少单次 Delta 开销）

3. **数据结构**：
   - **COWDeltaRef**：Delta Chain 引用计数管理
   - **Delta**：增量记录（key, value, op）
   - **LeafPage**：叶子节点（包含 cowDelta 字段）

4. **容错设计**：
   - Delta Chain 损坏时自动降级到深拷贝
   - 引用计数泄漏检测（测试模式）
   - 并发安全性验证（race detector）

### 4. 风险评估与应对措施

| 风险点 | 影响等级 | 应对措施 |
|--------|---------|----------|
| **性能优化效果不达预期** | 中 | 1. 建立基准测试基线<br>2. 分阶段验证<br>3. 如 P0 收益 <10%，调整为 P2 优先 |
| **Delta Chain 物化阈值过高导致内存泄漏** | 高 | 1. 渐进式调整阈值（当前 10 → 20 → 40）<br>2. 添加具体监控指标：<br>   &nbsp;&nbsp;- Delta Chain 长度分布（P50/P95/P99）<br>   &nbsp;&nbsp;- 引用计数泄漏检测<br>   &nbsp;&nbsp;- 内存使用趋势监控<br>3. 测试覆盖长时间运行场景（24h 稳定性测试） |
| **并发安全性问题** | 中 | 1. 完整的并发测试覆盖<br>2. race detector 验证<br>3. stress test 验证 |
| **向后兼容性** | 低 | 1. 保持 API 不变<br>2. 内部优化不影响外部接口 |

### 5. 架构师评审记录（循环优化，直至通过）

| 评审轮次 | 评审日期 | 评审人（架构师） | 核心评审意见 | 优化措施（含AI辅助修改） | 优化结果 |
|----------|----------|------------------|--------------|--------------------------|----------|
| 第1轮 | 2026-03-24 | 待评审 | 待评审 | 待优化 | 待评审 |

### 6. 预审批确认
> **架构师签字/备注**：XXX 2026-03-XX 该Feature方案可行，风险可控，同意启动开发，需严格按照文档落地，确保CI通过后提交Post总结。

---

## 第二部分：流程节点记录（开发/CI过程追溯）

### 1. 开发过程记录

| 节点 | 完成日期 | 具体内容 | 交付物 |
|------|----------|----------|--------|
| 启动开发 | 2026-03-24 | 分析当前 Clone 实现路径 | 代码分析笔记 |
| 本地测试 | 待完成 | 单元测试 + 性能基准测试 | 测试报告 |
| Post文档编写 | 待完成 | 编写后置总结文档 | 第三部分：后置部分 |
| 架构师Post批准 | 待完成 | 架构师评审Post文档 | 批准签字/备注 |
| 提交GitHub | 待完成 | 推送分支，创建PR | GitHub PR链接 |

### 2. CI流程记录（修复Bug直至通过）

| CI轮次 | 触发时间 | 结果 | 问题详情 | 修复措施 | 修复结果 |
|--------|----------|------|----------|----------|----------|
| 第1轮 | 待提交 | 待验证 | - | - | 待验证 |

### 3. 合并记录

| 合并时间 | 合并方式 | 审批人 | 备注 |
|----------|----------|--------|------|
| 待合并 | Squash Merge / Merge Commit | 待审批 | 待补充 |

---

## 第三部分：后置部分（CI通过后编写，总结/成果/ToDo）

> ⚠️ **注**：本部分在 CI 通过后填写，当前为 Pre 状态。

### 1. 核心成果总结（开发了啥，结果怎样）

#### 1.1 功能成果
- **已完成**：待开发完成后填写
- **与Pre文档差异**：待开发完成后填写

#### 1.2 性能/数据成果
- **性能数据**：待基准测试后填写
- **测试成果**：待测试完成后填写

#### 1.3 代码/文档交付物

| 类型 | 具体内容 | 链接/路径 |
|------|----------|-----------|
| 代码变更 | 待填写 | GitHub PR链接 |
| 文档更新 | 待填写 | 文档路径 |

### 2. 未完成项与ToDo清单（有哪些没干，后续规划）

#### 2.1 本次PR未完成项
- **未支持**：
  - P2 方案：CCOW 路径复制（工期 5-10 天）
  - M 方案：mmap 内存池（工期 12-15 天）
- **遗留问题**：待开发完成后填写

#### 2.2 ToDo清单（优先级排序）

| 优先级 | 任务内容 | 预估工期 | 关联PR/需求 | 备注 |
|--------|----------|----------|-------------|------|
| 中 | P2 方案：CCOW 路径复制 | 5-10 天 | 后续 PR | 需 P0 验证收益后决策 |
| 低 | M 方案：mmap 内存池 | 12-15 天 | 后续 PR | 根本性解决，但风险高 |

### 3. 下一步工作建议（建议干啥）
1. **优先推进**：待开发完成后填写
2. **监控要点**：待开发完成后填写
3. **运维补充**：待开发完成后填写
4. **后续规划**：
   - 如 P0 收益 >20%，继续推进 P2
   - 如 P0 收益 <10%，考虑直接跳到 M 方案
5. **反馈收集**：待开发完成后填写

---

## 文档归档信息

| 项目 | 内容 |
|------|------|
| 文档最终版本 | V0.1（Pre 状态） |
| 归档日期 | 待归档 |
| 归档路径 | `docs/06_PM/feature/2026-03-24_PR-btree-4kb-page-optimization_全流程.md` |
| 后续维护人 | jzhang405 |

---

## 附录：参考资料

1. **相关 PR 文档**：
   - `2026-03-17_PR-cow-delta-chain-optimization_全流程.md`（Delta Chain 优化）
   - `2026-03-23_PR-btree-concurrent-optimization_全流程.md`（并发性能优化）

2. **当前实现代码**：
   - `internal/infrastructure/storage/btree/leaf_page.go:414-447`（Clone）
   - `internal/infrastructure/storage/btree/cow_delta_ref.go:179-211`（ShouldMaterialize）
   - `internal/domain/model/btree_types.go:136-137`（配置常量）

3. **基准测试**：
   - `internal/infrastructure/storage/btree/btree_bench_test.go`
   - `docs/10_benchmark/2026-03-17_baseline/`（性能基线数据）

4. **方案说明**：
   - **M 方案（内存池 mmap）**：仅涉及 4KB 页面的内存管理优化，使用 mmap 分配内存池以减少 GC 压力，**不涉及文件持久化**。文件持久化仍通过现有的 PageSerializer/PageDeserializer 机制实现。
