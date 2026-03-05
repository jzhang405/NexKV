# V4 异步管道与 DDD Interface 接口对比分析

> **审查日期**: 2026-03-05
> **审查人**: jzh
> **审查类型**: 接口规范对比 + 完备性分析

---

## 1. 概述

### 1.1 分析目的

对比 V4 异步管道接口规范和 DDD Interface 接口规范的异同，针对各模块设计完备的接口分类，为后续接口统一提供参考。

### 1.2 对比文档

| 文档 | 路径 | 侧重点 | 版本 |
|------|------|--------|------|
| V4 异步管道 | `docs/07_spike/2026-03-04-spike-async-pipeline-v4.md` | 存储引擎层异步任务模型 | v4.0 |
| DDD Interface | `docs/07_spike/2026-02-18_spike_nexkv-ddd-interface.md` | 全系统 47 个接口定义 | v3.0 |

### 1.3 分析范围

| 模块 | V4 定义 | DDD 定义 | 对比重点 |
|------|---------|----------|----------|
| **Clock/HLC** | HLC 结构体 | 暂无明确定义 | 是否需要独立接口 |
| **BTree** | BTreeManager | BTree 接口 | 同步/异步统一 vs 纯同步 |
| **WAL** | WALManager | WAL 接口 | Append/Flush/Recover 方法 |
| **KVAPI** | Pipeline.Get/Set | KVStore 接口 | Task 抽象 vs 直接方法 |
| **Transaction** | 暂无 | LocalTx/TxManager | V4 缺失事务接口 |
| **Task/Executor** | TaskRunner + Task[Result] | GoroutineProvider + TaskExecutor | 双层接口 vs 单层 any |
| **AsyncOp** | AsyncOp[Result] 包装 Task | AsyncOperation[T] | 是否包装 Task vs 直接异步操作 |

---

## 2. 接口完备性分类标准

使用四级分类标准：

| 级别 | 含义 | 必须实现 | 备注 |
|------|------|---------|------|
| **MUST** | 核心必需 | 是 | 系统无法运行 |
| **SHOULD** | 强烈建议 | 是（推荐） | 影响可用性/性能 |
| **NICE-TO-HAVE** | 锦上添花 | 可选 | 提升开发体验 |
| **OPTIONAL** | 可选扩展 | 可选 | 特定场景使用 |

---

## 3. 核心接口对比

### 3.1 Task/Executor 模块

#### V4 定义

```go
// ═══════════════════════════════════════════════════════════════
// 第一层：TaskRunner（非泛型）—— Executor 只看到这个
// ═══════════════════════════════════════════════════════════════
type TaskRunner interface {
    Run(ctx context.Context, p *Pipeline)
    Priority() Priority
    SourceID() model.SourceID
}

// ═══════════════════════════════════════════════════════════════
// 第二层：Task[Result]（泛型）—— 用户使用，类型安全
// ═══════════════════════════════════════════════════════════════
type Task[Result any] interface {
    TaskRunner  // 嵌入第一层
    Execute(ctx context.Context, p *Pipeline) (Result, error)
}

// ═══════════════════════════════════════════════════════════════
// BaseTask 泛型基类
// ═══════════════════════════════════════════════════════════════
type BaseTask[Result any] struct {
    opType   OpType
    priority Priority
    sourceID model.SourceID
    done     chan struct{}  // 完成信号
    result   Result         // 直接存储结果
    err      error
}

func (b *BaseTask[Result]) Run(ctx context.Context, p *Pipeline)
func (b *BaseTask[Result]) Wait() (Result, error)
func (b *BaseTask[Result]) Done() <-chan struct{}

// ═══════════════════════════════════════════════════════════════
// Pipeline.Submit 方法
// ═══════════════════════════════════════════════════════════════
func (p *Pipeline) Submit(task TaskRunner) error {
    return p.executor.Submit(
        p.ctx,
        task.SourceID(),
        model.TaskPriority(task.Priority()),
        func(ctx context.Context) {
            task.Run(ctx, p)
        },
    )
}
```

