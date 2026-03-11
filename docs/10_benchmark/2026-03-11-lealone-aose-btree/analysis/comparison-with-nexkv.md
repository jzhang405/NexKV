# NexKV vs Lealone BTree 性能对比分析

> **对比日期**: 2026-03-11
> **对比人**: jzhang405
> **NexKV 版本**: feature/btree-perf-phase1
> **Lealone 版本**: 8.0.0-SNAPSHOT (AOSE)

---

## 执行摘要

### 对比概述

**对比范围**:
- NexKV BTree: Go 实现的 CCOW BTree
- Lealone AOSE BTree: Java 实现的异步 Append-Only BTree

**测试状态**:
- ✅ NexKV: 完整基准测试数据
- ⚠️ Lealone: 无法运行 benchmark（构建问题）

**对比重点**:
- 架构设计差异
- 技术实现差异
- 性能特性分析
- 适用场景分析

---

## 1. 架构设计对比

### 1.1 NexKV BTree 架构

**核心特性**:
- **语言**: Go 1.24
- **架构**: CCOW (Copy-on-Write on Write)
- **并发**: VersionedRoot + CAS (Compare-And-Swap)
- **持久化**: WAL (Write-Ahead Log) + PageManager
- **存储**: BTree Node 包含 Keys/Values/Children/ChildIDs

**Node 结构**:
```go
type Node struct {
    PageID   model.PageID
    Keys     [][]byte      // 键切片
    Values   [][]byte      // 值切片
    Children []*Node       // 子节点指针
    ChildIDs []model.PageID // 子节点 PageID
    IsLeaf   bool
}
```

**写入流程**:
```
1. FindPath(key) → 找到路径
2. CopyPathBottomUp(path, modifyFunc) → 从叶子到根复制
3. root.Update(newRoot) → CAS 更新根节点
4. WAL.Write(entry) → 写 WAL（可选）
```

---

### 1.2 Lealone AOSE BTree 架构

**核心特性**:
- **语言**: Java 21
- **架构**: Append-Only Storage Engine
- **并发**: 异步化 BTree (Async BTree)
- **持久化**: Chunk-based (256MB 大块)
- **存储**: Page-based + Chunk Manager

**存储层次**:
```
BTreeMap
  ↓
BTreeStorage
  ↓
ChunkManager → Chunks (256MB files)
  ↓
Page (4KB default)
```

**关键设计**:
- **Append-Only**: 数据追加到 Chunk，不覆盖
- **异步化**: 异步保存，不阻塞写入
- **批量写入**: 积累脏页，批量 flush
- **压缩**: 支持 LZF / Deflate 压缩

---

## 2. 性能数据对比

### 2.1 NexKV BTree 实测性能

**测试配置**: WAL 未启用（纯内存操作）

| 操作 | 延迟 | 吞吐量 | 说明 |
|------|------|--------|------|
| **单次写入** | 5.04 µs/op | 198k ops/sec | BenchmarkCCOW_Complete |
| **批量写入** | 0.43 µs/key | 2.3M ops/sec | BatchSize=100 |
| **单次读取** | 179 ns/op | 5.6M ops/sec | BenchmarkBTree_ReadThroughput |
| **并发写入** | 2.06 µs/op | 485k ops/sec | BenchmarkWrite_Concurrent |
| **并发读取** | 108.5 ns/op | 9.2M ops/sec | BenchmarkRead_Concurrent |

**内存分配**:
- 写入: 17928 B/op, 12 allocs/op
- 读取: 16 B/op, 1 allocs/op

---

### 2.2 Lealone AOSE 性能分析

**预期性能特点**（基于架构设计）:

**优势**:
1. **异步写入**: 写入不阻塞，延迟更低
2. **批量持久化**: Chunk 批量写入，摊销 I/O 开销
3. **JVM 优化**: JIT 编译，GC 优化
4. **大对象**: Java 对象更大，但操作更少

**可能劣势**:
1. **GC 压力**: Java 对象分配更多
2. **JIT 预热**: 需要预热才能达到最佳性能
3. **内存占用**: Java 对象开销 > Go

