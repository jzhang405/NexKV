# PR-089 剩余工作清单

> **创建日期**: 2026-03-07
> **分支**: `feature/m2-bftree-p1-p2-optimization`
> **基于**: PR-089 Phase 2.1+2.2+2.3 合并后

## 📊 总体完成情况

### ✅ 已完成 (Phase 2.1 + 2.2 + 2.3 P0)

- ✅ WAL 完整实现（覆盖率 82.0%）
- ✅ Bf-Tree 核心数据结构
- ✅ Mini-Page 机制（3-level）
- ✅ Delta Chain 基础实现
- ✅ CRUD 接口（同步 + 异步）
- ✅ 范围查询（Iterator）
- ✅ 节点分裂逻辑
- ✅ 测试覆盖率 77.2%

### ⚠️ 剩余工作

## 🔥 P1 高优先级任务

### 1. BitmapLock 细粒度锁实现 (2 周)

**目标**: 从 `sync.RWMutex`（全局锁）升级到细粒度锁，减少锁竞争

**设计要点**:
- 使用 bitmap 表示页面锁状态
- 支持分片策略（默认 16 分片）
- 实现 Lock/Unlock/TryLock
- 集成到 Bf-Tree

**文件**:
```
internal/infrastructure/storage/bftree/
├── bitmaplock.go         # BitmapLock 实现
├── bitmaplock_test.go    # 单元测试
```

**关键接口**:
```go
type BitmapLock struct {
    shards    []*lockShard
    shardMask uint64
}

func (l *BitmapLock) Lock(pageID uint64)
func (l *BitmapLock) Unlock(pageID uint64)
func (l *BitmapLock) TryLock(pageID uint64) bool
func (l *BitmapLock) RLock(pageID uint64)
func (l *BitmapLock) RUnlock(pageID uint64)
```

**验收标准**:
- [ ] 单元测试通过
- [ ] 并发测试通过（race detector）
- [ ] 性能对比：读性能提升 50%~100%
- [ ] 测试覆盖率 ≥ 80%

### 2. 节点合并逻辑完善 (3-5 天)

**当前状态**: 骨架存在，但标记为 `TODO: 实现完整版本`

**文件**: `internal/infrastructure/storage/bftree/merge.go`

**需要实现**:
- [ ] MergeLeafNodes 完整实现
- [ ] MergeInnerNodes 完整实现
- [ ] 父节点更新逻辑
- [ ] 合并阈值检测
- [ ] 合并后的页面回收

**验收标准**:
- [x] 单元测试覆盖所有合并场景
- [x] 与分裂逻辑协同工作
- [x] 页面利用率保持健康水平

## 🎯 P2 中优先级任务

### 3. Delta Chain 性能优化 (1 周)

**当前状态**: 基础实现已完成，需要性能优化

**优化方向**:
- [ ] Delta Chain 大小限制优化
- [ ] 合并策略优化
- [ ] 减少内存拷贝
- [ ] 批量合并

**性能目标**:
- 写入放大降低 30%
- 内存占用降低 20%

### 4. 压缩算法实现 (1 周)

**目标**: 减少 Bf-Tree 存储空间占用

**设计要点**:
- 支持多种压缩算法（Snappy, ZSTD, LZ4）
- 页面级压缩
- 配置化压缩策略

**文件**:
```
internal/infrastructure/storage/bftree/
├── compression.go       # 压缩接口
├── compressor_snappy.go
├── compressor_zstd.go
├── compressor_lz4.go
```

**验收标准**:
- [ ] 支持至少 2 种压缩算法
- [ ] 压缩比 ≥ 50%
- [ ] 性能损失 ≤ 20%

## 📋 P3 低优先级任务

### 5. 云存储后端 (2 周)

**目标**: 支持 S3、Azure Blob 等云存储

**设计要点**:
- 实现 `PageStore` 接口
- 支持多云后端
- 缓存策略优化

**关联 PR**: PR-093

## 📈 性能基准目标

### 当前性能 (P0 基线)

| 操作 | 当前性能 | P1 目标 | P2 目标 |
|------|----------|---------|---------|
| 点查询 (Get) | ~80μs | < 60μs | < 40μs |
| 写入 (Set) | ~150μs | < 100μs | < 80μs |
| 范围查询 (Scan) | ~3ms | < 2ms | < 1.5ms |
| 并发吞吐 | ~10K ops/s | > 15K ops/s | > 20K ops/s |

### 测试覆盖目标

- [ ] 单元测试覆盖率 ≥ 85%
- [ ] 并发测试覆盖率 ≥ 80%
- [ ] 性能测试通过

## 🧪 测试计划

### 并发测试
```bash
# 运行并发测试
go test -race -v ./internal/infrastructure/storage/bftree/...

# 运行性能基准
go test -bench=. -benchmem ./internal/infrastructure/storage/bftree/...
```

### 对比测试
- [ ] 与 BoltDB 性能对比
- [ ] P0 vs P1 vs P2 性能对比
- [ ] 并发性能对比

## 📅 实施计划

| 阶段 | 任务 | 预估时间 | 依赖 |
|------|------|----------|------|
| Week 1 | BitmapLock 实现 | 3 天 | - |
| Week 1 | BitmapLock 集成测试 | 2 天 | BitmapLock |
| Week 2 | 节点合并完善 | 3 天 | - |
| Week 2 | Delta Chain 优化 | 2 天 | - |
| Week 3 | 压缩算法实现 | 3 天 | - |
| Week 3 | 性能测试与调优 | 2 天 | 所有任务 |

## ✅ 检查清单

### 代码质量
- [ ] 代码通过 golangci-lint
- [ ] 代码通过 go vet
- [ ] 代码格式化（gofmt）
- [ ] 所有 TODO 已处理或记录

### 测试质量
- [ ] 单元测试覆盖率 ≥ 85%
- [ ] 并发测试通过（race detector）
- [ ] 性能测试通过
- [ ] 集成测试通过

### 文档质量
- [ ] API 文档完整
- [ ] 设计文档更新
- [ ] 性能测试报告

## 🔗 相关文档

- **主文档**: `docs/06_PM/feature/2026-03-01_PR-089_m2-bftree-core_Pre.md`
- **Spike 文档**: `docs/07_spike/2026-02-21_spike_m2-storage-engine-roadmap.md`
- **代码审查**: `docs/09_code-review/`

## 📝 备注

- 所有 P1 任务必须在本次 PR 完成
- P2 任务可根据实际情况调整优先级
- 云存储后端延后到 PR-093
- 持续监控性能指标，确保优化有效