#### DDD 定义

```go
// ═══════════════════════════════════════════════════════════════
// TaskExecutor - 已实现的接口
// ═══════════════════════════════════════════════════════════════
type TaskExecutor interface {
    Submit(ctx context.Context, sourceID model.SourceID, priority TaskPriority, task func(context.Context)) error
    Close() error
}

// ═══════════════════════════════════════════════════════════════
// GoroutineProvider - 全局 goroutine 提供者（使用 any 类型）
// ═══════════════════════════════════════════════════════════════
type GoroutineProvider interface {
    // 基础方法
    Submit(ctx context.Context, task func(context.Context)) error
    SubmitWithArg(ctx context.Context, task func(context.Context, any), arg any) error
    SubmitWithResult(ctx context.Context, task func(context.Context) (any, error)) Result[any]

    // 快捷方法
    SubmitWithPriority(ctx context.Context, priority Priority, task func(context.Context)) error
    SubmitDelayed(ctx context.Context, delay time.Duration, task func(context.Context)) error

    // 批量方法
    SubmitBatch(ctx context.Context, tasks []func(context.Context)) error

    // 监控和管理
    Stats() PoolStats
    Health() HealthStatus
    SetCapacity(capacity int) error
    Close() error
    CloseWithTimeout(timeout time.Duration) error
}

// ═══════════════════════════════════════════════════════════════
// Result[T] 泛型结果
// ═══════════════════════════════════════════════════════════════
type Result[T any] interface {
    Get(ctx context.Context) (T, error)
    GetWithTimeout(timeout time.Duration) (T, error)
    Done() <-chan struct{}
    IsDone() bool
}
```

#### 对比分析

| 维度 | V4 | DDD | 评估 |
|------|----|----|------|
| **类型安全** | ✅ 双层泛型 | ⚠️ any + 辅助函数 | V4 更优 |
| **接口复杂度** | 高（双层） | 中（单层） | DDD 更简单 |
| **Executor 视角** | TaskRunner 非泛型 | func(context.Context) | V4 更结构化 |
| **用户视角** | Task[Result] 泛型 | any + 类型断言 | V4 更安全 |
| **优先级支持** | ✅ Priority() | ✅ SubmitWithPriority | 一致 |
| **SourceID 支持** | ✅ SourceID() | ✅ Submit 参数 | 一致 |
| **生命周期管理** | ⚠️ 缺 Close | ✅ Close/CloseWithTimeout | DDD 更完整 |

#### 统一建议

```go
// 建议统一接口：结合 V4 的类型安全和 DDD 的生命周期管理

// TaskRunner - Executor 调度视角（非泛型）
type TaskRunner interface {
    Run(ctx context.Context)
    Priority() Priority
    SourceID() model.SourceID
}

// Task[Result] - 用户使用视角（泛型）
type Task[Result any] interface {
    TaskRunner
    Execute(ctx context.Context) (Result, error)
    Wait() (Result, error)
    Done() <-chan struct{}
}

// TaskExecutor - 提交入口
type TaskExecutor interface {
    Submit(runner TaskRunner) error
    SubmitBatch(runners []TaskRunner) error
    Stats() ExecutorStats
    Close() error
    CloseWithTimeout(timeout time.Duration) error
}
```

---

### 3.2 AsyncOp 模块

#### V4 定义

```go
// AsyncOp[Result] 异步操作句柄（包装 Task[Result]）
type AsyncOp[Result any] struct {
    task Task[Result]  // 持有 Task 引用
}

// Wait 等待完成并返回结果
func (op *AsyncOp[Result]) Wait() (Result, error) {
    return op.task.Wait()
}

// Done 返回完成 channel（用于 select）
func (op *AsyncOp[Result]) Done() <-chan struct{} {
    return op.task.Done()
}

// IsComplete 非阻塞检查是否完成
func (op *AsyncOp[Result]) IsComplete() bool {
    select {
    case <-op.task.Done():
        return true
    default:
        return false
    }
}
```

