# 【预研报告】HLC 混合逻辑时钟设计

> **预研目标**：设计混合逻辑时钟解决分布式时钟漂移问题

---

## 📋 预研信息

| 项目 | 内容 |
|------|------|
| **预研主题** | HLC (Hybrid Logical Clock) 混合逻辑时钟设计 |
| **预研日期** | 2026-02-14 |
| **预研负责人** | 🤖 核心开发 A |
| **关联文档** | `2026-02-14_consistency-implementation-review.md` |
| **预研状态** | ✅ 已完成 |

---

## 1. 问题分析

### 1.1 物理时钟的问题

```
分布式系统中的时钟漂移问题：

节点 A (时间: 10:00:01.000)  ←── 写入 x=1
         ↓
    网络延迟 50ms
         ↓
节点 B (时间: 10:00:00.950)  ←── 时钟落后 50ms
         ↓
    写入 x=2 (时间戳: 10:00:00.950)
         ↓
    LWW: max(10:00:01.000, 10:00:00.950) = 10:00:01.000
         ↓
    结果: x=1 获胜，但实际 B 的写入在 A 之后！
```

**问题总结**：
| 问题 | 影响 |
|------|------|
| 时钟漂移 | 不同节点时钟不同步 |
| NTP 跳变 | 时钟可能回退 |
| 无法排序 | 无法确定操作先后顺序 |
| 因果违反 | 可能违反 happens-before 关系 |

### 1.2 现有方案对比

| 方案 | 优点 | 缺点 |
|------|------|------|
| **物理时钟** | 简单 | 时钟漂移，NTP 跳变 |
| **Lamport 时钟** | 简单，保证因果 | 不反映真实时间 |
| **向量时钟** | 精确因果 | 空间复杂度高 O(n) |
| **TrueTime (Spanner)** | 精确 | 需要原子钟/GPS |
| **HLC** | 兼顾时间和因果 | 实现稍复杂 |

---

## 2. HLC 设计原理

### 2.1 HLC 定义

HLC (Hybrid Logical Clock) 结合物理时钟和逻辑时钟：

```
HLC 时间戳 = (物理时间, 逻辑时间)

特性：
1. 单调递增
2. 反映真实时间（接近 NTP 时间）
3. 满足 happens-before 关系
4. 空间复杂度 O(1)
```

### 2.2 HLC 时间戳结构

```go
// HLCimestamp HLC 时间戳
type HLCimestamp struct {
    // 物理时间：毫秒级 Unix 时间戳
    PhysicalTime int64

    // 逻辑时间：同一物理时间内的计数器
    LogicalTime int64

    // 节点 ID（可选，用于调试和冲突解决）
    NodeID string
}

// Compare 比较两个 HLC 时间戳
// 返回: 1 表示 a > b, -1 表示 a < b, 0 表示相等
func (a HLCimestamp) Compare(b HLCimestamp) int {
    if a.PhysicalTime > b.PhysicalTime {
        return 1
    }
    if a.PhysicalTime < b.PhysicalTime {
        return -1
    }
    if a.LogicalTime > b.LogicalTime {
        return 1
    }
    if a.LogicalTime < b.LogicalTime {
        return -1
    }
    return 0
}

// After 判断 a 是否在 b 之后
func (a HLCimestamp) After(b HLCimestamp) bool {
    return a.Compare(b) > 0
}

// Before 判断 a 是否在 b 之前
func (a HLCimestamp) Before(b HLCimestamp) bool {
    return a.Compare(b) < 0
}

// String 格式化输出
func (h HLCimestamp) String() string {
    return fmt.Sprintf("%d.%d@%s", h.PhysicalTime, h.LogicalTime, h.NodeID)
}
```

---

## 3. HLC 实现

### 3.1 核心 HLC 结构

