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

### Phase 6：文档和清理（1 天）

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

### 风险更新（根据 Code Review 反馈）

#### 新增风险

| 风险 | 等级 | 原计划 | 补充措施 |
|------|------|--------|----------|
| **锁顺序复杂度** | 高 | 未识别 | ✅ 代码审查 + go-deadlock 检测 |
| **版本号性能开销** | 中 | 未识别 | ✅ 性能监控 + 重试延迟策略 |
| **回归测试工作量** | 高 | 低估 | ✅ 自动化回归脚本 |

#### 时间估算调整

| 阶段 | 原计划 | 调整后 | 变化 |
|------|--------|--------|------|
| Phase 1-5 | 5.5 天 | **9 天** | +63% |
| Phase 6 | 0.5 天 | **1 天** | +100% |
| **总计** | **6 天** | **10 天** | **+67%** |

**调整理由**（Code Review 反馈）：
- 架构级改动，复杂性被低估
- 回归测试工作量巨大
- 并发问题难以调试
- 需要更多缓冲时间

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

---

## 📐 技术细节补充（审核决定确认）

### 1. 锁顺序规范（严格执行）

#### 原则
1. **禁止嵌套锁定同一锁**
2. **锁定顺序**：treeLock → bitmapLock
3. **解锁顺序**：bitmapLock → treeLock（倒序）

#### 正确示例 ✅
```go
// 正确的加锁顺序
func (t *BfTree) Get(ctx context.Context, key []byte) ([]byte, error) {
    // 1. 先获取 treeLock（外层）
    t.treeLock.RLock()
    defer t.treeLock.RUnlock()
    
    // 2. 查找页面
    pageID, err := t.findLeafPage(t.rootPageID, key)
    
    // 3. 在 treeLock 内获取 bitmapLock（内层）
    t.bitmapLock.RLock(pageID)
    defer t.bitmapLock.RUnlock(pageID)
    
    // 4. 读取数据
    // ...
    
    // 自动释放：bitmapLock 先释放，treeLock 后释放 ✅
}
```

#### 错误示例 ❌
```go
// ❌ 错误：嵌套锁定 treeLock
func (t *BfTree) Get(ctx context.Context, key []byte) ([]byte, error) {
    t.treeLock.RLock()           // ← 第 1 层
    
    t.bitmapLock.RLock(pageID)   // ← 第 2 层
    t.treeLock.RLock()           // ❌ 死锁！嵌套锁定
}
```

#### 检测工具
```bash
# 安装 go-deadlock
go install github.com/alphadose/go-deadlock@latest

# 检测潜在的死锁
go-deadlock ./internal/infrastructure/storage/bftree/...
```

---

### 2. 版本号机制设计

#### 版本号递增
```go
// 写入页面时递增版本号
func (t *BfTree) incrementPageVersion(pageID uint64) uint64 {
    entry, found := t.pageTable.Get(pageID)
    if !found {
        return 0
    }
    
    newVersion := entry.version.Add(1)
    return newVersion
}

// 读取页面版本号
func (t *BfTree) getPageVersion(pageID uint64) uint64 {
    entry, found := t.pageTable.Get(pageID)
    if !found {
        return 0
    }
    
    return entry.version.Load()
}
```

#### 重试策略（指数退避）
```go
const MaxRetries = 10

func (t *BfTree) Set(ctx context.Context, key, value []byte) error {
    var lastErr error
    
    for retry := 0; retry < MaxRetries; retry++ {
        // 查找并锁定
        pageID, version, err := t.findLeafPageWithVersion(t.rootPageID, key)
        if err != nil {
            return err
        }
        
        t.bitmapLock.Lock(pageID)
        
        // 检查版本
        if t.getPageVersion(pageID) == version {
            // 版本匹配，写入数据
            err = t.setDataToPage(pageID, key, value)
            t.incrementPageVersion(pageID)
            t.bitmapLock.Unlock(pageID)
            
            if err == nil {
                return nil  // 成功
            }
        }
        
        t.bitmapLock.Unlock(pageID)
        
        // 指数退避重试
        time.Sleep(time.Duration(retry) * 10 * time.Microsecond)
    }
    
    return fmt.Errorf("max retries (%d) exceeded, last error: %w", MaxRetries, lastErr)
}
```

---

### 3. 性能监控机制

