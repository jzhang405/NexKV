# PR-089 Phase 2.1 Day 1-2 DDD 架构审查报告

> **审查人**：DDD 架构专家
> **审查日期**：2026-03-06
> **审查范围**：BfTree 基础 + WAL 接口实现

---

## 一、综合评分

| 评估维度 | 评分 | 说明 |
|---------|------|------|
| DDD 分层架构 | 7/10 | 基本分层清晰，但缺少应用层 |
| 领域建模 | 8/10 | WAL 接口设计合理，错误处理规范 |
| v4 Task[Result] 集成 | 6/10 | 基本集成完成，但实现过于简化 |
| 接口设计 | 8/10 | 接口设计清晰，职责明确 |
| 依赖方向 | 9/10 | 正确的依赖倒置 |

**综合评分：7.2/10**

---

## 二、DDD 分层架构评估

### 2.1 当前架构

```
Domain Layer (internal/domain/)
├── model/
│   └── task.go              # v4 Task[Result] 模型 ✅
└── service/
    └── task.go              # 任务执行器接口 ✅

Infrastructure Layer (internal/infrastructure/)
└── storage/
    ├── bftree/              # BfTree 实现 ✅
    │   └── config.go        # 包含 EnsureDataDir() ❌
    └── wal/                 # WAL 实现 ✅
        └── diskwal.go       # 包含文件操作 ✅
```

### 2.2 分层问题

#### ❌ 缺少应用层
**问题**：`Config.EnsureDataDir()` 在基础设施层

```go
// internal/infrastructure/storage/bftree/config.go:148
func (c *Config) EnsureDataDir() error {
    if err := os.MkdirAll(c.DataDir, 0755); err != nil {
        return fmt.Errorf("failed to create data dir %s: %w", c.DataDir, err)
    }
    // ...
}
```

**分析**：
- 目录创建属于应用层职责，不应在基础设施层
- 基础设施层应该只负责存储引擎本身

**建议**：创建应用层服务

```go
// internal/application/storage/service.go
package storage

type StorageService struct {
    config *bftree.Config
    bftree *bftree.BfTree // 未来实现
}

func (s *StorageService) Initialize() error {
    // 应用层负责基础设施初始化
    return s.config.EnsureDataDir()
}
```

#### ✅ 正确的依赖方向
```
Application Layer (未实现)
    ↓ 依赖
Domain Layer (internal/domain/)
    ↓ 依赖
Infrastructure Layer (internal/infrastructure/)
```

**评估**：Infrastructure → Domain 依赖关系正确 ✅

---

## 三、领域建模评估

### 3.1 WAL 接口设计 ✅

```go
// internal/infrastructure/storage/wal/wal.go
type WAL interface {
    Append(entry *WALEntry) (LSN, error)
    Sync() error
    Recover() ([]*WALEntry, error)
    Truncate(lsn LSN) error
    AppendAsync(ctx context.Context, entry *WALEntry) model.Task[LSN]
    TruncateAsync(ctx context.Context, lsn LSN) model.Task[struct{}]
    Close() error
}
```

**优点**：
- ✅ 接口清晰，职责单一
- ✅ 同步/异步模式并存
- ✅ 返回类型明确（LSN 用于标识日志）
- ✅ 错误处理符合 Go 惯例

**改进建议**：
- ⚠️ 缺少批量操作接口（AppendBatch）
- ⚠️ 缺少查询接口（QueryByLSN）

### 3.2 错误定义 ✅

```go
// internal/infrastructure/storage/wal/errors.go
var (
    ErrWALClosed     = errors.New("wal: closed")
    ErrWALCorrupted  = errors.New("wal: corrupted")
    ErrWALLSNGap     = errors.New("wal: lsn gap detected")
)
```

**优点**：
- ✅ 使用 sentinel errors 模式
- ✅ 错误命名清晰（Err 前缀）
- ✅ 错误信息小写开头

### 3.3 Config 模型 ⚠️

```go
// internal/infrastructure/storage/bftree/config.go
type Config struct {
    PageSize         int
    MaxDepth         int
    DataDir          string
    EnableWAL        bool
    PromotionConfig  PromotionConfig
    // ...
}
```

**问题**：
- Config 是值对象还是实体？
- 包含基础设施路径（DataDir、WALDir）
- 验证逻辑（Validate）与配置混合

**建议**：分离关注点

```go
// 领域配置（不包含路径）
type BfTreeSpec struct {
    PageSize         int
    MaxDepth         int
    PromotionConfig  PromotionConfig
}

// 基础设施配置（包含路径）
type StorageConfig struct {
    Spec    BfTreeSpec
    DataDir string
    WALDir  string
}
```

---

## 四、v4 Task[Result] 集成评估

### 4.1 集成方式 ✅

```go
// 异步模式返回 Task[Result]
AppendAsync(ctx context.Context, entry *WALEntry) model.Task[LSN]
TruncateAsync(ctx context.Context, lsn LSN) model.Task[struct{}]
```

**优点**：
- ✅ 使用泛型 Task[Result]
- ✅ 接口设计符合 v4 架构
- ✅ 返回类型明确

### 4.2 实现质量 ❌

```go
// internal/infrastructure/storage/wal/diskwal.go:226
func (w *DiskWAL) AppendAsync(ctx context.Context, entry *WALEntry) model.Task[LSN] {
    return NewCompletedWALTask(func() (LSN, error) {
        return w.Append(entry)  // 同步调用 ❌
    })
}
```

**问题**：
- ❌ 不是真正的异步，只是包装了同步调用
- ❌ 没有使用 Pipeline.Submit() 提交任务
- ❌ 没有利用 v4 的异步执行能力

