# 【PR全流程文档】Feature - btree_bench KV Set 落盘 Benchmark

> **文档说明**：本文档包含「前置规划」和「后置总结」两部分，记录 btree_bench 增加 KV Set 落盘（WAL + AO 文件写入）基准测试的全流程。

---

## 第一部分：前置部分（开工前必完成）

### 1. 基础信息（与分支/PR绑定）

| 项目 | 内容 |
|------|------|
| 工作类型 | 新功能开发（Feature） |
| PR编号 | PR-XXX（创建GitHub PR后补充完整） |
| 分支名称 | `docs/btree-bench-persistence-benchmark` |
| 工作主题 | btree_bench：KV Set 落盘（WAL + AO 文件写入）吞吐量基准测试 |
| 负责人 | jzhang405 |
| 分支创建日期 | 2026-06-07 |
| 计划完成日期 | 2026-06-10 |
| 关联需求/Issue | 存储引擎持久化性能度量 |
| 更新类型 | ☑ 新增功能（在 btree_bench 中新增落盘 benchmark） |

### 2. 背景与目标（为什么需要）

#### 2.1 背景

- **当前状态**：`cmd/tools/btree_bench` 已实现 BTree KV 纯内存操作（Set/Get/BatchSet/BatchGet）的吞吐量基准测试，覆盖顺序/并发/混合读写等多维度场景。
- **缺失能力**：现有 benchmark 仅度量 mmap 内存页面的 COW 操作性能，**未覆盖落盘路径**——即 WAL（Write-Ahead Log）写入和 AO（Append-Only Chunk）文件写入。
- **业务价值**：在 NexKV 的存储架构中，数据持久化是最关键的 I/O 路径。Set 操作经过 BTree COW → WAL 序列化 → WAL 文件 fwrite/fsync → ChunkManager 页面序列化 → AO 文件写入，这一整条链路是实际生产环境的真实写入路径，必须在 benchmark 中可度量。

#### 2.2 目标

1. **准确性**：benchmark 度量结果反映真实落盘路径的吞吐量，包含 WAL 序列化/写入/Sync 和 AO 页面写入的完整耗时。
2. **可控性**：通过命令行 flag 控制 WAL Sync 策略（EveryWrite / GroupCommit / EverySecond）、AO 文件大小、是否启用落盘，方便对比纯内存 vs 落盘性能。
3. **可观测性**：输出落盘相关的关键指标：WAL 写入字节数、Sync 次数、AO 文件写入次数、ChunkManager 统计信息。

#### 2.3 明确边界

- **本次实现**：
  - 在 `cmd/tools/btree_bench` 中新增 `-persist` flag 控制的落盘模式
  - 落盘模式下，每个 Set 操作经过完整的 BTree → WAL → AO 路径
  - 新增 WAL Sync 策略选择 flag（`-wal-sync`）
  - 输出落盘相关的吞吐量和 I/O 统计
- **暂不实现**：
  - 落盘模式下的 BatchSet/BatchGet（Phase 2）
  - 落盘 + 多节点复制场景
  - 落盘恢复后的正确性验证（由集成测试覆盖）

### 3. 技术设计

#### 3.1 落盘数据流

```
Set(key, value)
    │
    ▼
┌──────────────────────────────────────────────────┐
│ 1. BTree.Set()                                   │
│    ├─ COW 页面分配（OffheapBTreeStorage.AllocXXX）│
│    ├─ MVCC 值编码（mvcc.BuildMVCC）               │
│    └─ 页面 CAS 替换                              │
└──────────────────────────────────────────────────┘
    │
    ▼
┌──────────────────────────────────────────────────┐
│ 2. WAL 写入（落盘第一步）                         │
│    ├─ WALEntry 序列化（MarshalWALEntry）          │
│    │   └─ 二进制格式：[CRC32C:4][Len:4][LSN:8]   │
│    │                 [Type:1]...[Key:N][Value:M]  │
│    ├─ DiskWAL.Append() → fwrite                   │
│    └─ SyncPolicy 控制 fsync 行为                  │
│        ├─ EveryWrite: 每条 fsync                  │
│        ├─ GroupCommit: 批量 fsync（16条/1ms）      │
│        └─ EverySecond: 每秒 fsync                 │
└──────────────────────────────────────────────────┘
    │
    ▼
┌──────────────────────────────────────────────────┐
│ 3. AO 页面持久化（落盘第二步）                     │
│    ├─ PageSerializer.Serialize(page) → []byte     │
│    ├─ ChunkManager.Allocate(size, pageType)       │
│    │   └─ 在 .ao chunk 文件中预留位置              │
│    ├─ ChunkManager.WritePage(pos, data)            │
│    │   └─ 写入 .ao 文件（256MB/段）               │
│    └─ Storage.UpdatePageLocs(mapping)             │
│        └─ 记录 pageID → ChunkPosition 映射        │
└──────────────────────────────────────────────────┘
```

