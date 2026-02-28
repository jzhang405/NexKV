#!/bin/bash
# 缓存命中率快速分析脚本
# 使用方法: ./scripts/cache_analysis.sh

set -e

# ========== 设置 Go 环境 ==========
export GOROOT=/home/jzh/go
export GOPATH=/home/jzh/ws/go
export PATH=$GOROOT/bin:$PATH
# ===================================

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$PROJECT_ROOT"

echo "========================================"
echo "缓存命中率分析"
echo "========================================"
echo ""

# ========== 测试 1: 缓存友好模式 ==========
echo "测试 1: 缓存友好模式（独立内存区域）"
echo "--- 绑核版本 ---"
perf stat -e cache-references,cache-misses,cycles,instructions,L1-dcache-load-misses \
	go test -bench=BenchmarkPerCore_CacheHitRate -benchtime=3s \
	./internal/infrastructure/concurrency/ -run=^$ 2>&1 | \
	grep -E "cache|cycles|instructions" || true

echo ""
echo "--- 无绑核版本 ---"
# 临时禁用绑核（环境变量方式）
AUTOMAXPROCS_DISABLE=true go test -bench=BenchmarkPerCore_CacheHitRate/WithoutAffinity \
	-benchtime=3s ./internal/infrastructure/concurrency/ -run=^$ 2>&1 | \
	grep -E "cache|cycles|instructions" || true

echo ""
echo "========================================"
echo ""

# ========== 测试 2: 缓存不友好模式 ==========
echo "测试 2: 缓存不友好模式（共享内存区域）"
echo "--- 绑核版本 ---"
# 注意：这里需要实际的共享内存测试
echo "（需要实现共享内存测试）"

echo ""
echo "========================================"
echo "分析完成！"
echo ""
echo "关键指标说明:"
echo "  cache-misses/cache-references  -> 缓存命中率"
echo "  L1-dcache-load-misses          -> L1 数据缓存未命中"
echo "  cycles/instructions            -> CPI（每指令周期数）"
echo ""
echo "理想结果:"
echo "  绑核版本应该有更低的缓存未命中率"
echo "  CPI 应该更接近 1.0（更高效）"
