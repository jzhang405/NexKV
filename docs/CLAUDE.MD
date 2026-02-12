# NexKV 文档中心

> **NexKV 分布式 KV 存储系统 - 完整技术文档**

---

## 📚 文档概览

本文档目录遵循 `workflow.md` 规范组织，涵盖 NexKV 项目从需求到部署的全生命周期文档。

```
docs/
├── 01_requirement_planning/   # 需求与规划（4 个文档）
├── 02_design/                 # 架构与详细设计（6 个文档）
├── 03_development/            # 开发与编码（3 个文档）
├── 04_test/                   # 测试与验收（5 个文档）
├── 05_deployment_operation/   # 部署与运维（3 个文档）
└── 06_project_management/     # 项目管理（2 个文档）
```

**总计**: 23 个文档，涵盖项目全生命周期

---

## 🗂️ 文档分类

### 1️⃣ 需求与规划 (01_requirement_planning/)

> **定位**: 项目启动阶段的规划文档，定义产品目标、技术需求和项目计划

**文档清单**:
- **01_产品需求文档.md** - 产品需求文档
  - 产品概述和核心价值
  - 功能需求清单
  - 非功能性需求
  - 用户场景和用例

- **02_技术需求文档.md** - 技术需求文档
  - 技术选型说明
  - 技术架构要求
  - 性能指标定义
  - 技术约束和依赖

- **03_需求评审纪要.md** - 需求评审纪要
  - 评审会议记录
  - 需求决策记录
  - 需求变更历史

- **04_项目计划.md** - 项目计划
  - 里程碑规划
  - 资源分配
  - 风险评估
  - 进度跟踪

**适用场景**:
- 项目启动和规划
- 新成员了解项目背景
- 需求变更评估
- 项目范围管理

---

### 2️⃣ 架构与详细设计 (02_design/)

> **定位**: 系统架构和详细技术设计文档，指导开发实施

**文档清单**:
- **01_系统架构设计.md** - 系统架构设计文档
  - 整体架构概览
  - 分层架构设计
  - 模块划分和职责
  - 技术选型理由

- **04_一致性协议设计.md** - 一致性协议设计文档
  - Gossip 协议设计
  - Quorum 机制设计
  - 2PC 协议设计
  - 一致性模型说明

- **02_详细设计文档.md** - 详细设计文档
  - 模块详细设计
  - 接口定义
  - 数据结构设计
  - 算法实现细节

- **03_存储引擎设计.md** - 存储引擎设计文档
  - MVStore 架构
  - WAL 设计
  - 快照机制
  - 崩溃恢复

- **05_API接口设计.md** - API 接口设计文档
  - Transport 接口
  - Codec 接口
  - MetadataStore 接口
  - API 使用示例

- **06_设计评审纪要.md** - 设计评审纪要
  - 设计评审记录
  - 技术决策历史
  - 架构演进记录

**适用场景**:
- 开发前的技术设计
- 架构评审和决策
- 新成员了解系统架构
- 技术债务管理

---

### 3️⃣ 开发与编码 (03_development/)

> **定位**: 开发过程中的规范、依赖和运行时细节文档

**文档清单**:
- **01_编码规范文档.md** - 编码规范文档
  - Go 代码风格规范
  - 命名约定
  - 错误处理规范
  - 并发安全规范
  - 测试编码规范
  - Git 提交规范
  - Code Review 检查清单

- **02_运行时细节文档.md** - 运行时细节文档
  - 各模块实现细节
  - 性能指标数据
  - 配置参数说明
  - 故障排查指南
  - 6 个阶段的实施报告

- **03_第三方依赖文档.md** - 第三方依赖文档
  - 直接依赖说明
  - 间接依赖清单
  - 许可证信息
  - 安全扫描流程
  - 依赖升级策略

**适用场景**:
- 日常开发参考
- Code Review 标准
- 新成员上手
- 问题排查和诊断

---

### 4️⃣ 测试与验收 (04_test/)

> **定位**: 测试策略、测试用例、测试报告和验收文档

**文档清单**:
- **01_测试计划文档.md** - 测试计划文档
  - 测试策略（测试金字塔）
  - 测试环境搭建
  - 单元测试计划
  - 集成测试计划
  - 性能测试计划
  - CI/CD 集成

- **02_测试用例文档.md** - 测试用例文档
  - 200 个测试用例详细说明
  - 单元测试用例（120 个）
  - 集成测试用例（45 个）
  - 性能测试用例（20 个）
  - 混沌测试用例（15 个）