#### 3.2 新增 Flag 设计

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `-persist` | bool | false | 启用落盘模式（WAL + AO 全路径） |
| `-wal-sync` | string | `every-write` | WAL Sync 策略：`every-write` / `group-commit` / `every-second` |
| `-wal-dir` | string | `$TMPDIR/nexkv-bench-wal` | WAL 文件目录 |
| `-ao-dir` | string | `$TMPDIR/nexkv-bench-ao` | AO chunk 文件目录 |
| `-ao-chunk-size` | int | 256（MB） | 单个 AO chunk 文件大小上限 |

#### 3.3 Benchmark 场景

| 场景名称 | 落盘 | WAL Sync | 线程数 | 说明 |
|----------|------|----------|--------|------|
| `seq-put-persist-every-write` | ✔ | EveryWrite | 1 | 最强持久化保证下的顺序写入 |
| `seq-put-persist-group-commit` | ✔ | GroupCommit | 1 | 批量 fsync 优化下的顺序写入 |
| `seq-put-persist-every-second` | ✔ | EverySecond | 1 | 延迟 fsync 下的顺序写入（最大吞吐） |
| `seq-put-mem` | ✘ | - | 1 | 纯内存顺序写入（对照组） |
| `par-put-persist-4` | ✔ | GroupCommit | 4 | 4 线程并发落盘写入 |
| `par-put-persist-8` | ✔ | GroupCommit | 8 | 8 线程并发落盘写入 |
| `par-put-persist-16` | ✔ | GroupCommit | 16 | 16 线程并发落盘写入 |
| `par-put-mem-4` | ✘ | - | 4 | 纯内存并发写入（对照组） |

#### 3.4 输出指标

除现有 QPS 外，落盘模式下新增：

```
=== Persistence Stats ===
wal.segments        : 1           # WAL 段文件数量
wal.written_bytes   : 256MB       # WAL 总写入字节
wal.sync_count      : 1024        # fsync 调用次数
wal.avg_entry_size  : 238 bytes   # 平均 WAL Entry 大小
ao.chunks           : 2           # AO chunk 文件数量
ao.written_pages    : 65536       # 持久化页面数量
ao.written_bytes    : 512MB       # AO 总写入字节
ao.avg_page_size    : 4096 bytes  # 平均序列化页面大小
```

#### 3.5 代码结构

```
cmd/tools/btree_bench/
├── main.go              # 现有：纯内存 benchmark（不改动）
├── main_test.go         # 新增：单元测试
├── persist.go           # 新增：落盘相关逻辑
│   ├── newPersistStorage()  # 创建带 WAL+AO 的 OffheapBTreeStorage
│   ├── persistSetLoop()     # 落盘 Set 循环
│   └── printPersistStats()  # 输出落盘统计
└── wal_bench.go         # 新增：WAL 独立 benchmark
    └── runWALBench()       # 纯 WAL Append 吞吐量测试
```

### 4. 预期结果与风险

#### 4.1 预期性能（MacBook Pro M-series, 本地 SSD）

| 场景 | 预期 QPS | 说明 |
|------|----------|------|
| `seq-put-mem` | ~3,400,000 | 当前基线（纯内存） |
| `seq-put-persist-every-write` | ~15,000–30,000 | 每条 fsync，受磁盘 fsync 延迟限制 |
| `seq-put-persist-group-commit` | ~100,000–200,000 | 批量 fsync（16条/批），大幅减少 fsync 次数 |
| `seq-put-persist-every-second` | ~500,000–1,000,000 | 延迟 Sync，接近内存性能 |
| `par-put-persist-8` | ~200,000–500,000 | 多线程并发 + GroupCommit |

#### 4.2 风险

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| 落盘目录残留占用磁盘 | 低 | benchmark 结束后自动清理 WAL/AO 临时目录 |
| 落盘模式下 mmap 扩容 | 中 | 增大 mmap 初始大小或限制操作总数 |
| WAL + AO 双重写入放大 | 中 | 度量并记录写入放大比（写入放大比 = (WAL_Bytes + AO_Bytes) / UserData_Bytes） |

### 5. 评审要点

