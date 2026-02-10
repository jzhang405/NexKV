# TCP 帧格式设计决策：长度字段在 FixedHeader 中

> **文档类型**: 📊 技术决策
> **创建日期**: 2026-01-25
> **状态**: ✅ 已决策
> **优先级**: P0（高）

---

## 背景说明

在实现 NexKV 的传输层时，存在两种常见的 TCP 帧格式设计方案：
1. **Length 前缀格式**：`[Length(4B)][FixedHeader][Data][CRC]`
2. **Length 内嵌格式**：`[FixedHeader(含长度)][Data][CRC]`

本文档记录我们选择方案 2 的设计决策和理由。

---

## 决策内容

### 选择方案：长度信息内嵌在 FixedHeader 中

**帧格式**：
```
+--------------+--------------+--------+----------+
| FixedHeader  | VarExtHeader | Data   | Checksum |
+--------------+--------------+--------+----------+
| 42 bytes     | 变长         | 变长   | 4 bytes  |
+--------------+--------------+--------+----------+
```

**FixedHeader 中的长度字段**：
- `ExtHeaderLen` (uint16, 2 bytes)：变长扩展头的长度
- `DataLength` (uint32, 4 bytes)：消息数据的长度
- 总共 6 字节存储长度信息（足以表达最大 4GB 的消息）

### 未选择方案：Length 前缀字段

```
+--------+--------------+--------------+--------+----------+
| Length | FixedHeader  | VarExtHeader | Data   | Checksum |
+--------+--------------+--------------+--------+----------+
| 4B     | 42 bytes     | 变长         | 变长   | 4 bytes  |
+--------+--------------+--------------+--------+----------+
```

---

## 决策理由

### 1. 功能等价性

两种方案都能正确解决 TCP 粘包/半包问题：

| 功能 | Length 前缀方案 | FixedHeader 内嵌方案 | 等价性 |
|------|----------------|---------------------|--------|
| 解决粘包 | ✅ Length 告诉你读多少 | ✅ FixedHeader 中的长度告诉你读多少 | ✅ 等价 |
| 解决半包 | ✅ io.ReadFull 阻塞直到读够 | ✅ io.ReadFull 阻塞直到读够 | ✅ 等价 |
| 帧边界清晰 | ✅ 每帧以 Length 开始 | ✅ 每帧以 FixedHeader 开始 | ✅ 等价 |

### 2. 性能对比

| 指标 | Length 前缀方案 | FixedHeader 内嵌方案 | 结论 |
|------|----------------|---------------------|------|
| 额外开销 | 4 字节/帧 | 0 字节（长度信息已包含在 FixedHeader 中） | ✅ 内嵌方案更优 |
| 内存分配 | 2 次（先读 Length，再读帧） | 2 次（先读 FixedHeader，再读剩余） | ✅ 等价 |
| 网络传输 | 多传输 4 字节 | 不多传输 | ✅ 内嵌方案更优 |

**高频场景影响**：
- 假设每秒 10000 帧的消息流量
- Length 前缀方案：额外传输 40KB/秒
- FixedHeader 内嵌方案：节省 40KB/秒

### 3. 实现简洁性

**Length 前缀方案需要**：
- 单独的 Length 字段编解码逻辑
- Length 字段验证（防止恶意攻击）
- 额外的文档说明 Length 字段的含义

**FixedHeader 内嵌方案**：
- 长度信息是 FixedHeader 的自然组成部分
- 一次解析 FixedHeader 即可获得所有长度信息
- 语义更清晰（FixedHeader 本身就描述帧的结构）

### 4. 兼容性和扩展性

| 维度 | Length 前缀方案 | FixedHeader 内嵌方案 |
|------|----------------|---------------------|
| 协议版本兼容 | ✅ 长度前缀是常见设计 | ✅ FixedHeader 包含 Magic 字段用于版本识别 |
| 扩展性 | ✅ Length 字段可预留 | ✅ FixedHeader 有保留字段，可扩展长度字段 |
| 调试友好性 | ⚠️ 需要先解析 Length 才能解析 FixedHeader | ✅ 直接读取 FixedHeader 即可获取帧信息 |

---

## 实现细节

### 核心代码：FrameReader.ReadFrame()

