# BTree 并发控制架构设计

**日期**: 2026-03-09
**主题**: 4读1写并发模型详细设计
**基于**: Lealone BTree 并发机制

---

## 一、核心并发模型

### 1.1 设计原则

```
┌─────────────────────────────────────────────────────────────┐
│           Lealone 风格的 4读1写并发模型                        │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  核心原则:                                                   │
│  ✅ 单写线程: 避免写操作的 CAS 冲突和路径复制竞争              │
│  ✅ 多读线程: 充分利用多核 CPU 的并行能力                       │
│  ✅ 读写隔离: CCOW + 版本化根指针，读写互不阻塞                │
│  ✅ 快照隔离: 每个读操作看到一致的数据快照                     │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

### 1.2 架构图

```
┌─────────────────────────────────────────────────────────────────┐
│                      BTree 并发架构                              │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  ┌──────────────────────────────────────────────────────┐      │
│  │                  BTree 实例                          │      │
│  │                                                          │      │
│  │  ┌────────────────┐  ┌──────────────┐               │      │
│  │  │ VersionedRoot │  │  WriteQueue  │               │      │
│  │  │  (atomic.Value) │  │ (Single Writer)│               │      │
│  │  └────────┬───────┘  └──────┬───────┘               │      │
│  │           │                    │                       │      │
│  │           ▼                    ▼                       │      │
│  │    ┌──────────────────────────────────────────┐         │      │
│  │    │       PageManager (三层缓存)             │         │      │
│  │    │  ┌─────────┐  ┌─────────┐  ┌─────────┐   │         │      │
│  │    │  │ L1 Page │  │ L2 Buff  │  │ L3 Disk │   │         │      │
│  │    │  └────┬────┘  └────┬────┘  └────┬────┘   │         │      │
│  │    │       │            │            │          │         │      │
│  │    └───────┼────────────┼────────────┼──────────┘         │      │
│  │           │            │            │                   │      │
│  │    ┌──────▼────┐    ┌────▼────┐    ┌────▼────┐          │      │
│  │    │ ReadPool  │    │ReadPool  │    │ReadPool  │          │      │
│  │    │ (4线程)  │    │ (4线程)  │    │ (4线程)  │          │      │
│  │    └──────────┘    └──────────┘    └──────────┘          │      │
│  └───────────────────────────────────────────────────────────┘      │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

---

## 二、写队列设计（单线程）

### 2.1 写队列数据结构

```go
// write_queue.go
type WriteQueue struct {
    tasks      chan WriteTask     // 任务队列
    wg         sync.WaitGroup     // 等待组
    tree       *BTree             // BTree 引用
    config     *WriteQueueConfig  // 配置
    metrics    *WriteMetrics      // 指标统计
}

type WriteQueueConfig struct {
    QueueSize    int           // 队列大小（默认 1000）
    BatchSize    int           // 批量大小（默认 10）
    FlushTimeout time.Duration // 刷新超时（默认 10ms）
}

type WriteMetrics struct {
    TotalProcessed atomic.Uint64
    TotalFailed    atomic.Uint64
    AvgLatency      atomic.Int64  // ns
}
```

### 2.2 写任务类型

```go
// write_task.go
type WriteTask struct {
    ID        uint64          // 任务 ID
    OpType    WriteOpType     // 操作类型
    Key       []byte          // 键
    Value     []byte          // 值
    Result    chan error       // 结果通道
    Timestamp int64           // 提交时间
    Priority  TaskPriority    // 优先级（0-9）
}

type WriteOpType int

const (
    OpInsert WriteOpType = iota  // 插入
    OpDelete                     // 删除
    OpUpdate                     // 更新
    OpBatch                      // 批量操作
)

type TaskPriority int

const (
    PriorityLow    TaskPriority = 0
    PriorityNormal TaskPriority = 5
    PriorityHigh   TaskPriority = 9
)
```

### 2.3 写队列实现

