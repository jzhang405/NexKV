# NexKV 存储引擎文件格式设计白皮书

**版本**: v2.0
**创建日期**: 2026-03-07
**状态**: 正式发布

---

## 目录

- [一、设计背景与核心目标](#一设计背景与核心目标)
- [二、核心设计规范](#二核心设计规范)
- [三、Manifest 文件（版本清单）](#三manifest-文件版本清单)
- [四、Index 文件（全局索引）](#四index-文件全局索引)
- [五、Chunk 文件（数据块）](#五chunk-文件数据块)
- [六、DDD 接口设计](#六ddd-接口设计)
  - [6.1 分层架构](#61-分层架构)
  - [6.2 核心设计原则](#62-核心设计原则)
  - [6.3 领域对象接口](#63-领域对象接口)
  - [6.4 算法接口](#64-算法接口)
  - [6.5 存储仓储接口](#65-存储仓储接口)
  - [6.6 领域服务接口](#66-领域服务接口)
  - [6.7 应用层接口（KVStore）](#67-应用层接口kvstore)
  - [6.8 Iterator 接口](#68-iterator-接口)
  - [6.9 WAL 接口](#69-wal-接口)
  - [6.10 LocalTx 接口](#610-localtx-接口)
  - [6.11 AsyncOp 泛型接口（V4）](#611-asyncop-泛型接口v4)
  - [6.12 Context 使用规范](#612-context-使用规范)
  - [6.13 BlockDevice 接口](#613-blockdevice-接口)
  - [6.14 LocalStorage 本地存储接口](#614-localstorage-本地存储接口)
  - [6.15 CloudStorage 云存储接口](#615-cloudstorage-云存储接口)
  - [6.16 5层架构总览](#616-5层架构总览)
  - [6.17 接口依赖关系](#617-接口依赖关系)
- [七、核心能力落地](#七核心能力落地)
- [八、关键设计亮点](#八关键设计亮点)
- [九、S3 流式写入设计](#九s3-流式写入设计)
- [十、总结](#十总结)

---

## 一、设计背景与核心目标

### 1.1 设计初衷

- 解决传统 KV 引擎「本地/云存储双架构」问题，实现一套代码适配本地磁盘与 S3 对象存储
- 满足企业级云上安全需求（加密存储），同时兼顾高性能
- 预留多模态/AI/向量扩展能力，避免后续架构重构
- 对齐 Iceberg 核心特性（ACID、时间旅行、不可变数据），针对 KV 场景轻量化优化

### 1.2 核心目标

| 维度 | 目标要求 |
|------|----------|
| 存储兼容 | 本地磁盘 + S3 双模式，目录/格式完全统一 |
| 事务能力 | 支持 ACID，读写无锁、崩溃安全 |
| 版本管理 | 时间旅行、版本回滚，不重复存储数据 |
| 性能 | 整块索引加载、Chunk 随机读、压缩减少存储成本 |
| 安全 | 可插拔加密（AES-GCM/ChaCha20），数据加密后上云 |
| 扩展性 | 原生预留多模态/AI/向量字段 |
| 云原生 | S3 友好（无随机写、流式上传、原子提交） |

### 1.3 与 Apache Iceberg 对比

| 特性 | Apache Iceberg | NexKV 设计 | 差异说明 |
|------|---------------|------------|----------|
| 核心架构 | Snapshot + Manifest + DataFile | Manifest + Index + Chunk | 轻量化适配 KV |
| 数据模型 | 面向结构化数据 | 面向 KV 数据 | 简化分区设计 |
| 不可变数据 | DataFile 不可变 | Chunk 不可变 | 完全对齐 |
| ACID 实现 | Manifest 原子替换 | Manifest 原子替换 | 完全对齐 |
| 时间旅行 | 基于 Snapshot 回溯 | 基于 Manifest 回溯 | 轻量化实现 |
| 数据跳过 | 基于 DataFile 元数据裁剪 | 基于 Chunk 元数据裁剪 | 完全对齐 |
| 云原生适配 | 依赖对象存储 | 原生支持 S3 分段流式上传 | 针对 KV 优化 |

---

## 二、核心设计规范

### 2.1 整体架构（DDD 分层）

- **领域对象**：Chunk（数据块）、Index（全局索引）、Manifest（版本清单）
- **核心能力**：
  - 存储层：统一适配本地/S3，提供流式读写、加密/压缩/序列化能力
  - 索引层：Index 整块加载进内存构建 BfTree，快速定位 Key 所在 Chunk
  - 版本层：Manifest 作为版本入口，原子替换保证事务性

### 2.2 三大核心文件设计

所有文件遵循「固定头 + 变长 Payload + 固定尾」结构，字节序为 Little-Endian，Payload 支持「序列化→压缩→加密」可插拔处理。

### 2.3 可插拔能力设计

| 控制位 | 枚举值（默认加粗） | 说明 |
|--------|-------------------|------|
| SerType（序列化） | 0=无 / **1=MessagePack** / 2=Protobuf / 3=FlatBuffer | 轻量、跨语言、快 |
| CompType（压缩） | 0=无 / **1=LZ4** / 2=Snappy / 3=Zstd | 解压快、CPU 低 |
| EncType（加密） | 0=无 / **1=AES-GCM** / 2=ChaCha20-Poly1305 | 云上合规、通用性强 |

### 2.4 目录结构规范

```
nexkv/
├── manifest/    # 版本清单（ACID/时间旅行）
├── index/       # 全局索引
├── chunk/       # 数据块
└── tmp/         # 仅本地使用（原子提交），S3 禁用
```

### 2.5 核心流程设计

#### 写入流程

```
生成 KV 数据 → 序列化 → 压缩 → 加密
                    ↓
              本地: tmp目录 → rename
              S3: 流式上传
                    ↓
              生成 Index 文件
                    ↓
              生成 Manifest（版本生效）
```

#### 读取流程

```
加载 Manifest → 读取 Index → 构建 BfTree
                    ↓
              查询 Key → 定位 Chunk+Offset
                    ↓
              本地随机读 / S3 Range GET
                    ↓
              解密 → 解压缩 → 反序列化 → 返回 Value
```

---

## 三、Manifest 文件（版本清单）

### 3.1 核心定位

Manifest 是 NexKV 的「版本中枢」，承担 ACID 事务、时间旅行、版本回滚的核心职责。

- **核心作用**：记录某一版本的 Index 文件名、Chunk 列表、全局元数据
- **原子性保障**：Manifest 未完成则版本不生效
- **版本管理**：保留历史 Manifest 文件，实现时间旅行

### 3.2 文件规范

- **路径**：`nexkv/manifest/manifest_v{version}_{ts}.bin`
- **命名规则**：`manifest_v{version}_{timestamp}.bin`
- **示例**：`manifest_v123_1710000000000.bin`

### 3.3 二进制格式

#### 固定 Header（64 字节）

| 偏移量 | 字段名 | 类型 | 长度 | 说明 |
|--------|--------|------|------|------|
| 0 | Magic | uint32 | 4 | 固定值 `0x4E45584D`（NEXM） |
| 4 | Version | uint64 | 8 | 版本号，单调递增 |
| 12 | Timestamp | uint64 | 8 | 创建时间戳（ms） |
| 20 | SerType | uint8 | 1 | 序列化类型 |
| 21 | CompType | uint8 | 1 | 压缩类型 |
| 22 | EncType | uint8 | 1 | 加密类型 |
| 23 | Reserved1 | uint8 | 1 | 对齐字段 |
| 24 | IndexNameLen | uint32 | 4 | 索引文件名长度 |
| 28 | ChunkCount | uint32 | 4 | Chunk 数量 |
| 32 | MinKeyLen | uint32 | 4 | 全局最小 Key 长度 |
| 36 | MaxKeyLen | uint32 | 4 | 全局最大 Key 长度 |
| 40 | ExtLen | uint32 | 4 | 扩展字段长度 |
| 44 | Reserved2 | uint32 | 4 | 预留字段 |
| 48 | HeaderChecksum | uint32 | 4 | Header CRC32 校验和 |
| 52-63 | Reserved3 | uint8[12] | 12 | 预留扩展空间 |

#### 变长 Payload

| 字段名 | 类型 | 说明 |
|--------|------|------|
| IndexName | bytes | 对应 Index 文件名 |
| ChunkList | []ChunkEntry | Chunk 列表 |
| MinKey | bytes | 全局最小 Key |
| MaxKey | bytes | 全局最大 Key |
| ExtData | bytes | 扩展字段 |

#### ChunkEntry 子结构

| 字段名 | 类型 | 说明 |
|--------|------|------|
| ChunkNameLen | uint32 | Chunk 文件名长度 |
| ChunkName | bytes | Chunk 完整名称 |
| ChunkMinKeyLen | uint32 | 该 Chunk 最小 Key 长度 |
| ChunkMinKey | bytes | 该 Chunk 最小 Key |
| ChunkMaxKeyLen | uint32 | 该 Chunk 最大 Key 长度 |
| ChunkMaxKey | bytes | 该 Chunk 最大 Key |

#### 固定 Footer（16 字节）

| 偏移量 | 字段名 | 类型 | 长度 | 说明 |
|--------|--------|------|------|------|
| 0 | Magic | uint32 | 4 | 固定值 `0x4E45584D` |
| 4 | Version | uint64 | 8 | 与 Header 版本一致 |
| 12 | PayloadChecksum | uint32 | 4 | Payload CRC32 校验和 |

---

## 四、Index 文件（全局索引）

### 4.1 核心定位

Index 是 NexKV 的「索引核心」，负责 Key→Chunk+Offset 映射，整块加载构建 BfTree。

- **核心作用**：Key→Chunk+Offset 映射，整块加载进内存
- **性能设计**：避免随机读，启动速度快
- **扩展能力**：预留多模态/AI/向量索引空间

### 4.2 文件规范

- **路径**：`nexkv/index/index_v{version}_{ts}.bin`
- **命名规则**：`index_v{version}_{timestamp}.bin`

### 4.3 二进制格式

#### 固定 Header（64 字节）

| 偏移量 | 字段名 | 类型 | 长度 | 说明 |
|--------|--------|------|------|------|
| 0 | Magic | uint32 | 4 | 固定值 `0x4E455849`（NEXI） |
| 4 | Version | uint64 | 8 | 版本号 |
| 12 | EntryCount | uint32 | 4 | 索引条目数 |
| 16 | IndexDataLen | uint32 | 4 | 索引数据长度 |
| 20 | SerType | uint8 | 1 | 序列化类型 |
| 21 | CompType | uint8 | 1 | 压缩类型 |
| 22 | EncType | uint8 | 1 | 加密类型 |
| 23 | Reserved1 | uint8 | 1 | 对齐字段 |
| 24 | MinKeyLen | uint32 | 4 | 全局最小 Key 长度 |
| 28 | MaxKeyLen | uint32 | 4 | 全局最大 Key 长度 |
| 32 | ExtLen | uint32 | 4 | 扩展字段长度 |
| 36 | Reserved2 | uint32 | 4 | 预留字段 |
| 40 | HeaderChecksum | uint32 | 4 | Header CRC32 |
| 44-63 | Reserved3 | uint8[20] | 20 | 预留扩展空间 |

#### 变长 Payload

| 字段名 | 类型 | 说明 |
|--------|------|------|
| IndexEntries | []IndexEntry | 索引条目列表 |
| MinKey | bytes | 全局最小 Key |
| MaxKey | bytes | 全局最大 Key |
| Extension | bytes | 扩展字段 |

#### IndexEntry 子结构

| 字段名 | 类型 | 说明 |
|--------|------|------|
| Key | bytes | Key 二进制值 |
| ChunkID | bytes | Chunk ID |
| Offset | uint64 | 在 Chunk 中的偏移量 |
| Length | uint32 | Value 长度 |

#### 固定 Footer（16 字节）

| 偏移量 | 字段名 | 类型 | 长度 | 说明 |
|--------|--------|------|------|------|
| 0 | Magic | uint32 | 4 | 固定值 `0x4E455849` |
| 4 | Reserved | uint8[8] | 8 | 预留字段 |
| 12 | PayloadChecksum | uint32 | 4 | Payload CRC32 |

### 4.4 关键设计细节

- **整块加载优势**：本地一次顺序读，S3 一次 GET 请求，避免随机 IO
- **范围查询优化**：通过 Chunk 的 Min/Max Key 做数据裁剪
- **向量索引扩展**：Extension 预留 VectorDim、IndexType 等字段

---

## 五、Chunk 文件（数据块）

### 5.1 核心定位

Chunk 是 NexKV 的「数据载体」，存储真实 KV 数据。

- **核心作用**：存储真实 KV 数据，不可变设计
- **性能设计**：支持本地随机读、S3 Range GET
- **扩展能力**：支持大 Value、多模态/二进制数据

### 5.2 文件规范

- **路径**：`nexkv/chunk/chunk_{uuid}.bin`
- **命名规则**：`chunk_{uuid}.bin`
- **示例**：`chunk_123e4567-e89b-12d3-a456-426614174000.bin`

### 5.3 二进制格式

#### 固定 Header（64 字节）

| 偏移量 | 字段名 | 类型 | 长度 | 说明 |
|--------|--------|------|------|------|
| 0 | Magic | uint32 | 4 | 固定值 `0x4E455843`（NEXC） |
| 4 | ChunkID | uint8[16] | 16 | UUID 二进制值 |
| 20 | EntryCount | uint32 | 4 | KV 条目数量 |
| 24 | SerType | uint8 | 1 | 序列化类型 |
| 25 | CompType | uint8 | 1 | 压缩类型 |
| 26 | EncType | uint8 | 1 | 加密类型 |
| 27 | Reserved1 | uint8 | 1 | 对齐字段 |
| 28 | DataLen | uint32 | 4 | KV 列表长度 |
| 32 | MinKeyLen | uint32 | 4 | 最小 Key 长度 |
| 36 | MaxKeyLen | uint32 | 4 | 最大 Key 长度 |
| 40 | CreateTs | uint64 | 8 | 创建时间戳 |
| 48 | Reserved2 | uint32 | 4 | 预留字段 |
| 52 | HeaderChecksum | uint32 | 4 | Header CRC32 |
| 56-63 | Reserved3 | uint8[8] | 8 | 预留扩展空间 |

#### 变长 Payload

| 字段名 | 类型 | 说明 |
|--------|------|------|
| KVList | []ChunkKV | KV 条目列表 |
| MinKey | bytes | 最小 Key |
| MaxKey | bytes | 最大 Key |
| ExtData | bytes | 扩展字段 |

#### ChunkKV 子结构

| 字段名 | 类型 | 说明 |
|--------|------|------|
| KeyLen | uint32 | Key 长度 |
| Key | bytes | Key 二进制值 |
| ValLen | uint32 | Value 长度 |
| Value | bytes | Value 二进制值 |

#### 固定 Footer（16 字节）

| 偏移量 | 字段名 | 类型 | 长度 | 说明 |
|--------|--------|------|------|------|
| 0 | Magic | uint32 | 4 | 固定值 `0x4E455843` |
| 4 | Reserved | uint8[8] | 8 | 预留字段 |
| 12 | PayloadChecksum | uint32 | 4 | Payload CRC32 |

### 5.4 关键设计细节

- **不可变设计**：避免并发写冲突，历史版本可通过 Manifest 回溯
- **小 Chunk 合并**：后台 Compaction 合并小 Chunk
- **大 Value 支持**：无长度限制，S3 分段上传支持大文件
- **扩展字段**：存储多模态/AI 元数据

---

## 六、DDD 接口设计

### 6.1 分层架构

```
┌─────────────────────────────────────────────┐
│           接口层 Interface                   │
│    HTTP/gRPC API、CLI 工具                   │
└─────────────────────────────────────────────┘
                      ↓
┌─────────────────────────────────────────────┐
│           应用层 Application                 │
│    KVStore、配置管理、流程编排              │
└─────────────────────────────────────────────┘
                      ↓
┌─────────────────────────────────────────────┐
│            领域层 Domain                     │
│  领域对象、领域服务、领域接口（依赖倒置）     │
└─────────────────────────────────────────────┘
                      ↓
┌─────────────────────────────────────────────┐
│         基础设施层 Infrastructure           │
│  本地存储、S3 存储、算法实现                 │
└─────────────────────────────────────────────┘
```

### 6.2 核心设计原则

| 原则 | 具体要求 |
|------|----------|
| 依赖倒置 | 领域层定义接口，基础设施层实现，领域层不依赖外部 |
| 接口隔离 | 每个接口仅包含单一职责 |
| 开闭原则 | 新增存储类型/算法仅需实现接口 |
| 单一职责 | 领域层管业务规则，基础设施层管技术实现 |
| 无状态设计 | 所有接口无状态，通过 Context 传递配置 |

### 6.3 领域对象接口

#### Manifest 领域对象

```go
type Manifest struct {
    Version     uint64
    Timestamp   time.Time
    IndexName   string
    ChunkList   []ChunkEntry
    MinKey      []byte
    MaxKey      []byte
    ExtData     []byte
    SerType     SerializerType
    CompType    CompressorType
    EncType     EncryptorType
}

func (m *Manifest) Validate() error { ... }
func (m *Manifest) Filename() string { ... }
```

#### Index 领域对象

```go
type Index struct {
    FormatVersion uint32
    EntryCount    uint32
    Entries       []IndexEntry
    MinKey        []byte
    MaxKey        []byte
    Extension     IndexExtension
    SerType       SerializerType
    CompType      CompressorType
    EncType       EncryptorType
}
```

#### Chunk 领域对象

```go
type Chunk struct {
    ChunkID    string
    EntryCount uint32
    KVList     []ChunkKV
    MinKey     []byte
    MaxKey     []byte
    ExtData    []byte
    CreateTs   time.Time
    SerType    SerializerType
    CompType   CompressorType
    EncType    EncryptorType
}
```

### 6.4 算法接口

```go
type Serializer interface {
    Serialize(ctx context.Context, obj any) ([]byte, error)
    Deserialize(ctx context.Context, data []byte, obj any) error
    Type() SerializerType
}

type Compressor interface {
    Compress(ctx context.Context, data []byte) ([]byte, error)
    Decompress(ctx context.Context, data []byte) ([]byte, error)
    Type() CompressorType
}

type Encryptor interface {
    Encrypt(ctx context.Context, plaintext []byte) ([]byte, error)
    Decrypt(ctx context.Context, ciphertext []byte) ([]byte, error)
    Type() EncryptorType
}
```

### 6.5 存储仓储接口

```go
type StorageRepository interface {
    // Manifest 操作
    SaveManifest(ctx context.Context, manifest *Manifest) error
    LoadManifest(ctx context.Context, version uint64) (*Manifest, error)
    ListManifests(ctx context.Context) ([]uint64, error)
    
    // Index 操作
    SaveIndex(ctx context.Context, index *Index, version uint64, ts time.Time) error
    LoadIndex(ctx context.Context, indexName string) (*Index, error)
    
    // Chunk 操作
    SaveChunk(ctx context.Context, chunk *Chunk, dataStream io.Reader) error
    LoadChunk(ctx context.Context, chunkID string) (*Chunk, error)
    ReadChunkRange(ctx context.Context, chunkID string, offset uint64, length uint32) ([]byte, error)
    
    // 通用操作
    DeleteFile(ctx context.Context, path string) error
    Exists(ctx context.Context, path string) (bool, error)
}
```

### 6.6 领域服务接口

```go
type VersionService interface {
    CreateVersion(ctx context.Context, chunks []*Chunk, index *Index) (uint64, error)
    RollbackVersion(ctx context.Context, targetVersion uint64) (uint64, error)
    GetLatestVersion(ctx context.Context) (uint64, error)
}

type IndexService interface {
    BuildIndex(ctx context.Context, chunks []*Chunk) (*Index, error)
    LookupKey(ctx context.Context, index *Index, key []byte) (*IndexEntry, error)
    RangeLookup(ctx context.Context, index *Index, startKey, endKey []byte) ([]*IndexEntry, error)
}

type ChunkService interface {
    SplitChunks(ctx context.Context, kvs []*ChunkKV, chunkSize uint64) ([]*Chunk, error)
    CompactChunks(ctx context.Context, chunkIDs []string) (*Chunk, error)
}
```

### 6.7 应用层接口

```go
type KVStore interface {
    // 基础 KV 操作
    Put(ctx context.Context, key []byte, value []byte) error
    Get(ctx context.Context, key []byte, version ...uint64) ([]byte, error)
    Delete(ctx context.Context, key []byte) error
    Range(ctx context.Context, startKey, endKey []byte, version ...uint64) (map[string][]byte, error)
    
    // 版本管理
    VersionRollback(ctx context.Context, targetVersion uint64) (uint64, error)
    ListVersions(ctx context.Context) ([]uint64, error)
    GetVersionInfo(ctx context.Context, version uint64) (*Manifest, error)
    
    // 后台任务
    Compact(ctx context.Context) error
    GC(ctx context.Context) error
    
    // 基础操作
    Close(ctx context.Context) error
```

### 6.8 Iterator 接口

```go
type Iterator interface {
    // Next 移动到下一个键值对
    Next() (valid bool, key []byte, value []byte, err error)
    
    // Close 关闭迭代器
    Close() error
}
```

### 6.9 WAL 接口

```go
type WAL interface {
    // Append 追加日志条目
    Append(entry *WALEntry) (LSN, error)
    
    // Read 读取指定 LSN 的日志
    Read(lsn LSN) (*WALEntry, error)
    
    // Recover 恢复数据
    Recover() ([]*WALEntry, error)
    
    // Sync 同步到磁盘
    Sync() error
    
    // Close 关闭 WAL
    Close() error
}

type WALEntry struct {
    LSN      LSN
    Type     WALType      // Insert/Update/Delete
    Key      []byte
    Value    []byte
   checksum uint64
}

type WALType uint8

const (
    WALTypeInsert WALType = iota + 1
    WALTypeUpdate
    WALTypeDelete
)
```

### 6.10 LocalTx 接口

```go
type LocalTx interface {
    // 获取事务隔离级别
    IsolationLevel() IsolationLevel
    
    // 基础操作（自动加入事务）
    Get(ctx context.Context, key []byte) ([]byte, error)
    Set(ctx context.Context, key, value []byte) error
    Delete(ctx context.Context, key []byte) error
    
    // 提交事务
    Commit(ctx context.Context) error
    
    // 回滚事务
    Rollback(ctx context.Context) error
}

type IsolationLevel uint8

const (
    IsolationReadCommitted IsolationLevel = iota
    IsolationRepeatableRead
    IsolationSerializable
)
```

### 6.11 AsyncOp 泛型接口（V4）

```go
type AsyncOp[T any] interface {
    // Await 等待操作完成
    Await(ctx context.Context) (T, error)
    
    // OnComplete 完成回调
    OnComplete(callback func(T, error)) string
    OnError(callback func(error)) string
    OnSuccess(callback func(T)) string
    OffComplete(cbID string) error
    
    // WithTimeout 超时设置
    WithTimeout(timeout time.Duration) AsyncOp[T]
    
    // 状态检查
    IsDone() bool
    IsSuccess() bool
    IsFailed() bool
    IsCanceled() bool
}

// 向后兼容别名
type AsyncOperation[T any] = AsyncOp[T]

// 读写 Future 类型
type ReadFuture = AsyncOp[[]byte]
type WriteFuture = AsyncOp[error]
type IteratorFuture = AsyncOp[Iterator]
type BatchGetFuture = AsyncOp[[]KeyValue]
```

### 6.12 Context 使用规范

所有接口的 `context.Context` 参数用于：
- **取消操作**：监听 `ctx.Done()`，及时终止长时间运行的操作
- **超时控制**：通过 `context.WithTimeout()` 设置操作超时
- **链路追踪**：传递 trace ID 等上下文信息

**实现要求**：
- 所有阻塞操作必须监听 `ctx.Done()`
- 及时释放资源，避免 goroutine 泄漏
- 不要在结构体中存储 context，应在方法调用时传递

---

### 6.13 BlockDevice 接口

```go
type BlockDevice interface {
    // Read 读取数据块
    Read(ctx context.Context, offset int64, size int) ([]byte, error)
    
    // Write 写入数据块
    Write(ctx context.Context, offset int64, data []byte) error
    
    // Sync 同步到磁盘
    Sync() error
    
    // Close 关闭设备
    Close() error
    
    // Size 获取设备大小
    Size() (int64, error)
}
```

### 6.14 LocalStorage 本地存储接口

```go
type LocalStorage interface {
    BlockDevice
    
    // Open 打开文件
    Open(path string) error
    
    // Create 创建文件
    Create(path string, size int64) error
    
    // Delete 删除文件
    Delete(path string) error
    
    // Exists 检查文件是否存在
    Exists(path string) (bool, error)
    
    // List 列出文件
    List(dir string) ([]string, error)
}
```

### 6.15 CloudStorage 云存储接口

```go
type CloudStorage interface {
    BlockDevice
    
    // PutObject 上传对象
    PutObject(ctx context.Context, key string, data []byte) error
    
    // GetObject 下载对象
    GetObject(ctx context.Context, key string) ([]byte, error)
    
    // DeleteObject 删除对象
    DeleteObject(ctx context.Context, key string) error
    
    // ListObjects 列出对象
    ListObjects(ctx context.Context, prefix string) ([]string, error)
    
    // HeadObject 获取对象元数据
    HeadObject(ctx context.Context, key string) (ObjectMetadata, error)
}

type ObjectMetadata struct {
    Size        int64
    LastModified time.Time
    ContentType string
    ETag        string
}
```

---

### 6.16 存储引擎在 5 层架构中的位置

```
┌─────────────────────────────────────────────┐
│           ① API 层                          │
│    HTTP/gRPC API、CLI 工具                   │
└─────────────────────────────────────────────┘
                      ↓
┌─────────────────────────────────────────────┐
│           ② 控制平面层                      │
│    分片路由、选举、分布式锁、负载均衡        │
└─────────────────────────────────────────────┘
                      ↓
┌─────────────────────────────────────────────┐
│           ③ 数据平面层                      │
│    复制/一致性、事务、副本管理               │
└─────────────────────────────────────────────┘
                      ↓
┌─────────────────────────────────────────────┐
│           ④ 存储引擎层                      │
│    KVStore、LocalTx、WAL、Iterator          │
│    BTree、AsyncOp、BlockDevice       │
└─────────────────────────────────────────────┘
                      ↓
┌─────────────────────────────────────────────┐
│           ⑤ 基础设施层                      │
│    本地存储、云存储、网络通信                │
└─────────────────────────────────────────────┘
```

### 6.17 接口依赖关系

```
KVStore → Iterator
KVStore → LocalTx → WAL
KVStore → BTree → BlockDevice
BlockDevice → LocalStorage
BlockDevice → CloudStorage
```

---

## 七、核心能力落地

### 7.1 ACID 事务保证

| 特性 | 实现方式 |
|------|----------|
| 原子性 | Manifest 最后写入，未完成则版本不生效 |
| 一致性 | 一个 Manifest 对应一套完整的 Index+Chunk |
| 隔离性 | 读旧版本 Manifest，写新版本文件 |
| 持久性 | 所有文件不可变，永久保存 |

### 7.2 时间旅行与版本回滚

- 保留所有历史 Manifest 文件
- 加载指定版本 Manifest 即可读取对应版本数据
- 回滚仅需重新写入旧版本 Manifest 内容

### 7.3 云上安全保障

- 数据加密后上传 S3
- AEAD 加密算法自带完整性校验
- S3 写入全程无 tmp 目录

### 7.4 多模态/AI/向量扩展

- Index/Chunk 扩展字段预留
- 支持大 Value 存储
- 向量数据可存储在 Value 中

---

## 八、关键设计亮点

1. **架构轻量化**：对齐 Iceberg 核心思想，简化设计
2. **存储无感知**：本地/S3 目录/格式完全统一
3. **算法可插拔**：序列化/压缩/加密通过文件头标识配置
4. **S3 友好设计**：禁用 tmp 目录，分段流式上传
5. **扩展预留充足**：多模态/AI/向量字段原生嵌入

---

## 九、S3 流式写入设计

### 9.1 核心能力

- **无限流式写入**：边生成边传，写到多大都行（上限 5TB）
- **分段缓冲**：默认 5MB 分段，内存只占一个分段大小
- **可暂停/续传/失败重试**：单个分段失败不影响整体
- **零 tmp**：直接写到最终路径

### 9.2 写入模式

| 文件类型 | 写入模式 | 说明 |
|----------|----------|------|
| Manifest/Index | PutObject 流式 | 小文件，已知长度 |
| Chunk | 分段流式上传 | 大文件，未知长度 |

### 9.3 写入流程

1. 生成 Chunk 最终路径
2. 启动分段上传 → 拿到 UploadID
3. KV 序列化→压缩→加密 → 底层自动 5MB 分段上传
4. Chunk 写完 → 完成分段上传
5. 生成 Index → PutObject
6. **最后一步**：生成 Manifest → PutObject
   - 只有 Manifest 成功，整个版本才算提交

---

## 十、总结

### 10.1 核心亮点

1. **架构统一**：本地/S3 目录、格式完全一致
2. **性能最优**：Index 整块加载、Chunk 随机读/Range GET
3. **安全可控**：可插拔加密算法
4. **扩展灵活**：预留多模态/AI/向量空间
5. **云原生友好**：S3 流式分段上传、原子提交

### 10.2 落地建议

1. 优先实现默认配置（MessagePack + LZ4 + AES-GCM）
2. 本地/S3 接口抽象封装
3. 后台任务规划（Compaction、GC、版本清理）
4. 监控指标（Manifest/Index/Chunk 数量、大小、性能）

---

**文档结束**
