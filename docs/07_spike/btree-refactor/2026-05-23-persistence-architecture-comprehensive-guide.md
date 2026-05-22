# NexKV 持久化架构全景指南

> 从头理解 NexKV 的存储引擎、WAL、AO 落盘、MVCC 事务、Checkpoint 与崩溃恢复
> 创建日期：2026-05-23
> 覆盖范围：Page 物理布局 → BTree COW → WAL 日志 → AO Chunk 落盘 → MVCC 事务 → Checkpoint → Recovery

---

## 目录

1. [总览：数据的一生](#一总览数据的一生)
2. [第一层：物理 Page 布局](#二第一层物理-page-布局)
3. [第二层：mmap 页面池与 COW](#三第二层mmap-页面池与-cow)
4. [第三层：BTree 存储引擎](#四第三层btree-存储引擎)
5. [第四层：AO Chunk 落盘](#五第四层ao-chunk-落盘)
6. [第五层：WAL 日志](#六第五层wal-日志)
7. [第六层：Checkpoint 检查点](#七第六层checkpoint-检查点)
8. [第七层：MVCC 多版本并发控制](#八第七层mvcc-多版本并发控制)
9. [第八层：Epoch 页面回收](#九第八层epoch-页面回收)
10. [第九层：崩溃恢复](#十第九层崩溃恢复)
11. [完整数据流：一条 Put 的旅程](#十一完整数据流一条-put-的旅程)
12. [关键设计决策汇总](#十二关键设计决策汇总)

---

## 一、总览：数据的一生

在深入细节之前，先用一张全景图理解 NexKV 的存储架构。

### 1.1 全景架构图

```
                            ┌─────────────────────────┐
                            │     Client API           │
                            │  Get / Set / Delete / Tx │
                            └───────────┬─────────────┘
                                        │
                    ┌───────────────────┼───────────────────┐
                    │                   ▼                   │
                    │   ┌───────────────────────────┐      │
                    │   │      MVCC Layer            │      │
                    │   │  Transaction / VersionChain │      │
                    │   │  KeyLock / WriteBuffer      │      │
                    │   └───────────┬───────────────┘      │
                    │               │                       │
                    │               ▼                       │
                    │   ┌───────────────────────────┐      │
                    │   │      BTree Engine           │      │
                    │   │  COW Pages / CAS PageRef    │      │
                    │   │  Split / Merge / Compact    │      │
                    │   └───────┬───────┬───────────┘      │
                    │           │       │                   │
                    │           ▼       ▼                   │
                    │   ┌──────────┐ ┌──────────────┐      │
                    │   │   WAL    │ │  AO Chunks   │      │
                    │   │ 日志先行 │ │ 页面落盘     │      │
                    │   │ fsync()  │ │ fsync()      │      │
                    │   └────┬─────┘ └──────┬───────┘      │
                    │        │              │               │
                    └────────┼──────────────┼───────────────┘
                             │              │
                             ▼              ▼
                    ┌─────────────────────────────────┐
                    │          Disk (SSD/HDD)          │
                    │  *.wal files    *.ao files       │
                    └─────────────────────────────────┘
```

### 1.2 数据的两条持久化路径

NexKV 采用 **WAL + Checkpoint 双路径持久化**，借鉴了经典数据库的 Write-Ahead Logging 模式：

```
路径 1: 实时持久化（WAL）
  Put(key, value)
    → MVCC 编码
    → WAL.Append(entry)  ← 立即写入 WAL 文件
    → WAL.Sync()         ← fsync 强制落盘
    → 返回成功给客户端

路径 2: 定期持久化（Checkpoint → AO）
  每 30 秒：
    → 扫描 BTree 脏页
    → 序列化为 PageFrame
    → 写入 .ao Chunk 文件
    → WAL 写入 Checkpoint 标记
    → 截断旧的 WAL 文件
```

**为什么需要两条路径？**

- **WAL**：低延迟的顺序写，保证每个操作都不丢失。WAL 文件是临时的，Checkpoint 后会被截断。
- **AO Chunk**：高吞吐的页面落盘，将内存中的 BTree 页面持久化。AO 文件是持久的，数据最终存在这里。

### 1.3 关键数据结构速览

| 数据结构 | 位置 | 用途 |
|---------|------|------|
| `PageHeader` (56B) | mmap 每页开头 | 页面元数据：版本号、类型、计数、兄弟链 |
| `LeafEntry` (16B) | mmap 页内 | 叶子页的 KV 条目：key 偏移 + value 偏移 |
| `IndexEntry` (16B) | mmap 页内 | 内部节点的索引条目：key 偏移 + child pageID |
| `PageInfo` | Go 堆 | PageRef 发布的不变元数据：PageID、Version、NodeState、ChunkPos |
| `PageRef` | Go 堆 | CAS 可替换的页面引用：pInfo + children cache |
| `WALEntry` | WAL 文件 | 操作日志：LSN、Type、Key、Value、TxID、CRC32C |
| `ChunkHeader` | .ao 文件头 | Chunk 元数据：ID、pageCount、removedPageOffset |
| `PageFrame` | .ao 文件体 | 页面序列化：CRC32C + PageHeader + KV Data |
| `VersionChain` | Go 堆 | MVCC 版本链：head → node(commitTS, oldValue) → node(...) |
| `ChunkPosition` (uint64) | 各处 | 页面在 .ao 文件中的定位编码 |

---

## 二、第一层：物理 Page 布局

NexKV 最底层的存储单元是 **4KB 的 Page**，通过 mmap 映射到内存。理解 Page 的物理布局是理解一切的基础。

### 2.1 Page 的整体结构

每个 Page（无论叶子还是内部节点）都是 4096 字节，分为四个区域：

```
┌──────────────────────────────────────────────────────────────┐
│                    Page (4096 bytes)                         │
│                                                              │
│  ┌──────────────┬──────────────┬──────────────┬────────────┐ │
│  │ PageHeader   │ Entry Array  │  Free Space  │ KV Data    │ │
│  │ 56 bytes     │ N × 16 bytes │  (grows ↓)   │ (grows ↑)  │ │
│  └──────────────┴──────────────┴──────────────┴────────────┘ │
│  offset 0       offset 56                      offset 4095  │
│                                                              │
│  Entry Array grows →→→→→→→→→→→→→→→→→                        │
│  KV Data Area grows ←←←←←←←←←←←←←←←←←                        │
│                                                              │
│  中间的空闲区域 = 页面剩余可用空间                              │
│  当它们相遇时 → 页面满 → 触发 Split                           │
└──────────────────────────────────────────────────────────────┘
```

**为什么这样设计？**

Entry Array 从前往后增长，KV Data 从后往前增长。两者在中间相遇时页面才满。这样设计利用了页面的全部空间，不需要预先划分"元数据区"和"数据区"。

### 2.2 PageHeader：每个 Page 的身份证（56 字节）

`PageHeader` 是每个 Page 的前 56 字节，代码位置：`internal/infrastructure/storage/offheap/page_layout.go:32-44`

```
Byte  │ 0       1       2       3       4       5       6       7
──────┼────────────────────────────────────────────────────────────
 0-7  │ version (uint64)                    COW 版本号，每次修改 +1
 8-11 │ prevPage (uint32)                   前一个兄弟页的 PageID
12-15 │ nextPage (uint32)                   后一个兄弟页的 PageID
16-23 │ extraChild (uint64)                 内部节点：第 N+1 个子页
24-25 │ count (uint16)                      当前条目数
 26   │ pageType (uint8)                    0=内部节点 1=叶子
 27   │ deleted (uint8)                     0=正常 1=已标记删除
28-29 │ tombstoneCount (uint16)             Tombstone 条目计数
30-31 │ (gap: 隐式对齐填充) 
32-39 │ deleteEpoch (uint64)                安全回收 Epoch
40-47 │ chunkPos (uint64)                   AO 文件中的位置（辅助）
48-55 │ _padding ([8]byte)                  显式填充，保证 56 字节对齐
──────┴────────────────────────────────────────────────────────────
总共：56 字节
```

**关键字段解读：**

- **version**：COW 版本号。每次 COW 分配新页面时 version = 原页面 version + 1。用于快照隔离。
- **prevPage / nextPage**：组成叶子页的双向链表，用于 Range Scan。初始值为 `0xFFFFFFFF`（哨兵）。
- **extraChild**：B+Tree 内部节点的特殊设计。N 个 Key 有 N+1 个 Child，前 N 个 Child 存在 IndexEntry 中，第 N+1 个存在这里。
- **pageType**：决定 Entry Array 里存的是 LeafEntry 还是 IndexEntry。
- **tombstoneCount**：Phase 6.5 引入，追踪逻辑删除但物理未删除的条目数。

### 2.3 LeafEntry：叶子页的 KV 条目（16 字节）

代码位置：`internal/infrastructure/storage/offheap/page_layout.go:64-69`

```
Byte  │ 0       1       2       3
──────┼─────────────────────────────────────
 0-3  │ keyOff (uint32)    Key 在 Page 内的字节偏移
 4-7  │ keyLen (uint32)    Key 的字节长度
 8-11 │ valOff (uint32)    Value 在 Page 内的字节偏移
12-15 │ valLen (uint32)    Value 的字节长度
──────┴─────────────────────────────────────
总共：16 字节
```

**LeafEntry 不存储 Key/Value 本身，只存储偏移量和长度**。实际的 Key 和 Value 数据存在页面的 KV Data 区域。

```
举例：一个叶子页有 3 个条目

LeafEntry[0]: {keyOff:4040, keyLen:6, valOff:4080, valLen:10}  → "hello" → "world!!!!!!"
LeafEntry[1]: {keyOff:4030, keyLen:5, valOff:4070, valLen:10}  → "foo"   → "bar!!!!!!!!"
LeafEntry[2]: {keyOff:4010, keyLen:3, valOff:4060, valLen:10}  → "xyz"   → "abc!!!!!!!!"

KV Data 区（从 Page 尾部向前排列）：
offset 4095 ← value[0]: "world!!!!!!" (10B)
offset 4080 ← key[0]:   "hello" (6B)
offset 4070 ← value[1]: "bar!!!!!!!!" (10B)
offset 4060 ← key[1]:   "foo" (5B)
...
```

### 2.4 IndexEntry：内部节点的索引条目（16 字节）

代码位置：`internal/infrastructure/storage/offheap/page_layout.go:54-58`

```
Byte  │ 0       1       2       3
──────┼─────────────────────────────────────
 0-3  │ keyOff (uint32)    Key 在 Page 内的字节偏移
 4-7  │ keyLen (uint32)    Key 的字节长度
 8-15 │ child (uint64)     子页编码：高 32 位 = version，低 32 位 = PageID
──────┴─────────────────────────────────────
总共：16 字节
```

**Child 编码**（`EncodeChildWithVersion`）：
```
child = (version << 32) | pageID

高 32 位：子页的 COW 版本号
低 32 位：子页的 PageID
```

**B+Tree 内部节点的组织规则**：

```
Internal Node (Count=3):
  
  Child[0] → 子树 Key < Key[0]
  Key[0]   → "apple"
  Child[1] → Key[0] ≤ 子树 Key < Key[1]
  Key[1]   → "orange"
  Child[2] → Key[1] ≤ 子树 Key < Key[2]
  Key[2]   → "zebra"
  Child[3] → 子树 Key ≥ Key[2]    ← 这是 extraChild

索引条目存储在 IndexEntry 数组中：
  IndexEntry[0]: {key:"apple", child:Child[0]}
  IndexEntry[1]: {key:"orange", child:Child[1]}
  IndexEntry[2]: {key:"zebra", child:Child[2]}
  
  Child[3] 存储在 PageHeader.extraChild 中
```

### 2.5 页面容量计算

**空间检查**（`IsFull()`）：判断能否插入新的 KV 对：

```
可用空间 = 4096 - 56(Header) - count×16(EntryArray) - dataEnd(KVData)

新条目需要 = 16(新Entry) + len(key) + len(value)
若 需要 > 可用空间 → 页面满 → 触发 Split
```

**最大容量**：一个 4KB 页面约可存储：
- 叶子页：~168 个 12-byte key + 12-byte value 的条目
- 内部节点：~189 个纯 key 条目（因为内部节点无 value，但 child 指针占用 8 字节）

---

## 三、第二层：mmap 页面池与 COW

### 3.1 mmap 页面池

NexKV 的整个 BTree 存储在一片巨大的 mmap 映射中。代码位置：`internal/infrastructure/storage/offheap/page_manager.go`

```
┌──────────────────────────────────────────────────────────────────┐
│                  mmap region (默认 512MB)                        │
│                                                                  │
│  ┌──────┬──────┬──────┬──────┬──────┬──────┬──────┬──────┐     │
│  │Page 0│Page 1│Page 2│Page 3│ ...  │ ...  │ ...  │Page N│     │
│  │ 4KB  │ 4KB  │ 4KB  │ 4KB  │      │      │      │ 4KB  │     │
│  └──────┴──────┴──────┴──────┴──────┴──────┴──────┴──────┘     │
│  offset 0      4096    8192   12288                    N×4096   │
│                                                                  │
│  每个 Page 可以通过 pageID 直接计算内存地址：                      │
│    ptr = base_ptr + pageID × 4096                                │
│                                                                  │
│  无需页表、无需指针解引用、无需 GC 扫描                            │
└──────────────────────────────────────────────────────────────────┘
```

**PageID 到指针的转换**（O(1)）：
```go
// page_manager.go:157
func (pm *PageManager) PageIDToPtr(pageID model.PageID) unsafe.Pointer {
    return unsafe.Add(pm.base, uintptr(pageID) * PageSize)
}
```

**分配策略**：
1. 优先从 FreeList（无锁队列）获取回收的 PageID
2. FreeList 为空时，从 `nextPageID` 单调递增分配
3. 新分配的 Page 清零（`clearPage`），version 初始化为 1

### 3.2 COW（Copy-On-Write）语义

NexKV 的 BTree 采用 **页面级 Copy-On-Write**。这是整个系统的核心并发控制机制。

```
传统方案（原地修改）：
  修改 Page 5 → 锁住 Page 5 → 修改 → 解锁
  风险：并发读者看到中间状态

COW 方案：
  读取 Page 5 → 分配新 Page 99 → 复制 5 的内容到 99 → 在 99 上修改
          → 原子地将引用从 5 切换到 99
          → Page 5 变成旧的，等待回收
  
  并发读者：始终看到 Page 5 的完整一致状态
  并发写者：通过 CAS 解决冲突
```

**COW 的完整流程**（以 Set 操作为例）：

```
Step 1: searchPath
  root → child → ... → leafRef (PageRef 指向当前叶子页 PageID=5)

Step 2: 读取旧页
  oldInfo = leafRef.GetPageInfo()  → {PageID:5, Version:10}

Step 3: 复制并修改（COW）
  oldLeaf = GetLeafPage(5)     ← 从 mmap 读取
  newPageID = AllocLeafPage()  ← 分配新页，比如 PageID=99
  newLeaf = Copy(oldLeaf) + Modify(newLeaf)  ← 在 99 上执行修改
  newInfo = {PageID:99, Version:11}

Step 4: CAS 切换引用
  leafRef.CAS(oldInfo, newInfo)  ← 原子地将引用从 5 改到 99
  
  成功：读者现在看到 Page 99
  失败：另一个写者抢先了 → 释放 Page 99 → 重试

Step 5: 回收旧页
  Page 5 不再被引用 → Retire(5) → Epoch 延迟释放
```

### 3.3 PageRef：CAS 可替换的页面引用

代码位置：`internal/infrastructure/storage/btree/page_ref.go`

```go
type PageRef struct {
    pageID   model.PageID                   // 不变：这个引用指向哪个 Page
    pInfo    atomic.Pointer[PageInfo]       // 原子替换：当前 Page 的元数据
    children atomic.Pointer[ChildrenCache]  // 原子替换：子页缓存（内部节点用）
    refCount atomic.Int32                   // 引用计数：多少 reader 正在使用
    freeFunc func(model.PageID)             // refCount 归零时的回调
}
```

**PageInfo**（代码位置：`internal/infrastructure/storage/btree/page_info.go`）：

```go
type PageInfo struct {
    PageID       model.PageID         // 页面 ID
    Version      uint64               // COW 版本号
    Redirect     bool                 // 是否已重定向（split/merge 后）
    NewRef       *PageRef             // Redirect=true 时指向新页面
    IsLeaf       bool                 // 是否叶子页
    NodeState    NodeState            // 状态：Normal / Splitting / Merging / Compacting / Redirect
    ChildVersion uint64               // 子页版本校验
    ChunkPos     model.ChunkPosition  // AO 位置（0=脏页）
}
```

**NodeState 状态机**：

```
                 ┌──────────┐
                 │  Normal  │ ← 正常操作状态
                 └────┬─────┘
        ┌─────────────┼─────────────┐
        ▼             ▼             ▼
  ┌──────────┐ ┌──────────┐ ┌───────────┐
  │Splitting │ │ Merging  │ │Compacting │  ← 结构修改进行中
  └────┬─────┘ └────┬─────┘ └─────┬─────┘
       │             │             │
       └─────────────┼─────────────┘
                     ▼
              ┌──────────┐
              │ Redirect │  ← 已从树中移除，指向新位置
              └──────────┘
```

**Read 路径如何使用 PageRef**：

```go
// searchPath 遍历（search.go）
func searchPath(rootRef *RootPageRef, key []byte) (SearchPath, error) {
    currentRef := rootRef.PageRef
    currentRef.Retain()  // refCount++

    for {
        pInfo := currentRef.GetPageInfo()  // 原子读取
        
        if pInfo.Redirect {
            currentRef = pInfo.NewRef      // 跟随重定向
            continue
        }
        
        if pInfo.IsLeaf {
            return path, nil  // 到达叶子页
        }
        
        cache := currentRef.GetChildren()  // 读取子页缓存
        idx := cache.Search(key)
        childRef := cache.Children[idx]
        childRef.Retain()
        currentRef = childRef              // 下降到子页
    }
}
```

---

## 四、第三层：BTree 存储引擎

### 4.1 BTree 结构体

代码位置：`internal/infrastructure/storage/btree/btree.go`

```go
type BTree struct {
    rootRef        *RootPageRef           // 根页面引用（CAS 替换）
    storage        *OffheapBTreeStorage   // mmap 存储管理
    size           atomic.Int64           // 逻辑 key 数量
    metrics        *BTreeMetrics          // 性能指标
    epochMgr       *EpochManager          // 旧页延迟回收
    tsGen          mvcc.TSGenerator       // MVCC 时间戳生成
    txMgr          mvcc.TxManager         // 事务管理器
    compactWp      WatermarkProvider      // Compaction 水位
}
```

### 4.2 Set 操作的完整流程

```
func (b *BTree) Set(ctx, key, value) error:
  
  1. MVCC 编码（如果启用事务）
     encoded = BuildMVCC(FlagNormal, beginTS, value)
     → [0x00][beginTS:8][value:N]

  2. writeOperation(key, mutateFunc)
     
     2a. searchPath(rootRef, key)
         → 返回 SearchPath: [root → child → ... → leafRef]
         每个 PageRef 都已 Retain()
     
     2b. 读取 oldInfo = leafRef.GetPageInfo()
         检查 IsBusy() → 如果在 Splitting/Merging，等待/重试
     
     2c. 读取 oldLeaf = GetLeafPage(oldInfo.PageID)
     
     2d. 判断是否满（IsFull）
         ├─ 不满 → 执行 mutate(oldLeaf) → COW 新页 → CAS 替换
         └─ 满   → CAS 标记 NodeSplitting → doSplitWithSplitting
                   → handleInternalSplit（向上传播）
     
     2e. CAS 成功：
         - Retire(oldInfo.PageID) ← 旧页进入 Epoch 延迟释放
         - maybeMergeAfterWrite() ← 检查是否需要 Merge
     
     2f. path.ReleaseAll() ← 释放所有 PageRef

  3. 返回成功
```

### 4.3 Split 分裂流程（树长高）

当一个页面满了，需要分裂为两个页面：

```
Before Split:                           After Split:
                                        
  Parent                                 Parent
  ┌──────────────┐                      ┌──────────────────────────┐
  │ Child[0] = A │                      │ Child[0] = Left          │
  │ Key[0] = "M" │                      │ Key[0] = "G" ← 提升的key │
  └──────┬───────┘                      │ Child[1] = Right         │
         │                              └──────────────────────────┘
         ▼                                       │
  Leaf A (满)                            ┌──────┴──────┐
  ┌────────────────┐                    ▼             ▼
  │ A B C D E F G  │              Left Leaf      Right Leaf
  │ H I J K L M N  │              ┌────────┐     ┌────────┐
  └────────────────┘              │A B C D │     │H I J K │
                                  │E F G   │     │L M N   │
                                  └────────┘     └────────┘
```

**Split 的 CAS 协议**（`handleLeafSplit`，`operations.go:736-878`）：

```
1. 标记 Splitting: leafRef.CAS(oldInfo, splittingInfo) ← NodeState=Splitting
2. Split: leftPage, rightPage, splitKey = leaf.Split()
3. Double-COW: mutate(targetHalf) ← 在正确的半页上执行修改
4. 更新父节点: parent.InsertChild(idx, splitKey, leftID, rightID)
5. CAS 父节点: parentRef.CAS(oldParInfo, newParInfo)
6. 更新 children cache: 立即可见
7. 标记旧页 Redirect: leafRef.CAS(splittingInfo, redirectInfo)
8. 级联检查: 父节点是否也满了 → handleInternalSplit
```

### 4.4 Merge 合并流程（树收缩）

当页面利用率低于 50%，触发 Lazy Merge（`handleLeafMerge`, `merge_ops.go:27-173`）：

```
Before Merge:                           After Merge:
                                        
  Parent                                 Parent
  ┌───────────────────┐                 ┌──────────────┐
  │ Child[0] = Left    │                 │ Child[0] =   │
  │ Key[0] = separator │                 │   Merged     │
  │ Child[1] = Right   │                 └──────┬───────┘
  └───────────────────┘                        │
         │              │                      ▼
         ▼              ▼               Merged Leaf
    Left Leaf      Right Leaf           ┌────────────────┐
    ┌────────┐     ┌────────┐           │ A B C D E F G H │ ← 合并了左右
    │A B C D │     │E F G H │           └────────────────┘
    └────────┘     └────────┘
```

**Merge 的 4-Phase CAS 协议**：

```
Phase 1: CAS 标记 NodeMerging（按 PageID 升序防死锁）
Phase 2: COW Merge → MergeLeaves(left, right)
Phase 3: COW 父节点 → RemoveChild + ReplaceChild → CAS parentRef
Phase 4: 标记旧页 Redirect → Epoch Retire 旧页
Underflow: 父节点稀疏？→ handleInternalMerge 向上递归
```

---

## 五、第四层：AO Chunk 落盘

AO（Append-Only）Chunk 是 NexKV 的页面持久化文件。代码位置：`internal/infrastructure/storage/chunk/`

### 5.1 .ao 文件物理布局

```
┌──────────────────────────────────────────────────────────────────┐
│                btree_{chunkID}_{seq}.ao 文件                      │
│                                                                  │
│  Offset 0x0000 ┌────────────────────────────────────────────┐   │
│                │         Header Block 0 (4096 bytes)        │   │
│  0x0000-0x0FFF │  id:0                                     │   │
│                │  rootPagePos:0                             │   │
│                │  pageCount:42                              │   │
│                │  sumOfPageLength:172200                    │   │
│                │  blockSize:4096                            │   │
│                │  format:1                                  │   │
│                │  removedPageCount:3                        │   │
│                │  (key:value 文本格式，对齐 Lealone)         │   │
│                └────────────────────────────────────────────┘   │
│                                                                  │
│  Offset 0x1000 ┌────────────────────────────────────────────┐   │
│                │   Header Block 1 (4096 bytes, 完全相同)    │   │
│  0x1000-0x1FFF │   用于崩溃恢复：Block 0 损坏 → Block 1     │   │
│                └────────────────────────────────────────────┘   │
│                                                                  │
│  Offset 0x2000 ┌────────────────────────────────────────────┐   │
│                │         Page Frame 0 (4100 bytes)          │   │
│  0x2000-0x3003 │  [CRC32C:4][PageHeader:56][...KV Data...] │   │
│                ├────────────────────────────────────────────┤   │
│  Offset 0x3004 │         Page Frame 1 (4100 bytes)          │   │
│  0x3004-0x4007 │  [CRC32C:4][PageHeader:56][...KV Data...] │   │
│                ├────────────────────────────────────────────┤   │
│                │              ...                           │   │
│                └────────────────────────────────────────────┘   │
│                                                                  │
│  文件以 256MB 为单位预分配                                        │
│  Chunk 写满后自动创建下一个 Chunk                                  │
└──────────────────────────────────────────────────────────────────┘
```

**为什么需要双 Block Header？**

写 Header 时可能崩溃：
```
危险时序：
  T1: 开始写 Block 0
  T2: 写了一半 → 崩溃 ← Block 0 损坏
  T3: Block 1 还是旧版本 ← 可以恢复！

安全时序：
  T1: 写 Block 0 → 成功
  T2: 写 Block 1 → 崩溃 ← Block 1 损坏
  T3: Block 0 是完整的新版本 ← 可以恢复！
```

**恢复逻辑**（`readHeader()`）：
```
先读 Block 0 → CRC/解析失败 → 读 Block 1 → 两者都失败 → Chunk 损坏，跳过
```

### 5.2 PageFrame：页面的磁盘表示

代码位置：`internal/infrastructure/storage/chunk/page_serializer.go`

```
┌────────────────────────────────────────────┐
│              PageFrame                     │
│                                            │
│  ┌──────────┬─────────────────────────┐   │
│  │ CRC32C   │    Page Data (payload)   │   │
│  │ 4 bytes  │    60 ~ 4096 bytes       │   │
│  │ LE u32   │                          │   │
│  └──────────┴─────────────────────────┘   │
│        ↑                                    │
│        └── CRC 覆盖 payload 部分             │
│                                             │
│  MinDiskPageSize = 4 + 56 = 60 字节         │
│  MaxDiskPageSize = 4 + 4096 = 4100 字节     │
└────────────────────────────────────────────┘
```

**序列化流程**（`Serialize`）：
```go
func (ps *PageSerializer) Serialize(ptr unsafe.Pointer, pageLength int) []byte {
    buf := make([]byte, CRCSize+pageLength)     // 分配 [4 + N] 字节
    copy(buf[CRCSize:], mmap[ptr : ptr+pageLength])  // 从 mmap 复制
    crc := wal.CRC32C(buf[CRCSize:])            // 计算 CRC（Castagnoli 多项式）
    binary.LittleEndian.PutUint32(buf[:CRCSize], crc)  // 小端序写入 CRC
    return buf
}
```

**注意**：CRC32C 使用 Castagnoli 多项式（不是 IEEE），与 WAL 保持一致，硬件加速友好。

### 5.3 ChunkPosition：页面在磁盘上的"GPS 坐标"

代码位置：`internal/domain/model/chunk_position.go`

```
ChunkPosition (uint64):
┌──────────────────────────────────────────────────────────────────┐
│ 63-38 │ 37-6       │ 5-1       │ 0          │
│ 26bit │ 32bit      │ 5bit      │ 1bit       │
│ChunkID│ FileOffset │ PageType  │ Reserved   │
└──────────────────────────────────────────────────────────────────┘
  ↑        ↑            ↑
  │        │            └── 0=内部节点, 1=叶子
  │        └── 在 .ao 文件内的字节偏移（按 4100 对齐）
  └── .ao 文件的编号（最多 6700 万个 Chunk）

ChunkPosition(0) = 脏页，尚未持久化
```

### 5.4 ChunkManager 操作流程

#### Allocate：在 Chunk 中预留空间

```go
func (cm *DiskChunkManager) Allocate(size int, pageType uint8) (ChunkPosition, error) {
    cm.mu.Lock()
    defer cm.mu.Unlock()

    // 当前 Chunk 空间不够 → 创建新 Chunk
    if cm.lastChunk.nextOffset + size > cm.lastChunk.capacity {
        cm.createChunk()  // 新文件：btree_{chunkID}_{seq}.ao
    }

    c := cm.lastChunk
    pos := EncodeChunkPosition(c.id, c.nextOffset, pageType)
    c.nextOffset += MaxDiskPageSize  // 固定步长 4100
    return pos, nil
}
```

#### WritePage：写入页面数据

```go
func (cm *DiskChunkManager) WritePage(pos ChunkPosition, data []byte) error {
    c := cm.idToChunk[pos.ChunkID()]
    
    c.mu.Lock()
    defer c.mu.Unlock()

    c.file.WriteAt(data, pos.FileOffset())     // 在指定偏移量写入
    c.pagePosToLen[pos] = int32(len(data))      // 记录长度供 ReadPage 使用
    return nil
}
```

#### ReadPage：读取页面数据

```go
func (cm *DiskChunkManager) ReadPage(pos ChunkPosition) ([]byte, error) {
    c := cm.idToChunk[pos.ChunkID()]
    
    c.mu.Lock()
    length := c.pagePosToLen[pos]  // 从内存 map 查找长度
    c.mu.Unlock()

    buf := make([]byte, length)
    c.file.ReadAt(buf, pos.FileOffset())
    return buf, nil
}
```

#### FreePage：标记页面为已删除

```go
func (cm *DiskChunkManager) FreePage(pos ChunkPosition) error {
    c := cm.idToChunk[pos.ChunkID()]
    
    c.mu.Lock()
    c.removedPages[pos] = struct{}{}     // per-chunk 标记
    cm.removedPages[pos] = struct{}{}    // 全局标记
    c.mu.Unlock()
    
    return nil  // 注意：不立即释放空间，由 ChunkCompactor 异步回收
}
```

### 5.5 ChunkCompactor：空间回收

当一个 Chunk 的 `removedPages` 占比过高时（fillRate ≤ 30%），触发压缩：

```
压缩流程：
  1. 收集全局 removedPages 快照
  2. 遍历所有 Chunk（跳过 lastChunk）计算 fillRate
  3. fillRate = 1 + 98 × liveLen / totalLen
     结果：1 = 完全空，99 = 满
  4. 选中 fillRate ≤ 30% 的 Chunk
  5. 快照活跃页面（pagePosToLen - removedPages）
  6. 将活跃页面复制到新 Chunk
  7. Sync 新 Chunk
  8. 删除旧 Chunk（rename → .ao.deleting → os.Remove）
```

---

## 六、第五层：WAL 日志

WAL（Write-Ahead Logging）保证每个操作在内存修改前先持久化到磁盘。代码位置：`internal/infrastructure/storage/wal/`

### 6.1 WAL 文件组织

```
wal_dir/
├── 00000000000000000001.wal    ← 第 1 个 Segment
├── 00000000000000001234.wal    ← 第 2 个 Segment（LSN 达到 1234 时创建）
├── 00000000000000005678.wal    ← 第 3 个 Segment
└── ...
```

**Segment 轮转**：当一个 Segment 达到 64MB 时，自动创建下一个。文件名用 20 位零填充的 LSN，保证字典序 = LSN 顺序。

### 6.2 WAL Entry 的磁盘格式

```
每个 WAL Entry 的物理布局：

┌────────┬────────┬────────┬────────┬────────┬────────┬────────┬────────┐
│ CRC32C │ Length │  LSN   │  Type  │ShardID │  Term  │  TxID  │  Time  │
│  4B BE │  4B BE │  8B BE │   1B   │   2B   │   2B   │  8B BE │  8B BE │
├────────┼────────┼────────┼────────┼────────┼────────┼────────┼────────┤
│PrevLSN │ KeyLen │ ValLen │  Key   │ Value  │Padding │Trailer │
│ 8B BE  │ 4B BE  │ 4B BE  │  N B   │  M B   │ 0~7 B  │  4B BE │
└────────┴────────┴────────┴────────┴────────┴────────┴────────┘
                                                    Trailer = 0xDEADBEEF

BE = Big Endian（大端序）

CRC32C 覆盖范围：[Length 字段开始 ... Padding 结束]
即 CRC32C 校验除自身外的所有字段
```

**Entry 头部固定 43 字节**，加上可变长度的 Key 和 Value。总长度按 8 字节对齐（Padding 填 0）。

**尾部 Magic Number**（`0xDEADBEEF`）：用于恢复时的快速边界验证。如果在一个偏移量读取的 4 字节不是 `0xDEADBEEF`，说明数据已损坏或不在 Entry 边界上。

### 6.3 WAL 写入与 Sync

```go
// 同步写入（默认）
func (dw *DiskWAL) AppendBatch(entries []*WALEntry) ([]LSN, error) {
    lsns := make([]LSN, len(entries))
    for i, entry := range entries {
        lsn := dw.nextLSN.Add(1)
        entry.LSN = lsn
        dw.writeEntry(entry)      // 写入 OS 缓冲区
        lsns[i] = lsn
    }
    dw.file.Sync()               // fsync 强制落盘
    return lsns, nil
}
```

### 6.4 Group Commit（批量提交）

高并发场景下，每次写入都 fsync 效率低。Group Commit 将多个事务的 WAL 写入合并为一次 fsync：

```
时间线：
─────────────────────────────────────────────────────→

Tx1: Append → 写入 OS 缓冲区（不 fsync）
Tx2: Append → 写入 OS 缓冲区（不 fsync）
Tx3: Append → 写入 OS 缓冲区（不 fsync）
  ↓ (1ms 超时或批量满 16 条)
Batch Flush: fsync() ← 一次 fsync 提交所有三个事务
  ↓
Tx1, Tx2, Tx3 同时收到成功确认
```

代码实现：
```go
// WALAppendItem: 异步 WAL 写入任务
type WALAppendItem struct {
    entries []*WALEntry
    result  chan LSN
}

// PostBatchHook: 批量 fsync
func (dw *DiskWAL) FlushBatch(items []*WALAppendItem) {
    dw.file.Sync()    // 一次 fsync
    for _, item := range items {
        item.SignalSuccess()
    }
}
```

### 6.5 CRC32C 校验

```
WAL Entry:  [CRC32C:4][...data...][0xDEADBEEF:4]
             ↑                        ↑
             校验范围                  Magic Number

恢复时：
  读 4 字节 → 是 0xDEADBEEF 吗？→ 否 → 数据损坏
  计算 CRC32C(data) == 记录的 CRC32C？→ 否 → 数据损坏
  
  两者都通过 → Entry 完整有效
```

### 6.6 Truncate：安全截断

Checkpoint 后，需要删除已持久化的旧 WAL 文件。采用**重命名后删除**协议防止崩溃时丢失数据：

```
安全截断协议：
  1. rename("0001.wal", "0001.wal.deleting")  ← 重命名
  2. fsync(parent_dir)                         ← 确保持久化
  3. os.Remove("0001.wal.deleting")            ← 物理删除
  4. fsync(parent_dir)                         ← 确保持久化

为什么需要重命名后删除？
  崩溃场景：
    直接删除 0001.wal → 崩溃 → 文件丢失 → 数据丢失 ❌
    
  重命名后删除：
    rename → 崩溃 → 重启后看到 0001.wal.deleting
    → cleanDeleting() 清理残留 → 安全 ✓
```

---

## 七、第六层：Checkpoint 检查点

Checkpoint 将内存中的 BTree 页面刷新到 AO Chunk，然后截断 WAL。代码位置：`internal/infrastructure/storage/checkpoint/checkpoint_manager.go`

### 7.1 为什么需要 Checkpoint？

```
没有 Checkpoint：
  WAL 文件无限增长 → 磁盘空间耗尽
  重启时需要回放所有 WAL → 恢复时间无限增长

有了 Checkpoint：
  定期将页面持久化到 AO
  WAL 只需保留 Checkpoint 之后的增量
  恢复时：加载 AO + 回放少量 WAL → 快速恢复
```

### 7.2 FuzzyCheckpoint 的 7 步流程

```
T0: 加锁（防止并发 Checkpoint）

T1: 记录 startLSN
  startLSN = wal.CurrentLSN()
  语义：startLSN 之前的 WAL 条目将被 Checkpoint 覆盖
  
T2: COW 根快照
  root := btree.RootPage()
  获取当前的根引用（COW 保证这是稳定的快照）
  
T3: DFS 枚举 + AO 刷新 ← 核心步骤
  items := btree.EnumeratePages(root)
  后序遍历所有可达页面：
    对于每个脏页（ChunkPos == 0）：
      cm.Allocate(size, pageType)  → 获得 ChunkPosition
      cm.WritePage(pos, data)      → 写入 .ao 文件
      pageLocs[pageID] = pos       → 记录映射
  cm.Sync()                        → fsync 所有 Chunk

T4: 写入 Checkpoint WAL Entry
  ckpKey = encodeCheckpointKey(startLSN, pageLocs)
  ckpEntry = WALEntry{
      Type: WALTypeCheckpoint,
      Key:  [startLSN:8][pageCount:4][(pageID,ChunkPos)*N]
  }
  wal.Append(ckpEntry)

T5: WAL Sync
  wal.Sync()  → 确保持久化

T6: WAL Truncate
  wal.Truncate(startLSN)
  删除 startLSN 之前的所有 WAL Segment

T7: 解锁 + 异步触发
  - ChunkCompactor.NeedCompaction()? → 异步 Compact
  - BTree.Compact() → 异步 Tombstone 回收
```

### 7.3 Checkpoint Key 格式

```
CheckpointEntry 的 Key 字段编码：
┌──────────────┬──────────────┬────────────────────────────────────┐
│  startLSN    │  pageCount   │  (PageID, ChunkPos) × N           │
│  8 bytes BE  │  4 bytes BE  │  16 bytes × N                     │
└──────────────┴──────────────┴────────────────────────────────────┘

恢复时：
  读取 CheckpointEntry → 解析 pageLocs 映射
  → pageLocs[PageID] = ChunkPosition
  → BTree 惰性加载：首次访问 PageID 时从 ChunkPosition 读取
```

### 7.4 SharpCheckpoint vs FuzzyCheckpoint

| 特性 | FuzzyCheckpoint | SharpCheckpoint |
|------|----------------|-----------------|
| 触发时机 | 定时（30s） | 关闭时 |
| 是否暂停写入 | 否（COW 快照） | 是 |
| pageLocs | 有 | 无（pageCount=0） |
| 用途 | 常规持久化 | 干净关闭 |

### 7.5 DirtyTracker 为什么是空的？

```
BTree COW 语义：
  每次 Set 分配新 PageID → 旧 Page 不变
  
Checkpoint 时：
  从 root 开始 DFS → 访问所有"当前版本"的页面
  → 这些就是需要持久化的"脏页"
  
  不需要额外的脏页位图 ← COW 天然解决了这个问题
```

---

## 八、第七层：MVCC 多版本并发控制

MVCC 是 NexKV 事务支持的基石。代码位置：`internal/infrastructure/storage/mvcc/`

### 8.1 Value 的 MVCC 编码

BTree 中存储的 Value 并非用户原始数据，而是带 MVCC 头的数据：

```
BTree 中的 Value：
┌──────┬──────────────────┬───────────────────────────┐
│ Flag │    beginTS        │     RealVal               │
│ 1 B  │    8 B (BE)       │     N B                   │
└──────┴──────────────────┴───────────────────────────┘
        MVCCHeader = 9 bytes

Flag = 0x00: 正常值（FlagNormal）
Flag = 0x01: 墓碑标记（FlagTombstone，表示 key 已被删除）
```

**为什么 Flag 在 BTree 内部，而不是外部？**

因为 B+Tree 只存每个 Key 的单版本最新值。Flag 内联在 BTree Value 中，意味着：
- Get 可以直接判断 key 是否存在（FlagTombstone → ErrKeyNotFound）
- Set 可以直接判断是否需要创建 VersionChain（已有旧版本才需要）
- 不需要额外的 Tombstone 位图

### 8.2 VersionChain：历史版本链

BTree 只存最新版本，历史版本存在 VersionChain 中：

```
Key "balance" 的版本链（从最新到最旧）：

BTree 当前值：               VersionChain（链表）：
┌────────────────────┐        ┌─────────────────┐
│ FlagNormal         │   ┌──→│ commitTS = 300   │ ← head
│ beginTS = 300      │   │   │ value = "250"    │
│ RealVal = "300"    │   │   │ flag = Normal    │
└────────────────────┘   │   └────────┬────────┘
                         │            │ next
                         │   ┌────────▼────────┐
                         │   │ commitTS = 200   │
                         │   │ value = "100"    │
                         │   │ flag = Normal    │
                         │   └────────┬────────┘
                         │            │ next
                         │   ┌────────▼────────┐
                         └───│ commitTS = 100   │
                             │ value = nil      │
                             │ flag = Tombstone │ ← 更早的版本
                             └─────────────────┘
```

**快照读**（snapshotTS = 250）：
```
1. 读 BTree → beginTS=300 > snapshotTS=250 → 不可见
2. 遍历 VersionChain:
   head: commitTS=300 > 250 → 太新，跳过
   node2: commitTS=200 ≤ 250 → 可见！返回 value="100"
```

### 8.3 事务生命周期

```
BeginTx():
  snapshotTS = tsGen.NextTS()
  WriteBuffer = {}
  注册到 ActiveTxRegistry

Put(key, value):
  oldVal = BTree.Get(key)  ← 读已提交最新值（ReadCommitted）
  WriteBuffer.Put(key, value, oldVal)

Get(key):
  if key in WriteBuffer:
      return WriteBuffer[key]  ← Read-Your-Own-Writes
  else:
      return snapshotGet(key) ← 快照读

Commit():
  Phase 1: PreCheck
    重新读 WriteBuffer 中所有 key 的最新值
    ValueHash 不匹配 → 冲突 → Rollback
    
  Phase 2: 分配 commitTS
    commitTS = tsGen.NextTS()
    
  Phase 3: WAL Append + Sync
    WAL.Append(所有 WriteBuffer 条目 + Commit 标记)
    WAL.Sync()
    
  Phase 4: applyWriteBuffer
    按 key 排序（防死锁）
    对每个 key: commitKey() ← 在 KeyLock 内执行

Rollback():
  applyWriteBuffer 失败 → rollbackApplied(undoBuf)
  已提交的 key 逐个回滚（best-effort undo）
```

### 8.4 commitKey：提交一个 Key 的原子操作

这是 MVCC 最核心的函数，在 per-key KeyLock 内执行：

```go
func commitKey(key, entry, commitTS) UndoEntry {
    // Step 1: 预创建 VersionChain（Lock 外）
    VersionStore.LoadOrStore(key)
    
    // Step 2: 获取 KeyLock
    KeyLock.Lock()
    defer KeyLock.Unlock()
    
    // Step 3: 读 BTree 当前值
    rawVal = storage.GetRaw(key)
    beginTS, flag, realVal = ParseMVCC(rawVal)
    
    // Step 4: 冲突检测（definitive check）
    if entry.Op == Insert && flag == FlagNormal {
        return ErrConflict  // key 已存在
    }
    if entry.Op == Update/Delete && beginTS != entry.OldBeginTS {
        return ErrConflict  // beginTS 变了 = 被其他事务修改了
    }
    
    // Step 5: Prepend-before-Set
    VersionChain.Prepend(commitTS, oldVal, oldFlag)
    // 先建链，保证旧值不会丢失
    
    // Step 6: Set BTree
    newVal = BuildMVCC(flag, commitTS, newVal)
    storage.Set(key, newVal)
    
    return UndoEntry{...}
}
```

**为什么 Prepend-before-Set？**

```
Prepend-before-Set（当前方案）：
  崩溃在 Prepend 后、Set 前：
    BTree 值未变（仍是旧值）
    VersionChain 有额外节点
    → 恢复时重做 Set → 幂等安全 ✓

Set-before-Prepend（旧方案）：
  崩溃在 Set 后、Prepend 前：
    BTree 值已更新（新值）
    VersionChain 无旧版本
    → 旧值永久丢失 ❌
```

### 8.5 快照读（snapshotGet）—— 无锁乐观读

```go
func snapshotGet(key, snapshotTS) (value, error) {
    for retry := 0; retry < 3; retry++ {
        // Step 1: 读 BTree 当前值
        rawVal = storage.GetRaw(key)
        beginTS, flag, realVal = ParseMVCC(rawVal)
        
        // Step 2: 可见性判断
        if beginTS <= snapshotTS {
            // BTree 版本可见
            if flag == FlagTombstone { return ErrKeyNotFound }
            return deepCopy(realVal)
        }
        
        // Step 3: BTree 版本太新 → 遍历 VersionChain
        chain = VersionStore.Load(key)
        gen = chain.Generation()  ← 记录代数（乐观校验）
        
        // 遍历链表找 commitTS ≤ snapshotTS 的最大版本
        bestNode = nil
        for node = chain.head; node != nil; node = node.next {
            if node.rolledBack || node.reclaimed { continue }
            if node.commitTS <= snapshotTS {
                bestNode = node
                break
            }
        }
        
        // Step 4: 乐观校验
        if chain.Generation() != gen {
            continue  // 链在遍历期间被修改 → 重试
        }
        
        if bestNode == nil { return ErrKeyNotFound }
        if bestNode.flag == FlagTombstone { return ErrKeyNotFound }
        return deepCopy(bestNode.value)
    }
}
```

### 8.6 VersionChain GC

```go
func (vc *VersionChain) Prune(watermark uint64) int {
    // watermark = min(所有活跃事务的 snapshotTS)
    // 含义：commitTS < watermark 的版本对任何活跃事务都不可见
    
    // 保留规则：
    // 1. 链头始终保留（BTree 中的最新版本）
    // 2. commitTS < watermark 的最近版本保留（快照可能需要）
    // 3. 如果保留的版本是 Tombstone，额外保留前一个非 Tombstone 版本
    //    （防止旧快照看到 key "复活"）
    
    // 其他 commitTS < watermark 的节点标记为 reclaimed
}
```

---

## 九、第八层：Epoch 页面回收

### 9.1 问题：COW 旧页何时可以释放？

```
COW Set 操作：
  oldLeaf (PageID=5) ──CAS──→ newLeaf (PageID=99)
  
  Page 5 现在不被 BTree 引用，可以释放吗？
  
  危险时序：
    T1: Reader R 读 rootRef → 看到 PageID=5
    T2: Writer W CAS 切换到 PageID=99
    T3: W FreePage(5)          ← 回收 Page 5
    T4: Allocator 把 Page 5 分配给新页面 → count=0
    T5: R 读 Page 5 → count=0 → PANIC ❌
```

### 9.2 EpochManager：延迟释放

代码位置：`internal/infrastructure/storage/btree/epoch.go`

```
核心思想：记录旧页时标记当前 epoch，仅当所有看到该 epoch 的 reader 都退出后才释放。

数据结构：
  globalEpoch: 全局 epoch 计数器（仅在 tryReclaim 时递增）
  readers[64]: 每个 reader slot 的当前 epoch（0=inactive）
  slots[64]:   每个 slot 的 retired 页面列表（MPSC ring buffer）
```

**Reader 协议**：
```go
// 读路径
func (b *BTree) Get(key) {
    slot := epochMgr.AllocSlot()      // 分配一个 slot
    epochMgr.EnterRead(slot)          // 注册当前 epoch
    defer epochMgr.ExitRead(slot)     // 退出

    path := searchPath(rootRef, key)  // 遍历 BTree
    // ... 读页面数据 ...
    return value
}

// EnterRead: 双检协议
func (em *EpochManager) EnterRead(slot int) {
    epoch := em.globalEpoch.Load()
    em.readers[slot].Store(epoch)
    // 双检：防止在 Load 和 Store 之间 globalEpoch 被推进
    if em.globalEpoch.Load() != epoch {
        em.readers[slot].Store(em.globalEpoch.Load())
    }
}
```

**Writer 协议**：
```go
// 写路径 CAS 成功后
oldPageID := oldInfo.PageID
epochMgr.Retire(slot, oldPageID)  // 追加到 retire 列表，不立即释放
```

**Reclaimer（后台 500ms ticker）**：
```go
func (em *EpochManager) tryReclaim() {
    newEpoch := em.globalEpoch.Add(1)      // 推进 epoch
    
    // 计算 safeEpoch = min(所有活跃 reader 的 epoch)
    safeEpoch := min(em.readers[*].Load())
    
    // 遍历所有 slot
    for _, slot := range em.slots {
        slot.mu.Lock()
        list := slot.list
        slot.list = nil
        slot.mu.Unlock()
        
        for _, page := range list {
            if page.epoch < safeEpoch {
                em.freeFn(page.pageID)      // 安全：无 reader 引用
            } else {
                // 还可能有 reader → 放回列表
                slot.list = append(slot.list, page)
            }
        }
    }
}
```

**安全性证明**：

```
Reader R                     Writer W                    Reclaimer
────────                     ────────                    ─────────
EnterRead(slot)                                          
  epoch = globalEpoch(=5)                                
  readers[slot] = 5                                       
                              CAS(old→new) ✓              
                              Retire(oldPage, epoch=5)    
                                                         tryReclaim()
                                                           newEpoch=6
                              ExitRead(slot)                safeEpoch=5(min)
                                readers[slot]=0             
                                                         5 < 5 = false → 不回收 ✓
```

---

## 十、第九层：崩溃恢复

### 10.1 恢复全景

```
崩溃发生 ↓
────────────────────────────────────────────────────────
重启：
  
  Phase A: 基础设施初始化
  ├── 1. ChunkManager 初始化（RestoreDiskChunkManager）
  │      扫描 .ao 文件 → 重建 pagePosToLen → 恢复 Chunk 结构
  ├── 2. PageManager 初始化（空 mmap 池）
  └── 3. WAL 扫描 → 找最新 CheckpointEntry
  
  Phase B: BTree 结构重建
  ├── 4. 解析 CheckpointEntry → rootPageID + pageLocs
  ├── 5. OffheapBTreeStorage.pageLocs ← pageLocs 映射
  └── 6. RebuildBTree(rootPageID) → 惰性加载 PageRef 图
  
  Phase C: 增量 WAL 回放
  ├── 7. 从 checkpointStartLSN 开始扫描 WAL
  ├── 8. 按 TxID 分组，丢弃无 Commit 标记的事务
  ├── 9. 对每个已提交事务：
  │     对每个 key：
  │       - 三阶段幂等性检查（beginTS 比对）
  │       - 重建 BTree 值 + VersionChain
  └── 10. 恢复完成，BTree 可服务
```

### 10.2 WAL 恢复协议

```go
func recoverFromWAL(entries []WALEntry) {
    // 按 TxID 分组
    txGroups := groupByTxID(entries)
    
    for txID, txEntries := range txGroups {
        // 检查事务完整性
        hasCommit := hasCommitMarker(txEntries)
        if !hasCommit {
            continue  // 未提交事务 → 丢弃
        }
        
        // 提取 commitTS
        commitTS := extractCommitTS(txEntries)
        
        for _, entry := range txEntries {
            if entry.Type == WALTypeCommit { continue }
            
            // 三阶段幂等性检查
            currentVal := btree.GetRaw(entry.Key)
            currentBeginTS := ParseMVCC(currentVal).BeginTS
            
            if currentBeginTS > commitTS {
                continue  // 已有更新版本 → 跳过
            }
            if currentBeginTS == commitTS {
                // 检查 VersionChain 是否已存在
                if versionChainContains(entry.Key, commitTS) {
                    continue  // 已回放 → 跳过
                }
            }
            
            // 执行回放
            applyWALEntry(entry, commitTS)
        }
    }
}
```

### 10.3 .ao 文件恢复

```go
func RestoreDiskChunkManager(dir string) *DiskChunkManager {
    // Step 1-2: 扫描目录，删除零长度文件
    files := os.ReadDir(dir)
    
    // Step 3: 解析文件名，去重（同 chunkID 留最高 seq）
    for _, f := range files {
        chunkID, seq := parseChunkFilename(f.Name())
        // btree_0_1.ao → chunkID=0, seq=1
        keep max seq per chunkID
    }
    
    // Step 4-6: 按 seq 排序，打开文件，验证双 Block Header
    for _, f := range sortedFiles {
        chunkFile := openChunkFile(f)
        header := readHeader(chunkFile)
        // Block 0 损坏 → Block 1 fallback
        // 两者都损坏 → 跳过此 Chunk
    }
    
    // Step 7: scanPageFrames 重建 pagePosToLen
    for offset := ChunkHeaderSize; offset < fileSize; offset += MaxDiskPageSize {
        frame := readFrame(offset)
        if CRC32C(frame.data) == frame.crc {
            pagePosToLen[pos] = MaxDiskPageSize
        }
    }
    
    // Step 8: 重建内存结构
    rebuild chunkIDs, idToChunk, seqToID, chunks
}
```

### 10.4 Jump Scan：损坏恢复

当 WAL 文件部分损坏时，恢复不放弃整个文件，而是执行 Jump Scan：

```
正常读取：
  [Entry 0][Entry 1][Entry 2]...[Entry N]
   ↑ 顺序读取每个 Entry

Entry 2 的 CRC 校验失败：
  [Entry 0][Entry 1][XXXXXXX]...[Entry N]
                        ↑
                    从此处开始 Jump Scan：
                    以 8 字节步长向前扫描
                    在每个偏移量尝试：
                      读 4 字节 → == 0xDEADBEEF？→ 是：找到下一个 Entry 边界
                      计算 CRC → 匹配？→ 是：有效 Entry
                    
                    连续 16 次失败 → 放弃文件剩余部分
                    
  结果：恢复了 Entry 0, 1, (跳过错位部分), Entry K, K+1, ..., N
```

---

## 十一、完整数据流：一条 Put 的旅程

现在把所有层次串起来，追踪一条 `Put("balance", "100")` 的完整旅程。

### 11.1 写入路径

```
┌─────────────────────────────────────────────────────────────────┐
│ Step 1: Client API                                               │
│   btree.Set(ctx, "balance", "100")                               │
└────────────────────────┬────────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────────┐
│ Step 2: MVCC 编码（如果启用事务）                                 │
│   tx.Put("balance", "100")                                      │
│     → WriteBuffer["balance"] = {Op:Insert, Value:"100"}         │
│   tx.Commit():                                                  │
│     → commitTS = 1000                                           │
│     → encoded = BuildMVCC(FlagNormal, 1000, "100")              │
│     → encoded = [0x00][0x00...0x3E8][100]                       │
│                    ↑ Flag  ↑ beginTS=1000  ↑ "100"              │
│     → WAL.Append({Type:Insert, Key:"balance", Value:encoded})   │
│     → WAL.Sync() ← fsync 保证持久化                              │
└────────────────────────┬────────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────────┐
│ Step 3: BTree writeOperation                                    │
│   searchPath(rootRef, "balance")                                │
│     → root → internal_node → leafRef (指向 PageID=5)             │
│                                                                  │
│   oldLeaf = GetLeafPage(5)  ← 从 mmap 读取                       │
│   oldInfo = {PageID:5, Version:10, IsLeaf:true}                 │
│                                                                  │
│   检查 oldLeaf.IsFull() → 未满                                   │
│                                                                  │
│   COW mutate:                                                    │
│     newPageID = AllocLeafPage() → 99                             │
│     newLeaf = copy(oldLeaf) + Insert("balance", encoded)         │
│     newInfo = {PageID:99, Version:11, IsLeaf:true}              │
│                                                                  │
│   CAS: leafRef.CAS(oldInfo, newInfo) → ✓                         │
│     → BTree 现在指向 Page 99                                     │
│                                                                  │
│   Retire(5) ← Page 5 进入 Epoch 延迟释放                          │
│   path.ReleaseAll()                                              │
└────────────────────────┬────────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────────┐
│ Step 4: Page 99 的 mmap 布局                                     │
│                                                                  │
│   Offset 0-55:  PageHeader                                       │
│     version = 11                                                 │
│     count = 1                                                    │
│     pageType = 1 (Leaf)                                         │
│                                                                  │
│   Offset 56-71: LeafEntry[0]                                     │
│     keyOff = 4079, keyLen = 7  → "balance"                       │
│     valOff = 4067, valLen = 12 → [0x00,0x00..0x3E8,"100"]       │
│                                                                  │
│   Offset 4067-4095: KV Data                                      │
│     "balance" (7 bytes) + MVCC-encoded "100" (12 bytes)          │
└────────────────────────┬────────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────────┐
│ Step 5: Checkpoint（30 秒后触发）                                 │
│                                                                  │
│   T1: startLSN = 5000                                            │
│   T2: root snapshot                                              │
│   T3: enumeratePages(root):                                      │
│        → PageFlushItem{PageID:99, PageType:Leaf, PageData:...}  │
│        → cm.Allocate(4100, PageTypeLeaf) → pos = (chunk=0, off=0)│
│        → cm.WritePage(pos, serialize(Page 99))                   │
│        → pageLocs[99] = pos                                      │
│        → cm.Sync()                                               │
│                                                                  │
│   .ao 文件现在包含：                                              │
│     [Header Block 0][Header Block 1]                             │
│     [CRC32C][PageHeader(v=11,count=1)][LeafEntry][KV Data]       │
│                                                                  │
│   T4-T6: WAL Checkpoint + Sync + Truncate                        │
│     → Checkpoint 之前的 WAL 文件被删除                            │
└─────────────────────────────────────────────────────────────────┘
```

### 11.2 读取路径

```
┌─────────────────────────────────────────────────────────────────┐
│ Step 1: Client API                                               │
│   btree.Get(ctx, "balance")                                      │
└────────────────────────┬────────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────────┐
│ Step 2: Epoch 注册                                               │
│   slot = epochMgr.AllocSlot()                                    │
│   epochMgr.EnterRead(slot)                                       │
│   defer epochMgr.ExitRead(slot)                                  │
└────────────────────────┬────────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────────┐
│ Step 3: searchPath                                               │
│   rootRef.PageRef → pInfo → IsLeaf? No → GetChildren             │
│   → cache.Search("balance") → childIdx                           │
│   → childRef → pInfo → IsLeaf? Yes → 到达叶子页                  │
│                                                                  │
│   leafRef.GetPageInfo() → {PageID:99, Version:11}               │
│   GetLeafPage(99) → 从 mmap offset 99×4096 读 PageHeader         │
│   leaf.Search("balance") → idx=0, found=true                     │
│   rawVal = leaf.GetValue(0) → [0x00][0x00..0x3E8][100]          │
└────────────────────────┬────────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────────┐
│ Step 4: MVCC 解析                                                │
│   ParseMVCC(rawVal):                                             │
│     flag = 0x00 (FlagNormal)                                     │
│     beginTS = 1000                                               │
│     realVal = "100"                                              │
│                                                                  │
│   可见性判断（snapshotTS = 1200）:                                │
│     beginTS(1000) ≤ snapshotTS(1200) → 可见                      │
│     返回 "100"                                                   │
└─────────────────────────────────────────────────────────────────┘
```

### 11.3 崩溃恢复路径

```
崩溃后重启：
┌─────────────────────────────────────────────────────────────────┐
│ Phase A:                                                        │
│   RestoreDiskChunkManager(dir)                                   │
│     → 扫描 3 个 .ao 文件                                         │
│     → scanPageFrames: 每个文件 4100 字节步长，CRC 验证            │
│     → 重建 pagePosToLen: {pos1:4100, pos2:4100, ...}            │
│                                                                  │
│   WAL 扫描:                                                      │
│     → 找到最新 CheckpointEntry: startLSN=5000                    │
│     → 解析 pageLocs: {99: ChunkPosition(chunk=0, offset=8192)}  │
│     → 设置 storage.pageLocs[99] = ChunkPosition(...)             │
└────────────────────────┬────────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────────┐
│ Phase B:                                                        │
│   RebuildBTree(rootPageID=1):                                    │
│     创建 PageRef(rootPageID=1)                                   │
│     rootPageRef.GetPageInfo() → 需要惰性加载                      │
│                                                                  │
│   惰性加载 Page 99:                                              │
│     pos = pageLocs[99] → ChunkPosition(chunk=0, offset=8192)    │
│     data = cm.ReadPage(pos) → [CRC32C:4][PageHeader+Data]        │
│     serializer.Deserialize(data, mmap[99×4096])                   │
│       → 验证 CRC → 复制到 mmap                                   │
│     → Page 99 现在在内存中                                       │
└────────────────────────┬────────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────────┐
│ Phase C:                                                        │
│   WAL 回放（LSN 5000 → 最新）:                                   │
│     Entry at LSN 5001: {Type:Insert, Key:"balance", Value:...}  │
│       → TxID=42, 无 Commit 标记 → 跳过（未提交事务）              │
│                                                                  │
│     Entry at LSN 5020: {Type:Insert, Key:"balance", Value:...}  │
│       → TxID=43, 有 Commit 标记 → commitTS=1100                  │
│       → 当前 BTree: "balance" beginTS=1000 ≤ 1100                │
│       → 幂等检查通过 → Apply                                      │
│       → BTree.Set("balance", BuildMVCC(Normal, 1100, "200"))    │
│       → VersionChain.Prepend(1100, oldVal, oldFlag)              │
│                                                                  │
│   恢复完成！                                                     │
└─────────────────────────────────────────────────────────────────┘
```

---

## 十二、关键设计决策汇总

| # | 决策 | 原因 | 位置 |
|---|------|------|------|
| 1 | **4KB Page + mmap** | 直接内存映射，零拷贝读取，O(1) PageID→Ptr | `offheap/` |
| 2 | **PageHeader 56B 固定头** | 通过 `unsafe.Pointer` 直接映射，无需反序列化 | `offheap/page_layout.go` |
| 3 | **Entry Array + KV Data 相向增长** | 最大化空间利用，只在相遇时页面才满 | `offheap/page_layout.go` |
| 4 | **页面级 COW（非行级）** | 简化并发控制，整页 CAS 原子替换 | `btree/operations.go` |
| 5 | **CAS 乐观锁（非 Mutex）** | 无锁读，写路径 CAS 重试，高并发性能 | `btree/page_ref.go` |
| 6 | **NodeState 状态机** | Splitting/Merging/Compacting 标记防止并发结构修改 | `btree/page_info.go` |
| 7 | **WAL 先行 + AO Checkpoint** | 低延迟顺序写 WAL + 高吞吐页面落盘 AO | `wal/` + `chunk/` |
| 8 | **Dual-Block Header** | 崩溃时 Header 损坏 → Block 1 fallback | `chunk/chunk_file.go` |
| 9 | **CRC32C (Castagnoli)** | WAL 和 AO 统一使用，硬件加速（SSE4.2 / ARM CRC32） | `wal/crc.go` |
| 10 | **PageFrame = CRC + Payload** | 每个页面帧自校验，损坏只丢一帧 | `chunk/page_serializer.go` |
| 11 | **Group Commit** | 批量 fsync，减少磁盘 I/O | `wal/wal_append_item.go` |
| 12 | **Rename-before-Delete** | 安全截断 WAL，崩溃可恢复 | `wal/diskwal.go` |
| 13 | **Fuzzy Checkpoint (COW)** | 不暂停写入的快照式 Checkpoint | `checkpoint/` |
| 14 | **惰性加载（Lazy Load）** | 恢复时不加载所有页面，按需从 AO 读取 | `btree/offheap_storage.go` |
| 15 | **9-byte MVCC Header** | Flag + beginTS 内联在 BTree Value 中 | `mvcc/codec.go` |
| 16 | **Prepend-before-Set** | 先建链再写 BTree，崩溃后旧值不丢失 | `mvcc/transaction.go` |
| 17 | **无锁 snapshotGet** | 乐观读 + generation 校验 + 重试 | `mvcc/transaction.go` |
| 18 | **Epoch-based Reclamation** | COW 旧页安全延迟释放 | `btree/epoch.go` |
| 19 | **Jump Scan 恢复** | 损坏 WAL 不放弃全部数据 | `wal/diskwal.go` |
| 20 | **ChunkCompactor 贪心选择** | 低填充率 Chunk 空间回收 | `chunk/chunk_compactor.go` |

---

## 附录 A：关键文件索引

| 层 | 文件 | 内容 |
|----|------|------|
| Page | `offheap/page_layout.go` | PageHeader、LeafEntry、IndexEntry、PageAccessor |
| Page | `offheap/page_manager.go` | mmap 管理、Alloc/Free、LockFreeQueue |
| BTree | `btree/btree.go` | BTree 结构体、Set/Get/Delete |
| BTree | `btree/operations.go` | writeOperation、handleLeafSplit、handleInternalSplit |
| BTree | `btree/merge_ops.go` | handleLeafMerge、handleInternalMerge、mergeRoot |
| BTree | `btree/page_ref.go` | PageRef CAS、retain/release、children cache |
| BTree | `btree/page_info.go` | PageInfo、NodeState、IsBusy |
| BTree | `btree/search.go` | searchPath、SearchPath、Redirect 跟随 |
| BTree | `btree/epoch.go` | EpochManager、EnterRead/ExitRead/Retire/tryReclaim |
| BTree | `btree/compaction.go` | Tombstone compaction |
| WAL | `wal/diskwal.go` | DiskWAL、Segment 轮转、Group Commit、Truncate |
| WAL | `wal/types.go` | WALEntry 格式、编解码、CRC32C |
| WAL | `wal/recovery.go` | WAL 恢复（RecoverFromWAL） |
| WAL | `wal/recovery_manager.go` | 三阶段恢复协议 |
| Chunk | `chunk/disk_chunk_manager.go` | Allocate/WritePage/ReadPage/FreePage/Restore |
| Chunk | `chunk/chunk_file.go` | ChunkFile、readHeader/writeHeader、scanPageFrames |
| Chunk | `chunk/chunk_header.go` | ChunkHeader 文本编解码 |
| Chunk | `chunk/page_serializer.go` | PageFrame CRC+序列化 |
| Chunk | `chunk/chunk_compactor.go` | 空间回收压缩 |
| Checkpoint | `checkpoint/checkpoint_manager.go` | FuzzyCheckpoint T0-T7 |
| MVCC | `mvcc/transaction.go` | TxManager、BeginTx、Commit、commitKey、Rollback |
| MVCC | `mvcc/version_chain.go` | VersionChain、Prepend、Prune |
| MVCC | `mvcc/codec.go` | BuildMVCC、ParseMVCC、9-byte Header |
| MVCC | `mvcc/write_buffer.go` | WriteBuffer 暂存 |
| MVCC | `mvcc/gc.go` | 后台 GC 循环 |
| MVCC | `mvcc/key_lock.go` | Per-key 自旋锁 |
| MVCC | `mvcc/wal_integration.go` | WriteBuffer → WAL Entry 转换 |
| Model | `domain/model/chunk_position.go` | ChunkPosition 64-bit 编码 |
| Model | `domain/model/btree_types.go` | PageID、BTreeConfig |

---

## 附录 B：磁盘文件格式速查

| 文件类型 | 命名 | 格式 |
|---------|------|------|
| WAL Segment | `%020d.wal` | `[CRC32C:4][Length:4][Header:43][Key:?][Value:?][Pad:?][0xDEADBEEF:4]` |
| AO Chunk | `btree_{id}_{seq}.ao` | `[Header:4096][Header:4096][Frame:4100]...[Frame:4100]` |
| Chunk Header | (同上) | Text: `key:value\n` × 12 fields, padded to 4096B |
| Page Frame | (同上) | `[CRC32C:4 LE][PageHeader:56][Entries:16×N][KVData:?]` |

---

**文档版本**：v1.0
**创建日期**：2026-05-23
**字数**：~18,000 中文 + ~8,000 代码/图表 = 约 26,000 字（按中文字数统计方式）
