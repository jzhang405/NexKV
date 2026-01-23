# PR-020 UDP 分片优化 - 风险跟踪清单

> **文档说明**: 本文档将 PR-020 Pre 文档中的风险转化为可执行的开发检查项，确保风险得到有效控制。
> **创建日期**: 2026-01-23
> **最后更新**: 2026-01-23（v1.2 - 补充开发细节检查项）
> **状态**: ✅ 已批准，准备开发

---

## 📋 风险概览

| 风险 ID | 风险名称 | 概率 | 影响 | 优先级 |
|---------|---------|------|------|--------|
| **R-001** | 位图性能下降 | 中 | 中 | P1 |
| **R-002** | 超时清理误删 | 低 | 中 | P2 |
| **R-003** | 协议不兼容 | 低 | 高 | P0 |
| **R-004** | 测试覆盖不足 | 中 | 中 | P1 |
| **R-005** | 并发安全问题 | **高** | **高** | **P0** ⚠️ |
| **R-006** | 内存泄漏风险 | 中 | 中 | P1 |
| **R-007** | 性能基准不准确 | 中 | 中 | P1 |

> **⚠️ 架构师评审新增风险**：
> - **R-005（并发安全）**：`big.Int` 非并发安全，必须严格 mutex 保护（**P0**）
> - **R-006（内存泄漏）**：超时清理失效可能导致 partial 消息累积（**P1**）
> - **R-007（性能基准）**：没有基准数据无法验证优化效果（**P1**）
>
> **移除风险**：
> - ~~R-002 反压过于敏感~~（反压机制已移除，设计无效）

---

## R-001: 位图性能下降

### 风险描述
引入 `big.Int` 位图操作后，可能增加分片重组的延迟，影响 UDP 原有的高性能指标（1,099 ns/op）。

### 性能目标
- 位图操作延迟 < 100ns
- 整体 UDP 性能不低于 1,099 ns/op

### 开发检查项

#### ✅ 检查项 1.1: 位图操作性能基准测试
**实施阶段**: 阶段 6（性能基准测试）

**检查内容**:
- [ ] 编写位图操作性能测试
  - `bitmap.SetBit()` 延迟测试
  - `bitmap.Bit()` 延迟测试
  - 批量位图操作测试（模拟 100 个分片）
- [ ] 验证位图操作延迟 < 100ns
- [ ] 记录性能基准数据

**验证方法**:
```bash
# 运行性能基准测试
go test -bench=BenchmarkBitmap -benchmem ./internal/metadata/transport/
```

**通过标准**:
- `SetBit` 操作: < 50ns
- `Bit` 操作: < 30ns
- 批量操作（100 次）: < 5μs

---

#### ✅ 检查项 1.2: 分片重组性能对比测试
**实施阶段**: 阶段 6（性能基准测试）

**检查内容**:
- [ ] 对比优化前后的 `addFragment()` 性能
- [ ] 对比优化前后的 `isComplete()` 性能
- [ ] 验证整体 UDP 性能未下降

**验证方法**:
```bash
# 运行分片重组性能测试
go test -bench=BenchmarkFragmentReassembly -benchmem ./internal/metadata/transport/
```

**通过标准**:
- `addFragment()`: 性能下降 < 10%
- `isComplete()`: 性能下降 < 10%
- UDP `Send()`: <= 1,099 ns/op（现有性能）

---

#### ✅ 检查项 1.3: 使用原子操作优化
**实施阶段**: 阶段 1（位图跟踪机制实现）

**检查内容**:
- [ ] 使用 `big.Int` 的原子操作方法
- [ ] 避免锁竞争（分片级别锁）
- [ ] 代码审查确认无性能瓶颈

**代码示例**:
```go
// ✅ 推荐：使用分片级锁
func (p *partialMessage) addFragment(index uint16, data []byte) error {
    p.mu.Lock()
    defer p.mu.Unlock()
    p.bitmap.SetBit(int(index), 1)  // 原子操作
    // ...
}

// ❌ 避免：全局锁
var globalMutex sync.Mutex
func addFragment(index uint16, data []byte) {
    globalMutex.Lock()
    defer globalMutex.Unlock()
    // ...
}
```

---

#### ✅ 检查项 1.4: 快速路径边界条件处理
**实施阶段**: 阶段 1（位图跟踪机制实现）

**检查内容**:
- [ ] 实现 `newPartialMessage()` 初始化函数
- [ ] total > 64 时自动使用 `big.Int`（慢速路径）
- [ ] total <= 64 时使用 `bitmapFast`（快速路径）
- [ ] 单元测试验证边界条件（total = 64, 65）

**代码示例**:
```go
// 初始化函数 - 自动路径选择
func newPartialMessage(msgID uint64, total uint16, timeout time.Duration) *partialMessage {
    pm := &partialMessage{
        msgID:      msgID,
        total:      total,
        fragments:  make([][]byte, total),
        firstMissing: 0,
        lastUpdate: time.Now(),
        timeout:    timeout,
    }

    // 路径选择：total > 64 触发慢速路径
    if total > 64 {
        pm.bitmap = new(big.Int)
    }
    // total <= 64 使用快速路径（bitmapFast，默认 0）

    return pm
}
```

