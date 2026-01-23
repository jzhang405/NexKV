# 【PR全流程文档】Feature - UDP 分片传输性能优化（前置规划）

> **文档说明**：本文档是 PR-020 的前置规划文档，定义需求、设计方案和实施计划。
> **状态**: 🔄 待架构师评审

---

## 第一部分：前置部分（待批准）

### 1. 基础信息

| 项目 | 内容 |
|------|------|
| 工作类型 | 性能优化（Performance Optimization） |
| PR编号 | PR-020 |
| 分支名称 | feature/udp-fragmentation-optimization |
| 工作主题 | UDP 分片传输性能优化（位图跟踪 + 反压机制） |
| 负责人 | AI Agent（核心开发工程师 B） |
| 分支创建日期 | 2026-01-23 |
| 预计完成日期 | 2026-01-25 |
| Pre 批准状态 | 🔄 待架构师评审 |

### 2. 核心目标

#### 2.1 性能目标

**当前问题**：
1. **分片重组顺序依赖**（U-003）
   - 当前实现假设分片按顺序到达
   - 中间分片丢失导致永远无法重组
   - 场景：发送 [0,1,2,3]，接收 [0,2,3]（分片 1 丢失）

2. **缺少流量控制**（U-004）
   - 发送端无限制发送，导致接收端通道溢出
   - 场景：发送 1000 条消息，recvCh（4096 缓冲区）溢出
   - 结果：大量消息被丢弃，channelBlockCount 增加

**优化目标**：
1. **位图跟踪机制**（U-003 方案 A）
   - 使用位图跟踪已接收分片
   - 不依赖分片到达顺序
   - 检测所有分片是否收齐

2. **反压机制**（U-004 方案 B）
   - 基于通道大小的反压
   - 防止接收端缓冲区溢出
   - 优雅降级而非直接丢弃

#### 2.2 性能指标

| 指标 | 优化前 | 目标 | 测量方法 |
|------|--------|------|---------|
| **分片重组成功率** | 依赖顺序，分片丢失则失败 | 95%+ | 单元测试模拟丢包 |
| **重组超时** | 无超时，永久等待 | 5 秒超时 | 单元测试 + 集成测试 |
| **通道溢出率** | 无限制，可能溢出 | < 1% | 压力测试 |
| **消息丢失率** | 无流量控制，可能高丢失 | < 0.1% | 集成测试 |

---

### 3. 需求分析

#### 3.1 功能需求

**FR-1: 位图跟踪机制**
- 使用 `big.Int` 位图跟踪已接收分片
- 提供快速"完整性检查"方法
- 支持最大 65535 个分片（uint16 限制）

**FR-2: 反压机制**
- 检测接收端通道使用率
- 超过阈值（75%）时返回错误或延迟发送
- 统计反压触发次数

**FR-3: 超时机制**
- 分片重组超时（5 秒）
- 超时后清理 partial 消息
- 记录超时统计

#### 3.2 非功能需求

**NFR-1: 性能**
- 位图操作延迟 < 100ns
- 反压检查延迟 < 1μs
- 不影响现有 UDP 性能（1,099 ns/op）

**NFR-2: 兼容性**
- 不改变 UDP 分片协议格式
- 向后兼容旧版本（可选择性启用）
- 协议版本号管理

**NFR-3: 可测试性**
- 单元测试覆盖位图操作
- 集成测试模拟分片丢失
- 性能基准测试对比

---

### 4. 设计方案

#### 4.1 位图跟踪机制（U-003 方案 A）

**数据结构**：
```go
type partialMessage struct {
    mu           sync.RWMutex
    msgID        uint64
    total        uint16
    received     uint16
    fragments    [][]byte
    bitmap       *big.Int     // 新增：位图跟踪
    firstMissing uint16       // 新增：首个丢失分片索引
    lastUpdate   time.Time
    timeout      time.Duration // 新增：超时时间
}
```

