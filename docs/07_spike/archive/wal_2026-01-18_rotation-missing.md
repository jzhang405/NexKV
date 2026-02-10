# WAL 轮换机制缺失发现

**类型**: Findings（发现）
**状态**: 📋 待讨论
**创建日期**: 2026-01-18
**标签**: wal, storage, implementation

---

## 问题描述

### 代码审查发现

当前 `MetadataWAL` 实现位于 `internal/metadata/store/wal.go:42-537`，**缺少 WAL 轮换机制**。

### 已实现的功能

| 方法 | 行号 | 状态 |
|------|------|------|
| `Append` | 86-120 | ✅ 已实现 |
| `Recover` | 123-203 | ✅ 已实现 |
| `Truncate` | 206-233 | ✅ 已实现（从头截断，非多段管理） |
| `Sync` | 236-245 | ✅ 已实现 |
| `Close` | 248-263 | ✅ 已实现 |
| `Rotate` | - | ❌ **未实现** |

### 当前实现的问题

```go
// internal/metadata/store/wal.go:42-55
type MetadataWAL struct {
    file   *os.File   // 单个文件，无限增长
    path   string     // 单一路径
    mu     sync.Mutex
    offset int64      // 当前写入位置
    closed bool
}
```

**问题分析**：
1. **文件无限增长**：没有文件大小检查和切换机制
2. **恢复时间递增**：`Recover` 需要重放整个文件
3. **磁盘空间风险**：高负载场景下可能耗尽磁盘
4. **缺少分段管理**：没有多 WAL 段（segment）的概念

---

## 设计文档参考

### 设计规范中定义的轮换机制

**文档位置**: `docs/02_design/modules/02_存储引擎设计.md:648-662`

```go
// Rotate 轮换日志
func (w *MetadataWAL) Rotate() error {
    // 1. 检查文件大小，触发轮换
    // 2. 如果未达到轮换阈值，直接返回
    // 3. 关闭当前文件
    // 4. 重命名为带时间戳的历史文件
    // 5. 创建新文件
}
```

### 设计文档中的关键设计点

| 特性 | 设计要求 | 实现状态 |
|------|----------|----------|
| **轮换阈值** | 达到大小限制自动切换 | ❌ 未实现 |
| **多段管理** | 维护多个 WAL 段 | ❌ 未实现 |
| **旧段清理** | 基于 checkpoint 清理 | ❌ 未实现 |
| **命名规范** | `wal-{timestamp}` | ❌ 未实现 |

---

## 运行时文档参考

**文档位置**: `docs/03_development/02_运行时细节文档.md:354-375`

### 设计的轮换逻辑

```go
// 检查文件大小，触发轮换
if w.offset >= w.maxSize {
    if err := w.rotate(); err != nil {
        return fmt.Errorf("WAL 轮换失败: %w", err)
    }
}
```

**自动轮换特性**：
- 达到大小限制自动切换文件
- 基于时间戳的文件命名
- 旧段自动清理

---

## 影响评估

| 维度 | 影响 |
|------|------|
| **功能完整性** | 设计与实现不一致，核心功能缺失 |
| **生产可用性** | 长时间运行存在磁盘耗尽风险 |
| **恢复性能** | WAL 越大，恢复时间越长 |
| **测试覆盖** | 设计文档中的轮换测试用例无法执行 |

---

## 建议方案

### 方案 A：完整实现 WAL 轮换

**新增接口**：
```go
// Rotate 轮换 WAL 文件
func (w *MetadataWAL) Rotate() error

// ListSegments 列出所有 WAL 段
func (w *MetadataWAL) ListSegments() ([]string, error)

// CleanupOldSegments 清理旧段
func (w *MetadataWAL) CleanupOldSegments(checkpointOffset int64) error
```

**数据结构调整**：
```go
type MetadataWAL struct {
    currentSegment *os.File
    segmentDir     string
    currentOffset  int64
    maxSize        int64      // 轮换阈值
    segments       []string   // 活跃段列表
    mu             sync.Mutex
    closed         bool
}
```

### 方案 B：最小化实现

**仅实现文件大小检查和警告**：
- 达到阈值时记录警告日志
- 触发外部监控告警
- 不自动轮换，依赖运维手动处理

---

## 待讨论事项

1. **实现范围**：是否需要完整的多段管理，还是先实现基础轮换？
2. **轮换阈值**：默认大小设置为多少？（设计文档未明确）
3. **旧段清理**：清理策略是什么？基于时间还是 checkpoint？
4. **向后兼容**：如何处理现有单文件 WAL 的迁移？
5. **优先级**：相对于其他功能，轮换机制的优先级如何？

---

## 参考文档

- **设计文档**: `docs/02_design/modules/02_存储引擎设计.md:648-662`
- **运行时细节**: `docs/03_development/02_运行时细节文档.md:354-375`
- **当前实现**: `internal/metadata/store/wal.go:42-537`
- **Phase 2 报告**: `docs/06_project_management/reports/phase2-storage.md:282-461`
