# PR-003: WAL Checkpoint 机制实现 - Pre 文档

> **PR 类型**: Feature (新功能)
> **优先级**: P0 - 生产就绪关键功能
> **预计工期**: 3-5 天
> **创建日期**: 2026-01-19
> **文档版本**: V1.0 (Pre)

---

## 第一部分：前置部分（Pre）

### 1.1 需求背景

**问题陈述**：

当前 WAL (Write-Ahead Log) 实现存在严重缺陷：

1. **WAL 无限增长**：WAL 文件持续追加写入，无定期清理机制
2. **恢复时间过长**：崩溃恢复需要重放完整 WAL，随着 WAL 增长线性增加
3. **磁盘空间风险**：长时间运行后 WAL 文件可能占满磁盘
4. **生产环境不可用**：当前实现仅适合开发/测试，不适合生产部署

**影响范围**：

- **数据可靠性**：WAL 过大时恢复失败风险增加
- **系统性能**：大文件 I/O 影响正常写入性能
- **运维成本**：需要手动清理 WAL，增加运维负担

**业务场景**：

NexKV 作为分布式 KV 存储系统，在生产环境中需要 7×24 小时稳定运行：
- 数据目录磁盘容量有限（通常 100GB-1TB）
- 崩溃恢复时间要求 < 30 秒（当前可能长达数分钟）
- 要求自动化运维，减少人工干预

### 1.2 功能目标

**主要目标**：

1. ✅ **实现 CreateCheckpoint() 方法**
   - 定期创建 MVStore 一致性快照
   - 快照完成后清理已应用的 WAL 条目
   - 支持增量快照（可选，P2）

2. ✅ **实现 RecoverFromCheckpoint() 方法**
   - 崩溃恢复优先从最新 Checkpoint 加载
   - 重放 Checkpoint 之后的 WAL 条目
   - 将恢复时间从分钟级降至秒级（目标 < 10 秒）

3. ✅ **Checkpoint 调度机制**
   - 基于时间间隔（默认 1 小时）
   - 基于 WAL 大小（默认 100MB）
   - 手动触发 Checkpoint（管理接口）

4. ✅ **Checkpoint 验证机制**
   - Checkpoint 完成后校验数据一致性
   - 损坏的 Checkpoint 自动回退到上一个版本
   - 保留最近 N 个 Checkpoint（默认 3 个）

**非目标（本次不实现）**：

- ❌ WAL 轮换机制（独立 PR-004）
- ❌ 增量 Checkpoint（P2 优化）
- ❌ 分布式 Checkpoint（仅单机 Checkpoint）

### 1.3 验收标准

**功能验收**：

1. **Checkpoint 创建**：
   - [ ] 能够成功创建一致性快照
   - [ ] Checkpoint 文件格式正确（Protobuf 定义）
   - [ ] Checkpoint 完成后清理旧 WAL 条目
   - [ ] Checkpoint 失败不影响 MVStore 正常运行

2. **Checkpoint 恢复**：
   - [ ] 崩溃恢复优先加载最新 Checkpoint
   - [ ] 正确重放 Checkpoint 之后的 WAL
   - [ ] 恢复后的数据与崩溃前完全一致
   - [ ] 恢复时间 < 10 秒（100 万条记录）

3. **Checkpoint 调度**：
   - [ ] 定时触发（1 小时）
   - [ ] 基于大小触发（100MB WAL）
   - [ ] 手动触发接口工作正常

4. **Checkpoint 验证**：
   - [ ] Checkpoint 数据完整性校验
   - [ ] 损坏 Checkpoint 自动回退
   - [ ] 保留最近 3 个 Checkpoint

**性能验收**：

1. **Checkpoint 创建性能**：
   - 创建时间 < 5 秒（100 万条记录）
   - 期间不影响正常读写（阻塞 < 100ms）

2. **Checkpoint 恢复性能**：
   - 恢复时间 < 10 秒（100 万条记录）
   - 比纯 WAL 恢复快 10 倍以上

3. **磁盘空间**：
   - Checkpoint 文件大小约为 MVStore 的 50%
   - WAL 清理后释放 80% 以上空间

**测试覆盖率**：

- 单元测试覆盖率 > 85%
- 集成测试覆盖 10+ 场景
- 性能测试验证恢复时间

### 1.4 技术方案设计

