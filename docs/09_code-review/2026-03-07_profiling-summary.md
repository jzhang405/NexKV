# 性能分析总结 - Profiling 发现真正的瓶颈

> **日期**: 2026-03-07
> **工具**: go tool pprof
> **状态**: ✅ 分析完成

---

## 🔥 关键发现

### 之前的假设（错误）

❌ WAL 同步写入是主要瓶颈
❌ 页面分配开销大
❌ 锁竞争严重

### 真正的瓶颈（通过 profiling 发现）

✅ **LeafNode.compact() 占用 66.09% CPU**
✅ **LeafNode.compact() 占用 98.81% 内存**
✅ **map 写入是最大瓶颈（570ms, 24.8%）**

---

## 📊 CPU 热点分布

```
LeafNode.compact()       1.52s (66.09%) 🔥
├── map 写入             570ms (24.8%)  🔥🔥🔥
├── 第二次排序           220ms (9.6%)   🔥
├── 字符串比较           140ms (6.1%)   🔥
├── 第一次排序           120ms (5.2%)   🔥
└── 其他                470ms (20.4%)
```

---

## 💾 内存热点分布

```
LeafNode.compact()       2123.28MB (98.81%) 🔥
├── tempSlots append     ~800MB
├── processed map        ~600MB
├── newMiniPage.slots    ~400MB
└── newMiniPage.slotMap  ~323MB
```

---

## 🎯 优化方向（基于 Profiling）

### P0-1: 消除重复字符串转换

**问题**: `string(slot.key)` 被调用 3 次

```go
// ❌ 当前
keyStr := string(slot.key)  // 第1次
newMiniPage.slotMap[keyStr] = ...
// 后续又使用 string(slot.key)

// ✅ 优化
type SlotWithString struct {
    key    []byte
    keyStr string  // 预先转换
    value  []byte
}
```

**预期提升**: +30%~50% (0.5 天)

---

### P0-2: 预分配 map 容量

**问题**: `slotMap` 动态扩容导致重新哈希

```go
// ❌ 当前
newMiniPage := NewMiniPage(n.level)
// slotMap 容量为 0

// ✅ 优化
capacity := len(n.miniPage.slots) + len(n.deltas)
newMiniPage.slotMap = make(map[string]int, capacity)
```

**预期提升**: +40%~60% (0.5 天)

---

### P0-3: 减少排序次数

**问题**: sort.Slice 被调用 2 次

```go
// ❌ 当前
sort.Slice(tempSlots, ...)  // 第1次
// 应用 Delta...
sort.Slice(tempSlots, ...)  // 第2次

// ✅ 优化
// 使用二分查找保持有序插入
// 只在最后排序一次
```

**预期提升**: +15%~25% (1 天)

---

## 📈 预期总体提升

| 操作 | 当前性能 | 优化后目标 | 提升幅度 |
|------|---------|-----------|---------|
| **compact()** | ~1,520 ms | < 400 ms | **+3.8x** |
| **顺序写入** | 182,622 ns | < 50,000 ns | **+3.6x** |
| **随机写入** | 275,696 ns | < 80,000 ns | **+3.4x** |

**总体提升**: **+200%~300%** (3x - 4x)

---

## ⏱️ 实施时间表

| 日期 | 任务 | 预期提升 |
|------|------|---------|
| **Day 1** | P0-1: 消除重复字符串转换 | +30%~50% |
| **Day 1** | P0-2: 预分配 map 容量 | +40%~60% |
| **Day 2** | P0-3: 减少排序次数 | +15%~25% |
| **Day 2** | 性能验证 | - |

**总计**: 2 天完成核心优化

---

## ✅ 下一步行动

1. ✅ 性能分析完成
2. ⏳ 开始实施 P0-1
3. ⏳ 运行性能测试验证
4. ⏳ 更新优化计划文档

---

**状态**: 准备开始优化
**预计完成**: 2026-03-09