```go
// frame.go
func (fr *FrameReader) ReadFrame() (*Frame, error) {
    // 步骤 1：读取固定头（42 字节）- io.ReadFull 确保完整性
    fixedHeaderData := make([]byte, FixedHeaderLen)
    if _, err := io.ReadFull(fr.r, fixedHeaderData); err != nil {
        return nil, err
    }

    // 步骤 2：解析固定头，从中获取长度信息
    fixedHeader, err := DeserializeFixedHeader(fixedHeaderData)
    extHeaderLen := int(fixedHeader.ExtHeaderLen)
    dataLength := int(fixedHeader.DataLength)

    // 步骤 3：验证长度的合理性（防止 DoS 攻击）
    totalSize := FixedHeaderLen + extHeaderLen + dataLength + CRCLen
    if totalSize > MaxFrameSize {
        return nil, types.NewFrameTooLargeError(totalSize)
    }

    // 步骤 4：精确读取剩余数据 - io.ReadFull 解决半包问题
    remainingSize := extHeaderLen + dataLength + CRCLen
    remainingData := make([]byte, remainingSize)
    if _, err := io.ReadFull(fr.r, remainingData); err != nil {
        return nil, types.NewFrameIOReadError("读取帧数据失败", err)
    }

    // 组装完整帧
    fullData := make([]byte, totalSize)
    copy(fullData[0:FixedHeaderLen], fixedHeaderData)
    copy(fullData[FixedHeaderLen:], remainingData)

    frame := &Frame{}
    if err := frame.Unmarshal(fullData); err != nil {
        return nil, err
    }

    return frame, nil
}
```

### 关键点分析

1. **io.ReadFull 是解决半包的核心**：
   - 无论长度信息在哪里，`io.ReadFull` 都能阻塞等待，直到读取指定字节数
   - 第一次读取 42 字节 FixedHeader
   - 第二次读取 remainingSize 字节（从 FixedHeader 解析出的长度）
   - 自动处理 TCP 半包，无需额外逻辑

2. **帧边界清晰**：
   - 每次调用 `ReadFrame()` 都返回一个完整的帧
   - 下一次调用自动从下一帧开始
   - 粘包的多个帧会被正确拆分

3. **安全性保护**：
   - 检查 `totalSize > MaxFrameSize`，防止超大帧攻击
   - 使用 `io.ReadFull` 防止缓冲区溢出

---

## 常见问题解答

### Q1: 为什么不用 Length 前缀字段？

**A**: FixedHeader 中已经包含了长度信息（ExtHeaderLen + DataLength），无需额外的 Length 前缀。两者在功能上完全等价，但 FixedHeader 内嵌方案节省了 4 字节/帧的网络开销。

### Q2: 这样能解决粘包/半包问题吗？

**A**: 能。解决粘包/半包的核心是 `io.ReadFull`，它能够精确读取指定字节数。我们的实现：
1. 先用 `io.ReadFull` 读取 42 字节 FixedHeader
2. 从 FixedHeader 解析出剩余长度
3. 再用 `io.ReadFull` 精确读取剩余字节
- 粘包：自动拆分（每次 ReadFrame 只读取一帧）
- 半包：自动拼接（io.ReadFull 阻塞直到读够）

### Q3: FixedHeader 中的长度字段不够用怎么办？

**A**:
- `ExtHeaderLen` (uint16) 最大 65535 字节
- `DataLength` (uint32) 最大 4GB
- 总共支持最大 4GB 的消息，对于 KV 存储系统完全够用
- 如果未来需要更大，FixedHeader 有保留字段可扩展

### Q4: 为什么删除了 TCPFrameCodec？

**A**: `TCPFrameCodec` 是一个不必要的包装层：
- 它只是简单包装了 `Frame.Marshal()` / `Unmarshal()`
- 除了加 Length 前缀外，没有额外功能
- Transport 层直接使用 `FrameReader` 更简洁
- 删除后减少了 1119 行未使用代码

---

## 相关文件

- `internal/metadata/transport/frame.go` - Frame 定义和 FrameReader 实现
- `internal/metadata/transport/msg_codec.go` - MessageReader/Writer 实现
- `internal/metadata/transport/tcp_transport.go` - TCP Transport 实现

---

## 参考资料

- [TCP粘包/半包问题与解决方案](https://blog.cloudflare.com/how-to-receive-a-million-packets/)
- [io.ReadFull 文档](https://pkg.go.dev/io#ReadFull)
- [Binary.BigEndian 文档](https://pkg.go.dev/encoding/binary#BigEndian)

---

**维护者**: NexKV 开发团队
**最后更新**: 2026-01-25
