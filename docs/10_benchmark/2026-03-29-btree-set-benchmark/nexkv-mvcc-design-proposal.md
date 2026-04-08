# NexKV 完整 MVCC 设计方案

## 1. 设计目标

基于 Lealone 的 MVCC 机制，为 NexKV 设计完整的 MVCC 支持，实现：
- **读不阻塞写**：读操作看到快照，写操作创建新版本
- **事务隔离**：支持 Snapshot Isolation 或 Serializable
- **原子性**：多页修改原子提交
- **版本回收**：自动清理过期版本

## 2. 核心架构

### 2.1 整体架构图

```
┌─────────────────────────────────────────────────────────────┐
│                      Transaction Manager                     │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────┐  │
│  │ Begin Txn   │  │ Commit Txn  │  │ Rollback Txn        │  │
│  │ (分配版本号) │  │ (原子提交)   │  │ (回滚变更)          │  │
│  └─────────────┘  └─────────────┘  └─────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                      Version Manager                         │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────┐  │
│  │ Global      │  │ Active      │  │ Version             │  │
│  │ Version     │  │ Transactions│  │ Chain               │  │
│  │ (单调递增)   │  │ (快照集合)   │  │ (页面版本链)         │  │
│  └─────────────┘  └─────────────┘  └─────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                      Storage Layer                           │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────┐  │
│  │ Base Page   │  │ Delta       │  │ Version             │  │
│  │ (基础页面)   │  │ Chain       │  │ GC                  │  │
│  │             │  │ (增量变更)   │  │ (版本回收)          │  │
│  └─────────────┘  └─────────────┘  └─────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

### 2.2 核心数据结构

#### 2.2.1 Transaction 结构

```go
// Transaction 事务结构
type Transaction struct {
    txnID       uint64           // 事务唯一ID
    startVersion uint64          // 事务开始时的全局版本号（快照）
    commitVersion uint64         // 提交版本号（提交时分配）
    state       TxnState         // 事务状态
    
    // 写集合
    writeSet    map[PageID]*PageDelta  // 修改的页面
    
    // 读集合（用于冲突检测）
    readSet     map[PageID]uint64      // pageID -> version
    
    // 新分配的页面（需要回滚时释放）
    allocatedPages []PageID
    
    // 锁（用于并发控制）
    mu          sync.RWMutex
}

type TxnState int

const (
    TxnActive TxnState = iota     // 活跃状态
    TxnCommitting                 // 提交中
    TxnCommitted                  // 已提交
    TxnAborted                    // 已回滚
)
```

#### 2.2.2 Version Manager 结构

```go
// VersionManager 版本管理器
type VersionManager struct {
    // 全局版本号（单调递增）
    globalVersion atomic.Uint64
    
    // 活跃事务集合
    activeTxns    sync.Map  // txnID -> *Transaction
    
    // 已提交事务的版本号（用于快照读）
    committedVersions []uint64
    
    // 版本链管理
    versionChains   map[PageID]*VersionChain
    
    // GC 相关
    gcWatermark     uint64   // GC 水位线，低于此版本可以回收
    gcInterval      time.Duration
}

// VersionChain 页面版本链
type VersionChain struct {
    pageID      PageID
    basePage    *BasePage        // 基础页面（最老版本）
    latestDelta atomic.Pointer[PageDelta]  // 最新增量
    mu          sync.RWMutex
}

// PageDelta 页面增量
type PageDelta struct {
    version     uint64           // 版本号
    txnID       uint64           // 创建此版本的事务ID
    changes     map[string][]byte  // key -> value（nil表示删除）
    prevDelta   atomic.Pointer[PageDelta]  // 上一个版本
    commitTime  time.Time
    
    // 用于GC
    refCount    atomic.Int32     // 引用计数
}