**建议**：真正的异步实现

```go
func (w *DiskWAL) AppendAsync(ctx context.Context, entry *WALEntry) model.Task[LSN] {
    task := NewWALAppendTask(w, entry)

    // 提交到 Pipeline（异步执行）
    if err := w.pipeline.Submit(task); err != nil {
        return model.NewFailedTask[LSN](err)
    }

    return task
}
```

### 4.3 completed_task.go 分析

```go
type completedWALTask struct {
    result LSN
    err    error
    done   chan struct{}
}
```

**分析**：
- ✅ 接口实现正确
- ✅ 立即关闭 done channel
- ⚠️ 作为 MVP 简化实现可接受
- ❌ 不符合真正的异步模式

---

## 五、问题列表（P0/P1/P2）

### P0 - 架构问题

1. **缺少应用层**
   - 位置：架构层面
   - 问题：没有应用层，基础设施层职责混乱
   - 影响：代码可维护性、可测试性
   - 修复：创建 `internal/application/storage/`

2. **v4 Task[Result] 集成过度简化**
   - 位置：`diskwal.go:226-243`
   - 问题：异步模式实际是同步执行
   - 影响：无法利用 v4 异步能力
   - 修复：实现真正的 Pipeline 集成

### P1 - 重要问题

3. **Config 包含基础设施路径**
   - 位置：`bftree/config.go:28-49`
   - 问题：领域配置包含基础设施细节
   - 修复：分离 BfTreeSpec 和 StorageConfig

4. **缺少领域服务**
   - 位置：架构层面
   - 问题：没有 WALManager、BfTreeManager
   - 修复：添加领域服务层

### P2 - 改进建议

5. **SourceID 硬编码**
   - 位置：`completed_task.go:108, 152`
   - 问题：SourceID 使用固定字符串
   - 修复：从配置或上下文获取

6. **任务优先级固定**
   - 位置：`completed_task.go:99, 144`
   - 问题：所有任务都是 Normal 优先级
   - 修复：根据操作类型设置优先级

---

## 六、改进建议

### 6.1 添加应用层

```go
// internal/application/storage/service.go
package storage

type Service struct {
    config *config.Config
    wal    wal.WAL
    bftree *bftree.BfTree
}

func (s *Service) Initialize() error {
    // 初始化基础设施
    if err := s.ensureDirectories(); err != nil {
        return err
    }

    // 初始化 WAL
    walConfig := &wal.WALConfig{
        Dir:         s.config.WALDir,
        SegmentSize: s.config.SegmentSize,
    }
    s.wal, err = wal.NewDiskWAL(walConfig)
    return err
}

func (s *Service) ensureDirectories() error {
    return os.MkdirAll(s.config.DataDir, 0755)
}
```

### 6.2 重构 Config 模型

```go
// 领域规范（纯业务逻辑）
type BfTreeSpec struct {
    PageSize        int
    MaxDepth        int
    PromotionConfig PromotionConfig
}

// 存储配置（包含基础设施细节）
type StorageConfig struct {
    Spec        BfTreeSpec
    DataDir     string
    WALDir      string
    SegmentSize int64
}
```

### 6.3 实现真正的异步任务

```go
func (w *DiskWAL) AppendAsync(ctx context.Context, entry *WALEntry) model.Task[LSN] {
    task := NewWALAppendTask(w, entry)

    // 使用 v4 Pipeline 提交任务
    if err := w.pipeline.Submit(task); err != nil {
        return model.NewFailedTask[LSN](err)
    }

    return task
}
```

### 6.4 添加领域服务

```go
// internal/domain/service/wal_service.go
package service

type WALManager interface {
    Append(entry *WALEntry) (LSN, error)
    Recover() error
    Truncate(lsn LSN) error
    GetStats() WALStats
}
```

---

## 七、最终结论

### 7.1 总体评价

当前的代码具有良好的基础架构，DDD 分层基本清晰，但在以下方面需要改进：

1. **缺少应用层**：导致基础设施层职责混乱
2. **v4 集成简化**：异步模式实际是同步执行
3. **领域服务缺失**：缺少高层次的业务逻辑封装

### 7.2 优势

1. ✅ 依赖方向正确（Infrastructure → Domain）
2. ✅ 接口设计清晰，职责明确
3. ✅ 错误处理符合 Go 最佳实践
4. ✅ 配置驱动设计灵活

### 7.3 主要缺陷

1. ❌ 缺少应用层（Architecture Layering）
2. ❌ v4 Task[Result] 集成过度简化
3. ❌ Config 模型混合关注点
4. ❌ 缺少领域服务

### 7.4 推荐行动

**P0（必须修复）**：
1. 创建应用层 `internal/application/storage/`
2. 实现 Pipeline 集成的真正异步任务
3. 重构 Config 分离关注点

**P1（建议修复）**：
4. 添加领域服务层
5. 优化 SourceID 和优先级设置

### 7.5 是否通过审查

**当前阶段**：⚠️ **有条件通过**

**条件**：
1. 完成 Recover 和 Truncate 实现（Go/WAL 专家要求）
2. 实现真正的异步模式（或记录为技术债务）
3. 添加应用层（或记录为架构改进项）

**说明**：
- 作为 MVP（最小可行产品），当前实现可以接受
- 需要在后续迭代中完善架构
- 建议将架构改进作为 Phase 2.2 的重点

---

**审查完成时间**：2026-03-06
**审查结论**：⚠️ 有条件通过（需完成 TODO 项）
**下一步**：完善架构，实现完整的 v4 集成
