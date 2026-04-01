# BTree 测试整理计划

**日期**: 2026-04-01
**状态**: 整理完成，待实施

---

## 1. 测试文件概览

| 指标 | 数量 |
|------|------|
| 测试文件总数 | 37 |
| 包含 skip 的文件 | 13 |
| 无 skip 的文件 | 24 |

---

## 2. Skip 测试分类

### 2.1 Phase 1-6 修复后可启用的测试

这些测试之前因 BTree 分裂 bug 被 skip，经过 Phase 1-6 修复后**应该可以启用**：

| 文件 | Skip 数量 | 原因 | 建议 |
|------|-----------|------|------|
| `root_split_stress_test.go` | 5 | "flaky test", "pre-existing retry exhaustion issue" | **移除 skip** |
| `offheap_delete_concurrent_test.go` | 5 | 3 个是 "short 模式"，1 个 "pre-existing retry exhaustion issue" | **移除 retry exhaustion skip** |
| `simple_test.go` | 4 | 3 个大型测试 + 1 个内存限制 | **移除大型测试 skip** |

### 2.2 功能未实现，需继续 skip

| 文件 | Skip 数量 | 原因 | 建议 |
|------|-----------|------|------|
| `persistence_test.go` | 4 | Off-Heap 持久化未实现 | **保持 skip** |
| `page_persist_test.go` | 1 | Off-Heap 持久化未实现 | **保持 skip** |
| `ccow_manager_test.go` | 1 | COW 快照系统设计用于 Off-Heap | **保持 skip** |
| `split_leaf_test.go` | 4 | splitLeaf 未完全集成 | **保持 skip** |
| `internal_page_split_test.go` | 2 | 多层树结构支持 | **保持 skip** |
| `page_info_clone_test.go` | 9 | Off-Heap GetLeafPage 不可用 | **审查是否可删除** |

### 2.3 测试环境限制，合理的 skip

| 文件 | Skip 数量 | 原因 | 建议 |
|------|-----------|------|------|
| `btree_batch_test.go` | 2 | CI 环境不稳定 + short 模式 | **保持 skip** |
| `btree_delta_test.go` | 2 | Delta Chain Off-Heap 不支持 | **保持 skip** |
| `btree_offheap_integration_test.go` | 2 | Off-Heap Get 内存安全问题 | **保持 skip（待修复）** |
| `merge_api_test.go` | 2 | 长时间测试 | **保持 skip** |

---

## 3. 已删除的测试文件

| 文件 | 删除原因 |
|------|----------|
| `search_path_test.go` | "searchPath implementation pending" |
| `lazy_load_test.go` | "ChunkManager integration pending" |
| `merge_leaf_test.go` | "已迁移到 merge_api_test.go" |

---

## 4. 待办事项

### 4.1 高优先级（修复后即可启用）

- [ ] `root_split_stress_test.go`: 移除 5 个 skip
  - "temporarily skipped: pre-existing retry exhaustion issue" (2处)
  - "flaky test: skip until handleSplitOffHeapSync race is fixed" (1处)
  - 2 个 short 模式 skip

- [ ] `offheap_delete_concurrent_test.go`: 移除 retry exhaustion skip
  - "temporarily skipped: pre-existing retry exhaustion issue" (1处)

### 4.2 中优先级（功能完成后启用）

- [ ] `persistence_test.go`: 实现 Off-Heap 持久化后启用
- [ ] `page_persist_test.go`: 实现 Off-Heap 持久化后启用
- [ ] `split_leaf_test.go`: splitLeaf 集成完成后启用
- [ ] `internal_page_split_test.go`: 多层树结构支持完成后启用

### 4.3 低优先级（审查后决定）

- [ ] `page_info_clone_test.go`: 9 个 Off-Heap 相关 skip
  - 审查是否可以删除或迁移到 offheap 包
- [ ] `btree_offheap_integration_test.go`: Off-Heap Get 内存安全问题
  - 需要调查并修复 Off-Heap Get 的内存安全

---

## 5. 实施计划

### Phase A: 启用已修复的测试（立即可做）

```bash
# 移除 root_split_stress_test.go 中的 skip
# 移除 offheap_delete_concurrent_test.go 中的 retry exhaustion skip
```

### Phase B: 审查待删除的测试（本周）

```bash
# 审查 page_info_clone_test.go 是否可删除
# 审查 btree_offheap_integration_test.go 内存安全问题
```

### Phase C: 功能完成后启用（取决于功能实现）

```bash
# persistence_test.go - 等待 Off-Heap 持久化实现
# split_leaf_test.go - 等待 splitLeaf 集成
# internal_page_split_test.go - 等待多层树支持
```

---

## 6. 当前测试状态

```
ok  btree     4.2s
ok  offheap   15.4s

总测试文件: 37
有 skip 的文件: 13
无 skip 的文件: 24
```

---

## 7. 相关文档

- 测试整理计划: `docs/10_benchmark/2026-03-29-btree-set-benchmark/test-cleanup-plan.md`
- BTree 分裂 bug 修复提案: `btree-split-bug-proposal.md`