#### 性能阈值定义
```go
const (
    // P99 延迟阈值
    MaxGetLatencyP99    = 100 * time.Microsecond
    MaxSetLatencyP99    = 150 * time.Microsecond
    MaxScanLatencyP99   = 200 * time.Microsecond
    
    // 重试率阈值
    MaxRetryRate        = 0.1  // 10%
    
    // 吞吐量阈值
    MinThroughput       = 10000  // ops/s
)
```

#### 性能监控统计
```go
type PerformanceStats struct {
    // 延迟统计
    GetLatencyP50    time.Duration
    GetLatencyP99    time.Duration
    SetLatencyP50    time.Duration
    SetLatencyP99    time.Duration
    
    // 重试统计
    RetryCount       int64
    MaxRetriesExceeded int64
    RetryRate        float64  // 重试率
    
    // 吞吐量统计
    Throughput        float64  // ops/s
}

// 检查性能阈值
func (s *PerformanceStats) CheckThresholds() error {
    if s.GetLatencyP99 > MaxGetLatencyP99 {
        return fmt.Errorf("Get latency P99 exceeded: %v > %v", 
            s.GetLatencyP99, MaxGetLatencyP99)
    }
    
    if s.RetryRate > MaxRetryRate {
        return fmt.Errorf("Retry rate exceeded: %.2f%% > %.2f%%", 
            s.RetryRate*100, MaxRetryRate*100)
    }
    
    return nil
}
```

---

### 4. 自动化回归测试脚本

#### 回归测试脚本
```bash
#!/bin/bash
# scripts/test_bitmaplock_regression.sh

set -e

echo "=== BitmapLock 回归测试 ==="

# 颜色输出
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# 1. RWMutex 模式测试
echo -e "\n${YELLOW}[1/6] RWMutex 模式测试${NC}"
go test -v ./internal/infrastructure/storage/bftree -run "TestBfTree_.*" \
  -tags="rmutex" 2>&1 | tee results/rmutex.txt

# 2. BitmapLock 模式测试
echo -e "\n${YELLOW}[2/6] BitmapLock 模式测试${NC}"
go test -v ./internal/infrastructure/storage/bftree -run "TestBfTree_.*" \
  -tags="bitmaplock" 2>&1 | tee results/bitmaplock.txt

# 3. 对比测试结果
echo -e "\n${YELLOW}[3/6] 对比测试结果${NC}"
go run scripts/compare_results.go \
  results/rmutex.txt \
  results/bitmaplock.txt

# 4. 性能基准测试
echo -e "\n${YELLOW}[4/6] 性能基准测试${NC}"
go test -bench=. -benchmem ./internal/infrastructur-e/storage/bftree \
  -benchtime=10s | tee results/benchmark.txt

# 5. 死锁检测
echo -e "\n${YELLOW}[5/6] 死锁检测${NC}"
go-deadlock ./internal/infrastructure/storage/bftree/... 2>&1

# 6. 汇总
echo -e "\n${GREEN}✅ 回归测试完成${NC}"
echo "详细结果：results/"
```

#### Makefile 集成
```makefile
# Makefile BitmapLock 回归测试

.PHONY: test-regression
test-regression:
	@echo "运行 BitmapLock 回归测试..."
	./scripts/test_bitmaplock_regression.sh

.PHONY: test-rmutex
test-rmutex:
	@echo "运行 RWMutex 模式测试..."
	go test -v -tags="rmutex" ./internal/infrastructure/storage/bftree/...

.PHONY: test-bitmaplock
test-bitmaplock:
	@echo "运行 BitmapLock 模式测试..."
	go test -v -tags="bitmaplock" ./internal/infrastructure/storage/bftree/...
```

---

## 📊 实施计划更新（10 天）

### Phase 1：结构重构（1.5 天）

#### 任务 1.1：添加 treeLock 和版本号
- [ ] 在 BfTree 中添加 `treeLock sync.RWMutex`
- [ ] 在 PageEntry 中添加 `version atomic.Uint64`
- [ ] 更新 NewBfTree 初始化
- [ ] 更新 Close 方法

#### 任务 1.2：添加性能监控
- [ ] 定义性能阈值常量
- [ ] 添加性能统计结构
- [ ] 实现性能检查方法

**验收标准**：
- [ ] 编译通过
- [ ] 现有测试通过
- [ ] 新增编译 0 警告

---

### Phase 2：查找逻辑重构（1.5 天）

