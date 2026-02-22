# Bf-Tree WAL 机制分析

> **预研究报告**
> **创建日期**: 2026-02-09
> **最后更新**: 2026-02-22（DDD 架构适配更新）
> **状态**: 🔄 进行中
> **源码位置**: `/Users/zhangcz/ws/rust/src/github.com/microsoft/bf-tree/src/wal/`
> **参考文档**: `docs/07_spike/2026-02-18_spike-nexkv-ddd-interface.md`

---

## 📋 研究目标

分析 Bf-Tree 的 WAL（Write-Ahead Log）实现，评估与 NexKV 现有 WAL 的兼容性。

---

## 一、架构概览

### 1.1 文件结构

```
src/wal/
├── mod.rs           # WAL 主实现（17KB）
└── operations.rs    # 日志操作定义（3.8KB）
```

---

## 二、核心数据结构

### 2.1 WriteOp 写操作

**位置**：`src/wal/operations.rs`

```rust
pub(crate) enum WriteOp {
    Insert {
        key: Vec<u8>,
        value: Vec<u8>,
    },
    Delete {
        key: Vec<u8>,
    },
    InsertMiniPage {
        base_page_offset: usize,
        mini_page: Vec<u8>,
    },
    DeleteMiniPage {
        base_page_offset: usize,
    },
    UpgradeToFullPage {
        base_page_offset: usize,
        full_page: Vec<u8>,
    },
}
```

**操作类型分析**：

| 操作 | 说明 | 对应 NexKV |
|------|------|-----------|
| **Insert** | 插入键值对 | ✅ WALEntry.Put |
| **Delete** | 删除键 | ✅ WALEntry.Delete |
| **InsertMiniPage** | 插入 Mini-Page | ❌ 新增（增量更新） |
| **DeleteMiniPage** | 删除 Mini-Page | ❌ 新增（增量回收） |
| **UpgradeToFullPage** | 升级到完整页面 | ❌ 新增（页面升级） |

---

### 2.2 LogEntry 日志条目

**位置**：`src/wal/operations.rs`

```rust
pub(crate) struct LogEntry<'a> {
    pub lsn: u64,              // Log Sequence Number
    pub op: WriteOp,           // 写操作
    pub marker: PhantomData<&'a ()>,
}
```

**特点**：
- ✅ 使用 LSN（Log Sequence Number）标识日志顺序
- ✅ 使用泛型生命周期（`'a`）管理内存
- ✅ 支持 5 种操作类型

---

### 2.3 WriteAheadLogInner 内部结构

**位置**：`src/wal/mod.rs:61-69`

```rust
struct WriteAheadLogInner {
    buffer: RawBuffer,              // 原始缓冲区
    file_handle: Arc<dyn VfsImpl>,  // 文件句柄（虚拟文件系统）
    buffer_cursor: usize,           // 缓冲区游标
    file_offset: usize,             // 文件偏移
    next_lsn: u64,                  // 下一个 LSN
    flushed_lsn: u64,               // 已刷新的 LSN
    need_flush: bool,              // 是否需要刷新
}
```

**关键参数**：
```rust
const BLOCK_SIZE: usize = 512;  // 块大小（磁盘对齐）
```

---

## 三、WAL 工作流程

### 3.1 写入流程

**简化流程**：

```
1. 分配 LogEntry
   ↓
2. 序列化到缓冲区（RawBuffer）
   ↓
3. 更新 buffer_cursor
   ↓
4. 检查是否需要刷盘
   ├─ 缓冲区满 → 刷盘
   └─ 定时刷盘 → 后台任务
   ↓
5. 写入文件（file_handle.write）
   ↓
6. 更新 flushed_lsn
```

**关键代码**（`src/wal/mod.rs:72-89`）：

