# Pipeline 通用框架设计方案

> **提案日期**: 2026-03-08
> **状态**: 💡 设计提案 v2.0
> **优先级**: P1 (高)
> **依赖**: Phase 1.5 RunLoop 优化（已完成）

---

## 文档结构

本文档分为两部分：
1. **Part 1: 通用 Pipeline 框架** - 基础设施设计
2. **Part 2: NexKV 应用实例** - 具体业务实现

---

# Part 1: 通用 Pipeline 框架

## 1. 框架设计目标

### 1.1 核心原则

| 原则 | 说明 | 示例 |
|------|------|------|
| **通用性** | 不绑定具体业务 | 可用于 WAL、RPC、批处理等 |
| **类型安全** | 使用泛型 | `Pipeline[T]` |
| **可组合** | Stage 可灵活组装 | 用户决定顺序 |
| **高性能** | 零拷贝传递 | 指针引用 |
| **可观测** | 内置监控接口 | Metrics、Tracing |

### 1.2 框架职责 vs 业务职责

| 框架负责 | 业务负责 |
|---------|---------|
| Stage 连接（Channel） | 具体业务逻辑 |
| 数据传递（泛型 T） | 数据转换规则 |
| 错误传播（OnError） | 错误处理策略 |
| 背压控制（队列容量） | 流量控制策略 |
| 监控统计（Metrics） | 业务指标定义 |

---

## 2. 核心接口设计

### 2.1 Stage 接口（核心抽象）

```go
// Package pipeline

// Stage[T] 处理阶段接口
//
// T 是输入输出类型，可以是：
// - *WriteRequest（写请求）
// - *ReadRequest（读请求）
// - []byte（原始数据）
// - any（任意类型）
type Stage[T any] interface {
    // Name 阶段名称（用于监控）
    Name() string

    // Process 处理数据
    //
    // 返回值：
    //   T: 处理后的数据（可以是输入类型，也可以转换类型）
    //   error: 处理错误（会触发 OnError）
    Process(ctx context.Context, item T) (T, error)

    // OnError 错误处理回调
    //
    // 当 Process 返回 error 时调用
    // 可以选择：
    //   1. 返回修改后的 item（继续处理）
    //   2. 返回 error（终止 Pipeline）
    //   3. 记录日志并继续
    OnError(ctx context.Context, item T, err error) (T, error)
}
```

### 2.2 Pipeline 接口（组装器）

```go
// Pipeline[T] 管道组装器
//
// 使用 Builder 模式组装 Stage
type Pipeline[T any] struct {
    stages []Stage[T]
    config *Config
}

// NewPipeline 创建新 Pipeline
func NewPipeline[T any]() *Pipeline[T] {
    return &Pipeline[T]{
        config: DefaultConfig(),
    }
}

// AddStage 添加 Stage（顺序决定执行顺序）
//
// 示例：
//   pipeline.NewPipeline[Request]().
//       AddStage(&ValidationStage{}).
//       AddStage(&ProcessStage{}).
//       AddStage(&SaveStage{})
func (p *Pipeline[T]) AddStage(stage Stage[T]) *Pipeline[T] {
    p.stages = append(p.stages, stage)
    return p
}

// WithConfig 配置 Pipeline
func (p *Pipeline[T]) WithConfig(config *Config) *Pipeline[T] {
    p.config = config
    return p
}

// Build 构建 Pipeline（启动所有 Stage）
//
// 返回 RunningPipeline，可以提交数据
func (p *Pipeline[T]) Build() (*RunningPipeline[T], error) {
    if len(p.stages) == 0 {
        return nil, errors.New("no stages added")
    }

    running := &RunningPipeline[T]{
        stages: p.stages,
        config: p.config,
    }

    // 启动各个 Stage 的 Worker
    if err := running.start(); err != nil {
        return nil, err
    }

    return running, nil
}
```

### 2.3 RunningPipeline 接口（运行时）

