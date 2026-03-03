# ADR 001: 双存储引擎策略

**状态**: 已接受 | **日期**: 2026-02-18 | **决策者**: 架构团队

---

## 上下文（Context）

NexKV 需要同时支持两种截然不同的数据访问模式：

1. **元数据访问**：节点信息、分片映射、副本状态等
   - 访问频率极高
   - 数据量相对较小
   - 需要极低的访问延迟
   - 通常是点查询（精确键查找）

2. **业务数据访问**：应用实际存储的键值对
   - 数据量可能非常大
   - 需要范围查询
   - 需要持久化保证
   - 写入吞吐量要求高

**单一存储引擎的问题**：
- B+树：范围查询性能好，但点查询不如哈希表快
- 哈希表：点查询 O(1)，但无法支持范围查询
- 通用方案必然会在某种场景下性能不优

---

## 决策（Decision）

**采用双存储引擎策略**：

| 存储引擎 | 实现方式 | 数据类型 | 核心优势 |
|---------|---------|---------|----------|
| **Metadata KV** | `sync.Map` + MVStore | 元数据 | O(1) 点查询，极致性能 |
| **External KV** | Bf-Tree（B+树变体） | 业务数据 | 有序存储，范围扫描 |

**职责划分**：
- **Metadata KV**：存储所有控制平面数据（拓扑、分片、副本状态）
- **External KV**：存储用户数据（应用键值对）

---

## 理由（Rationale）

### 优势

1. **性能最优**
   - 元数据访问：O(1) 哈希查找，无锁并发
   - 业务数据：B+树优化，支持范围扫描

2. **职责清晰**
   - 每个引擎专注于自己的使用场景
   - 优化目标明确，不妥协

3. **可扩展性**
   - 两个引擎可以独立优化和演进
   - 技术栈灵活选择

4. **简化实现**
   - 不需要为单一引擎处理所有场景
   - 减少复杂度

### 劣势与缓解

| 劣势 | 缓解措施 |
|------|----------|
| 维护两套代码 | 清晰的接口隔离，职责单一 |
| 学习成本 | 详细的文档和示例 |
| 资源占用 | Metadata KV 内存占用小（MB级） |

---

## 后果（Consequences）

### 正面影响

- ✅ 元数据访问延迟 < 10μs（O(1) 查找）
- ✅ 业务数据支持范围查询
- ✅ 两个引擎可以独立优化
- ✅ 代码职责清晰，易于维护

### 负面影响

- ⚠️ 需要维护两套存储引擎代码
- ⚠️ 新开发者需要理解两种引擎的使用场景

### 风险与应对

| 风险 | 应对措施 |
|------|----------|
| 数据一致性问题 | 两引擎独立存储，无交叉访问 |
| 资源竞争 | Metadata KV 内存占用极小，可忽略 |
| 错误使用 | 严格的接口文档和使用示例 |

---

## 实施细节

### Metadata KV 实现

```go
// internal/infrastructure/storage/metadata/metadata_kv.go
package metadata

import "sync"

type MetadataKV struct {
    data sync.Map
}

func (m *MetadataKV) Get(key string) (interface{}, bool) {
    return m.data.Load(key)
}

func (m *MetadataKV) Set(key string, value interface{}) {
    m.data.Store(key, value)
}

func (m *MetadataKV) Delete(key string) {
    m.data.Delete(key)
}
```

### External KV 实现

```go
// internal/infrastructure/storage/bftree/tree.go
package bftree

type BfTree struct {
    root *Node
    wal  WAL
}

func (bt *BfTree) Get(key []byte) ([]byte, error) {
    // B+树查找
}

func (bt *BfTree) Set(key, value []byte) error {
    // B+树插入 + WAL
}

func (bt *BfTree) Scan(start, end []byte) (Iterator, error) {
    // 范围扫描
}
```

### 接口层统一

```go
// internal/domain/service/storage.go
package service

type KVStore interface {
    Get(ctx context.Context, key []byte) ([]byte, error)
    Set(ctx context.Context, key, value []byte) error
    Delete(ctx context.Context, key []byte) error
}

// MetadataKV 实现 KVStore
// BfTree 实现 KVStore
```

---

## 使用指南

### 何时使用 Metadata KV

- ✅ 节点注册信息
- ✅ 分片映射关系
- ✅ 副本状态
- ✅ 集群拓扑
- ✅ 选举状态

### 何时使用 External KV

- ✅ 应用数据存储
- ✅ 需要范围查询的场景
- ✅ 大量数据持久化
- ✅ 事务性操作

---

## 替代方案

### 方案 A：单一 B+树引擎

- ❌ 元数据访问性能不如哈希表
- ❌ 无法极致优化两种场景

### 方案 B：单一哈希表引擎

- ❌ 无法支持范围查询
- ❌ 业务数据场景受限

### 方案 C：可插拔存储引擎

- ⚠️ 增加抽象复杂度
- ✅ 未来可考虑，但当前双引擎已足够

---

## 参考资料

- [B-Tree vs Hash Table](https://stackoverflow.com/questions/1520877/b-tree-vs-hash-table)
- [Why Redis uses different data structures](https://redis.io/topics/data-types)
- `docs/07_spike/2026-02-21_spike_m2-storage-engine-implement.md`

---

**相关 ADR**:
- [ADR 002: 异步流水线架构](./002-async-pipeline.md)
- [ADR 003: 5层 DDD 架构](./003-5layer-ddd.md)
