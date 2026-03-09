# Delta Chain 优化实施计划

**日期**: 2026-03-09
**目标**: 通过 Delta Chain 优化实现 600K+ ops/s 写性能
**预计时间**: 2-3 周
**优先级**: 🔴 P0 (关键优化)

---

## 📊 性能目标

### 当前性能

| 指标 | 当前值 | Lealone | 差距 |
|------|--------|---------|------|
| 写吞吐 | 275K ops/s | 669K ops/s | **2.4x** |
| 写延迟 | 3633 ns/op | 1596 ns/op | **2.3x** |
| 写放大 | 3-5x (估计) | 1.1-1.5x | **2-3x** |

### 优化目标

| 指标 | 当前值 | 目标值 | 提升 |
|------|--------|--------|------|
| 写吞吐 | 275K ops/s | **600K+ ops/s** | **2.2x** |
| 写延迟 | 3633 ns/op | **< 1800 ns/op** | **2x** |
| 写放大 | 3-5x | **1.1-1.5x** | **2-3x** |

---

## 🎯 核心原理

### Delta Chain 工作流程

```
传统写入 (当前):
Insert(key, value)
  ↓
CopyPathBottomUp (复制完整路径)
  ↓
ModifyPage (序列化到磁盘)
  ↓
每次写入都写磁盘 ❌

Delta Chain 优化:
Insert(key, value)
  ↓
检查 Delta Chain
  ↓
写入 Delta Chain (内存) ✅
  ↓
[达到阈值] → 批量合并到磁盘
  ↓
减少磁盘写入 2-3x ✅
```

### 写放大对比

```
场景：100 次插入

传统实现 (当前):
├── 100 × CopyPathBottomUp (每次复制路径)
├── 100 × ModifyPage (每次序列化)
└── 写放大：~3-5x

Delta Chain 优化:
├── 100 × Delta Chain 写入 (内存，极快)
├── 1 × 批量合并 (磁盘)
└── 写放大：1.1-1.5x ✅
```

---

## 📐 架构设计

### 数据结构

```go
// delta_chain.go

// DeltaType Delta 操作类型
type DeltaType int

const (
    DeltaInsert DeltaType = iota
    DeltaUpdate
    DeltaDelete
)

// DeltaEntry Delta 记录
type DeltaEntry struct {
    Key     []byte
    Value   []byte  // Delete 时为 nil
    Type    DeltaType
    Version uint64
}

// DeltaChain Delta 链（内存缓冲区）
type DeltaChain struct {
    mu         sync.RWMutex
    entries    []DeltaEntry
    basePage   *Page           // 基础页面
    pageManager *PageManager
    threshold  int             // 合并阈值（默认 100）
    lastMerge  time.Time
    mergeTimer *time.Timer
    stats      DeltaChainStats
}

// DeltaChainStats 统计信息
type DeltaChainStats struct {
    TotalWrites    uint64
    TotalMerges    uint64
    TotalEntries   uint64
    AvgMergeSize   float64
    LastMergeTime  time.Time
}
```

### 关键接口

```go
// Write 写入 Delta Chain（不立即写磁盘）
func (dc *DeltaChain) Write(key, value []byte, op DeltaType) error

// Get 查找键（先查 Delta Chain，再查基础页面）
func (dc *DeltaChain) Get(key []byte) ([]byte, bool, error)

// Merge 合并 Delta Chain 到基础页面
func (dc *DeltaChain) Merge() error

// ShouldMerge 判断是否应该触发合并
func (dc *DeltaChain) ShouldMerge() bool

// Flush 强制刷新到磁盘
func (dc *DeltaChain) Flush() error
```

---

## 🔧 实施计划

### Week 1: 核心实现 (5 天)

#### Day 1-2: Delta Chain 数据结构

**文件**：
- `internal/infrastructure/storage/btree/delta_chain.go`
- `internal/infrastructure/storage/btree/delta_chain_test.go`

**任务**：
- [ ] 实现 `DeltaEntry` 结构
- [ ] 实现 `DeltaChain` 结构
- [ ] 实现 `Write()` 方法
- [ ] 实现 `Get()` 方法（优先查 Delta）
- [ ] 单元测试

