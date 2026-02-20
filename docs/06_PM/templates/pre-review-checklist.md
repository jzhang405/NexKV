# Pre 文档评审检查清单

> **文档类型**: 评审指南
> **创建日期**: 2026-02-21
> **最后更新**: 2026-02-21
> **适用范围**: 所有 Pre 文档评审
> **来源**: BroadcastTracker Callback 评审经验总结

---

## 📋 评审维度

### 1️⃣ 接口设计审查

#### 1.1 Nil 参数行为

**检查项**：
- [ ] 所有接口方法的参数是否可能为 `nil`？
- [ ] 如果可能为 `nil`，是否在文档中明确说明？
- [ ] 是否明确了 `nil` 参数的语义？

**示例**（来自 BroadcastTracker Callback）：
```go
// ✅ 明确说明
// OnSuccess 每次收到成功响应时调用
// 参数说明：
//   - resp: 成功响应消息（不会为 nil）
OnSuccess(peer model.PeerID, resp model.Message, stats BroadcastStats)

// OnFailure 每次收到失败响应时调用
// 参数说明：
//   - err: 错误信息（不会为 nil，包含具体错误类型）
//          - 超时错误：context.DeadlineExceeded
//          - 网络错误：net.Error
//          - 业务错误：业务逻辑返回的错误
OnFailure(peer model.PeerID, err error, stats BroadcastStats)
```

**反面示例**：
```go
// ❌ 未说明 nil 行为
func OnSuccess(peer model.PeerID, resp model.Message, stats BroadcastStats)
// 问题：resp 可能为 nil 吗？如果为 nil 如何处理？
```

---

#### 1.2 触发条件明确性

**检查项**：
- [ ] 回调/事件的触发条件是否明确？
- [ ] 是否说明了在什么情况下触发？
- [ ] 是否说明了不在什么情况下触发？
- [ ] 是否有具体的示例？

**示例**（来自 BroadcastTracker Callback）：
```go
// ✅ 触发条件明确
// OnMajorityReached 达到多数派时调用（仅调用一次）
// 触发条件：
//   - 成功响应数 >= majority（len(targets)/2 + 1）
//   - 只在 RecordSuccess 时检查，RecordFailure 不会触发
//   - 例如：3 个节点，2 个成功即触发（即使 1 个失败）
OnMajorityReached(stats BroadcastStats)
```

**反面示例**：
```go
// ❌ 触发条件不明确
// OnMajorityReached 达到多数派时调用
// 问题：什么是多数派？失败节点算不算？何时检查？
```

---

#### 1.3 "仅触发一次"机制

**检查项**：
- [ ] 是否承诺"仅触发一次"？
- [ ] 是否有实现机制保证？
- [ ] 是否考虑了并发场景？

**实现机制建议**：
1. **方案 A**：标志位 + 双重检查
   ```go
   type BroadcastTracker struct {
       majorityCallbackTriggered bool // 标志位
   }

   // 锁内判断 + 锁外触发
   t.mu.Lock()
   if len(t.responses) >= majority && !t.majorityCallbackTriggered {
       t.majorityCallbackTriggered = true
       shouldTriggerMajority = true
   }
   t.mu.Unlock()

   // 锁外触发
   if shouldTriggerMajority {
       callback.OnMajorityReached(stats)
   }
   ```

2. **方案 B**：使用 `sync.Once`
   ```go
   var triggerMajorityOnce sync.Once

   if len(t.responses) >= majority {
       triggerMajorityOnce.Do(func() {
           callback.OnMajorityReached(stats)
       })
   }
   ```

**反面示例**：
```go
// ❌ 无"仅触发一次"机制
if len(t.responses) >= majority {
    callback.OnMajorityReached(stats) // 可能被多次调用
}
```

---

### 2️⃣ 测试计划审查

#### 2.1 边界场景测试

**检查项**：
- [ ] 是否包含空输入测试？
- [ ] 是否包含全部失败场景？
- [ ] 是否包含并发场景？
- [ ] 是否包含顺序依赖场景？

**示例**（来自 BroadcastTracker Callback）：
| 测试场景 | 说明 | 优先级 |
|---------|------|--------|
| **TestBroadcastCallback_EmptyTargets** | 空 targets（targets=[]）| P0 |
| **TestBroadcastCallback_AllFailed** | 全部失败（验证 OnFailure + OnFullDone）| P0 |
| **TestBroadcastCallback_MajorityThenFullDone** | 先 Majority 后 FullDone（验证顺序）| P1 |
| **TestBroadcastCallback_ConcurrentRecordSuccess** | 并发 RecordSuccess（验证仅触发一次）| P0 |

**反面示例**：
```markdown
❌ 测试用例清单：
1. TestBroadcastCallback_OnSuccess
2. TestBroadcastCallback_OnFailure
// 问题：缺少边界场景测试（空输入、全部失败、并发、顺序）
```

---

#### 2.2 测试覆盖率