```go
// HybridLogicalClock 混合逻辑时钟
type HybridLogicalClock struct {
    mu            sync.Mutex
    physicalTime  int64  // 当前物理时间
    logicalTime   int64  // 当前逻辑时间
    nodeID        string // 节点 ID
    maxOffset     int64  // 最大时钟偏移（用于检测异常）
}

// NewHybridLogicalClock 创建 HLC
func NewHybridLogicalClock(nodeID string, maxOffset time.Duration) *HybridLogicalClock {
    return &HybridLogicalClock{
        physicalTime: time.Now().UnixNano() / int64(time.Millisecond),
        logicalTime:  0,
        nodeID:       nodeID,
        maxOffset:    int64(maxOffset / time.Millisecond),
    }
}

// Now 获取当前 HLC 时间戳
func (h *HybridLogicalClock) Now() HLCimestamp {
    h.mu.Lock()
    defer h.mu.Unlock()

    // 获取当前物理时间
    now := time.Now().UnixNano() / int64(time.Millisecond)

    if now > h.physicalTime {
        // 物理时间推进，重置逻辑时间
        h.physicalTime = now
        h.logicalTime = 0
    } else {
        // 物理时间未推进，递增逻辑时间
        h.logicalTime++
    }

    return HLCimestamp{
        PhysicalTime: h.physicalTime,
        LogicalTime:  h.logicalTime,
        NodeID:       h.nodeID,
    }
}

// Update 更新时钟（收到远程消息时调用）
func (h *HybridLogicalClock) Update(remote HLCimestamp) HLCimestamp {
    h.mu.Lock()
    defer h.mu.Unlock()

    // 获取当前物理时间
    now := time.Now().UnixNano() / int64(time.Millisecond)

    // 检测时钟异常
    if remote.PhysicalTime > now+h.maxOffset {
        log.Warn("Remote timestamp is too far in the future",
            "remote", remote,
            "local_now", now,
            "max_offset", h.maxOffset)
    }

    // HLC 更新算法
    // 新物理时间 = max(local物理时间, remote物理时间, 当前时间)
    newPhysical := max(h.physicalTime, remote.PhysicalTime, now)

    var newLogical int64

    if newPhysical == h.physicalTime && newPhysical == remote.PhysicalTime {
        // 三者相等：逻辑时间取最大 + 1
        newLogical = max(h.logicalTime, remote.LogicalTime) + 1
    } else if newPhysical == h.physicalTime {
        // 本地物理时间最大：逻辑时间 + 1
        newLogical = h.logicalTime + 1
    } else if newPhysical == remote.PhysicalTime {
        // 远程物理时间最大：远程逻辑时间 + 1
        newLogical = remote.LogicalTime + 1
    } else {
        // 当前时间最大（正常情况）：重置逻辑时间
        newLogical = 0
    }

    h.physicalTime = newPhysical
    h.logicalTime = newLogical

    return HLCimestamp{
        PhysicalTime: h.physicalTime,
        LogicalTime:  h.logicalTime,
        NodeID:       h.nodeID,
    }
}
```

### 3.2 HLC 时间戳序列化

```go
// MarshalBinary 序列化为字节
func (h HLCimestamp) MarshalBinary() ([]byte, error) {
    buf := make([]byte, 16+len(h.NodeID))
    binary.BigEndian.PutUint64(buf[0:8], uint64(h.PhysicalTime))
    binary.BigEndian.PutUint64(buf[8:16], uint64(h.LogicalTime))
    copy(buf[16:], h.NodeID)
    return buf, nil
}

// UnmarshalBinary 从字节反序列化
func (h *HLCimestamp) UnmarshalBinary(data []byte) error {
    if len(data) < 16 {
        return errors.New("data too short")
    }
    h.PhysicalTime = int64(binary.BigEndian.Uint64(data[0:8]))
    h.LogicalTime = int64(binary.BigEndian.Uint64(data[8:16]))
    if len(data) > 16 {
        h.NodeID = string(data[16:])
    }
    return nil
}

// MarshalJSON JSON 序列化
func (h HLCimestamp) MarshalJSON() ([]byte, error) {
    return json.Marshal(struct {
        PhysicalTime int64  `json:"pt"`
        LogicalTime  int64  `json:"lt"`
        NodeID       string `json:"node"`
    }{
        PhysicalTime: h.PhysicalTime,
        LogicalTime:  h.LogicalTime,
        NodeID:       h.NodeID,
    })
}
```

---

## 4. HLC 应用场景

### 4.1 LWW-Register（最后写入胜利）