#### DDD 定义

```go
// AsyncOperation[T] 统一的异步操作接口
type AsyncOperation[T any] interface {
    // Get 等待异步操作完成并返回结果
    Get(ctx context.Context) (T, error)

    // Status 返回操作当前状态（非阻塞）
    Status() OperationStatus

    // Cancel 取消异步操作
    Cancel() (canceled bool, err error)

    // Discard 放弃结果，释放资源
    Discard() error

    // IsStarted 返回是否已启动
    IsStarted() bool

    // OnComplete 注册回调函数
    OnComplete(callback func(T, error)) string

    // OffComplete 注销回调函数
    OffComplete(cbID string) error
}

// OperationStatus 操作状态枚举
type OperationStatus int

const (
    StatusPending   OperationStatus = iota  // 待执行
    StatusRunning                            // 执行中
    StatusCompleted                          // 成功完成
    StatusFailed                             // 失败
    StatusCanceled                           // 取消
    StatusDiscarded                          // 丢弃
    StatusTimeout                            // 超时
)

// AsyncOp[T] 新版接口（待实施）
type AsyncOp[T any] interface {
    Await(ctx context.Context) (T, error)
    OnComplete(callback func(T, error)) string
    OnError(callback func(error)) string
    OnSuccess(callback func(T)) string
    OffComplete(cbID string) error
    WithTimeout(timeout time.Duration) AsyncOp[T]
    IsDone() bool
    IsSuccess() bool
    IsFailed() bool
    IsCanceled() bool
}
```

#### 对比分析

| 维度 | V4 | DDD | 评估 |
|------|----|----|------|
| **等待结果** | Wait() | Get(ctx)/Await(ctx) | DDD 支持 context |
| **完成检查** | IsComplete() | IsDone()/Status() | DDD 更详细 |
| **取消操作** | ❌ 缺失 | ✅ Cancel() | DDD 更完整 |
| **状态机** | ❌ 简单 | ✅ 7 种状态 | DDD 更完整 |
| **回调支持** | ❌ 缺失 | ✅ OnComplete/OnError | DDD 更完整 |
| **资源释放** | ❌ 缺失 | ✅ Discard() | DDD 更完整 |
| **超时支持** | ⚠️ 需外部 | ✅ WithTimeout() | DDD 更方便 |

#### 统一建议

```go
// 建议统一接口：扩展 V4 AsyncOp，添加 DDD 的状态管理能力

type AsyncOp[Result any] interface {
    // 核心方法（V4 已有）
    Wait() (Result, error)
    Done() <-chan struct{}
    IsComplete() bool

    // 扩展方法（从 DDD 借鉴）
    WaitWithContext(ctx context.Context) (Result, error)
    Status() OperationStatus
    Cancel() (canceled bool, err error)

    // 回调支持（可选，NICE-TO-HAVE）
    OnComplete(callback func(Result, error)) string
    OffComplete(cbID string) error
}
```

---

### 3.3 BTree 模块

#### V4 定义

```go
// BTreeManager - 具体结构体（无接口抽象）
type BTreeManager struct {
    tree     *btree.BTree
    mu       sync.RWMutex
    versions map[string]*VersionChain
}

// VersionChain 版本链
type VersionChain struct {
    key      []byte
    versions []Version
    mu       sync.RWMutex
}

// Version 版本
type Version struct {
    Value     []byte
    Timestamp *HLC
    TxnID     uint64
    Deleted   bool
}

// 方法
func (b *BTreeManager) Get(key []byte) []byte
func (b *BTreeManager) GetWithSnapshot(key []byte, snapshot *HLC) []byte
func (b *BTreeManager) ReplaceOrInsert(key, value []byte)
func (b *BTreeManager) UpdateVersion(key, value []byte, ts *HLC)
```

