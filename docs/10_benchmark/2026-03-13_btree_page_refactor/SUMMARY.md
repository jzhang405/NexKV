# 性能分析快速概览

## 📊 核心指标

| 指标 | 数值 | 说明 |
|------|------|------|
| **Get QPS** | 2593 万/秒 | 并发读取，纯内存 |
| **Set QPS** | 1,696/秒 | 受磁盘 fsync 限制 |
| **Get 延迟** | 38.56 ns | 业界顶尖水平 |
| **Set 延迟** | 478.6 μs | 包含磁盘 I/O |

## 🔥 CPU 热点

### Set 操作 (写入)
- 🔴 **39.87%** - fsync (磁盘同步，主要瓶颈)
- 🟡 **3.89%** - futex (锁等待)
- 🟢 **2.95%** - binary.PutUint32 (序列化)

### Get 操作 (读取)
- 🟢 **15.77%** - cmpbody (字节比较，正常开销)
- 🟢 **11.95%** - InternalPage.search (B-Tree 搜索)
- 🟡 **12.54%** - mallocgc (内存分配，可优化)

## 💾 内存热点

- 🔴 **85.58%** - PageSerializer.Finalize (4KB 页面缓冲区)
- 🟡 **9.07%** - bytes.growSlice (切片增长)
- 🟢 **< 4%** - Clone 操作 (CCOW 设计良好)

## ⚡ 关键优化成果

1. **ChunkManager 无锁**: 70.7 ns (53% ↑)
2. **并发读取**: 38.56 ns (23% ↑)
3. **内存效率**: Get 1 alloc (67% ↓)

## ⚠️ 主要瓶颈

1. **磁盘 I/O (P0)**: 40% CPU 用于 fsync
2. **内存分配 (P1)**: 85% 用于序列化缓冲区
3. **GC 压力 (P2)**: ~10% CPU 时间

## 🚀 下一步优化

- **立即**: PageSerializer 缓冲区池化 (-85% 分配)
- **短期**: WAL 实现 (10x 写入提升)
- **长期**: LSM-Tree 结构 (100x 写入提升)

## 📁 文件说明

- `README.md` - 完整分析报告
- `cpu_set.prof` - Set 操作 CPU profile
- `cpu_get.prof` - Get 操作 CPU profile
- `cpu_*_callgraph.svg` - 调用图可视化
- `mem_set.prof` - Set 操作内存 profile
- `*_top.txt` - 热点函数文本报告