// BasePage 基础页面
type BasePage struct {
    pageID      PageID
    version     uint64           // 基础版本号
    data        []byte           // 序列化的页面数据
    createTime  time.Time
}
```

#### 2.2.3 MVCC Page 结构

```go
// MVCCPage MVCC 页面包装器
type MVCCPage struct {
    pageID      PageID
    
    // 版本链
    versionChain *VersionChain
    
    // 当前可见版本（缓存）
    visibleVersion atomic.Uint64
    visibleData    atomic.Value  // []byte
    
    // 锁
    mu          sync.RWMutex
}

// Read 读取指定版本的值
func (p *MVCCPage) Read(key []byte, readVersion uint64) ([]byte, bool) {
    // 1. 检查缓存
    if p.visibleVersion.Load() == readVersion {
        if data := p.visibleData.Load(); data != nil {
            return p.readFromData(data.([]byte), key)
        }
    }
    
    // 2. 沿着版本链查找
    p.mu.RLock()
    defer p.mu.RUnlock()
    
    // 从最新版本开始回溯
    result := p.readFromChain(key, readVersion)
    
    // 3. 更新缓存
    if result.found {
        p.visibleVersion.Store(readVersion)
        p.visibleData.Store(result.data)
    }
    
    return result.value, result.found
}

// Write 写入（创建新版本）
func (p *MVCCPage) Write(key, value []byte, txn *Transaction) error {
    p.mu.Lock()
    defer p.mu.Unlock()
    
    // 检查写冲突
    if err := p.checkWriteConflict(key, txn); err != nil {
        return err
    }
    
    // 创建新的 delta
    delta := &PageDelta{
        version:    txn.commitVersion,
        txnID:      txn.txnID,
        changes:    map[string][]byte{string(key): value},
        commitTime: time.Now(),
    }
    
    // 链接到版本链
    delta.prevDelta.Store(p.versionChain.latestDelta.Load())
    p.versionChain.latestDelta.Store(delta)
    
    // 记录到事务的写集合
    txn.writeSet[p.pageID] = delta
    
    return nil
}
```

## 3. 核心流程

### 3.1 事务开始

```go
func (vm *VersionManager) BeginTransaction() *Transaction {
    txn := &Transaction{
        txnID:        vm.generateTxnID(),
        startVersion: vm.globalVersion.Load(),  // 获取当前全局版本作为快照
        state:        TxnActive,
        writeSet:     make(map[PageID]*PageDelta),
        readSet:      make(map[PageID]uint64),
    }
    
    // 注册到活跃事务集合
    vm.activeTxns.Store(txn.txnID, txn)
    
    return txn
}
```

### 3.2 读操作（快照读）

```go
func (txn *Transaction) Read(pageID PageID, key []byte) ([]byte, bool, error) {
    // 1. 检查写集合（读自己未提交的修改）
    if delta, ok := txn.writeSet[pageID]; ok {
        if value, ok := delta.changes[string(key)]; ok {
            return value, value != nil, nil  // nil 表示删除
        }
    }
    
    // 2. 检查读集合（已读过的页面）
    if version, ok := txn.readSet[pageID]; ok {
        // 使用缓存的版本号读取
        return vm.readPageVersion(pageID, key, version)
    }
    
    // 3. 沿着版本链查找合适的版本
    // 找到最大的 version <= txn.startVersion
    version, value, found := vm.findVisibleVersion(pageID, key, txn.startVersion)
    
    // 4. 记录到读集合
    txn.readSet[pageID] = version
    
    return value, found, nil
}

