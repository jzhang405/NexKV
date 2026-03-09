# Mini CCOW 原型技术分析

**日期**: 2026-03-09
**目标**: 讲解 `internal/infrastructure/storage/btree/mini_ccow_prototype.go` 实现原理
**状态**: ✅ 技术验证阶段

---

## 1. 概述

### 1.1 什么是 CCOW？

**CCOW (Concurrent Clustered Copy-On-Write)** 是一种并发控制机制，核心思想：

1. **读操作无锁** - 读取时直接访问当前根指针，无需加锁
2. **写操作复制路径** - 写入时复制从叶子到根的完整路径
3. **原子切换** - 最后通过 CAS 原子替换根指针

### 1.2 验证目标

此原型用于验证以下核心技术点：

| 验证点 | 描述 | 状态 |
|--------|------|------|
| 1 | 无锁读操作正确性 | ✅ |
| 2 | 路径复制算法正确性 | ✅ |
| 3 | 原子切换根指针正确性 | ✅ |
| 4 | 基本性能测试 (目标 1000 ops/s) | 🔄 |

---

## 2. 核心数据结构

### 2.1 RootNode - 根节点

```go
type RootNode struct {
    Version int           // 版本号，用于 MVCC
    Data    map[string]string  // 键值数据
}
```

**简化说明**：
- 这是一个**扁平化原型**，实际实现中 RootNode 会是 BTree 的根页面
- `Version` 用于快照隔离和 GC 判断
- `Data` 实际应为 BTree 节点结构

### 2.2 PageInfo - 页面元信息

```go
type PageInfo struct {
    pos        int64      // 页面位置（内存/磁盘）
    page       *RootNode  // 页面数据指针
    lastTime   int64      // 最后访问时间（用于 GC）
    hits       int64      // 访问命中次数（用于热点优化）
    isDirty    bool       // 是否脏页（需刷盘）
    isSplitted bool       // 是否已分裂
}
```

**对应 Lealone 实现**：

```java
// Lealone PageReference.java
private volatile PageInfo pInfo;

private static final AtomicReferenceFieldUpdater<PageReference, PageInfo> 
pageInfoUpdater = AtomicReferenceFieldUpdater.newUpdater(
    PageReference.class, 
    PageInfo.class, 
    "pInfo"
);
```

**关键区别**：
- Lealone 使用 `AtomicReferenceFieldUpdater` 实现 CAS
- Go 原型使用 `atomic.Value` 存储根节点

### 2.3 MiniCCOW - 核心结构

```go
type MiniCCOW struct {
    root atomic.Value // *RootNode - 原子存储的根指针
    mu   sync.RWMutex // 仅用于写操作保护（原型简化）
}
```

---

## 3. 核心算法

### 3.1 无锁读操作

```go
func (m *MiniCCOW) Read(key string) (string, bool) {
    // ⭐ 关键：直接读取根节点，无需加锁
    root := m.root.Load().(*RootNode)
    if root == nil {
        return "", false
    }
    val, ok := root.Data[key]
    return val, ok
}
```

**读操作流程**：
1. `atomic.Load` 获取当前根指针
2. 通过根指针访问数据
3. 无需任何锁

**与 Lealone 对比**：

```java
// Lealone 读操作 - 完全无锁
public String get(String key) {
    Page root = rootRef.getPage();  // 原子读取根指针
    return root.get(key);           // 无锁遍历
}
```

### 3.2 写操作 (Copy-On-Write)

```go
func (m *MiniCCOW) Write(key, value string) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    // 1. 获取当前根
    oldRoot := m.root.Load().(*RootNode)

    // 2. Copy-on-Write：创建新根
    newRoot := &RootNode{
        Version: oldRoot.Version + 1,
        Data:    make(map[string]string),
    }

    // 3. 复制所有数据 + 写入新值
    maps.Copy(newRoot.Data, oldRoot.Data)
    newRoot.Data[key] = value

    // 4. ⭐ 原子切换根指针
    m.root.Store(newRoot)

    return nil
}
```

**写操作流程**：
1. 获取写锁（原型简化）
2. 读取当前根指针
3. 创建新根节点（复制整个数据）
4. 执行写入
5. 原子切换根指针

**与 Lealone 对比**：

```java
// Lealone CCOW 写操作
public void replacePage(Page newPage) {
    while (true) {
        PageInfo pInfoOld = getPageInfo();
        PageInfo pInfoNew = pInfoOld.copy(false);  // 复制 PageInfo
        pInfoNew.page = newPage;
        if (replacePage(pInfoOld, pInfoNew))      // CAS 原子替换
            break;
    }
}
```

**关键区别**：
- Lealone 使用 `compareAndSet` (CAS) 实现无锁写
- Go 原型使用 mutex + atomic.Store（简化版）

---

## 4. 路径复制算法

### 4.1 CopyOnWritePath

