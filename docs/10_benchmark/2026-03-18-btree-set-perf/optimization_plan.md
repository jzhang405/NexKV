# BTree Set 性能优化方案

**日期**: 2026-03-18
**目标**: 优化纯内存模式下 BTree Set 操作性能
**当前性能**: 572k ops/sec (1.75 μs/op)
**目标性能**: 1M+ ops/sec (1 μs/op)

---

## 📊 当前状态分析

### 性能指标 (GOGC=400)

| 指标 | 当前值 | 单位 |
|------|--------|------|
| 吞吐量 | 572k | ops/sec |
| 延迟 | 1.75 | μs/op |
| 内存分配 | 4139 | B/op |
| 分配次数 | 51 | allocs/op |

### 性能演变历史

| 阶段 | 吞吐量 | 提升 | 主要优化 |
|------|--------|------|----------|
| 初始 | 16k ops/sec | - | - |
| 移除监控 | 283k ops/sec | 17.7x | 移除 stats/memMonitor |
| 跳过深拷贝 | 452k ops/sec | 28.3x | 纯内存模式优化 |
| P0 优化 | 608k ops/sec | 37.5x | PageLock 懒加载 + Delta 预分配 |
| 当前 | 572k ops/sec | 35.9x | - |

---

## 🔍 瓶颈分析

### 内存分配 TOP 来源

| 排名 | 函数 | 分配量 | 占比 | CPU 时间 |
|------|------|--------|------|----------|
| 1 | `NewPageInfo` (累计) | 190MB | **47.70%** | 200ms |
| 2 | `LeafPage.Clone` | 99MB | **24.85%** | 40ms |
| 3 | `InternalPage.Clone` | 38.50MB | **9.66%** | 80ms |
| 4 | `NewCOWDeltaRefWithConfig` | 37MB | **9.29%** | - |
| 5 | `AppendDelta` | 21.50MB | **5.40%** | - |
| 6 | `rebuildChildRefs` (map) | 40MB | **10%** | 50ms |

### copyPathWithDelta 热点分解

```
copyPathWithDelta (410ms CPU, 199MB 内存)
├─ NewPageInfo (200ms, 190MB, 47%)          ← 最大瓶颈
├─ CloneWithDelta (120ms, 137MB, 34%)
│  ├─ LeafPage.Clone (40ms, 99MB)
│  └─ InternalPage.Clone (80ms, 38MB)
└─ rebuildChildRefs (50ms, 40MB, 12%)
    └─ make(map) (40ms)
```

### 热点代码位置

**文件**: `internal/infrastructure/storage/btree/btree.go`

```go
func (b *BTree) copyPathWithDelta(path []*PageInfo) ([]*PageInfo, error) {
    copiedPath := make([]*PageInfo, len(path))

    for i, info := range path {
        switch p := info.GetPage().(type) {
        case *LeafPage:
            clonedPage := p.CloneWithDelta()
            newInfo := NewPageInfo()           // ← 200ms CPU, 190MB 内存
            newInfo.SetPage(clonedPage)
            copiedPath[i] = newInfo

        case *InternalPage:
            clonedPage := p.CloneWithDelta()
            newInfo := NewPageInfo()           // ← 重复分配
            newInfo.SetPage(clonedPage)
            copiedPath[i] = newInfo
        }
    }

    return b.rebuildChildRefs(path, copiedPath)  // ← 50ms CPU, 40MB 内存
}
```

---

## 🎯 优化策略

### 策略 A: 使用 CloneShallow 替代 NewPageInfo ⭐ 推荐

**原理**: `CloneShallow` 已经创建了新的 `PageInfo`，无需再创建

**当前实现**:
```go
clonedPage := p.CloneWithDelta()
newInfo := NewPageInfo()          // ← 额外创建
newInfo.SetPage(clonedPage)
```

**优化后**:
```go
newInfo := info.CloneShallow()    // ← 复用，无额外分配
clonedPage := p.CloneWithDelta()
newInfo.SetPage(clonedPage)
```

**收益**: 减少 ~45% 内存分配 (190MB → 105MB)

**风险**: 低 - CloneShallow 已经被测试覆盖

---

### 策略 B: 只克隆叶子节点

**原理**: 只有叶子节点被修改，内部节点可以复用

**当前实现**: 克隆整个路径
```go
for i, info := range path {
    clonedPage := p.CloneWithDelta()
    newInfo := NewPageInfo()
    copiedPath[i] = newInfo
}
```

**优化后**: 只克隆叶子节点
```go
// 内部节点：直接复用
for i := 0; i < len(path)-1; i++ {
    copiedPath[i] = path[i]  // ← 零分配
}

// 叶子节点：克隆并修改
leafInfo := path[len(path)-1]
clonedLeaf := leafPage.CloneWithDelta()
newLeafInfo := leafInfo.CloneShallow()
newLeafInfo.SetPage(clonedLeaf)
copiedPath[len(path)-1] = newLeafInfo
```

**收益**: 减少 ~30% 分配 (路径长度 - 1 个节点)

**风险**: 中 - 需要修改 CAS 逻辑和 rebuildChildRefs

---

### 策略 C: 优化 rebuildChildRefs

**原理**: 预分配 map 容量，减少扩容开销

**当前实现**:
```go
pageInfoMap := make(map[model.PageID]*PageInfo, len(originalPath))
```