```go
// RunningPipeline[T] 运行中的 Pipeline
type RunningPipeline[T any] struct {
    stages    []Stage[T]
    workers   []*stageWorker[T]
    config    *Config
    metrics   *Metrics
    ctx       context.Context
    cancel    context.CancelFunc
}

// Submit 提交数据到 Pipeline
//
// 数据会按顺序经过各个 Stage：
//   Stage1 → Stage2 → Stage3 → ... → Result
//
// 返回：
//   error: 提交失败（如队列满）
func (p *RunningPipeline[T]) Submit(ctx context.Context, item T) error {
    // 背压检查
    if p.config.EnableBackpressure {
        if p.workers[0].QueueLength() > p.config.MaxQueueLength {
            return errors.ErrBackpressure
        }
    }

    // 提交到第一个 Stage
    return p.workers[0].Submit(ctx, item)
}

// SubmitWithResult 提交并等待结果
func (p *RunningPipeline[T]) SubmitWithResult(ctx context.Context, item T) (T, error) {
    resultCh := make(chan result[T], 1)

    // 包装 item，添加结果回调
    wrapper := &itemWrapper[T]{
        item:      item,
        resultCh:  resultCh,
        startTime: time.Now(),
    }

    if err := p.Submit(ctx, wrapper); err != nil {
        var zero T
        return zero, err
    }

    select {
    case res := <-resultCh:
        return res.item, res.err
    case <-ctx.Done():
        var zero T
        return zero, ctx.Err()
    }
}

// Close 关闭 Pipeline
func (p *RunningPipeline[T]) Close() error {
    p.cancel()

    // 等待所有 Worker 完成
    var wg sync.WaitGroup
    for _, w := range p.workers {
        wg.Add(1)
        go func(worker *stageWorker[T]) {
            defer wg.Done()
            worker.Stop()
        }(w)
    }
    wg.Wait()

    return nil
}

// Stats 获取统计信息
func (p *RunningPipeline[T]) Stats() *Stats {
    return p.metrics.Snapshot()
}
```

---

## 3. 内部实现

### 3.1 Stage Worker（执行器）

```go
// stageWorker[T] Stage 执行器
type stageWorker[T any] struct {
    stage     Stage[T]
    index     int  // 在 Pipeline 中的位置
    next      *stageWorker[T]  // 下一个 Worker
    taskCh    chan *itemWrapper[T]
    config    *Config

    // 统计
    metrics   *stageMetrics
    running   atomic.Bool
}

// itemWrapper[T] 数据包装器
type itemWrapper[T any] struct {
    item      T
    resultCh  chan result[T]
    startTime time.Time
    metadata  map[string]interface{}  // 用于 Stage 间传递元数据
}

// result[T] 处理结果
type result[T any] struct {
    item T
    err  error
}

// Start 启动 Worker
func (w *stageWorker[T]) Start() error {
    if !w.running.CompareAndSwap(false, true) {
        return errors.New("already started")
    }

    go w.runLoop()
    return nil
}

// runLoop 事件循环
func (w *stageWorker[T]) runLoop() {
    defer w.running.Store(false)

    for {
        select {
        case wrapper := <-w.taskCh:
            w.process(wrapper)

        case <-w.ctx.Done():
            return
        }
    }
}

// process 处理单个数据
func (w *stageWorker[T]) process(wrapper *itemWrapper[T]) {
    start := time.Now()

    // 调用 Stage.Process
    item, err := w.stage.Process(wrapper.ctx, wrapper.item)

    // 更新统计
    latency := time.Since(start)
    w.metrics.Record(latency, err)

    if err != nil {
        // 错误处理
        item, err = w.stage.OnError(wrapper.ctx, wrapper.item, err)
    }

    // 决定后续处理
    if err != nil {
        // 终止 Pipeline，返回错误
        w.sendResult(wrapper, err)
    } else if w.next == nil {
        // 最后一个 Stage，返回成功
        wrapper.item = item
        w.sendResult(wrapper, nil)
    } else {
        // 传递给下一个 Stage
        wrapper.item = item
        w.next.Submit(wrapper.ctx, wrapper)
    }
}

// sendResult 发送结果
func (w *stageWorker[T]) sendResult(wrapper *itemWrapper[T], err error) {
    select {
    case wrapper.resultCh <- result[T]{item: wrapper.item, err: err}:
    case <-wrapper.ctx.Done():
    }
}

// Submit 提交数据（由前一个 Stage 或外部调用）
func (w *stageWorker[T]) Submit(ctx context.Context, item T) error {
    wrapper := &itemWrapper[T]{
        item:      item,
        resultCh:  make(chan result[T], 1),
        startTime: time.Now(),
        ctx:       ctx,
    }

    select {
    case w.taskCh <- wrapper:
        return nil
    default:
        return errors.ErrQueueFull
    }
}

// QueueLength 获取队列长度
func (w *stageWorker[T]) QueueLength() int {
    return len(w.taskCh)
}
```

