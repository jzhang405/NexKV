# 【预研报告】Porcupine 增强模型设计

> **预研目标**：设计拓扑感知、版本校验、失败恢复的 Porcupine 增强验证模型

---

## 📋 预研信息

| 项目 | 内容 |
|------|------|
| **预研主题** | Porcupine 增强模型设计（拓扑感知 + 版本校验 + 失败恢复）|
| **预研日期** | 2026-02-14 |
| **预研负责人** | 🤖 核心开发 A |
| **关联文档** | `2026-02-14_porcupine-runtime-verification.md` |
| **预研状态** | ✅ 已完成 |

---

## 1. 现有模型的局限性

### 1.1 原始 Layer1 模型

```go
// 原始模型（过于简化）
func Layer1Model() porcupine.Model {
    return porcupine.Model{
        Step: func(state, input, output interface{}) (bool, interface{}) {
            op := input.(Operation)
            switch op.OpType {
            case "put":
                // 问题：假设所有节点都参与
                // 实际：只有父节点+兄弟节点参与
                newSt := copyMap(st)
                newSt[op.Key] = op.Value
                return output == "ok", newSt
            }
        },
    }
}
```

### 1.2 局限性分析

| 局限性 | 问题 | 影响 |
|--------|------|------|
| **无拓扑感知** | 假设所有节点参与 | 无法验证拓扑相关的 Bug |
| **无版本校验** | 不检查版本号 | 可能漏检版本冲突 |
| **无失败处理** | 假设操作总是成功 | 无法验证回滚逻辑 |
| **无跨层事务** | 只验证单层 | 无法验证 Saga |
| **无 Leader 切换** | 不验证 Leader 变更 | 无法验证 Fencing Token |

---

## 2. 增强模型设计

### 2.1 拓扑感知模型

```go
// TopologyAwareModel 拓扑感知的 Porcupine 模型
func TopologyAwareModel() porcupine.Model {
    return porcupine.Model{
        Init: func() interface{} {
            return &TopologyState{
                // 每个节点的存储
                NodeStores: make(map[string]map[string]VersionedValue),

                // 拓扑信息
                Topology: &Topology{
                    Nodes:    make(map[string]*NodeInfo),
                    ParentOf: make(map[string][]string), // 父节点 -> 子节点列表
                    ChildOf:  make(map[string]string),   // 子节点 -> 父节点
                },

                // 当前 Leader
                CurrentLeader: "",
                CurrentTerm:   0,
            }
        },
        Step: func(state, input, output interface{}) (bool, interface{}) {
            st := state.(*TopologyState)
            op := input.(TopologyOperation)

            switch op.Type {
            case "init_topology":
                // 初始化拓扑
                newSt := st.Clone()
                for _, node := range op.Nodes {
                    newSt.Topology.Nodes[node.ID] = node
                    newSt.NodeStores[node.ID] = make(map[string]VersionedValue)

                    // 建立父子关系
                    if node.ParentID != "" {
                        newSt.Topology.ParentOf[node.ParentID] = append(
                            newSt.Topology.ParentOf[node.ParentID], node.ID)
                        newSt.Topology.ChildOf[node.ID] = node.ParentID
                    }
                }
                return output == "ok", newSt

            case "write_with_2pc":
                // 2PC 写入（拓扑感知）
                return handle2PCWrite(st, op, output)

            case "write_with_quorum":
                // Quorum 写入（拓扑感知）
                return handleQuorumWrite(st, op, output)

            case "write_with_gossip":
                // Gossip 写入（树感知传播）
                return handleGossipWrite(st, op, output)

            case "read":
                // 读取操作
                return handleRead(st, op, output)

            case "leader_change":
                // Leader 变更
                return handleLeaderChange(st, op, output)
            }

            return false, st
        },
    }
}

// TopologyState 拓扑状态
type TopologyState struct {
    NodeStores    map[string]map[string]VersionedValue
    Topology      *Topology
    CurrentLeader string
    CurrentTerm   uint64
}

// Topology 拓扑信息
type Topology struct {
    Nodes    map[string]*NodeInfo
    ParentOf map[string][]string
    ChildOf  map[string]string
}

// NodeInfo 节点信息
type NodeInfo struct {
    ID       string
    ParentID string
    Children []string
    IsLeader bool
}

// TopologyOperation 拓扑操作
type TopologyOperation struct {
    Type       string
    NodeID     string
    Key        string
    Value      []byte
    Timestamp  HLCimestamp
    Version    uint64
    Term       uint64
    NewLeader  string
    Nodes      []*NodeInfo
    Participants []string
}

// Clone 克隆状态
func (s *TopologyState) Clone() *TopologyState {
    newNodeStores := make(map[string]map[string]VersionedValue)
    for nodeID, store := range s.NodeStores {
        newStore := make(map[string]VersionedValue)
        for k, v := range store {
            newStore[k] = v
        }
        newNodeStores[nodeID] = newStore
    }

    return &TopologyState{
        NodeStores:    newNodeStores,
        Topology:      s.Topology, // 拓扑不变，共享引用
        CurrentLeader: s.CurrentLeader,
        CurrentTerm:   s.CurrentTerm,
    }
}
```

