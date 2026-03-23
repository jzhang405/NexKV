# BTree 并发优化性能分析报告

**测试日期**: 2026-03-23
**测试环境**: GOGC=500, 8 核心并发
**测试模式**: SetWithRetryAndQueue (Scheduler模式)

---

## 一、性能测试结果

### 1.1 吞吐量

| 并发度 | Direct (ops/s) | Scheduler (ops/s) | 提升 |
|--------|----------------|-------------------|------|
| 1 核心 | 711K | 716K | +0.7% |
| 2 核心 | 1.12M | 1.32M | **+17.9%** |
| 4 核心 | 2.01M | 2.10M | +4.5% |
| 8 核心 | 2.68M | 2.92M | **+9.0%** |

### 1.2 不同 GOGC 环境下的表现

| GOGC | 1核心 | 2核心 | 4核心 | 8核心 |
|------|-------|-------|-------|-------|
| 400 | +16.7% | +5.3% | -3.1% | +11.4% |
| 500 | +0.7% | **+17.9%** | +4.5% | +9.0% |
| 600 | **+19.6%** | -8.9% | **+9.6%** | +9.7% |

**结论**: Scheduler 模式在大多数场景下有正向收益，2 核心表现最佳。

---

## 二、Perf 性能瓶颈分析

### 2.1 主要 CPU 热点函数 (Top 10)

```
# Overhead  Function
# --------  ---------
   6.05%    runtime.tryDeferToSpanScan
   4.80%    cmpbody
   3.52%    runtime.memmove
   3.04%    runtime.typePointers.next
   2.88%    runtime.wbBufFlush1
   2.66%    runtime.scanObject
   2.46%    runtime.typedslicecopy
   1.81%    btree.(*InternalPage).search
   1.60%    btree.(*LeafPage).search
   1.37%    btree.binarySearch
```

### 2.2 性能瓶颈分类

#### A. GC 相关（~15% CPU）

| 函数 | 开销 | 说明 |
|------|------|------|
| `runtime.tryDeferToSpanScan` | 6.05% | 延迟扫描 |
| `runtime.scanObject` | 2.66% | 对象扫描 |
| `runtime.wbBufFlush1` | 2.88% | 写屏障刷新 |
| `runtime.gcDrain` | 2.57% | GC 排空 |

**分析**: GC 相关开销占总 CPU 的 ~15%，在 GOGC=500 环境下这是正常的。

#### B. 内存复制（~6% CPU）

| 函数 | 开销 | 来源 |
|------|------|------|
| `runtime.memmove` | 3.52% | 内存复制 |
| `runtime.typedslicecopy` | 2.46% | 切片复制 |
| `btree.insertSlice` | 1.29% | Leaf 插入时的切片增长 |

**分析**: 内存复制开销主要来自：
- Leaf Page 的 materialize 操作
- Delta Chain 应用
- 切片增长时的数据迁移

**优化建议**:
- 预分配更大的切片容量
- 使用 copy-on-write 减少复制

#### C. BTree 搜索操作（~5% CPU）

| 函数 | 开销 | 说明 |
|------|------|------|
| `btree.(*InternalPage).search` | 1.81% | 内部节点搜索 |
| `btree.(*LeafPage).search` | 1.60% | 叶子节点搜索 |
| `btree.binarySearch` | 1.37% | 二分查找 |
| `btree.(*InternalPage).FindChildRef` | 1.74% | 查找子节点引用 |

**分析**: 搜索路径是不可避免的，但可以优化：
- 当前搜索路径需要遍历从根到叶的所有节点
- 树深度直接影响搜索开销

#### D. BTree 插入操作（~3% CPU）

| 函数 | 开销 | 来源 |
|------|------|------|
| `btree.(*LeafPage).Insert` | 1.46% | 叶子节点插入 |
| `btree.(*LeafPage).materialize` | 0.66% | 叶子节点物化 |

**分析**: 插入操作开销包括：
- 查找插入位置
- 检查是否需要分裂
- 实际插入数据

---

## 三、性能瓶颈总结

### 3.1 主要瓶颈（按优先级）

| 优先级 | 瓶颈 | 开销占比 | 优化潜力 |
|--------|------|----------|----------|
| P0 | **内存复制** | ~6% | 中等 |
| P1 | **BTree 搜索** | ~5% | 低（必需操作）|
| P2 | **BTree 插入** | ~3% | 低（必需操作）|
| P3 | **GC 开销** | ~15% | 环境相关 |

### 3.2 具体优化建议

#### 1. 减少内存复制（P0）

**当前实现**:
```go
// LeafPage.materialize
func (p *LeafPage) materialize() *LeafPage {
    newPage := NewLeafPage()
    copy(newPage.keys, p.keys)        // 复制 keys
    copy(newPage.values, p.values)    // 复制 values
    return newPage
}
```

**优化方案**:
- 使用预分配更大的切片容量，减少重新分配
- 对于小 Page，考虑使用浅拷贝 + 写时复制

#### 2. 优化搜索路径缓存（P1）

**当前实现**:
- 每次操作都需要从根节点搜索到叶子节点
- 树深度越大，搜索开销越高

**优化方案**:
- 实现搜索路径缓存（只缓存热点 key）
- 使用 Skip List 优化多层访问

#### 3. 减少叶子节点分裂频率（P2）

**当前实现**:
- 叶子节点满了就分裂
- 每次分裂需要 materialize 和复制所有数据

**优化方案**:
- 提高填充因子，减少分裂频率
- 使用页内压缩延迟分裂

---

## 四、优化效果验证

### 4.1 当前实现 (Leaf-Level Locking + TaskScheduler)

- **8 核心吞吐量**: 2.92M ops/sec
- **扩展比**: 8 核 / 1 核 = 4.08x
- **与 Direct 相比**: +9.0% 提升

### 4.2 进一步优化空间

如果解决主要瓶颈（内存复制），预计可以获得：
- **单核**: +10-15% (减少内存复制开销)
- **8 核**: +15-20% (内存复制 + 缓存优化)

**预计 8 核心吞吐量**: 3.3-3.5M ops/sec

---

## 五、结论

### 5.1 当前实现评估

✅ **优点**:
- TaskScheduler 集成成功，高并发下有稳定提升
- Leaf-Level Locking 有效减少锁竞争
- 代码结构清晰，易于维护

⚠️ **限制**:
- 内存复制开销较大（~6% CPU）
- 搜索路径没有缓存优化
- GC 开销在 GOGC=500 下仍然明显

### 5.2 下一步优化方向

1. **短期（易实现）**:
   - 增大切片预分配容量
   - 优化 insertSlice 实现

2. **中期（需要测试）**:
   - 实现热点 key 的搜索路径缓存
   - 调整 Page 填充因子

3. **长期（架构级）**:
   - 考虑使用 Art Tree 或 B+Tree 优化搜索
   - 实现 SIMD 优化的 key 比较

---

## 附录：Perf 命令

### 记录性能数据
```bash
GOGC=500 perf record -g --call-graph dwarf -o /tmp/btree_perf.data \
  /tmp/btree_perf_scheduler -threads 8 -count 50000 -mode scheduler
```

### 查看报告
```bash
perf report -i /tmp/btree_perf.data --no-children --stdio
perf annotate -i /tmp/btree_perf.data --stdio
```