#### DDD 定义

```go
// BTree - B+tree 核心接口
type BTree interface {
    // 同步页管理
    LoadPage(ctx context.Context, pageID uint32) (Page, error)
    WritePage(ctx context.Context, page Page) error

    // 异步页管理
    LoadPageAsync(ctx context.Context, pageID uint32) PageFuture
    WritePageAsync(ctx context.Context, page Page) WriteFuture
    PrefetchPages(ctx context.Context, pageIDs []uint32) WriteFuture

    // 同步 B+tree 操作
    Insert(key, value []byte) error
    Delete(key []byte) error
    Search(key []byte) ([]byte, error)
    Scan(start, end []byte) (Iterator, error)

    // 异步 B+tree 操作
    InsertAsync(ctx context.Context, key, value []byte) WriteFuture
    DeleteAsync(ctx context.Context, key []byte) WriteFuture
    SearchAsync(ctx context.Context, key []byte) Future
    ScanAsync(ctx context.Context, start, end []byte) IteratorFuture

    // 刷盘
    Flush() error
    FlushAsync(ctx context.Context) WriteFuture

    Close() error
}
```

#### 对比分析

| 维度 | V4 | DDD | 评估 |
|------|----|----|------|
| **接口抽象** | ❌ 仅结构体 | ✅ 完整接口 | DDD 更优 |
| **MVCC 支持** | ✅ VersionChain | ❌ 缺失 | V4 更优 |
| **异步操作** | ❌ 无 | ✅ 完整 | DDD 更优 |
| **页管理** | ❌ 无 | ✅ LoadPage/WritePage | DDD 更优 |
| **范围查询** | ❌ 缺失 | ✅ Scan/ScanAsync | DDD 更优 |

#### 统一建议

```go
// 建议统一接口：结合 V4 的 MVCC 和 DDD 的接口抽象

type BTree interface {
    // 基础操作（同步）
    Get(key []byte) []byte
    Insert(key, value []byte) error
    Delete(key []byte) error
    Scan(start, end []byte) (Iterator, error)

    // MVCC 支持（V4 特有）
    GetWithSnapshot(key []byte, snapshot *HLC) []byte
    UpdateVersion(key, value []byte, ts *HLC)

    // 异步操作（DDD 风格）
    GetAsync(ctx context.Context, key []byte) AsyncOp[[]byte]
    InsertAsync(ctx context.Context, key, value []byte) AsyncOp[error]

    // 生命周期
    Flush() error
    Close() error
}
```

---

### 3.4 WAL 模块

#### V4 定义

```go
// WALManager - 具体结构体
type WALManager struct {
    file          *os.File
    writer        *bufio.Writer
    mu            sync.Mutex
    flushInterval time.Duration
    stopCh        chan struct{}
}

// LogEntry 日志条目
type LogEntry struct {
    Op        OpType
    Key       []byte
    Value     []byte
    Timestamp *HLC
    TxnID     uint64
    CRC       uint32
}

// 方法
func (w *WALManager) AppendBatch(entries []*LogEntry) error
func (w *WALManager) Flush() error
```

#### DDD 定义

```go
// WAL - 写前日志接口
type WAL interface {
    // 同步写日志
    Append(entry WALEntry) error
    Sync() error

    // 异步写日志
    AppendAsync(entry WALEntry) WriteFuture

    // 恢复和截断
    Recover() ([]WALEntry, error)
    Truncate(lsn uint64) error
    TruncateAsync(lsn uint64) WriteFuture

    // 生命周期
    Close() error
}

// WALEntry 日志条目（完整元数据）
type WALEntry struct {
    LSN       uint64
    TxID      uint64
    Timestamp int64
    Type      WALType
    Key       []byte
    Value     []byte
    PrevLSN   uint64
}

// WALType 日志类型
type WALType uint8

const (
    WALTypeInsert WALType = iota
    WALTypeDelete
    WALTypeTxBegin
    WALTypeCommit
    WALTypeTxRollback
    WALTypeCheckpoint
    // Bf-Tree 扩展类型
    WALTypeInsertMiniPage
    WALTypeDeleteMiniPage
    WALTypeUpgradeToFullPage
)
```