### 2.2 2PC 写入处理

```go
// handle2PCWrite 处理 2PC 写入（拓扑感知）
func handle2PCWrite(st *TopologyState, op TopologyOperation, output interface{}) (bool, interface{}) {
    // 验证参与者
    node := st.Topology.Nodes[op.NodeID]
    if node == nil {
        return false, st
    }

    // 2PC 参与者：父节点 + 兄弟节点 + 本地节点
    participants := []string{op.NodeID}
    if node.ParentID != "" {
        participants = append(participants, node.ParentID)
    }
    // 添加兄弟节点
    if siblings := st.Topology.ParentOf[node.ParentID]; len(siblings) > 0 {
        for _, sibling := range siblings {
            if sibling != op.NodeID {
                participants = append(participants, sibling)
            }
        }
    }

    // 验证所有参与者的状态
    newSt := st.Clone()
    for _, participantID := range participants {
        store := newSt.NodeStores[participantID]

        // 检查版本冲突
        if existing, exists := store[op.Key]; exists {
            if existing.Version >= op.Version {
                return false, st // 版本冲突
            }
        }

        // 更新存储
        store[op.Key] = VersionedValue{
            Value:   op.Value,
            Version: op.Version,
        }
    }

    return output == "ok", newSt
}
```

### 2.3 Quorum 写入处理

```go
// handleQuorumWrite 处理 Quorum 写入（拓扑感知）
func handleQuorumWrite(st *TopologyState, op TopologyOperation, output interface{}) (bool, interface{}) {
    // 验证参与者
    if len(op.Participants) == 0 {
        return false, st
    }

    // 计算 Quorum
    quorum := (len(op.Participants) / 2) + 1

    // 统计成功数
    successCount := 0
    newSt := st.Clone()

    for _, participantID := range op.Participants {
        store := newSt.NodeStores[participantID]
        if store == nil {
            continue
        }

        // 检查版本冲突
        if existing, exists := store[op.Key]; exists {
            if existing.Version >= op.Version {
                continue // 跳过冲突的节点
            }
        }

        // 更新存储
        store[op.Key] = VersionedValue{
            Value:   op.Value,
            Version: op.Version,
        }
        successCount++
    }

    // 验证是否达到 Quorum
    if successCount >= quorum {
        return output == "ok", newSt
    }

    return output == "quorum_failed", st
}
```

### 2.4 树感知 Gossip 传播