```rust
fn flush(&mut self) {
    if self.buffer_cursor == 0 {
        return;  // 没有数据需要刷盘
    }

    // 清空下一个头部（8 字节零填充）
    self.clear_next_header();

    // 写入文件
    self.file_handle.write(self.file_offset, self.buffer.as_slice());

    // 更新文件偏移
    if !self.should_inplace_flush() {
        self.file_offset += self.buffer.buffer_size;
        self.buffer_cursor = 0;
    }

    // 更新已刷新 LSN
    self.flushed_lsn = self.next_lsn - 1;
    self.need_flush = false;
}
```

---

### 3.2 缓冲区管理

**RawBuffer 原始缓冲区**：

```rust
struct RawBuffer {
    buffer_size: usize,
    ptr: *mut u8,          // 原始指针（手动管理）
}
```

**特点**：
- ✅ **内存对齐**：对齐到 512 字节（BLOCK_SIZE）
- ✅ **手动管理**：使用 `alloc`/`dealloc`
- ✅ **线程安全**：`unsafe impl Send/Sync`

**与 Go 的差异**：

| Rust | Go |
|------|-----|
| 手动内存管理 | GC 自动管理 |
| 内存对齐（512B） | 默认 8 字节对齐 |
| 原始指针（`*mut u8`） | 切片（`[]byte`） |

---

## 四、与 NexKV WAL 对比

### 4.1 现有 WAL 实现

**位置**：`internal/wal/wal.go`

**核心结构**：

```go
type WALEntry struct {
    Timestamp *clock.HLC  // 操作时间戳
    Type      WALType     // 操作类型
    Key       string      // 键
    Value     []byte      // 值
    Checksum  uint32      // 校验和
}

type WALType uint16

const (
    WALTypePut WALType = iota
    WALTypeDelete
    WALTypeCheckpoint
)
```

**特点**：
- ✅ 使用 HLC（混合逻辑时钟）
- ✅ 支持 Put/Delete/Checkpoint
- ✅ CRC32 校验和
- ❌ 不支持 Mini-Page 操作

---

### 4.2 差异分析

| 维度 | Bf-Tree WAL | NexKV WAL | 兼容性 |
|------|------------|-----------|--------|
| **操作类型** | 5 种（含 Mini-Page） | 3 种 | ⚠️ 需扩展 |
| **时间戳** | LSN（u64） | HLC（物理+逻辑） | ⚠️ 需映射 |
| **校验和** | 无 | CRC32 | ✅ 可添加 |
| **缓冲区** | 原始缓冲区 | 切片缓冲区 | ✅ 兼容 |
| **文件对齐** | 512B | 4KB | ⚠️ 需调整 |

---

## 五、移植建议

### 5.1 兼容性方案

**选项 A：扩展现有 WAL**

```go
// 扩展 WALType
const (
    WALTypePut WALType = iota
    WALTypeDelete
    WALTypeCheckpoint
    WALTypeInsertMiniPage      // 新增
    WALTypeDeleteMiniPage      // 新增
    WALTypeUpgradeToFullPage   // 新增
)

// 扩展 WALEntry
type WALEntry struct {
    Timestamp *clock.HLC
    Type      WALType
    Key       string
    Value     []byte
    Checksum  uint32

    // 新增字段（用于 Mini-Page 操作）
    BasePageOffset uint64      // Base-Page 偏移
    MiniPageData   []byte      // Mini-Page 数据
}
```

**优点**：
- ✅ 复用现有 WAL 实现
- ✅ 保持一致性
- ✅ 无需重写

**缺点**：
- ⚠️ 需要扩展接口
- ⚠️ 可能影响现有功能

---

**选项 B：独立 Bf-Tree WAL**

```go
// 独立的 WAL 实现
type BfTreeWAL struct {
    buffer       []byte
    bufferCursor int
    fileOffset   int64
    nextLSN      uint64
    flushedLSN   uint64
    file         *os.File
}

func (w *BfTreeWAL) Append(op WriteOp) error
func (w *BfTreeWAL) Flush() error
func (w *BfTreeWAL) Recover() ([]LogEntry, error)
```

**优点**：
- ✅ 独立实现，不影响现有 WAL
- ✅ 可以针对 Bf-Tree 优化

**缺点**：
- ❌ 代码重复
- ❌ 维护成本高

