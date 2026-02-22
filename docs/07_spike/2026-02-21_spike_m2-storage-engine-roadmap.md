# M2 存储引擎层 - 实施路线图

> **预研类型**: Spike
> **创建日期**: 2026-02-21
> **最后更新**: 2026-02-22
> **分支**: `spike/m2-storage-engine`
> **状态**: ✅ 已完成

---

## 📋 关联文档

| 文档 | 说明 |
|------|------|
| [Interface 定义](./2026-02-21_spike_m2-storage-engine-interface.md) | 接口设计（总纲领性文件，10 个接口） |
| [实现方案](./2026-02-21_spike_m2-storage-engine-implement.md) | 技术实现 |
| [实施路线图](./2026-02-21_spike_m2-storage-engine-roadmap.md) | 时间规划（本文档） |
| [**DDD 架构参考**](./2026-02-18_spike_nexkv-ddd-roadmap.md) | **完整 DDD 实施路线图** |

---

## 📋 项目概览

**项目目标**：
- 实现完整存储引擎层（10 个接口）
- 双存储引擎策略：Metadata KV + External KV
- 复用现有 WAL 实现
- 满足分级性能目标

**接口清单（10 个）**：

| 接口 | 优先级 | 异步支持 |
|------|--------|---------|
| KVStore | P0 | ✅ |
| WAL | P0 | ✅ |
| BTree | P0 | ✅ |
| Iterator | P0 | - |
| LocalTx | P1 | ✅ |
| BlockDevice | P1 | ✅ |
| LocalStorage | P1 | ✅ |
| CloudStorage | P2 | ✅ |
| DistributedStorage | P2 | ✅ |
| AsyncOperation | P0 | 泛型 |

**技术栈**：
- **存储引擎**：Bf-Tree（B+ 树变体）
- **并发控制**：sync.RWMutex（MVP）
- **内存管理**：sync.Pool + GC
- **WAL**：扩展现有 `internal/wal`
- **异步接口**：AsyncOperation[T] 泛型

**总周期**：18-20 周

---

## 一、实施阶段规划

| 阶段 | 内容 | 周期 | 优先级 | 交付物 |
|------|------|------|--------|--------|
| **Phase 2.0** | AsyncOperation 泛型接口 | 1 周 | P0 | `operation.go` |
| **Phase 2.1** | Bf-Tree 核心（无持久化） | 5 周 | P0 | `tree.go`, `leaf_node.go`, `inner_node.go` |
| **Phase 2.2** | WAL 集成 + Snapshot | 4 周 | P0 | `bftree_wal.go`, `snapshot.go` |
| **Phase 2.3** | Iterator + LocalTx | 2 周 | P1 | `iterator_impl.go`, `local_tx_impl.go` |
| **Phase 2.4** | BlockDevice + LocalStorage | 2 周 | P1 | `block_device.go`, `local_storage.go` |
| **Phase 2.5** | CloudStorage + DistributedStorage | 2 周 | P2 | `cloud_*.go`, `distributed_*.go` |
| **集成测试** | 集成测试 + Bug 修复 | 2 周 | P0 | 测试报告 |

**总计**: 18-20 周

---

## 二、每周任务分解

### 2.0 Phase 2.0 详细任务（Week 0）

| 周次 | 任务 | 交付物 | 预计时间 |
|------|------|--------|----------|
| **Week 0** | AsyncOperation 泛型接口 | `operation.go` | 3 天 |
| | - AsyncOperation[T] 接口定义 | | 1 天 |
| | - 默认实现 asyncOp[T] | | 1 天 |
| | - Future 类型别名 | | 0.5 天 |
| | - 单元测试 | | 0.5 天 |

### 2.1 Phase 2.1 详细任务（Week 1-4）

| 周次 | 任务 | 交付物 | 预计时间 |
|------|------|--------|----------|
| **Week 1** | 基础设施搭建 | `config.go`, `bits.go`, `errors.go` | 5 天 |
| | - Config 模块 | | 1 天 |
| | - 位操作工具 | | 2 天 |
| | - 错误定义 | | 1 天 |
| | - 目录结构搭建 | | 1 天 |
| **Week 2** | LeafNode 实现 | `leaf_node.go` | 5 天 |
| | - 节点结构定义 | | 1 天 |
| | - 插入/删除逻辑 | | 2 天 |
| | - 查找逻辑 | | 1 天 |
| | - 单元测试 | | 1 天 |
| **Week 3** | InnerNode + PageTable | `inner_node.go`, `pagetable.go` | 5 天 |
| | - InnerNode 结构 | | 1 天 |
| | - 节点分裂/合并 | | 2 天 |
| | - PageTable 存储 | | 1 天 |
| | - 单元测试 | | 1 天 |
| **Week 4** | Tree 结构 + CRUD + 异步方法 | `tree.go`, `bftree_store.go` | 5 天 |
| | - Tree 主结构 | | 1 天 |
| | - Get/Put/Delete | | 1 天 |
| | - 异步方法实现 | | 1 天 |
| | - KVStore 适配 | | 1 天 |
| | - 集成测试 | | 1 天 |