```go
// write_queue.go
func NewWriteQueue(tree *BTree, config *WriteQueueConfig) *WriteQueue {
    wq := &WriteQueue{
        tasks:   make(chan WriteTask, config.QueueSize),
        tree:    tree,
        config:  config,
        metrics: &WriteMetrics{},
    }

    // ✅ 启动单个写线程（防止互相干扰）
    wq.wg.Add(1)
    go func() {
        defer wq.wg.Done()
        wq.writerLoop()
    }()

    return wq
}

// 写线程主循环
func (wq *WriteQueue) writerLoop() {
    for task := range wq.tasks {
        start := time.Now()

        // 执行写操作
        err := wq.executeWrite(task)

        // 更新指标
        latency := time.Since(start).Nanoseconds()
        wq.metrics.TotalProcessed.Add(1)
        if err != nil {
            wq.metrics.TotalFailed.Add(1)
        }
        wq.metrics.AvgLatency.Store(latency)

        // 返回结果
        task.Result <- err
    }
}

// 执行写操作
func (wq *WriteQueue) executeWrite(task WriteTask) error {
    switch task.OpType {
    case OpInsert:
        return wq.tree.insertInternal(task.Key, task.Value)

    case OpDelete:
        return wq.tree.deleteInternal(task.Key)

    case OpUpdate:
        return wq.tree.updateInternal(task.Key, task.Value)

    case OpBatch:
        // 批量操作优化
        return wq.executeBatch(task)

    default:
        return ErrUnknownOpType
    }
}

// 批量操作（优化写吞吐）
func (wq *WriteQueue) executeBatch(task WriteTask) error {
    batch := task.Value.([]BatchItem)

    for _, item := range batch {
        if err := wq.tree.insertInternal(item.Key, item.Value); err != nil {
            return err
        }
    }

    return nil
}

// 提交写任务
func (wq *WriteQueue) Submit(ctx context.Context, task WriteTask) error {
    task.Timestamp = time.Now().UnixMilli()
    task.Result = make(chan error, 1)

    select {
    case wq.tasks <- task:
        // 等待执行结果
        select {
        case err := <-task.Result:
            return err
        case <-time.After(wq.config.FlushTimeout):
            return ErrWriteTimeout
        case <-ctx.Done():
            return ctx.Err()
        }

    case <-ctx.Done():
        return ctx.Err()
    }
}
```

### 2.4 写队列优化

```go
// 写队列优化：批量提交

type BatchWriter struct {
    queue    chan WriteTask
    batchSize int
    timeout  time.Duration
}

func (bw *BatchWriter) batchLoop() {
    ticker := time.NewTicker(bw.timeout)
    defer ticker.Stop()

    batch := make([]WriteTask, 0, bw.batchSize)

    for {
        select {
        case task := <-bw.queue:
            batch = append(batch, task)

            // 达到批量大小，立即执行
            if len(batch) >= bw.batchSize {
                bw.executeBatch(batch)
                batch = batch[:0]
            }

        case <-ticker.C:
            // 超时，执行当前批次
            if len(batch) > 0 {
                bw.executeBatch(batch)
                batch = batch[:0]
            }
        }
    }
}
```

---

## 三、读池设计（4线程）

### 3.1 读池数据结构

```go
// read_pool.go
type ReadPool struct {
    pool    chan *Reader
    wg      sync.WaitGroup
    tree    *BTree
    pm      *PageManager
    config  *ReadPoolConfig
}

type ReadPoolConfig struct {
    NumReaders   int           // 读线程数量（默认 4）
    MaxQueueSize int           // 队列大小（默认 100）
}

type Reader struct {
    id        int
    tree      *BTree
    pm        *PageManager
    stopChan  chan struct{}
    metrics   *ReaderMetrics
}

type ReaderMetrics struct {
    TotalReads   atomic.Uint64
    CacheHits    atomic.Uint64
    AvgLatency    atomic.Int64  // ns
}
```

### 3.2 读池实现