```go
// handleGossipWrite 处理 Gossip 写入（树感知传播）
func handleGossipWrite(st *TopologyState, op TopologyOperation, output interface{}) (bool, interface{}) {
    node := st.Topology.Nodes[op.NodeID]
    if node == nil {
        return false, st
    }

    newSt := st.Clone()

    // 1. 本地写入
    newSt.NodeStores[op.NodeID][op.Key] = VersionedValue{
        Value:   op.Value,
        Version: op.Version,
    }

    // 2. 模拟树感知传播
    // 叶子节点 -> 父节点
    // 父节点 -> 子节点
    // 根节点 -> 所有子节点

    visited := make(map[string]bool)
    visited[op.NodeID] = true

    var propagate func(nodeID string)
    propagate = func(nodeID string) {
        node := st.Topology.Nodes[nodeID]
        if node == nil {
            return
        }

        // 向父节点传播
        if node.ParentID != "" && !visited[node.ParentID] {
            visited[node.ParentID] = true
            newSt.NodeStores[node.ParentID][op.Key] = VersionedValue{
                Value:   op.Value,
                Version: op.Version,
            }
            propagate(node.ParentID)
        }

        // 向子节点传播
        for _, childID := range node.Children {
            if !visited[childID] {
                visited[childID] = true
                newSt.NodeStores[childID][op.Key] = VersionedValue{
                    Value:   op.Value,
                    Version: op.Version,
                }
                propagate(childID)
            }
        }
    }

    propagate(op.NodeID)

    return output == "ok", newSt
}

// handleRead 处理读取
func handleRead(st *TopologyState, op TopologyOperation, output interface{}) (bool, interface{}) {
    store := st.NodeStores[op.NodeID]
    if store == nil {
        return output == nil, st
    }

    val, exists := store[op.Key]
    if !exists {
        return output == nil, st
    }

    // 验证返回的值
    outVal := output.(ReadOutput)
    return bytes.Equal(outVal.Value, val.Value) &&
           outVal.Version == val.Version, st
}

// handleLeaderChange 处理 Leader 变更
func handleLeaderChange(st *TopologyState, op TopologyOperation, output interface{}) (bool, interface{}) {
    // 验证 Term 递增
    if op.Term <= st.CurrentTerm {
        return false, st // Term 不能回退
    }

    newSt := st.Clone()
    newSt.CurrentLeader = op.NewLeader
    newSt.CurrentTerm = op.Term

    return output == "ok", newSt
}
```

---

## 3. 失败恢复模型

### 3.1 失败场景建模