#### 架构设计

```mermaid
flowchart TD
    subgraph WAL ["WAL 组件"]
        A[WAL Writer]
        B[WAL Reader]
        C[WAL 文件]
    end

    subgraph Checkpoint ["Checkpoint 组件"]
        D[Checkpoint Manager]
        E[Snapshot Writer]
        F[Snapshot Reader]
        G[Checkpoint 文件]
    end

    subgraph MVStore ["MVStore 组件"]
        H[MemTable]
        I[DiskFile]
    end

    D -->|触发条件| A
    D -->|创建快照| H
    E -->|写入| G
    F -->|读取| G
    B -->|恢复| C
    F -->|加载| D
    D -->|重放 WAL| B

    style D fill:#e1f5ff
    style G fill:#fff4e6
```

#### Protobuf Schema 设计

**文件**: `internal/metadata/proto/checkpoint.proto`

```protobuf
syntax = "proto3";
package nexkv.metadata;

option go_package = "./;proto";

// CheckpointMetadata Checkpoint 元数据
message CheckpointMetadata {
  uint64 checkpoint_id = 1;        // Checkpoint ID（单调递增）
  uint64 timestamp = 2;             // 创建时间戳（Unix 毫秒）
  uint64 last_wal_offset = 3;       // Checkpoint 位置对应的 WAL 偏移量（字节）
  uint32 entry_count = 4;           // 快照包含的条目数量
}

// CheckpointData Checkpoint 数据
message CheckpointData {
  map<string, bytes> data = 1;      // 键值对数据
  uint64 version = 2;                // MVStore 版本号
}
```

**说明**：`codec_type` 从 Protobuf 中移除，放在文件头中（与 Transport 保持一致）。

---

#### Checkpoint 文件格式设计（统一两段式）

**设计原则**：与 Transport 帧保持一致的简洁风格

| 格式 | Transport 帧 | WAL 条目 | Checkpoint 文件 | Snapshot 文件 |
|------|-------------|---------|-----------------|----------------|
| **文件头** | Magic + Type + Codec + Length + CRC | **Magic + Type + KeyLen + ValueLen + OldValueLen + TimestampLen + CRC** | **Magic + Version + Codec + Length + CRC** | **Magic + Version + Codec + Length + CRC** |
| **数据区** | 变长 | 变长 | 变长（Protobuf） | 变长（Protobuf） |
| **文件尾** | 无 | 无 | 无 | 无 |

##### 文件格式

```mermaid
flowchart TD
    subgraph CheckpointFile["Checkpoint File - 两段式格式"]
        direction TB

        subgraph Header["文件头（固定 16B）"]
            direction LR
            H1["Magic<br/>4B<br/>'NxCP'"]
            H2["Version<br/>2B<br/>uint16"]
            H3["Codec Type<br/>2B<br/>uint16"]
            H4["Length<br/>4B<br/>uint32"]
            H5["CRC<br/>4B<br/>CRC32"]
        end

        subgraph Data["数据区（变长，Protobuf 编码）"]
            direction TB
            D1["CheckpointMetadata<br/>checkpoint_id, timestamp,<br/>last_wal_offset, entry_count"]
            D2["CheckpointData<br/>map<string, bytes>, version"]
        end

        Header --> Data
    end

    style Header fill:#e1f5ff
    style Data fill:#fff4e6
    style H3 fill:#f3e5f5
```

##### 字段详细说明

| 部分 | 字段 | 类型 | 大小 | 说明 |
|------|------|------|------|------|
| **🔵 文件头** | Magic | bytes | 4 B | 魔术字 `"NxCP"`（NexKV Checkpoint） |
| | Version | uint16 | 2 B | 格式版本号（当前 = 1） |
| | Codec Type | uint16 | 2 B | 编解码器类型（与 types.CodecType 对应） |
| | Length | uint32 | 4 B | 数据区字节长度 |
| | CRC | uint32 | 4 B | 校验和 CRC32(Magic + Version + Codec + Length + Data) |
| **🟠 数据区** | CheckpointMetadata + Data | message | 变长 | Protobuf 编码（Length 字节） |

**Codec Type 枚举值**：
- `1` = MessagePack
- `2` = JSON
- `3` = Protobuf（默认）