#### 对比分析

| 维度 | V4 | DDD | 评估 |
|------|----|----|------|
| **接口抽象** | ❌ 仅结构体 | ✅ 完整接口 | DDD 更优 |
| **批量写入** | ✅ AppendBatch | ❌ 单条 Append | V4 更优 |
| **恢复功能** | ❌ 缺失 | ✅ Recover() | DDD 更优 |
| **截断功能** | ❌ 缺失 | ✅ Truncate() | DDD 更优 |
| **异步操作** | ❌ 无 | ✅ AppendAsync | DDD 更优 |
| **日志类型** | ⚠️ 简单 | ✅ 完整类型 | DDD 更优 |

#### 统一建议

```go
// 建议统一接口：结合 V4 的批量写入和 DDD 的恢复/截断

type WAL interface {
    // 同步写入
    Append(entry *LogEntry) error
    AppendBatch(entries []*LogEntry) error  // V4 批量
    Sync() error

    // 恢复和截断
    Recover() ([]*LogEntry, error)  // DDD
    Truncate(lsn uint64) error      // DDD

    // 异步操作（可选）
    AppendAsync(ctx context.Context, entry *LogEntry) AsyncOp[error]

    // 生命周期
    Close() error
}
```

---

### 3.5 Clock/HLC 模块

#### V4 定义

```go
// HLC - 混合逻辑时钟（仅结构体）
type HLC struct {
    pt int64   // 物理时间（毫秒）
    c  uint16  // 逻辑计数
}

func (h *HLC) Now() *HLC {
    return &HLC{pt: time.Now().UnixMilli(), c: 0}
}

func (h *HLC) LessThanOrEqual(other *HLC) bool {
    if h.pt != other.pt {
        return h.pt <= other.pt
    }
    return h.c <= other.c
}
```

#### DDD 定义

暂无明确定义（在 Transaction 层提及 `hlc.Timestamp`，但无完整接口）

#### 对比分析

| 维度 | V4 | DDD | 评估 |
|------|----|----|------|
| **结构定义** | ✅ 基础结构体 | ❌ 缺失 | V4 有基础 |
| **接口抽象** | ❌ 无 | ❌ 无 | 都需要添加 |
| **方法完整性** | ⚠️ 仅 2 个方法 | - | 需扩展 |
| **时钟同步** | ❌ 无 | - | 需添加 |

#### 统一建议

```go
// 建议定义完整的 HLCClock 接口

type HLC struct {
    PT int64   // 物理时间（毫秒）
    C  uint16  // 逻辑计数
}

type HLCClock interface {
    // 获取当前时间
    Now() *HLC

    // 比较操作
    Compare(other *HLC) int        // -1, 0, 1
    LessThan(other *HLC) bool
    LessThanOrEqual(other *HLC) bool
    Equal(other *HLC) bool

    // 时钟同步（接收远程时间）
    Update(remote *HLC) *HLC

    // 序列化
    Marshal() ([]byte, error)
    Unmarshal(data []byte) error
}
```

---

### 3.6 Transaction 模块

#### V4 定义

**暂无事务接口定义**

V4 设计中，事务逻辑被隐式处理：
- `CompositeWriteTask` 在单个 Task 中顺序执行 WAL + BTree
- 无显式的事务开始/提交/回滚语义

#### DDD 定义