```go
// FailureRecoveryModel 失败恢复的 Porcupine 模型
func FailureRecoveryModel() porcupine.Model {
    return porcupine.Model{
        Init: func() interface{} {
            return &FailureRecoveryState{
                NodeStores:     make(map[string]map[string]VersionedValue),
                Transactions:   make(map[string]*TransactionState),
                FailedNodes:    make(map[string]bool),
                RecoveredNodes: make(map[string]bool),
            }
        },
        Step: func(state, input, output interface{}) (bool, interface{}) {
            st := state.(*FailureRecoveryState)
            op := input.(FailureRecoveryOperation)

            switch op.Type {
            case "node_fail":
                // 节点故障
                newSt := st.Clone()
                newSt.FailedNodes[op.NodeID] = true
                return output == "ok", newSt

            case "node_recover":
                // 节点恢复
                newSt := st.Clone()
                delete(newSt.FailedNodes, op.NodeID)
                newSt.RecoveredNodes[op.NodeID] = true
                return output == "ok", newSt

            case "write_with_2pc":
                // 2PC 写入（考虑故障节点）
                return handle2PCWithFailure(st, op, output)

            case "write_with_quorum":
                // Quorum 写入（考虑故障节点）
                return handleQuorumWithFailure(st, op, output)

            case "saga_begin":
                // 开始 Saga 事务
                newSt := st.Clone()
                newSt.Transactions[op.TxID] = &TransactionState{
                    ID:     op.TxID,
                    Status: TxStatusRunning,
                    Ops:    make([]SagaOp, 0),
                }
                return output == "ok", newSt

            case "saga_execute":
                // 执行 Saga 操作
                return handleSagaExecute(st, op, output)

            case "saga_compensate":
                // 补偿 Saga 操作
                return handleSagaCompensate(st, op, output)

            case "saga_commit":
                // 提交 Saga
                newSt := st.Clone()
                if tx, exists := newSt.Transactions[op.TxID]; exists {
                    tx.Status = TxStatusCommitted
                }
                return output == "ok", newSt

            case "saga_abort":
                // 中止 Saga
                newSt := st.Clone()
                if tx, exists := newSt.Transactions[op.TxID]; exists {
                    tx.Status = TxStatusCompensated
                }
                return output == "ok", newSt

            case "read":
                return handleReadWithFailure(st, op, output)
            }

            return false, st
        },
    }
}

// FailureRecoveryState 失败恢复状态
type FailureRecoveryState struct {
    NodeStores     map[string]map[string]VersionedValue
    Transactions   map[string]*TransactionState
    FailedNodes    map[string]bool
    RecoveredNodes map[string]bool
}

// TransactionState 事务状态
type TransactionState struct {
    ID     string
    Status TxStatus
    Ops    []SagaOp
}

// SagaOp Saga 操作
type SagaOp struct {
    Layer    Layer
    Key      string
    Value    []byte
    OldValue []byte
    Status   OpStatus
}

// FailureRecoveryOperation 失败恢复操作
type FailureRecoveryOperation struct {
    Type        string
    NodeID      string
    TxID        string
    Layer       Layer
    Key         string
    Value       []byte
    Version     uint64
    Participants []string
}

// handle2PCWithFailure 处理带故障的 2PC
func handle2PCWithFailure(st *FailureRecoveryState, op FailureRecoveryOperation, output interface{}) (bool, interface{}) {
    // 检查参与者是否有故障
    for _, participantID := range op.Participants {
        if st.FailedNodes[participantID] {
            // 有故障节点，2PC 应该失败
            return output == "failed", st
        }
    }

    // 所有参与者正常，执行写入
    newSt := st.Clone()
    for _, participantID := range op.Participants {
        store := newSt.NodeStores[participantID]
        if store == nil {
            store = make(map[string]VersionedValue)
            newSt.NodeStores[participantID] = store
        }
        store[op.Key] = VersionedValue{
            Value:   op.Value,
            Version: op.Version,
        }
    }

    return output == "ok", newSt
}

// handleQuorumWithFailure 处理带故障的 Quorum
func handleQuorumWithFailure(st *FailureRecoveryState, op FailureRecoveryOperation, output interface{}) (bool, interface{}) {
    // 过滤故障节点
    var healthyParticipants []string
    for _, participantID := range op.Participants {
        if !st.FailedNodes[participantID] {
            healthyParticipants = append(healthyParticipants, participantID)
        }
    }

    // 计算 Quorum
    quorum := (len(op.Participants) / 2) + 1

    if len(healthyParticipants) < quorum {
        // 无法达到 Quorum
        return output == "quorum_failed", st
    }

    // 执行写入
    newSt := st.Clone()
    for _, participantID := range healthyParticipants {
        store := newSt.NodeStores[participantID]
        if store == nil {
            store = make(map[string]VersionedValue)
            newSt.NodeStores[participantID] = store
        }
        store[op.Key] = VersionedValue{
            Value:   op.Value,
            Version: op.Version,
        }
    }

    return output == "ok", newSt
}

// handleSagaExecute 处理 Saga 执行
func handleSagaExecute(st *FailureRecoveryState, op FailureRecoveryOperation, output interface{}) (bool, interface{}) {
    tx, exists := st.Transactions[op.TxID]
    if !exists {
        return false, st
    }

    // 检查节点是否故障
    if st.FailedNodes[op.NodeID] {
        return output == "failed", st
    }

    newSt := st.Clone()

    // 获取旧值
    store := newSt.NodeStores[op.NodeID]
    if store == nil {
        store = make(map[string]VersionedValue)
        newSt.NodeStores[op.NodeID] = store
    }

    var oldValue []byte
    if existing, exists := store[op.Key]; exists {
        oldValue = existing.Value
    }

    // 执行写入
    store[op.Key] = VersionedValue{
        Value:   op.Value,
        Version: 1,
    }

    // 记录操作
    newTx := newSt.Transactions[op.TxID]
    newTx.Ops = append(newTx.Ops, SagaOp{
        Layer:    op.Layer,
        Key:      op.Key,
        Value:    op.Value,
        OldValue: oldValue,
        Status:   OpStatusCompleted,
    })

    return output == "ok", newSt
}

// handleSagaCompensate 处理 Saga 补偿
func handleSagaCompensate(st *FailureRecoveryState, op FailureRecoveryOperation, output interface{}) (bool, interface{}) {
    tx, exists := st.Transactions[op.TxID]
    if !exists {
        return false, st
    }

    newSt := st.Clone()

    // 找到对应的操作
    for i := len(tx.Ops) - 1; i >= 0; i-- {
        txOp := tx.Ops[i]
        if txOp.Key == op.Key && txOp.Status == OpStatusCompleted {
            // 执行补偿
            store := newSt.NodeStores[op.NodeID]
            if store == nil {
                return false, st
            }

            if txOp.OldValue == nil {
                delete(store, op.Key)
            } else {
                store[op.Key] = VersionedValue{
                    Value:   txOp.OldValue,
                    Version: 2, // 补偿版本
                }
            }

            // 更新操作状态
            newSt.Transactions[op.TxID].Ops[i].Status = OpStatusCompensated
            break
        }
    }

    return output == "ok", newSt
}

// handleReadWithFailure 处理带故障的读取
func handleReadWithFailure(st *FailureRecoveryState, op FailureRecoveryOperation, output interface{}) (bool, interface{}) {
    // 检查节点是否故障
    if st.FailedNodes[op.NodeID] {
        return output == "node_failed", st
    }

    store := st.NodeStores[op.NodeID]
    if store == nil {
        return output == nil, st
    }

    val, exists := store[op.Key]
    if !exists {
        return output == nil, st
    }

    outVal := output.(ReadOutput)
    return bytes.Equal(outVal.Value, val.Value), st
}
```

