# 三种编解码（JSON/MessagePack/Protobuf）统一实现方案

> **📋 文档类型**: Proposals（技术方案建议）
> **🏷️ 主题**: MessagePack/Protobuf 编解码统一架构
> **📅 创建日期**: 2026-01-19
> **✅ 状态**: 待讨论

---

## 📌 问题背景

当前项目中，我们需要**同时支持 JSON、MessagePack、Protobuf 三种编解码**，实现「一份结构体，适配三种数据格式」，满足分布式 KV 存储的多样化场景：
- **对外 API**: JSON（可读性强）
- **内部同步**: MessagePack（灵活、开发效率高）
- **高性能传输**: Protobuf（极致性能、体积紧凑）

---

## 🎯 核心方案

### 关键前提

三种编解码的支持方式存在**细微差异**：

| 编解码 | 实现方式 | 是否需要编译 |
|--------|---------|-------------|
| **JSON** | 结构体双标签（`json`） | ❌ 否（反射） |
| **MessagePack** | 结构体双标签（`msgpack`） | ❌ 否（反射） |
| **Protobuf** | 结构体标签 + `proto.Message` 接口 | ✅ 是（protoc 编译） |

### 最终实现

**同一个结构体添加三种标签 + Protobuf 预编译**：

```go
type ClusterMetadata struct {
    Shards  map[string]ShardMetadata  `json:"shards" msgpack:"shards" protobuf:"bytes,1,opt,name=shards"`
    Nodes   map[string]NodeMetadata   `json:"nodes" msgpack:"nodes" protobuf:"bytes,2,opt,name=nodes"`
    Tables  map[string]TableMetadata  `json:"tables" msgpack:"tables" protobuf:"bytes,3,opt,name=tables"`
    Version uint64                    `json:"version" msgpack:"version" protobuf:"varint,4,opt,name=version"`
}
```

---

## 🔧 实现步骤

### 步骤 1: 安装 Protobuf 工具链

```bash
# 1. 安装 protoc 编译工具
# 下载地址：https://github.com/protocolbuffers/protobuf/releases

# 2. 安装 Go 语言 Protobuf 插件
go get google.golang.org/protobuf/cmd/protoc-gen-go
```

### 步骤 2: 定义 Protobuf 描述文件

创建 `proto/metadata.proto`：

```protobuf
syntax = "proto3";
option go_package = "./metadata;metadata";

message ShardMetadataProto {
  string id = 1;
  repeated string range = 2;
  repeated string replicas = 3;
}

message NodeMetadataProto {
  string id = 1;
  string addr = 2;
  string status = 3;
  string role = 4;
}

message ClusterMetadataProto {
  map<string, ShardMetadataProto> shards = 1;
  map<string, NodeMetadataProto> nodes = 2;
  uint64 version = 3;
}
```

### 步骤 3: 编译 Protobuf 生成 Go 代码

```bash
protoc --go_out=. ./proto/metadata.proto
```

生成结果：`proto/metadata_go.pb.go`

### 步骤 4: 实现结构体转换工具

```go
// ClusterMetadataToProto 将自定义结构体转换为 Protobuf 结构体
func (c *ClusterMetadata) ClusterMetadataToProto() *proto.ClusterMetadataProto {
    // 处理 map 转换
    protoShards := make(map[string]*proto.ShardMetadataProto, len(c.Shards))
    for shardID, shard := range c.Shards {
        protoShards[shardID] = &proto.ShardMetadataProto{
            Id:       shard.ID,
            Range:    []string{shard.Range[0], shard.Range[1]}, // 固定数组 → 切片
            Replicas: shard.Replicas,
        }
    }

    return &proto.ClusterMetadataProto{
        Shards:  protoShards,
        Version: c.Version,
    }
}

// ClusterMetadataFromProto 将 Protobuf 结构体转换为自定义结构体
func ClusterMetadataFromProto(protoMeta *proto.ClusterMetadataProto) *ClusterMetadata {
    shards := make(map[string]ShardMetadata, len(protoMeta.Shards))
    for shardID, protoShard := range protoMeta.Shards {
        var rangeArr [2]string
        if len(protoShard.Range) >= 2 {
            rangeArr[0] = protoShard.Range[0]
            rangeArr[1] = protoShard.Range[1]
        }
        shards[shardID] = ShardMetadata{
            ID:    protoShard.Id,
            Range: rangeArr, // 切片 → 固定数组
        }
    }

    return &ClusterMetadata{
        Shards:  shards,
        Version: protoMeta.Version,
    }
}
```