```go
// LocalTx - 本地事务接口
type LocalTx interface {
    // 单条操作（同步）
    Get(ctx context.Context, key []byte) ([]byte, error)
    Set(ctx context.Context, key, value []byte) error
    Delete(ctx context.Context, key []byte) error

    // 批量操作（同步）
    BatchSet(ctx context.Context, kvs []KeyValue) error
    BatchGet(ctx context.Context, keys [][]byte) ([]KeyValue, error)
    BatchDelete(ctx context.Context, keys [][]byte) error

    // 提交/回滚
    Commit() error
    CommitAsync() WriteFuture
    Rollback() error
}

// TxManager - 分布式事务管理器
type TxManager interface {
    // 开始事务
    Begin(ctx context.Context) (Tx, error)

    // 获取事务
    Get(txID TxID) (Tx, error)

    // 事务状态
    Status(txID TxID) (TxStatus, error)
}

// TxCoordinator - 2PC 协调器
type TxCoordinator interface {
    // 准备阶段
    Prepare(ctx context.Context, tx Tx) error

    // 提交/回滚
    Commit(ctx context.Context, tx Tx) error
    Rollback(ctx context.Context, tx Tx) error
}
```

#### 对比分析

| 维度 | V4 | DDD | 评估 |
|------|----|----|------|
| **本地事务** | ❌ 缺失 | ✅ LocalTx | DDD 完整 |
| **分布式事务** | ❌ 缺失 | ✅ TxManager/TxCoordinator | DDD 完整 |
| **ACID 保证** | ⚠️ 隐式 | ✅ 显式 | DDD 更清晰 |
| **隔离级别** | ❌ 无 | ⚠️ 待定义 | 都需补充 |

#### 统一建议

```go
// V4 需要添加事务支持

// LocalTx - 本地事务（V4 版本）
type LocalTx interface {
    // 操作
    Get(ctx context.Context, key []byte) ([]byte, error)
    Set(ctx context.Context, key, value []byte) error
    Delete(ctx context.Context, key []byte) error

    // 生命周期
    Commit() error
    Rollback() error
}

// Pipeline 扩展事务支持
func (p *Pipeline) BeginTx() (LocalTx, error)
```

---

## 4. 接口完备性矩阵

### 4.1 核心模块接口完备性

| 模块 | 接口 | V4 状态 | DDD 状态 | 级别 | 建议 |
|------|------|---------|----------|------|------|
| **Clock/HLC** | HLCClock | ⚠️ 仅结构体 | ❌ 缺失 | **MUST** | 定义完整接口 |
| **BTree** | BTreeManager | ⚠️ 仅结构体 | ✅ 完整 | **MUST** | V4 抽象为接口 |
| **WAL** | WALManager | ⚠️ 部分实现 | ✅ 完整 | **MUST** | V4 添加 Recover/Truncate |
| **KVAPI** | Pipeline | ✅ Task 抽象 | ✅ KVStore | **MUST** | 两者兼容 |
| **Transaction** | LocalTx | ❌ 缺失 | ✅ 完整 | **MUST** | V4 添加事务接口 |
| **Task/Executor** | TaskRunner | ✅ 双层泛型 | ✅ 单层 any | **SHOULD** | 统一设计 |
| **AsyncOp** | AsyncOp[T] | ⚠️ 简单 | ✅ 完整 | **SHOULD** | V4 扩展 Cancel/Status |

### 4.2 功能完备性检查