### 3.2 配置结构

```go
// Config Pipeline 配置
type Config struct {
    // 队列配置
    QueueSize int  // 默认: 1000

    // 背压配置
    EnableBackpressure bool   // 默认: true
    MaxQueueLength     int    // 默认: 10000

    // Worker 配置
    WorkerCount int  // 默认: 1（每个 Stage 一个 Worker）

    // 监控配置
    EnableMetrics bool  // 默认: true
    EnableTracing bool  // 默认: false
}

// DefaultConfig 默认配置
func DefaultConfig() *Config {
    return &Config{
        QueueSize:          1000,
        EnableBackpressure: true,
        MaxQueueLength:     10000,
        WorkerCount:        1,
        EnableMetrics:      true,
        EnableTracing:      false,
    }
}
```

### 3.3 监控接口

```go
// Metrics Pipeline 指标
type Metrics struct {
    mu       sync.Mutex
    stages   map[string]*stageMetrics
}

type stageMetrics struct {
    name      string
    processed int64
    failed    int64
    totalLatency int64  // nanoseconds
    p50Latency time.Duration
    p95Latency time.Duration
    p99Latency time.Duration
}

// Record 记录一次处理
func (m *stageMetrics) Record(latency time.Duration, err error) {
    m.mu.Lock()
    defer m.mu.Unlock()

    m.processed++
    m.totalLatency += int64(latency)

    if err != nil {
        m.failed++
    }

    // TODO: 计算 P50/P95/P99
}

// Snapshot 获取快照
func (m *Metrics) Snapshot() *Stats {
    m.mu.Lock()
    defer m.mu.Unlock()

    stages := make(map[string]*StageStats)
    for name, metrics := range m.stages {
        stages[name] = &StageStats{
            Name:       metrics.name,
            Processed:  metrics.processed,
            Failed:     metrics.failed,
            AvgLatency: time.Duration(metrics.totalLatency / metrics.processed),
            P50Latency: metrics.p50Latency,
            P95Latency: metrics.p95Latency,
            P99Latency: metrics.p99Latency,
        }
    }

    return &Stats{Stages: stages}
}

// Stats 统计信息
type Stats struct {
    Stages map[string]*StageStats
}

// StageStats 阶段统计
type StageStats struct {
    Name       string
    Processed  int64
    Failed     int64
    AvgLatency time.Duration
    P50Latency time.Duration
    P95Latency time.Duration
    P99Latency time.Duration
}
```

---

## 4. 框架特性

### 4.1 零拷贝传递

```go
// 框架只传递指针，不复制数据
type itemWrapper[T any] struct {
    item T  // T 通常是指针类型
}

// 示例：
type WriteRequest struct {
    Key   []byte  // 只传递 slice header（24 bytes）
    Value []byte
}

// Stage 之间传递的是 WriteRequest 的指针
// []byte 的底层数据不会被复制
```

### 4.2 背压控制

```go
// 两级背压机制

// Level 1: Channel 容量限制
select {
case w.taskCh <- wrapper:
    return nil
default:
    return errors.ErrQueueFull  // 队列满，拒绝新请求
}

// Level 2: 动态背压
func (p *RunningPipeline[T]) Submit(ctx context.Context, item T) error {
    if p.config.EnableBackpressure {
        if p.workers[0].QueueLength() > p.config.MaxQueueLength {
            return errors.ErrBackpressure
        }
    }
    // ...
}
```

### 4.3 错误处理链

```go
// 方案 A: 终止 Pipeline（默认）
func (s *MyStage) OnError(ctx context.Context, item T, err error) (T, error) {
    log.Errorf("Stage %s error: %v", s.Name(), err)
    var zero T
    return zero, err  // 返回 error，终止 Pipeline
}

// 方案 B: 继续处理
func (s *MyStage) OnError(ctx context.Context, item T, err error) (T, error) {
    log.Warnf("Stage %s error (skipping): %v", s.Name(), err)
    return item, nil  // 返回原 item，继续下一个 Stage
}

// 方案 C: 降级处理
func (s *MyStage) OnError(ctx context.Context, item T, err error) (T, error) {
    // 使用降级逻辑
    item = s.fallback(item)
    return item, nil
}
```

---

## 5. 框架性能基准

### 5.1 理论分析

