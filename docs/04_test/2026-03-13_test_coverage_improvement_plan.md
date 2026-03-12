# 测试覆盖率提升计划

> **目标**：将测试覆盖率从 63.7% 提升至 80%+
> **优先级**：P1（高优先级）
> **预计时间**：4-6 小时

---

## 📊 当前覆盖率分析

### 完全未覆盖的函数（0%）

| 函数 | 行号 | 优先级 | 说明 |
|------|------|--------|------|
| **insertFromWAL** | 474 | P3 | Phase 3 功能，延后 |
| **loadPage** | 533 | P1 | 懒加载核心，必须补充 |
| **splitInternal** | 859 | P2 | Phase 2 功能，重要但不紧急 |
| **splitRootFromInternal** | 922 | P2 | Phase 2 功能，重要但不紧急 |

### 低覆盖率函数（< 50%）

| 函数 | 覆盖率 | 优先级 | 说明 |
|------|--------|--------|------|
| **replayWAL** | 42.9% | P3 | Phase 3 功能 |
| **getPageOrLoad** | 27.3% | P1 | 懒加载核心，必须补充 |
| **splitLeaf** | 41.7% | P1 | 核心功能，需要补充 |

### 中等覆盖率函数（50-80%）

| 函数 | 覆盖率 | 优先级 | 说明 |
|------|--------|--------|------|
| **Delete** | 65.7% | P2 | 核心功能，需补充边界测试 |
| **Get** | 70.6% | P2 | 核心功能，需补充边界测试 |
| **GetHeight** | 66.7% | P3 | 辅助功能，优先级较低 |

---

## 🎯 测试补充计划

### Phase 1: 核心功能测试（P1）⭐

#### 1.1 懒加载功能测试

**目标**：覆盖 `loadPage` 和 `getPageOrLoad`

**测试场景**：
1. **测试懒加载基本功能**
   - 验证页面从 ChunkManager 加载
   - 验证缓存机制
   - 验证并发加载安全性

2. **测试懒加载边界条件**
   - 页面不存在
   - ChunkManager 未初始化
   - 页面位置为 0

**预期收益**：+5-8% 覆盖率

#### 1.2 Split Leaf 测试

**目标**：提升 `splitLeaf` 覆盖率

**测试场景**：
1. **测试各种分裂场景**
   - 页面刚好满（maxKeys）
   - 页面超过 maxKeys
   - 页面分裂后验证键分布

2. **测试分裂边界条件**
   - 空页面分裂
   - 单键页面分裂
   - 大键值分裂

**预期收益**：+3-5% 覆盖率

---

### Phase 2: 边界条件测试（P2）

#### 2.1 Delete 边界测试

**测试场景**：
1. 删除不存在的键
2. 删除空树
3. 删除后触发 Merge
4. 删除后触发 Borrow
5. 并发删除相同键

**预期收益**：+2-3% 覆盖率

#### 2.2 Get 边界测试

**测试场景**：
1. 获取不存在的键
2. 从空树获取
3. 获取超大键
4. 并发获取相同键

**预期收益**：+2-3% 覆盖率

---

### Phase 3: 辅助功能测试（P3）

#### 3.1 快照功能测试

**测试场景**：
1. 创建快照
2. 释放快照
3. 快照隔离验证
4. 并发快照

**预期收益**：+1-2% 覆盖率

#### 3.2 统计功能测试

**测试场景**：
1. GetHeight 各种树结构
2. GetPageCount 统计
3. Stats 收集

**预期收益**：+1-2% 覆盖率

---

## 📋 实施步骤

### Step 1: 创建懒加载测试文件

**文件**：`lazy_load_test.go`（已存在，需补充）

**测试用例**：
- `TestLoadPage_Basic()` - 基本加载功能
- `TestLoadPage_NotFound()` - 页面不存在
- `TestLoadPage_Concurrent()` - 并发加载
- `TestGetPageOrLoad_CacheHit()` - 缓存命中
- `TestGetPageOrLoad_CacheMiss()` - 缓存未命中

### Step 2: 补充 Split 测试

**文件**：`split_leaf_test.go`（已存在，需补充）

**测试用例**：
- `TestSplitLeaf_FullPage()` - 满页面分裂
- `TestSplitLeaf_Overflow()` - 超过 maxKeys
- `TestSplitLeaf_KeyDistribution()` - 键分布验证
- `TestSplitLeaf_EdgeCases()` - 边界条件

### Step 3: 补充 Delete 测试

**文件**：`delete_test.go`（新建）

**测试用例**：
- `TestDelete_KeyNotFound()` - 键不存在
- `TestDelete_EmptyTree()` - 空树
- `TestDelete_TriggerMerge()` - 触发合并
- `TestDelete_TriggerBorrow()` - 触发借用
- `TestDelete_ConcurrentSameKey()` - 并发删除相同键

### Step 4: 补充 Get 测试

**文件**：`get_test.go`（新建）

**测试用例**：
- `TestGet_KeyNotFound()` - 键不存在
- `TestGet_EmptyTree()` - 空树
- `TestGet_LargeKey()` - 大键
- `TestGet_ConcurrentSameKey()` - 并发获取

### Step 5: 运行测试并验证覆盖率

**命令**：
```bash
go test -v -coverprofile=coverage.out ./internal/infrastructure/storage/btree/...
go tool cover -func=coverage.out | grep total
```

**目标**：> 80%

---

## ⏱️ 时间估算

| 任务 | 预计时间 |
|------|----------|
| Step 1: 懒加载测试 | 1.5 小时 |
| Step 2: Split 测试 | 1 小时 |
| Step 3: Delete 测试 | 1 小时 |
| Step 4: Get 测试 | 0.5 小时 |
| Step 5: 验证和调试 | 1 小时 |
| **总计** | **5 小时** |

---

## 📈 预期收益

| 阶段 | 覆盖率提升 | 累计覆盖率 |
|------|------------|------------|
| **当前** | - | 63.7% |
| **Step 1 完成后** | +6% | 69.7% |
| **Step 2 完成后** | +4% | 73.7% |
| **Step 3 完成后** | +3% | 76.7% |
| **Step 4 完成后** | +2% | 78.7% |
| **Step 5 完成后** | +2% | **80.7%** |

---

## 🎯 成功标准

- [ ] 所有新测试用例通过
- [ ] 测试覆盖率 ≥ 80%
- [ ] Race detector 通过
- [ ] 无测试超时

---

**创建日期**：2026-03-13
**负责人**：NexKV BTree Team
**状态**：✅ 计划完成，待实施