### 步骤 5: 封装三种编解码方法

```go
// ---------------------- JSON ----------------------
func (c *ClusterMetadata) SerializeToJSON() ([]byte, error) {
    return json.Marshal(c)
}

func (c *ClusterMetadata) DeserializeFromJSON(data []byte) error {
    return json.Unmarshal(data, c)
}

// ---------------------- MessagePack ----------------------
func (c *ClusterMetadata) SerializeToMsgPack() ([]byte, error) {
    return msgpack.Marshal(c)
}

func (c *ClusterMetadata) DeserializeFromMsgPack(data []byte) error {
    return msgpack.Unmarshal(data, c)
}

// ---------------------- Protobuf ----------------------
func (c *ClusterMetadata) SerializeToProtobuf() ([]byte, error) {
    protoMeta := c.ClusterMetadataToProto()
    return proto.Marshal(protoMeta)
}

func (c *ClusterMetadata) DeserializeFromProtobuf(data []byte) error {
    protoMeta := &proto.ClusterMetadataProto{}
    if err := proto.Unmarshal(data, protoMeta); err != nil {
        return err
    }
    *c = *ClusterMetadataFromProto(protoMeta)
    return nil
}
```

---

## 📊 性能对比

根据之前的 benchmark 测试结果（`codec_benchmark_test.go`）：

| 操作 | MessagePack | JSON | Protobuf（预期） |
|------|------------|------|----------------|
| 编码速度 | 700 ns/op | 1754 ns/op | ~500 ns/op |
| 解码速度 | 656 ns/op | 9652 ns/op | ~400 ns/op |
| 编码后大小 | 1609 bytes | 2153 bytes | ~1200 bytes |

**预期**：Protobuf 应该是三者中性能最优、体积最小的。

---

## 🎯 使用场景建议

| 场景 | 推荐编解码 | 理由 |
|------|-----------|------|
| **对外 API/调试** | JSON | 可读性强，便于人工检查 |
| **集群内部普通同步** | MessagePack | 灵活、开发效率高 |
| **高频同步/大体积数据** | Protobuf | 极致性能、体积紧凑 |

---

## ⚠️ 注意事项

### 1. 性能优化
Protobuf 的性能瓶颈在于「自定义结构体 ↔ Protobuf 结构体」的转换，可通过：
- 内存池复用结构体
- 减少动态内存分配
- 使用 sync.Pool

提升高频同步场景的性能。

### 2. 兼容性保障
- **Protobuf**: 支持向后兼容（新增字段不影响老版本）
- **JSON/MsgPack**: 需手动处理字段新增/删除
- 建议：使用 `omitempty` 标签减少兼容性问题

### 3. 类型差异
Protobuf 不支持 Go 中的固定数组 `[n]T`，需在序列化时转换为切片 `[]string`。

---

## 📝 TODO

### 短期（1-2 周）
1. 安装 Protobuf 工具链
2. 定义 `proto/metadata.proto` 文件
3. 生成 Protobuf Go 代码
4. 实现结构体转换工具

### 中期（2-4 周）
1. 实现 ProtobufCodec（在 `transport/codec.go` 中）
2. 添加 Protobuf 性能对比测试
3. 补充 Protobuf 单元测试

### 长期（1-2 月）
1. 性能优化（内存池、零拷贝）
2. 集成到 Gossip 协议
3. 文档完善

---

## 🔗 相关文档

- `internal/metadata/store/codec_benchmark_test.go` - Codec 性能对比测试
- `internal/metadata/transport/codec.go` - Transport Codec 实现
- `docs/06_project_management/pr_documents/feature/2026-01-19_PR-001_WAL优化与增强_全流程.md` - PR-001 文档

---

**文档版本**: v1.0
**最后更新**: 2026-01-19