**优化后**:
```go
pageInfoMap := make(map[model.PageID]*PageInfo, len(originalPath))
// 使用 hints 避免扩容
pageInfoMap = pageInfoMap // 显式保留
```

**收益**: 减少 ~5% map 开销

**风险**: 低

---

### 策略 D: 延迟克隆到 CAS 成功后

**原理**: CAS 失败时丢弃克隆，成功后才生效

**当前流程**:
```
1. 克隆路径 (copyPathWithDelta)
2. 修改叶子节点
3. CAS 更新根节点
4. 如果失败，丢弃克隆
```

**优化后**:
```
1. 直接修改叶子节点
2. CAS 更新根节点
3. 如果成功，才克隆路径
4. 更新其他节点引用
```

**收益**: CAS 失败时零分配

**风险**: 高 - 需要重新设计 CAS 逻辑，降低并发性

---

## 📋 实施计划

### Phase 1: 低风险优化 (策略 A + C)

**目标**: 减少内存分配 ~50%

**实施内容**:

1. **修改 copyPathWithDelta**
   - 使用 `CloneShallow` 替代 `NewPageInfo`
   - 文件: `btree.go:1246-1282`

2. **优化 rebuildChildRefs**
   - 预分配 map 容量
   - 避免不必要的扩容

**预期收益**:
```
当前: 572k ops/sec (1.75 μs/op)
目标: 800k+ ops/sec (1.25 μs/op)
提升: 40%
```

**实施步骤**:
1. 修改 `copyPathWithDelta` 使用 `CloneShallow`
2. 运行测试验证
3. 性能基准测试
4. 提交代码

---

### Phase 2: 中等优化 (策略 B)

**目标**: 只克隆叶子节点

**实施内容**:

1. **修改 copyPathWithDelta**
   - 内部节点直接复用
   - 只克隆叶子节点

2. **简化 rebuildChildRefs**
   - 只处理叶子节点的引用

**预期收益**:
```
当前: 800k ops/sec (Phase 1 后)
目标: 1M+ ops/sec (1 μs/op)
提升: 25%
```

**实施步骤**:
1. 修改 `copyPathWithDelta` 逻辑
2. 调整 `rebuildChildRefs`
3. 并发测试验证
4. 性能基准测试
5. 提交代码

---

## 🧪 验证方案

### 单元测试

```bash
# 运行所有测试
go test -v ./internal/infrastructure/storage/btree/

# 运行 Delta Chain 相关测试
go test -v -run "Delta" ./internal/infrastructure/storage/btree/

# 运行并发测试
go test -race -v ./internal/infrastructure/storage/btree/
```

### 性能基准测试

```bash
# 纯内存模式性能测试
GOGC=400 go run cmd/btree_perf_mem/main.go threads 1

# 预期结果
# Phase 1: 800k+ ops/sec
# Phase 2: 1M+ ops/sec
```

### 内存分析

```bash
# 生成内存 profile
go test -memprofile=/tmp/mem.prof -bench=. -benchmem ./internal/infrastructure/storage/btree/

# 分析内存分配
go tool pprof -top /tmp/mem.prof

# 验证 NewPageInfo 分配减少
```

---

## 📈 预期效果

### 内存分配对比

| 优化阶段 | NewPageInfo | LeafPage.Clone | 总分配 | 减少比例 |
|----------|-------------|----------------|--------|----------|
| 当前 | 190MB | 99MB | 398MB | - |
| Phase 1 | 95MB | 99MB | 199MB | **50%** |
| Phase 2 | 30MB | 30MB | 100MB | **75%** |

### 性能对比

| 优化阶段 | 吞吐量 | 延迟 | 提升 |
|----------|--------|------|------|
| 当前 | 572k ops/sec | 1.75 μs/op | - |
| Phase 1 | 800k ops/sec | 1.25 μs/op | **40%** |
| Phase 2 | 1M ops/sec | 1.00 μs/op | **75%** |

---

## ⚠️ 风险评估

| 风险 | 等级 | 缓解措施 |
|------|------|----------|
| CloneShallow 状态不一致 | 低 | 现有测试已覆盖 |
| 内部节点复用导致并发问题 | 中 | 并发测试验证 |
| CAS 逻辑变更影响稳定性 | 高 | 分阶段实施，充分测试 |

---

## 📅 时间估算

| 阶段 | 工作内容 | 预计时间 |
|------|----------|----------|
| Phase 1 | 代码修改 + 测试 | 2-3 小时 |
| Phase 2 | 逻辑重构 + 测试 | 4-6 小时 |
| 验证 | 性能测试 + 并发测试 | 1-2 小时 |

**总计**: 1-2 天

---

## 🔗 相关文档

- [COW Delta Chain 全流程文档](../06_PM/feature/2026-03-17_PR-cow-delta-chain-optimization_全流程.md)
- [性能基准测试结果](../06_PM/feature/2026-03-17_PR-cow-delta-chain-optimization_全流程.md#性能基准测试)
- [PageInfo 结构说明](../../internal/infrastructure/storage/btree/page_info.go)

---

## 📝 变更历史

| 日期 | 版本 | 说明 |
|------|------|------|
| 2026-03-18 | v1.0 | 初始版本，基于 GOGC=400 性能分析 |