**验证方法**:
```go
func TestFastPathBoundaryConditions(t *testing.T) {
    tests := []struct {
        name        string
        total       uint16
        expectBig   bool  // 是否使用 big.Int
    }{
        {"快速路径边界-63", 63, false},
        {"快速路径边界-64", 64, false},
        {"慢速路径边界-65", 65, true},
        {"慢速路径-100", 100, true},
    }
    // ... 测试实现
}
```

**通过标准**:
- total <= 64 时使用 `bitmapFast`（快速路径）
- total > 64 时使用 `big.Int`（慢速路径）
- 边界条件测试全部通过

---

### 风险缓解措施
1. **使用 `big.Int` 原子操作**: `SetBit()`、`Bit()` 都是原子操作，无锁竞争
2. **快速路径优化**: total <= 64 使用 `uint64` 位掩码，O(1) 性能
3. **性能测试验证**: 阶段 6 专项性能基准测试
4. **性能回归监控**: 对比优化前后性能数据

---

## R-002: 超时清理误删

### 风险描述
5 秒的超时时间可能过于保守，部分分片因网络延迟到达时，已被清理，导致重组失败。

### 设计目标
- 保守超时时间（5 秒）避免内存泄漏
- 添加日志便于排查误删场景
- 统计超时次数监控

### 开发检查项

#### ✅ 检查项 3.1: 超时日志记录
**实施阶段**: 阶段 3（超时清理机制）

**检查内容**:
- [ ] 添加超时清理日志（WARNING 级别）
- [ ] 记录超时的 partial 消息信息
  - `msgID`
  - `received` / `total`
  - `duration`
  - `missingFragments`

**代码示例**:
```go
func (t *UDPTransport) cleanTimeoutFragments() {
    for key, partial := range t.pendingFragments {
        if time.Since(partial.lastUpdate) > t.fragmentTimeout {
            // 记录详细日志
            logging.Warnf("分片重组超时 msgID=%d received=%d total=%d duration=%v missing=%v",
                partial.msgID,
                partial.received,
                partial.total,
                time.Since(partial.lastUpdate),
                partial.getMissingIndexes(),
            )

            // 统计超时
            t.stats.RecordFragmentTimeout()

            // 删除
            delete(t.pendingFragments, key)
        }
    }
}
```

---

#### ✅ 检查项 3.2: 超时统计监控
**实施阶段**: 阶段 3（超时清理机制）

**检查内容**:
- [ ] 添加统计字段 `fragmentTimeout atomic.Uint64`
- [ ] 每次超时清理时计数
- [ ] 暴露统计接口

**验证方法**:
```go
// 监控指标
stats := transport.GetStats()
timeoutRate := float64(stats.FragmentTimeout) / float64(stats.TotalFragments)
assert.True(t, timeoutRate < 0.001, "超时率过高: %.2f%%", timeoutRate*100)
```

**通过标准**:
- 正常网络条件下超时率 < 0.1%
- 5% 丢包网络条件下超时率 < 1%

---

#### ✅ 检查项 3.3: 超时时间可配置
**实施阶段**: 阶段 3（超时清理机制）

**检查内容**:
- [ ] 添加配置项 `FragmentTimeout`（默认 5 秒）
- [ ] 支持运行时调整
- [ ] 文档说明超时时间选择理由

**代码示例**:
```go
type UDPTransportConfig struct {
    // ... 现有字段
    FragmentTimeout time.Duration `json:"fragment_timeout"`
}

// 默认配置
const defaultFragmentTimeout = 5 * time.Second

// 配置说明
// - 5 秒超时：适用于局域网环境（RTT < 10ms）
// - 跨公网部署建议：10-30 秒
// - 高丢包环境建议：15-60 秒
```

---

#### ✅ 检查项 2.4: 超时清理锁粒度优化
**实施阶段**: 阶段 2（超时清理机制）

**检查内容**:
- [ ] 实现"快照遍历"优化
- [ ] 先使用读锁复制 keys，减少锁持有时间
- [ ] 再使用写锁删除超时项
- [ ] 避免高并发场景下的全局锁竞争

**代码示例**:
```go
// 优化：快照遍历，减少锁持有时间
func (t *UDPTransport) cleanTimeoutFragments() {
    // 1. 读锁复制 keys（快速释放）
    t.pendingFragmentsMu.RLock()
    keys := make([]uint64, 0, len(t.pendingFragments))
    for k := range t.pendingFragments {
        keys = append(keys, k)
    }
    t.pendingFragmentsMu.RUnlock()

    // 2. 写锁删除超时项（短暂持有）
    t.pendingFragmentsMu.Lock()
    defer t.pendingFragmentsMu.Unlock()

    now := time.Now()
    for _, k := range keys {
        if pm, ok := t.pendingFragments[k]; ok && now.Sub(pm.lastUpdate) > t.fragmentTimeout {
            // 详细结构化日志
            logging.Warnf("分片重组超时 msgID=%d received=%d total=%d duration=%v missing=%v",
                pm.msgID,
                pm.received,
                pm.total,
                now.Sub(pm.lastUpdate),
                pm.getMissingIndexes(),
            )

            t.stats.RecordFragmentTimeout()
            delete(t.pendingFragments, k)
        }
    }
}
```