---

## 3. 关键差异分析

### 3.1 并发模型

| 方面 | NexKV (Go) | Lealone (Java) |
|------|-------------|----------------|
| **并发原语** | CAS + Mutex | Lock-free + 异步 |
| **一致性** | 强一致性 (CCOW) | 最终一致性 (异步) |
| **锁竞争** | 低 (CAS) | 极低 (异步) |
| **复杂度** | 简单 | 较复杂 |

**性能影响**:
- **NexKV**: CAS 循环可能增加延迟
- **Lealone**: 异步化延迟更低，但吞吐可能受影响

---

### 3.2 内存管理

| 方面 | NexKV (Go) | Lealone (Java) |
|------|-------------|----------------|
| **分配方式** | make() 直接分配 | new() + GC |
| **对象大小** | 小 (56 bytes Node) | 大 (对象头 + 引用) |
| **GC 算法** | 三色标记 GC | G1GC / ZGC |
| **内存开销** | 低 | 较高 |

**性能影响**:
- **NexKV**: 内存分配少，GC 压力低
- **Lealone**: 对象分配多，但 GC 优化更好

---

### 3.3 持久化策略

| 方面 | NexKV | Lealone |
|------|-------|---------|
| **WAL** | 顺序写 WAL | Append-Only Chunk |
| **fsync** | 每次 WAL 写入 | 批量 Chunk 写入 |
| **持久化粒度** | 每次写入 | 定期 batch (256MB) |
| **I/O 模式** | 小 I/O 频繁 | 大 I/O 批量 |

**性能影响**:
- **NexKV**: WAL + fsync 开销大 (+10x ~ +40x)
- **Lealone**: 批量写入摊销开销，但延迟可能增加

---

## 4. 场景适用性分析

### 4.1 写密集型场景

**NexKV**:
- ✅ 单次写入延迟低（5.04 µs）
- ✅ 批量写入极快（0.43 µs/key）
- ❌ 高并发下 CAS 竞争

**Lealone**:
- ✅ 异步化延迟极低
- ✅ 高并发性能好
- ❌ 批量写入可能不如 NexKV

**推荐**: **Lealone** 在高并发写场景更有优势

---

### 4.2 读密集型场景

**NexKV**:
- ✅ 单次读取极快（179 ns）
- ✅ 内存操作效率高
- ✅ 无 GC 压力

**Lealone**:
- ✅ 读性能可能也不错（预估 200-500 ns）
- ❌ 对象分配更多

**推荐**: **NexKV** 在读密集型场景更有优势

---

### 4.3 混合读写场景

**NexKV**:
- ✅ 读写均衡
- ✅ 延迟可预测（同步）
- ❌ 写瓶颈明显

**Lealone**:
- ✅ 读写分离（异步）
- ✅ 写吞吐高
- ⚠️ 最终一致性

**推荐**: **Lealone** 适合高吞吐场景

---

## 5. 技术优势对比

### 5.1 NexKV 优势

1. **简单可靠**
   - Go 语言简单，易于理解
   - CCOW 语义清晰，易于调试
   - 强一致性，数据安全

2. **性能可预测**
   - 同步操作，延迟确定
   - GC 压力小
   - 无 JIT 预热问题

3. **内存效率高**
   - 对象小，开销低
   - 无对象头开销
   - 栈分配优化

4. **适合场景**
   - 嵌入式系统
   - 低延迟要求
   - 读密集型工作负载

---

### 5.2 Lealone 优势

1. **高并发性能**
   - 异步化架构
   - Lock-free 设计
   - 适合多核环境

2. **吞吐量高**
   - 异步写入不阻塞
   - 批量持久化
   - 写吞吐极高

3. **企业级特性**
   - 插件化架构
   - SQL 支持
   - 集群/分布式

4. **适合场景**
   - OLTP 系统
   - 高并发写
   - 大规模部署

---

## 6. 性能优化方向