**检查项**：
- [ ] 是否明确测试覆盖率目标（≥ 80%）？
- [ ] 是否包含单元测试？
- [ ] 是否包含集成测试？
- [ ] 是否包含性能测试？

---

### 3️⃣ 实施计划审查

#### 3.1 时间估算合理性

**检查项**：
- [ ] 时间估算是否考虑了"仅触发一次"等复杂逻辑？
- [ ] 是否预留了缓冲时间？
- [ ] 是否有分阶段检查点？

**经验值**：
- **简单接口实现**：0.5-1 小时
- **含并发控制的接口**：2-3 小时（需要考虑线程安全）
- **含"仅触发一次"机制的接口**：+0.5-1 小时

**示例**（来自 BroadcastTracker Callback）：
| 阶段 | 原估算 | 新估算 | 理由 |
|------|--------|--------|------|
| 回调触发逻辑 | 2h | **3h** | 需要实现标志位 + 双重检查 |

---

### 4️⃣ 风险评估审查

#### 4.1 常见风险识别

**检查项**：
- [ ] 是否识别了"回调重复触发"风险？
- [ ] 是否识别了"Nil 参数处理"风险？
- [ ] 是否识别了"并发安全"风险？
- [ ] 是否有对应的缓解措施？

**风险清单模板**：
| 风险 | 可能性 | 影响 | 缓解措施 |
|------|--------|------|----------|
| **回调重复触发** | 低 | 高 | 使用标志位确保仅触发一次 |
| **Nil 参数处理** | 低 | 中 | 文档明确不会为 nil，实现无需检查 |
| **并发安全** | 中 | 高 | 锁外执行回调 + 标志位双重检查 |

---

## 📊 评审打分表

### 总体评分（满分 10 分）

| 维度 | 权重 | 得分 | 说明 |
|------|------|------|------|
| **任务范围明确** | 20% | /2.0 | 背景清晰，目标明确，边界清晰 |
| **接口设计完整** | 30% | /3.0 | Nil 行为、触发条件、"仅触发一次"机制 |
| **测试计划全面** | 20% | /2.0 | 单元测试 + 集成测试 + 边界场景 |
| **实施计划合理** | 15% | /1.5 | 时间估算合理，分阶段清晰 |
| **风险评估充分** | 15% | /1.5 | 风险识别充分，缓解措施有效 |
| **总分** | 100% | **/10** | |

### 评分标准

- **9-10 分**：✅ 优秀，立即批准
- **8-8.9 分**：✅ 良好，小调整后批准
- **7-7.9 分**：⚠️ 需要修改后批准
- **< 7 分**：❌ 需要重大修改

---

## 🎯 快速检查清单

**Pre 文档评审前快速检查**：

### 必查项（P0）

- [ ] **Nil 参数行为**：所有接口方法的参数是否说明了 nil 行为？
- [ ] **触发条件**：回调/事件的触发条件是否明确？
- [ ] **仅触发一次**：是否承诺"仅触发一次"？是否有实现机制？
- [ ] **边界测试**：是否包含空输入、全部失败、并发场景测试？
- [ ] **时间估算**：是否考虑了并发控制、仅触发一次等复杂逻辑？

### 建议项（P1）

- [ ] **错误类型**：是否明确了不同类型的错误（超时、网络、业务）？
- [ ] **性能目标**：是否有具体的性能指标（如回调执行时间 < 10ms）？
- [ ] **日志级别**：是否明确了错误日志级别（slog.Error vs slog.Warn）？

---

## 📖 参考资料

### 成功案例

1. **BroadcastTracker Callback Pre 文档 v1.3**
   - 文档位置：`docs/06_PM/feature/2026-02-21_PR-broadcast-tracker-callback_Pre.md`
   - 评审评分：待评审
   - 亮点：
     - ✅ Nil 参数行为明确
     - ✅ 触发条件清晰
     - ✅ "仅触发一次"机制详细
     - ✅ 边界场景测试全面
     - ✅ 时间估算合理（7.5h）

### 反面案例

1. **DDD 架构 Pre 文档 v1.6**
   - 文档位置：`docs/06_PM/feature/2026-02-18_PR-nexkv-ddd-architecture_Pre.md`
   - 评审评分：8.5/10（已批准）
   - 不足：
     - ⚠️ Nil 参数行为未明确
     - ⚠️ "仅触发一次"机制未说明
     - ⚠️ 边界场景测试缺失
   - 改进方向：在后续 Phase 实施时补充

---

## 💡 使用建议

### 对架构师

1. **评审前**：使用本检查清单快速扫描 Pre 文档
2. **评审中**：对照检查项逐项评审
3. **评审后**：给出评分和改进建议

### 对开发人员

1. **编写 Pre 文档前**：阅读本检查清单，了解评审标准
2. **编写 Pre 文档时**：对照检查项逐项完善
3. **编写 Pre 文档后**：自检是否覆盖所有必查项

---

**文档版本**: v1.0
**创建日期**: 2026-02-21
**最后更新**: 2026-02-21
**维护者**: NexKV 开发团队
**状态**: ✅ 已批准