**性能对比**:
| 方法 | 锁持有时间 | 并发影响 |
|------|-----------|---------|
| **优化前** | 遍历 + 删除全持写锁 | 高并发阻塞 |
| **优化后** | 读锁复制（短）+ 写锁删除（短） | 最小化阻塞 |

**通过标准**:
- 锁持有时间 < 10ms（1000 个 pendingFragments）
- 高并发场景无阻塞
- 通过 `go test -race` 验证

---

### 风险缓解措施
1. **保守超时时间**: 5 秒适用于局域网，跨公网可调整
2. **详细日志记录**: 便于排查误删场景
3. **统计监控**: 实时监控超时频率
4. **锁粒度优化**: 快照遍历减少锁竞争（v1.2 新增）

---

## R-004: 协议不兼容 ⚠️ 高影响风险

### 风险描述
如果优化过程中修改了 UDP 分片的协议格式，会导致新旧版本节点无法互通。

### 设计目标
- **不修改协议格式**
- 仅内部优化实现
- 保证向后兼容

### 开发检查项

#### ✅ 检查项 4.1: 协议格式兼容性检查
**实施阶段**: 全程（每个阶段）

**检查内容**:
- [ ] 确认不修改分片头部格式
- [ ] 确认不修改分片序号字段
- [ ] 确认不修改分片总数字段
- [ ] 确认不修改分片数据字段

**现有协议格式**（必须保持不变）:
```go
// UDP 分片头部（24 字节）
type FragmentHeader struct {
    Magic     uint16  // 0xAF12 (固定)
    Version   uint8   // 协议版本 (当前: 1)
    Flags     uint8   // 标志位
    MsgID     uint64  // 消息唯一标识
    Index     uint16  // 分片索引
    Total     uint16  // 分片总数
    Checksum  uint32  // 校验和
}
```

**验证清单**:
- [ ] `FragmentHeader` 结构体不变
- [ ] `Serialize()` 方法不变
- [ ] `Deserialize()` 方法不变
- [ ] 帧格式字节对齐不变

---

#### ✅ 检查项 4.2: 版本兼容性测试
**实施阶段**: 阶段 5（集成测试）

**检查内容**:
- [ ] 新旧版本互发消息测试
- [ ] 旧版本接收新版本分片
- [ ] 新版本接收旧版本分片
- [ ] 混合版本集群测试

**验证方法**:
```go
func TestProtocolCompatibility(t *testing.T) {
    // 旧版本 transport
    oldTransport := setupOldUDPTransport()

    // 新版本 transport
    newTransport := setupNewUDPTransport()

    // 测试旧版本 -> 新版本
    oldSendNewReceive(t, oldTransport, newTransport)

    // 测试新版本 -> 旧版本
    newSendOldReceive(t, newTransport, oldTransport)
}
```

**通过标准**:
- 旧版本 -> 新版本: ✅ 互通
- 新版本 -> 旧版本: ✅ 互通
- 混合版本集群: ✅ 正常运行

---

#### ✅ 检查项 4.3: 代码审查强制检查
**实施阶段**: 代码审查阶段

**检查内容**:
- [ ] 代码审查确认无协议格式修改
- [ ] Lint 检查通过
- [ ] 单元测试覆盖协议字段
- [ ] 集成测试验证兼容性

**代码审查清单**:
```markdown
## R-004 协议兼容性检查

- [ ] FragmentHeader 结构体未修改
- [ ] 分片序列化方法未修改
- [ ] 分片反序列化方法未修改
- [ ] 无新增协议字段
- [ ] 无新增协议标志位
- [ ] 版本兼容性测试通过
```

---

### 风险缓解措施
1. **协议格式锁定**: 明确禁止修改协议格式
2. **版本兼容性测试**: 强制要求新旧版本互通测试
3. **代码审查清单**: 每次提交强制检查协议兼容性

---

## R-005: 测试覆盖不足

### 风险描述
单元测试和集成测试无法覆盖所有边界场景（如 65535 个分片、极端丢包），导致上线后出现隐藏问题。

### 设计目标
- 三层测试策略（单元 + 集成 + 性能）
- 边界场景全覆盖
- 代码覆盖率 > 80%

### 开发检查项

#### ✅ 检查项 5.1: 单元测试覆盖边界场景
**实施阶段**: 阶段 4（单元测试）

**检查内容**:
- [ ] 0 个分片（空消息）
- [ ] 1 个分片（无需分片）
- [ ] 2 个分片（最小分片数）
- [ ] 65535 个分片（最大分片数）
- [ ] 全部接收（完整场景）
- [ ] 全部丢失（极端场景）
- [ ] 中间分片丢失（乱序场景）
- [ ] 第一个分片丢失
- [ ] 最后一个分片丢失