```go
// read_pool.go
func NewReadPool(tree *BTree, pm *PageManager, config *ReadPoolConfig) *ReadPool {
    pool := &ReadPool{
        pool:   make(chan *Reader, config.NumReaders),
        tree:   tree,
        pm:     pm,
        config: config,
    }

    // ✅ 启动 4 个读线程
    for i := 0; i < config.NumReaders; i++ {
        pool.wg.Add(1)
        go func(id int) {
            defer pool.wg.Done()

            reader := &Reader{
                id:       id,
                tree:     tree,
                pm:       pm,
                stopChan: make(chan struct{}),
                metrics:  &ReaderMetrics{},
            }

            // 将 reader 放入池中
            pool.pool <- reader

            // 等待停止信号
            <-reader.stopChan
        }()
    }

    return pool
}

// 执行读操作（无锁，并发）
func (rp *ReadPool) Execute(ctx context.Context, fn func(*Reader) error) error {
    start := time.Now()

    select {
    case reader := <-rp.pool:
        defer func() {
            rp.pool <- reader

            // 更新指标
            latency := time.Since(start).Nanoseconds()
            reader.metrics.TotalReads.Add(1)
            reader.metrics.AvgLatency.Store(latency)
        }()

        return fn(reader)

    case <-ctx.Done():
        return ctx.Err()
    }
}

// 停止读池
func (rp *ReadPool) Stop() {
    close(rp.pool)

    // 通知所有 reader 停止
    for i := 0; i < rp.config.NumReaders; i++ {
        reader := <-rp.pool
        close(reader.stopChan)
        rp.wg.Done()
    }

    rp.wg.Wait()
}
```

### 3.3 Reader 实现

```go
// reader.go
func (r *Reader) Get(ctx context.Context, key []byte) ([]byte, error) {
    // 1. 读取版本化根（原子操作，无锁）✅
    rootInfo := r.tree.root.Load().(*RootInfo)

    // 2. 获取 Page（通过 PageManager）
    rootPage, err := r.pm.Get(rootInfo.RootPageID)
    if err != nil {
        return nil, err
    }
    defer rootPage.Unpin()

    // 3. 反序列化为 Node
    node, err := DeserializeNode(rootPage)
    if err != nil {
        return nil, err
    }

    // 4. 查找数据（只读，完全无锁）✅
    r.metrics.CacheHits.Add(1)
    return r.searchNode(node, key)
}

func (r *Reader) searchNode(node *Node, key []byte) ([]byte, error) {
    idx := node.Search(key)

    if node.IsLeaf {
        if idx < len(node.Keys) && bytes.Equal(node.Keys[idx], key) {
            return node.Values[idx], nil
        }
        return nil, ErrKeyNotFound
    }

    // 延迟加载子节点（无锁）
    childPageID := node.Children[idx]
    childPage, err := r.pm.Get(childPageID)
    if err != nil {
        return nil, err
    }
    defer childPage.Unpin()

    childNode, err := DeserializeNode(childPage)
    if err != nil {
        return nil, err
    }

    return r.searchNode(childNode, key)
}

// RangeScan（范围查询）
func (r *Reader) RangeScan(ctx context.Context, start, end []byte) (Iterator, error) {
    rootInfo := r.tree.root.Load().(*RootInfo)

    rootPage, err := r.pm.Get(rootInfo.RootPageID)
    if err != nil {
        return nil, err
    }

    node, err := DeserializeNode(rootPage)
    if err != nil {
        return nil, err
    }

    return NewBTreeIterator(r, node, start, end), nil
}
```

---

## 四、并发隔离机制

### 4.1 版本化根指针

```go
// root_info.go
type RootInfo struct {
    RootPageID model.PageID
    Version    uint64          // CCOW 版本号
    CreatedAt  int64          // 创建时间
    RefCount   atomic.Int32    // 引用计数（防止回收）
}

func (ri *RootInfo) Acquire() {
    ri.RefCount.Add(1)
}

func (ri *RootInfo) Release() {
    ri.RefCount.Add(-1)
}

func (ri *RootInfo) IsPinned() bool {
    return ri.RefCount.Load() > 0
}
```

### 4.2 CCOW 路径复制（写操作）

