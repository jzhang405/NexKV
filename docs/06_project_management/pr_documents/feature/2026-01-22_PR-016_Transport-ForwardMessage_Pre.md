# 【PR Pre 文档】Feature - Transport ForwardMessage 实现

> **文档说明**：本文档为 PR-016 的前置规划文档，包含需求分析、技术设计和风险评估。本文档需经过架构师评审通过后才能启动开发。

---

## 1. 基础信息

| 项目 | 内容 |
|------|------|
| **工作类型** | 新功能开发（Feature） |
| **PR编号** | PR-016 |
| **分支名称** | feature/transport-forward-message |
| **工作主题** | 实现 Transport 层消息转发方法，自动递减 Hop Count |
| **负责人** | AI Agent + 架构师评审 |
| **分支创建日期** | 2026-01-22 |
| **预估工期** | 0.5天 |
| **关联PR** | PR-014（Hop Count 扩展）、PR-015（MsgExt & SendOpt） |
| **架构师评审状态** | ☐ 待评审 |
| **预审批结果** | ☐ 未通过 ☐ 已通过（架构师签字/备注：___） |

---

## 2. 背景与目标

### 2.1 背景

**业务场景**：
- PR-014 已实现 TLV Hop Count 扩展字段
- PR-015 已实现 MsgExt 增强消息结构和 SendOpt 函数选项模式
- 当前缺少消息转发功能，节点无法将接收到的消息转发给其他节点

**现有问题**：
1. **缺少转发接口**：Transport 层没有提供 `ForwardMessage()` 方法
2. **手动处理复杂**：业务层需要手动递减 Hop Count、重新编码 TLV
3. **容易出错**：手动处理 TLV 字段容易遗漏或出错

**价值**：
- 提供标准化的消息转发接口
- 自动处理 Hop Count 递减
- 简化业务层代码，降低出错概率
- 为 Gossip 协议、消息广播等场景提供基础支持

### 2.2 核心目标

**功能目标**：
1. 实现 `Transport.ForwardMessage()` 方法
2. 自动递减 Hop Count（如果存在）
3. 当 Hop Count 减至 0 时，丢弃消息（不再转发）
4. 支持所有 TLV 扩展字段的透明转发

**性能目标**：
- ForwardMessage() 开销 < 500ns（不含网络 I/O）
- 不引入额外的内存分配（零拷贝）

**质量目标**：
- 单元测试覆盖率 > 80%
- 通过 Code Review（无 P0/P1 问题）

---

## 3. 需求分析

### 3.1 功能需求

#### FR-1: ForwardMessage 接口定义

**接口签名**：
```go
// ForwardMessage 转发消息到指定节点
// addr: 目标节点地址
// msgExt: 接收到的增强消息（包含 TLV 扩展）
// 返回: 转发的消息序列号，或错误
ForwardMessage(ctx context.Context, addr string, msgExt MsgExt) (uint32, error)
```

#### FR-2: Hop Count 自动递减

**行为**：
1. 检查 `msgExt.HopCount` 是否存在
2. 如果存在且 `Hop > 0`，则递减：`NewHop = Hop - 1`
3. 如果 `Hop == 0`，返回错误（消息已过期，不再转发）
4. 如果不存在，直接转发（不添加 Hop Count）

#### FR-3: TLV 字段透明转发

**行为**：
1. 保留所有 TLV 扩展字段（除 Hop Count 递减外）
2. 不修改 Message.Type、Message.Payload
3. 支持所有 TLV 类型（HopExt、PriorityExt、TimestampExt 等）

#### FR-4: 错误处理

**错误场景**：
| 场景 | 返回错误 |
|------|---------|
| Hop Count == 0 | ErrHopCountExpired |
| 目标地址无效 | ErrInvalidAddress |
| 网络连接失败 | ErrConnectionFailed |
| 编码失败 | ErrEncodingFailed |

### 3.2 非功能需求

#### NFR-1: 性能要求
- ForwardMessage() CPU 开销 < 500ns
- 不引入额外的内存分配（使用 sync.Pool 或缓冲区重用）

#### NFR-2: 兼容性要求
- 与现有 `Send()` 方法兼容
- 与 TCP/UDP Transport 兼容
- 向后兼容（无 Hop Count 的消息也能转发）