### 2.2 Phase 2.2 详细任务（Week 5-7）

| 周次 | 任务 | 交付物 | 预计时间 |
|------|------|--------|----------|
| **Week 5** | Mini-Page 机制 | `mini_page.go` | 5 天 |
| | - 3 级 Mini-Page | | 2 天 |
| | - Delta Chain | | 2 天 |
| | - 单元测试 | | 1 天 |
| **Week 6** | WAL 集成 | `bftree_wal.go` | 5 天 |
| | - WALType 扩展 | | 1 天 |
| | - 写入集成 | | 1 天 |
| | - 异步 WAL 方法 | | 1 天 |
| | - 恢复集成 | | 1 天 |
| | - 单元测试 | | 1 天 |
| **Week 7** | Snapshot | `snapshot.go` | 5 天 |
| | - 快照创建 | | 2 天 |
| | - 快照恢复 | | 2 天 |
| | - 单元测试 | | 1 天 |

### 2.3 Phase 2.3 详细任务（Week 8-9）

| 周次 | 任务 | 交付物 | 预计时间 |
|------|------|--------|----------|
| **Week 8** | Iterator 实现 | `iterator.go` | 5 天 |
| | - Iterator 接口实现 | | 2 天 |
| | - 范围扫描逻辑 | | 2 天 |
| | - 单元测试 | | 1 天 |
| **Week 9** | LocalTx 实现 | `local_tx.go` | 5 天 |
| | - 事务操作 | | 2 天 |
| | - 异步提交/回滚 | | 1 天 |
| | - MVCC 支持 | | 1 天 |
| | - 单元测试 | | 1 天 |

### 2.4 Phase 2.4 详细任务（Week 10-11）

| 周次 | 任务 | 交付物 | 预计时间 |
|------|------|--------|----------|
| **Week 10** | BlockDevice 抽象 | `block_device.go` | 5 天 |
| | - BlockDevice 接口实现 | | 2 天 |
| | - 异步读写方法 | | 2 天 |
| | - 单元测试 | | 1 天 |
| **Week 11** | LocalStorage 实现 | `local_storage.go` | 5 天 |
| | - 本地文件存储 | | 2 天 |
| | - 预读/碎片整理 | | 2 天 |
| | - 单元测试 | | 1 天 |

### 2.5 Phase 2.5 详细任务（Week 12-13）

| 周次 | 任务 | 交付物 | 预计时间 |
|------|------|--------|----------|
| **Week 12** | CloudStorage 实现 | `s3_storage.go`, `azure_storage.go` | 5 天 |
| | - S3 适配 | | 2 天 |
| | - Azure Blob 适配 | | 2 天 |
| | - 单元测试 | | 1 天 |
| **Week 13** | DistributedStorage 实现 | `ceph_storage.go`, `minio_storage.go` | 5 天 |
| | - Ceph 适配 | | 2 天 |
| | - MinIO 适配 | | 2 天 |
| | - 单元测试 | | 1 天 |

### 2.6 集成测试详细任务（Week 14-15）

| 周次 | 任务 | 交付物 | 预计时间 |
|------|------|--------|----------|
| **Week 16** | 集成测试 | 测试报告 | 5 天 |
| | - 接口集成测试 | | 2 天 |
| | - 性能回归测试 | | 2 天 |
| | - Bug 修复 | | 1 天 |
| **Week 17** | 系统测试 | 测试报告 | 5 天 |
| | - 压力测试 | | 2 天 |
| | - 稳定性测试 | | 2 天 |
| | - 文档完善 | | 1 天 |

---

## 三、里程碑时间表

```mermaid
gantt
    title M2 存储引擎层实施时间表
    dateFormat  YYYY-MM-DD
    section Phase 2.0
    AsyncOperation         :a0, 2026-03-01, 1w
    section Phase 2.1
    基础设施搭建           :a1, after a0, 1w
    LeafNode 实现          :a2, after a1, 1w
    InnerNode + PageTable  :a3, after a2, 1w
    Tree 结构 + CRUD       :a4, after a3, 1w
    section Phase 2.2
    Mini-Page 机制         :b1, after a4, 1w
    WAL 集成              :b2, after b1, 1w
    Snapshot              :b3, after b2, 1w
    section Phase 2.3
    Iterator + LocalTx    :c1, after b3, 2w
    section Phase 2.4
    BlockDevice 抽象      :d1, after c1, 2w
    section Phase 2.5
    Cloud/Distributed     :e1, after d1, 2w
```