**一致性改进**：
- ✅ 与 Transport 帧格式一致（Magic + Type/Version + Codec + Length + CRC）
- ✅ 简洁的两段式结构（Header + Data）
- ✅ CRC 覆盖整个文件（Header + Data）

---

#### WAL 条目格式设计（统一两段式）

##### WAL 条目格式

```mermaid
flowchart TD
    subgraph WALEntry["WAL Entry - 两段式格式"]
        direction TB

        subgraph Header["条目头（固定 24B）"]
            direction LR
            H1["Magic<br/>4B<br/>'NxWL'"]
            H2["Type<br/>2B<br/>uint16"]
            H3["KeyLen<br/>4B<br/>uint32"]
            H4["ValueLen<br/>4B<br/>uint32"]
            H5["OldValueLen<br/>4B<br/>uint32"]
            H6["TimestampLen<br/>2B<br/>uint16"]
            H7["CRC<br/>4B<br/>CRC32"]
        end

        subgraph Data["数据区（变长）"]
            direction TB
            D1["Key<br/>KeyLen"]
            D2["Value<br/>ValueLen"]
            D3["OldValue<br/>OldValueLen"]
            D4["Timestamp<br/>TimestampLen (HLC)"]
        end

        Header --> Data
    end

    style Header fill:#e1f5ff
    style Data fill:#fff4e6
    style H1 fill:#f3e5f5
```

##### 字段详细说明

| 部分 | 字段 | 类型 | 大小 | 说明 |
|------|------|------|------|------|
| **🔵 条目头** | Magic | bytes | 4 B | 魔术字 `"NxWL"`（NexKV WAL） |
| | Type | uint16 | 2 B | 操作类型（PUT/DELETE） |
| | KeyLen | uint32 | 4 B | Key 长度 |
| | ValueLen | uint32 | 4 B | Value 长度 |
| | OldValueLen | uint32 | 4 B | 旧值长度（用于回滚） |
| | TimestampLen | uint16 | 2 B | HLC 时间戳长度（固定 10） |
| | CRC | uint32 | 4 B | 校验和 CRC32(Header + Data) |
| **🟠 数据区** | Key + Value + OldValue + Timestamp | bytes | 变长 | 实际数据 |

**WAL 文件结构**：由多个 WAL Entry 追加组成

```
┌────────────────────────────────────────────────────────────┐
│  WAL File                                                  │
├────────────────────────────────────────────────────────────┤
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐     │
│  │  Entry 1     │  │  Entry 2     │  │  Entry 3...  │     │
│  │  [24B Header]│  │  [24B Header]│  │              │     │
│  │  [Data]      │  │  [Data]      │  │              │     │
│  └──────────────┘  └──────────────┘  └──────────────┘     │
└────────────────────────────────────────────────────────────┘
```

---

#### Snapshot 文件格式设计（统一两段式）

##### Snapshot 文件格式

```mermaid
flowchart TD
    subgraph SnapshotFile["Snapshot File - 两段式格式"]
        direction TB

        subgraph Header["文件头（固定 16B）"]
            direction LR
            H1["Magic<br/>4B<br/>'NxSN'"]
            H2["Version<br/>2B<br/>uint16"]
            H3["Codec Type<br/>2B<br/>uint16"]
            H4["Length<br/>4B<br/>uint32"]
            H5["CRC<br/>4B<br/>CRC32"]
        end

        subgraph Data["数据区（变长，Protobuf 编码）"]
            direction TB
            D1["MVStore Data<br/>map<string, bytes>, version"]
        end

        Header --> Data
    end

    style Header fill:#e1f5ff
    style Data fill:#fff4e6
    style H1 fill:#f3e5f5
```

##### 字段详细说明

| 部分 | 字段 | 类型 | 大小 | 说明 |
|------|------|------|------|------|
| **🔵 文件头** | Magic | bytes | 4 B | 魔术字 `"NxSN"`（NexKV Snapshot） |
| | Version | uint16 | 2 B | 格式版本号（当前 = 1） |
| | Codec Type | uint16 | 2 B | 编解码器类型（默认 Protobuf = 3） |
| | Length | uint32 | 4 B | 数据区字节长度 |
| | CRC | uint32 | 4 B | 校验和 CRC32(Header + Data) |
| **🟠 数据区** | MVStore Data | bytes | 变长 | Protobuf 编码的 MVStore 数据 |