```go
// LWWRegisterWithHLC 使用 HLC 的 LWW 寄存器
type LWWRegisterWithHLC struct {
    mu      sync.RWMutex
    value   []byte
    version HLCimestamp
    hlc     *HybridLogicalClock
}

// Set 设置值
func (r *LWWRegisterWithHLC) Set(value []byte) HLCimestamp {
    r.mu.Lock()
    defer r.mu.Unlock()

    newVersion := r.hlc.Now()

    // 只有新时间戳更大时才更新
    if newVersion.After(r.version) {
        r.value = value
        r.version = newVersion
    }

    return r.version
}

// SetWithTimestamp 使用指定时间戳设置值（用于复制）
func (r *LWWRegisterWithHLC) SetWithTimestamp(value []byte, ts HLCimestamp) HLCimestamp {
    r.mu.Lock()
    defer r.mu.Unlock()

    // 更新 HLC
    r.hlc.Update(ts)

    // 只有新时间戳更大时才更新
    if ts.After(r.version) {
        r.value = value
        r.version = ts
    }

    return r.version
}

// Get 获取值
func (r *LWWRegisterWithHLC) Get() ([]byte, HLCimestamp) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    return r.value, r.version
}
```

### 4.2 版本号生成

```go
// VersionGenerator 基于 HLC 的版本号生成器
type VersionGenerator struct {
    hlc *HybridLogicalClock
}

// Generate 生成版本号
func (g *VersionGenerator) Generate() int64 {
    ts := g.hlc.Now()
    // 将 HLC 时间戳编码为单一 int64
    // 高 48 位：物理时间，低 16 位：逻辑时间
    return (ts.PhysicalTime << 16) | (ts.LogicalTime & 0xFFFF)
}

// Parse 解析版本号
func (g *VersionGenerator) Parse(version int64) HLCimestamp {
    return HLCimestamp{
        PhysicalTime: version >> 16,
        LogicalTime:  version & 0xFFFF,
    }
}
```

### 4.3 因果一致性检测

```go
// CausalityTracker 因果追踪器
type CausalityTracker struct {
    mu         sync.RWMutex
    hlc        *HybridLogicalClock
    seen       map[string]HLCimestamp // 已见过的操作
}

// RecordOperation 记录操作
func (t *CausalityTracker) RecordOperation(opID string, ts HLCimestamp) {
    t.mu.Lock()
    defer t.mu.Unlock()

    t.hlc.Update(ts)
    t.seen[opID] = ts
}

// HappenedBefore 判断 a 是否 happened-before b
func (t *CausalityTracker) HappenedBefore(a, b HLCimestamp) bool {
    return a.Before(b)
}

// IsCausallyReady 检查操作是否因果就绪
func (t *CausalityTracker) IsCausallyReady(ts HLCimestamp, dependencies []HLCimestamp) bool {
    t.mu.RLock()
    defer t.mu.RUnlock()

    for _, dep := range dependencies {
        if ts.Before(dep) {
            return false
        }
    }
    return true
}
```

---

## 5. 与 Tree Coordinator 集成

### 5.1 树拓扑感知的 Gossip 传播

**设计洞察**：Gossip 不用乱发，可以利用树拓扑优化传播：

```
传播策略：
- 叶子节点 → 父节点（向上传播）
- 父节点 → 子节点（向下广播）
- 越靠近叶子节点越快，root 节点最慢

好处：
1. 减少冗余消息
2. 利用树的层次结构
3. 本地性优化
```