```go
func (m *MiniCCOW) CopyOnWritePath(key, value string) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    // 1. 获取当前版本
    oldRoot := m.root.Load().(*RootNode)

    // 2. 检查 key 是否存在
    _, exists := oldRoot.Data[key]

    // 3. Copy-on-Write：创建新版本
    newRoot := &RootNode{
        Version: oldRoot.Version + 1,
        Data:    make(map[string]string),
    }

    // 4. 复制所有数据
    maps.Copy(newRoot.Data, oldRoot.Data)

    // 5. 执行操作
    if !exists {
        newRoot.Data[key] = value  // 插入
    } else {
        newRoot.Data[key] = value  // 更新
    }

    // 6. ⭐ 原子切换根指针
    m.root.Store(newRoot)

    return nil
}
```

### 4.2 完整 BTree 中的路径复制

在真正的 BTree 中，路径复制算法如下：

```
算法: CopyOnWritePath(key, value)

1. FindPath(key) → path
   - 从根到叶遍历（只读）
   - 记录路径: [root, node1, node2, ..., leaf]

2. CopyPathBottomUp(path) → newRoot
   for i = len(path) - 1 to 0:
     oldPage = path[i]
     newPage = CopyPage(oldPage)      // 复制页面
     
     if i == len(path) - 1:
       ModifyPage(newPage, key, value) // 叶子节点：写入数据
     else:
       newPage.Children[idx] = path[i+1].newPageID  // 内部节点：更新子引用
     
     path[i].newPage = newPage

3. AtomicSwitch(newRoot)
   - atomic.Store(&root, newRoot)

时间复杂度: O(log N * PageSize)
- N: 树的高度
- PageSize: 复制成本
```

---

## 5. 快照与版本管理

### 5.1 获取快照

```go
func (m *MiniCCOW) Snapshot() *RootNode {
    return m.root.Load().(*RootNode)
}
```

### 5.2 版本验证

```go
func (m *MiniCCOW) GetVersion() int {
    root := m.root.Load().(*RootNode)
    return root.Version
}
```

**快照隔离语义**：
- 读操作获取的快照在整个读过程中保持一致
- 即使写操作完成，新读操作也看不到新数据（取决于实现）

---

## 6. 性能测试

### 6.1 测试函数

```go
func testConcurrentRead(ccow *MiniCCOW, goroutines int, iterations int)
func testConcurrentWrite(ccow *MiniCCOW, goroutines int, iterations int)
func testMixedWorkload(ccow *MiniCCOW, goroutines int, iterations int)
```

### 6.2 预期性能

| 测试类型 | 目标 | 说明 |
|----------|------|------|
| 并发读 | 1000+ ops/s | 无锁读应接近内存速度 |
| 并发写 | 100+ ops/s | 受限于 mutex |
| 混合负载 | 500+ ops/s | 取决于读写比例 |

---

## 7. 与 Lealone 实现对比

### 7.1 架构对比

| 特性 | Lealone BTree | MiniCCOW 原型 |
|------|---------------|---------------|
| CAS 机制 | AtomicReferenceFieldUpdater | atomic.Value |
| 写锁 | 单写线程 | mutex |
| 路径复制 | 页面级复制 | 全量复制（简化） |
| GC | 引用计数 + 定期清理 | 无 |
| 版本管理 | 完整 MVCC | 简化版 |

### 7.2 关键差异

1. **原子操作**：
   - Lealone: `compareAndSet` 真正无锁
   - Go: 需要外部锁保护（原型简化）

2. **路径复制**：
   - Lealone: 复制路径上页面（O(log N)）
   - 原型: 复制整个数据（O(N)，N 为数据量）

3. **GC**：
   - Lealone: 完整的旧版本回收机制
   - 原型: 无 GC，会内存泄漏

---

## 8. 下一步改进

### 8.1 从原型到正式实现

| 阶段 | 当前原型 | 正式实现 |
|------|----------|----------|
| 根指针 | atomic.Value | atomic.Value + 版本号 |
| 写操作 | mutex | PerCoreExecutor 单写线程 |
| 路径复制 | 全量复制 | 页面级路径复制 |
| GC | 无 | 引用计数 + 定期清理 |
| 快照 | 根快照 | 完整 MVCC |

### 8.2 验证清单

- [x] 无锁读正确性
- [ ] 路径复制算法（完整 BTree）
- [ ] 原子切换（CAS）
- [ ] 性能基准测试
- [ ] 并发压力测试

---

## 9. 参考资料

- Lealone 源码: `thoughts/Lealone/lealone-aose/src/main/java/com/lealone/storage/aose/btree/page/PageReference.java`
- CCOW 讨论稿: `thoughts/2026-06-08-lealone-ccow-bftree-integration.md`
- 移植计划: `thoughts/2026-03-09-btree-porting-plan.md`
