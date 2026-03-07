# BitmapLock 完全集成计划

> **创建日期**: 2026-03-07
> **作者**: AI Agent
> **状态**: 待审核
> **预估时间**: 3-5 天
> **风险等级**: 高（架构级别改动）

---

## 📋 执行摘要

### 目标
将 BfTree 从**树级别锁**（RWMutex）重构为**页面级别锁**（BitmapLock），充分利用细粒度锁的优势，提升并发性能。

### 当前状态
- ✅ BitmapLock 已实现（独立组件）
- ✅ 基础集成已完成（配置、字段、辅助方法）
- ⚠️ 但 Get/Set/Delete 仍使用全局锁，未实际应用 BitmapLock

### 期望成果
- ✅ Get/Set/Delete 使用页面级别锁
- ✅ 高并发场景性能提升 50%+
- ✅ 架构更加灵活，可扩展

---

## 🎯 核心设计

### 锁粒度重新设计

#### 当前架构（树级别锁）
```
Thread 1: Get("key1")
  └─> rwLock.RLock()          ─┐
                                ├── 锁定整个树
Thread 2: Get("key2")           │
  └─> rwLock.RLock()          <─┘ 等待 Thread 1
```

**问题**：
- ❌ 所有操作串行化
- ❌ 即使操作不同页面，也要等待
- ❌ 并发度低

#### 新架构（页面级别锁）
```
Thread 1: Get("key1" -> page1)
  └─> 1. 遍历树（不锁定，或读锁）
      2. 找到 page1
      3. bitmapLock.RLock(page1)  ─┐
                                   ├── 只锁定 page1
Thread 2: Get("key2" -> page2)    │
  └─> 4. 遍历树（不锁定）       <─┘ 并发执行！
      5. 找到 page2
      6. bitmapLock.RLock(page2)
```

**优势**：
- ✅ 操作不同页面可并发
- ✅ 并发度大幅提升
- ✅ 性能提升 50%+

---

## 📐 技术方案

### 方案概览

#### 阶段 1：树结构保护（新增树锁）
```go
type BfTree struct {
    // ... 现有字段

    treeLock sync.RWMutex  // ✅ 新增：保护树结构（root、父节点等）
    // bitmapLock *BitmapLock  // 已有：保护单个页面
}
```

**职责划分**：
- `treeLock`：保护树结构（root 指针、父节点关系等）
- `bitmapLock`：保护单个页面内容（叶子节点数据）

#### 阶段 2：查找逻辑重构
```go
// 查找页面（不锁定或读锁）
func (t *BfTree) findLeafPage(root uint64, key []byte) (uint64, error) {
    t.treeLock.RLock()
    defer t.treeLock.RUnlock()
    
    // 遍历树结构，找到叶子节点
    // 返回 pageID（不锁定页面）
}

// 读取页面（使用 bitmapLock）
func (t *BfTree) readPage(pageID uint64) ([]byte, error) {
    // 使用 bitmapLock.RLock(pageID) 锁定页面
    t.bitmapLock.RLock(pageID)
    defer t.bitmapLock.RUnlock(pageID)
    
    // 读取页面内容
    // ...
}
```

#### 阶段 3：写操作重构
```go
func (t *BfTree) Set(ctx context.Context, key, value []byte) error {
    // 1. 查找叶子节点（使用 treeLock 读锁）
    leafPageID, err := t.findLeafPage(t.rootPageID, key)
    
    // 2. 锁定叶子页面（使用 bitmapLock 写锁）
    t.bitmapLock.Lock(leafPageID)
    defer t.bitmapLock.Unlock(leafPageID)
    
    // 3. 写入数据
    // ...
}
```

#### 阶段 4：并发修改处理
```go
type Page struct {
    // ... 现有字段
    
    version atomic.Uint64  // ✅ 新增：版本号
}

// 读取版本号
func (p *Page) Version() uint64 {
    return p.version.Load()
}

// 检查版本号
func (p *Page) CheckVersion(expected uint64) bool {
    return p.version.Load() == expected
}
```

---

## 📝 详细实施计划

### Phase 1：结构重构（1 天）

#### 任务 1.1：添加 treeLock
- [ ] 在 BfTree 中添加 `treeLock sync.RWMutex`
- [ ] 更新 NewBfTree 初始化
- [ ] 更新 Close 方法

#### 任务 1.2：添加版本号支持
- [ ] 在 PageEntry 中添加 `version atomic.Uint64`
- [ ] 添加版本号递增方法
- [ ] 添加版本号检查方法

#### 任务 1.3：重构查找逻辑
- [ ] 修改 `findLeafPage` 使用 `treeLock.RLock()`
- [ ] 修改 `findParentPage` 使用 `treeLock.RLock()`
- [ ] 确保查找期间不修改树结构