##### 文件创建流程

```mermaid
sequenceDiagram
    participant CM as CheckpointManager
    participant MV as MVStore
    participant WAL as WAL
    participant FS as FileSystem

    CM->>MV: 1. 获取快照
    MV-->>CM: snapshot_data

    CM->>WAL: 2. 获取当前 offset
    WAL-->>CM: wal_offset

    CM->>CM: 3. 构建 Protobuf Metadata<br/>（checkpoint_id, timestamp,<br/>last_wal_offset, entry_count）

    CM->>CM: 4. 编码 Protobuf Data<br/>（默认 CodecType = PROTOBUF）

    CM->>FS: 5. 写入临时文件<br/>checkpoint.XXX.tmp
    Note over FS: 顺序写入:<br/>1. 文件头（16B）<br/>2. 数据区（Protobuf）

    CM->>FS: 6. 原子重命名<br/>checkpoint.XXX.tmp → checkpoint.XXX

    CM->>WAL: 7. 清理 WAL<br/>Truncate(wal_offset)

    CM-->>CM: ✅ Checkpoint 创建完成
```

##### 恢复流程

```mermaid
sequenceDiagram
    participant R as Recovery
    participant CM as CheckpointManager
    participant FS as FileSystem
    participant WAL as WAL
    participant MV as MVStore

    R->>CM: 1. 启动恢复

    CM->>FS: 2. 列出 Checkpoint 文件
    FS-->>CM: checkpoint_files

    CM->>FS: 3. 读取最新 Checkpoint 文件头（12B）

    alt Magic 不匹配
        CM->>FS: 尝试上一个 Checkpoint
    end

    CM->>CM: 4. 解析文件头<br/>（Magic, Version, Codec Type, Length）

    alt Codec Type = PROTOBUF
        CM->>CM: 使用 ProtobufCodec
    else Codec Type = MSGPACK
        CM->>CM: 使用 MessagePackCodec
    else Codec Type = JSON
        CM->>CM: 使用 JSONCodec
    end

    CM->>FS: 5. 读取数据区（根据 Length 字段）
    CM->>CM: 6. 解码 Protobuf Data

    CM->>CM: 7. 验证 CRC（CRC32(Magic + Version + Codec + Length + Data)）

    CM->>MV: 8. 加载到 MVStore

    CM->>WAL: 9. 获取 Checkpoint 之后的 WAL（offset > last_wal_offset）
    WAL-->>CM: wal_entries

    CM->>MV: 10. 重放 WAL 条目

    MV-->>R: ✅ 恢复完成
```

##### 大小估算示例

**假设条件**：
- 记录数：100 万条
- 平均 Key：20 bytes
- 平均 Value：100 bytes

**各部分大小**：
- 文件头：16 bytes（固定）
- 数据区：~100 MB（Protobuf 编码）

**总计**：约 100 MB/Checkpoint

#### 核心接口设计

**CheckpointManager 接口**：

```go
// CheckpointManager Checkpoint 管理器
type CheckpointManager interface {
    // CreateCheckpoint 创建一致性快照
    // 返回 Checkpoint ID 和错误信息
    CreateCheckpoint(ctx context.Context) (uint64, error)

    // RecoverFromCheckpoint 从 Checkpoint 恢复
    // 返回恢复的 MVStore 实例和错误信息
    RecoverFromCheckpoint(ctx context.Context) (*MVStore, error)

    // GetLatestCheckpoint 获取最新 Checkpoint 信息
    GetLatestCheckpoint() (*CheckpointMetadata, error)

    // ListCheckpoints 列出所有 Checkpoint
    ListCheckpoints() ([]*CheckpointMetadata, error)

    // DeleteCheckpoint 删除指定 Checkpoint
    DeleteCheckpoint(checkpointID uint64) error

    // StartScheduler 启动自动 Checkpoint 调度器
    StartScheduler()

    // StopScheduler 停止调度器
    StopScheduler()
}
```

**Checkpoint 创建流程**：

