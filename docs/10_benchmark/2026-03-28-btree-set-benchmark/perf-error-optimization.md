# 热路径错误构造优化 — 性能报告

## 背景

CPU profile 显示 `errors.Wrapf` + `fmt.Sprintf` 占 BTree Set 热路径 ~6% CPU。
热路径错误几乎从不被人类阅读（上层用 `errors.Is(err, ErrRetry)` 判断），但每次构造都付出完整格式化代价。

## 优化策略

分两 步执行：

1. **第一步**: `Wrapf` → `stderrors.Join`（消除 `fmt.Sprintf` 开销，）
2. **第二步**: `stderrors.Join` → 直接返回 `err` / sentinel（消除 Join 分配开销）

## 性能对比

| 階段 | 1T ops/s | 延迟 | 变化 |
|------|----------|------|------|
| 优化前 (Wrapf + fmt.Sprintf) | ~22,7K | ~44 μs | 基线 |
| 第一步 (stderrors.Join) | ~25K | ~40 μs | +10% |
| **第二步（直接返回）** | **~27.5K** | **~36 μs** | **+21%** |

**第二步额外提升 ~10%，**，总计 **+21%**，vs 基线。

根因分析：
- 只有 `ErrRetry`、`ErrCircularReference`→`ErrKeyNotFound` 被生产代码用 `errors.Is()` 检查
- 其余 ~40 个 sentinel 从未被检查，`errors.Is` → Join 分配纯粹浪费
- 直接返回 `err` 或 sentinel 避免所有分配开销（零 cost）

## 改动范围
`pkg/errors/errors.go` — 40+ 个热路径错误函数去除 `stderrors.Join` 分配开销