**验收标准**：
- [ ] 编译通过
- [ ] 现有测试通过

---

### Phase 2：读操作重构（1 天）

#### 任务 2.1：重构 Get 方法
```go
func (t *BfTree) Get(ctx context.Context, key []byte) ([]byte, error) {
    // 1. 查找叶子节点
    leafPageID, version, err := t.findLeafPageWithVersion(t.rootPageID, key)
    if err != nil {
        return nil, err
    }
    
    // 2. 读取页面（使用 bitmapLock）
    value, currentVersion, err := t.readPageContent(leafPageID, key)
    if err != nil {
        return nil, err
    }
    
    // 3. 检查版本号（并发修改检测）
    if currentVersion != version {
        // 页面已被修改，重试
        return t.Get(ctx, key)
    }
    
    return value, nil
}
```

#### 任务 2.2：重构 Scan 方法
- [ ] 使用 `treeLock.RLock()` 保护遍历
- [ ] 使用 `bitmapLock.RLock()` 锁定当前页面
- [ ] 处理页面移动/删除情况

#### 任务 2.3：添加重试逻辑
- [ ] 版本号不匹配时自动重试
- [ ] 最大重试次数限制（防止活锁）
- [ ] 添加重试计数统计

**验收标准**：
- [ ] 单元测试通过
- [ ] 并发测试通过
- [ ] 性能不下降

---

### Phase 3：写操作重构（1.5 天）

#### 任务 3.1：重构 Set 方法
```go
func (t *BfTree) Set(ctx context.Context, key, value []byte) error {
    for retry := 0; retry < 10; retry++ {
        // 1. 查找叶子节点
        leafPageID, version, err := t.findLeafPageWithVersion(t.rootPageID, key)
        if err != nil {
            return err
        }
        
        // 2. 锁定页面并修改
        t.bitmapLock.Lock(leafPageID)
        
        // 3. 检查版本号
        currentVersion := t.getPageVersion(leafPageID)
        if currentVersion != version {
            // 页面已被修改，重试
            t.bitmapLock.Unlock(leafPageID)
            continue
        }
        
        // 4. 写入数据
        t.pageStore.putLeaf(leafPageID, leafNode)
        t.incrementPageVersion(leafPageID)
        t.bitmapLock.Unlock(leafPageID)
        
        return nil
    }
    
    return ErrMaxRetries
}
```

#### 任务 3.2：重构 Delete 方法
- [ ] 使用相同的版本号 + 重试模式
- [ ] 处理页面合并情况
- [ ] 更新统计信息

#### 任务 3.3：重构 Insert/Update 方法
- [ ] 应用相同的模式
- [ ] 确保 WAL 正确集成

**验收标准**：
- [ ] 单元测试通过
- [ ] 并发测试通过（1000 goroutines）
- [ ] 正确性验证（与原有实现结果一致）

---

### Phase 4：Split/Merge 集成（1 天）

#### 任务 4.1：Split 中使用 bitmapLock
```go
func (t *BfTree) splitLeafNode(pageID uint64) error {
    // 1. 锁定要分裂的页面
    t.bitmapLock.Lock(pageID)
    defer t.bitmapLock.Unlock(pageID)
    
    // 2. 执行分裂
    // ...
    
    // 3. 新页面使用 bitmapLock
    // ...
}
```

#### 任务 4.2：Merge 中使用 bitmapLock
- [ ] 使用 bitmapLock 锁定相关页面
- [ ] 确保死锁避免（加锁顺序）
- [ ] 添加死锁检测

**验收标准**：
- [ ] Split 功能正常
- [ ] Merge 功能正常
- [ ] 无死锁发生

---

### Phase 5：测试验证（1 天）

#### 任务 5.1：单元测试
- [ ] Get 方法测试（基础 + 并发）
- [ ] Set 方法测试（基础 + 并发）
- [ ] Delete 方法测试（基础 + 并发）
- [ ] Scan 方法测试（基础 + 并发）

#### 任务 5.2：正确性测试
- [ ] 与原有实现对比测试
- [ ] 随机操作序列测试
- [ ] 边界条件测试

#### 任务 5.3：性能测试
- [ ] 单操作基准测试
- [ ] 并发读写基准测试
- [ ] 与 RWMutex 模式对比
- [ ] 目标：性能提升 50%+

#### 任务 5.4：压力测试
- [ ] 长时间运行测试（1 小时+）
- [ ] 高并发测试（10000 goroutines）
- [ ] 内存泄漏检测

**验收标准**：
- [ ] 所有测试通过
- [ ] 性能提升达到目标
- [ ] 无内存泄漏
- [ ] 无死锁/活锁

