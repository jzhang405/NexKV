# Lealone 序列化实现研究总结

**日期**: 2026-03-09
**主题**: KV → Page 序列化机制深度分析
**基于**: `thoughts/2026-03-09-kv-to-page-serialization.md` + `thoughts/2026-06-08-lealone-btree-deep-dive.md`

---

## 一、核心发现：Lealone 的序列化策略

### 1.1 设计原则

```
Lealone 的 Page 序列化遵循以下原则：

1. 固定大小：Page 固定 4KB（4096 bytes）
   - 避免内存碎片
   - 简化内存管理
   - 便于磁盘 I/O（对齐）

2. 变长字段：Key/Value 使用 Length + Data 格式
   - 灵活支持不同大小的键值
   - 避免浪费空间

3. 延迟序列化：只在需要时序列化
   - 内存操作时保持 Node 对象
   - 持久化时才序列化到 Page

4. 三层缓存：L1(Page) → L2(Buffer) → L3(Disk)
   - 热数据：已反序列化的 Page 对象
   - 温数据：序列化后的 ByteBuffer
   - 冷数据：磁盘文件
```

### 1.2 与传统方案的对比

| 维度 | 传统 B+Tree | Lealone BTree |
|------|-----------|----------------|
| **页面大小** | 固定 4KB | 固定 4KB ✅ |
| **节点存储** | Page 级别 | Node → Page 序列化 |
| **缓存策略** | 单层缓存 | 三层缓存（L1/L2/L3）✅ |
| **序列化时机** | 立即序列化 | 延迟序列化 ✅ |
| **内存效率** | 中等 | 高（L2 Buffer）✅ |

---

## 二、Page 内存布局详解

### 2.1 完整布局（4096 bytes）

```
Page Structure (4KB = 4096 bytes)
├── Header (21 bytes)
│   ├── Type: 1 byte         (LeafPage/NodePage)
│   ├── Version: 8 bytes     (并发版本号)
│   ├── ID: 8 bytes          (PageID)
│   └── RefCount: 4 bytes    (引用计数)
│
└── Data (4075 bytes)
    ├── Metadata (5 bytes)
    │   ├── IsLeaf: 1 byte
    │   └── NumKeys: 4 bytes
    │
    ├── Keys Section (变长)
    │   └── [KeyLen: 2 bytes][KeyData: KeyLen]
    │       [KeyLen: 2 bytes][KeyData: KeyLen]
    │       ...
    │
    ├── Values Section (变长，仅叶子节点)
    │   └── [ValueLen: 2 bytes][ValueData: ValueLen]
    │       [ValueLen: 2 bytes][ValueData: ValueLen]
    │       ...
    │
    └── Children Section (变长，仅内部节点)
        └── [ChildPageID: 8 bytes]
            [ChildPageID: 8 bytes]
            ...
```

### 2.2 字节级示例（叶子节点）

```
输入 Node:
  IsLeaf: true
  Keys: ["key1", "key2"]
  Values: ["val1", "val2"]

序列化输出 Page.Data:
Offset  Hex                         Content
────────────────────────────────────────────────────────────
0x00    01                          IsLeaf = 1 (true)
0x01    00 00 00                   Padding
0x04    02 00 00 00                NumKeys = 2

0x08    04 00                       Key[0] Length = 4
0x0A    6b 65 79 31                 "key1"
0x0E    05 00                       Value[0] Length = 5
0x10    76 61 6c 75 31              "val1"

0x15    03 00                       Key[1] Length = 3
0x17    6b 65 79                    "key2"
0x1A    05 00                       Value[1] Length = 5
0x1C    76 61 6c 75 31              "val2"

...     ...                         (剩余空间 padding)
```

---

## 三、序列化流程（Node → Page）

### 3.1 Lealone 的实现模式

```java
// Lealone Page.write() 方法（简化版）
public void write(PageInfo pInfo, Chunk chunk, DataBuffer buff) {
    // 1. 写入页面类型
    buff.putByte((byte) getType());

    // 2. 写入键数量
    buff.putInt(map.size());

    // 3. 写入键值对（叶子节点）
    for (Map.Entry<Object, Object> entry : map.entrySet()) {
        // 写入 Key
        Object key = entry.getKey();
        writeObject(buff, key);

        // 写入 Value
        Object value = entry.getValue();
        writeObject(buff, value);
    }

    // 4. 写入子节点引用（内部节点）
    if (!isLeafType()) {
        for (Map.Entry<Object, PageReference> entry : children.entrySet()) {
            PageReference ref = entry.getValue();
            buff.putLong(ref.getPage().getPagePos());
        }
    }
}
```