| 评审项 | 检查内容 | 评审人 | 评审结果 |
|--------|---------|--------|---------|
| 落盘数据流正确性 | Set → WAL → AO 路径是否完整，是否遗漏关键步骤 | 架构师 | □ 通过 □ 需修改 |
| Flag 设计合理性 | 命令行参数是否清晰，默认值是否合理 | 架构师 | □ 通过 □ 需修改 |
| 与现有 benchmark 的兼容性 | 不启用 `-persist` 时行为是否与当前完全一致 | 架构师 | □ 通过 □ 需修改 |
| 临时文件清理 | benchmark 结束后是否清理所有临时文件 | 架构师 | □ 通过 □ 需修改 |
| 输出格式一致性 | 新增输出是否与现有输出风格一致 | 架构师 | □ 通过 □ 需修改 |

### 6. 评审记录

| 评审轮次 | 评审日期 | 评审人 | 核心意见 | 修改措施 | 完成状态 |
|----------|----------|--------|---------|---------|---------|
| 第1轮 | （待定） | 架构师 | - | - | 待评审 |

---

## 第二部分：流程节点记录

### 1. 开发过程记录

| 节点 | 完成日期 | 具体内容 | 交付物 |
|------|----------|----------|--------|
| Pre文档编写 | 2026-06-07 | 编写前置规划文档 | 本文件（第一部分） |
| 架构师Pre批准 | （待定） | 架构师评审Pre文档 | 批准签字 |
| 代码实现 | （待定） | 实现 persist.go + wal_bench.go | 代码 |
| 代码评审 | （待定） | code-reviewer 评审 | 评审意见 |
| Post文档编写 | （待定） | 编写后置总结文档 | 本文件（第三部分） |
| 架构师Post批准 | （待定） | 架构师评审Post文档 | 批准签字 |
| 提交GitHub | （待定） | 创建PR | PR链接 |

### 2. CI流程记录

| CI轮次 | 触发时间 | 结果 | 问题详情 | 修复措施 | 修复结果 |
|--------|----------|------|----------|----------|----------|
| 第1轮 | （待定） | - | - | - | - |

### 3. 合并记录

| 合并时间 | 合并方式 | 审批人 | 备注 |
|----------|----------|--------|------|
| （待定） | Squash Merge | 架构师 | - |

---

## 第三部分：后置部分（PR合并后编写）

### 1. 开发成果总结

#### 1.1 完成情况
- **新增文件**：（待实现后填写）
- **修改文件**：（待实现后填写）
- **变更统计**：（待实现后填写）
- **与Pre文档差异**：（待实现后填写）

#### 1.2 质量验证
- **单元测试**：（待填写）
- **Benchmark 结果**：（待填写）
- **代码覆盖率**：（待填写）

#### 1.3 实际 Bench 结果

（实现后填写实际 QPS 数据）

| 场景 | QPS | WAL Bytes | AO Bytes | 写入放大比 |
|------|-----|-----------|----------|-----------|
| `seq-put-mem` | - | - | - | - |
| `seq-put-persist-every-write` | - | - | - | - |
| `seq-put-persist-group-commit` | - | - | - | - |
| `seq-put-persist-every-second` | - | - | - | - |
| `par-put-persist-8` | - | - | - | - |

#### 1.4 交付物清单

| 类型 | 具体内容 | 链接/路径 |
|------|----------|-----------|
| 新增文件 | persist.go | `cmd/tools/btree_bench/persist.go` |
| 新增文件 | wal_bench.go | `cmd/tools/btree_bench/wal_bench.go` |
| 新增文件 | main_test.go | `cmd/tools/btree_bench/main_test.go` |
| 修改文件 | main.go | `cmd/tools/btree_bench/main.go` |

### 2. 后续优化建议

#### 2.1 未完成项
- **Batch 落盘**：暂不实现 BatchSet/BatchGet 的落盘路径
- **多节点复制**：落盘 + Gossip 复制的联动 benchmark
- **恢复验证**：落盘数据重启后的正确性自动验证

#### 2.2 ToDo清单

| 优先级 | 任务内容 | 预估时间 | 关联PR | 备注 |
|--------|----------|---------|--------|------|
| 中 | BatchSet 落盘 benchmark | 4h | - | Phase 2 |
| 低 | WAL 恢复正确性自动化测试 | 8h | - | 集成测试 |
| 低 | 落盘 + 压缩联动 benchmark | 6h | - | 压缩对落盘的影响 |

### 3. 维护建议

1. **定期运行**：每次存储引擎变更后，运行落盘 benchmark 对比性能回归
2. **多平台数据**：收集 Linux/macOS、SSD/HDD 环境下的基准数据，建立性能基线
3. **CI 集成**：将 `-persist -n 100000` 的快速 smoke test 集成到 CI 中

---

## 文档归档信息

| 项目 | 内容 |
|------|------|
| 文档最终版本 | V1.0 |
| 归档日期 | 2026-06-07 |
| 归档路径 | `docs/06_PM/feature/2026-06-07_PR-btree-bench-persistence_Pre.md` |
| 后续维护人 | jzhang405 |