// findVisibleVersion 查找对指定版本可见的数据
func (vm *VersionManager) findVisibleVersion(
    pageID PageID, 
    key []byte, 
    readVersion uint64,
) (uint64, []byte, bool) {
    chain := vm.versionChains[pageID]
    
    // 从最新版本开始回溯
    for delta := chain.latestDelta.Load(); delta != nil; delta = delta.prevDelta.Load() {
        if delta.version <= readVersion {
            // 此版本对读可见
            if value, ok := delta.changes[string(key)]; ok {
                return delta.version, value, value != nil
            }
        }
    }
    
    // 在 base page 中查找
    if chain.basePage != nil {
        value, found := chain.basePage.Read(key)
        return chain.basePage.version, value, found
    }
    
    return 0, nil, false
}
```

### 3.3 写操作

```go
func (txn *Transaction) Write(pageID PageID, key, value []byte) error {
    if txn.state != TxnActive {
        return errors.New("transaction not active")
    }
    
    // 1. 获取页面锁（防止并发修改）
    page := vm.getOrCreateMVCCPage(pageID)
    
    // 2. 检查写冲突（WW冲突）
    if err := txn.checkWriteConflict(pageID, key); err != nil {
        return err  // 冲突，需要回滚或重试
    }
    
    // 3. 创建新的 delta（不立即提交）
    delta := &PageDelta{
        version:    0,  // 提交时分配
        txnID:      txn.txnID,
        changes:    map[string][]byte{string(key): value},
        commitTime: time.Now(),
    }
    
    // 4. 记录到写集合
    txn.writeSet[pageID] = delta
    
    return nil
}

// checkWriteConflict 检查写冲突
func (txn *Transaction) checkWriteConflict(pageID PageID, key []byte) error {
    chain := vm.versionChains[pageID]
    
    // 检查是否有其他事务在 startVersion 之后修改了相同的 key
    for delta := chain.latestDelta.Load(); delta != nil; delta = delta.prevDelta.Load() {
        if delta.version > txn.startVersion {
            // 有其他事务在此事务开始后提交了
            if _, ok := delta.changes[string(key)]; ok {
                // WW 冲突
                return ErrWriteConflict
            }
        } else {
            // 已经到达此事务开始前的版本，停止检查
            break
        }
    }
    
    return nil
}
```

### 3.4 事务提交（两阶段提交）

```go
func (txn *Transaction) Commit() error {
    if txn.state != TxnActive {
        return errors.New("transaction not active")
    }
    
    txn.state = TxnCommitting
    
    // 阶段 1: 预提交（分配版本号，验证冲突）
    commitVersion := vm.globalVersion.Add(1)
    txn.commitVersion = commitVersion
    
    // 验证所有写操作没有冲突
    for pageID, delta := range txn.writeSet {
        if err := txn.validateWrite(pageID, delta); err != nil {
            txn.Rollback()
            return err
        }
    }
    
    // 阶段 2: 原子提交
    // 使用 WAL 保证原子性
    if err := vm.wal.LogCommit(txn); err != nil {
        txn.Rollback()
        return err
    }
    
    // 更新版本链
    for pageID, delta := range txn.writeSet {
        delta.version = commitVersion
        vm.installDelta(pageID, delta)
    }
    
    // 更新全局状态
    txn.state = TxnCommitted
    vm.committedVersions = append(vm.committedVersions, commitVersion)
    vm.activeTxns.Delete(txn.txnID)
    
    return nil
}

// installDelta 将 delta 安装到版本链
func (vm *VersionManager) installDelta(pageID PageID, delta *PageDelta) {
    chain := vm.versionChains[pageID]
    
    // 原子地链接到版本链
    for {
        oldLatest := chain.latestDelta.Load()
        delta.prevDelta.Store(oldLatest)
        
        if chain.latestDelta.CompareAndSwap(oldLatest, delta) {
            break  // 成功
        }
        // CAS 失败，重试
    }
}
```

### 3.5 事务回滚

```go
func (txn *Transaction) Rollback() error {
    if txn.state != TxnActive && txn.state != TxnCommitting {
        return nil  // 已经提交或回滚
    }
    
    txn.state = TxnAborted
    
    // 1. 释放新分配的页面
    for _, pageID := range txn.allocatedPages {
        vm.pageManager.Free(pageID)
    }
    
    // 2. 清理写集合（这些 delta 不会被安装到版本链）
    txn.writeSet = nil
    
    // 3. 从活跃事务集合移除
    vm.activeTxns.Delete(txn.txnID)
    
    return nil
}
```

## 4. 版本回收（GC）

### 4.1 GC 策略

```go
// VersionGC 版本垃圾回收
type VersionGC struct {
    vm              *VersionManager
    watermark       uint64  // 低于此版本的可以回收
    retentionTime   time.Duration  // 保留时间
}