### 3.2 Go 移植实现

```go
// serializer.go - SerializeNode
func SerializeNode(node *Node, page *Page) error {
    var buf bytes.Buffer
    buf.Grow(1024)  // 预分配，减少扩容

    // 1. 写入元数据 (5 bytes)
    buf.WriteByte(byte(node.IsLeaf))                    // 1 byte
    binary.Write(&buf, binary.LittleEndian, uint32(len(node.Keys))) // 4 bytes

    // 2. 写入 Keys (变长)
    for _, key := range node.Keys {
        binary.Write(&buf, binary.LittleEndian, uint16(len(key))) // 2 bytes
        buf.Write(key)  // KeyData
    }

    // 3. 写入 Values（叶子节点）
    if node.IsLeaf {
        for _, value := range node.Values {
            binary.Write(&buf, binary.LittleEndian, uint16(len(value))) // 2 bytes
            buf.Write(value)  // ValueData
        }
    }

    // 4. 写入 Children PageIDs（内部节点）
    if !node.IsLeaf {
        for _, childID := range node.Children {
            binary.Write(&buf, binary.LittleEndian, uint64(childID)) // 8 bytes
        }
    }

    // 5. 复制到 Page.Data
    copy(page.Data[:], buf.Bytes())

    // 6. 标记脏页
    page.MarkDirty()

    return nil
}
```

### 3.3 关键优化点

```
性能优化：
1. bytes.Buffer 预分配（Grow 1024）
   - 减少扩容次数
   - 降低内存分配

2. 直接使用 binary.Write/Read
   - 避免 JSON/XML 序列化开销
   - 二进制格式紧凑

3. 固定 4KB Page
   - 避免内存碎片
   - 简化内存管理

预期性能：
- 序列化：~500-1000 ns/op
- 内存分配：~2-3 次/op
- 内存拷贝：~1-2 次/op
```

---

## 四、反序列化流程（Page → Node）

### 4.1 Lealone 的实现模式

```java
// Lealone Page.read() 方法（简化版）
public void read(DataBuffer buff) {
    // 1. 读取页面类型
    type = buff.getByte();

    // 2. 读取键数量
    int keyCount = buff.getInt();

    // 3. 读取键值对（叶子节点）
    if (isLeafType()) {
        map = new HashMap<>(keyCount);
        for (int i = 0; i < keyCount; i++) {
            Object key = readObject(buff);
            Object value = readObject(buff);
            map.put(key, value);
        }
    }

    // 4. 读取子节点引用（内部节点）
    else {
        children = new HashMap<>(keyCount);
        for (int i = 0; i < keyCount; i++) {
            long pagePos = buff.getLong();
            // 创建 PageReference
            children.put(keys[i], createRef(pagePos));
        }
    }
}
```

### 4.2 Go 移植实现

```go
// serializer.go - DeserializeNode
func DeserializeNode(page *Page) (*Node, error) {
    node := &Node{
        ID: page.ID,
    }

    buf := bytes.NewBuffer(page.Data[:])

    // 1. 读取元数据 (5 bytes)
    isLeafByte, err := buf.ReadByte()
    if err != nil {
        return nil, err
    }
    node.IsLeaf = isLeafByte != 0

    var numKeys uint32
    if err := binary.Read(buf, binary.LittleEndian, &numKeys); err != nil {
        return nil, err
    }

    // 2. 读取 Keys (变长)
    node.Keys = make([][]byte, 0, numKeys)
    for i := 0; i < int(numKeys); i++ {
        var keyLen uint16
        if err := binary.Read(buf, binary.LittleEndian, &keyLen); err != nil {
            return nil, err
        }

        key := make([]byte, keyLen)
        if _, err := buf.Read(key); err != nil {
            return nil, err
        }
        node.Keys = append(node.Keys, key)
    }

    // 3. 读取 Values（叶子节点）
    if node.IsLeaf {
        node.Values = make([][]byte, 0, numKeys)
        for i := 0; i < int(numKeys); i++ {
            var valueLen uint16
            if err := binary.Read(buf, binary.LittleEndian, &valueLen); err != nil {
                return nil, err
            }

            value := make([]byte, valueLen)
            if _, err := buf.Read(value); err != nil {
                return nil, err
            }
            node.Values = append(node.Values, value)
        }
    }

    // 4. 读取 Children PageIDs（内部节点）
    if !node.IsLeaf {
        node.Children = make([]model.PageID, 0, numKeys+1)
        for i := 0; i <= int(numKeys); i++ {
            var childID uint64
            if err := binary.Read(buf, binary.LittleEndian, &childID); err != nil {
                return nil, err
            }
            node.Children = append(node.Children, model.PageID(childID))
        }
    }

    return node, nil
}
```

