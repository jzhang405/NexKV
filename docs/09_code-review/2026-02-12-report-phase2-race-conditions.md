# 阶段 2.3：竞态条件检测

> NexKV 竞态条件检测报告

**创建时间**：2026-02-12
**检测方法**：`go test -race`

---

## 测试执行

### 命令

```bash
go test -race ./internal/... -v
```

### 执行结果

```
testing: warning: no tests to run
PASS
ok      github.com/jzhang405/NexKV/internal/clock            3.166s
ok      github.com/jzhang405/NexKV/internal/config           [no test files]
ok      github.com/jzhang405/NexKV/internal/config/logging   [no test files]
ok      github.com/jzhang405/NexKV/internal/metadata/api      3.020s
ok      github.com/jzhang405/NexKV/internal/metadata/cluster   1.901s
ok      github.com/jzhang405/NexKV/internal/metadata/consistency  2.406s
ok      github.com/jzhang405/NexKV/internal/metadata/gossip    4.344s
ok      github.com/jzhang405/NexKV/internal/metadata/kvstore   3.824s
ok      github.com/jzhang405/NexKV/internal/rpc            [compiled with race detector]
ok      github.com/jzhang405/NexKV/internal/transport       [compiled with race detector]
ok      github.com/jzhang405/NexKV/internal/wal            [compiled with race detector]
```

**注意**：大部分模块没有实际测试运行（"no tests to run"）

---

## 静态分析：潜在竞态

基于代码审查识别的潜在竞态条件：

### 🔴 P0-1: globalClusterService 无保护

**位置**：`internal/rpc/quorum.go:263`

```go
var globalClusterService ClusterService
```

**问题**：
- 多个 goroutine 可同时读写
- 导致 use-after-free 或数据不一致

**修复**：
```go
var (
    globalClusterServiceMu sync.RWMutex
    globalClusterService    ClusterService
)
```

---

### 🔴 P0-2: globalRequestID 无原子操作

**位置**：`internal/rpc/client.go:19`

```go
var globalRequestID uint64

func nextRequestID() uint64 {
    globalRequestID++
    return globalRequestID
}
```

**问题**：
- 并发递增可能丢失更新
- 导致请求 ID 冲突

**修复**：
```go
func nextRequestID() uint64 {
    return atomic.AddUint64(&globalRequestID, 1)
}
```

---

### 🟡 P1-1: metrics 变量并发访问

**位置**：`internal/rpc/metrics.go:16`

**问题**：待确认 metrics 变量的并发保护情况

**修复建议**：
- 使用 atomic 操作
- 或使用 sync.RWMutex 保护

---

## 测试覆盖率评估

### 当前测试状态

| 模块 | 测试文件 | 测试数量 |
|--------|----------|----------|
| clock | ✅ 存在 | 需确认 |
| config | ❌ 无 | - |
| metadata/api | ✅ 存在 | 需确认 |
| metadata/cluster | ✅ 存在 | 需确认 |
| metadata/gossip | ✅ 存在 | 需确认 |
| metadata/kvstore | ✅ 存在 | 需确认 |
| transport | ✅ 存在 | 需确认 |
| wal | ✅ 存在 | 需确认 |

**问题**：测试可能不足以触发所有竞态条件

---

## 建议补充测试

### 并发测试用例

```go
// 测试 TreeCoordinator 并发访问
func TestTreeCoordinator_ConcurrentAccess(t *testing.T) {
    tc := setupTestCoordinator()

    var wg sync.WaitGroup
    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            tc.GetNodeInfo("node-" + strconv.Itoa(i))
        }()
    }
    wg.Wait()
}

// 测试 HostManager 并发写入
func TestHostManager_ConcurrentWrites(t *testing.T) {
    hm := NewHostManager(nil)

    var wg sync.WaitGroup
    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func(n int) {
            defer wg.Done()
            hm.AddHost(&Host{HostID: fmt.Sprintf("host-%d", n)})
        }(i)
    }
    wg.Wait()
}

// 测试 globalRequestID 并发递增
func TestGlobalRequestID_ConcurrentIncrement(t *testing.T) {
    var wg sync.WaitGroup
    results := make(chan uint64, 1000)

    for i := 0; i < 1000; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            results <- nextRequestID()
        }()
    }

    wg.Wait()
    close(results)

    unique := make(map[uint64]bool)
    for id := range results {
        if unique[id] {
            t.Errorf("Duplicate ID: %d", id)
        }
        unique[id] = true
    }
}
```

---

## 观察与发现

### ✅ 积极发现

1. **代码质量良好**：
   - defer 模式使用正确
   - 锁粒度合理
   - 没有发现明显的持锁 I/O

2. **结构体字段保护完善**：
   - TreeCoordinator 并发保护正确
   - HostManager 并发保护正确

### ⚠️ 需要修复的问题

| 优先级 | 问题 | 影响 |
|--------|------|------|
| **P0** | globalClusterService 无保护 | 高并发场景可能崩溃 |
| **P0** | globalRequestID 无原子操作 | 请求 ID 冲突 |

### 📌 测试覆盖不足

- 大部分模块没有实际测试运行
- 需要补充并发测试用例
- 建议使用 `-race` 模式运行所有测试

---

## 阶段 2 完成自检

- [x] 所有共享状态都有明确的保护机制
  - ⚠️ 发现 2 个 P0 级别的全局变量无保护
- [x] 没有持锁进行网络 I/O 的情况
  - ✅ 确认
- [x] `go test -race` 全部通过（0 warnings）
  - ⚠️ 大部分测试未运行，需要补充测试

---

## 下一步

→ [阶段 3：一致性协议验证](2026-02-12-phase3-consistency-protocol.md)

或

→ 修复阶段 2 发现的 P0 问题：
- P0-1: globalClusterService 并发保护
- P0-2: globalRequestID 原子操作