```mermaid
sequenceDiagram
    participant S as Scheduler
    participant CM as CheckpointManager
    participant M as MVStore
    participant W as WAL
    participant F as FileSystem

    S->>CM: 触发 Checkpoint
    CM->>M: 获取一致性快照
    M-->>CM: 返回完整数据
    CM->>F: 写入 Checkpoint 文件
    CM->>W: 获取最后 WAL 序列号
    W-->>CM: 返回序列号
    CM->>F: 写入元数据（含序列号）
    CM->>F: 验证 Checkpoint 完整性
    F-->>CM: 验证成功
    CM->>W: 清理已应用的 WAL
    W-->>CM: 清理完成
    CM->>F: 删除过期 Checkpoint
    CM-->>S: 返回 Checkpoint ID
```

**Checkpoint 恢复流程**：

```mermaid
sequenceDiagram
    participant R as Recovery
    participant CM as CheckpointManager
    participant F as FileSystem
    participant W as WAL
    participant M as MVStore

    R->>CM: 启动恢复
    CM->>F: 查找最新 Checkpoint
    F-->>CM: 返回 Checkpoint 文件
    CM->>F: 读取并验证 Checkpoint
    F-->>CM: 返回快照数据
    CM->>M: 加载快照到 MVStore
    CM->>W: 获取 Checkpoint 之后的 WAL
    W-->>CM: 返回 WAL 条目
    CM->>M: 重放 WAL 条目
    M-->>R: 恢复完成
```

#### 调度策略设计

**触发条件**（OR 逻辑）：

1. **时间触发**：
   - 默认间隔：1 小时
   - 配置项：`checkpoint.interval`
   - 从上次 Checkpoint 时间开始计算

2. **大小触发**：
   - 默认阈值：100MB
   - 配置项：`checkpoint.wal_size_threshold`
   - 检查 WAL 文件大小

3. **手动触发**：
   - 管理 API：`CreateCheckpoint()`
   - 立即执行，不等待调度器

**保留策略**：

- 保留最近 N 个 Checkpoint（默认 3 个）
- 配置项：`checkpoint.retention_count`
- 删除时保留元数据记录（用于审计）

#### 关键实现细节

**1. 一致性保证**：

```go
func (m *CheckpointManager) CreateCheckpoint(ctx context.Context) (uint64, error) {
    // 1. 暂停 WAL 写入（获取写锁）
    m.wal.Lock()
    defer m.wal.Unlock()

    // 2. 获取 MVStore 一致性快照
    snapshot, err := m.mvStore.GetSnapshot()
    if err != nil {
        return 0, fmt.Errorf("获取快照失败: %w", err)
    }

    // 3. 记录当前 WAL 序列号
    lastSeq := m.wal.LastSequence()

    // 4. 写入 Checkpoint 文件（临时文件）
    tmpFile := fmt.Sprintf("checkpoint.%d.tmp", time.Now().UnixNano())
    if err := m.writeCheckpointFile(tmpFile, snapshot, lastSeq); err != nil {
        return 0, err
    }

    // 5. 原子重命名（确保完整性）
    checkpointFile := fmt.Sprintf("checkpoint.%d", checkpointID)
    if err := os.Rename(tmpFile, checkpointFile); err != nil {
        return 0, err
    }

    // 6. 清理已应用的 WAL
    if err := m.wal.Truncate(lastSeq); err != nil {
        // 清理失败不影响 Checkpoint 有效性
        log.Warn("WAL 清理失败: %v", err)
    }

    return checkpointID, nil
}
```

**2. Checkpoint 验证**：

```go
func (m *CheckpointManager) validateCheckpoint(file string) error {
    // 1. 检查文件存在性
    if _, err := os.Stat(file); os.IsNotExist(err) {
        return fmt.Errorf("Checkpoint 文件不存在")
    }

    // 2. 读取 Checkpoint 数据
    data, err := os.ReadFile(file)
    if err != nil {
        return fmt.Errorf("读取 Checkpoint 失败: %w", err)
    }

    // 3. 解析 Protobuf
    var pbCheckpoint proto.CheckpointFile
    if err := proto.Unmarshal(data, &pbCheckpoint); err != nil {
        return fmt.Errorf("解析 Checkpoint 失败: %w", err)
    }

    // 4. 验证元数据校验和
    if !m.verifyMetadataChecksum(&pbCheckpoint.Metadata) {
        return fmt.Errorf("元数据校验和失败")
    }

    // 5. 验证魔术字
    if !m.verifyTrailerMagic(pbCheckpoint.Trailer) {
        return fmt.Errorf("尾部魔术字不匹配")
    }

    return nil
}
```

