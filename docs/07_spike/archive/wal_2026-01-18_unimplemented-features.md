# WAL 崩溃恢复未实现功能

**类型**: Findings（发现）
**状态**: 📋 待讨论
**创建日期**: 2026-01-18
**标签**: wal, storage, checkpoint, recovery

---

## 问题描述

审查 `docs/02_design/modules/03_WAL崩溃恢复.md` 设计文档与实际实现，发现核心崩溃恢复功能已实现，但关键优化功能缺失。

---

## 实现状态对比

### ✅ 已实现功能

| 功能 | 设计要求 | 代码位置 | 状态 |
|------|---------|---------|------|
| **WAL 接口** | `Append`, `Recover`, `Truncate`, `Sync`, `Close` | `internal/metadata/store/wal.go:42-53` | ✅ 完整实现 |
| **二进制格式** | Header (12B) + Data + Checksum (4B) | `internal/metadata/store/wal.go:21-35` | ✅ 已实现 |
| **Recover() 恢复** | 解析 WAL 并重放所有操作 | `internal/metadata/store/wal.go:123-203` | ✅ 已实现 |
| **CRC32 校验** | IEEE CRC32 校验数据完整性 | `internal/metadata/store/wal.go:174-178` | ✅ 已实现 |
| **错误处理** | 校验和不匹配时跳过条目 | `internal/metadata/store/wal.go:176-177` | ✅ 已实现 |
| **HLC 时间戳** | 混合逻辑时钟支持 | `internal/metadata/store/wal.go:268-271` | ✅ 已实现 |

**Recover() 核心流程**：
```go
// internal/metadata/store/wal.go:123-203
func (w *MetadataWAL) Recover() ([]*WALEntry, error) {
    // 1. 重新打开文件从头读取
    // 2. 逐条解析 Header (12 bytes)
    // 3. 读取 Entry Data (Key + Value + Timestamp)
    // 4. 读取并验证 Checksum (4 bytes)
    // 5. 解码 WALEntry
    // 6. 返回所有条目用于重放
}
```

---

### ❌ 未实现功能

#### 1. Checkpoint 机制

**设计文档**: `docs/02_design/modules/03_WAL崩溃恢复.md:207-275`

**设计目标**：
```go
// SnapshotManager 快照管理接口
type SnapshotManager interface {
    Create(store MVStore) error           // 创建快照
    List() ([]string, error)               // 列出所有快照
    Restore(snapshotName string) ([]byte, error)  // 从快照恢复
    Delete(snapshotName string) error      // 删除快照
}
```

**当前状态**：
- ❌ 接口已定义但未实现恢复逻辑
- ❌ 每次崩溃恢复需重放整个 WAL 文件
- ❌ WAL 文件越大，恢复时间越长

**影响评估**：
| 维度 | 影响 |
|------|------|
| **恢复性能** | O(n) 线性增长，n 为 WAL 条目数 |
| **磁盘占用** | WAL 无限增长，无法清理旧日志 |
| **生产可用性** | 长时间运行后恢复时间不可接受 |

#### 2. WAL 轮换机制

**设计文档**: `docs/02_design/modules/02_存储引擎设计.md:648-718`

**设计目标**：
```go
// Rotate 轮换日志
func (w *MetadataWAL) Rotate() error {
    // 1. 检查文件大小
    // 2. 达到阈值时关闭当前文件
    // 3. 重命名为带时间戳的历史文件
    // 4. 创建新文件
    // 5. 重置序列号
}
```

**当前状态**：
- ❌ 单文件无限增长
- ❌ 无法自动清理旧 WAL
- ❌ 磁盘耗尽风险

**设计参数**：
| 参数 | 元数据 WAL | 业务 WAL |
|------|-----------|---------|
| **单文件大小限制** | 100MB | 1GB |
| **保留时间** | 30 天 | 3 个月 |

**参见**: `docs/06_project_management/brainstorm/wal_2026-01-18_rotation-missing.md`

#### 3. Codec 接口扩展（MessagePack）

**设计文档**: `docs/02_design/modules/03_WAL崩溃恢复.md:534-583`

**设计目标**：
```go
// Codec 编码器接口
type Codec interface {
    Encode(v interface{}) ([]byte, error)
    Decode(data []byte, v interface{}) error
    Name() string
}

// MessagePack Codec（默认）
type MessagePackCodec struct{}
```