| 指标 | 数值 | 说明 |
|------|------|------|
| **Stage 间延迟** | ~100-200 ns | Channel 发送 + 接收 |
| **内存分配** | ~32B/item | itemWrapper 结构体 |
| **零拷贝** | ✅ | 传递指针 |
| **背压开销** | ~10 ns | 队列长度检查 |

### 5.2 预期性能

| 并发数 | 单次延迟 | 吞吐量 |
|--------|---------|--------|
| 1 | ~500 ns/stage | 2M ops/stage/s |
| 4 | ~600 ns/stage | 6.6M ops/stage/s |
| 16 | ~800 ns/stage | 20M ops/stage/s |

**说明**：
- 假设每个 Stage Process 耗时 ~300ns
- 4 个 Stage 的总延迟 ≈ 4 × (300 + 200) = 2μs
- 吞吐量受限于最慢的 Stage

---

# Part 2: NexKV 应用实例

## 6. 应用场景：写入 Pipeline

### 6.1 业务需求

**目标**：优化写入流程，提升吞吐量

**当前问题**：
- BfTree + WAL + Disk 延迟线性叠加（~10,000ns）
- CPU 利用率低（~20%）
- 吞吐量受限（24K ops/s @ 100 并发）

**解决方案**：使用 Pipeline 框架构建异步写入流程

---

## 7. 数据结构定义

### 7.1 WriteRequest

```go
// Package storage

// WriteRequest 写入请求
type WriteRequest struct {
    // 基础字段
    ID     uint64
    Type   OpType  // Put / Delete / Batch

    // 数据（零拷贝传递）
    Key     []byte
    Value   []byte

    // WAL Entry（由 WALStage 填充）
    WALEntry *WALEntry

    // 选项
    Options *WriteOptions

    // 上下文
    Context context.Context

    // 元数据（Stage 间传递）
    Metadata map[string]interface{}
}

// OpType 操作类型
type OpType int

const (
    OpTypePut OpType = iota
    OpTypeDelete
)

// WriteOptions 写入选项
type WriteOptions struct {
    Sync bool  // 是否同步刷盘
}
```

---

## 8. Stage 实现

### 8.1 BfTreeStage

```go
// BfTreeStage BfTree 处理阶段
type BfTreeStage struct {
    tree *BfTree
}

func NewBfTreeStage(tree *BfTree) *BfTreeStage {
    return &BfTreeStage{tree: tree}
}

func (s *BfTreeStage) Name() string {
    return "bftree"
}

func (s *BfTreeStage) Process(ctx context.Context, req *WriteRequest) (*WriteRequest, error) {
    // 1. 更新 MemTable
    entry, err := s.tree.Put(req.Key, req.Value)
    if err != nil {
        return nil, err
    }

    // 2. 附加 WAL Entry（传递给下一个 Stage）
    req.WALEntry = entry

    return req, nil
}

func (s *BfTreeStage) OnError(ctx context.Context, req *WriteRequest, err error) (*WriteRequest, error) {
    log.Errorf("[BfTree] Put failed: %v", err)
    return nil, err  // 终止 Pipeline
}
```

### 8.2 WALStage

```go
// WALStage WAL 处理阶段
type WALStage struct {
    wal *WAL
}

func NewWALStage(wal *WAL) *WALStage {
    return &WALStage{wal: wal}
}

func (s *WALStage) Name() string {
    return "wal"
}

// Process 处理请求（单条写入）
//
// 注意：批量优化应该在 WAL 内部实现，而不是在 Stage 层面
// 这样可以保持 Stage 简单，同时保留批量优化的能力
func (s *WALStage) Process(ctx context.Context, req *WriteRequest) (*WriteRequest, error) {
    // 单条写入 WAL
    // WAL 内部可以选择批量写入优化
    err := s.wal.Write(req.WALEntry)
    return req, err
}

func (s *WALStage) OnError(ctx context.Context, req *WriteRequest, err error) (*WriteRequest, error) {
    log.Errorf("[WAL] Write failed: %v", err)

    // WAL 写入失败，数据未持久化
    // 根据策略，可以重试或返回错误
    return nil, err
}
```

**设计说明**：
- WALStage 只负责调用 WAL 写入接口
- 批量优化由 WAL 内部实现（如 WriteBatch）
- 这样可以保持 Stage 简单，同时不失去性能优化
- 如果需要批量 API，可以提供 `WALBatchStage` 专门处理批量请求