**3. WAL 清理策略**：

```go
func (w *WAL) Truncate(sequence uint64) error {
    w.mu.Lock()
    defer w.mu.Unlock()

    // 1. 确保序列号有效
    if sequence > w.lastSequence {
        return fmt.Errorf("无效的序列号: %d", sequence)
    }

    // 2. 重写 WAL 文件（保留未应用的条目）
    tmpFile := w.file.Name() + ".tmp"
    f, err := os.Create(tmpFile)
    if err != nil {
        return err
    }
    defer f.Close()

    // 3. 写入未应用的 WAL 条目
    for seq := sequence + 1; seq <= w.lastSequence; seq++ {
        entry, ok := w.entries[seq]
        if !ok {
            continue
        }
        if err := w.writeEntry(f, entry); err != nil {
            return err
        }
    }

    // 4. 原子替换
    if err := f.Sync(); err != nil {
        return err
    }
    if err := f.Close(); err != nil {
        return err
    }

    w.file.Close()
    if err := os.Rename(tmpFile, w.file.Name()); err != nil {
        return err
    }

    // 5. 重新打开文件
    w.file, err = os.OpenFile(w.file.Name(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
    if err != nil {
        return err
    }

    // 6. 更新内存状态
    for seq := uint64(0); seq <= sequence; seq++ {
        delete(w.entries, seq)
    }

    return nil
}
```

**4. Checkpoint 调度器**：

```go
func (m *CheckpointManager) StartScheduler() {
    m.ticker = time.NewTicker(m.config.Interval)
    go m.scheduleLoop()
}

func (m *CheckpointManager) scheduleLoop() {
    for {
        select {
        case <-m.ticker.C:
            // 时间触发
            m.tryCreateCheckpoint(context.Background())
        case <-m.sizeCheckTicker.C:
            // 大小触发
            if m.getWALSize() > m.config.SizeThreshold {
                m.tryCreateCheckpoint(context.Background())
            }
        case <-m.done:
            return
        }
    }
}

func (m *CheckpointManager) tryCreateCheckpoint(ctx context.Context) {
    // 避免频繁 Checkpoint
    if time.Since(m.lastCheckpointTime) < m.config.MinInterval {
        return
    }

    checkpointID, err := m.CreateCheckpoint(ctx)
    if err != nil {
        log.Error("Checkpoint 创建失败: %v", err)
        return
    }

    log.Info("Checkpoint %d 创建成功", checkpointID)
    m.lastCheckpointTime = time.Now()
}
```

### 1.5 实现计划

#### 任务分解

| 阶段 | 任务 | 预计时间 | 交付物 |
|------|------|---------|--------|
| **阶段 1** | Protobuf Schema 定义与生成 | 0.5 天 | checkpoint.proto、*.pb.go |
| **阶段 2** | CheckpointWriter 实现 | 1 天 | checkpoint_writer.go |
| **阶段 3** | CheckpointReader 实现 | 1 天 | checkpoint_reader.go |
| **阶段 4** | CheckpointManager 实现 | 1 天 | checkpoint_manager.go |
| **阶段 5** | WAL Truncate 实现 | 0.5 天 | wal.go 修改 |
| **阶段 6** | 单元测试编写 | 1 天 | checkpoint_manager_test.go |
| **阶段 7** | 集成测试编写 | 0.5 天 | checkpoint_integration_test.go |
| **阶段 8** | 性能测试与优化 | 0.5 天 | 性能测试报告 |

**总计**: 6 天

#### 文件清单

**新增文件**：

1. `internal/metadata/proto/checkpoint.proto` - Protobuf Schema
2. `internal/metadata/proto/checkpoint.pb.go` - 生成的 Go 代码
3. `internal/metadata/store/checkpoint_manager.go` - Checkpoint 管理器
4. `internal/metadata/store/checkpoint_writer.go` - Checkpoint 写入器
5. `internal/metadata/store/checkpoint_reader.go` - Checkpoint 读取器
6. `internal/metadata/store/checkpoint_manager_test.go` - 单元测试
7. `internal/metadata/store/checkpoint_integration_test.go` - 集成测试

**修改文件**：

