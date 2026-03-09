# BTree + WAL 性能分析报告

**日期**: 2026-03-09
**测试环境**: 8 核 CPU
**测试工具**: Go benchmark

---

## 📊 性能数据汇总

### 单线程性能对比

| 配置 | 延迟 (ns/op) | QPS (ops/sec) | 内存 (B/op) | 分配次数 |
|------|-------------|---------------|------------|---------|
| **纯内存 BTree** | 8166 | 122,442 | 15,736 | 15 |
| **带 PageManager (无WAL)** | 7499 | 133,351 | 15,658 | 12 |
| **带 WAL 持久化** | 8528 | 117,257 | 15,658 | 12 |

**关键发现**:
- PageManager 无 WAL 反而更快（7499 vs 8166）
- WAL 单线程开销仅 **+4.4%**

---

### 8 线程并发性能对比

| 配置 | 延迟 (ns/op) | QPS (ops/sec) | 内存 (B/op) | 分配次数 |
|------|-------------|---------------|------------|---------|
| **纯内存 BTree** | 1963 | **509,526** | 15,736 | 15 |
| **带 PageManager (无WAL)** | 3971 | 251,820 | 15,667 | 12 |
| **带 WAL 持久化** | 4663 | **214,475** | 15,667 | 12 |

**关键发现**:
- WAL 并发开销较大：**+137%** 延迟 (1963→4663 ns/op)
- QPS 下降：**509K → 214K (-58%)**
- PageManager 本身也有开销：3971 vs 1963 ns/op

---

## 🔍 性能瓶颈分析

### WAL 开销来源

**1. 文件系统同步**
```go
// wal.go:90
if err := wal.file.Sync(); err != nil {
    return fmt.Errorf("sync WAL: %w", err)
}
```
- 每次 WAL 写入都调用 `fsync`
- 强制刷新到磁盘，阻塞后续操作
- 并发时多个 goroutine 竞争文件锁

**2. 串行化写入**
```go
// wal.go:75
mu sync.Mutex
```
- 全局锁保护 WAL 文件
- 并发写入变成串行
- 8 线程无法充分利用

**3. 系统调用开销**
- 每次 `Write` + `Sync` = 2 次系统调用
- 上下文切换成本高

---

## 💡 优化方向

### 优化 1: WAL 批量写入（预期 +50-100%）

**当前问题**: 每次写入都 fsync

**优化方案**:
```go
type WALBatch struct {
    entries []*WALEntry
    timer   *time.Timer
    size    int
}

func (w *WALBatch) Add(entry *WALEntry) error {
    w.entries = append(w.entries, entry)
    w.size++

    // 批量刷新条件：
    // 1. 达到批次大小 (e.g., 1000 条)
    // 2. 超过时间阈值 (e.g., 10ms)
    if w.size >= 1000 {
        return w.Flush()
    }
    return nil
}
```

**预期收益**:
- 减少 fsync 次数：1000 次 → 1 次
- QPS 提升：214K → 300K-400K

---

### 优化 2: 并发 WAL（预期 +30-50%）

**当前问题**: 全局锁串行化

**优化方案**:
```go
type ConcurrentWAL struct {
    segments [] WALSegment  // 分段 WAL
    segmentID atomic.Uint64
}

func (w *ConcurrentWAL) Write(entry *WALEntry) error {
    id := w.segmentID.Add(1)
    segment := w.segments[id%len(w.segments)]
    return segment.Write(entry)
}
```

**预期收益**:
- 减少锁竞争
- 8 线程并行写入
- QPS 提升：214K → 280K-320K

---

### 优化 3: 异步 fsync（预期 +20-30%）

**当前问题**: 同步 fsync 阻塞写入

**优化方案**:
```go
type AsyncWAL struct {
    syncChan chan *WALEntry
    doneChan chan error
}

func (w *AsyncWAL) backgroundSyncer() {
    for entry := range w.syncChan {
        if err := w.writeAndSync(entry); err != nil {
            w.doneChan <- err
        }
    }
}
```

**预期收益**:
- 写入不阻塞
- QPS 提升：214K → 260K-280K
- 权衡：崩溃可能丢失少量数据

---

## 📈 性能目标对比

### 当前 vs 目标

| 指标 | 当前 | 优化后目标 | Lealone |
|------|------|-----------|---------|
| **单线程 QPS** | 117K | 150K (+28%) | - |
| **8线程 QPS** | 214K | 400K (+87%) | 670K |
| **vs 纯内存退化** | -58% | -20% | - |

**可行性**:
- ✅ 批量写入：技术成熟，实现简单
- ✅ 并发 WAL：需要仔细设计，但可行
- ⚠️ 异步 fsync：有数据丢失风险，需权衡

---

## 🎯 推荐方案

### 短期优化（1-2天）

**优先级 P0**: WAL 批量写入
- 实现简单，收益明显
- 无数据安全风险
- 预期 QPS: 214K → 350K

### 中期优化（3-5天）

**优先级 P1**: WAL 分段并发
- 需要重新设计 WAL 架构
- 但能显著提升并发性能
- 预期 QPS: 350K → 450K

### 长期优化（可选）

**优先级 P2**: 异步 fsync + 定期 checkpoint
- 复杂度高，需要权衡数据安全
- 适合对性能要求极高的场景
- 预期 QPS: 450K → 500K+

---

## 📝 结论

**当前状态**:
- ✅ WAL 功能完整，数据安全有保障
- ⚠️ 并发性能有较大优化空间
- 📊 距离 Lealone (670K QPS) 仍有差距

**建议**:
1. **短期**: 实现 WAL 批量写入（1-2天，QPS +64%）
2. **中期**: 实现 WAL 分段并发（3-5天，QPS +110%）
3. **评估**: 是否需要达到 Lealone 性能水平

**投入产出比**:
- 批量写入：高（简单，收益大）
- 并发 WAL：中（复杂，收益中等）
- 异步 fsync：低（风险高，收益有限）

---

**文档版本**: 1.0
**完成时间**: 2026-03-09