### 8.3 DiskStage

```go
// DiskStage 磁盘刷盘阶段
type DiskStage struct {
    disk *DiskManager
}

func NewDiskStage(disk *DiskManager) *DiskStage {
    return &DiskStage{disk: disk}
}

func (s *DiskStage) Name() string {
    return "disk"
}

func (s *DiskStage) Process(ctx context.Context, req *WriteRequest) (*WriteRequest, error) {
    if req.Options.Sync {
        // 同步刷盘
        if err := s.disk.Flush(req.WALEntry); err != nil {
            return nil, err
        }
    } else {
        // 异步刷盘（后台任务）
        go func() {
            s.disk.FlushAsync(req.WALEntry)
        }()
    }

    return req, nil
}

func (s *DiskStage) OnError(ctx context.Context, req *WriteRequest, err error) (*WriteRequest, error) {
    log.Errorf("[Disk] Flush failed: %v", err)
    return req, nil  // 刷盘失败不影响写入成功（异步）
}
```

---

## 9. Pipeline 组装

### 9.1 方案 A: BfTree → WAL → Disk（先写内存）

```go
// NewWritePipeline 创建写入 Pipeline（BfTree 优先）
func NewWritePipeline(tree *BfTree, wal *WAL, disk *DiskManager) (*pipeline.RunningPipeline[*WriteRequest], error) {
    return pipeline.NewPipeline[*WriteRequest]().
        WithConfig(pipeline.DefaultConfig()).
        AddStage(NewBfTreeStage(tree)).  // 1. 更新 MemTable
        AddStage(NewWALStage(wal)).      // 2. 写入 WAL
        AddStage(NewDiskStage(disk)).    // 3. 刷盘
        Build()
}

// 使用
pipeline, err := NewWritePipeline(tree, wal, disk)
if err != nil {
    return err
}
defer pipeline.Close()

// 提交写入
req := &WriteRequest{
    Key:     []byte("key"),
    Value:   []byte("value"),
    Options: &WriteOptions{Sync: false},
}

result, err := pipeline.SubmitWithResult(context.Background(), req)
```

### 9.2 方案 B: WAL → BfTree → Disk（先持久化）

```go
// NewWritePipelineWALFirst 创建写入 Pipeline（WAL 优先）
func NewWritePipelineWALFirst(tree *BfTree, wal *WAL, disk *DiskManager) (*pipeline.RunningPipeline[*WriteRequest], error) {
    return pipeline.NewPipeline[*WriteRequest]().
        WithConfig(pipeline.DefaultConfig()).
        AddStage(NewWALStage(wal)).      // 1. 先写 WAL
        AddStage(NewBfTreeStage(tree)).  // 2. 后更新 MemTable
        AddStage(NewDiskStage(disk)).    // 3. 最后刷盘
        Build()
}
```

### 9.3 两种方案对比

| 维度 | BfTree 优先 | WAL 优先 |
|------|------------|----------|
| **延迟** | 低（先写内存） | 高（先写磁盘） |
| **一致性** | 中（进程崩溃可能丢失） | 高（WAL 先写） |
| **复杂度** | 低（简单） | 中（需要回滚机制） |
| **适用场景** | 性能优先 | 一致性优先 |

**推荐**：默认使用 BfTree 优先，提供 WAL 优先作为可选配置

---

## 10. 与现有系统集成

### 10.1 兼容性设计

```go
// BfTreeConfig BfTree 配置
type BfTreeConfig struct {
    // Pipeline 模式开关
    EnablePipeline bool

    // Pipeline 配置
    PipelineConfig *pipeline.Config

    // Stage 顺序
    PipelineOrder string  // "bftree-first" | "wal-first"
}

// BfTree BfTree 实现
type BfTree struct {
    config    *BfTreeConfig

    // 传统模式
    executor *PerCoreExecutor

    // Pipeline 模式
    pipeline *pipeline.RunningPipeline[*WriteRequest]
}

// NewBfTree 创建 BfTree
func NewBfTree(config *BfTreeConfig) (*BfTree, error) {
    tree := &BfTree{
        config: config,
    }

    if config.EnablePipeline {
        // 初始化 Pipeline 模式
        var err error
        tree.pipeline, err = NewWritePipeline(tree.memTable, tree.wal, tree.disk)
        if err != nil {
            return nil, err
        }
    } else {
        // 初始化传统模式
        tree.executor = NewPerCoreExecutor()
    }

    return tree, nil
}

// Put 写入（自动路由）
func (t *BfTree) Put(ctx context.Context, key, value []byte) error {
    if t.config.EnablePipeline {
        return t.putPipeline(ctx, key, value)
    }
    return t.putSync(ctx, key, value)
}

func (t *BfTree) putPipeline(ctx context.Context, key, value []byte) error {
    req := &WriteRequest{
        Type:     OpTypePut,
        Key:      key,
        Value:    value,
        Options:  &WriteOptions{Sync: false},
        Context:  ctx,
    }

    _, err := t.pipeline.SubmitWithResult(ctx, req)
    return err
}
```