### 里程碑定义

| 里程碑 | 完成时间 | 验收标准 |
|--------|---------|---------|
| **M0: AsyncOperation 完成** | Week 0 | 泛型接口实现，单元测试通过 |
| **M1: Bf-Tree 核心完成** | Week 5 | Get/Put/Delete 功能正常，同步+异步方法通过 |
| **M2: WAL 集成完成** | Week 9 | 崩溃恢复测试通过，数据不丢失 |
| **M3: 存储引擎层完成** | Week 17 | 所有 10 个接口实现完成，集成测试通过，性能测试达标 |

---

## 四、时间线对比分析

### 4.1 路线图 M2（6 周）vs 完整计划（18-20 周）

| 维度 | 原路线图 | 完整计划 | 说明 |
|------|---------|----------|------|
| **周期** | 6 周 | 18-20 周 | 完整计划更全面，包含缓冲时间 |
| **接口数量** | 5 个 | 10 个 | 包含异步接口和块设备层 |
| **异步支持** | P2 计划 | P0 内置 | AsyncOperation 泛型接口 |
| **块设备层** | 未提及 | 完整实现 | Local/Cloud/Distributed |
| **集成测试** | 未提及 | 2 周 | 确保质量 |

**结论**：采用完整计划，确保所有接口实现。

---

## 五、性能目标分级

> **注意**：性能目标基于 MVP 简化实现（sync.RWMutex + sync.Pool），与 benchmark 文档保持一致。
>
> **文档一致性要求**：
> - ✅ 本文档（roadmap）定义性能目标
> - ✅ benchmark 文档必须使用相同的目标值
> - ✅ 任何调整需要同步更新所有相关文档

| 操作 | P0（最低） | P1（推荐） | P2（理想） |
|------|-----------|-----------|-----------|
| **点查询（同步）** | < 50μs | < 30μs | < 20μs |
| **点查询（异步）** | < 60μs | < 40μs | < 25μs |
| **写入吞吐（同步）** | > 5万 ops/s | > 10万 ops/s | > 20万 ops/s |
| **写入吞吐（异步）** | > 8万 ops/s | > 15万 ops/s | > 30万 ops/s |
| **范围查询** | O(log N + M) | O(log N + M) | O(log N + M) |

**与 Rust 原版对比**：

| 操作 | Rust 原版 | Go MVP P0 | 差距 |
|------|----------|----------|------|
| 点查询 | 10μs | 50μs | 5x |
| 写入吞吐 | 200万 ops/s | 5万 ops/s | 40x |

**性能目标分级说明**：

- **P0（最低）**：MVP 阶段必须达到的最低性能，可接受部署
- **P1（推荐）**：生产环境推荐性能，经过优化后可实现
- **P2（理想）**：理想性能目标，需要深度优化（如 Lock-free SMR）

**性能差距原因**：

1. **GC 开销**：Go GC 暂停（10-50ms）vs Rust 无 GC
2. **并发控制**：sync.RWMutex vs Lock-free SMR
3. **内存管理**：sync.Pool vs 手动内存池
4. **编译优化**：Go 编译器 vs Rust LLVM 优化

---

## 六、风险与缓解

| 风险 | 可能性 | 影响 | 缓解措施 |
|------|--------|------|---------|
| Bf-Tree 移植复杂度超预期 | 中 | 高 | 采用简化 MVP 策略 |
| 性能不达标 | 中 | 中 | 分级性能目标（P0/P1/P2） |
| WAL 集成困难 | 低 | 中 | 现有 WAL 已验证 |
| 内存占用过高 | 中 | 中 | Mini-Page 分级管理 |
| 并发 Bug | 中 | 高 | 使用 `go test -race` 检测 |
| 异步接口复杂度 | 中 | 中 | 使用泛型统一接口 |
| 云存储 SDK 兼容性 | 低 | 低 | 标准化接口抽象 |

---

## 七、决策状态追踪