---

## 4. 验证场景

### 4.1 拓扑感知验证

```go
// TestTopologyAware_2PCParticipants 测试 2PC 参与者
func TestTopologyAware_2PCParticipants(t *testing.T) {
    model := TopologyAwareModel()
    recorder := NewTopologyRecorder()

    // 初始化拓扑
    //      Root
    //     /    \
    //   L1-A   L1-B
    //   /  \   /  \
    // L2-1 L2-2 L2-3 L2-4

    recorder.Record("system", "init_topology", TopologyOperation{
        Type: "init_topology",
        Nodes: []*NodeInfo{
            {ID: "root", ParentID: "", Children: []string{"L1-A", "L1-B"}},
            {ID: "L1-A", ParentID: "root", Children: []string{"L2-1", "L2-2"}},
            {ID: "L1-B", ParentID: "root", Children: []string{"L2-3", "L2-4"}},
            {ID: "L2-1", ParentID: "L1-A"},
            {ID: "L2-2", ParentID: "L1-A"},
            {ID: "L2-3", ParentID: "L1-B"},
            {ID: "L2-4", ParentID: "L1-B"},
        },
    }, "ok")

    // L2-1 写入（参与者应该是 L1-A, L2-2, L2-1）
    recorder.Record("L2-1", "write_with_2pc", TopologyOperation{
        Type:    "write_with_2pc",
        NodeID:  "L2-1",
        Key:     "k1",
        Value:   []byte("v1"),
        Version: 1,
    }, "ok")

    // 验证 L1-A, L2-2 也应该有数据
    recorder.Record("L1-A", "read", TopologyOperation{
        Type:   "read",
        NodeID: "L1-A",
        Key:    "k1",
    }, ReadOutput{Value: []byte("v1"), Version: 1})

    recorder.Record("L2-2", "read", TopologyOperation{
        Type:   "read",
        NodeID: "L2-2",
        Key:    "k1",
    }, ReadOutput{Value: []byte("v1"), Version: 1})

    result, _ := porcupine.CheckOperations(model, recorder.GetHistory(), time.Minute)
    assert.Equal(t, porcupine.Ok, result)
}

// TestTopologyAware_TreeGossip 测试树感知 Gossip
func TestTopologyAware_TreeGossip(t *testing.T) {
    model := TopologyAwareModel()
    recorder := NewTopologyRecorder()

    // 初始化相同的拓扑
    // ...

    // L2-1 写入（Gossip）
    recorder.Record("L2-1", "write_with_gossip", TopologyOperation{
        Type:    "write_with_gossip",
        NodeID:  "L2-1",
        Key:     "k1",
        Value:   []byte("v1"),
        Version: 1,
    }, "ok")

    // 验证传播路径：L2-1 → L1-A → Root → L1-B → L2-3, L2-4
    // 最终所有节点都应该有数据
    for _, nodeID := range []string{"L2-1", "L1-A", "Root", "L1-B", "L2-3", "L2-4"} {
        recorder.Record(nodeID, "read", TopologyOperation{
            Type:   "read",
            NodeID: nodeID,
            Key:    "k1",
        }, ReadOutput{Value: []byte("v1"), Version: 1})
    }

    result, _ := porcupine.CheckOperations(model, recorder.GetHistory(), time.Minute)
    assert.Equal(t, porcupine.Ok, result)
}
```

