# NexKV 文档中心

> **NexKV 分布式 KV 存储系统 - 完整技术文档导航**

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
├── 06_PM/                     # 项目管理
│   ├── 01_项目初始化报告.md    # 项目初始化报告
│   ├── 02_团队分工清单.md      # AI 团队分工
│   ├── 04_风险评估与缓解.md    # 风险评估与缓解
│   ├── doc/                   # PR 文档（实际 PR 文件）
│   ├── feature/               # Feature 相关文档
│   ├── fix/                   # Fix 相关文档
│   ├── refactor/              # 重构相关文档
│   │   ├── CLAUDE.md          # 📋 文档中心（本文件）
│   │   ├── monitoring.md      # RPC 监控指标说明
│   │   └── performance-tuning.md # RPC 性能调优指南
│   └── templates/             # PR 文档模板
│       ├── feature_pr_full_template.md
│       ├── fix_pr_full_template.md
│       └── doc_pr_full_template.md
├── 07_spike/                  # 预研究（技术探索）
├── 08_api/                     # API 文档（3 个文档）
├── 09_code-review/            # Code Review 报告
└── 10_handover/              # 上下文交接文档
```

**总计**: 23 个主目录，涵盖项目全生命周期

---

## 🗂️ 文档分类

### 1️⃣ 需求与规划 (01_requirement_planning/)

> **定位**: 项目启动阶段的规划文档，定义产品目标、技术需求和项目计划

**文档清单**:
- **01_产品需求文档.md** - 产品需求文档
- **02_技术需求文档.md** - 技术需求文档
- **03_需求评审纪要.md** - 需求评审纪要
- **04_项目计划.md** - 项目计划

**适用场景**:
- 项目启动和规划
- 新成员了解项目背景
- 需求变更评估

---

### 2️⃣ 架构与详细设计 (02_design/)

> **定位**: 系统架构和详细技术设计文档，指导开发实施

**文档清单**:
- **01_系统架构设计.md** - 系统架构设计文档
- **02_详细设计文档.md** - 详细设计文档
- **03_存储引擎设计.md** - 存储引擎设计文档
- **04_API接口设计.md** - API 接口设计文档
- **05_设计评审纪要.md** - 设计评审纪要
- **06_设计决策.md** - 架构决策记录

**适用场景**:
- 开发前的技术设计
- 架构评审和决策
- 新成员了解系统架构

---

### 3️⃣ 开发与编码 (03_development/)

> **定位**: 开发过程中的规范、依赖和运行时细节文档

**文档清单**:
- **01_编码规范文档.md** - 编码规范文档
- **02_运行时细节文档.md** - 运行时细节文档
- **03_第三方依赖文档.md** - 第三方依赖文档

**适用场景**:
- 日常开发参考
- Code Review 标准
- 新成员上手

---

### 4️⃣ 测试与验收 (04_test/)

> **定位**: 测试策略、测试用例、测试报告和验收文档

**文档清单**:
- **01_测试计划文档.md** - 测试计划文档
- **02_测试用例文档.md** - 测试用例文档
- **03_测试报告.md** - 测试报告
- **04_Bug清单.md** - Bug 清单及修复报告
- **05_验收报告.md** - 验收报告

**适用场景**:
- 测试执行参考
- 质量评估
- 验收标准确认

---

### 5️⃣ 部署与运维 (05_deployment_operation/)

> **定位**: 部署指南、运维手册、版本发布文档

**文档清单**:
- **01_部署手册.md** - 部署手册
- **02_运维手册.md** - 运维手册（含监控、性能调优）

**适用场景**:
- 生产环境部署
- 日常运维操作
- 系统监控和告警
- 版本发布管理

---

### 6️⃣ 项目管理 (06_PM/)

> **定位**: 项目初始化报告、团队分工、头脑风暴和协作机制

**子目录结构**:
- **templates/**: PR 文档模板（feature、fix、doc 三种类型）
- **doc/**: 实际 PR Pre/Post 文档（按日期命名：`YYYY-MM-DD_PR-{编号}_{类型}_{标题}.md`）
- **feature/**: Feature 相关 PR 文档
- **fix/**: Fix 相关 PR 文档
- **refactor/**: 重构相关文档（含代码审查、监控、性能调优）

**文档清单**:
- **01_项目初始化报告.md** - 项目初始化报告
- **02_团队分工清单.md** - AI 团队分工
- **04_风险评估与缓解.md** - 风险评估与缓解

**适用场景**:
- 项目管理和跟踪
- 团队协作参考
- 技术决策记录

---

### 7️⃣ 预研究 (07_spike/)

> **定位**: 验证性任务（POC、原型开发），技术探索

**文档清单**:
- Spike 分析报告
- 技术方案对比
- POC 验证代码

**适用场景**:
- 技术可行性验证
- 架构方案评估
- 学习新技术/框架

---

## 📋 快速导航

### 按角色查找文档

#### 📖 架构师

- **PR 评审**: `docs/06_PM/doc/` → 查找最新 Pre/Post 文档
- **决策记录**: `docs/02_design/06_设计决策.md`
- **团队分工**: `docs/06_PM/02_团队分工清单.md`

#### 🤖 核心开发

- **编码规范**: `docs/03_development/01_编码规范文档.md`
- **详细设计**: `docs/02_design/02_详细设计文档.md`
- **运行时细节**: `docs/03_development/02_运行时细节文档.md`

#### 🧪 测试工程师

- **测试计划**: `docs/04_test/01_测试计划文档.md`
- **测试用例**: `docs/04_test/02_测试用例文档.md`
- **Bug 清单**: `docs/04_test/04_Bug清单.md`

#### 🛠️ DevOps 工程师

- **部署手册**: `docs/05_deployment_operation/01_部署手册.md`
- **运维手册**: `docs/05_deployment_operation/02_运维手册.md`

---

### 按场景查找文档

#### 🚀 项目启动

1. 阅读 `docs/06_PM/01_项目初始化报告.md` 了解项目目标
2. 阅读 `docs/01_requirement_planning/01_产品需求文档.md` 了解产品需求
3. 阅读 `docs/02_design/architecture/01_系统架构设计.md` 了解系统架构
4. 阅读 `docs/06_PM/02_团队分工清单.md` 了解团队分工

#### 💻 开发实施

1. **阅读 `workflow.md`** - 📋 **必读第一步**
   - 完整的 12 步 PR 工作流程规范
   - Pre/Post 文档模板和评审门禁
   - 完整验证清单（lint + build + test + test-race + fmt + clean）
   - 角色与职责定义
   - ⚠️ **所有 PR 必须严格按照此流程执行**

2. 阅读 `docs/03_development/01_编码规范文档.md` 了解编码规范
3. 阅读 `docs/02_design/modules/01_详细设计文档.md` 了解详细设计
4. 参考 `docs/03_development/02_运行时细节文档.md` 了解实现细节

#### 🧪 测试验证

1. 阅读 `docs/04_test/01_测试计划文档.md` 了解测试计划
2. 执行 `docs/04_test/02_测试用例文档.md` 中的测试用例
3. 查阅 `docs/04_test/03_测试报告.md` 了解测试结果

#### 🚀 部署上线

1. 阅读 `docs/05_deployment_operation/01_部署手册.md` 进行部署
2. 配置 `docs/05_deployment_operation/02_运维手册.md` 中的监控告警
3. 参考 `docs/05_deployment_operation/03_版本发布文档.md` 执行版本发布

---

## 📖 文档使用指南

### 文档命名规范

| 编号 | 文件名 | 中英文缩写 | 说明 |
|------|--------|-----------|------|
| **01_** | 产品需求文档.md | PRD | Product Requirements Document |
| **02_** | 技术需求文档.md | TRD | Technical Requirements Document |
| **01_** | 系统架构设计.md | SAD | System Architecture Document |
| **02_** | 详细设计文档.md | DDD | Detailed Design Document |
| **04_** | API接口设计.md | APID | API Interface Design |
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

### 特殊文档

| 文件名 | 说明 | 适用场景 |
|--------|------|----------|
| **workflow.md** | 开发流程规范 | 📋 **所有开发活动必读** |
| **CLAUDE.md** | 文档中心 | 项目文档导航 |

**workflow.md 核心内容**：
- ✅ **12 步骤 PR 工作流程**：从 Pre 文档到合并的完整流程
- ✅ **Pre/Post 文档模板**：标准化的文档格式
- ✅ **评审门禁机制**：Pre 和 Post 文档必须通过架构师评审
- ✅ **完整验证清单**：lint + build + test + test-race + fmt + clean
- ✅ **CI 通过通知机制**：CI 通过后通知架构师
- ✅ **角色与职责**：明确架构师、核心开发、代码审查工程师的职责
- ✅ **子目录结构**：06_PM 子目录包含 templates（PR 模板）、doc（实际 PR 文档）、feature、fix、refactor

### 文档版本管理

所有文档遵循以下版本管理规范：

```markdown
> **文档版本**: v2.2
> **创建日期**: 2026-02-12
> **最后更新**: 2026-02-12
> **维护者**: 👤 架构师 + 🤖 AI 团队
> **状态**: ✅ 已批准
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
|---------|--------|----------|----------|
| **文档问题** | 🤖 产品经理 | docs@nexkv.io | 48 小时 |
| **技术支持** | 👤 架构师 | support@nexkv.io | 24 小时 |
| **商务合作** | 🤖 产品经理 | business@nexkv.io | 1 周 |

> **🎯 团队模式**: 本项目采用**人机协作模式**。👤 **架构师**为真实人员，负责技术决策和最终评审；🤖 其他角色（产品经理、开发工程师、测试工程师、DevOps工程师）由 AI Agent 充当。

---

## 📄 许可证

本文档遵循 [AGPL-3.0](../LICENSE) 许可证。

---

**文档版本**: v3.0
**创建日期**: 2026-02-12
**最后更新**: 2026-03-01
**维护者**: 👤 架构师 + 🤖 AI 团队
**状态**: ✅ 已批准