```go
// TreeAwareGossip 树感知的 Gossip 传播
type TreeAwareGossip struct {
    hlc      *HybridLogicalClock
    topology TreeTopology
    localID  string

    // 传播配置
    fanoutToParent   int // 向父节点传播的节点数
    fanoutToChildren int // 向子节点传播的节点数
}

// GossipEvent Gossip 事件
type GossipEvent struct {
    Key       string
    Value     []byte
    Timestamp HLCimestamp
    Source    string
    HopCount  int // 跳数（用于调试）
}

// Broadcast 广播事件
func (g *TreeAwareGossip) Broadcast(key string, value []byte) {
    event := GossipEvent{
        Key:       key,
        Value:     value,
        Timestamp: g.hlc.Now(),
        Source:    g.localID,
        HopCount:  0,
    }

    // 根据拓扑位置决定传播策略
    node := g.topology.GetNode(g.localID)

    if node.IsLeaf() {
        // 叶子节点：只向父节点传播
        g.sendToParent(event)
    } else if node.IsRoot() {
        // 根节点：向所有子节点传播
        g.sendToChildren(event)
    } else {
        // 中间节点：双向传播
        g.sendToParent(event)
        g.sendToChildren(event)
    }
}

// Receive 接收事件
func (g *TreeAwareGossip) Receive(event GossipEvent) error {
    // 更新 HLC
    g.hlc.Update(event.Timestamp)

    // 检查是否已处理（去重）
    // ...

    // 本地处理
    // ...

    // 继续传播（增加跳数）
    event.HopCount++

    node := g.topology.GetNode(g.localID)

    // 根据来源决定传播方向
    if event.Source == node.ParentID {
        // 来自父节点，向子节点传播
        g.sendToChildren(event)
    } else {
        // 来自子节点，向父节点传播
        g.sendToParent(event)
    }

    return nil
}

// sendToParent 向父节点发送
func (g *TreeAwareGossip) sendToParent(event GossipEvent) {
    node := g.topology.GetNode(g.localID)
    if node.ParentID == "" {
        return // 没有父节点
    }

    // 发送到父节点
    g.send(event, node.ParentID)
}

// sendToChildren 向子节点发送
func (g *TreeAwareGossip) sendToChildren(event GossipEvent) {
    node := g.topology.GetNode(g.localID)

    for _, childID := range node.Children {
        g.send(event, childID)
    }
}
```

### 5.2 HLC 在三层一致性中的应用

```go
// TreeCoordinatorWithHLC 带 HLC 的 Tree Coordinator
type TreeCoordinatorWithHLC struct {
    hlc       *HybridLogicalClock
    gossip    *TreeAwareGossip
    // ...
}

// Put 写入（带 HLC 时间戳）
func (c *TreeCoordinatorWithHLC) Put(ctx context.Context, ns kvstore.Namespace, key string, value []byte) error {
    // 生成 HLC 时间戳
    ts := c.hlc.Now()

    // 根据层级选择一致性策略
    layer := c.getLayerForNamespace(ns)

    switch layer {
    case Layer1:
        return c.putWith2PC(ctx, ns, key, value, ts)
    case Layer2:
        return c.putWithQuorum(ctx, ns, key, value, ts)
    case Layer3:
        return c.putWithGossip(ctx, ns, key, value, ts)
    }

    return nil
}

// putWithGossip 使用 Gossip 写入（带 HLC）
func (c *TreeCoordinatorWithHLC) putWithGossip(ctx context.Context, ns kvstore.Namespace, key string, value []byte, ts HLCimestamp) error {
    // 1. 本地写入（带时间戳）
    if err := c.localStore.PutWithTimestamp(ns, key, value, ts); err != nil {
        return err
    }

    // 2. 树感知 Gossip 传播
    c.gossip.Broadcast(key, value)

    return nil
}
```

---

## 6. Porcupine 验证

### 6.1 HLC 模型