**测试**：
```go
func TestDeltaChain_WriteAndGet(t *testing.T)
func TestDeltaChain_Delete(t *testing.T)
func TestDeltaChain_Override(t *testing.T)
func TestDeltaChain_Full(t *testing.T)
```

#### Day 3-4: 合并逻辑

**任务**：
- [ ] 实现 `Merge()` 方法
- [ ] 实现 `ShouldMerge()` 判断逻辑
- [ ] 实现阈值触发（默认 100）
- [ ] 实现定时触发（默认 1 秒）
- [ ] 集成测试

**测试**：
```go
func TestDeltaChain_Merge(t *testing.T)
func TestDeltaChain_ThresholdTrigger(t *testing.T)
func TestDeltaChain_TimerTrigger(t *testing.T)
func TestDeltaChain_MergeCorrectness(t *testing.T)
```

#### Day 5: 与 BTree 集成

**任务**：
- [ ] 修改 `BTree` 结构，添加 `deltaChain` 字段
- [ ] 修改 `Insert()` 方法，使用 Delta Chain
- [ ] 修改 `Search()` 方法，先查 Delta Chain
- [ ] 修改 `ModifyPage()`，延迟写入
- [ ] 集成测试

**关键代码**：
```go
// btree.go
type BTree struct {
    // ... 现有字段 ...
    deltaChains map[PageID]*DeltaChain  // 页面 → Delta Chain
    deltaMu     sync.RWMutex
}

func (b *BTree) Insert(key, value []byte) error {
    // 1. FindPath
    path, err := b.FindPath(key)
    if err != nil {
        return err
    }

    // 2. 获取或创建 Delta Chain
    leafPageID := path[len(path)-1].PageID
    deltaChain := b.getOrCreateDeltaChain(leafPageID)

    // 3. 写入 Delta Chain（内存，极快）
    if err := deltaChain.Write(key, value, DeltaInsert); err != nil {
        return err
    }

    // 4. 检查是否需要合并
    if deltaChain.ShouldMerge() {
        go deltaChain.Merge()  // 异步合并
    }

    return nil
}
```

---

### Week 2: 优化和测试 (5 天)

#### Day 1-2: 性能优化

**任务**：
- [ ] Delta Chain 容量预分配
- [ ] 减少内存分配（sync.Pool）
- [ ] 批量合并优化
- [ ] 异步合并机制

**优化点**：
```go
// 预分配 entries
func NewDeltaChain(basePage *Page) *DeltaChain {
    return &DeltaChain{
        entries:   make([]DeltaEntry, 0, 100),  // 预分配
        threshold: 100,
        basePage:  basePage,
    }
}

// 使用 sync.Pool
var deltaEntryPool = sync.Pool{
    New: func() any {
        return &DeltaEntry{}
    },
}
```

#### Day 3-4: 完整测试

**任务**：
- [ ] 端到端测试
- [ ] 压力测试（1M ops）
- [ ] 并发测试（race detector）
- [ ] 崩溃恢复测试

**测试场景**：
```go
// 压力测试
func BenchmarkBTree_Insert_WithDeltaChain(b *testing.B)
func BenchmarkBTree_Insert_WithoutDeltaChain(b *testing.B)

// 并发测试
func TestDeltaChain_ConcurrentWrites(t *testing.T)

// 崩溃恢复
func TestDeltaChain_CrashRecovery(t *testing.T)
```

#### Day 5: 性能验证

**任务**：
- [ ] 运行完整基准测试
- [ ] 生成性能报告
- [ ] 对比优化前后
- [ ] 验证目标达成

**预期结果**：
```
BenchmarkBTree_Insert-8:
  优化前: 275K ops/s (3633 ns/op)
  优化后: 600K+ ops/s (< 1800 ns/op) ✅

写放大:
  优化前: 3-5x
  优化后: 1.1-1.5x ✅
```

---

### Week 3: 集成和文档 (5 天)

#### Day 1-2: WAL 集成