---

### Phase 6：文档和清理（0.5 天）

#### 任务 6.1：代码清理
- [ ] 删除旧的锁使用（如果完全迁移）
- [ ] 统一命名规范
- [ ] 添加注释说明

#### 任务 6.2：文档更新
- [ ] 更新设计文档
- [ ] 更新性能测试报告
- [ ] 添加迁移指南

**验收标准**：
- [ ] 代码清晰易读
- [ ] 文档完整准确

---

## ⚠️ 风险评估

### 高风险项

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| **死锁** | 高 | 严格的加锁顺序规范 |
| **活锁** | 中 | 限制重试次数 |
| **性能下降** | 中 | 性能基准测试验证 |
| **正确性问题** | 高 | 与原有实现对比测试 |
| **回归** | 中 | 完整的测试覆盖 |

### 中风险项

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| **复杂度增加** | 中 | 充分的注释和文档 |
| **维护成本** | 低 | 清晰的代码结构 |
| **测试成本** | 中 | 自动化测试 |

---

## 📊 成功标准

### 功能完整性
- [ ] 所有操作功能正确
- [ ] 并发安全保证
- [ ] 无死锁/活锁

### 性能目标
- [ ] 单操作性能不下降
- [ ] 并发性能提升 50%+
- [ ] 高并发场景 CPU 使用率降低

### 质量标准
- [ ] 测试覆盖率 ≥ 80%
- [ ] 无 data race
- [ ] 无内存泄漏
- [ ] 代码审查通过

---

## 🔍 审核检查清单

### 设计审核
- [ ] 锁粒度设计合理
- [ ] 并发控制策略清晰
- [ ] 死锁避免方案充分
- [ ] 性能目标可验证

### 实施审核
- [ ] 任务分解合理
- [ ] 时间估算现实
- [ ] 风险识别充分
- [ ] 验收标准明确

### 技术审核
- [ ] 版本号机制设计合理
- [ ] 重试逻辑安全
- [ ] 加锁顺序规范
- [ ] 测试策略完整

---

## 📅 时间表

| 阶段 | 任务 | 预估时间 | 依赖 |
|------|------|----------|------|
| Phase 1 | 结构重构 | 1 天 | - |
| Phase 2 | 读操作重构 | 1 天 | Phase 1 |
| Phase 3 | 写操作重构 | 1.5 天 | Phase 2 |
| Phase 4 | Split/Merge 集成 | 1 天 | Phase 3 |
| Phase 5 | 测试验证 | 1 天 | Phase 4 |
| Phase 6 | 文档清理 | 0.5 天 | Phase 5 |
| **总计** | **6 天** | - |

---

## 🤔 待审核问题

### 关键决策点

1. **是否添加 treeLock？**
   - 优点：保护树结构，简化设计
   - 缺点：增加一层锁，可能影响性能
   - **建议**：添加，更安全

2. **版本号机制是否必要？**
   - 优点：检测并发修改
   - 缺点：增加复杂性
   - **建议**：添加，保证正确性

3. **重试次数限制？**
   - 建议：最多 10 次重试
   - 超过返回错误，防止活锁

4. **是否保持向后兼容？**
   - 建议：通过配置项切换（UseBitmapLock）
   - 保留 RWMutex 模式作为备选

---

## 📝 审核意见

### 架构审核
- [ ] 锁粒度设计是否合理？
- [ ] treeLock vs bitmapLock 职责是否清晰？
- [ ] 是否有更简单的方案？

### 技术审核
- [ ] 版本号机制是否必要？
- [ ] 重试逻辑是否安全？
- [ ] 性能目标是否可达成？

### 风险审核
- [ ] 死锁风险是否充分评估？
- [ ] 测试覆盖是否充分？
- [ ] 回归方案是否考虑？

### 资源审核
- [ ] 时间估算是否合理？（6 天）
- [ ] 是否有足够的缓冲时间？
- [ ] 是否影响其他工作？

---

## ✅ 批准签字

- [ ] **架构师审核**: _____________  日期: ______
- [ ] **技术负责人审核**: _____________  日期: ______
- [ **安全审核**: _____________  日期: ______

**批准后开始执行**

---

## 📚 附录

### A. 相关文档
- [ ] PR-089 原始文档
- [ ] BitmapLock 实现文档
- [ ] Bf-Tree 设计文档
- [ ] 性能测试基准

### B. 参考实现
- [ ] Microsoft Bf-Tree (Rust)
- [ ] BoltDB (Go)
- [ ] BadgerDB (Go)

### C. 测试数据
- [ ] 当前性能基准
- [ ] 并发测试结果
- [ ] 压力测试报告
