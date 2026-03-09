# KV → Page 序列化详解

## 一、整体架构

```mermaid
flowchart TB
    subgraph 内存操作
        A["Node 结构<br/>Keys: [][]byte<br/>Values: [][]byte<br/>Children: []*Node"] --> B[序列化]
    end
    
    subgraph Page结构
        C["Page.Data<br/>[4KB 固定大小]"] --> D[写入 Buffer]
    end
    
    subgraph 磁盘存储
        E["磁盘文件<br/>Chunk"] --> F[持久化]
    end
    
    B --> C
    D --> E
    
    style A fill:#e1f5fe
    style C fill:#fff3e0
    style E fill:#e8f5e9
```

## 二、序列化流程

```mermaid
sequenceDiagram
    participant Node as 内存 Node
    participant Buf as Page.Data Buffer
    participant Page as Page 结构
    participant Disk as 磁盘文件

    Note over Node,Disk: SerializeNode(node, page)
    
    Node->>Buf: 1. 写入 IsLeaf (1B) + NumKeys (4B)
    Note over Buf: [01] [00 00 00] [03 00 00 00]<br/>IsLeaf=1, NumKeys=3
    
    Node->>Buf: 2. 遍历 Keys，依次写入
    Note over Buf: [04 00] [6b 65 79 31] [03 00] ...<br/>KeyLen(2B) + KeyData + ValueLen(2B) + ValueData
    
    Node->>Buf: 3. 遍历 Values（叶子节点）
    
    Node->>Buf: 4. 遍历 Children PageIDs（内部节点）
    
    Buf->>Page: MarkDirty()
    Page->>Disk: 刷盘（可选）
```

## 三、Page 内存布局

```mermaid
graph LR
    subgraph Page 结构 (4KB = 4096 bytes)
        H["Page Header<br/>21 bytes"]
        D["Page Data<br/>4075 bytes"]
    end
    
    subgraph Header 详解
        H --> H1["Type: 1 byte"]
        H --> H2["Version: 8 bytes"]
        H --> H3["ID: 8 bytes"]
        H --> H4["RefCount: 4 bytes"]
    end
    
    subgraph Data 布局
        D --> D1["Metadata<br/>5 bytes"]
        D --> D2["Keys Section<br/>变长"]
        D --> D3["Values Section<br/>变长"]
        D --> D4["Children Section<br/>变长"]
    end
    
    style H fill:#ffcdd2
    style D fill:#c8e6c9
```

## 四、反序列化流程

```mermaid
flowchart TB
    A["从磁盘读取 Page"] --> B{检查缓存}
    B -->|L1 命中| C["返回 Page 对象"]
    B -->|L2 命中| D["反序列化 Buffer → Page"]
    B -->|L3 未命中| E["从磁盘读取"]
    D --> C
    E --> D
    
    C --> F["DeserializeNode(page)"]
    F --> G["解析 Header → IsLeaf, NumKeys"]
    G --> H["解析 Keys"]
    H --> I{"IsLeaf?"}
    I -->|Yes| J["解析 Values"]
    I -->|No| K["解析 Children PageIDs"]
    J --> L["返回 Node"]
    K --> L
    
    style C fill:#e1f5fe
    style L fill:#fff3e0
```

## 五、详细数据格式

```mermaid
classDiagram
    class PageHeader {
        +uint8 Type
        +uint64 Version
        +uint64 ID
        +int32 RefCount
    }
    
    class PageData {
        +[4075]byte Data
    }
    
    class Node {
        +[][]byte Keys
        +[][]byte Values
        +[]*Node Children
        +bool IsLeaf
    }
    
    PageHeader --> PageData : 组合
    Node ..> PageData : Serialize/Deserialize
```

## 六、三层缓存与序列化

```mermaid
flowchart LR
    subgraph L1["L1: Page 对象缓存 (热数据)"]
        L1A["Page 对象<br/>Keys: [][]byte<br/>Values: [][]byte"]
    end
    
    subgraph L2["L2: Buffer 缓存 (温数据)"]
        L2A["[]byte<br/>序列化后的原始数据"]
    end
    
    subgraph L3["L3: 磁盘 (冷数据)"]
        L3A["Chunk 文件"]
    end
    
    L1 -->|"读取"| L1
    L1 -->|"releasePage() 降级"| L2
    L2 -->|"deserialize() 升级"| L1
    L2 -->|"releaseBuff() 降级"| L3
    L3 -->|"readFromDisk() 升级"| L2
    
    style L1 fill:#e1f5fe
    style L2 fill:#fff3e0
    style L3 fill:#e8f5e9
```

## 七、关键代码对应

```mermaid
graph TB
    subgraph Go实现
        G1["func SerializeNode(node *Node, page *Page)"]
        G2["func DeserializeNode(page *Page) (*Node, error)"]
        G3["type Page struct { ID, Type, Version, Data }"]
    end
    
    subgraph Java实现
        J1["Page.write(PageInfo, Chunk, DataBuffer)"]
        J2["Page.read(ByteBuffer)"]
        J3["class Page { Object[] keys; Object[] values; }"]
    end
    
    G1 -.->|"对应"| J1
    G2 -.->|"对应"| J2
    G3 -.->|"对应"| J3
```

## 八、序列化示例

### 输入：Node

```go
node := &Node{
    IsLeaf: true,
    Keys:   [][]byte{[]byte("key1"), []byte("key2")},
    Values: [][]byte{[]byte("val1"), []byte("val2")},
}
```

### 输出：Page.Data

```
Offset  Bytes                          Content
─────────────────────────────────────────────────────────────
0       01                            IsLeaf = 1 (true)
1       00 00 00                     Padding
4       02 00 00 00                 NumKeys = 2

6       04 00                        Key[0] Length = 4
8       6b 65 79 31                  Key[0] = "key1"
12      05 00                        Value[0] Length = 5
14      76 61 6c 75 31               Value[0] = "val1"

19      03 00                        Key[1] Length = 3
21      6b 65 79                     Key[1] = "key2"
24      05 00                        Value[1] Length = 5
26      76 61 6c 75 31               Value[1] = "val1"
...     ...                          (后续可能有 padding)
```

## 九、关键要点

| 要点 | 说明 |
|------|------|
| **固定 Page 大小** | 4KB 固定，避免内存碎片 |
| **变长字段** | Key/Value 用 Length + Data 格式 |
| **序列化方向** | Node → Page → Disk |
| **反序列化** | Disk → Page → Node（按需） |
| **三层缓存** | L1: Page 对象, L2: Buffer, L3: Disk |
| **CCOW 兼容** | Version 字段支持并发版本 |

---

**生成日期**: 2026-03-09
**主题**: KV → Page 序列化详解