### 4.2 失败恢复验证

```go
// TestFailureRecovery_QuorumWithFailure 测试带故障的 Quorum
func TestFailureRecovery_QuorumWithFailure(t *testing.T) {
    model := FailureRecoveryModel()
    recorder := NewFailureRecorder()

    // 节点故障
    recorder.Record("system", "node_fail", FailureRecoveryOperation{
        Type:   "node_fail",
        NodeID: "node-3",
    }, "ok")

    // Quorum 写入（5 节点，3 故障 1）
    // Quorum = 3，健康节点 = 4，应该成功
    recorder.Record("client", "write_with_quorum", FailureRecoveryOperation{
        Type:         "write_with_quorum",
        NodeID:       "node-1",
        Key:          "k1",
        Value:        []byte("v1"),
        Version:      1,
        Participants: []string{"node-1", "node-2", "node-3", "node-4", "node-5"},
    }, "ok")

    result, _ := porcupine.CheckOperations(model, recorder.GetHistory(), time.Minute)
    assert.Equal(t, porcupine.Ok, result)
}

// TestFailureRecovery_QuorumFailed 测试 Quorum 失败
func TestFailureRecovery_QuorumFailed(t *testing.T) {
    model := FailureRecoveryModel()
    recorder := NewFailureRecorder()

    // 3 节点故障（5 节点集群）
    for _, nodeID := range []string{"node-3", "node-4", "node-5"} {
        recorder.Record("system", "node_fail", FailureRecoveryOperation{
            Type:   "node_fail",
            NodeID: nodeID,
        }, "ok")
    }

    // Quorum 写入（Quorum = 3，健康节点 = 2，应该失败）
    recorder.Record("client", "write_with_quorum", FailureRecoveryOperation{
        Type:         "write_with_quorum",
        NodeID:       "node-1",
        Key:          "k1",
        Value:        []byte("v1"),
        Version:      1,
        Participants: []string{"node-1", "node-2", "node-3", "node-4", "node-5"},
    }, "quorum_failed")

    result, _ := porcupine.CheckOperations(model, recorder.GetHistory(), time.Minute)
    assert.Equal(t, porcupine.Ok, result)
}

// TestFailureRecovery_SagaCompensation 测试 Saga 补偿
func TestFailureRecovery_SagaCompensation(t *testing.T) {
    model := FailureRecoveryModel()
    recorder := NewFailureRecorder()

    txID := "tx-001"

    // 开始事务
    recorder.Record("client", "saga_begin", FailureRecoveryOperation{
        Type: "saga_begin",
        TxID: txID,
    }, "ok")

    // Layer1 写入成功
    recorder.Record("client", "saga_execute", FailureRecoveryOperation{
        Type:   "saga_execute",
        TxID:   txID,
        NodeID: "node-1",
        Layer:  Layer1,
        Key:    "k1",
        Value:  []byte("v1"),
    }, "ok")

    // Layer2 写入失败
    recorder.Record("client", "saga_execute", FailureRecoveryOperation{
        Type:   "saga_execute",
        TxID:   txID,
        NodeID: "node-2",
        Layer:  Layer2,
        Key:    "k1",
        Value:  []byte("v1"),
    }, "failed")

    // 补偿 Layer1
    recorder.Record("client", "saga_compensate", FailureRecoveryOperation{
        Type:   "saga_compensate",
        TxID:   txID,
        NodeID: "node-1",
        Key:    "k1",
    }, "ok")

    // 中止事务
    recorder.Record("client", "saga_abort", FailureRecoveryOperation{
        Type: "saga_abort",
        TxID: txID,
    }, "ok")

    // 验证 node-1 的 k1 应该被回滚
    recorder.Record("client", "read", FailureRecoveryOperation{
        Type:   "read",
        NodeID: "node-1",
        Key:    "k1",
    }, nil) // 应该返回 nil

    result, _ := porcupine.CheckOperations(model, recorder.GetHistory(), time.Minute)
    assert.Equal(t, porcupine.Ok, result)
}
```