| 决策 | 选项 | 建议 | 状态 |
|------|------|------|------|
| **WAL 实现** | 新写 vs 复用 | ✅ 复用现有 WAL | 已确定 |
| **并发控制** | Lock-free SMR vs sync.RWMutex | sync.RWMutex（MVP） | 已确定 |
| **内存管理** | 手动 vs GC | sync.Pool + GC（MVP） | 已确定 |
| **Mini-Page 级别** | 3 级 vs 6+ 级 | 3 级（MVP） | 已确定 |
| **目录结构** | DDD vs 传统 | DDD 架构 | 已确定 |
| **异步接口** | 多种 Future vs 泛型 | AsyncOperation[T] | 已确定 |
| **块设备层** | 单一 vs 多种 | 可插拔设计 | 已确定 |

---

## 八、质量保证

### 8.1 测试覆盖率要求

| 组件 | 覆盖率要求 |
|------|-----------|
| AsyncOperation | ≥ 90% |
| BfTree 核心 | ≥ 80% |
| WAL 集成 | ≥ 80% |
| KVStore 适配 | ≥ 80% |
| BlockDevice 层 | ≥ 80% |
| 整体覆盖率 | ≥ 80% |

### 8.2 提交前检查

```bash
# 完整的本地验证流程
make build     # 编译项目
make lint      # 代码质量检查
make test      # 运行所有测试
make test-race # 竞态检测
make fmt       # 格式化代码
make clean     # 清理编译文件
```

### 8.3 CI 验证

- ✅ 单元测试通过
- ✅ 竞态检测通过（`go test -race`）
- ✅ 代码覆盖率 ≥ 80%
- ✅ Lint 检查通过

### 8.4 测试类型覆盖

| 测试类型 | 工具/命令 | 说明 |
|---------|----------|------|
| **单元测试** | `go test ./...` | 每个模块独立测试 |
| **竞态检测** | `go test -race` | 并发安全验证 |
| **崩溃恢复** | `go test -tags=crash` | WAL 恢复测试 |
| **模糊测试** | `go test -fuzz=. -fuzztime=5m` | 随机输入测试 |
| **基准测试** | `go test -bench=. -benchtime=10s` | 性能回归检测 |
| **压力测试** | `./scripts/stress_test.sh` | 高负载稳定性 |

---

## 九、下一步行动

### 9.1 立即行动（Week 0）

1. **实现 AsyncOperation 泛型接口**
   - 位置：`internal/domain/async/operation.go`
   - 参考：[DDD 架构参考 - AsyncOperation](./2026-02-18_spike_nexkv-ddd-interface.md#226-asyncoperation---统一异步操作接口)

2. **定义完整接口** - `internal/domain/service/storage.go`
   ```go
   // KVStore 单机 KV 存储接口（同步+异步统一）
   type KVStore interface {
       // 同步读写
       Get(ctx context.Context, key []byte) ([]byte, error)
       Set(ctx context.Context, key, value []byte) error
       Delete(ctx context.Context, key []byte) error

       // 异步读写
       GetAsync(ctx context.Context, key []byte) ReadFuture
       SetAsync(ctx context.Context, key, value []byte) WriteFuture
       DeleteAsync(ctx context.Context, key []byte) WriteFuture

       // 范围查询
       Scan(ctx context.Context, start, end []byte) (Iterator, error)
       ScanAsync(ctx context.Context, start, end []byte) IteratorFuture

       // 批量操作
       BatchGet(ctx context.Context, keys [][]byte) (map[string][]byte, error)
       BatchSet(ctx context.Context, kvs []KeyValue) error
       BatchGetAsync(ctx context.Context, keys [][]byte) BatchGetFuture
       BatchSetAsync(ctx context.Context, kvs []KeyValue) WriteFuture

       // 事务支持
       NewTx() (LocalTx, error)

       // 资源管理
       Close() error
       Sync() error
       SyncAsync(ctx context.Context) WriteFuture
   }
   ```

3. **搭建骨架** - 目录结构和基础类型定义
   ```
   internal/
   ├── domain/
   │   ├── service/
   │   │   ├── storage.go      # 存储接口定义
   │   │   └── blockdevice.go  # 块设备接口定义
   │   └── async/
   │       └── operation.go    # AsyncOperation 泛型接口
   │
   └── infrastructure/
       └── storage/
           ├── metadata/       # Metadata KV 实现
           ├── bftree/         # Bf-Tree 实现
           ├── block/          # BlockDevice 基础实现
           ├── local/          # LocalStorage 实现
           ├── cloud/          # CloudStorage 实现
           └── distributed/    # DistributedStorage 实现
   ```

---

**文档版本**: v2.0
**创建日期**: 2026-02-21
**最后更新**: 2026-02-22
**维护者**: NexKV 开发团队
**状态**: ✅ 已完成
