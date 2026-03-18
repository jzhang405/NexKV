# BTree Set 性能优化项目

**日期**: 2026-03-18
**目标**: 优化纯内存模式下 BTree Set 操作性能，从 572k ops/sec 提升到 1M+ ops/sec

---

## 📁 文档列表

| 文件 | 说明 |
|------|------|
| [optimization_plan.md](./optimization_plan.md) | 性能优化方案（推荐阅读） |
| [performance_analysis.md](./performance_analysis.md) | 性能分析报告 |

---

## 📊 快速概览

### 当前性能

```
吞吐量: 572k ops/sec (1.75 μs/op)
内存: 4139 B/op, 51 allocs/op
```

### 主要瓶颈

| 瓶颈 | 占比 | 优化策略 |
|------|------|----------|
| NewPageInfo | 47.70% | 使用 CloneShallow |
| LeafPage.Clone | 24.85% | 只克隆叶子节点 |
| InternalPage.Clone | 9.66% | 只克隆叶子节点 |
| rebuildChildRefs | 10% | 优化 map 预分配 |

### 预期目标

```
Phase 1: 800k ops/sec (1.25 μs/op)  ← 40% 提升
Phase 2: 1M ops/sec (1.00 μs/op)    ← 75% 提升
```

---

## 🎯 实施步骤

### Phase 1: 低风险优化（推荐优先）

**修改**: `internal/infrastructure/storage/btree/btree.go`

```go
// 当前实现
for i, info := range path {
    clonedPage := p.CloneWithDelta()
    newInfo := NewPageInfo()  // ← 额外分配
    newInfo.SetPage(clonedPage)
    copiedPath[i] = newInfo
}

// 优化后
newInfo := info.CloneShallow()  // ← 复用，无额外分配
clonedPage := p.CloneWithDelta()
newInfo.SetPage(clonedPage)
copiedPath[i] = newInfo
```

**预期收益**: 减少 50% 内存分配，性能提升 40%

### Phase 2: 中等优化

**原理**: 只克隆叶子节点，内部节点直接复用

```go
// 内部节点：直接复用
for i := 0; i < len(path)-1; i++ {
    copiedPath[i] = path[i]
}

// 叶子节点：克隆并修改
leafInfo := path[len(path)-1]
clonedLeaf := leafPage.CloneWithDelta()
newLeafInfo := leafInfo.CloneShallow()
newLeafInfo.SetPage(clonedLeaf)
copiedPath[len(path)-1] = newLeafInfo
```

**预期收益**: 减少 75% 总分配，性能提升 75%

---

## 🧪 测试验证

```bash
# 性能测试
GOGC=400 go run cmd/btree_perf_mem/main.go threads 1

# 单元测试
go test -v ./internal/infrastructure/storage/btree/

# 并发测试
go test -race -v ./internal/infrastructure/storage/btree/
```

---

## 📚 相关资源

- [COW Delta Chain 全流程文档](../../06_PM/feature/2026-03-17_PR-cow-delta-chain-optimization_全流程.md)
- [性能基准测试历史](../../06_PM/feature/2026-03-17_PR-cow-delta-chain-optimization_全流程.md#性能基准测试)

---

## 📝 变更历史

| 日期 | 说明 |
|------|------|
| 2026-03-18 | 初始版本，基于 GOGC=400 性能分析 |
