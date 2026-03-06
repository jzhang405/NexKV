# Phase 2.2 P1 问题修复代码

本文档包含 Phase 2.2 代码审查中发现的所有 P1 问题的修复代码。

---

## P1-1: splitKey 深拷贝问题

**文件**：`internal/infrastructure/storage/bftree/split.go`
**位置**：第 44 行
**风险**：分隔键可能被后续修改，导致数据不一致

### 修复前
```go
// 4. 找到中间位置
midIndex := len(allPairs) / 2
splitKey = allPairs[midIndex].key  // ⚠️ 引用，没有深拷贝
```

### 修复后
```go
// 4. 找到中间位置
midIndex := len(allPairs) / 2
// 深拷贝分隔键，防止后续修改
splitKey = make([]byte, len(allPairs[midIndex].key))
copy(splitKey, allPairs[midIndex].key)
```

### 应用方式
```bash
# 编辑文件
vim internal/infrastructure/storage/bftree/split.go

# 找到第 44 行，替换为上述修复后代码
```

---

## P1-3: sync_test.go 断言错误

**文件**：`internal/infrastructure/storage/bftree/sync_test.go`
**位置**：第 64 行和第 138 行
**风险**：测试逻辑错误，可能无法正确验证功能

### 修复前（第 64 行）
```go
// 验证统计信息
stats := tree.GetStats()
assert.Equal(t, int64(1), stats.WALAppends)
assert.GreaterOrEqual(t, int64(1), stats.WALSyncCount)  // ⚠️ 参数顺序错误
```

### 修复后
```go
// 验证统计信息
stats := tree.GetStats()
assert.Equal(t, int64(1), stats.WALAppends)
assert.GreaterOrEqual(t, stats.WALSyncCount, int64(1))  // ✅ 正确顺序
```

### 修复前（第 138 行）
```go
// 验证统计信息
stats := tree.GetStats()
assert.Equal(t, int64(10), stats.WALAppends)
assert.GreaterOrEqual(t, int64(10), stats.WALSyncCount)  // ⚠️ 参数顺序错误
```

### 修复后
```go
// 验证统计信息
stats := tree.GetStats()
assert.Equal(t, int64(10), stats.WALAppends)
assert.GreaterOrEqual(t, stats.WALSyncCount, int64(10))  // ✅ 正确顺序
```

### 应用方式
```bash
# 编辑文件
vim internal/infrastructure/storage/bftree/sync_test.go

# 替换第 64 行和第 138 行
```

---

## P2-1: collectAllSlots 性能优化（可选）

**文件**：`internal/infrastructure/storage/bftree/split.go`
**位置**：第 226 行
**优先级**：P2（可选优化）

### 修复前
```go
func collectAllSlots(mp *MiniPage) []Slot {
    var slots []Slot  // ⚠️ 没有预分配，多次扩容
    for _, slot := range mp.slots {
        // ...
        slots = append(slots, Slot{...})
    }
    return slots
}
```

### 修复后
```go
func collectAllSlots(mp *MiniPage) []Slot {
    slots := make([]Slot, 0, len(mp.slots))  // ✅ 预分配容量
    for _, slot := range mp.slots {
        // ...
        slots = append(slots, Slot{...})
    }
    return slots
}
```

---

## 验证修复

运行测试确保修复后没有破坏功能：

```bash
cd /home/jzh/ws/go/src/github.com/jzhang405/NexKV

# 运行 bftree 测试
go test -v ./internal/infrastructure/storage/bftree/

# 运行所有测试
go test -v ./...
```

---

## 预期结果

修复后，所有测试应该通过：

```
ok  	github.com/jzhang405/NexKV/internal/infrastructure/storage/bftree	0.041s
```

---

## Git 提交建议

如果修复后需要提交：

```bash
git add internal/infrastructure/storage/bftree/split.go
git add internal/infrastructure/storage/bftree/sync_test.go
git commit -m "fix(bftree): Phase 2.2 P1 问题修复

- splitLeafNode: 添加 splitKey 深拷贝
- sync_test.go: 修复 GreaterOrEqual 断言参数顺序

修复代码审查发现的 P1 问题"
```

---

**修复优先级**：
1. P1-1：必须修复（数据一致性风险）
2. P1-3：必须修复（测试准确性）
3. P2-1：可选（性能优化）

**预计工作量**：10 分钟