**测试用例示例**:
```go
func TestBitmapBoundaryConditions(t *testing.T) {
    tests := []struct {
        name        string
        total       uint16
        received    []uint16
        expectComplete bool
    }{
        {"0个分片", 0, []uint16{}, false},
        {"1个分片", 1, []uint16{0}, true},
        {"2个分片-完整", 2, []uint16{0, 1}, true},
        {"2个分片-丢失", 2, []uint16{0}, false},
        {"最大分片数", 65535, generateAllIndices(65535), true},
        // ...
    }
    // ...
}
```

---

#### ✅ 检查项 5.2: 集成测试模拟真实场景
**实施阶段**: 阶段 5（集成测试）

**检查内容**:
- [ ] 模拟 5% 丢包率环境
- [ ] 模拟 10% 丢包率环境
- [ ] 模拟乱序到达场景
- [ ] 模拟网络抖动场景
- [ ] 模拟大量并发分片

**测试用例示例**:
```go
func TestFragmentLossScenarios(t *testing.T) {
    tests := []struct {
        name         string
        lossRate     float64      // 丢包率
        msgSize      int          // 消息大小
        expectSuccess bool         // 是否预期成功
    }{
        {"无丢包", 0.0, 1024, true},
        {"5%丢包", 0.05, 1024, true},
        {"10%丢包", 0.10, 1024, true},
        {"20%丢包", 0.20, 1024, false},
        {"大消息+5%丢包", 0.05, 1024*1024, true},
    }
    // ...
}
```

---

#### ✅ 检查项 5.3: 性能基准测试覆盖
**实施阶段**: 阶段 6（性能基准测试）

**检查内容**:
- [ ] 位图操作性能测试
- [ ] 分片重组性能测试
- [ ] 反压检查性能测试
- [ ] 超时清理性能测试
- [ ] 整体 UDP 性能测试

**性能基准清单**:
```go
// 基准测试用例
func BenchmarkBitmapSetBit(b *testing.B)
func BenchmarkBitmapBit(b *testing.B)
func BenchmarkFragmentReassembly(b *testing.B)
func BenchmarkBackpressureCheck(b *testing.B)
func BenchmarkTimeoutCleaner(b *testing.B)
func BenchmarkUDPSend(b *testing.B)
```

---

#### ✅ 检查项 5.4: 代码覆盖率统计
**实施阶段**: 阶段 7（代码审查）

**检查内容**:
- [ ] 运行覆盖率测试
- [ ] 验证覆盖率 > 80%
- [ ] 检查未覆盖代码行
- [ ] 补充测试用例

**验证方法**:
```bash
# 运行覆盖率测试
go test -coverprofile=coverage.out ./internal/metadata/transport/
go tool cover -html=coverage.out

# 查看覆盖率
go tool cover -func=coverage.out
```

**通过标准**:
- 总体覆盖率 > 80%
- 核心逻辑覆盖率 > 90%
- 分片重组逻辑覆盖率 > 95%

---

### 风险缓解措施
1. **三层测试策略**: 单元 + 集成 + 性能
2. **边界场景全覆盖**: 最小/最大值、极端情况
3. **代码覆盖率强制**: > 80% 覆盖率要求

---

## R-005: 并发安全问题 ⚠️ P0 高影响风险

### 风险描述
`big.Int` 在 Go 中**不是并发安全的**，如果多个 goroutine 同时访问位图，会导致数据竞争和不可预测的行为。

### 设计目标
- 所有 `big.Int` 访问都在 mutex 保护下
- 通过 `go test -race` 验证无竞态条件
- 支持高并发分片接收场景

### 开发检查项

#### ✅ 检查项 5.1: Mutex 保护设计
**实施阶段**: 阶段 1（位图跟踪机制实现）

**检查内容**:
- [ ] 所有 `big.Int` 访问都在 `p.mu.Lock()` 或 `p.mu.RLock()` 保护下
- [ ] 添加详细的并发安全注释
- [ ] 代码审查确认无 unprotected 访问

**代码示例**:
```go
// ✅ 正确：在 mutex 保护下访问 big.Int
func (p *partialMessage) isComplete() bool {
    p.mu.RLock()
    defer p.mu.RUnlock()

    for i := 0; i < int(p.total); i++ {
        if p.bitmap.Bit(i) == 0 {  // 安全：读锁保护
            return false
        }
    }
    return true
}

// ❌ 错误：未加锁访问 big.Int
func (p *partialMessage) isCompleteUnsafe() bool {
    for i := 0; i < int(p.total); i++ {
        if p.bitmap.Bit(i) == 0 {  // 危险：无锁保护
            return false
        }
    }
    return true
}
```

---

#### ✅ 检查项 5.2: 竞态检测测试
**实施阶段**: 阶段 3（单元测试）

**检查内容**:
- [ ] 运行 `go test -race` 验证无竞态条件
- [ ] 高并发分片接收测试（1000 并发）
- [ ] 压力测试（10000 次分片添加）