**任务**：
- [ ] 扩展 WAL 类型支持 Delta Chain
- [ ] 实现 Delta Chain 持久化
- [ ] 实现恢复时重建 Delta Chain
- [ ] 测试

**WAL 类型**：
```go
const (
    WALTypeDeltaChainWrite WALType = 20
    WALTypeDeltaChainMerge WALType = 21
)
```

#### Day 3-4: 文档和调优

**任务**：
- [ ] API 文档
- [ ] 架构文档
- [ ] 性能调优指南
- [ ] 故障排查指南

#### Day 5: 性能回归测试

**任务**：
- [ ] 建立性能基准
- [ ] CI/CD 集成
- [ ] 性能回归检测

---

## 📁 文件结构

```
internal/infrastructure/storage/btree/
├── delta_chain.go              # Delta Chain 核心
├── delta_chain_test.go         # 单元测试
├── delta_chain_bench_test.go   # 基准测试
├── btree.go                    # [修改] 集成 Delta Chain
├── btree_test.go               # [修改] 添加集成测试
└── ...

internal/infrastructure/storage/wal/
└── types.go                    # [修改] 扩展 WAL 类型
```

---

## 🧪 测试策略

### 单元测试

**覆盖率要求**: > 90%

**关键测试**：
- Delta Chain 写入/读取
- Delta Chain 合并逻辑
- 触发条件验证
- 边界条件处理

### 性能测试

**基准测试**：
```go
func BenchmarkDeltaChain_Write(b *testing.B)
func BenchmarkDeltaChain_Merge(b *testing.B)
func BenchmarkBTree_Insert_WithDeltaChain(b *testing.B)
func BenchmarkBTree_Insert_WithoutDeltaChain(b *testing.B)
```

**性能目标**：
- 写入: < 100 ns/op (内存操作)
- 合并: < 10 μs/op (批量操作)
- 整体: 600K+ ops/s

### 并发测试

```bash
go test -race ./internal/infrastructure/storage/btree/
```

### 压力测试

**场景**：
- 1M 次连续写入
- 1000 并发写入
- 读写混合负载
- 崩溃恢复

---

## ⚠️ 风险和缓解

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **Delta Chain 一致性** | 高 | 中 | WAL + 版本控制 |
| **合并期间性能抖动** | 中 | 中 | 异步合并 + 阈值调优 |
| **内存使用增长** | 中 | 中 | 限制 Delta Chain 大小 |
| **崩溃恢复复杂度** | 高 | 低 | 简化恢复逻辑 |

---

## 📈 预期成果

### 性能提升

| 指标 | 优化前 | 优化后 | 提升 |
|------|--------|--------|------|
| 写吞吐 | 275K ops/s | **600K+ ops/s** | **2.2x** |
| 写延迟 | 3633 ns/op | **< 1800 ns/op** | **2x** |
| 写放大 | 3-5x | **1.1-1.5x** | **2-3x** |

### 技术收益

- ✅ 接近 Lealone 性能水平
- ✅ 单 BTree 可达到 600K+ ops/s
- ✅ 写放大显著降低
- ✅ SSD 寿命延长

### 业务收益

- ✅ 减少需要 Sharding 的分片数
- ✅ 降低硬件成本
- ✅ 提升系统稳定性
- ✅ 更好的扩展性

---

## 🚀 下一步行动

### 立即开始 (Week 1)

1. **创建分支**: `feature/delta-chain-optimization`
2. **实现数据结构**: Delta Chain 核心
3. **编写单元测试**: 确保正确性
4. **集成到 BTree**: 修改 Insert/Search

### 中期目标 (Week 2)

1. **性能优化**: 减少内存分配
2. **完整测试**: 压力测试 + 并发测试
3. **性能验证**: 达成 600K+ ops/s 目标

### 长期目标 (Week 3+)

1. **WAL 集成**: 持久化支持
2. **文档完善**: API + 架构文档
3. **性能监控**: 回归检测

---

**计划创建时间**: 2026-03-09
**预计完成时间**: 2026-03-30 (3 周)
**负责人**: NexKV Team
**状态**: 📝 待审批