- **03_测试报告.md** - 测试报告
  - TLA+ 形式化验证结果
  - 测试执行统计
  - 性能基准数据
  - 代码覆盖率报告
  - 11 个 Bug 的发现和修复

- **04_Bug清单.md** - Bug 清单及修复报告
  - 30 个 Bug 的详细记录
  - 根因分析
  - 修复方案（TLA+ 和 Go）
  - 验证结果

- **05_验收报告.md** - 验收报告
  - 功能性验收结果
  - 性能指标验收
  - 质量指标验收
  - 安全性验收
  - 发布建议

**适用场景**:
- 测试执行参考
- 质量评估
- 验收标准确认
- 发布决策

---

### 5️⃣ 部署与运维 (05_deployment_operation/)

> **定位**: 部署指南、运维手册、版本发布文档

**文档清单**:
- **01_部署手册.md** - 部署手册
  - 环境准备（系统要求、Go 环境）
  - 编译构建（本地、交叉编译）
  - 配置说明（单机、集群模式）
  - 部署方式（本地、systemd、Docker、K8s）
  - 部署验证
  - 升级指南
  - 安全加固（TLS、防火墙）

- **02_运维手册.md** - 运维手册
  - 日常运维操作
  - 监控告警配置（Prometheus + Grafana）
  - 备份恢复策略
  - 故障处理流程
  - 性能优化建议
  - 容量规划指南
  - 运维最佳实践

- **03_版本发布文档.md** - 版本发布文档
  - 版本号规范（语义化版本）
  - 发布流程（7 个阶段）
  - 发布检查清单
  - CHANGELOG 管理
  - Release Notes 模板
  - 紧急发布流程
  - 版本回滚流程
  - 分支策略

**适用场景**:
- 生产环境部署
- 日常运维操作
- 系统监控和告警
- 版本发布管理

---

### 6️⃣ 项目管理 (06_project_management/)

> **定位**: 项目初始化报告、团队分工、头脑风暴和协作机制

**文档清单**:
- **01_项目初始化报告.md** - 项目初始化报告
  - 项目概述和目标
  - 组织架构和团队构成
  - 项目里程碑（7 个阶段）
  - 进度跟踪（当前 85%）
  - 资源管理
  - 质量管理
  - 风险管理
  - 发布计划

- **02_团队分工清单.md** - 团队分工与协作
  - 团队结构和成员
  - 角色职责详细定义
  - 模块分工矩阵
  - 协作机制（Code Review、技术评审）
  - 沟通机制（会议、渠道）
  - 工作量评估
  - 绩效评估