**验证方法**:
```bash
# 运行竞态检测
go test -race -count=1000 ./internal/metadata/transport/

# 运行高并发测试
go test -race -run TestConcurrentFragmentReassembly -v ./internal/metadata/transport/
```

**通过标准**:
- `go test -race` 无 WARNING 输出
- 高并发测试无 panic 或数据竞争
- 压力测试稳定运行

---

#### ✅ 检查项 5.3: 并发安全代码审查
**实施阶段**: 阶段 6（代码审查和文档）

**检查内容**:
- [ ] 代码审查确认所有 big.Int 访问都在锁保护下
- [ ] 代码审查确认无死锁风险
- [ ] 添加并发安全设计文档

**代码审查清单**:
```markdown
## R-005 并发安全检查

- [ ] isComplete(): 使用 RLock 保护 ✅
- [ ] addFragment(): 使用 Lock 保护 ✅
- [ ] getFirstMissing(): 使用 RLock 保护 ✅
- [ ] updateFirstMissingUnsafe(): 内部方法，调用者已持有锁 ✅
- [ ] isTimeout(): 使用 RLock 保护 ✅
- [ ] 无 unprotected 的 big.Int 访问 ✅
- [ ] 无死锁风险 ✅
```

---

### 风险缓解措施
1. **Mutex 保护**: 所有 big.Int 访问都在锁保护下
2. **Race 测试**: 使用 `go test -race` 验证
3. **代码审查**: 人工审查并发安全设计

---

## R-006: 内存泄漏风险

### 风险描述
超时清理机制可能失效（如协程停止、清理逻辑错误），导致 `partialMessage` 在 `pendingFragments` 中累积，造成内存泄漏。

### 设计目标
- 超时清理机制可靠运行
- 长时间运行无内存泄漏
- 监控 pendingFragments 大小

### 开发检查项

#### ✅ 检查项 6.1: 超时清理可靠性
**实施阶段**: 阶段 2（超时清理机制）

**检查内容**:
- [ ] 清理协程正确启动
- [ ] 清理协程在 `Stop()` 时正确停止
- [ ] 清理逻辑无遗漏（遍历所有 pendingFragments）

**代码示例**:
```go
// 启动清理协程
func (t *UDPTransport) startTimeoutCleaner() {
    t.timeoutCleaner = time.NewTicker(1 * time.Second)
    go func() {
        defer t.timeoutCleaner.Stop()
        for {
            select {
            case <-t.timeoutCleaner.C:
                t.cleanTimeoutFragments()
            case <-t.timeoutCleanerStop:
                return
            }
        }
    }()
}

// 停止清理协程
func (t *UDPTransport) Stop() error {
    // ...
    close(t.timeoutCleanerStop)
    // ...
}
```

---

#### ✅ 检查项 6.2: 内存泄漏测试
**实施阶段**: 阶段 4（集成测试）

**检查内容**:
- [ ] 长时间运行测试（24 小时）
- [ ] 监控 pendingFragments 大小
- [ ] 监控内存使用情况

**验证方法**:
```go
func TestMemoryLeak(t *testing.T) {
    transport := setupTestTransport()

    // 模拟大量分片（包含超时场景）
    for i := 0; i < 10000; i++ {
        transport.Send(context.Background(), addr, largeMsg)
    }

    // 等待超时清理
    time.Sleep(10 * time.Second)

    // 验证 pendingFragments 已清理
    assert.Equal(t, 0, len(transport.GetPendingFragments()))
}
```

**通过标准**:
- 24 小时运行后内存稳定
- pendingFragments 大小 < 10
- 无内存持续增长

---

#### ✅ 检查项 6.3: 监控指标暴露
**实施阶段**: 阶段 2（超时清理机制）

**检查内容**:
- [ ] 暴露 `GetPendingFragmentsCount()` 方法
- [ ] 添加超时清理统计
- [ ] 添加日志记录超时清理事件

**代码示例**:
```go
// 获取 pendingFragments 数量
func (t *UDPTransport) GetPendingFragmentsCount() int {
    t.pendingFragmentsMu.RLock()
    defer t.pendingFragmentsMu.RUnlock()
    return len(t.pendingFragments)
}
```

---

### 风险缓解措施
1. **清理协程可靠性**: 确保正确启动和停止
2. **长时间测试**: 24 小时运行验证
3. **监控指标**: 暴露 pendingFragments 大小

---

## R-007: 性能基准不准确

### 风险描述
没有建立优化前的性能基准数据，无法验证优化效果是否达到预期，可能导致"优化后性能下降"未被发现。

### 设计目标
- 优化前建立性能基准
- 对比优化前后性能数据
- 验证优化效果符合预期

### 开发检查项

#### ✅ 检查项 7.1: 阶段 0 性能基准测试
**实施阶段**: 阶段 0（性能基准测试）

**检查内容**:
- [ ] 测量当前 UDP ForwardMessage 性能
- [ ] 测量当前分片重组性能
- [ ] 记录当前内存使用情况
- [ ] 建立性能基准数据文件