**当前状态**：
- ❌ 当前使用 gob 编码（Go 专有）
- ❌ 不支持跨语言访问
- ❌ 缺少可插拔编码器接口

**影响**：
- 无法用其他语言读取 WAL 文件
- 调试困难（无法用 Python 工具解析）
- 团队协作受限

---

## 实现建议

### 优先级 P0：Checkpoint 机制

**理由**：避免 WAL 无限增长和恢复时间爆炸

**实现方案**：

```go
// 1. 创建 Checkpoint
func (s *MVStoreImpl) CreateCheckpoint() error {
    // 1. 获取当前所有数据
    snapshot := make(map[string][]byte)
    s.memTable.Range(func(key, value interface{}) bool {
        mv := value.(*memTableValue)
        if !mv.Deleted {
            snapshot[key.(string)] = mv.Data
        }
        return true
    })

    // 2. 序列化快照
    data, err := json.Marshal(snapshot)
    if err != nil {
        return err
    }

    // 3. 写入快照文件
    snapshotName := fmt.Sprintf("snapshot-%d.json", time.Now().Unix())
    snapshotPath := filepath.Join(s.config.DataDir, "snapshots", snapshotName)
    if err := os.WriteFile(snapshotPath, data, 0644); err != nil {
        return err
    }

    // 4. 写入 Checkpoint 条目到 WAL
    checkpointEntry := &WALEntry{
        Type:      WALTypeCheckpoint,
        Timestamp: s.clock.Now(),
        Key:       "__checkpoint__",
        Value:     []byte(snapshotName),
    }
    return s.wal.Append(checkpointEntry)
}

// 2. 优化 Recover，跳过 Checkpoint 之前的日志
func (w *MetadataWAL) RecoverFromCheckpoint() ([]*WALEntry, error) {
    // 1. 查找最新的 Checkpoint 条目
    // 2. 从 Checkpoint 位置开始恢复
    // 3. 加载快照数据
    // 4. 重放 Checkpoint 之后的 WAL 条目
}
```

### 优先级 P1：WAL 轮换机制

**理由**：防止磁盘耗尽

**实现方案**：

```go
const (
    DefaultMaxFileSize = 100 * 1024 * 1024  // 100MB
)

func (w *MetadataWAL) checkRotate() error {
    info, err := w.file.Stat()
    if err != nil {
        return err
    }

    if info.Size() >= DefaultMaxFileSize {
        return w.Rotate()
    }
    return nil
}

func (w *MetadataWAL) Rotate() error {
    // 1. 关闭当前文件
    // 2. 重命名为 wal-<timestamp>.log
    // 3. 创建新文件
    // 4. 清理旧段（基于 Checkpoint）
}
```

### 优先级 P2：Codec 接口

**理由**：提升调试和跨语言能力

**实现方案**：

```go
type Codec interface {
    Encode(v interface{}) ([]byte, error)
    Decode(data []byte, v interface{}) error
    Name() string
}

type MetadataWAL struct {
    file   *os.File
    codec  Codec
    // ...
}
```

---

## 待讨论事项

1. **Checkpoint 触发策略**：
   - 定时触发（如每小时）
   - WAL 条目数阈值（如 10000 条）
   - 手动触发
   - 混合策略？

2. **旧 WAL 清理策略**：
   - Checkpoint 后立即删除？
   - 保留最近 N 个 Checkpoint？
   - 保留时间策略（30 天）？

3. **WAL 轮换阈值**：
   - 元数据 WAL：100MB 是否合适？
   - 业务 WAL：1GB 是否合适？
   - 是否需要可配置？

4. **Codec 选择**：
   - 是否需要立即实现 MessagePack？
   - 是否保持二进制格式兼容性？
   - 如何处理历史 WAL 文件迁移？

---

## 参考文档

- **设计文档**: `docs/02_design/modules/03_WAL崩溃恢复.md`
- **存储引擎设计**: `docs/02_design/modules/02_存储引擎设计.md:648-718`
- **当前实现**: `internal/metadata/store/wal.go`
- **WAL 轮换缺失**: `docs/06_project_management/brainstorm/wal_2026-01-18_rotation-missing.md`
