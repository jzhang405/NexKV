# BTree Page 层实施计划 - 基于 Lealone 实现分析

**日期**: 2026-03-09
**版本**: v3.0 (简化版)
**基于**: `thoughts/2026-03-09-lealone-page-based-btree-implementation.md`

---

## 📋 执行摘要

### 问题诊断

**当前状态** (纯内存 CCOW 架构):
- ✅ 极致读性能: ~10 ns/op
- ✅ 无锁并发读取
- ❌ 并发写入瓶颈: 491K QPS (4线程，几乎无提升)
- ❌ 无法持久化: 崩溃后数据全丢
- ❌ 内存碎片化: 大量小对象分配

**根本原因**:
```
当前 CCOW 方案的问题：
1. 单写机制 - 所有写操作串行化
2. 路径复制 - 每次写入复制整条路径
3. 内存开销 - 大量临时对象分配

结果：4线程并发写入仅 491K QPS（接近单线程性能）
```

### Lealone 的成功经验

**Lealone Page-based BTree**:
- ✅ 写入性能: 670K QPS
- ✅ 写延迟: ~1.6 μs (比 NexKV 快 25x)
- ✅ 持久化: Chunk + WAL
- ✅ 大容量: TB 级存储
- ✅ 生产可用: 完整的数据安全机制

**关键差异**:
```
Lealone 为什么并发更好？
1. 真正的 BTree 结构 → 不同叶节点独立锁
2. Page 级并发 → 多个写操作真正并行
3. 固定大小 Page → 减少内存碎片
```

---

## 📊 性能对比分析

### 实测数据对比

| 指标 | NexKV 当前 | Lealone | 优势方 | 倍数 |
|------|-----------|---------|--------|------|
| **读延迟** | 10.97 ns | 941 ns | **NexKV** | 85x |
| **写延迟** | 41.7 μs | 1.6 μs | **Lealone** | 25x |
| **并发写入** | 491K QPS (4线程) | 670K QPS | **Lealone** | 1.4x |
| **单线程写入** | 505K QPS | ~600K QPS | 相近 | - |
| **持久化** | ❌ | ✅ | **Lealone** | - |
| **大容量** | ❌ (OOM) | ✅ (TB级) | **Lealone** | - |

### 架构权衡

| 场景需求 | 推荐方案 | 原因 |
|---------|---------|------|
| 极致读性能 | 纯内存 CCOW (NexKV 当前) | 10 ns 硬件极限 |
| 读写均衡 | Page-based (Lealone) | 1.6 μs 写延迟 |
| 生产环境 | Page-based (Lealone) | 持久化 + 容量 |
| 简单原型 | 纯内存 CCOW | 快速开发 |

**核心洞察**:
- **NexKV 当前架构**适合：极致读性能场景（如缓存、实时系统）
- **Lealone 架构**适合：生产数据库（持久化、大容量、稳定）

---

## 🎯 两种实施路径

### 选项 A: 纯内存优化 (保持当前架构)

**适用场景**: 追求极致读性能，可接受数据丢失风险

**改进方向**:
```
1. 批量写入优化
   - 合并多个写操作
   - 减少路径复制次数

2. 内存分配优化
   - 对象池复用
   - 减少小对象分配

3. 并发写入优化
   - 分片写入（不同 key range）
   - 减少锁竞争
```

**预期效果**:
- 并发写入: 491K → 800K QPS (1.6x)
- 读延迟: 保持 10 ns
- 风险: 仍然无法持久化

**工作量**: 1-2周

---

### 选项 B: Page-based 迁移 (借鉴 Lealone) ⭐ 推荐

**适用场景**: 生产环境，需要持久化和大容量

**核心设计**:

```go
// 简化的多层 BTree 结构
type Node struct {
    Keys     [][]byte
    Values   [][]byte    // 叶节点用
    Children []*Node     // 内部节点用（直接指针）
    mu       sync.RWMutex // 节点级锁
    IsLeaf   bool
}

// 关键特性：
// 1. 不同叶节点独立锁 → 真正并发
// 2. 直接 Node 指针 → 性能优先
// 3. 可选 Page 序列化 → 支持持久化
```

**分阶段实施**:

**Phase 1: 简化的多层 BTree** (2周)
```go
// 目标：实现真正的 BTree 结构
type BTree struct {
    root *Node
}

// 核心改进：
// - 不再使用 PageID 间接（性能优先）
// - 每个节点独立锁
// - 叶节点按 key 范围自动分割
```

**验证**:
- 4线程写入 > 1M QPS
- 不同 key 范围可并发
- 通过并发测试

**Phase 2: Page 持久化层** (可选，2-3周)
```go
// 复用已有的基础设施：
// - page_cache.go (三层缓存)
// - serialize.go (序列化)

type Page struct {
    ID   model.PageID
    Data [4096]byte
    mu   sync.RWMutex
}
```

**验证**:
- 数据持久化到磁盘
- 崩溃恢复可工作
- 性能无明显退化

**预期效果**:
- 并发写入: 491K → 1.5M QPS (3x)
- 写延迟: 41.7 μs → ~3 μs (13x)
- 支持持久化和大容量

---

## 🗑️ 删除的过时设计

从之前的 v2.0 计划中删除（agent review 反馈）：