---

## 11. 实施方案

### 11.1 Phase 1: 框架实现（3-5 天）

**任务**：
1. 实现 `Stage[T]` 接口
2. 实现 `Pipeline[T]` 组装器
3. 实现 `RunningPipeline[T]` 运行时
4. 实现 `stageWorker[T]` 执行器
5. 实现监控接口

**验收**：
- 框架编译通过
- 单元测试覆盖率 > 80%
- 性能基准测试

### 11.2 Phase 2: 业务实现（3-4 天）

**任务**：
1. 实现 `WriteRequest` 数据结构
2. 实现 `BfTreeStage`
3. 实现 `WALStage`
4. 实现 `DiskStage`
5. 实现 Pipeline 组装

**验收**：
- 集成测试通过
- 功能测试通过
- 性能达标

### 11.3 Phase 3: 优化和集成（2-3 天）

**任务**：
1. 背压优化
2. 错误处理完善
3. 监控集成
4. 文档完善
5. 性能调优

**验收**：
- 压力测试通过
- 长时间运行稳定
- 文档完整

---

## 12. 性能预期

### 12.1 理论分析

| 指标 | 当前同步 | Pipeline 异步（预期） | 说明 |
|------|---------|---------------------|------|
| **单次延迟** | ~10,000 ns | ~2,000-5,000 ns | -50% 至 -80% |
| **吞吐量** | 24K ops/s | 100K-500K ops/s | **2-5x 提升** |
| **CPU 利用率** | ~20% | ~50-70% | +150% 至 +250% |

**说明**：
- 性能数据基于业界类似方案（Lealone、RocksDB）的经验估算
- 实际性能取决于具体实现和硬件环境
- **实测数据将在 Phase 1 完成后更新**

### 12.2 验收标准

| 指标 | 目标 | 测试方法 |
|------|------|---------|
| **P50 延迟** | < 5μs | 基准测试 |
| **P99 延迟** | < 20μs | 基准测试 |
| **吞吐量** | > 100K ops/s | 压力测试 |
| **内存分配** | < 32B/op | pprof 分析 |
| **相比当前提升** | > 2x | 对比测试 |

---

## 13. 风险评估

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| **框架复杂度** | 🟡 中 | 充分的单元测试、文档 |
| **业务耦合** | 🟢 低 | 框架与业务分离 |
| **数据一致性** | 🟡 中 | 严格测试、灰度发布 |
| **性能回归** | 🟢 低 | 性能基准测试 |

---

## 14. 总结

### 14.1 框架价值

| 价值 | 说明 |
|------|------|
| **通用性** | 可用于任何场景，不限于存储 |
| **类型安全** | 泛型保证编译时类型检查 |
| **可组合** | Stage 灵活组装，顺序可配置 |
| **高性能** | 零拷贝传递，低延迟 |
| **可观测** | 内置监控接口 |

### 14.2 实施路径

```
Part 1: 框架实现（3-5 天）
    ↓
Part 2: 业务实现（3-4 天）
    ↓
优化和集成（2-3 天）

总计: 8-12 天
```

### 14.3 下一步行动

**立即行动**：
1. ⏳ 审批本设计方案
2. ⏳ 创建 GitHub Issue 跟踪
3. ⏳ 启动 Phase 1（框架实现）

**准备阶段**：
1. 搭建测试环境
2. 准备基准测试
3. 设计监控面板

---

**文档版本**: v2.1
**主要变更**:
- 框架与业务分离，使用泛型设计
- WALStage 简化为单条写入（批量优化由 WAL 内部实现）
- 性能预期调整为保守估计（2-5x 提升，需实测验证）

**提案者**: NexKV Team
**预计工期**: 8-12 天
**风险等级**: 🟢 低