---

## 4. 技术设计

### 4.1 接口设计

#### Transport 接口扩展

```go
// Transport 接口新增方法
type Transport interface {
    // ... 现有方法 ...

    // ForwardMessage 转发消息到指定节点
    // 自动递减 Hop Count（如果存在）
    // 当 Hop Count 减至 0 时，返回 ErrHopCountExpired
    ForwardMessage(ctx context.Context, addr string, msgExt MsgExt) (uint32, error)
}
```

#### TCP Transport 实现

```go
// ForwardMessage TCP 转发实现
func (t *TCPTransport) ForwardMessage(ctx context.Context, addr string, msgExt MsgExt) (uint32, error) {
    // 1. 检查 Hop Count
    if msgExt.HopCount != nil {
        if msgExt.HopCount.Hop == 0 {
            return 0, types.NewOpErr(types.ErrCodeInvalidParam, "ForwardMessage",
                "消息已过期（HopCount=0），不再转发", nil)
        }
        // 递减 Hop Count
        msgExt.HopCount.Hop--
    }

    // 2. 获取或创建连接
    conn, err := t.getOrCreateConn(addr)
    if err != nil {
        return 0, err
    }

    // 3. 重新编码 TLV（使用递减后的 Hop Count）
    tlvFields, err := msgExt.EncodeTLVs()
    if err != nil {
        return 0, types.NewOpErr(types.ErrCodeEncoding, "ForwardMessage",
            "编码 TLV 失败", err)
    }

    // 4. 发送消息
    return conn.writer.WriteMessageWithOptions(
        msgExt.Message,
        t.NodeID.Load(),
        t.nextMsgSeq(),
        WithTLVFields(tlvFields),
    )
}
```

#### UDP Transport 实现

```go
// ForwardMessage UDP 转发实现
func (t *UDPTransport) ForwardMessage(ctx context.Context, addr string, msgExt MsgExt) (uint32, error) {
    // 1. 检查 Hop Count（同 TCP）
    if msgExt.HopCount != nil {
        if msgExt.HopCount.Hop == 0 {
            return 0, types.NewOpErr(types.ErrCodeInvalidParam, "ForwardMessage",
                "消息已过期（HopCount=0），不再转发", nil)
        }
        msgExt.HopCount.Hop--
    }

    // 2. 解析目标地址
    udpAddr, err := net.ResolveUDPAddr("udp", addr)
    if err != nil {
        return 0, types.NewOpErr(types.ErrCodeInvalidParam, "ForwardMessage",
            "无效的 UDP 地址", err)
    }

    // 3. 重新编码 TLV
    tlvFields, err := msgExt.EncodeTLVs()
    if err != nil {
        return 0, types.NewOpErr(types.ErrCodeEncoding, "ForwardMessage",
            "编码 TLV 失败", err)
    }

    // 4. 发送消息
    return t.writer.WriteMessageWithOptions(
        msgExt.Message,
        t.NodeID.Load(),
        t.nextMsgSeq(),
        WithTLVFields(tlvFields),
    )
}
```

### 4.2 MsgExt 扩展方法

```go
// EncodeTLVs 编码所有 TLV 扩展字段（使用当前字段值）
func (m *MsgExt) EncodeTLVs() ([]frame.TLVField, error) {
    var fields []frame.TLVField

    // Hop Count
    if m.HopCount != nil {
        hopField, err := EncodeHopExt(m.HopCount.Hop, m.HopCount.TotalHop)
        if err != nil {
            return nil, err
        }
        fields = append(fields, hopField)
    }

    // Priority
    if m.PriorityExt != nil {
        priorityField, err := EncodePriorityExt(m.PriorityExt.Level)
        if err != nil {
            return nil, err
        }
        fields = append(fields, priorityField)
    }

    // Timestamp
    if m.Timestamp != nil {
        tsField, err := EncodeTimestampExt(m.Timestamp.Timestamp)
        if err != nil {
            return nil, err
        }
        fields = append(fields, tsField)
    }

    // ... 其他 TLV 类型 ...

    return fields, nil
}
```

### 4.3 错误码定义