**验证方法**:
```bash
# 运行性能基准测试
go test -bench=. -benchmem ./internal/metadata/transport/ > baseline.txt
```

**基准数据示例**:
```
BenchmarkUDPForwardMessage-8    1000000    1099 ns/op    512 B/op    8 allocs/op
BenchmarkFragmentReassembly-8     100000    8234 ns/op   2048 B/op   16 allocs/op
```

---

#### ✅ 检查项 7.2: 优化后性能对比
**实施阶段**: 阶段 5（性能基准测试）

**检查内容**:
- [ ] 运行优化后性能测试
- [ ] 对比基准数据，验证性能未下降
- [ ] 验证新增功能的性能开销

**验证方法**:
```bash
# 运行优化后测试
go test -bench=. -benchmem ./internal/metadata/transport/ > optimized.txt

# 对比结果
benchstat baseline.txt optimized.txt
```

**通过标准**:
- UDP ForwardMessage 性能不下降
- isComplete() < 1μs（total=100）
- SetBit/Bit < 100ns
- 新增功能开销 < 10%

---

#### ✅ 检查项 7.3: 性能基准测试场景覆盖
**实施阶段**: 阶段 6（性能基准测试）

**检查内容**:
- [ ] **场景 1：小消息（快速路径）**
  - total = 50（≤ 64，使用 `bitmapFast`）
  - 目标：isComplete() < 50ns
  - 基准测试：`BenchmarkIsComplete_FastPath()`

- [ ] **场景 2：中等消息（慢速路径）**
  - total = 100（> 64，使用 `big.Int`）
  - 目标：isComplete() < 1μs
  - 基准测试：`BenchmarkIsComplete_SlowPath()`

- [ ] **场景 3：极限消息（边界条件）**
  - total = 65535（最大分片数）
  - 目标：无性能崩溃，延迟合理
  - 基准测试：`BenchmarkIsComplete_ExtremePath()`

- [ ] **延迟分位数记录**
  - 记录 P50（中位数）
  - 记录 P99（99 分位数）
  - 记录 P999（99.9 分位数）
  - 工具：`go test -benchtime=10s -bench` + 自定义统计

**测试代码示例**:
```go
// 场景 1: 小消息（快速路径）
func BenchmarkIsComplete_FastPath(b *testing.B) {
    pm := newPartialMessage(1, 50, 5*time.Second) // total=50 <= 64
    fillAllFragments(pm)
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        pm.isComplete()
    }
}

// 场景 2: 中等消息（慢速路径）
func BenchmarkIsComplete_SlowPath(b *testing.B) {
    pm := newPartialMessage(1, 100, 5*time.Second) // total=100 > 64
    fillAllFragments(pm)
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        pm.isComplete()
    }
}

// 场景 3: 极限消息（边界条件）
func BenchmarkIsComplete_ExtremePath(b *testing.B) {
    pm := newPartialMessage(1, 65535, 5*time.Second) // total=65535
    fillAllFragments(pm)
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        pm.isComplete()
    }
}
```

**性能目标清单**:
| 场景 | total | 路径 | 目标 | 记录分位数 |
|------|-------|------|------|-----------|
| 小消息 | 50 | 快速 | < 50ns | P50/P99/P999 |
| 中等消息 | 100 | 慢速 | < 1μs | P50/P99/P999 |
| 极限消息 | 65535 | 慢速 | < 10μs | P50/P99/P999 |

**通过标准**:
- 所有场景的 P99 延迟达标
- 极限场景无性能崩溃
- 性能回归 < 5%

---

#### ✅ 检查项 7.4: 性能回归监控
**实施阶段**: 阶段 6（代码审查和文档）

**检查内容**:
- [ ] 性能测试报告包含基准对比
- [ ] Post 文档记录性能数据
- [ ] 标记性能目标达成情况
- [ ] 包含 P50/P99/P999 延迟分位数

**报告模板**:
```markdown
## 性能测试结果

### 基准对比
| 指标 | 基准 | 优化后 | 目标 | 达成 |
|------|------|--------|------|------|
| UDP ForwardMessage | 1099 ns/op | 1087 ns/op | ≤ 1099 | ✅ |
| SetBit/Bit | N/A | 45 ns/op | < 100ns | ✅ |

### isComplete() 性能（三类场景）
| 场景 | total | P50 | P99 | P999 | 目标 | 达成 |
|------|-------|-----|-----|------|------|------|
| 小消息 | 50 | 35ns | 42ns | 48ns | < 50ns | ✅ |
| 中等消息 | 100 | 0.72μs | 0.85μs | 0.94μs | < 1μs | ✅ |
| 极限消息 | 65535 | 7.2μs | 8.5μs | 9.4μs | < 10μs | ✅ |
```

---

### 风险缓解措施
1. **阶段 0 基准测试**: 优化前建立基准
2. **三类场景覆盖**: 小/中/极限消息全覆盖
3. **延迟分位数记录**: P50/P99/P999 全面监控
4. **性能报告**: 记录所有性能数据和对比

---

