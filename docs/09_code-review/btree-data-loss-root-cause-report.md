# B-Tree 版本号编码数据丢失问题 - 根本原因调查

## 问题概述

在实施 16-bit 版本号编码后，发现 **1076 个 key 阈值处** 出现严重数据丢失：
- 1075 个 key: ✅ 全部存在
- 1076 个 key: ❌ 122 存在, 954 丢失 (88.6% 丢失率)

## 根本原因

### 1. 页面内 keys 完全无序

**Page 92 的 keys 顺序**（36 个 keys）：
```
key-0, key-1, key-10, key-100, key-1000, key-1001...key-1009,
key-101, key-1010...key-1019, key-102, key-1020...key-1029
```

**Page 16 的 keys 范围**：
- First: key-245
- Last: key-28  (完全颠倒！)

### 2. 恶性循环机制

```
keys 无序 → SearchKey (二分查找) 返回错误位置 →
InsertLeafEntry 在错误位置插入 → keys 更无序 →
下一次 SearchKey 更错误 → ...
```

### 3. 数据丢失统计

插入 1076 个 key 后，根节点的 24 个子页面总计只有 **972 个 keys**：
- 丢失 104 个 keys (9.7% 完全不在任何页面中)
- 剩余 972 个 keys 中，大部分在错误的页面中

## 失败的关键尝试

1. ✅ **GetChild/GetIndexEntryOffset 解码修复**（多处）
   - 位置：leaf_lock_set.go:438, btree.go:1003, offheap_adapter.go:306/337/396/405/819/986/1005
   - 结果：减少了部分错误，但未解决根本问题

2. ❌ **UpdateIndexEntry extraChild 处理**
   - 添加详细注释说明逻辑
   - 结果：无改进

3. ❌ **InitPage 清空页面**
   - 尝试清空 entries 区域和整个数据区
   - 结果：无改进，且引入性能问题

## 核心问题

**SearchKey 依赖 B+Tree 的有序性假设**，但页面内容已经无序，导致：

```go
// page_layout.go:400-419
for left <= right {
    mid := left + (right-left)/2
    midKey := ... // 读取中间位置的 key
    cmp := bytes.Compare(key, midKey)
    // 如果 keys 无序，二分查找结果错误
}
```

## 建议解决方案

### 方案 A：强制排序验证（短期）

在 `InsertLeafEntry` 中添加验证：
- 检查插入位置前后的 key 是否满足有序性
- 如果不满足，使用线性搜索重新定位

**优点**：快速实施，防止进一步恶化
**缺点**：性能影响，治标不治本

### 方案 B：页面重建（中期）

在检测到 keys 无序时：
- 收集页面所有 keys
- 排序后重新写入新页面
- 更新父节点指针

**优点**：修复已有损坏
**缺点**：复杂度高，性能开销大

### 方案 C：重新设计插入逻辑（长期）

不依赖 SearchKey 返回的位置：
- 使用线性扫描找到正确插入位置
- 或在插入时维护排序不变性

**优点**：彻底解决问题
**缺点**：需要大量测试，风险高

## 相关文件

| 文件 | 优先级 | 状态 |
|------|--------|------|
| offheap/page_layout.go | 🔴 高 | SearchKey 依赖有序性 |
| offheap_adapter.go | 🔴 高 | InsertToOffHeap, UpdateIndexEntry |
| leaf_lock_set.go | 🔴 高 | handleSplitOffHeapSync |
| offheap/materialize.go | 🟡 中 | MaterializePageFromBytes |

## 下一步行动

1. **短期**：实施方案 A，防止问题恶化
2. **中期**：评估方案 B 的可行性
3. **长期**：考虑方案 C 的重新设计

## 时间线

- 2026-03-27 10:05 - 开始系统性调查
- 2026-03-27 10:10 - 发现 keys 无序根本原因
- 累计调查时间：约 5 小时
- 尝试修复次数：5 次（1 次成功，4 次失败）
