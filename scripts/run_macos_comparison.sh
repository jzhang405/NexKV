#!/bin/bash
# macOS 对比测试脚本：PerCoreExecutor vs Ants
# 在 macOS 上，PerCoreExecutor 使用 LockOSThread 优化

set -e

echo "========================================="
echo "macOS 对比测试：PerCoreExecutor vs Ants"
echo "========================================="
echo ""

# 检查操作系统
if [[ "$OSTYPE" != "darwin"* ]]; then
    echo "❌ 错误：此脚本仅适用于 macOS"
    echo "当前系统: $OSTYPE"
    exit 1
fi

echo "✅ 检测到 macOS 系统"
echo ""

# 获取 CPU 信息
CPU_CORES=$(sysctl -n hw.ncpu)
CPU_FREQ=$(sysctl -n hw.cpufrequency)
echo "📊 硬件信息："
echo "  - CPU 核心数: $CPU_CORES"
echo "  - CPU 频率: $(( CPU_FREQ / 1000000000 )) GHz"
echo ""

# 设置 Go 环境
export GOOS=darwin
export GOARCH=amd64

echo "🔧 运行环境："
echo "  - Go 版本: $(go version)"
echo "  - GOOS: $GOOS"
echo "  - GOARCH: $GOARCH"
echo ""

# 创建结果目录
RESULT_DIR="benchmark_results/macos_$(date +%Y%m%d_%H%M%S)"
mkdir -p "$RESULT_DIR"

echo "📁 结果将保存到: $RESULT_DIR"
echo ""

# ==========================================
# Test 1: 基础对比测试
# ==========================================
echo "========================================="
echo "Test 1: 基础对比测试（PerCore vs Ants）"
echo "========================================="
echo ""

echo "📊 测试场景："
echo "  1. PerCoreExecutor (LockOSThread)"
echo "  2. Ants Default Pool"
echo "  3. Ants Custom Pool"
echo "  4. Ants Func Pool"
echo "  5. Ants Multi Pool"
echo ""

# 运行基准测试（5次取平均）
echo "运行基准测试（5次迭代）..."
for i in {1..5}; do
    echo ""
    echo "第 $i 次迭代..."
    go test -v -bench=. -benchmem -run=^$ \
        -benchtime=3s \
        ./internal/infrastructure/concurrency/... \
        -cpuprofile="$RESULT_DIR/cpu_$i.prof" \
        -memprofile="$RESULT_DIR/mem_$i.prof" \
        2>&1 | tee "$RESULT_DIR/benchmark_$i.log"
done

echo ""
echo "✅ 基础对比测试完成"
echo ""

# ==========================================
# Test 2: 并行测试
# ==========================================
echo "========================================="
echo "Test 2: 并行性能测试"
echo "========================================="
echo ""

echo "📊 测试场景："
echo "  - 多 goroutine 并发提交任务"
echo "  - 测试并发吞吐量"
echo ""

go test -v -bench=Parallel -benchmem -run=^$ \
    -benchtime=5s \
    ./internal/infrastructure/concurrency/... \
    2>&1 | tee "$RESULT_DIR/parallel_test.log"

echo ""
echo "✅ 并行测试完成"
echo ""

# ==========================================
# Test 3: 不同任务时长测试
# ==========================================
echo "========================================="
echo "Test 3: 不同任务时长测试"
echo "========================================="
echo ""

echo "📊 测试场景："
echo "  - Short Task: 10μs"
echo "  - Medium Task: 100μs"
echo "  - Long Task: 1ms"
echo ""

go test -v -bench="PerCore_(Short|Medium|Long)" -benchmem -run=^$ \
    -benchtime=3s \
    ./internal/infrastructure/concurrency/... \
    2>&1 | tee "$RESULT_DIR/task_duration_test.log"

echo ""
echo "✅ 任务时长测试完成"
echo ""

# ==========================================
# 生成性能报告
# ==========================================
echo "========================================="
echo "生成性能分析报告"
echo "========================================="
echo ""

# 汇总结果
echo "📊 测试结果汇总："
echo ""

# 提取关键指标
echo "1. PerCoreExecutor (LockOSThread) 性能："
grep "^Benchmark_PerCore_Affinity" "$RESULT_DIR"/benchmark_*.log | \
    awk '{sum+=$3; count++} END {if(count>0) print "  平均: " sum/count " ns/op"}'

echo ""
echo "2. Ants Default Pool 性能："
grep "^Benchmark_Ants_Default" "$RESULT_DIR"/benchmark_*.log | \
    awk '{sum+=$3; count++} END {if(count>0) print "  平均: " sum/count " ns/op"}'

echo ""
echo "3. Ants Func Pool 性能："
grep "^Benchmark_Ants_FuncPool" "$RESULT_DIR"/benchmark_*.log | \
    awk '{sum+=$3; count++} END {if(count>0) print "  平均: " sum/count " ns/op"}'

echo ""
echo "4. 并发吞吐量对比："
grep "^Benchmark_Parallel" "$RESULT_DIR"/parallel_test.log | \
    awk '{print "  " $1 ": " $3 " ns/op (" $5 " allocs/op)"}'

echo ""
echo "========================================="
echo "✅ macOS 对比测试完成"
echo "========================================="
echo ""

echo "📁 结果文件："
echo "  - 测试日志: $RESULT_DIR/*.log"
echo "  - CPU profile: $RESULT_DIR/cpu_*.prof"
echo "  - 内存 profile: $RESULT_DIR/mem_*.prof"
echo ""

echo "📊 查看详细结果："
echo "  cat $RESULT_DIR/benchmark_1.log"
echo ""

echo "🔍 性能分析（可选）："
echo "  go tool pprof $RESULT_DIR/cpu_1.prof"
echo "  go tool pprof $RESULT_DIR/mem_1.prof"
echo ""

echo "💡 macOS 说明："
echo "  - macOS 不支持 CPU 亲和性绑定（sched_setaffinity）"
echo "  - PerCoreExecutor 使用 LockOSThread 作为替代优化"
echo "  - 可以提供有限的性能提升（避免 goroutine 迁移）"
echo "  - 虽然不如真正的 CPU 绑核，但仍优于无优化版本"
echo ""
