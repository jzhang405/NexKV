# C2 对象池优化测试结果 - ⚠️ 未集成

> **测试时间**: 2026-03-11 10:45
> **测试配置**: MaxKeys=256, EnablePool=true
> **预期收益**: 写延迟 -15%, 内存分配 -60% ~ -70%
> **实际结果**: ⚠️ **对象池代码存在但未集成，性能无变化**

---

## 关键发现

### ❌ 对象池未被集成到代码路径中

**问题**:
- ✅ `pool.go` 文件存在，包含完整的对象池实现
- ✅ `btree_types.go` 中有 `EnablePool` 配置项（默认为 true）
- ❌ **但 `Node.Clone()` 仍然使用 `&Node{}` 直接分配，未调用 `AcquireNode()`**
- ❌ **代码中完全没有任何地方检查或使用 `EnablePool` 配置**

**当前 Node.Clone() 实现**:
```go
func (n *Node) Clone() *Node {
    clone := &Node{  // ❌ 直接分配，绕过了对象池
        PageID:   n.PageID,
        IsLeaf:   n.IsLeaf,
        Keys:     make([][]byte, len(n.Keys), cap(n.Keys)),
        Values:   make([][]byte, len(n.Values), cap(n.Values)),
        Children: make([]*Node, len(n.Children), cap(n.Children)),
        ChildIDs: make([]model.PageID, len(n.ChildIDs), cap(n.ChildIDs)),
    }
    // ... 复制数据
    return clone
}
```

**缺失的功能**:
1. `Node.Clone()` 未使用 `AcquireNode()`
2. 没有节点引用计数机制
3. 没有调用 `ReleaseNode()` 的时机
4. Path 对象池未实现

---

## 测试结果

### 性能对比

| 指标 | C0 (基线) | C2 (EnablePool=true) | 变化 | 说明 |
|------|----------|---------------------|------|------|
| **写延迟** | 5035 ns/op | 5408 ns/op | +7% ⬆️ | ⚠️ **正常波动** |
| **CCOW 延迟** | 5028 ns/op | 6249 ns/op | +24% ⬆️ | ⚠️ **正常波动** |
| **内存分配** | 17928 B/op | 17928 B/op | 0% | ✅ **完全相同** |
| **分配次数** | 12 allocs/op | 12 allocs/op | 0% | ✅ **完全相同** |
| **读延迟** | 179.0 ns/op | 213.7 ns/op | +19% ⬆️ | ⚠️ **正常波动** |

### 详细数据

**写性能 (BenchmarkWriteThroughput_Single)**:
```
C0: 5035 ns/op, 17928 B/op, 12 allocs/op
C2: 5408 ns/op, 17928 B/op, 12 allocs/op
变化: +7%, 0%, 0%
```

**CCOW 性能 (BenchmarkCCOW_Complete)**:
```
C0: 5028 ns/op, 17944 B/op, 12 allocs/op
C2: 6249 ns/op, 17944 B/op, 12 allocs/op
变化: +24%, 0%, 0%
```

**读性能 (BenchmarkBTree_ReadThroughput)**:
```
C0: 179.0 ns/op, 16 B/op, 1 allocs/op
C2: 213.7 ns/op, 16 B/op, 1 allocs/op
变化: +19%, 0%, 0%
```

---

## 结论

### ⚠️ C2 测试无法验证对象池效果

由于对象池代码**未被集成**到实际代码路径中：
- `EnablePool=true` 配置无任何效果
- C2 测试结果与 C0 **基本相同**（差异在正常测试波动范围内）
- **无法验证对象池优化的预期收益**

**关键证据**:
- 内存分配完全相同（17928 B/op）
- 分配次数完全相同（12 allocs/op）
- 性能差异在正常波动范围内（±25%）

---

## 对象池集成计划

### 需要实现的功能

#### 1. Node 对象池集成

**修改 Node.Clone()**:
```go
// 当前实现
func (n *Node) Clone() *Node {
    clone := &Node{...}  // 直接分配
    // ...
    return clone
}

// 修改为
func (n *Node) Clone() *Node {
    clone := AcquireNode()  // 使用对象池
    clone.PageID = n.PageID
    clone.IsLeaf = n.IsLeaf
    // ... 复制数据
    return clone
}
```

