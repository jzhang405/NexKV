# Phase 2: CCOW 机制实施计划

**创建时间**: 2026-03-09
**预计工期**: 3 周（从原计划 1.5 周调整，降低风险）
**目标**: 实现完整的 CCOW 并发控制机制

---

## 📋 背景

CCOW (Copy-On-Write) 是 BTree 的核心并发机制：
- **无锁读操作**: 读操作完全无锁，只访问不可变数据
- **单写线程**: 通过 PerCoreExecutor SourceBTree 绑定，消除锁竞争
- **版本化根指针**: 使用 atomic.Value 实现原子切换
- **路径复制**: 写操作复制从根到叶的整个路径
- **快照隔离**: 支持版本化和 MVCC

**时间调整说明**: 经专家评审，CCOW 路径复制是最可能失败的环节，从 1.5 周调整为 3 周。

---

## 🎯 Week 1: CCOW 基础框架

### Day 1-3: 版本化根指针

**文件**: `internal/infrastructure/storage/btree/version.go`

**核心结构**:
```go
// RootInfo 根节点信息
type RootInfo struct {
    RootID   model.PageID
    Version  uint64
    Created  time.Time
    WALSeqNum uint64
}

// VersionedRoot 版本化根指针
type VersionedRoot struct {
    current  atomic.Value // *RootInfo
    versions sync.Map     // map[uint64]*RootInfo (历史版本)
    mu       sync.RWMutex  // 保护 versions map
    gc       *BTreeGC
}
```

**核心方法**:
1. `Get() *RootInfo` - 获取当前根信息（无锁读）
2. `Update(newRoot PageID, walSeqNum uint64) error` - 更新根指针（原子切换）
3. `CreateSnapshot() SnapshotID` - 创建快照
4. `ReleaseSnapshot(id SnapshotID) error` - 释放快照

**测试要求**:
- [ ] 并发读写测试 (1000 goroutines)
- [ ] 版本切换原子性测试
- [ ] 快照创建/释放测试
- [ ] 覆盖率 > 90%

---

### Day 4-7: 路径复制实现

**文件**: `internal/infrastructure/storage/btree/path.go`

**核心结构**:
```go
// PathNode 路径节点
type PathNode struct {
    Node    *Node
    PageID  model.PageID
    Level   int
}

// Path 从根到叶的路径
type Path []*PathNode
```

**核心算法**:
```
CCOW 路径复制流程:
1. FindPath(key) → Path
   - 从当前根开始，二分查找到叶子节点
   - 记录路径: [root, node1, node2, ..., leaf]
   - 完全只读操作，无需加锁

2. CopyPathBottomUp(path) → PageID
   for i = len(path) - 1 to 0:
     a. oldPage = path[i]
     b. newPage = CopyPage(oldPage)
     c. if i == len(path) - 1:
          // 叶子节点: 插入/删除/更新
          ModifyPage(newPage, key, value)
        else:
          // 内部节点: 更新子节点引用
          newPage.Children[idx] = path[i+1].newPageID
     d. path[i].newPage = newPage

   return path[0].newPageID

3. AtomicSwitch(newRoot)
   - 使用 atomic.Value.Store(newRoot)
   - 保证原子性

4. UpdateWAL(newRoot)
   - 异步写入 WAL
```

**核心方法**:
1. `FindPath(key []byte) (Path, error)` - 查找路径
2. `CopyPathBottomUp(path Path) (PageID, error)` - 从底向上复制路径
3. `CopyPage(old *Page) (*Page, error)` - 复制页面
4. `ModifyPage(page *Page, key, value []byte) error` - 修改页面

**测试要求**:
- [ ] 正确性测试: 单线程插入/删除
- [ ] 并发测试: 1000 goroutines 同时写入
- [ ] 压力测试: 10K ops/s 持续 1 分钟
- [ ] 覆盖率 > 90%

---

## 🔧 Week 2: CCOW 增强

### Day 1-2: 边界条件处理

**任务**:
- [ ] 页面分裂时的路径复制
- [ ] 页面合并时的路径复制
- [ ] 根节点分裂
- [ ] 错误处理和重试机制

**测试**:
- [ ] 分裂压力测试
- [ ] 合并压力测试
- [ ] 根节点分裂测试
- [ ] 错误恢复测试

### Day 3-4: 性能优化

**优化方向**:
- [ ] 减少内存拷贝
- [ ] 优化路径查找
- [ ] 批量操作支持
- [ ] 缓存优化

**性能目标**:
- 路径复制 < 5 μs/op (对于 3 层树)
- 内存分配 < 10 KB/op

### Day 5: 错误处理增强

**任务**:
- [ ] 完善错误类型定义
- [ ] 重试机制
- [ ] 回滚机制
- [ ] 错误日志

---

## 🗑️ Week 2, Day 6-10: 垃圾回收

**文件**: `internal/infrastructure/storage/btree/gc.go`