#### 任务 2.1：重构 findLeafPage
```go
func (t *BfTree) findLeafPageWithVersion(root uint64, key []byte) (uint64, uint64, error) {
    t.treeLock.RLock()
    defer t.treeLock.RUnlock()
    
    // 遍历树结构
    currentPageID := root
    for {
        entry, found := t.pageTable.Get(currentPageID)
        if !found {
            return 0, 0, ErrPageNotFound
        }
        
        if entry.pageType == PageTypeLeaf {
            return currentPageID, entry.version.Load(), nil
        }
        
        // 继续向下查找
        innerNode, _ := t.pageStore.getInner(currentPageID)
        idx := innerNode.findChild(key)
        currentPageID = innerNode.children[idx]
    }
}
```

#### 任务 2.2：重构 findParentPage
- [ ] 使用 treeLock.RLock() 保护遍历
- [ ] 返回父节点 pageID 和版本号
- [ ] 添加并发安全检查

**验收标准**：
- [ ] 编译通过
- [ ] 现有测试通过
- [ ] 死锁检测通过

---

### Phase 3：读操作重构（2 天）

#### 任务 3.1：重构 Get 方法
- [ ] 使用 findLeafPageWithVersion 查找
- [ ] 使用 bitmapLock.RLock() 锁定页面
- [ ] 添加版本号检查和重试
- [ ] 添加性能监控

#### 任务 3.2：重构 Scan 方法
- [ ] 使用 treeLock.RLock() 保护遍历
- [ ] 使用 bitmapLock.RLock() 锁定当前页面
- [ ] 处理页面移动/删除情况
- [ ] 添加重试逻辑

**验收标准**：
- [ ] 单元测试通过
- [ ] 并发测试通过（1000 goroutines）
- [ ] 性能不下降
- [ ] 性能监控无告警

---

### Phase 4：写操作重构（2.5 天）

#### 任务 4.1：重构 Set 方法
- [ ] 使用 findLeafPageWithVersion 查找
- [ ] 使用 bitmapLock.Lock() 锁定页面
- [ ] 版本号检查 + 指数退避重试
- [ ] 更新性能统计

#### 任务 4.2：重构 Delete 方法
- [ ] 使用相同的版本号 + 重试模式
- [ ] 处理页面合并情况
- [ ] 更新统计信息

#### 任务 4.3：重构 Insert/Update
- [ ] 应用相同的模式
- [ ] 确保 WAL 正确集成

**验收标准**：
- [ ] 单元测试通过
- [ ] 并发测试通过（1000 goroutines）
- [ ] 正确性验证通过
- [ ] 性能监控正常

---

### Phase 5：Split/Merge 集成（1.5 天）

#### 任务 5.1：Split 中使用 bitmapLock
```go
func (t *BfTree) splitLeafNode(pageID uint64) error {
    // 锁定要分裂的页面
    t.bitmapLock.Lock(pageID)
    defer t.bitmapLock.Unlock(pageID)
    
    // 获取当前版本
    currentVersion := t.getPageVersion(pageID)
    
    // 执行分裂
    newPageID, err := t.splitLeafNodeInternal(pageID)
    if err != nil {
        return err
    }
    
    // 更新版本号
    t.setPageVersion(newPageID, currentVersion+1)
    
    return nil
}
```

#### 任务 5.2：Merge 中使用 bitmapLock
- [ ] 使用 bitmapLock 锁定相关页面
- [ ] 确保死锁避免（严格加锁顺序）
- [ ] 添加死锁检测

**验收标准**：
- [ ] Split 功能正常
- [ ] Merge 功能正常
- [ ] 无死锁发生
- [ ] go-deadlock 检测通过

---

### Phase 6：测试验证（1.5 天）

#### 任务 6.1：单元测试
- [ ] Get 方法测试（基础 + 并发）
- [ ] Set 方法测试（基础 + 并发 + 重试）
- [ ] Delete 方法测试（基础 + 并发）
- [ ] Scan 方法测试（基础 + 并发）

#### 任务 6.2：正确性测试
- [ ] 与原有实现对比测试
- [ ] 随机操作序列测试（10000+ 操作）
- [ ] 边界条件测试

#### 任务 6.3：性能测试
- [ ] 单操作基准测试
- [ ] 并发读写基准测试（1000 goroutines）
- [ ] 与 RWMutex 模式对比
- [ ] 目标：性能提升 50%+