#### 2. 节点生命周期管理

**添加引用计数**:
```go
type Node struct {
    // ... 现有字段
    refCount int32  // 引用计数
}

func (n *Node) Retain() {
    atomic.AddInt32(&n.refCount, 1)
}

func (n *Node) Release() {
    if atomic.AddInt32(&n.refCount, -1) == 0 {
        ReleaseNode(n)  // 归还到对象池
    }
}
```

#### 3. Path 对象池实现

**创建 path_pool.go**:
```go
var pathPool = sync.Pool{
    New: func() any {
        return make(Path, 0, 8)  // 预分配 8 层深度
    },
}

func AcquirePath() Path {
    return pathPool.Get().(Path)[:0]
}

func ReleasePath(path Path) {
    if path == nil {
        return
    }
    // 清空但保留容量
    path = path[:0]
    pathPool.Put(path)
}
```

#### 4. 集成到关键代码路径

**需要修改的位置**:
- `Node.Clone()` → 使用 `AcquireNode()`
- `BTree.CopyPathBottomUp()` → 使用 `AcquirePath()`
- `BTree.FindPath()` → 使用 `AcquirePath()`
- 路径销毁时调用 `ReleasePath()`
- 节点不再被引用时调用 `Node.Release()`

---

## 工作量评估

| 任务 | 优先级 | 预估工作量 | 复杂度 |
|------|--------|-----------|--------|
| Node.Clone() 集成对象池 | P0 | 2-3 小时 | 低 |
| 节点引用计数实现 | P0 | 4-6 小时 | 中 |
| Path 对象池实现 | P1 | 2-3 小时 | 低 |
| 关键代码路径集成 | P0 | 6-8 小时 | 中 |
| 单元测试和回归测试 | P0 | 4-6 小时 | 中 |
| **总计** | - | **18-26 小时** | **中-高** |

---

## 建议

### 短期建议（Phase 1.5）

**选项 A**: 集成 Node 对象池
- 修改 `Node.Clone()` 使用 `AcquireNode()`
- 添加简单的引用计数
- 预期收益：写延迟 -10% ~ -15%
- 工作量：1-2 天

**选项 B**: 延迟到 Phase 2
- 将对象池集成作为 Phase 2 的一部分
- 与值指针方案（ValueRef）一起实现
- 可以做更完整的架构优化

### 长期建议（Phase 2+）

1. **完整的对象池系统**
   - Node 对象池 + 引用计数
   - Path 对象池
   - 自动内存管理

2. **值指针方案（ValueRef）**
   - 减少大值复制开销
   - 需要 PO C  验证

3. **并发写入优化**
   - 分段 CAS
   - 减少锁竞争

---

## 数据文件

- **C2 写入吞吐量**: [C2_write_single_*.txt](assets/raw/)
- **C2 CCOW 性能**: [C2_ccow_complete_*.txt](assets/raw/)
- **C2 读吞吐量**: [C2_read_throughput_*.txt](assets/raw/)

---

## 代码证据

### 对象池代码存在

**文件**: `internal/infrastructure/storage/btree/pool.go`
```go
var nodePool = sync.Pool{
    New: func() any {
        return &Node{
            Keys:     make([][]byte, 0, model.DefaultMaxKeys),
            Values:   make([][]byte, 0, model.DefaultMaxKeys),
            Children: make([]*Node, 0, model.DefaultMaxKeys+1),
        }
    },
}

func AcquireNode() *Node { /* ... */ }
func ReleaseNode(node *Node) { /* ... */ }
```

### 但未被使用

**文件**: `internal/infrastructure/storage/btree/node.go`
```go
func (n *Node) Clone() *Node {
    clone := &Node{  // ❌ 未调用 AcquireNode()
        // ...
    }
    // ...
}
```

---

**测试人**: jzhang405
**报告日期**: 2026-03-11
**状态**: ⚠️ C2 测试完成，**对象池未集成，无法验证效果**
**结论**: **需要先集成对象池到代码路径，才能测试其性能收益**
