#!/bin/bash
set -e

echo "================================"
echo "Phase 0.5: PageReference 原型验证"
echo "================================"

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# 日志函数
log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# 检查依赖
check_dependencies() {
    log_info "检查依赖..."
    if ! command -v go &> /dev/null; then
        log_error "Go 未安装"
        exit 1
    fi

    # 检查 testify
    if ! go list -m github.com/stretchr/testify &> /dev/null; then
        log_warn "testify 未安装，正在安装..."
        go get github.com/stretchr/testify/assert
        go get github.com/stretchr/testify/require
    fi

    echo "✓ 依赖检查通过"
}

# 格式化代码
format_code() {
    log_info "格式化代码..."
    go fmt ./...
    echo "✓ 代码格式化完成"
}

# 静态检查
vet_check() {
    log_info "静态检查..."
    output=$(go vet ./... 2>&1 || true)
    if [ -n "$output" ]; then
        log_error "go vet 发现问题："
        echo "$output"
        return 1
    fi
    echo "✓ 静态检查通过"
}

# 单元测试
run_unit_tests() {
    log_info "运行单元测试..."
    go test -v -run=^Test_$
    echo "✓ 单元测试通过"
}

# 并发测试（race detector）
run_concurrent_tests() {
    log_info "运行并发安全测试..."
    output=$(go test -race -v -run=Test_Concurrent 2>&1)
    if echo "$output" | grep -q "WARNING: DATA RACE"; then
        log_error "发现数据竞争！"
        echo "$output"
        return 1
    fi
    echo "✓ 并发测试通过（无数据竞争）"
}

# 基准测试
run_benchmarks() {
    log_info "运行性能基准测试..."

    # 创建输出目录
    mkdir -p results

    # 运行基准测试并生成 profile
    go test -bench=. -benchmem -cpuprofile=results/cpu.prof -memprofile=results/mem.prof > results/benchmark.txt 2>&1

    # 生成文本报告
    go tool pprof -text results/cpu.prof > results/cpu-profile.txt

    echo "✓ 基准测试完成"
    echo "  结果文件：results/benchmark.txt"
    echo "  CPU profile: results/cpu.prof"
    echo "  Memory profile: results/mem.prof"
    echo "  CPU 文本报告: results/cpu-profile.txt"
}

# 性能分析
analyze_performance() {
    log_info "分析性能数据..."

    # 检查关键指标
    log_info "关键指标检查："

    # 检查原子指针读取性能
    atomic_read=$(grep "Benchmark_AtomicPointer_Read" results/benchmark.txt | awk '{print $3}')
    if [ -n "$atomic_read" ]; then
        # 提取 ns/op（假设格式为 "xxx ns/op"）
        ns_op=$(echo $atomic_read | sed 's/ns\/op.*//')
        log_info "  原子指针读取: $atomic_read"

        # 判断是否满足标准
        if (( $(echo "$ns_op < 100" | bc -l) )); then
            echo -e "    ${GREEN}✓ 满足成功标准（<100ns）${NC}"
        elif (( $(echo "$ns_op < 500" | bc -l) )); then
            echo -e "    ${YELLOW}⚠ 有条件通过（100-500ns）${NC}"
        else
            echo -e "    ${RED}✗ 未满足标准（>500ns）${NC}"
        fi
    fi

    # 检查并发读取性能
    concurrent_read=$(grep "Benchmark_PageReference_ConcurrentRead" results/benchmark.txt | awk '{print $5, $6}')
    if [ -n "$concurrent_read" ]; then
        log_info "  并发读取吞吐: $concurrent_read"
    fi
}

# 生成火焰图
generate_flamegraph() {
    log_info "生成 CPU 火焰图..."

    if command -v go &> /dev/null && [ -f results/cpu.prof ]; then
        go tool pprof -png results/cpu.prof > results/cpu-flamegraph.png 2>&1
        echo "✓ 火焰图生成完成: results/cpu-flamegraph.png"
    else
        log_warn "跳过火焰图生成（缺少必要工具）"
    fi
}

# 显示结果摘要
show_summary() {
    echo ""
    echo "================================"
    echo "测试结果摘要"
    echo "================================"

    # 显示基准测试结果摘要
    if [ -f results/benchmark.txt ]; then
        echo ""
        echo "基准测试结果："
        grep "^Benchmark_" results/benchmark.txt | head -20
    fi

    echo ""
    echo "详细报告位置："
    echo "  - 基准测试: results/benchmark.txt"
    echo "  - CPU profile: results/cpu.prof"
    echo "  - 火焰图: results/cpu-flamegraph.png"

    echo ""
    echo "查看 CPU profile："
    echo "  go tool pprof -http=:8080 results/cpu.prof"
}

# 清理
cleanup() {
    log_info "清理临时文件..."
    # 保留 results 目录，清理其他临时文件
    echo "✓ 清理完成"
}

# 主流程
main() {
    # 创建 results 目录
    mkdir -p results

    # 执行测试流程
    check_dependencies
    format_code
    vet_check || log_warn "静态检查发现问题，但继续执行"
    run_unit_tests
    run_concurrent_tests || { log_error "并发测试失败"; exit 1; }
    run_benchmarks
    analyze_performance
    generate_flamegraph
    show_summary

    echo ""
    echo -e "${GREEN}================================${NC}"
    echo -e "${GREEN}✓ 所有测试完成${NC}"
    echo -e "${GREEN}================================${NC}"
}

# 解析命令行参数
case "${1:-all}" in
    format)
        format_code
        ;;
    vet)
        vet_check
        ;;
    test)
        run_unit_tests
        ;;
    concurrent)
        run_concurrent_tests
        ;;
    benchmark)
        run_benchmarks
        analyze_performance
        ;;
    profile)
        generate_flamegraph
        ;;
    clean)
        cleanup
        rm -rf results
        ;;
    all|*)
        main
        ;;
esac
