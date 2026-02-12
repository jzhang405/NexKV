# PR-044: 错误类型简化 - Pre 文档

> **文档类型**: Pre 文档（需求+设计+风险评估）  
> **创建日期**: 2026-02-12  
> **目标分支**: main  
> **工作分支**: feature/refactor-error-types

---

## 1. 需求背景

### 1.1 问题来源

根据 `docs/09-code-review/2026-02-12-findings-report.md`：

| 问题编号 | 问题描述 | 位置 | 影响 |
|---------|---------|------|------|
| **P4-1** | 错误类型过度设计 | `internal/metadata/types/errors.go` | 代码冗长、维护成本高 |

### 1.2 现状分析

**代码规模**：
- 文件大小：36,245 tokens（过大）
- 错误构造函数：434 个
- 错误码数量：使用 iota 自动生成（预计 100+ 个）

**现有结构**：
```go
type Error struct {
    Code    ErrorCode // 错误码
    Message string    // 错误消息
    Op      string    // 操作（可选）
    Err     error     // 底层错误（可选）
}

type RPCError struct {
    Code       string       // 错误码
    Message    string       // 错误消息
    Type       RPCErrorType // 错误类型
    Retryable  bool         // 是否可重试
    Cause      error        // 原始错误
    Timestamp  time.Time    // 错误发生时间
    RequestID  string       // 请求ID
    TargetAddr string       // 目标地址
}
```

### 1.3 问题分析

**过度设计的表现**：
1. **大量专用构造函数**：`NewNotFoundError`、`NewAlreadyExistsError` 等 434 个
2. **重复的错误消息模板**：每个构造函数都手动格式化消息
3. **错误码管理复杂**：使用 iota 自动生成，但难以维护

**根本原因**：
- 为每个错误场景创建专用构造函数
- 没有使用 Go 标准库的 `fmt.Errorf` 和错误包装
- 过度追求类型安全的错误码

---

## 2. 技术方案

### 2.1 简化策略

#### 阶段 1：使用 Go 1.13+ 错误包装

**简化前**：
```go
func NewNotFoundError(key string) *Error {
    return newBase(ErrCodeNotFound, "键不存在: %s", key)
}

// 使用
err := NewNotFoundError("myKey")
```

**简化后**：
```go
// 使用标准库错误包装
import "fmt"

// 使用
err := fmt.Errorf("键不存在: %s", "myKey")
```

#### 阶段 2：保留关键错误类型

**保留的错误类型**：
1. **业务错误**：需要特定处理逻辑的错误（如 `ErrNotFound`）
2. **RPC 错误**：`RPCError`（因为需要重试逻辑）

**删除的错误类型**：
- 通用错误（使用 `fmt.Errorf`）
- 可通过错误消息区分的错误（使用 `errors.Is`）

#### 阶段 3：使用 `errors.Is` 和 `errors.As`

**错误检查模式**：
```go
// 定义错误
var ErrNotFound = errors.New("not found")
var ErrAlreadyExists = errors.New("already exists")

// 检查错误
if errors.Is(err, ErrNotFound) {
    // 处理未找到
}
```

### 2.2 迁移路径

#### 第 1 步：添加哨兵错误（向后兼容）

```go
// 在简化前，先定义哨兵错误
var (
    ErrNotFound     = errors.New("not found")
    ErrAlreadyExists = errors.New("already exists")
    // ... 其他关键错误
)
```

#### 第 2 步：更新调用方

```go
// 旧代码
if err == ErrCodeNotFound {
    // 处理未找到
}

// 新代码
if errors.Is(err, ErrNotFound) {
    // 处理未找到
}
```

#### 第 3 步：逐步删除专用构造函数

保留少量高频使用的构造函数，删除其余。

---

## 3. 实施计划

### 3.1 分析阶段

| 步骤 | 操作 | 预期产出 |
|------|------|---------|
| 1 | 统计错误类型使用频率 | 错误使用频率报告 |
| 2 | 识别关键错误类型 | 需要保留的错误列表 |
| 3 | 分析调用方代码 | 需要修改的文件列表 |

### 3.2 简化阶段（分批）

| 批次 | 操作 | 预期工作量 |
|------|------|-----------|
| 1 | 添加哨兵错误，保持向后兼容 | 0.5 天 |
| 2 | 更新高频使用 `errors.Is` | 1 天 |
| 3 | 删除低频构造函数 | 0.5 天 |

### 3.3 验证阶段

| 步骤 | 操作 | 预期产出 |
|------|------|---------|
| 1 | 运行所有测试 | 确保无破坏 |
| 2 | 检查错误日志输出 | 确保错误信息完整 |
| 3 | 验证错误处理逻辑 | 确保错误捕获正确 |

---

## 4. 风险评估

### 4.1 兼容性风险

| 风险项 | 风险等级 | 缓解措施 |
|--------|---------|---------|
| **破坏现有错误检查** | 高 | 使用哨兵错误保持兼容 |
| | | | 分批迁移，逐步验证 |
| **错误信息丢失** | 中 | 保留 Message 字段 |
| | | | 日志中包含完整上下文 |

### 4.2 性能风险

| 风险项 | 风险等级 | 缓解措施 |
|--------|---------|---------|
| **错误类型匹配变慢** | 低 | `errors.Is` 是常量时间比较 |
| **内存分配增加** | 低 | 哨兵错误是全局单例 |

---

## 5. 简化方案

### 5.1 保留的错误类型

```go
// 1. 业务错误（需要特定处理）
var (
    ErrNotFound     = errors.New("not found")
    ErrAlreadyExists = errors.New("already exists")
    ErrInvalidInput  = errors.New("invalid input")
)

// 2. RPC 错误（需要重试逻辑）
type RPCError struct {
    Code      string
    Message   string
    Retryable bool
    Cause     error
}
```

### 5.2 删除的构造函数

**保留**（高频使用）：
- `NewNotFoundError` → 替换为 `fmt.Errorf` + `ErrNotFound`
- `NewAlreadyExistsError` → 替换为 `fmt.Errorf` + `ErrAlreadyExists`

**删除**（低频使用）：
- 大量专用构造函数（约 400+ 个）

---

## 6. 验收标准

### 6.1 功能验收

- [ ] 所有测试通过
- [ ] 错误处理逻辑保持不变
- [ ] 错误日志信息完整

### 6.2 代码质量验收

- [ ] 文件大小减少 50%+
- [ ] 构造函数数量减少 90%+
- [ ] 代码可读性提升

### 6.3 性能验收

- [ ] 错误处理性能无显著下降
- [ ] 内存使用无显著增加

---

## 7. 预估工作量

| 任务 | 预估时间 |
|------|---------|
| 分析当前使用情况 | 0.5 天 |
| 添加哨兵错误 | 0.5 天 |
| 更新错误处理逻辑 | 1 天 |
| 删除冗余构造函数 | 0.5 天 |
| 测试和验证 | 0.5 天 |
| **总计** | **3 天** |

---

**Pre 文档状态**: ⏸️ 待架构师评审

---

**文档版本**: v1.0  
**创建者**: 🤖 AI 核心开发  
**评审状态**: ⏳ 待评审
