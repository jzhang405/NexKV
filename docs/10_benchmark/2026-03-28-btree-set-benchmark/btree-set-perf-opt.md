# BTree Set Performance Tuning Plan

## Context

pprof 分析（`docs/10_benchmark/2026-03-28-btree-set-benchmark/pprof-analysis.md`）显示 BTree Set 操作存在严重性能问题：

- **成功率 14.17%**（200K 操作中 171K 失败）
- **叶子分裂占 48% CPU**（`handleSplitOffHeapSync` + `SplitOffHeapLeafPage`)
- **锁竞争 25%+**
- **内存分配 22.7%**
- **线性搜索 6%****

根因：叶子分裂持锁时间长 → 阻塞其他线程 → CAS 重试风暴 → 86% 操作浪费 CPU。


**根因链**:
1. 叶子分裂持锁时间长（全程持锁)
2. 分裂逻辑过度复杂(活锁检测、多层退避,多重分裂比例尝试)
3. 分裂中大量 Debug 输出
4. `linearSearchLeaf` 篡 `二 分搜索)

5. 分裂中的 KV 拷贝产生大量 `make([]byte)` 化桶 0.1 | `append` 分配` 和 `SplitOffHeapLeafPage` 中每次遍历所有条目执行线性搜索 + 复制 KV → 觻入临时 slice)
6. **4KB 新页清零** 占 9.3% CPU
7. `make([]byte`)+`append` 在循环中触发 `mallocgcTiny` (627,631 行) — 每次分裂为每个 KV 都分配 2 个临时 byte slice
   - **分配 2 个新页面 + 清零**: `pm.Alloc()` 每次调用 `runtime.memclrNoHeapPointers` 清零 4KB 页面
   - **分裂策略**: 尝试 50/50、 60/40, 70/90 多种比例 (`leaf_lock_set.go:720-742`), 应该只用 `dynamicRatio` 并带回 fallback

方案 — 我觉得对于大部分场景过于复杂
9. 活锁检测 + 动态退避 ` 的 CPU 开销不值得关注
10. 分裂操作期间全程持锁—— 所有写操作都被阻塞
11. `DebugPrintf` 在热路径中产生大量字符串格式化开销
12. `PageRefCache` 操作： 在 CAS 成功后更新 Page引用缓存和遍历 `PageRefCache.Delete/Replace` 等

13. 持锁期间:
 - 分裂完成后仍需 re试插入 KV → 分裂失败触发 `runtime.lock2`
14. 分裂过程中还在父节点上获取锁 `parentLock.TryLock()`—— 进一步阻塞
15. 整个分裂路径锁持有时间长且复杂
16. 锁竞争严重
17. 调试代码： 每个 KV 都 `make([]byte` + `append` 产生大量分配
18. 大量 goroutine 在 futex 上等待
19. 86% 操作在重试 10 次后丢弃
20. 10 次重试不够 + 没有指数退避 → CPU 浪费

21. 叶子页满时 Insert操作直接触发分裂，而不是先尝试在 `setWithLeafLock` 中插入 → 分裂
22. 分裂期间持锁阻塞所有其他写入
23 - 锁竞争严重的根源

24. **分裂开销大**: `linearSearchLeaf` 指数搜索(6%) + `make([]byte` 内存拷贝(22%) + `memclr`(9.3%)

25. **代码臃肿**: 分裂中每次分裂操作触发 `runtime.mallocgcTiny` + `memclrNoHeapPointers`

26. **`SplitOffHeapLeafPage` 中尝试多种分裂比例(50/50, 60/40, 70/90)， 使用 8 次分裂比例尝试)
27. 活锁检测阈值高（`pendingCount > 15`）直接进入 fallback 硬编码
30. Debug 输出过多
31. 分裂中的多处 CAS 操作 `runtime.lock2` — 这些是在正常分裂路径中不应出现
32. **Debug 代码可以安全删除**: 从生产代码移除 `DebugPrintf`、 `DebugPrint` 瘸
 分为 `Safe()` 内联)

 考虑将当前热点函数改为非阻塞模式。如果获取失败，返回 `ErrRetry` 让外层重试。 不会 spin。

33. **优化**: 保留叶子满处理逻辑,但让写入返回 `ErrRetry`

 不让 CAS 籱阻塞其他操作
  - 叶子满时：尝试在当前页面插入" → 检查是否满 → 如果满才尝试不同分裂比例... → 再检查活锁。 这些过于复杂:
- 内存分配： 每次分裂 2 次 `make([]byte` 产生 2N 个临时 byte 刹片
  - 新页清零: 每次 4KB 清零 (`memclrNoHeapPointers`)
  - 分裂策略: 尝试 50/50, 60/40, 70/90 多种比例 — 紻锁检测(>15 时进入 fallback 方案
  - `ReplaceChild` 网父节点索引更新: 复杂度极高
  - **锁持有时间**: setWithLeafLock (line 56 TryLock) → 分裂 → handleSplitOffHeapSync → handleSplit 后 parent 更新 → ...全程持锁
  - 分裂完成后还 `pm.Free` 左右页面
  - 分裂中频繁调用 `InsertToOffHeap` → `splitRequired=true` → 再做一次 CAS

  - **DebugPrintf 泛滥**: 大量 debug 输出污染 CPU profile，应该**分裂代码不应该走****精简"路径。

将所有 Debug 代码替换为 `bytes.Compare` 二 分搜索,合并增加一个 `SearchKey` 的调用， 曔回 437 行的 `SearchKey`。分析: 这是才能在**一次 `bytes.Compare` 中得到正确的结果。

将线性搜索替换为二分搜索。如果 key < splitKey, 可以 `Insert position到左页面
- key > splitKey, 可以 `insert 到右页面
- 甿说插入到右页面）

- 使用 `fmt.Sprintf` 格式化 key — 诏个线程分配一个随机字节作为 key 的第一个字符

- `key := []byte(fmt.Sprintf("pval-%d", j))`
```

将结果记录在 metrics 中。

}

}
```

* `fmt.Sprintf` 生成 key 时使用 `fmt.Sprintf` 替换 `fmt.Sprintf`。直接输出，格式。
  - `fmt.Sprintf` 替代 go tool pprof -http=:8080` 可视化分析.

  - 用 `--count` 指定每线程操作数（默认 50000)")
  - 运行测试后比对 `pprof -top cpu.prof` 阶段验证