---

### 5.2 推荐方案

**阶段 1（MVP）**：使用选项 A（扩展现有 WAL）

```go
// 扩展现有 WAL，支持 Mini-Page 操作
type WALType uint16

const (
    WALTypePut WALType = iota
    WALTypeDelete
    WALTypeCheckpoint
    WALTypeInsertMiniPage      // 新增：支持 Mini-Page 插入
    WALTypeDeleteMiniPage      // 新增：支持 Mini-Page 删除
)
```

**阶段 2（优化）**：独立 Bf-Tree WAL

- 针对性能优化
- 支持批量写入
- 优化缓冲区管理

---

## 六、LSN vs HLC

### 6.1 LSN（Log Sequence Number）

**定义**：单调递增的日志序列号

```rust
pub struct LogEntry<'a> {
    pub lsn: u64,  // Log Sequence Number
    // ...
}
```

**特点**：
- ✅ 单调递增
- ✅ 简单高效
- ❌ 不支持分布式场景

---

### 6.2 HLC（Hybrid Logical Clock）

**定义**：物理时间 + 逻辑时间

```go
type HLC struct {
    Physical int64  // 物理时间（毫秒）
    Logical  uint16 // 逻辑时间（计数器）
}
```

**特点**：
- ✅ 支持分布式场景
- ✅ 时钟漂移补偿
- ❌ 实现复杂

---

### 6.3 映射方案

**方案 1：LSN 作为主要标识**

```go
type BfTreeWALEntry struct {
    LSN       uint64      // 日志序列号
    Timestamp *clock.HLC  // 混合逻辑时钟
    Type      WALType
    // ...
}
```

**方案 2：HLC 作为主要标识**

```go
type BfTreeWALEntry struct {
    Timestamp *clock.HLC  // 主要标识
    LSN       uint64      // 可选的本地序列号
    Type      WALType
    // ...
}
```

**推荐**：方案 1（LSN 主要，HLC 辅助）

---

## 七、性能优化

### 7.1 缓冲区优化

**Bf-Tree WAL 优化**：
- ✅ **内存对齐**：512 字节对齐，减少磁盘 I/O
- ✅ **批量写入**：积累多个条目后统一刷盘
- ✅ **原地刷新**：某些情况下原地刷新（避免文件增长）

**NexKV WAL 可借鉴**：
- ✅ 增加内存对齐
- ✅ 优化批量写入策略
- ✅ 实现原地刷新

---

### 7.2 刷盘策略

**Bf-Tree WAL 刷盘策略**：

```rust
// 检查是否需要原地刷新
fn should_inplace_flush(&self) -> bool {
    // 如果文件偏移 = 0（初始状态），原地刷新
    self.file_offset == 0
}
```

**优化点**：
- ✅ 避免小文件频繁增长
- ✅ 减少磁盘碎片

---

## 八、结论

### 8.1 核心发现

1. **Bf-Tree WAL 更复杂**：支持 5 种操作类型（含 Mini-Page）
2. **缓冲区管理精细**：512 字节对齐，手动管理
3. **LSN vs HLC 差异**：需要设计映射方案

### 8.2 移植建议

| 任务 | 优先级 | 复杂度 |
|------|--------|--------|
| **扩展 WALType** | ⭐⭐⭐ 高 | ⭐⭐ 低 |
| **映射 LSN/HLC** | ⭐⭐⭐ 高 | ⭐⭐⭐ 中 |
| **优化缓冲区对齐** | ⭐⭐ 中 | ⭐ 低 |
| **实现原地刷新** | ⭐ 低 | ⭐⭐ 低 |

### 8.3 下一步行动

- [ ] 设计 WAL 扩展方案
- [ ] 评估 LSN/HLC 映射策略
- [ ] 测试缓冲区对齐性能
- [ ] 实现 Mini-Page WAL 操作

---

**报告版本**: v1.0
**创建日期**: 2026-02-09
**维护者**: NexKV 开发团队
**状态**: 🔄 进行中
