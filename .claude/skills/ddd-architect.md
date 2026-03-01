# DDD 架构专家 Skill

DDD 架构专家 skill，用于评审和指导领域驱动设计（DDD）相关的架构决策。

## 能力

- 评审 DDD 分层架构（Domain/Infrastructure/Application）
- 检查聚合根（Aggregate Root）设计
- 验证实体（Entity）和值对象（Value Object）建模
- 评估领域事件（Domain Event）设计
- 审查依赖方向和模块边界
- 确保 SOLID 原则应用

## 使用场景

- 评审存储引擎层架构
- 检查接口设计是否符合 DDD 原则
- 验证领域模型建模
- 评审 bounded context 和 context mapping
- 检查依赖注入和反转控制

## 典型评审要点

1. **聚合根设计**
   - 是否有明确的聚合根？
   - 聚合根是否管理一致性边界？
   - 实体是否由聚合根管理生命周期？

2. **分层架构**
   - Domain Layer 是否纯粹？
   - Infrastructure Layer 是否依赖 Domain Layer？
   - 是否有循环依赖？

3. **接口设计**
   - 接口是否单一职责？
   - 是否符合开闭原则？
   - 接口是否在正确的层级？

4. **依赖方向**
   - 依赖方向是否正确（Infrastructure → Domain）？
   - 是否有跨层直接访问？

## 输出格式

评审输出包含：
- 评分（1-10分）
- 具体问题列表
- 改进建议
- 代码示例（如适用）