1. `internal/metadata/store/wal.go` - 添加 Truncate() 方法
2. `internal/metadata/store/mem_store.go` - 添加 GetSnapshot() 方法
3. `internal/metadata/store/mv_store.go` - 集成 CheckpointManager
4. `internal/metadata/config/loader.go` - 添加 Checkpoint 配置项
5. `Makefile` - 添加 Checkpoint 相关编译目标

### 1.6 风险评估

| 风险项 | 风险等级 | 影响 | 缓解措施 |
|-------|---------|------|---------|
| **Checkpoint 创建阻塞业务** | 🟡 中 | 用户体验下降 | 1. 使用快照读取，避免写锁<br>2. 限制快照时间 < 5 秒 |
| **Checkpoint 损坏无法恢复** | 🔴 高 | 数据丢失 | 1. 写入前校验<br>2. 保留多个版本<br>3. 自动回退到上一个版本 |
| **WAL 清理失败** | 🟡 中 | 磁盘空间未释放 | 1. 原子操作保证<br>2. 失败不影响 Checkpoint 有效性<br>3. 提供手动清理接口 |
| **恢复时间不达标** | 🟡 中 | 性能验收失败 | 1. 性能测试验证<br>2. 优化数据结构<br>3. 考虑增量 Checkpoint |
| **并发安全问题** | 🔴 高 | 数据损坏 | 1. 充分的并发测试<br>2. 使用锁保护共享状态<br>3. 代码 Review |
| **CheckPoint 文件过大** | 🟡 中 | 创建/恢复时间长 | 1. 监控文件大小<br>2. 考虑增量 Checkpoint（P2） |

### 1.7 配置设计

**新增配置项**：

```yaml
# config.yaml
checkpoint:
  # 是否启用 Checkpoint
  enabled: true

  # 自动 Checkpoint 间隔（0 = 禁用定时触发）
  interval: 1h

  # WAL 大小阈值（0 = 禁用大小触发）
  wal_size_threshold: 100MB

  # 最小 Checkpoint 间隔（避免过于频繁）
  min_interval: 10m

  # 保留的 Checkpoint 数量
  retention_count: 3

  # Checkpoint 目录
  dir: "./data/checkpoints"

  # Checkpoint 文件前缀
  file_prefix: "checkpoint"
```

### 1.8 依赖关系

**前置依赖**：

- ✅ PR-002 完成（Protobuf 编解码）
- ✅ MVStore 实现完成
- ✅ WAL 实现完成

**后续依赖**：

- PR-004 (WAL Rotation) - 依赖 Checkpoint 机制
- PR-005 (增量 Checkpoint) - 优化 Checkpoint 性能

**外部依赖**：

- Protobuf 编译器（protoc）
- Go 1.21+ 标准库（context、sync、io 等）

### 1.9 测试策略

#### 单元测试

1. **CheckpointWriter 测试**：
   - ✅ 正常创建 Checkpoint
   - ✅ 元数据正确写入
   - ✅ 数据完整性校验
   - ✅ 并发创建保护

2. **CheckpointReader 测试**：
   - ✅ 正常读取 Checkpoint
   - ✅ 损坏 Checkpoint 处理
   - ✅ 版本回退机制

3. **CheckpointManager 测试**：
   - ✅ CreateCheckpoint() 成功场景
   - ✅ CreateCheckpoint() 失败场景
   - ✅ RecoverFromCheckpoint() 成功场景
   - ✅ GetLatestCheckpoint() 查询
   - ✅ DeleteCheckpoint() 删除

4. **WAL Truncate 测试**：
   - ✅ 正常截断
   - ✅ 截断后数据一致性
   - ✅ 并发截断保护

#### 集成测试

1. **完整恢复流程**：
   - ✅ 写入数据 → 创建 Checkpoint → 崩溃 → 恢复 → 验证数据
   - ✅ 写入数据 → 创建 Checkpoint → 写入更多数据 → 崩溃 → 恢复 → 验证数据

2. **多 Checkpoint 场景**：
   - ✅ 创建多个 Checkpoint → 恢复最新版本
   - ✅ 删除中间 Checkpoint → 恢复最新版本
   - ✅ 损坏最新 Checkpoint → 回退到上一版本

3. **调度器测试**：
   - ✅ 时间触发 Checkpoint
   - ✅ 大小触发 Checkpoint
   - ✅ 手动触发 Checkpoint

#### 性能测试