| 组件 | 删除原因 |
|------|----------|
| **PageLock 可重入锁** | goroutineID 获取不安全 |
| **Scheduler 单写队列** | 过度设计，增加复杂度 |
| **PageReference 引用计数** | 可用 sync.RWMutex 简化 |
| **2.5M QPS 目标** | 不现实，Lealone 仅 670K QPS |
| **5阶段实施计划** | 过于复杂，简化为 2 阶段 |

---

## 📚 Lealone 核心设计精华

### 1. 并发控制机制

**Page 级轻量锁**:
```java
// Lealone: PageReference.java
public class PageReference {
    private final PageLock pageLock = new PageLock();

    public boolean tryLock(InternalScheduler scheduler, boolean waitingIfLocked) {
        return pageLock.tryLock(scheduler, waitingIfLocked);
    }
}
```

**关键**: 不同叶节点独立锁，写入不同 Page 可并发

### 2. 原子更新机制

**CAS 无锁替换**:
```java
private static final AtomicReferenceFieldUpdater<PageReference, PageInfo>
    pageInfoUpdater = AtomicReferenceFieldUpdater.newUpdater(...);

public boolean replacePage(PageInfo expect, PageInfo update) {
    return pageInfoUpdater.compareAndSet(this, expect, update);
}
```

**关键**: 无锁更新 Page 指针，支持并发读取

### 3. Copy-On-Write 机制

**路径复制**:
```java
// LeafPage.java
public Page copyAndInsertLeaf(int index, Object key, Object value) {
    // 1. 复制 keys 数组
    Object[] newKeys = new Object[keys.length + 1];
    DataUtils.copyWithGap(keys, newKeys, len - 1, index);
    newKeys[index] = key;

    // 2. 创建新 Page
    return copyLeaf(newKeys, null);
}
```

**关键**: 每次写入复制整个 Page，而非整条路径

### 4. 三层缓存架构

```
L1: Page 对象 (~100 ns) → 已反序列化
L2: ByteBuffer (~500 ns) → 原始数据，避免重复 I/O
L3: 磁盘文件 (~10-100 μs) → 持久化存储
```

**关键**: 逐级降级，平衡性能和容量

---

## 📁 文件清单

### 当前已有基础设施 (可复用)

| 文件 | 状态 | 用途 |
|------|------|------|
| `page_cache.go` | ✅ 已实现 | 三层缓存（L1/L2/L3） |
| `page_cache_test.go` | ✅ 已实现 | 缓存测试 |
| `serialize.go` | ✅ 已实现 | 序列化/反序列化 |
| `serialize_test.go` | ✅ 已实现 | 序列化测试 |

### Phase 1 需要新建/修改

| 文件 | 操作 | 内容 |
|------|------|------|
| `btree.go` | 修改 | 集成 Node 级锁 |
| `node.go` | 修改 | 添加 `mu sync.RWMutex` |
| `multilayer_btree_test.go` | 新建 | 多层 BTree 测试 |
| `multilayer_btree_bench_test.go` | 新建 | 性能基准测试 |

### Phase 2 需要新建 (可选)

| 文件 | 内容 |
|------|------|
| `page_persist.go` | Page 持久化逻辑 |
| `page_persist_test.go` | 持久化测试 |
| `wal.go` | Write-Ahead Log |
| `wal_test.go` | WAL 测试 |

---

## ✅ 验证标准

### Phase 1 验证

**功能测试**:
- [ ] 基本 Get/Set 操作正确
- [ ] 多层 BTree 结构正确（根节点 → 叶节点）
- [ ] 叶节点自动分裂
- [ ] 不同 key 范围落到不同叶节点

**并发测试**:
- [ ] 10 个 goroutine 并发写入不同 key 范围
- [ ] 验证不同叶节点可并发写入
- [ ] 无 data race
- [ ] 数据一致性检查

**性能测试**:
- [ ] 4线程写入 > 1M QPS
- [ ] 写延迟 < 5 μs
- [ ] 读延迟 < 200 ns

### Phase 2 验证 (可选)

**持久化测试**:
- [ ] 数据成功写入磁盘
- [ ] 重启后数据可恢复
- [ ] WAL 正确工作
- [ ] 性能无明显退化 (< 20%)

---

## 🎯 推荐方案总结

**选择**: **选项 B - 分阶段实施**

**理由**:
1. **解决根本问题**: 真正的多层 BTree 结构
2. **可行性强**: 复用已有基础设施 (page_cache, serialize)
3. **生产可用**: 支持持久化和大容量
4. **渐进式**: Phase 1 先优化并发，Phase 2 再加持久化

**预期收益**:
- 并发写入: 491K → 1.5M QPS (3x 提升)
- 写延迟: 41.7 μs → ~3 μs (13x 改善)
- 支持持久化和大容量
- 生产环境可用

**风险控制**:
- Phase 1 单独验证，不依赖持久化
- 可选 Phase 2，根据实际需求决定
- 复用已有代码，降低风险

---

## 📖 参考文档

- **Lealone 实现分析**: `thoughts/2026-03-09-lealone-page-based-btree-implementation.md`
- **Page 级别锁设计**: `docs/09_code-review/2026-03/2026-03-09-page-level-locking-design.md`
- **当前实现**: `internal/infrastructure/storage/btree/`

---

**文档版本**: 3.0 (简化版，基于 Lealone 实际数据)
**最后更新**: 2026-03-09
**状态**: 待评审