**核心方法**：
```go
// 检查是否收齐所有分片（不依赖顺序）
func (p *partialMessage) isComplete() bool {
    // 检查位图的前 total 位是否全部为 1
    for i := 0; i < int(p.total); i++ {
        if p.bitmap.Bit(i) == 0 {
            return false
        }
    }
    return true
}

// 添加分片
func (p *partialMessage) addFragment(index uint16, data []byte) error {
    p.mu.Lock()
    defer p.mu.Unlock()

    // 检查是否已接收
    if p.bitmap.Bit(int(index)) == 1 {
        return fmt.Errorf("分片 %d 已存在", index)
    }

    // 存储分片
    p.fragments[index] = data
    p.bitmap.SetBit(int(index), 1)
    p.received++
    p.lastUpdate = time.Now()

    // 更新首个丢失分片索引
    p.updateFirstMissing()

    return nil
}

// 获取首个丢失分片索引（用于 NACK）
func (p *partialMessage) getFirstMissing() uint16 {
    p.mu.RLock()
    defer p.mu.RUnlock()
    return p.firstMissing
}

// 更新首个丢失分片索引
func (p *partialMessage) updateFirstMissing() {
    for i := 0; i < int(p.total); i++ {
        if p.bitmap.Bit(i) == 0 {
            p.firstMissing = uint16(i)
            return
        }
    }
    p.firstMissing = p.total // 全部收到
}

// 检查是否超时
func (p *partialMessage) isTimeout() bool {
    return time.Since(p.lastUpdate) > p.timeout
}
```

#### 4.2 反压机制（U-004 方案 B）

**数据结构扩展**：
```go
type UDPTransport struct {
    // ... 现有字段

    // 反压机制
    backpressureEnabled    bool          // 是否启用反压
    backpressureThreshold  float64       // 反压阈值（0.75）
    backpressureTriggered  atomic.Uint64 // 反压触发次数
}
```

**核心方法**：
```go
// 检查是否可以发送（基于反压）
func (t *UDPTransport) canSend() bool {
    if !t.backpressureEnabled {
        return true
    }

    // 检查接收端通道使用率
    usage := float64(len(t.recvCh)) / float64(cap(t.recvCh))
    if usage > t.backpressureThreshold {
        t.backpressureTriggered.Add(1)
        return false
    }

    return true
}

// 发送消息（带反压检查）
func (t *UDPTransport) Send(ctx context.Context, addr string, msg Message) error {
    // 反压检查
    if !t.canSend() {
        return types.NewTransportError("接收端缓冲区接近满载（反压触发）")
    }

    // 原有发送逻辑
    return t.sendDirect(ctx, addr, msg)
}
```

#### 4.3 超时清理机制

**数据结构扩展**：
```go
type UDPTransport struct {
    // ... 现有字段

    // 超时管理
    fragmentTimeout    time.Duration // 分片重组超时
    timeoutCleaner     *time.Ticker  // 超时清理定时器
    timeoutCleanerStop chan struct{} // 停止信号
}
```

**核心方法**：
```go
// 启动超时清理协程
func (t *UDPTransport) startTimeoutCleaner() {
    t.timeoutCleaner = time.NewTicker(1 * time.Second)
    go func() {
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

// 清理超时的分片
func (t *UDPTransport) cleanTimeoutFragments() {
    t.pendingFragmentsMu.Lock()
    defer t.pendingFragmentsMu.Unlock()

    now := time.Now()
    for key, partial := range t.pendingFragments {
        if now.Sub(partial.lastUpdate) > t.fragmentTimeout {
            // 记录超时统计
            t.stats.RecordFragmentTimeout()
            // 删除 partial
            delete(t.pendingFragments, key)
        }
    }
}
```

---

### 5. 实施计划

#### 5.1 阶段划分

| 阶段 | 任务 | 预估工时 | 交付物 |
|------|------|---------|--------|
| **阶段 1** | 位图跟踪机制实现 | 0.5 天 | partialMessage 扩展、位图操作方法 |
| **阶段 2** | 反压机制实现 | 0.5 天 | canSend() 方法、反压统计 |
| **阶段 3** | 超时清理机制 | 0.5 天 | 超时检测、清理协程 |
| **阶段 4** | 单元测试 | 0.5 天 | 位图测试、反压测试、超时测试 |
| **阶段 5** | 集成测试 | 0.5 天 | 分片丢失模拟、压力测试 |
| **阶段 6** | 性能基准测试 | 0.5 天 | 对比优化前后性能 |
| **阶段 7** | 代码审查和文档 | 0.5 天 | Post 文档、代码审查 |

**总计预估**: 3.5 天

#### 5.2 详细任务清单

**阶段 1: 位图跟踪机制**
- [ ] 扩展 `partialMessage` 结构体
  - [ ] 添加 `bitmap *big.Int` 字段
  - [ ] 添加 `firstMissing uint16` 字段
  - [ ] 添加 `timeout time.Duration` 字段
- [ ] 实现 `isComplete()` 方法
- [ ] 实现 `addFragment()` 方法（带位图更新）
- [ ] 实现 `getFirstMissing()` 方法
- [ ] 实现 `updateFirstMissing()` 方法
- [ ] 实现 `isTimeout()` 方法

**阶段 2: 反压机制**
- [ ] 扩展 `UDPTransport` 结构体
  - [ ] 添加 `backpressureEnabled bool` 字段
  - [ ] 添加 `backpressureThreshold float64` 字段
  - [ ] 添加 `backpressureTriggered atomic.Uint64` 字段