**核心结构**:
```go
// BTreeGC 垃圾回收器
type BTreeGC struct {
    tree        *BTree
    maxVersions int
    stopChan    chan struct{}
    wg          sync.WaitGroup
    interval    time.Duration
}
```

**GC 策略**:
1. **保留最近 N 个版本** (默认 N=10)
2. **基于 WAL 序列号**判断版本是否可释放
3. **定期清理** (每 30 秒)
4. **触发清理** (版本数超过阈值)

**核心方法**:
1. `Start()` - 启动 GC 后台协程
2. `Stop()` - 停止 GC
3. `Collect() error` - 执行一次 GC
4. `ReleaseOldVersions(keepVersion uint64)` - 释放旧版本

**测试要求**:
- [ ] 内存泄漏测试
- [ ] GC 触发时机测试
- [ ] 并发安全性测试
- [ ] 长时间运行测试 (4 小时)
- [ ] 覆盖率 > 85%

---

## 🔒 Week 3: 并发安全专项测试

这是**最关键**的测试阶段，确保 CCOW 机制的并发安全性。

### Day 1-2: 竞态条件检测

**目标**: 确保无数据竞态

**测试方法**:
```bash
# 运行所有测试带上 -race 标志
go test -race ./internal/infrastructure/storage/btree/...
```

**任务**:
- [ ] 运行所有测试带上 `-race` 标志
- [ ] 修复所有检测到的竞态条件
- [ ] 验证修复后无竞态
- [ ] 建立 CI 竞态检测

### Day 3: 死锁检测

**目标**: 确保无死锁

**工具**: `go-deadlock`

**任务**:
- [ ] 使用 `go-deadlock` 检测潜在死锁
- [ ] 超时测试验证无死锁
- [ ] 代码审查检查锁顺序
- [ ] 建立锁顺序规范

### Day 4: 饥饿测试

**目标**: 确保公平性

**任务**:
- [ ] 低优先级任务饥饿测试
- [ ] 验证公平性
- [ ] 调整优先级策略

### Day 5: 压力测试

**目标**: 验证高负载下的稳定性

**测试场景**:
- [ ] 1000 goroutines 同时读写
- [ ] 持续 30 分钟压力测试
- [ ] 内存泄漏检测
- [ ] 性能监控

---

## 📁 新增文件清单

```
internal/infrastructure/storage/btree/
├── version.go              # 版本化根指针
├── version_test.go         # 版本管理测试
├── path.go                 # 路径复制逻辑
├── path_test.go            # 路径复制测试
├── gc.go                   # 垃圾回收
├── gc_test.go              # GC 测试
└── ccow_test.go            # CCOW 集成测试
```

---

## ✅ 验收标准

### 功能验收
- [ ] 完整的版本化根指针
- [ ] 正确的路径复制算法
- [ ] 垃圾回收机制
- [ ] 快照创建/释放

### 并发验收
- [ ] 无竞态条件 (race detector 100% 通过)
- [ ] 无死锁
- [ ] 无饥饿
- [ ] 1000 goroutines 并发测试通过

### 性能验收
- [ ] 路径复制 < 5 μs/op (3 层树)
- [ ] 无锁读操作 < 100 ns/op
- [ ] 内存增长 < 20% (1 小时运行)

### 质量验收
- [ ] 单元测试覆盖率 > 85%
- [ ] 并发测试覆盖率 > 90%
- [ ] 24 小时稳定性测试通过

---

## ⚠️ 关键风险

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| **CCOW 实现复杂度高** | 高 | Phase 0.5 已验证 Mini CCOW 原型 ✅ |
| **路径复制 bug** | 高 | 充分的单元测试和并发测试 |
| **内存泄漏** | 中 | GC 机制 + 4 小时泄漏测试 |
| **并发安全 bug** | 高 | Week 3 专项并发测试 |
| **性能不达标** | 中 | Week 2 性能优化 + 基准测试 |

---

## 📊 进度跟踪

- [ ] Week 1, Day 1-3: 版本化根指针
- [ ] Week 1, Day 4-7: 路径复制实现
- [ ] Week 2, Day 1-2: 边界条件处理
- [ ] Week 2, Day 3-4: 性能优化
- [ ] Week 2, Day 5: 错误处理增强
- [ ] Week 2, Day 6-10: 垃圾回收
- [ ] Week 3, Day 1-2: 竞态条件检测
- [ ] Week 3, Day 3: 死锁检测
- [ ] Week 3, Day 4: 饥饿测试
- [ ] Week 3, Day 5: 压力测试

---

## 🎯 Go/no-Go 决策点

在完成 Week 1 后，进行 Go/no-Go 评估：

**Go 条件** (全部满足):
1. ✅ 版本化根指针实现正确
2. ✅ 路径复制算法正确
3. ✅ 基本并发测试通过

**No-Go 条件** (任一满足):
1. ❌ 核心算法无法实现
2. ❌ 性能差距 > 10x 目标
3. ❌ 无法解决并发安全问题

---

**计划创建者**: Claude Code
**最后更新**: 2026-03-09
**状态**: 准备开始实施