```go
// ccow.go - Copy-On-Write 路径复制

func (b *BTree) insertInternal(key, value []byte) error {
    // 1. 获取当前根
    rootInfo := b.root.Load().(*RootInfo)

    // 2. 查找路径（只读，无锁）
    path, err := b.FindPath(rootInfo.RootPageID, key)
    if err != nil {
        return err
    }
    defer b.releasePath(path)

    // 3. 复制路径并修改（从底向上）
    newRootPageID, err := b.CopyPathBottomUp(path, key, value)
    if err != nil {
        return err
    }

    // 4. 分配新 Page 并序列化
    newRootInfo := &RootInfo{
        RootPageID: newRootPageID,
        Version:    rootInfo.Version + 1,
        CreatedAt:  time.Now().UnixMilli(),
    }

    // 5. CAS 更新根指针（原子操作）✅
    if b.root.CompareAndSwap(rootInfo, newRootInfo) {
        // ✅ CAS 成功
        return nil
    } else {
        // ❌ CAS 失败，重试
        return ErrRetry
    }
}

func (b *BTree) FindPath(rootPageID model.PageID, key []byte) (Path, error) {
    path := make(Path, 0, 4)

    currentPageID := rootPageID

    for {
        // 获取 Page（从 PageManager）
        page, err := b.pageManager.Get(currentPageID)
        if err != nil {
            return nil, err
        }
        defer page.Unpin()

        // 反序列化
        node, err := DeserializeNode(page)
        if err != nil {
            return nil, err
        }

        // 添加到路径
        path = append(path, &PathNode{
            Page:    page,
            Node:    node,
            PageID:  currentPageID,
        })

        // 到达叶子节点
        if node.IsLeaf {
            break
        }

        // 继续向下
        idx := node.Search(key)
        currentPageID = node.Children[idx]
    }

    return path, nil
}

func (b *BTree) CopyPathBottomUp(path Path, key, value []byte) (model.PageID, error) {
    // 从底向上复制路径
    for i := len(path) - 1; i >= 0; i-- {
        pathNode := path[i]

        // 复制 Page
        newPage, err := b.pageManager.Allocate()
        if err != nil {
            return 0, err
        }

        // 修改 Page
        if i == len(path) - 1 {
            // 叶子节点：插入键值对
            node := pathNode.Node
            node.Insert(key, value)
        } else {
            // 内部节点：更新子节点引用
            // ...
        }

        // 序列化到 Page
        if err = SerializeNode(pathNode.Node, newPage); err != nil {
            return 0, err
        }

        // 标记脏页
        newPage.MarkDirty()

        // 更新父节点的子引用
        if i > 0 {
            parentPathNode := path[i-1]
            parentPathNode.Node.Children[idx] = newPage.ID
        }
    }

    // 返回新的根 PageID
    return path[0].NewPageID, nil
}
```

### 4.3 并发时间线分析

```
┌─────────────────────────────────────────────────────────────┐
│                   并发时间线（4读1写）                        │
├─────────────────────────────────────────────────────────────┤
│                                                                 │
│ T1: 写线程 CAS V1 → V2                                               │
│     读线程1-4: 读取 V1（旧版本，一致性快照）                          │
│                                                                 │
│ T2: 写线程 CAS V2 → V3                                               │
│     读线程1: 读取 V2（新版本）                                     │
│     读线程2-4: 读取 V1（旧版本，仍然一致）                           │
│                                                                 │
│ T3: 写线程 CAS V3 → V4                                               │
│     读线程1-2: 读取 V3（新版本）                                 │
│     读线程3-4: 读取 V2（旧版本，仍然一致）                           │
│                                                                 │
│ T4: 写线程 CAS V4 → V5                                               │
│     读线程1: 读取 V4（新版本）                                     │
│     读线程2-3: 读取 V3（旧版本）                                 │
│     读线程4: 读取 V2（旧版本）                                   │
│                                                                 │
└─────────────────────────────────────────────────────────────┘
```

**关键机制**:
- ✅ **写操作**: CAS 原子更新根指针（单线程，无冲突）
- ✅ **读操作**: 读取版本化根（无锁，看到一致性快照）
- ✅ **隔离**: CCOW 路径复制，读写互不阻塞
- ✅ **引用计数**: 旧版本读完后自动回收

---

## 五、性能优化策略

### 5.1 写队列优化

```go
// 批量写入优化
func (wq *WriteQueue) BatchSubmit(tasks []WriteTask) error {
    // 按优先级排序
    sort.Slice(tasks, func(i, j int) bool {
        return tasks[i].Priority > tasks[j].Priority
    })

    // 提交到队列
    for _, task := range tasks {
        if err := wq.Submit(context.Background(), task); err != nil {
            return err
        }
    }

    return nil
}
```

### 5.2 读池优化

```go
// 读线程本地缓存（减少 PageManager 争用）
type Reader struct {
    localCache map[model.PageID]*Node  // 本地缓存
    ttl        time.Duration              // 缓存过期时间
}

func (r *Reader) Get(key []byte) ([]byte, error) {
    // 1. 尝试本地缓存
    if node, ok := r.localCache[key]; ok {
        return node.Value, nil
    }

    // 2. 从 PageManager 获取
    value, err := r.pm.Get(key)
    if err != nil {
        return nil, err
    }

    // 3. 更新本地缓存
    r.localCache[key] = value

    return value, nil
}
```