- [ ] 实现 `canSend()` 方法
- [ ] 修改 `Send()` 方法（添加反压检查）
- [ ] 添加反压统计到 Stats

**阶段 3: 超时清理机制**
- [ ] 扩展 `UDPTransport` 结构体
  - [ ] 添加 `fragmentTimeout time.Duration` 字段
  - [ ] 添加 `timeoutCleaner *time.Ticker` 字段
  - [ ] 添加 `timeoutCleanerStop chan struct{}` 字段
- [ ] 实现 `startTimeoutCleaner()` 方法
- [ ] 实现 `cleanTimeoutFragments()` 方法
- [ ] 在 `Stop()` 中停止清理协程
- [ ] 添加超时统计到 Stats

**阶段 4: 单元测试**
- [ ] 测试位图操作（设置、清除、检查）
- [ ] 测试分片重组完整性（不依赖顺序）
- [ ] 测试首个丢失分片索引计算
- [ ] 测试超时检测和清理
- [ ] 测试反压触发逻辑
- [ ] 测试边界条件（0 个分片、65535 个分片）

**阶段 5: 集成测试**
- [ ] 模拟分片丢失场景
- [ ] 模拟乱序到达场景
- [ ] 压力测试（大量小消息）
- [ ] 压力测试（大消息多分片）
- [ ] 验证反压机制有效性
- [ ] 验证超时清理机制

**阶段 6: 性能基准测试**
- [ ] 对比优化前后位图操作性能
- [ ] 对比优化前后反压检查性能
- [ ] 验证 UDP 性能未下降
- [ ] 测试分片重组成功率提升

**阶段 7: 代码审查和文档**
- [ ] 代码审查（self-review）
- [ ] 性能测试报告
- [ ] 编写 Post 文档
- [ ] 更新 UDP Transport 文档

---

### 6. 风险评估

| 风险 | 概率 | 影响 | 缓解措施 |
|------|------|------|---------|
| **位图性能下降** | 中 | 中 | 使用 `big.Int` 原子操作，性能测试验证 |
| **反压过于敏感** | 中 | 低 | 可配置阈值，默认 75% |
| **超时清理误删** | 低 | 中 | 保守超时时间（5 秒），添加日志 |
| **协议不兼容** | 低 | 高 | 不修改协议格式，仅内部优化 |
| **测试覆盖不足** | 中 | 中 | 单元测试 + 集成测试 + 性能测试 |

---

### 7. 验收标准

#### 7.1 功能验收

- [ ] **位图跟踪**: 支持不依赖顺序的分片重组
- [ ] **反压机制**: 接收端通道使用率 > 75% 时触发反压
- [ ] **超时清理**: 分片重组超时 5 秒后自动清理
- [ ] **统计增强**: 新增反压触发次数、超时次数统计

#### 7.2 性能验收

- [ ] **位图操作**: 延迟 < 100ns
- [ ] **反压检查**: 延迟 < 1μs
- [ ] **UDP 性能**: 不低于现有性能（1,099 ns/op）
- [ ] **分片重组成功率**: > 95%（模拟 5% 丢包率）

#### 7.3 质量验收

- [ ] **单元测试**: 覆盖率 > 80%
- [ ] **集成测试**: 所有场景通过
- [ ] **性能基准测试**: 无性能下降
- [ ] **代码审查**: 无 P0/P1 问题

---

### 8. 相关资料

#### 8.1 参考文档

- **Brainstorm 文档**: `docs/06_project_management/brainstorm/transport_2026-01-20_udp-fragmentation-improvements.md`
- **UDP Transport 实现**: `internal/metadata/transport/udp_transport.go`
- **PR-012 Post 文档**: `docs/06_project_management/pr_documents/feature/2026-01-20_PR-012_UDP-Transport_Post.md`

#### 8.2 相关 Issue

- U-003: 分片重组顺序依赖问题
- U-004: 缺少流量控制机制

---

## 文档归档信息

| 项目 | 内容 |
|------|------|
| 文档版本 | v1.0（Pre 阶段） |
| 创建日期 | 2026-01-23 |
| 归档路径 | `docs/06_project_management/pr_documents/feature/2026-01-23_PR-020_UDP-Fragmentation-Optimization_Pre.md` |
| 后续维护人 | AI Agent（核心开发工程师 B） |

---

**创建者**: AI Agent（核心开发工程师 B）
**审核者**: 👤 架构师（待评审）
**状态**: 🔄 待架构师评审
**下一步**: 等待架构师评审 Pre 文档，评审通过后启动开发