```go
// transport.go 新增错误码
const (
    ErrCodeHopCountExpired = ErrCodeTransportBase + 20
)

var (
    ErrHopCountExpired = &OpError{
        Code:    ErrCodeHopCountExpired,
        Method:  "ForwardMessage",
        Message: "消息已过期（HopCount=0），不再转发",
    }
)
```

---

## 5. 实现计划

### 5.1 实施步骤

| 步骤 | 任务 | 预估时间 | 产出 |
|------|------|----------|------|
| 1 | 定义 Transport.ForwardMessage() 接口 | 10分钟 | 接口定义 |
| 2 | 实现 TCP Transport.ForwardMessage() | 30分钟 | TCP 实现 |
| 3 | 实现 UDP Transport.ForwardMessage() | 30分钟 | UDP 实现 |
| 4 | 实现 MsgExt.EncodeTLVs() 方法 | 20分钟 | TLV 编码 |
| 5 | 编写单元测试 | 40分钟 | 测试用例 |
| 6 | 验证（lint/build/test/clean） | 20分钟 | 验证通过 |

**总预估时间**: 2.5小时（约 0.5 天）

### 5.2 文件变更清单

| 文件 | 变更类型 | 说明 |
|------|---------|------|
| `internal/metadata/transport/transport.go` | 修改 | 新增 ForwardMessage() 接口定义 |
| `internal/metadata/transport/tcp_transport.go` | 修改 | 实现 TCP ForwardMessage() |
| `internal/metadata/transport/udp_transport.go` | 修改 | 实现 UDP ForwardMessage() |
| `internal/metadata/transport/message_ext.go` | 修改 | 新增 EncodeTLVs() 方法 |
| `internal/metadata/transport/message_ext_test.go` | 新增 | ForwardMessage() 测试用例 |

---

## 6. 风险评估

### 6.1 技术风险

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **TLV 编码性能** | 中 | 低 | 使用 sync.Pool 复用缓冲区 |
| **Hop Count 递减竞态** | 中 | 低 | MsgExt 使用值拷贝，无并发风险 |
| **连接管理复杂度** | 低 | 低 | 复用现有连接池逻辑 |

### 6.2 兼容性风险

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **接口变更影响现有代码** | 低 | 低 | 新增接口，不修改现有接口 |
| **TLV 字段遗漏** | 中 | 低 | 完整的单元测试覆盖 |

### 6.3 测试风险

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **边界条件测试不充分** | 中 | 中 | 包含 HopCount=0、nil 等边界用例 |
| **并发安全问题** | 高 | 低 | MsgExt 使用值拷贝，天然并发安全 |

---

## 7. 验收标准

### 7.1 功能验收

- [ ] ForwardMessage() 接口定义完成
- [ ] TCP Transport 实现完成
- [ ] UDP Transport 实现完成
- [ ] Hop Count 自动递减功能正常
- [ ] Hop Count = 0 时返回正确错误
- [ ] 所有 TLV 字段正确转发

### 7.2 性能验收

- [ ] ForwardMessage() CPU 开销 < 500ns
- [ ] 无额外内存分配（使用 pprof 验证）

### 7.3 质量验收

- [ ] 单元测试覆盖率 > 80%
- [ ] `make lint` 通过（0 issues）
- [ ] `make build` 通过
- [ ] `make test` 通过（所有测试用例）
- [ ] Code Review 通过（无 P0/P1 问题）

---

## 8. 参考资料

### 8.1 关联文档

- **PR-014**: Transport Hop Count TTL 扩展
  - `docs/06_project_management/pr_documents/feature/2026-01-21_PR-014_Transport-Hop-Count-TTL_全流程.md`
- **PR-015**: Transport MsgExt & SendOpt
  - `docs/06_project_management/pr_documents/feature/2026-01-21_PR-015_Transport-MsgExt-SendOpt_全流程.md`

### 8.2 代码参考

- **TLV 编解码**: `internal/metadata/transport/frame.go`
- **MsgExt 结构**: `internal/metadata/transport/message_ext.go`
- **Transport 接口**: `internal/metadata/transport/transport.go`

---

**文档版本**: v1.0
**创建日期**: 2026-01-22
**维护者**: NexKV 开发团队
**状态**: 🔄 待评审