### 6.1 NexKV 优化方向

**短期**（已识别）:
1. **批量操作优化**（最推荐）
   - 已验证快 11.7 倍
   - 工作量：2-3 天

2. **减少切片复制**（高优先级）
   - COW 切片或分段 COW
   - 预期 -30% ~ -50%
   - 工作量：3-5 天

**长期**:
3. **值指针方案**（ValueRef）
   - 减少 big value 复制
   - 预期 -20% ~ -40%

4. **并发写入优化**
   - 分段 CAS
   - 减少竞争

---

### 6.2 Lealone 优化方向

**基于架构分析**:
1. **JVM 调优**
   - GC 参数优化
   - 堆内存调优
   - JIT 编译优化

2. **异步化优化**
   - 减少异步等待
   - 批量提交优化

3. **压缩优化**
   - LZF vs Deflate 对比
   - 压缩阈值调优

---

## 7. 结论与建议

### 7.1 性能对比结论

**读性能**: **NexKV 更优**
- 单次读取：179 ns vs 预估 200-500 ns
- 内存效率：Go < Java

**写性能**: **场景相关**
- 低并发写：NexKV 更优（5.04 µs）
- 高并发写：Lealone 更优（异步化）
- 批量写：NexKV 更优（0.43 µs/key，快 11.7 倍）

**生产环境**: **需要实测验证**
- 启用 WAL 后，NexKV 写延迟预计 +10x ~ +40x
- Lealone 异步持久化开销相对较小

---

### 7.2 技术选型建议

**选择 NexKV**，如果：
- ✅ 需要低延迟（< 10 µs）
- ✅ 读密集型工作负载
- ✅ 嵌入式场景
- ✅ 简单可靠优先

**选择 Lealone**，如果：
- ✅ 高并发写（> 10k QPS）
- ✅ 写密集型工作负载
- ✅ 需要企业级特性（SQL、集群）
- ✅ 大规模部署

---

### 7.3 后续工作建议

**NexKV**:
1. ✅ 完成当前 Phase 1 优化
2. ✅ 实施切片复制优化（方案 3）
3. ⏳ 启用 WAL 后重新测试
4. ⏳ 添加生产环境性能监控

**Lealone**:
1. ⏳ 修复 benchmark 构建问题
2. ⏳ 运行完整性能测试
3. ⏳ 启用持久化后测试
4. ⏳ 与 NexKV 对比实测数据

---

## 8. 数据源

**NexKV 数据**:
- `docs/10_benchmark/2026-03-11-btree-perf-phase1/SUMMARY.md`
- `docs/10_benchmark/2026-03-11-btree-perf-phase1/C0_baseline_results.md`
- 原始数据：`docs/10_benchmark/2026-03-11-btree-perf-phase1/assets/raw/`

**Lealone 数据**:
- `thoughts/Lealone/lealone-aose/src/main/java/com/lealone/storage/aose/btree/`
- `thoughts/Lealone/README.md`
- 无法运行 benchmark（构建问题）

---

## 附录：技术栈对比

### A.1 编程语言特性

| 特性 | Go 1.24 | Java 21 |
|------|---------|---------|
| **GC** | 三色标记 GC | G1GC / ZGC |
| **对象模型** | 无继承，轻量 | 有继承，重量 |
| **并发** | Goroutine + Channel | Thread + Executor |
| **编译** | 本地代码 | JIT 编译 |
| **启动时间** | 快 | 慢（JIT 预热） |

### A.2 BTree 特性

| 特性 | NexKV | Lealone |
|------|-------|---------|
| **高度** | 可配置（默认 256） | 固定 |
| **分支因子** | 256/257 | 固定 |
| **并发模型** | CCOW | 异步化 |
| **持久化** | WAL | Append-Only Chunk |
| **压缩** | 无 | LZF / Deflate |

---

**报告版本**: v1.0
**最后更新**: 2026-03-11
**状态**: ⚠️ Lealone benchmark 未运行，基于架构分析
**建议**: 需要实测数据验证分析结论