---

## 五、三层缓存与序列化集成

### 5.1 序列化在缓存中的作用

```
┌─────────────────────────────────────────────────────────────┐
│           三层缓存 + 序列化流程                             │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  写操作（Node → Page → L3）:                                │
│  1. SerializeNode(node, page)                                │
│     - 将 Node 的 Keys/Values 序列化到 page.Data             │
│  2. page.MarkDirty()                                         │
│  3. L2 缓存：copy(page.Data)                                 │
│  4. L3 持久化：storage.FlushPage(page)                       │
│                                                              │
│  读操作（L3 → L2 → L1 → Node）:                               │
│  1. L3：storage.LoadPage(pageID) → page.Data                  │
│  2. L2 缓存：l2Cache.Put(pageID, page.Data)                   │
│  3. L1 反序列化：DeserializeNode(page) → node                 │
│  4. L1 缓存：l1Cache.Put(pageID, page)                         │
│                                                              │
│  缓存降级：                                                   │
│  1. L1 → L2：releasePage()                                  │
│     - 释放 Page 对象，保留 page.Data                         │
│  2. L2 → L3：releaseBuff()                                  │
│     - 释放 page.Data，数据保留在磁盘                         │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

### 5.2 性能优化策略

```
优化点：
1. L2 缓存：保留序列化后的 page.Data
   - 避免：重复序列化 Node → Page
   - 收益：~500 ns/op（序列化开销）

2. L1 缓存：保留已反序列化的 Page 对象
   - 避免：重复反序列化 Page → Node
   - 收益：~800 ns/op（反序列化开销）

3. 延迟反序列化：只在 L1 命中时才反序列化
   - 减少：反序列化次数
   - 收益：~70% 反序列化操作（L2 命中率）

预期缓存命中率：
- L1 命中率：~85%（热数据，Page 对象）
- L2 命中率：~10%（温数据，ByteBuffer）
- L3 命中率：~5%（冷数据，磁盘）
```

---

## 六、与现有纯内存方案的对比

### 6.1 架构对比

```
┌─────────────────────────────────────────────────────────────┐
│  纯内存方案（当前）                                          │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  Node *                                                      │
│  ├─ Keys: [][]byte                                          │
│  ├─ Values: [][]byte                                        │
│  └─ Children: []*Node                                       │
│                                                              │
│  特点：                                                      │
│  ✅ 极致性能：10.97 ns/op                                     │
│  ❌ 无法持久化：崩溃数据全丢                                 │
│  ❌ 内存碎片：403 次分配/op                                  │
│                                                              │
└─────────────────────────────────────────────────────────────┘

                    ↓ 迁移

┌─────────────────────────────────────────────────────────────┐
│  Page 层方案（基于 Lealone）                                │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  Page (4KB 固定)                                            │
│  ├─ Header: 21 bytes                                       │
│  └─ Data: 4075 bytes                                       │
│      └─ Keys + Values + Children (序列化)                   │
│                                                              │
│  特点：                                                      │
│  ⭐ 高性能：100-200 ns/op (仍快 Lealone 5-10x)               │
│  ✅ 可持久化：支持 WAL + 崩溃恢复                             │
│  ✅ 内存友好：固定 4KB，无碎片                                │
│  ✅ 三层缓存：L1(Page) + L2(Buffer) + L3(Disk)               │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

### 6.2 性能对比

| 指标 | 纯内存 | Page 层 | Lealone | 说明 |
|------|-------|---------|---------|------|
| **读延迟** | 10.97 ns | 100-200 ns | 941 ns | Page 层快 4.7-9.4x ✅ |
| **写延迟** | 41.7K ns | 800-1500 ns | 1596 ns | Page 层快 1.1-2.0x ✅ |
| **持久化** | ❌ | ✅ | ✅ | Page 层支持持久化 |
| **内存效率** | 差 | 好 | 好 | 固定 4KB ✅ |
| **生产可用** | ❌ | ✅ | ✅ | Page 层可落地 |