## R-008: 日志与监控不足

### 风险描述
缺少结构化日志和监控指标，导致生产环境问题排查困难，无法及时发现异常情况（如超时率突增、内存泄漏）。

### 设计目标
- 结构化日志记录关键信息（msgID、total、received、firstMissing）
- 监控指标暴露（timeoutCount、pendingFragmentsCount、isCompleteLatency）
- 支持日志级别切换（INFO/WARNING/ERROR）
- 日志便于排查和告警

### 开发检查项

#### ✅ 检查项 8.1: 结构化日志实现
**实施阶段**: 阶段 1-3（位图跟踪 + 超时清理）

**检查内容**:
- [ ] `addFragment()` 添加 INFO 级别日志
  - 记录：msgID、total、received、firstMissing
  - 触发：每接收一个分片
  - 格式：`logging.Infof("接收分片 msgID=%d total=%d received=%d firstMissing=%d", ...)`

- [ ] `addFragment()` 添加 WARNING 级别日志（超时预警）
  - 触发：接收到超时风险分片（duration > 3 秒）
  - 记录：msgID、total、received、duration、missing

- [ ] `cleanTimeoutFragments()` 添加 WARNING 级别日志
  - 触发：清理超时 partialMessage
  - 记录：msgID、received、total、duration、missing

**日志格式示例**:
```go
// INFO：正常分片接收
logging.Infof("接收分片 msgID=%d total=%d received=%d firstMissing=%d",
    pm.msgID, pm.total, pm.received, pm.firstMissing)

// WARNING：超时预警
if duration > 3*time.Second {
    logging.Warnf("分片接收超时预警 msgID=%d total=%d received=%d duration=%v missing=%v",
        pm.msgID, pm.total, pm.received, duration, pm.getMissingIndexes())
}

// WARNING：超时清理
logging.Warnf("分片重组超时 msgID=%d received=%d total=%d duration=%v missing=%v",
    pm.msgID, pm.received, pm.total, duration, pm.getMissingIndexes())
```

**通过标准**:
- 所有关键路径都有结构化日志
- 日志字段包含完整的排查信息
- 日志级别使用合理（INFO/WARNING/ERROR）

---

#### ✅ 检查项 8.2: 监控指标暴露
**实施阶段**: 阶段 2-3（超时清理机制）

**检查内容**:
- [ ] 添加 `timeoutCount` 统计字段
  - 类型：`atomic.Uint64`
  - 更新时机：每次超时清理时
  - 用途：超时率监控和告警

- [ ] 添加 `pendingFragmentsCount` 统计字段
  - 类型：函数返回值（实时）
  - 实现方式：读锁保护
  - 用途：内存泄漏监控

- [ ] 添加 `isCompleteLatency` 统计字段
  - 类型：直方图或 P50/P99/P999
  - 更新时机：每次 `isComplete()` 调用时
  - 用途：性能回归监控

**监控指标示例**:
```go
// UDPTransport 统计字段
type UDPTransportStats struct {
    timeoutCount          atomic.Uint64  // 超时清理次数
    totalFragments        atomic.Uint64  // 总接收分片数
    isCompleteLatency     *LatencyHistogram  // isComplete() 延迟直方图
}

// 暴露监控接口
func (t *UDPTransport) GetStats() UDPTransportStats {
    return UDPTransportStats{
        TimeoutCount:  t.stats.timeoutCount.Load(),
        TotalFragments: t.stats.totalFragments.Load(),
        // ...
    }
}

// pendingFragments 数量（内存泄漏监控）
func (t *UDPTransport) GetPendingFragmentsCount() int {
    t.pendingFragmentsMu.RLock()
    defer t.pendingFragmentsMu.RUnlock()
    return len(t.pendingFragments)
}
```

**监控告警规则**:
| 指标 | 告警条件 | 级别 | 处理建议 |
|------|---------|------|---------|
| timeoutCount | 超时率 > 1% | WARNING | 检查网络质量 |
| pendingFragmentsCount | > 1000 | WARNING | 可能内存泄漏 |
| isCompleteLatency P99 | > 2μs | WARNING | 性能回归 |

**通过标准**:
- 所有关键指标都有统计
- 指标暴露接口可用
- 告警规则已定义

---

#### ✅ 检查项 8.3: 日志验证与测试
**实施阶段**: 阶段 4（单元测试）

**检查内容**:
- [ ] 单元测试验证日志输出
- [ ] 验证日志字段完整性
- [ ] 验证日志级别正确性

**测试代码示例**:
```go
func TestStructuredLogging(t *testing.T) {
    // 捕获日志输出
    logger, hook := test.NewNullLogger()
    logging.SetLogger(logger)

    transport := setupTestTransport()

    // 发送分片
    transport.Send(context.Background(), addr, largeMsg)

    // 验证 INFO 日志
    assert.True(t, hook.LastEntry().Level == log.InfoLevel)
    assert.Contains(t, hook.LastEntry().Message, "接收分片")
    assert.Contains(t, hook.LastEntry().Data["msgID"], uint64(1))

    // 验证 WARNING 日志（超时场景）
    time.Sleep(6 * time.Second)  // 触发超时
    assert.True(t, hook.Entries[len(hook.Entries)-2].Level == log.WarnLevel)
    assert.Contains(t, hook.Entries[len(hook.Entries)-2].Message, "分片重组超时")
}
```

