# PR-002 待架构师评审

> **📋 文档类型**: Proposals（技术方案建议）
> **🏷️ 主题**: Protobuf Codec 实现与测试覆盖率提升
> **📅 创建日期**: 2026-01-19
> **✅ 状态**: 🔄 待架构师评审

---

## 📌 评审请求

PR-002 **Pre 文档**已完成，需要架构师评审后才能继续开发。

**Pre 文档路径**：
`docs/06_project_management/pr_documents/feature/2026-01-19_PR-002_Protobuf编解码与测试覆盖率提升_全流程.md`

---

## 🎯 评审范围

### P1 任务：测试覆盖率提升（72% → 80%）

**当前状态**：
- 当前覆盖率：72.0%
- 目标覆盖率：>80.0%
- 差距：8 个百分点

**补充测试计划**（20+ 测试用例）：
| 测试ID | 测试名称 | 测试模块 | 测试场景 | 优先级 |
|--------|----------|---------|---------|--------|
| T-001 | `TestVerifyBatchChecksums_Valid` | wal_batch.go | 验证有效批次的校验和 | P1 |
| T-002 | `TestWALGroupCommit_FlushBySize` | wal_batch.go | 按批量大小触发 flush | P1 |
| T-003 | `TestWALBatchReader_ReadBatch` | wal_batch.go | 批量读取 100 条记录 | P1 |
| T-004 | `TestMVStore_GetVersion_History` | mem_store.go | 读取历史版本 | P1 |
| T-005 | `TestMVStore_VersionConflict` | mem_store.go | 版本冲突检测 | P1 |
| T-006 | `TestWALRotation_SingleFile` | wal_rotation.go | 单文件轮转 | P1 |
| T-007 | `TestWALCheckpoint_Create` | wal_checkpoint.go | 创建检查点 | P1 |
| T-008 | `TestMVStore_ConcurrentPuts` | mem_store.go | 并发写入 | P1 |
| ... | ... | ... | ... | ... |

**详细清单**：共 20+ 测试用例，覆盖边界条件、并发场景、错误恢复

---

### P2 任务：Protobuf Codec 实现

**实现方案**（参考 `messagepack_2026-01-19_triple-codec-proposal.md`）：

**关键设计**：
```go
// 同一个结构体添加三种标签
type ClusterMetadata struct {
    Shards  map[string]ShardMetadata  `json:"shards" msgpack:"shards" protobuf:"bytes,1,opt,name=shards"`
    Nodes   map[string]NodeMetadata   `json:"nodes" msgpack:"nodes" protobuf:"bytes,2,opt,name=nodes"`
    Version uint64                    `json:"version" msgpack:"version" protobuf:"varint,4,opt,name=version"`
}
```

**实现步骤**：
1. **安装 Protobuf 工具链**
   ```bash
   brew install protobuf
   go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
   ```

2. **定义 `.proto` Schema**
   - 创建 `internal/metadata/store/codec/wal.proto`
   - 创建 `internal/metadata/store/codec/metadata.proto`

3. **生成 Protobuf Go 代码**
   ```bash
   protoc --go_out=. wal.proto metadata.proto
   ```

4. **实现 ProtobufCodec**
   - 实现 `WALCodec` 接口
   - 提供结构体转换工具

5. **性能对比测试**
   - 添加 Protobuf Benchmark 测试
   - 对比 JSON vs MessagePack vs Protobuf

**预期性能**：
| Codec | 编码性能 | 解码性能 | 数据大小 |
|-------|---------|---------|---------|
| JSON | 基线（1x） | 基线（1x） | 基线（1x） |
| MessagePack | **2.5x 更快** | **14.7x 更快** | **节省 25%** |
| **Protobuf（预期）** | **3-5x 更快** ⚡⚡ | **3-5x 更快** ⚡⚡ | **节省 40-60%** 💾💾 |

---

## 📊 风险评估

| 风险点 | 影响等级 | 应对措施 |
|--------|----------|----------|
| **Protobuf 依赖引入** | 中 | Protobuf 是 Google 官方维护，稳定性高 |
| **Schema 变更兼容性** | 中 | 使用 `protoc` 生成代码，保持向后兼容 |
| **测试覆盖率提升困难** | 低 | 已识别未覆盖代码区域，有明确计划 |
| **Protobuf 性能不达标** | 低 | Protobuf 业界验证充分，性能优于 JSON |
| **三 Codec 维护成本** | 中 | 统一 Codec 接口，代码复用度高 |

---

## 🤔 架构师评审要点

请架构师评审以下关键点：

### ✅ 技术可行性

- [ ] Protobuf Schema 定义合理
- [ ] 结构体转换工具设计可行
- [ ] 三编解码器统一架构清晰

### ✅ 性能目标

- [ ] Protobuf 性能预期合理（3-5x 提升）
- [ ] 测试覆盖率 80% 目标可达
- [ ] 性能对比测试计划完善

### ✅ 风险可控

- [ ] Protobuf 依赖风险可控
- [ ] 兼容性保障措施到位
- [ ] 实施计划合理

### ✅ 开发规范

- [ ] 遵循项目编码规范
- [ ] 测试用例设计完善
- [ ] 文档输出符合要求

---

## 📝 评审决策

**架构师请选择**：

**A. ✅ 评审通过，同意开工**
- 方案可行，风险可控
- 可以立即启动开发

**B. ⚠️ 需要优化后再评审**
- 指出需要优化的点
- 修改后重新提交评审

**C. ❌ 评审不通过**
- 方案存在重大问题
- 需要重新设计

---

## 📂 相关文档

- **Pre 文档**：`docs/06_project_management/pr_documents/feature/2026-01-19_PR-002_Protobuf编解码与测试覆盖率提升_全流程.md`
- **Brainstorm 参考文档**：`docs/06_project_management/brainstorm/messagepack_2026-01-19_triple-codec-proposal.md`
- **PR-001 文档**：`docs/06_project_management/pr_documents/feature/2026-01-19_PR-001_WAL优化与增强_全流程.md`

---

**文档版本**: v1.0
**最后更新**: 2026-01-19
**状态**: 🔄 待架构师评审