### 5.3 并发指标监控

```go
// metrics.go
type ConcurrencyMetrics struct {
    // 写队列指标
    WriteQueueDepth   atomic.Int64
    WriteAvgLatency  atomic.Int64
    WriteThroughput   atomic.Int64

    // 读池指标
    ReadPoolUtilization atomic.Int64  // 0-100%
    ReadAvgLatency    atomic.Int64
    ReadThroughput     atomic.Int64

    // 缓存指标
    L1CacheHitRate    atomic.Int64  // 0-100
    L2CacheHitRate    atomic.Int64  // 0-100
    L3CacheHitRate    atomic.Int64  // 0-100
}

func (m *ConcurrencyMetrics) Report() string {
    return fmt.Sprintf(`
写队列:
  - 队列深度: %d
  - 平均延迟: %d ns
  - 吞吐量: %d ops/s

读池:
  - 利用率: %d%%
  - 平均延迟: %d ns
  - 吞吐量: %d ops/s

缓存:
  - L1 命中率: %d%%
  - L2 命中率: %d%%
  - L3 命中率: %d%%
`,
        m.WriteQueueDepth.Load(),
        m.WriteAvgLatency.Load(),
        m.WriteThroughput.Load(),
        m.ReadPoolUtilization.Load(),
        m.ReadAvgLatency.Load(),
        m.ReadThroughput.Load(),
        m.L1CacheHitRate.Load(),
        m.L2CacheHitRate.Load(),
        m.L3CacheHitRate.Load(),
    )
}
```

---

## 六、实施细节

### 6.1 写队列配置

```go
// config.go
type WriteQueueConfig struct {
    QueueSize    int           // 队列大小（默认 1000）
    BatchSize    int           // 批量大小（默认 10）
    FlushTimeout time.Duration // 刷新超时（默认 10ms）
}

func DefaultWriteQueueConfig() *WriteQueueConfig {
    return &WriteQueueConfig{
        QueueSize:    1000,
        BatchSize:    10,
        FlushTimeout: 10 * time.Millisecond,
    }
}
```

### 6.2 读池配置

```go
// config.go
type ReadPoolConfig struct {
    NumReaders    int           // 读线程数量（默认 4）
    MaxQueueSize  int           // 队列大小（默认 100）
    LocalCacheTTL time.Duration // 本地缓存过期（默认 1s）
}

func DefaultReadPoolConfig() *ReadPoolConfig {
    return &ReadPoolConfig{
        NumReaders:    4,
        MaxQueueSize:  100,
        LocalCacheTTL: 1 * time.Second,
    }
}
```

### 6.3 集成示例

```go
// main.go - 完整集成示例

func NewBTree(config *BTreeConfig) (*BTree, error) {
    // 1. 创建 PageManager
    pm, err := NewPageManager(config, nil)
    if err != nil {
        return nil, err
    }

    // 2. 创建 BTree
    tree := &BTree{
        pm:     pm,
        root:   atomic.Value{},
        config: config,
    }

    // 3. 初始化根节点
    if err := tree.init(); err != nil {
        return nil, err
    }

    // 4. 创建写队列（单线程）
    writeQueue := NewWriteQueue(tree, DefaultWriteQueueConfig())

    // 5. 创建读池（4线程）
    readPool := NewReadPool(tree, pm, DefaultReadPoolConfig())

    tree.writeQueue = writeQueue
    tree.readPool = readPool

    return tree, nil
}

// 写操作（通过队列）
func (b *BTree) Insert(ctx context.Context, key, value []byte) error {
    task := WriteTask{
        OpType:   OpInsert,
        Key:      key,
        Value:    value,
        Priority: PriorityNormal,
    }

    return b.writeQueue.Submit(ctx, task)
}

// 读操作（通过读池）
func (b *BTree) Get(ctx context.Context, key []byte) ([]byte, error) {
    return b.readPool.Execute(ctx, func(reader *Reader) error {
        return reader.Get(ctx, key)
    })
}
```

---

## 七、性能目标