**通过标准**:
- 日志测试覆盖率 > 80%
- 所有关键日志路径有测试
- 日志字段完整性验证通过

---

### 风险缓解措施
1. **结构化日志**: 记录关键信息，便于排查
2. **监控指标**: 实时暴露统计数据
3. **告警规则**: 超时率、内存泄漏、性能回归
4. **日志测试**: 验证日志输出完整性

---

## 📊 风险跟踪汇总表

| 风险 ID | 风险名称 | 检查项总数 | 已完成 | 待完成 | 完成率 |
|---------|---------|-----------|--------|--------|--------|
| **R-001** | 位图性能下降 | 4 | 0 | 4 | 0% |
| **R-002** | 超时清理误删 | 4 | 0 | 4 | 0% |
| **R-003** | 协议不兼容 | 3 | 0 | 3 | 0% |
| **R-004** | 测试覆盖不足 | 4 | 0 | 4 | 0% |
| **R-005** | 并发安全问题 | 3 | 0 | 3 | 0% |
| **R-006** | 内存泄漏风险 | 3 | 0 | 3 | 0% |
| **R-007** | 性能基准不准确 | 4 | 0 | 4 | 0% |
| **R-008** | 日志与监控不足 | 3 | 0 | 3 | 0% |
| **总计** | **8 个风险** | **28** | **0** | **28** | **0%** |

> **⚠️ v1.2 更新说明**（补充开发细节检查项）：
> - **R-001 新增**：检查项 1.4（快速路径边界条件处理）
> - **R-002 新增**：检查项 2.4（超时清理锁粒度优化）
> - **R-007 更新**：检查项 7.3（三类场景性能测试）、7.4（性能回归监控含 P50/P99/P999）
> - **R-008 新增**：日志与监控检查项（8.1 结构化日志、8.2 监控指标、8.3 日志验证）
> - 检查项总数：22 → 28

> **⚠️ v1.1 更新说明**（架构师评审更新）：
> - 移除 R-002（反压过于敏感）- 反压机制已移除
> - 新增 R-005（并发安全）- 3 项检查项
> - 新增 R-006（内存泄漏）- 3 项检查项
> - 新增 R-007（性能基准）- 3 项检查项

> **v1.0 初始版本**：
> - 初始 16 项检查项，覆盖 5 个核心风险

---

## ✅ 风险应对流程

### 开发前
1. 阅读本风险跟踪清单
2. 确认理解所有风险点
3. 准备缓解措施

### 开发中
1. 按阶段执行检查项
2. 勾选已完成的检查项
3. 记录发现的新风险

### 开发后
1. 复查所有检查项
2. 统计风险完成率
3. 编写风险总结报告

---

## 📝 使用说明

### 如何使用本清单

1. **开发前**: 阅读"风险描述"和"设计目标"
2. **开发中**: 按阶段执行"开发检查项"，勾选已完成项
3. **代码审查**: 使用检查清单验证风险控制
4. **Post 文档**: 记录风险处理结果

### 检查项状态符号

- ✅ **必须执行**: 强制要求，必须完成
- ⚠️ **高优先级**: 高度推荐，优先完成
- 💡 **建议执行**: 可选，视情况完成

---

**文档创建**: 2026-01-23
**最后更新**: 2026-01-23（v1.2 - 补充开发细节检查项）
**创建者**: AI Agent（核心开发工程师 B）
**维护者**: AI Agent（核心开发工程师 B）
**状态**: ✅ 已批准，准备开发

---

## 📝 版本历史

| 版本 | 日期 | 变更说明 | 检查项数 |
|------|------|---------|---------|
| **v1.2** | 2026-01-23 | 补充开发细节检查项 | **28 项** |
| v1.1 | 2026-01-23 | 架构师评审更新（新增 R-005/006/007） | 22 项 |
| v1.0 | 2026-01-23 | 初始版本 | 16 项 |

### v1.2 详细变更

**新增检查项**:
- ✅ 检查项 1.4：快速路径边界条件处理（newPartialMessage 函数）
- ✅ 检查项 2.4：超时清理锁粒度优化（快照遍历）
- ✅ 检查项 7.3：性能基准测试场景覆盖（小/中/极限消息 + P50/P99/P999）
- ✅ 检查项 7.4：性能回归监控（含延迟分位数报告）
- ✅ 检查项 8.1：结构化日志实现（INFO/WARNING 日志）
- ✅ 检查项 8.2：监控指标暴露（timeoutCount/pendingFragmentsCount/isCompleteLatency）
- ✅ 检查项 8.3：日志验证与测试（日志输出测试）

**风险覆盖**:
- 8 个核心风险
- 28 项可执行检查项
- 覆盖性能、安全、测试、监控全方位