```go
// HLCModel HLC 的 Porcupine 验证模型
func HLCModel() porcupine.Model {
    return porcupine.Model{
        Init: func() interface{} {
            return &HLCState{
                Store: make(map[string]VersionedValue),
                HLC: &HLCimestamp{
                    PhysicalTime: 0,
                    LogicalTime:  0,
                },
            }
        },
        Step: func(state, input, output interface{}) (bool, interface{}) {
            st := state.(*HLCState)
            op := input.(HLCOperation)

            switch op.Type {
            case "write":
                // 更新 HLC
                newSt := st.Clone()
                newHLC := st.HLC.Max(op.Timestamp)

                // 写入
                existing, exists := newSt.Store[op.Key]
                if !exists || op.Timestamp.After(existing.Version) {
                    newSt.Store[op.Key] = VersionedValue{
                        Value:   op.Value,
                        Version: op.Timestamp,
                    }
                }

                newSt.HLC = &newHLC
                return output == "ok", newSt

            case "read":
                val, exists := st.Store[op.Key]
                if !exists {
                    return output == nil, st
                }

                // 验证返回的值和版本
                outVal := output.(ReadOutput)
                return bytes.Equal(outVal.Value, val.Value) &&
                       outVal.Version == val.Version, st

            case "hlc_update":
                // 更新 HLC（收到远程消息）
                newSt := st.Clone()
                newHLC := st.HLC.Max(op.Timestamp)
                if op.Timestamp.PhysicalTime > newHLC.PhysicalTime {
                    newHLC.PhysicalTime = op.Timestamp.PhysicalTime
                    newHLC.LogicalTime = op.Timestamp.LogicalTime
                } else if op.Timestamp.PhysicalTime == newHLC.PhysicalTime {
                    newHLC.LogicalTime = max(newHLC.LogicalTime, op.Timestamp.LogicalTime) + 1
                }
                newSt.HLC = &newHLC
                return output == "ok", newSt
            }

            return false, st
        },
    }
}

// HLCState HLC 状态
type HLCState struct {
    Store map[string]VersionedValue
    HLC   *HLCimestamp
}

func (s *HLCState) Clone() *HLCState {
    newStore := make(map[string]VersionedValue)
    for k, v := range s.Store {
        newStore[k] = v
    }
    return &HLCState{
        Store: newStore,
        HLC:   &HLCimestamp{PhysicalTime: s.HLC.PhysicalTime, LogicalTime: s.HLC.LogicalTime},
    }
}

// Max 返回较大的时间戳
func (h HLCimestamp) Max(other HLCimestamp) HLCimestamp {
    if h.After(other) {
        return h
    }
    return other
}

type HLCOperation struct {
    Type      string
    Key       string
    Value     []byte
    Timestamp HLCimestamp
}

type ReadOutput struct {
    Value   []byte
    Version HLCimestamp
}
```

### 6.2 验证场景

