# 阶段 2：并发安全审查（最关键）

> 分布式系统的 bug，80% 来自并发问题。这一步必须细致。

**预计时间**：3-5 小时
**状态**：⏳ 待开始

---

## ⚠️ 严重警告

并发问题是最难调试的 bug 之一。本阶段发现的任何问题都应优先修复。

---

## 📋 任务清单

### Step 2.1：静态审查所有共享状态（2h）

**任务**：
1. [ ] 列出所有全局变量和包级变量
2. [ ] 对每个共享变量进行并发安全性分析

**命令参考**：
```bash
# 列出所有全局变量
grep -rn "^var " internal/ --include="*.go" | grep -v "_test.go"

# 列出包级变量（非函数内）
grep -rn "^var [A-Z]" internal/ --include="*.go" | grep -v "_test.go"
```

#### 共享状态审查模板

| 变量名 | 位置 | 读写场景 | 保护方式 | 风险评估 |
|--------|------|----------|----------|----------|
| globalHostMap | cluster/host.go:12 | 并发读写 | sync.RWMutex | ✅ 安全 |
| configCache | config/cache.go:8 | 读多写少 | sync.Map | ✅ 合适 |
| requestCounter | metrics/counter.go:5 | 高频更新 | 无 | ❌ 需加 atomic |

**关键检查点**：
- [ ] 所有共享状态都有保护
- [ ] 读多写少场景使用 sync.RWMutex 或 sync.Map
- [ ] 高频计数器使用 atomic 操作

---

### Step 2.2：锁使用模式审查（1.5h）

**任务**：
1. [ ] 检查所有锁的持有时间
2. [ ] 识别危险的持锁模式

**命令参考**：
```bash
# 找出所有锁操作
grep -rn "\.Lock()" internal/ --include="*.go" | grep -v "_test.go"
grep -rn "\.RLock()" internal/ --include="*.go" | grep -v "_test.go"

# 检查是否有 defer Unlock()
grep -A10 "\.Lock()" internal/ --include="*.go" | grep "defer.*Unlock"
```

#### 危险模式检查清单

| 模式 | 示例 | 风险 | 检查结果 |
|------|------|------|----------|
| 持锁网络 I/O | `Lock(); RPC()` | 死锁风险 | [ ] |
| 嵌套锁 | `Lock(); ...; Lock()` | 死锁风险 | [ ] |
| 缺少 defer | `Lock(); ...; Unlock()` | panic 风险 | [ ] |
| 读写锁混用 | RLock 后尝试写 | 数据竞争 | [ ] |

#### 锁使用最佳实践检查

- [ ] 持锁时间 < 1ms（纯内存操作）
- [ ] 所有 Lock() 都有 defer Unlock() 配对
- [ ] 没有在持锁期间调用外部函数
- [ ] RWMutex 的读操作用 RLock()

---

### Step 2.3：动态检测数据竞争（1.5h）

**任务**：
1. [ ] 运行带竞态检测的测试
2. [ ] 分析每个 warning

**命令参考**：
```bash
# 运行竞态检测
go test -race ./internal/... -v

# 运行特定模块的竞态检测
go test -race ./internal/metadata/... -run TestClusterManager

# 运行一段时间（某些竞态需要触发）
go test -race ./internal/... -count=100
```

#### 竞态分析模板

```markdown
## 数据竞争报告

### 警告 1：Write at goroutine X vs Read at goroutine Y

**位置**：`cluster/host.go:42`
**类型**：真实竞态
**严重性**：P0
**修复方案**：添加 sync.Mutex 保护

### 警告 2：...

---

### 统计

- 真实竞态：X 个
- 疑似误报：Y 个
- 已修复：Z 个
```

---

## 📝 修复优先级

| 优先级 | 问题类型 | 修复期限 |
|--------|----------|----------|
| P0 | 真实数据竞争 | 立即 |
| P1 | 持锁网络 I/O | 24h 内 |
| P2 | 缺少 defer Unlock | 48h 内 |
| P3 | 性能优化（如锁粒度） | 下个迭代 |

---

## ✅ 完成自检

- [ ] 所有共享状态都有明确的保护机制
- [ ] 没有持锁进行网络 I/O 的情况
- [ ] `go test -race` 全部通过（0 warnings）

---

## 📌 本阶段产出文件

- `phase2_shared_state_audit.md` - 共享状态审查
- `phase2_lock_usage_audit.md` - 锁使用审查
- `phase2_race_conditions.md` - 竞态检测报告