// RunGC 执行版本回收
func (gc *VersionGC) RunGC() {
    // 1. 计算 GC 水位线
    gc.calculateWatermark()
    
    // 2. 遍历所有版本链，回收过期版本
    for pageID, chain := range gc.vm.versionChains {
        gc.collectPageVersions(pageID, chain)
    }
}

// calculateWatermark 计算 GC 水位线
func (gc *VersionGC) calculateWatermark() {
    // 找到最老的活跃事务的版本号
    minActiveVersion := gc.vm.globalVersion.Load()
    
    gc.vm.activeTxns.Range(func(key, value interface{}) bool {
        txn := value.(*Transaction)
        if txn.startVersion < minActiveVersion {
            minActiveVersion = txn.startVersion
        }
        return true
    })
    
    // 水位线 = 最老活跃事务的版本 - 1
    gc.watermark = minActiveVersion - 1
}

// collectPageVersions 回收页面的过期版本
func (gc *VersionGC) collectPageVersions(pageID PageID, chain *VersionChain) {
    // 保留策略：
    // 1. 保留所有 >= watermark 的版本
    // 2. 保留最近 N 个版本（即使 < watermark）
    // 3. 合并旧的 deltas 到 base page
    
    var toMerge []*PageDelta
    
    for delta := chain.latestDelta.Load(); delta != nil; {
        if delta.version <= gc.watermark && delta.refCount.Load() == 0 {
            // 可以回收
            toMerge = append(toMerge, delta)
        }
        delta = delta.prevDelta.Load()
    }
    
    if len(toMerge) > 10 {  // 积累足够多再合并
        gc.mergeDeltasToBasePage(chain, toMerge)
    }
}

// mergeDeltasToBasePage 将 deltas 合并到 base page
func (gc *VersionGC) mergeDeltasToBasePage(chain *VersionChain, deltas []*PageDelta) {
    // 创建新的 base page
    newBase := &BasePage{
        pageID:  chain.pageID,
        version: deltas[len(deltas)-1].version,  // 最老的版本
        data:    gc.applyDeltas(chain.basePage, deltas),
    }
    
    // 原子替换
    chain.basePage = newBase
    
    // 释放旧的 deltas
    for _, delta := range deltas {
        gc.freeDelta(delta)
    }
}
```

### 4.2 增量合并优化

```go
// IncrementalMerge 增量合并
type IncrementalMerge struct {
    threshold int  // 触发合并的 delta 数量阈值
}

// MaybeMerge 检查是否需要合并
func (im *IncrementalMerge) MaybeMerge(chain *VersionChain) {
    count := 0
    for delta := chain.latestDelta.Load(); delta != nil; delta = delta.prevDelta.Load() {
        count++
        if count > im.threshold {
            // 触发后台合并
            go im.mergeInBackground(chain)
            break
        }
    }
}
```

## 5. 与现有架构集成

### 5.1 兼容性设计

```go
// MVCCBTree MVCC 版本的 BTree
type MVCCBTree struct {
    *BTree  // 嵌入现有 BTree
    
    vm      *VersionManager
    txnPool sync.Pool
}

// Set 兼容现有接口，自动包装为事务
func (bt *MVCCBTree) Set(ctx context.Context, key, value []byte) error {
    // 自动开始事务
    txn := bt.vm.BeginTransaction()
    defer func() {
        if r := recover(); r != nil {
            txn.Rollback()
            panic(r)
        }
    }()
    
    // 执行写操作
    if err := bt.setInTxn(txn, key, value); err != nil {
        txn.Rollback()
        return err
    }
    
    // 提交事务
    return txn.Commit()
}

// SetInTransaction 在指定事务中执行
func (bt *MVCCBTree) SetInTransaction(txn *Transaction, key, value []byte) error {
    return bt.setInTxn(txn, key, value)
}
```

### 5.2 渐进式迁移

```go
// MigrationPlan 迁移计划
type MigrationPlan struct {
    Phase1_EnableMVCCForReads    // 只读操作使用 MVCC
    Phase2_EnableMVCCForWrites   // 写操作使用 MVCC
    Phase3_EnableTransactions    // 完整事务支持
    Phase4_OptimizeAndCleanup    // 优化和清理旧代码
}
```

## 6. 性能优化

### 6.1 读路径优化

```go
// ReadCache 读缓存
type ReadCache struct {
    lru       *LRUCache
    hitRate   atomic.Float64
}