```go
// TestHLC_Causality 测试因果一致性
func TestHLC_Causality(t *testing.T) {
    model := HLCModel()
    recorder := NewHLCRecorder()

    // 场景：A 写入，B 读取后写入（因果依赖）

    // 1. A 写入 k1=v1
    recorder.Record("A", "write", HLCOperation{
        Type:      "write",
        Key:       "k1",
        Value:     []byte("v1"),
        Timestamp: HLCimestamp{PhysicalTime: 100, LogicalTime: 0, NodeID: "A"},
    }, "ok")

    // 2. A 的 HLC 更新（模拟时间推进）
    recorder.Record("A", "hlc_update", HLCOperation{
        Type:      "hlc_update",
        Timestamp: HLCimestamp{PhysicalTime: 100, LogicalTime: 0, NodeID: "A"},
    }, "ok")

    // 3. B 收到 A 的消息，HLC 更新
    recorder.Record("B", "hlc_update", HLCOperation{
        Type:      "hlc_update",
        Timestamp: HLCimestamp{PhysicalTime: 100, LogicalTime: 0, NodeID: "A"},
    }, "ok")

    // 4. B 写入 k2=v2（因果依赖于 A 的写入）
    recorder.Record("B", "write", HLCOperation{
        Type:      "write",
        Key:       "k2",
        Value:     []byte("v2"),
        Timestamp: HLCimestamp{PhysicalTime: 100, LogicalTime: 1, NodeID: "B"}, // B 的 HLC 已更新
    }, "ok")

    // 验证
    result, _ := porcupine.CheckOperations(model, recorder.GetHistory(), time.Minute)
    assert.Equal(t, porcupine.Ok, result)
}

// TestHLC_LWW 测试 LWW 语义
func TestHLC_LWW(t *testing.T) {
    model := HLCModel()
    recorder := NewHLCRecorder()

    // 场景：两个节点同时写入，后写入的获胜

    // 1. A 写入 k1=v1 (ts=100.0)
    recorder.Record("A", "write", HLCOperation{
        Type:      "write",
        Key:       "k1",
        Value:     []byte("v1"),
        Timestamp: HLCimestamp{PhysicalTime: 100, LogicalTime: 0, NodeID: "A"},
    }, "ok")

    // 2. B 写入 k1=v2 (ts=100.1) - 逻辑时间更大
    recorder.Record("B", "write", HLCOperation{
        Type:      "write",
        Key:       "k1",
        Value:     []byte("v2"),
        Timestamp: HLCimestamp{PhysicalTime: 100, LogicalTime: 1, NodeID: "B"},
    }, "ok")

    // 3. 读取应该返回 v2
    recorder.Record("C", "read", HLCOperation{
        Type: "read",
        Key:  "k1",
    }, ReadOutput{Value: []byte("v2"), Version: HLCimestamp{PhysicalTime: 100, LogicalTime: 1}})

    // 验证
    result, _ := porcupine.CheckOperations(model, recorder.GetHistory(), time.Minute)
    assert.Equal(t, porcupine.Ok, result)
}

// TestHLC_ClockSkew 测试时钟漂移容忍
func TestHLC_ClockSkew(t *testing.T) {
    model := HLCModel()
    recorder := NewHLCRecorder()

    // 场景：节点 B 时钟落后，但 HLC 仍然正确排序

    // 1. A (时钟正常) 写入 k1=v1 (ts=100.0)
    recorder.Record("A", "write", HLCOperation{
        Type:      "write",
        Key:       "k1",
        Value:     []byte("v1"),
        Timestamp: HLCimestamp{PhysicalTime: 100, LogicalTime: 0, NodeID: "A"},
    }, "ok")

    // 2. B 收到 A 的消息，HLC 更新到 100.0
    recorder.Record("B", "hlc_update", HLCOperation{
        Type:      "hlc_update",
        Timestamp: HLCimestamp{PhysicalTime: 100, LogicalTime: 0, NodeID: "A"},
    }, "ok")

    // 3. B (时钟落后，本地时间=90) 写入 k1=v2
    // 但 HLC 会使用 max(90, 100) = 100，逻辑时间 +1
    recorder.Record("B", "write", HLCOperation{
        Type:      "write",
        Key:       "k1",
        Value:     []byte("v2"),
        Timestamp: HLCimestamp{PhysicalTime: 100, LogicalTime: 1, NodeID: "B"},
    }, "ok")

    // 4. 读取应该返回 v2（B 的写入在 HLC 上更晚）
    recorder.Record("C", "read", HLCOperation{
        Type: "read",
        Key:  "k1",
    }, ReadOutput{Value: []byte("v2"), Version: HLCimestamp{PhysicalTime: 100, LogicalTime: 1}})

    // 验证
    result, _ := porcupine.CheckOperations(model, recorder.GetHistory(), time.Minute)
    assert.Equal(t, porcupine.Ok, result)
}
```

---

## 7. 总结

### 7.1 HLC 特性

| 特性 | 说明 |
|------|------|
| **单调递增** | 永不回退 |
| **因果一致** | 满足 happens-before |
| **接近真实时间** | 反映物理时间 |
| **空间高效** | O(1) 空间 |
| **时钟漂移容忍** | 自动处理时钟差异 |

### 7.2 与各层的关系

```
┌─────────────────────────────────────────────────────────┐
│                   HLC 应用层次                            │
├─────────────────────────────────────────────────────────┤
│  Layer 1 (2PC)                                           │
│  - 事务时间戳                                            │
│  - 版本号生成                                            │
├─────────────────────────────────────────────────────────┤
│  Layer 2 (Quorum)                                        │
│  - LWW-Register 冲突解决                                  │
│  - 读写版本号                                            │
├─────────────────────────────────────────────────────────┤
│  Layer 3 (Gossip)                                        │
│  - 事件时间戳                                            │
│  - 因果追踪                                              │
│  - 树感知传播优化 ✅                                      │
└─────────────────────────────────────────────────────────┘
```

### 7.3 树感知 Gossip 优化

| 优化点 | 效果 |
|--------|------|
| 叶子节点只向父传播 | 减少冗余消息 |
| 根节点只向子广播 | 避免环回 |
| 中间节点双向传播 | 保证可达性 |
| HLC 时间戳 | 因果一致 |

---

**文档版本**: v1.0
**创建日期**: 2026-02-14
**最后更新**: 2026-02-14
**维护者**: 🤖 核心开发 A
**状态**: ✅ 已完成