#### 任务 6.4：压力测试
- [ ] 长时间运行测试（1 小时+）
- [ ] 高并发测试（10000 goroutines）
- [ ] 内存泄漏检测

#### 任务 6.5：回归测试
- [ ] 运行自动化回归脚本
- [ ] 性能基线对比
- [ ] 死锁检测
- [ ] 性能告警检查

**验收标准**：
- [ ] 所有测试通过
- [ ] 性能提升 ≥ 50%
- [ ] 无内存泄漏
- [ ] 无死锁/活锁
- [ ] 性能无告警

---

### Phase 7：文档和清理（1 天）

#### 任务 7.1：代码清理
- [ ] 删除旧的锁使用（如果完全迁移）
- [ ] 统一命名规范
- [ ] 添加注释说明
- [ ] 添加 go-deadlock 注释

#### 任务 7.2：文档更新
- [ ] 更新设计文档
- [ ] 更新性能测试报告
- [ ] 添加迁移指南
- [ ] 添加故障排查指南

#### 任务 7.3：工具集成
- [ ] 集成 go-deadlock 到 CI
- [ ] 添加性能监控到仪表板
- [ ] 配置告警规则

**验收标准**：
- [ ] 代码清晰易读
- [ ] 文档完整准确
- [ ] CI/CD 集成完成

---

## 🔒 锁顺序规范（严格执行）

### 基本原则

1. **禁止嵌套锁定同一锁**
2. **锁定顺序**：treeLock → bitmapLock
3. **解锁顺序**：bitmapLock → treeLock（倒序）
4. **禁止**：在持有 bitmapLock 时获取 treeLock

### 锁层次结构

```
外层：treeLock
  ├── 内层：bitmapLock.Lock(pageID1)
  ├── 内层：bitmapLock.RLock(pageID2)
  └── 内层：bitmapLock.Lock(pageID3)

释放顺序（倒序）：
pageID3 → pageID2 → pageID1 → treeLock ✅
```

### 代码检查清单

- [ ] 所有 Lock/Unlock 成对出现
- [ ] defer 确保正确释放顺序
- [ ] go-deadlock 检查通过
- [ ] 代码审查通过

---

## 📈 性能目标更新

### 性能指标

| 指标 | 当前（RWMutex） | 目标（BitmapLock） | 提升 |
|------|-----------------|-------------------|------|
| Get 延迟 P99 | ~80μs | **≤100μs** | - |
| Set 延迟 P99 | ~150μs | **≤150μs** | - |
| 并发吞吐 | ~10K ops/s | **≥15K ops/s** | **+50%** |
| 重试率 | - | **≤10%** | - |
| CPU 使用率（高并发） | 高 | **降低 30%+** | - |

### 监控指标

```go
type Metrics struct {
    // 延迟
    LatencyP50    atomic.Int64  // 微秒
    LatencyP99    atomic.Int64  // 微秒
    Latency999    atomic.Int64  // 微秒
    
    // 重试
    RetryCount    atomic.Int64
    RetrySuccess  atomic.Int64
    
    // 吞吐量
    OpsPerSecond  atomic.Int64
    
    // 锁
    LockContentions atomic.Int64
    LockWaitTime   atomic.Int64  // 微秒
}
```

---

## ✅ 审核批准

### 审核签字确认
- [x] **时间估算**：10 天（已确认）
- [x] **锁顺序规范**：代码审查 + go-deadlock（已确认）
- [x] **版本号机制**：保留（已确认）
- [x] **双层锁架构**：保持（已确认）
- [x] **死锁检测**：组合方案（已确认）
- [x] **性能告警**：需要，100μs/150μs（已确认）
- [x] **回归测试**：自动化脚本（已确认）

---

## 🚀 下一步行动

计划已根据审核意见更新，所有决策已确认。

**准备开始执行 10 天的开发工作** ✅

**确认后我将开始 Phase 1 的实施工作。**

---

## 📝 审核记录

| 轮次 | 日期 | 审核人 | 意见 | 状态 |
|------|------|--------|------|------|
| 第 1 轮 | 2026-03-07 | 用户 | 7 条审核意见 | ✅ 已确认 |
| 第 2 轮 | 2026-03-07 | AI Agent | 根据确认更新计划 | ✅ 已更新 |

---

**状态**：✅ **已批准，准备执行**

---

**文档版本**: V2.0（审核确认版）
