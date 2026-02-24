# P0-2 技术债务：AsyncOperation 实现分层重构

**日期**：2026-02-24
**状态**：推迟（记录为技术债务）
**优先级**：中（非阻塞）

---

## 问题描述

### 当前状态

`internal/domain/service/rpc_async_impl.go` 包含 850 行实现代码，违反 DDD 分层架构原则：

- **领域层应只定义接口和领域概念**
- **具体实现应在基础设施层**
- **当前实现包括**：
  - `asyncOpImpl[T]` 结构体及方法（200+ 行）
  - 工厂函数（NewAsyncCall, NewAsyncBroadcast 等，300+ 行）
  - 辅助函数（submitTask, validateRPCAndConfig 等，50+ 行）
  - `RPCAsyncAdapter` 适配器（60+ 行）

### 违反的原则

1. **分层架构原则**：领域层包含基础设施关注点
2. **单一职责原则**：领域层承担了实现职责
3. **依赖倒置原则**：部分实现直接依赖并发原语

---

## 为什么推迟修复

### 1. 复杂性高

**循环导入问题**：
```
领域层 (domain/service) 定义接口
    ↓ 导入
基础设施层 (infrastructure/rpc) 实现接口
    ↓ 导入
领域层（需要使用领域类型）
    ↑ 循环导入！
```

**解决方案需要**：
- 重新设计领域类型的位置
- 可能需要引入独立的共享类型包
- 需要更新所有调用方的 import 路径

### 2. 影响范围大

**受影响的代码**：
- `internal/domain/service/rpc_async.go` - 接口定义
- `internal/domain/service/rpc_async_impl.go` - 实现（850 行）
- 所有使用 `NewAsyncCall` 等工厂函数的代码
- 测试文件（需要更新 import）

**预计工作量**：
- 代码重构：4-6 小时
- 测试验证：2-3 小时
- 文档更新：1 小时
- **总计**：7-10 小时

### 3. 风险评估

**当前风险**：低
- 代码功能正常
- 测试全部通过
- 不影响生产环境

**重构风险**：中
- 可能引入新的 bug
- 需要大量回归测试
- 可能影响现有 API 的向后兼容性

---

## 推荐的解决方案

### 方案 A：渐进式重构（推荐）

**阶段 1：抽象核心接口**（2 小时）
```go
// internal/domain/model/async.go
type Future[T any] interface {
    Await(ctx context.Context) (T, error)
    OnComplete(callback func(T, error)) string
}
```

**阶段 2：创建基础设施实现**（3 小时）
```go
// internal/infrastructure/async/future.go
type futureImpl[T any] struct { ... }
func NewFuture[T any](...) Future[T] { ... }
```

**阶段 3：迁移适配器**（2 小时）
```go
// internal/infrastructure/rpc/async_adapter.go
type RPCAsyncAdapter struct { ... }
func NewRPCAsyncAdapter(...) service.RPCAsync { ... }
```

**阶段 4：清理和验证**（3 小时）
- 删除领域层实现
- 更新所有 import 路径
- 运行完整测试套件

### 方案 B：引入应用层

**架构改进**：
```
Domain Layer (接口)
    ↑
Application Layer (编排/工厂)
    ↑
Infrastructure Layer (实现)
```

**优点**：
- 更清晰的职责分离
- 工厂函数放在应用层
- 避免循环导入

**缺点**：
- 需要新增应用层目录
- 架构复杂度增加

---

## 影响分析

### 当前影响（不修复）

**优点**：
- ✅ 功能正常，测试通过
- ✅ 无生产风险
- ✅ 团队可继续开发其他功能

**缺点**：
- ❌ 违反 DDD 原则
- ❌ 领域层职责不清晰
- ❌ 可维护性降低（长期）

### 未来影响（修复后）

**优点**：
- ✅ 符合 DDD 分层架构
- ✅ 领域层职责清晰
- ✅ 更好的可测试性
- ✅ 更容易替换实现

**缺点**：
- ⚠️ 需要团队学习新的 import 路径
- ⚠️ 可能有短暂的 API 不稳定期

---

## 行动计划

### 短期（1-2 周）

1. **记录技术债务** ✅
   - 创建此文档
   - 在代码中添加 TODO 注释
   - 添加到技术债务看板

2. **团队讨论**
   - 评估优先级
   - 确定修复时间
   - 分配责任人

### 中期（1-2 个月）

3. **方案评估**
   - 详细评估方案 A vs 方案 B
   - 进行技术预研（Spike）
   - 选择最终方案

4. **制定重构计划**
   - 拆分为多个小任务
   - 评估每个任务的工作量
   - 制定测试策略

### 长期（3-6 个月）

5. **执行重构**
   - 按阶段执行
   - 每个阶段独立测试
   - 逐步替换旧代码

6. **清理和文档**
   - 删除旧代码
   - 更新架构文档
   - 培训团队

---

## 相关文档

- **DDD 架构审查**：`docs/09_code-review/2026-02-24_DDD_Architecture_Review_PR-073.md`
- **修复进度报告**：`docs/06_PM/doc/2026-02-24_PR-073_fix_progress.md`
- **代码注释**：`internal/domain/service/rpc_async_impl.go` 头部

---

## 参考

### DDD 最佳实践

- **Domain Layer**: 业务逻辑、领域模型、领域服务接口
- **Infrastructure Layer**: 技术实现、外部服务集成、持久化
- **Application Layer**: 用例编排、事务管理、DTO 转换

### 类似项目

- **go-kit**: 清晰的三层架构
- **go-clean-arch**: 典型的 DDD 分层示例
- **Enterprise Go**: DDD 战术设计实现

---

**维护者**：NexKV 架构团队
**最后更新**：2026-02-24
**下次审查**：2026-03-24