---

## 七、关键实施要点

### 7.1 数据结构设计

```go
// Page 结构（固定 4KB）
type Page struct {
    ID       model.PageID
    Type     PageType
    Version  uint64       // CCOW 版本号
    RefCount atomic.Int32
    Dirty    bool

    // 固定 4KB 数据区
    Data     [PageSize]byte
}

const PageSize = 4096

// Page 类型
type PageType uint8

const (
    LeafPage PageType = iota
    NodePage
)
```

### 7.2 序列化接口

```go
// serializer.go
type Serializer interface {
    // 序列化：Node → Page
    SerializeNode(node *Node, page *Page) error

    // 反序列化：Page → Node
    DeserializeNode(page *Page) (*Node, error)
}

// 二进制序列化实现
type BinarySerializer struct{}

func (s *BinarySerializer) SerializeNode(node *Node, page *Page) error
func (s *BinarySerializer) DeserializeNode(page *Page) (*Node, error)
```

### 7.3 与三层缓存集成

```go
// page_manager.go
type PageManager struct {
    l1Cache *LRUCache[model.PageID, *Page]      // L1: Page 对象
    l2Cache *LRUCache[model.PageID, []byte]     // L2: ByteBuffer
    storage Storage                             // L3: 磁盘
    serializer Serializer
}

func (pm *PageManager) Get(pageID model.PageID) (*Page, error) {
    // 1️⃣ 尝试 L1: Page 对象缓存
    if page, ok := pm.l1Cache.Get(pageID); ok {
        page.Pin()
        return page, nil  // L1 命中: ~100 ns
    }

    // 2️⃣ 尝试 L2: ByteBuffer 缓存
    if data, ok := pm.l2Cache.Get(pageID); ok {
        // 从 L2 反序列化到 L1
        page := pm.acquirePage()
        copy(page.Data[:], data)

        node, err := pm.serializer.DeserializeNode(page)
        if err != nil {
            return nil, err
        }

        pm.l1Cache.Put(pageID, page)
        page.Pin()
        return page, nil  // L2 命中: ~500 ns
    }

    // 3️⃣ 从 L3: 磁盘读取
    data, err := pm.storage.LoadPage(pageID)
    if err != nil {
        return nil, err
    }

    // 放入 L2 缓存
    pm.l2Cache.Put(pageID, data)

    // 反序列化到 L1
    page := pm.acquirePage()
    copy(page.Data[:], data)

    node, err := pm.serializer.DeserializeNode(page)
    if err != nil {
        return nil, err
    }

    pm.l1Cache.Put(pageID, page)
    page.Pin()

    return page, nil  // L3 命中: ~10-100 μs
}
```

---

## 八、总结与建议

### 8.1 核心原则

1. **固定大小优于变长**：4KB 固定 Page 避免碎片
2. **二进制序列化**：使用 binary.Write/Read，避免 JSON
3. **三层缓存**：L1(Page) + L2(Buffer) + L3(Disk) 优化性能
4. **延迟序列化**：只在需要时序列化，减少 CPU 开销

### 8.2 实施建议

```
优先级 P0（必须）：
✅ 1. 实现 Page 结构（固定 4KB）
✅ 2. 实现 SerializeNode（基于设计文档）
✅ 3. 实现 DeserializeNode（基于设计文档）
✅ 4. 单元测试（验证序列化正确性）

优先级 P1（重要）：
⭐ 5. 性能优化（bytes.Buffer 预分配）
⭐ 6. 三层缓存集成（L1/L2/L3）
⭐ 7. 基准测试（验证性能目标）
```

### 8.3 参考文档

- `thoughts/2026-03-09-kv-to-page-serialization.md` - 详细序列化设计
- `thoughts/2026-06-08-lealone-btree-deep-dive.md` - Lealone 深入解析
- `docs/11_perf/btree-page-layer-implementation-plan.md` - 实施计划（已更新 Phase 2）

---

**研究完成**: 2026-03-09
**状态**: ✅ 完成 Phase 2 更新
**下一步**: 开始实施 Phase 1（基础设施）→ Phase 2（序列化层）