### 4.3 Leader 切换验证

```go
// TestTopologyAware_LeaderChange 测试 Leader 切换
func TestTopologyAware_LeaderChange(t *testing.T) {
    model := TopologyAwareModel()
    recorder := NewTopologyRecorder()

    // 初始化拓扑（略）
    // ...

    // 初始 Leader 写入
    recorder.Record("node-1", "write_with_2pc", TopologyOperation{
        Type:    "write_with_2pc",
        NodeID:  "node-1",
        Key:     "k1",
        Value:   []byte("v1"),
        Version: 1,
        Term:    1,
    }, "ok")

    // Leader 切换
    recorder.Record("system", "leader_change", TopologyOperation{
        Type:      "leader_change",
        NewLeader: "node-2",
        Term:      2,
    }, "ok")

    // 新 Leader 写入
    recorder.Record("node-2", "write_with_2pc", TopologyOperation{
        Type:    "write_with_2pc",
        NodeID:  "node-2",
        Key:     "k2",
        Value:   []byte("v2"),
        Version: 1,
        Term:    2,
    }, "ok")

    // 旧 Leader 尝试写入（应该失败，Term 过期）
    recorder.Record("node-1", "write_with_2pc", TopologyOperation{
        Type:    "write_with_2pc",
        NodeID:  "node-1",
        Key:     "k3",
        Value:   []byte("v3"),
        Version: 1,
        Term:    1, // 旧 Term
    }, "stale_term")

    result, _ := porcupine.CheckOperations(model, recorder.GetHistory(), time.Minute)
    assert.Equal(t, porcupine.Ok, result)
}
```

---

## 5. 集成到现有测试框架

### 5.1 Recorder 实现

