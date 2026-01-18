# Design 文档引用问题发现

**类型**: Findings（发现）
**状态**: ✅ 已全部修复
**创建日期**: 2026-01-18
**标签**: doc-review, design, references

---

## 问题描述

对 `docs/02_design/` 目录下的设计文档进行引用审查，发现多处文档内部引用使用了**不存在的缩写文件名**，导致链接失效。

---

## 问题清单

### ✅ 已修复

### 1. `02_存储引擎设计.md` - 缩写文件名引用错误

**文件位置**: `docs/02_design/modules/02_存储引擎设计.md:983-991`

| 错误引用 | 实际文件 | 状态 |
|---------|---------|------|
| `docs/02_design/DDD.md` | `../01_详细设计文档.md` | ✅ 已修复 |
| `docs/02_design/APID.md` | `../05_API接口设计.md` | ✅ 已修复 |
| `docs/02_design/SAD.md` | `../architecture/01_系统架构设计.md` | ✅ 已修复 |
| `docs/02_design/CPD.md` | `../protocols/01_一致性协议设计.md` | ✅ 已修复 |
| `docs/01_requirement_planning/TRD.md` | `../../01_requirement_planning/02_技术需求文档.md` | ✅ 已修复 |

---

### ❌ 待修复

### 2. `06_设计评审纪要.md` - 多处缩写引用错误

**文件位置**: `docs/02_design/06_设计评审纪要.md`

| 行号 | 错误引用 | 实际文件 |
|------|---------|---------|
| 44-48 | `SAD.md`, `CPD.md`, `APID.md`, `DDD.md`, `SED.md` | 见下表 |
| 432-436 | `SAD.md`, `CPD.md`, `APID.md`, `DDD.md`, `SED.md` | 见下表 |
| 440-442 | `PRD.md`, `TRD.md`, `project_plan.md` | 见下表 |

**引用对照**：

| 错误缩写 | 正确文件 |
|---------|---------|
| `SAD.md` | `architecture/01_系统架构设计.md` |
| `CPD.md` | `protocols/01_一致性协议设计.md` |
| `APID.md` | `05_API接口设计.md` |
| `DDD.md` | `modules/01_详细设计文档.md` |
| `SED.md` | `modules/02_存储引擎设计.md` |
| `PRD.md` | `../01_requirement_planning/01_产品需求文档.md` |
| `TRD.md` | `../01_requirement_planning/02_技术需求文档.md` |
| `project_plan.md` | `../01_requirement_planning/04_项目计划.md` |

---

### 3. `05_API接口设计.md` - 缩写引用错误

**文件位置**: `docs/02_design/05_API接口设计.md:1554-1559`

| 行号 | 错误引用 | 实际文件 |
|------|---------|---------|
| 1554 | `02_design/SAD.md` | `architecture/01_系统架构设计.md` |
| 1555 | `02_design/CPD.md` | `protocols/01_一致性协议设计.md` |
| 1556 | `02_design/DDD.md` | `modules/01_详细设计文档.md` |
| 1559 | `docs/01_requirement_planning/TRD.md` | `../01_requirement_planning/02_技术需求文档.md` |

---

### 4. `01_详细设计文档.md` - 缩写引用错误

**文件位置**: `docs/02_design/modules/01_详细设计文档.md:999-1004`

| 行号 | 错误引用 | 实际文件 |
|------|---------|---------|
| 999 | `02_design/SAD.md` | `../architecture/01_系统架构设计.md` |
| 1000 | `02_design/CPD.md` | `../protocols/01_一致性协议设计.md` |
| 1001 | `02_design/APID.md` | `../05_API接口设计.md` |
| 1004 | `docs/01_requirement_planning/TRD.md` | `../../01_requirement_planning/02_技术需求文档.md` |

---

### 5. `modules/README.md` - 缩写路径引用错误

**文件位置**: `docs/02_design/modules/README.md:71-73`

| 行号 | 错误引用 | 实际文件 |
|------|---------|---------|
| 71 | `../architecture/SAD.md` | `../architecture/01_系统架构设计.md` |
| 72 | `../protocols/CPD.md` | `../protocols/01_一致性协议设计.md` |
| 73 | `../APID.md` | `../05_API接口设计.md` |

---

### ✅ 验证无误

以下文件引用经验证正确：

| 文件 | 验证结果 |
|------|---------|
| `README.md` | 所有引用路径正确 |
| `01_一致性协议设计.md` | 引用路径正确 |
| `03_WAL崩溃恢复.md` | 引用路径正确 |
| `06_时钟漂移补偿.md` | 引用路径正确 |
| `07_树形协调器拓扑同步.md` | 引用路径正确 |
| `08_树形协调器自动发现与心跳.md` | 引用路径正确 |
| `architecture/README.md` | 仅说明文字，无实际链接 |

---

### 2. ✅ `README.md` - 引用正确

**文件位置**: `docs/02_design/README.md:65-67`

| 引用 | 验证结果 |
|------|---------|
| `../../00_overview/01_核心架构概念.md` | ✅ 存在 |
| `architecture/01_系统架构设计.md` | ✅ 存在 |
| `modules/01_详细设计文档.md` | ✅ 存在 |

---

### 3. ✅ `01_一致性协议设计.md` - 引用正确

**文件位置**: `docs/02_design/protocols/01_一致性协议设计.md:22`

| 引用 | 验证结果 |
|------|---------|
| `../00_overview/02_一致性级别定义.md` | ✅ 存在 |

---

## 根本原因分析

1. **文件命名不一致**：设计文档使用了完整的中文文件名（如 `01_系统架构设计.md`），但内部引用仍使用了旧的英文缩写（如 `SAD.md`）

2. **文档重构未同步更新**：在文档组织重构时，文件名已更新但相互引用未同步修改

3. **缺少引用验证**：文档更新后没有进行链接有效性检查

---

## 修复建议

### 方案 A：更新所有引用为完整文件名（推荐）

**修复后的引用**：

```markdown
### 输入文档
- `modules/01_详细设计文档.md` - 详细设计文档
- `05_API接口设计.md` - API 接口设计文档

### 输出文档
- `architecture/01_系统架构设计.md` - 系统架构设计文档
- `protocols/01_一致性协议设计.md` - 一致性协议设计文档

### 参考文档
- `../01_requirement_planning/02_技术需求文档.md` - 技术需求文档
```

**理由**：
- 与实际文件名一致
- 相对路径正确
- 易于维护

### 方案 B：创建缩写文件名的软链接（不推荐）

为每个文档创建英文缩写的软链接，例如：
```bash
cd docs/02_design
ln -s architecture/01_系统架构设计.md SAD.md
ln -s protocols/01_一致性协议设计.md CPD.md
```

**不推荐理由**：
- 增加维护复杂度
- Git 不跟踪软链接
- 跨平台兼容性问题

---

## 修复记录

| 文件 | 行号 | 修复内容 | 状态 |
|------|------|---------|------|
| `02_存储引擎设计.md` | 983-991 | 更新 5 处引用 | ✅ 已完成 |
| `06_设计评审纪要.md` | 44-48, 432-436, 440-442 | 更新 11 处引用 | ✅ 已完成 |
| `05_API接口设计.md` | 1554-1559 | 更新 4 处引用 | ✅ 已完成 |
| `01_详细设计文档.md` | 999-1004 | 更新 4 处引用 | ✅ 已完成 |
| `modules/README.md` | 71-73 | 更新 3 处引用 | ✅ 已完成 |

---

## 参考文档

- **问题文件**: `docs/02_design/modules/02_存储引擎设计.md:983-991`
- **文件结构**: `docs/02_design/README.md:10-34`