### 7.1 并发性能目标

| 指标 | 单线程基线 | 4读1写 | 提升 |
|------|-----------|--------|------|
| **读吞吐** | 3.33M ops/s | 13.32M ops/s | **4x** ✅ |
| **写吞吐** | 1M ops/s | 1M ops/s | **1x** ✅ |
| **总吞吐** | 4.33M ops/s | 14.32M ops/s | **3.3x** ✅ |
| **读延迟** | 300 ns | 300 ns | **稳定** ✅ |
| **写延迟** | 1000 ns | 1000 ns | **稳定** ✅ |

### 7.2 与 Lealone 对比

```
Lealone (4读1写):
  - 读吞吐: 1.07M x 4 = 4.28M ops/s
  - 写吞吐: 0.67M ops/s
  - 总吞吐: 4.95M ops/s

NexKV (4读1写):
  - 读吞吐: 3.33M x 4 = 13.32M ops/s
  - 写吞吐: 1M ops/s
  - 总吞吐: 14.32M ops/s

性能提升: 2.9x ✅
```

---

## 八、监控和调试

### 8.1 关键指标

```go
type ConcurrencyMonitor struct {
    writeQueue *WriteQueue
    readPool   *ReadPool
    metrics    *ConcurrencyMetrics
}

func (m *ConcurrencyMonitor) Start() {
    go m.monitorWriteQueue()
    go m.monitorReadPool()
    go m.monitorCache()
}

func (m *ConcurrencyMonitor) monitorWriteQueue() {
    ticker := time.NewTicker(time.Second)
    defer ticker.Stop()

    for range ticker.C {
        // 监控写队列深度
        depth := len(m.writeQueue.tasks)
        m.metrics.WriteQueueDepth.Store(int64(depth))

        // 监控写吞吐
        m.metrics.WriteThroughput.Store(m.writeQueue.metrics.TotalProcessed.Load())
    }
}

func (m *ConcurrencyMonitor) monitorReadPool() {
    ticker := time.NewTicker(time.Second)
    defer ticker.Stop()

    for range ticker.C {
        // 监控读池利用率
        utilization := len(m.readPool.pool) / cap(m.readPool.pool) * 100
        m.metrics.ReadPoolUtilization.Store(int64(utilization))

        // 监控读吞吐
        m.metrics.ReadThroughput.Store(m.readPool.metrics.TotalReads.Load())
    }
}

func (m *ConcurrencyMonitor) monitorCache() {
    ticker := time.NewTicker(time.Second)
    defer ticker.Stop()

    for range ticker.C {
        // 监控缓存命中率
        l1Hits := m.pageManager.l1Cache.metrics.Hits.Load()
        l1Total := m.pageManager.l1Cache.metrics.Total.Load()
        if l1Total > 0 {
            hitRate := l1Hits * 100 / l1Total
            m.metrics.L1CacheHitRate.Store(hitRate)
        }
    }
}
```

---

## 九、总结

### 9.1 核心设计

1. **单写线程**:
   - ✅ 避免 CAS 冲突
   - ✅ 避免路径复制竞争
   - ✅ 简化并发控制

2. **多读线程**:
   - ✅ 完全无锁（版本化根指针）
   - ✅ 线性扩展（4x 吞吐量）
   - ✅ 不阻塞写操作（CCOW 隔离）

3. **读写隔离**:
   - ✅ CCOW 机制：路径复制，读写互不阻塞
   - ✅ 版本化根：快照隔离
   - ✅ 引用计数：防止 Page 被提前回收

### 9.2 性能收益

```
vs 单线程:
  - 吞吐量: 3.3x 提升
  - 读延迟: 稳定
  - 写延迟: 稳定

vs Lealone:
  - 吞吐量: 2.9x 提升
  - 读延迟: 快 1.6-3.1x
  - 写延迟: 快 1.1-2.0x
```

### 9.3 实施建议

```
Phase 2: CCOW 机制实现（包含并发模型）
  ├─ 2.1 版本化根指针
  ├─ 2.2 CCOW 路径复制
  ├─ 2.3 写队列实现（单线程）
  ├─ 2.4 读池实现（4线程）
  └─ 2.5 并发测试（4读1写压力测试）
```

---

**文档生成**: 2026-03-09
**状态**: 详细设计文档
**版本**: v1.0
