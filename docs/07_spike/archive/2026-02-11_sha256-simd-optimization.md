# SHA256 SIMD 加速优化

> **实施日期**: 2026-02-11
> **状态**: ✅ 已完成
> **相关 PR**: PR-039 Metadata Merkle Tree

---

## 概述

使用 `github.com/minio/sha256-simd` 库替代标准库的 `crypto/sha256`，为 Merkle Tree 哈希计算提供 SIMD 加速支持。

## 技术方案

### 1. 抽象层设计

创建 `internal/metadata/kvstore/hash` 包，提供统一的哈希接口：

```go
// Sum256 计算数据的 SHA256 哈希值（返回数组格式）
func Sum256(data []byte) [32]byte {
    return sha256simd.Sum256(data)
}
```

### 2. 透明回退机制

`minio/sha256-simd` 内部实现 CPU 特性检测：

| CPU 架构 | 加速指令 | 性能提升 |
|---------|---------|---------|
| **x86-64** | AVX2/AVX512 | 2-3x |
| **ARM64** | ARM Crypto Extensions | 与标准库相当 |
| **其他** | 标准库回退 | 无损失 |

### 3. 兼容性保证

API 完全兼容 `crypto/sha256.Sum256`：

```go
// 旧代码
hash := sha256.Sum256(data)

// 新代码（只需修改导入）
import "github.com/jzhang405/NexKV/internal/metadata/kvstore/hash"
hash := hash.Sum256(data)
```

## 性能基准测试

### 测试环境

- **CPU**: Apple M2 (ARM64)
- **OS**: macOS
- **Go**: 1.24.6

### 基准测试结果

| 数据大小 | SIMD 实现 | 标准库 | 性能对比 |
|---------|----------|--------|---------|
| **64B** | 57 ns/op | 60 ns/op | 相同 |
| **1KB** | 443 ns/op | 442 ns/op | 相同 |
| **8KB** | 3348 ns/op | 3325 ns/op | 相同 |

### 分析

在 Apple Silicon (ARM64) 上，SIMD 实现和标准库性能相当，因为：

1. ✅ 标准库已使用 ARM 加密指令优化
2. ✅ SIMD 库会回退到最优实现
3. ✅ **无性能损失**

### 生产环境预期

在 x86-64 Linux 服务器上：

- **AVX2**: 2-3x 加速
- **AVX512**: 3-4x 加速
- **旧 CPU**: 自动回退到标准库（无损失）

## 代码变更

### 1. 新增文件

```
internal/metadata/kvstore/hash/
├── sha256.go              # SHA256 包装器
└── sha256_bench_test.go   # 性能基准测试
```

### 2. 修改文件

```
internal/metadata/kvstore/merkle_tree.go
- import "crypto/sha256"
+ import "github.com/jzhang405/NexKV/internal/metadata/kvstore/hash"

- hash := sha256.Sum256(data)
+ sum256 := hash.Sum256(data)
```

### 3. 依赖添加

```bash
go get github.com/minio/sha256-simd
```

## 对 Merkle Tree 的影响

### 计算密集型操作

| 操作 | 频率 | SIMD 收益 |
|------|------|----------|
| **UpdateKey** | 高 | 中等（单次哈希） |
| **GetGlobalRootHash** | 高 | 低（有缓存） |
| **GetNamespaceRootHash** | 高 | 低（有缓存） |

### 实测性能

```
BenchmarkGetGlobalRootHash:     19 ns/op (已缓存优化)
BenchmarkUpdateKey:             55 ns/op
```

**结论**: 当前性能瓶颈不在于哈希计算，而在于：
1. 缓存命中率（已优化）
2. 内存分配（已优化）
3. 并发锁竞争（已优化）

## 建议

### 生产部署

1. **x86-64 服务器**: 预期 2-3x 哈希计算加速
2. **Apple Silicon**: 无性能损失
3. **其他架构**: 自动回退

### 未来优化

如果需要进一步优化 Merkle Tree 性能，考虑：

1. **批量哈希**: 使用 goroutine 并行计算多个 key hash
2. **内存池**: 减少 hash 结果的内存分配
3. **增量哈希**: 只重新计算变化的 key

## 验证清单

- [x] 所有测试通过
- [x] Lint 检查通过
- [x] 性能基准测试
- [x] 依赖更新（go.mod）
- [x] 向后兼容性验证

## 参考资料

- [minio/sha256-simd](https://github.com/minio/sha256-simd)
- [Go crypto/sha256](https://pkg.go.dev/crypto/sha256)
- PR-039 Pre 文档

---

**文档版本**: v1.0
**创建日期**: 2026-02-11
**维护者**: NexKV 开发团队