// 缓存热点页面的可见版本
func (rc *ReadCache) Get(pageID PageID, version uint64) ([]byte, bool) {
    key := cacheKey{pageID, version}
    return rc.lru.Get(key)
}
```

### 6.2 写路径优化

```go
// BatchWrite 批量写入
func (txn *Transaction) BatchWrite(writes []WriteOp) error {
    // 批量检查冲突
    // 批量分配版本号
    // 批量提交
}
```

### 6.3 内存优化

```go
// DeltaCompression Delta 压缩
type DeltaCompression struct {
    // 对 changes 进行压缩
    // 使用字典编码或前缀压缩
}
```

## 7. 正确性保证

### 7.1 隔离级别

```go
// IsolationLevel 隔离级别
type IsolationLevel int

const (
    ReadUncommitted IsolationLevel = iota
    ReadCommitted
    SnapshotIsolation
    Serializable
)

// 根据隔离级别调整冲突检测策略
func (txn *Transaction) checkConflictByLevel(level IsolationLevel) error {
    switch level {
    case SnapshotIsolation:
        // 只检测 WW 冲突
        return txn.checkWriteWriteConflict()
    case Serializable:
        // 检测 RW 和 WW 冲突
        if err := txn.checkReadWriteConflict(); err != nil {
            return err
        }
        return txn.checkWriteWriteConflict()
    }
    return nil
}
```

### 7.2 持久化保证

```go
// WAL 集成
type MVCCWAL struct {
    wal wal.WAL
}

// LogTransaction 记录事务日志
func (w *MVCCWAL) LogTransaction(txn *Transaction) error {
    // 记录事务开始
    // 记录所有写操作
    // 记录提交或回滚
}
```

## 8. 实施计划

### Phase 1: 基础框架（2 周）
- [ ] 实现 VersionManager 和 Transaction 结构
- [ ] 实现版本链（VersionChain 和 PageDelta）
- [ ] 实现基本的事务开始/提交/回滚
- [ ] 单元测试

### Phase 2: 读写路径（2 周）
- [ ] 实现快照读
- [ ] 实现写操作和冲突检测
- [ ] 实现 WAL 集成
- [ ] 集成测试

### Phase 3: GC 和优化（2 周）
- [ ] 实现版本回收（GC）
- [ ] 实现增量合并
- [ ] 性能优化（缓存、批量操作）
- [ ] 性能测试

### Phase 4: 集成和迁移（2 周）
- [ ] 与现有 BTree 集成
- [ ] 渐进式迁移
- [ ] 生产环境验证
- [ ] 文档和培训

## 9. 风险评估

| 风险 | 概率 | 影响 | 缓解措施 |
|------|------|------|----------|
| 性能回归 | 中 | 高 | 充分测试，保留旧代码作为 fallback |
| 内存爆炸 | 中 | 高 | 严格的 GC 策略，版本数限制 |
| 死锁 | 低 | 高 | 锁顺序规范，超时检测 |
| 数据不一致 | 低 | 极高 | 形式化验证，混沌测试 |

## 10. 总结

NexKV 的完整 MVCC 方案基于以下核心设计：

1. **版本链**：每个页面维护一个版本链，支持多版本并发访问
2. **快照读**：事务看到开始时的快照，不受并发写影响
3. **乐观并发控制**：提交时检测冲突，失败则回滚
4. **增量存储**：使用 delta 存储变更，减少内存开销
5. **后台 GC**：自动回收过期版本，防止内存爆炸

该方案借鉴了 Lealone 的 MVCC 设计，但针对 NexKV 的 Off-Heap 架构进行了优化，实现了读不阻塞写、事务隔离和原子提交。