| 功能 | V4 支持 | DDD 支持 | 差距分析 | 优先级 |
|------|--------|---------|---------|--------|
| 同步操作 | ✅ | ✅ | 一致 | - |
| 异步操作 | ✅ Task[Result] | ✅ AsyncOperation[T] | 接口不同 | SHOULD |
| 优先级 | ✅ Priority | ✅ Priority | 一致 | - |
| SourceID | ✅ SourceID | ✅ SourceID | 一致 | - |
| 取消操作 | ⚠️ 需 ctx | ✅ Cancel() | V4 缺显式 Cancel | SHOULD |
| 状态查询 | ⚠️ IsComplete | ✅ Status() | V4 缺状态机 | SHOULD |
| 回调支持 | ❌ | ✅ OnComplete | V4 缺失 | NICE-TO-HAVE |
| 批量操作 | ✅ CompositeTask | ✅ BatchSet | 设计不同 | SHOULD |
| 事务支持 | ❌ | ✅ LocalTx | V4 缺失 | **MUST** |
| MVCC | ✅ VersionChain | ❌ 缺失 | DDD 缺失 | SHOULD |
| 生命周期 | ⚠️ 缺 Close | ✅ Close/CloseWithTimeout | V4 不完整 | MUST |

---

## 5. 接口完备性分类

### 5.1 MUST - 核心必需

| 接口 | 模块 | V4 缺失 | DDD 缺失 | 实现建议 |
|------|------|--------|--------|---------|
| **HLCClock** | Clock | 完整接口定义 | 完整接口定义 | 新建 `internal/domain/service/clock.go` |
| **BTree** | Storage | 接口抽象 | - | V4 抽象 BTreeManager 为接口 |
| **WAL.Recover** | Storage | Recover 方法 | - | V4 添加恢复功能 |
| **WAL.Truncate** | Storage | Truncate 方法 | - | V4 添加截断功能 |
| **LocalTx** | Transaction | 完整接口 | - | V4 添加事务支持 |
| **Close/CloseWithTimeout** | Lifecycle | 生命周期方法 | - | V4 添加优雅关闭 |

### 5.2 SHOULD - 强烈建议

| 接口 | 模块 | V4 缺失 | DDD 缺失 | 实现建议 |
|------|------|--------|--------|---------|
| **AsyncOp.Cancel** | Async | Cancel 方法 | - | V4 扩展 AsyncOp |
| **AsyncOp.Status** | Async | 状态查询 | - | V4 添加状态机 |
| **统一 TaskRunner** | Task | - | - | 统一双层/单层设计 |
| **MVCC** | Storage | - | VersionChain | DDD 添加 MVCC 支持 |
| **批量操作统一** | Batch | - | - | 统一 BatchSet/CompositeTask |

### 5.3 NICE-TO-HAVE - 锦上添花

| 接口 | 模块 | 说明 |
|------|------|------|
| **OnComplete 回调** | Async | 异步完成回调通知 |
| **WithTimeout 链式调用** | Async | 超时设置链式 API |
| **Metrics 集成** | Observability | 内置性能指标采集 |
| **调试接口** | Debug | 运行时状态检查 |

### 5.4 OPTIONAL - 可选扩展

| 接口 | 模块 | 说明 |
|------|------|------|
| **Tracing 集成** | Observability | 分布式链路追踪 |
| **自定义序列化** | Codec | 自定义编解码器 |
| **插件系统** | Extension | 可扩展插件机制 |

---

## 6. 统一建议

### 6.1 V4 需要补充的接口

```go
// 1. HLCClock 接口（MUST）
type HLCClock interface {
    Now() *HLC
    Compare(other *HLC) int
    Update(remote *HLC) *HLC
}

// 2. BTree 接口抽象（MUST）
type BTree interface {
    Get(key []byte) []byte
    Insert(key, value []byte) error
    Delete(key []byte) error
    Scan(start, end []byte) (Iterator, error)
    Close() error
}

// 3. WAL 扩展（MUST）
type WAL interface {
    Append(entry *LogEntry) error
    AppendBatch(entries []*LogEntry) error
    Recover() ([]*LogEntry, error)  // 新增
    Truncate(lsn uint64) error      // 新增
    Sync() error
    Close() error
}

// 4. LocalTx 事务接口（MUST）
type LocalTx interface {
    Get(ctx context.Context, key []byte) ([]byte, error)
    Set(ctx context.Context, key, value []byte) error
    Delete(ctx context.Context, key []byte) error
    Commit() error
    Rollback() error
}

// 5. AsyncOp 扩展（SHOULD）
type AsyncOp[Result any] interface {
    Wait() (Result, error)
    WaitWithContext(ctx context.Context) (Result, error)  // 新增
    Done() <-chan struct{}
    IsComplete() bool
    Status() OperationStatus  // 新增
    Cancel() (bool, error)    // 新增
}
```