```go
// EnhancedRecorder 增强的操作记录器
type EnhancedRecorder struct {
    mu       sync.Mutex
    history  []porcupine.Operation
    nodeID   string
    callID   int
}

// NewEnhancedRecorder 创建记录器
func NewEnhancedRecorder(nodeID string) *EnhancedRecorder {
    return &EnhancedRecorder{
        history: make([]porcupine.Operation, 0),
        nodeID:  nodeID,
        callID:  0,
    }
}

// Record 记录操作
func (r *EnhancedRecorder) Record(clientID string, opType string, input interface{}, output interface{}) {
    r.mu.Lock()
    defer r.mu.Unlock()

    r.callID++
    r.history = append(r.history, porcupine.Operation{
        ClientID: clientID,
        OpID:     r.callID,
        Input:    input,
        Output:   output,
    })
}

// GetHistory 获取历史
func (r *EnhancedRecorder) GetHistory() []porcupine.Operation {
    r.mu.Lock()
    defer r.mu.Unlock()

    return append([]porcupine.Operation{}, r.history...)
}
```

### 5.2 验证工具

```go
// VerifyLinearizability 验证线性化
func VerifyLinearizability(model porcupine.Model, history []porcupine.Operation, timeout time.Duration) (porcupine.CheckResult, error) {
    return porcupine.CheckOperations(model, history, timeout)
}

// VerifyWithModel 使用指定模型验证
func VerifyWithModel(modelName string, history []porcupine.Operation) (bool, string) {
    var model porcupine.Model

    switch modelName {
    case "topology_aware":
        model = TopologyAwareModel()
    case "failure_recovery":
        model = FailureRecoveryModel()
    case "leader_ha":
        model = LeaderHAModel()
    case "saga":
        model = SagaTransactionModel()
    case "hlc":
        model = HLCModel()
    case "degradation":
        model = DegradationModel()
    default:
        return false, "unknown model"
    }

    result, info := porcupine.CheckOperations(model, history, time.Minute)

    switch result {
    case porcupine.Ok:
        return true, "linearizable"
    case porcupine.Illegal:
        return false, fmt.Sprintf("non-linearizable: %v", info)
    case porcupine.Unknown:
        return false, fmt.Sprintf("unknown: %v", info)
    default:
        return false, fmt.Sprintf("unexpected result: %v", result)
    }
}
```

---

## 6. 总结

### 6.1 增强模型一览

| 模型 | 验证场景 | 关键特性 |
|------|---------|---------|
| **TopologyAwareModel** | Layer1/2/3 操作 | 拓扑感知、参与者验证 |
| **FailureRecoveryModel** | 故障场景 | 节点故障、Quorum 失败、Saga 补偿 |
| **LeaderHAModel** | Leader HA | Fencing Token、Term 单调性 |
| **SagaTransactionModel** | 跨层级事务 | 原子性、补偿完整性 |
| **HLCModel** | 时间戳 | 因果一致性、LWW 语义 |
| **DegradationModel** | 分区降级 | 状态转换、恢复同步 |

### 6.2 覆盖的验证点

```
┌─────────────────────────────────────────────────────────┐
│                  Porcupine 验证覆盖                       │
├─────────────────────────────────────────────────────────┤
│  Layer 1 (2PC)                                           │
│  ✅ 拓扑感知参与者                                        │
│  ✅ 版本号校验                                            │
│  ✅ Leader Fencing Token                                 │
│  ✅ 故障节点处理                                          │
├─────────────────────────────────────────────────────────┤
│  Layer 2 (Quorum)                                        │
│  ✅ 多数派确认                                            │
│  ✅ 故障节点排除                                          │
│  ✅ 分区降级                                              │
│  ✅ 恢复同步                                              │
├─────────────────────────────────────────────────────────┤
│  Layer 3 (Gossip)                                        │
│  ✅ 树感知传播                                            │
│  ✅ HLC 时间戳                                            │
│  ✅ 收敛性                                                │
├─────────────────────────────────────────────────────────┤
│  跨层级                                                   │
│  ✅ Saga 事务原子性                                        │
│  ✅ 补偿完整性                                            │
│  ✅ 故障恢复                                              │
└─────────────────────────────────────────────────────────┘
```

---

**文档版本**: v1.0
**创建日期**: 2026-02-14
**最后更新**: 2026-02-14
**维护者**: 🤖 核心开发 A
**状态**: ✅ 已完成