- **brainstorm/** - 头脑风暴与发现记录
  - **功能定位**：记录开发过程中的发现、问题、建议和待讨论事项
  - **命名规范**：`{tag}_YYYY-MM-DD_{topic}.md`
    - `tag`: 主题标识（如 `metadata`、`messagepack`、`doc-review`）
    - `YYYY-MM-DD`: 创建日期
    - `topic`: 简短描述（如 `implementation-findings`、`serialization-proposal`）
  - **文档类型**：
    - **Findings（发现）**：代码审查中发现的问题、实现差异、待改进项
    - **Proposals（建议）**：技术方案建议、架构改进提案、实施计划
    - **Decisions（决策）**：技术决策记录、方案选定理由、权衡分析
  - **使用场景**：
    - 代码审查发现 → 记录到 brainstorm → 团队讨论 → 形成决策
    - 技术方案讨论 → 记录到 brainstorm → 评审通过 → 实施落地
    - 文档与代码差异 → 记录到 brainstorm → 统一更新 → 避免混淆
  - **当前文档**：
    - `metadata_2026-01-18_strong-typed-proposal.md` - Metadata 强类型序列化改进方案（问题 + MessagePack 方案）
    - `merkle-tree_2026-01-18_gossip-optimization.md` - Merkle Tree 优化 Gossip 增量同步方案
    - `fix/2026-02-12_PR-041_并发安全与Phase0问题修复_Pre.md` - PR-041: 并发安全与 Phase 0 问题修复（Pre 文档）
    - `fix/2026-02-12_PR-041_并发安全与Phase0问题修复_Post.md` - PR-041: 并发安全与 Phase 0 问题修复（Post 文档）
    - `node-address-udp-cleanup_2026-02-12.md` - Spike: NodeAddress 与 UDP 遗留代码清理分析

**适用场景**:
- 项目管理和跟踪
- 团队协作参考
- 资源协调
- 绩效评估
- 技术决策记录
- 问题发现与追踪
- 方案讨论与评审

---

## 🎯 快速导航

### 按角色查找文档

#### 产品经理

- 需求了解：`01_requirement_planning/01_产品需求文档.md`
- 项目进度：`06_project_management/01_项目初始化报告.md`
- 验收确认：`04_test/05_验收报告.md`

#### 架构师

- 架构设计：`02_design/architecture/01_系统架构设计.md`
- 一致性协议：`02_design/protocols/01_一致性协议设计.md`
- 技术评审：`06_project_management/02_团队分工清单.md`

#### 开发工程师

- 编码规范：`03_development/01_编码规范文档.md`
- 详细设计：`02_design/modules/01_详细设计文档.md`
- 运行时细节：`03_development/02_运行时细节文档.md`
- 依赖管理：`03_development/03_第三方依赖文档.md`

#### 测试工程师

- 测试计划：`04_test/01_测试计划文档.md`
- 测试用例：`04_test/02_测试用例文档.md`
- Bug 管理：`04_test/04_Bug清单.md`

#### DevOps 工程师

- 部署手册：`05_deployment_operation/01_部署手册.md`
- 运维手册：`05_deployment_operation/02_运维手册.md`
- 版本发布：`05_deployment_operation/03_版本发布文档.md`

#### 项目经理

- 项目计划：`01_requirement_planning/04_项目计划.md`
- 进度跟踪：`06_project_management/01_项目初始化报告.md`
- 团队分工：`06_project_management/02_团队分工清单.md`

---

### 按场景查找文档

#### 项目启动

1. 阅读 `01_requirement_planning/01_产品需求文档.md` 了解产品需求
2. 阅读 `01_requirement_planning/02_技术需求文档.md` 了解技术需求
3. 阅读 `02_design/architecture/01_系统架构设计.md` 了解系统架构
4. 阅读 `06_project_management/02_团队分工清单.md` 了解团队分工

#### 开发实施

1. **阅读 `workflow.md`** - 📋 **必读第一步**
   - 完整的 12 步 PR 工作流程规范
   - Pre/Post 文档模板和评审门禁
   - 完整验证清单（lint + build + test + test-race + fmt + clean）
   - 角色与职责定义
   - ⚠️ **所有 PR 必须严格按照此流程执行**

2. 阅读 `03_development/01_编码规范文档.md` 了解编码规范
3. 阅读 `02_design/modules/01_详细设计文档.md` 了解详细设计
4. 参考 `03_development/02_运行时细节文档.md` 了解实现细节
5. 遵循 `03_development/01_编码规范文档.md` 的 Code Review 流程

#### 测试验证

1. 阅读 `04_test/01_测试计划文档.md` 了解测试计划
2. 执行 `04_test/02_测试用例文档.md` 中的测试用例
3. 查阅 `04_test/03_测试报告.md` 了解测试结果
4. 阅读 `04_test/04_Bug清单.md` 了解已知问题

#### 部署上线

1. 阅读 `05_deployment_operation/01_部署手册.md` 进行部署
2. 配置 `05_deployment_operation/02_运维手册.md` 中的监控告警
3. 参考 `05_deployment_operation/03_版本发布文档.md` 执行版本发布
4. 执行 `04_test/05_验收报告.md` 中的验收检查

---

## 📖 文档使用指南

### 文档命名规范

| 编号 | 文件名 | 原英文缩写 | 说明 |
|------|--------|-----------|------|
| **01_** | 产品需求文档.md | PRD | Product Requirements Document |
| **02_** | 技术需求文档.md | TRD | Technical Requirements Document |
| **03_** | 需求评审纪要.md | - | Requirements Review Minutes |
| **04_** | 项目计划.md | - | Project Plan |
| **01_** | 系统架构设计.md | SAD | System Architecture Document |
| **02_** | 详细设计文档.md | DDD | Detailed Design Document |
| **03_** | 存储引擎设计.md | SED | Storage Engine Design |
| **04_** | 一致性协议设计.md | CPD | Consistency Protocol Design |
| **05_** | API接口设计.md | APID | API Interface Design |
| **06_** | 设计评审纪要.md | - | Design Review Minutes |
| **01_** | 编码规范文档.md | CDS | Coding Standards Document |
| **02_** | 运行时细节文档.md | RDD | Runtime Details Document |
| **03_** | 第三方依赖文档.md | DRD | Dependency Requirements Document |
| **01_** | 测试计划文档.md | TPD | Test Plan Document |
| **02_** | 测试用例文档.md | TCD | Test Case Document |
| **03_** | 测试报告.md | TR | Test Report |
| **04_** | Bug清单.md | - | Bug List |
| **05_** | 验收报告.md | - | Acceptance Report |
| **01_** | 部署手册.md | DM | Deployment Manual |
| **02_** | 运维手册.md | OM | Operations Manual |
| **03_** | 版本发布文档.md | RD | Release Document |
| **01_** | 项目初始化报告.md | - | Project Init Report |
| **02_** | 团队分工清单.md | - | Team Division |

---

### 特殊文档

| 文件名 | 说明 | 适用场景 |
|--------|------|----------|
| **workflow.md** | 开发流程规范 | 📋 **所有开发活动必读** |
| **CLAUDE.md** | 文档中心 | 项目文档导航 |

**workflow.md 核心内容**：
- ✅ **12 步 PR 工作流程**：从 Pre 文档到合并的完整流程
- ✅ **Pre/Post 文档模板**：标准化的文档格式
- ✅ **评审门禁机制**：Pre 和 Post 文档必须通过用户/架构师评审
- ✅ **完整验证清单**：lint + build + test + test-race + fmt + clean
- ✅ **CI 通过通知机制**：CI 通过后通知用户/架构师（"make ci passed"）
- ✅ **角色与职责**：明确架构师、核心开发、代码审查工程师的职责

**⚠️ 重要**：
- 所有 PR 必须严格按照 `workflow.md` 执行
- Pre 文档未通过评审前，禁止启动开发
- Post 文档未通过评审前，禁止推送代码
- 提交前所有验证必须通过（lint + build + test + test-race + fmt + clean）
- CI 通过后必须通知用户/架构师，禁止自动合并

---

### 文档版本管理

所有文档遵循以下版本管理规范：

```markdown
> **文档版本**: v1.0
> **创建日期**: 2026-01-18
> **最后更新**: 2026-01-18
> **维护者**: NexKV 开发团队
> **状态**: ✅ 已批准 / 🔄 草稿 / ⚠️ 需要评审
```

**状态说明**:
- ✅ **已批准**: 文档已通过评审，正式发布
- 🔄 **草稿**: 文档正在编写中
- ⚠️ **需要评审**: 文档待评审

---

### 文档更新流程

```
1. 创建或修改文档
   ↓
2. 本地预览和检查
   ↓
3. 提交 Pull Request
   ↓
4. 代码审查（至少 1 个 Approval）
   ↓
5. 合并到主分支
   ↓
6. 更新文档版本号和状态
```

---

## 🔗 外部资源

### 官方文档

- **Go 语言**: https://golang.org/doc/
- **Go Modules**: https://golang.org/ref/mod
- **MessagePack**: https://msgpack.org/
- **Prometheus**: https://prometheus.io/docs/
- **Grafana**: https://grafana.com/docs/

### 相关项目

- **Lealone**: https://github.com/lealone/lealone
- **Cassandra**: https://cassandra.apache.org/doc/
- **etcd**: https://etcd.io/docs/

### 技术参考

- **TLA+**: https://lamport.azurewebsites.net/tla/tla.html
- **分布式系统**: https://www.distributedsystemscourse.com/
- **Go 最佳实践**: https://go.dev/doc/effective_go

---

## 📝 文档贡献

### 如何贡献

1. **发现问题**: 在 GitHub Issues 中提交问题
2. **提出改进**: 在 GitHub Discussions 中讨论
3. **提交修改**: Fork 项目，创建分支，提交 PR

### 贡献指南

- 文档格式：Markdown
- 代码示例：必须可运行
- 图片资源：放在 `docs/images/` 目录
- 链接检查：确保所有链接有效

---

## 📞 联系方式

| 联系事项 | 负责人 | 邮箱 | 响应时间 |
|---------|--------|------|---------|
| **文档问题** | 🤖 产品经理 | docs@nexkv.io | 48 小时 |
| **技术支持** | 👤 架构师 | support@nexkv.io | 24 小时 |
| **商务合作** | 🤖 产品经理 | business@nexkv.io | 1 周 |

> **🎯 团队模式**: 本项目采用**人机协作模式**。👤 **架构师**为真实人员，负责技术决策和最终评审；🤖 其他角色（产品经理、开发工程师、测试工程师、DevOps工程师）由 AI Agent 充当。

---

## 📄 许可证

本文档遵循 [AGPL-3.0](../LICENSE) 许可证。

---

**文档版本**: v1.0
**创建日期**: 2026-01-18
**最后更新**: 2026-01-18
**维护者**: 👤 架构师 + 🤖 AI 团队
**状态**: ✅ 已批准