### 6.2 DDD 需要更新的接口

```go
// 1. MVCC 支持（SHOULD）
type BTree interface {
    // 现有方法...

    // MVCC 扩展
    GetWithSnapshot(key []byte, snapshot *HLC) []byte
    UpdateVersion(key, value []byte, ts *HLC)
}

// 2. HLCClock 定义（MUST）
// 在 DDD 文档中添加 HLCClock 接口定义

// 3. TaskRunner 统一（SHOULD）
// 考虑采用 V4 的双层接口设计
```

### 6.3 统一接口设计原则

1. **接口优先**：所有核心组件都应定义接口，避免直接依赖具体实现
2. **类型安全**：优先使用泛型而非 any，提供编译时类型检查
3. **生命周期完整**：所有资源持有者必须提供 Close/CloseWithTimeout
4. **状态可见**：异步操作应提供状态查询能力
5. **取消支持**：长时间运行的操作应支持取消

---

## 7. 附录

### 7.1 完整接口对比表

| 模块 | V4 接口/结构 | DDD 接口 | 统一建议 |
|------|-------------|----------|---------|
| Clock | HLC struct | ❌ | HLCClock interface |
| BTree | BTreeManager struct | BTree interface | BTree interface + MVCC |
| WAL | WALManager struct | WAL interface | WAL interface |
| KVAPI | Pipeline struct | KVStore interface | 统一为 Pipeline + Task |
| Tx | ❌ | LocalTx interface | LocalTx interface |
| Task | TaskRunner + Task[Result] | TaskExecutor + GoroutineProvider | 统一 TaskRunner |
| AsyncOp | AsyncOp[Result] struct | AsyncOperation[T] interface | 扩展 AsyncOp |

### 7.2 参考文档

- `docs/07_spike/2026-03-04-spike-async-pipeline-v4.md` - V4 异步管道设计
- `docs/07_spike/2026-02-18_spike_nexkv-ddd-interface.md` - DDD Interface 定义
- `docs/07_spike/2026-02-25_spike-glm-unified-executor.md` - 统一执行器架构
- `internal/domain/service/task.go` - TaskExecutor 实现
- `internal/domain/service/asyncop.go` - AsyncOp[T] 实现

---

## 8. 总结

### 8.1 关键发现

1. **V4 优势**：
   - 双层泛型接口设计，类型安全
   - CompositeTask 组合模式优雅
   - MVCC VersionChain 已实现

2. **DDD 优势**：
   - 完整的事务支持（LocalTx/TxManager）
   - AsyncOp 状态机完整
   - 生命周期管理规范

3. **主要差距**：
   - V4 缺少事务接口
   - V4 缺少 WAL Recover/Truncate
   - DDD 缺少 MVCC 支持
   - DDD 缺少 HLCClock 定义

### 8.2 下一步行动

| 优先级 | 行动项 | 负责模块 | 预估时间 |
|--------|-------|---------|---------|
| P0 | V4 添加 WAL Recover/Truncate | Storage | 2h |
| P0 | V4 添加 LocalTx 接口 | Transaction | 4h |
| P0 | 定义 HLCClock 接口 | Clock | 1h |
| P1 | V4 BTreeManager 抽象为接口 | Storage | 2h |
| P1 | V4 AsyncOp 添加 Cancel/Status | Async | 2h |
| P2 | 统一 TaskRunner 设计 | Task | 4h |

---

> **文档版本**: v1.0
> **最后更新**: 2026-03-05
> **审查状态**: ✅ 完成