1. **Checkpoint 创建性能**：
   - 10 万条记录 < 1 秒
   - 100 万条记录 < 5 秒

2. **Checkpoint 恢复性能**：
   - 10 万条记录 < 2 秒
   - 100 万条记录 < 10 秒

3. **对比测试**：
   - Checkpoint 恢复 vs 纯 WAL 恢复
   - 目标：快 10 倍以上

### 1.10 API 接口设计

**管理接口**（新增到 MetadataStore）：

```go
// CreateCheckpoint 手动创建 Checkpoint
func (s *MetadataStore) CreateCheckpoint(ctx context.Context) (uint64, error)

// GetCheckpointInfo 获取 Checkpoint 信息
func (s *MetadataStore) GetCheckpointInfo(ctx context.Context, checkpointID uint64) (*CheckpointInfo, error)

// ListCheckpoints 列出所有 Checkpoint
func (s *MetadataStore) ListCheckpoints(ctx context.Context) ([]*CheckpointInfo, error)

// DeleteCheckpoint 删除指定 Checkpoint
func (s *MetadataStore) DeleteCheckpoint(ctx context.Context, checkpointID uint64) error
```

### 1.11 监控指标

**新增 Prometheus 指标**：

```go
// Checkpoint 创建次数
checkpoint_created_total

// Checkpoint 创建耗时
checkpoint_creation_duration_seconds

// Checkpoint 恢复次数
checkpoint_recovery_total

// Checkpoint 恢复耗时
checkpoint_recovery_duration_seconds

// Checkpoint 文件大小
checkpoint_size_bytes

// Checkpoint 数量
checkpoint_count

// WAL 清理次数
wal_truncate_total

// WAL 清理释放的空间
wal_truncated_bytes_total
```

### 1.12 文档计划

**需要更新的文档**：

1. **设计文档**：
   - `docs/02_design/modules/03_WAL崩溃恢复.md` - 添加 Checkpoint 章节

2. **API 文档**：
   - `docs/02_design/05_API接口设计.md` - 添加 Checkpoint API

3. **运维文档**：
   - `docs/05_deployment_operation/02_运维手册.md` - 添加 Checkpoint 管理
   - `docs/05_deployment_operation/01_部署手册.md` - 添加配置说明

4. **测试文档**：
   - `docs/04_test/02_测试用例文档.md` - 添加 Checkpoint 测试用例

---

## 第二部分：后置部分（Post）

> **说明**：第二部分在开发完成并经过本地测试验证后填写。

### 2.1 实现总结

（开发完成后填写）

### 2.2 成果展示

（开发完成后填写）

### 2.3 性能/数据成果

（开发完成后填写）

### 2.4 测试验证

（开发完成后填写）

### 2.5 遗留问题

（开发完成后填写）

### 2.6 经验教训

（开发完成后填写）

---

## 附录

### A. 参考资料

1. **WAL 设计文档**：`docs/02_design/modules/03_WAL崩溃恢复.md`
2. **MVStore 实现文档**：`docs/02_design/modules/02_存储引擎设计.md`
3. **Protobuf 官方文档**：https://protobuf.dev/
4. **LevelDB Checkpoint 设计**：https://github.com/google/leveldb

### B. 相关 Issue/PR

- PR-002: Protobuf 编解码与测试覆盖率提升
- PR-004: WAL 轮换机制（后续）

### C. 术语表

| 术语 | 全称 | 说明 |
|------|------|------|
| WAL | Write-Ahead Log | 预写日志 |
| Checkpoint | - | 一致性快照 |
| MVStore | Multi-Version Store | 多版本存储 |
| Truncate | - | 截断（清理日志） |

---

**文档变更历史**：

| 版本 | 日期 | 修改人 | 修改内容 |
|------|------|--------|---------|
| V1.0 (Pre) | 2026-01-19 | AI Assistant | 初始版本创建 |

**审批记录**：

| 角色 | 姓名 | 审批日期 | 意见 | 签字 |
|------|------|----------|------|------|
| 架构师 | - | - | ⏳ 待审批 | - |
| 项目经理 | - | - | ⏳ 待审批 | - |
| 测试工程师 | - | - | ⏳ 待审批 | - |

---

**状态**: 📝 待架构师评审
**下一步**: 等待架构师审批 Pre 文档
