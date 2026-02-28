#!/bin/bash
# CPU 绑核性能分析脚本
# 使用方法: ./scripts/perf_analysis.sh

set -e

# ========== 设置 Go 环境 ==========
export GOROOT=/home/jzh/go
export GOPATH=/home/jzh/ws/go
export PATH=$GOROOT/bin:$PATH
# ===================================

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
TEST_BINARY="/tmp/affinity_perf_test"
PERF_DATA_DIR="/tmp/perf_data_$(date +%Y%m%d_%H%M%S)"
RESULTS_FILE="$PERF_DATA_DIR/results.txt"

mkdir -p "$PERF_DATA_DIR"

echo "========================================"
echo "NexKV CPU 绑核性能分析"
echo "========================================"
echo "项目根目录: $PROJECT_ROOT"
echo "Perf 数据目录: $PERF_DATA_DIR"
echo ""

# ========== 步骤 1: 编译测试程序 ==========
echo "步骤 1/5: 编译测试程序..."
cd "$PROJECT_ROOT"
go test -c \
	-o "$TEST_BINARY" \
	-gcflags="-N -l" \
	./internal/infrastructure/concurrency/

echo "✅ 编译完成: $TEST_BINARY"
echo ""

# ========== 步骤 2: 运行绑核版本分析 ==========
echo "步骤 2/5: 分析绑核版本性能..."

echo "--- 2.1 使用 perf stat 收集硬件事件 ---"
perf stat -e cycles,instructions,cache-references,cache-misses,\
L1-dcache-loads,L1-dcache-load-misses,\
LLC-loads,LLC-load-misses,\
branches,branch-misses,\
context-switches,cpu-migrations \
-o "$PERF_DATA_DIR/stat_with_affinity.txt" \
"$TEST_BINARY" \
	-test.run=TestPerCore_CPUAffinity_PerfAnalysis \
	-test.v 2>&1 | tee "$PERF_DATA_DIR/output_with_affinity.txt"

echo "--- 2.2 使用 perf record 收集调用栈 ---"
perf record -g -e cycles:u \
	-F 99 \
	-o "$PERF_DATA_DIR/perf_with_affinity.data" \
	"$TEST_BINARY" \
	-test.run=TestPerCore_CPUAffinity_PerfAnalysis \
	-test.v 2>&1 | tee "$PERF_DATA_DIR/record_with_affinity.txt"

echo "--- 2.3 生成 perf report ---"
perf report -i "$PERF_DATA_DIR/perf_with_affinity.data" \
	--stdio > "$PERF_DATA_DIR/report_with_affinity.txt"

echo "✅ 绑核版本分析完成"
echo ""

# ========== 步骤 3: 修改测试为无绑核版本 ==========
echo "步骤 3/5: 编译无绑核版本..."

# 临时修改测试文件（设置 withAffinity = false）
sed -i.bak 's/withAffinity := true/withAffinity := false/' \
	"$PROJECT_ROOT/internal/infrastructure/concurrency/affinity_perf_test.go"

go test -c \
	-o "$TEST_BINARY" \
	-gcflags="-N -l" \
	./internal/infrastructure/concurrency/

# 恢复原文件
mv "$PROJECT_ROOT/internal/infrastructure/concurrency/affinity_perf_test.go.bak" \
	"$PROJECT_ROOT/internal/infrastructure/concurrency/affinity_perf_test.go"

echo "✅ 无绑核版本编译完成"
echo ""

# ========== 步骤 4: 运行无绑核版本分析 ==========
echo "步骤 4/5: 分析无绑核版本性能..."

echo "--- 4.1 使用 perf stat 收集硬件事件 ---"
perf stat -e cycles,instructions,cache-references,cache-misses,\
L1-dcache-loads,L1-dcache-load-misses,\
LLC-loads,LLC-load-misses,\
branches,branch-misses,\
context-switches,cpu-migrations \
-o "$PERF_DATA_DIR/stat_without_affinity.txt" \
"$TEST_BINARY" \
	-test.run=TestPerCore_CPUAffinity_PerfAnalysis \
	-test.v 2>&1 | tee "$PERF_DATA_DIR/output_without_affinity.txt"

echo "--- 4.2 使用 perf record 收集调用栈 ---"
perf record -g -e cycles:u \
	-F 99 \
	-o "$PERF_DATA_DIR/perf_without_affinity.data" \
	"$TEST_BINARY" \
	-test.run=TestPerCore_CPUAffinity_PerfAnalysis \
	-test.v 2>&1 | tee "$PERF_DATA_DIR/record_without_affinity.txt"

echo "--- 4.3 生成 perf report ---"
perf report -i "$PERF_DATA_DIR/perf_without_affinity.data" \
	--stdio > "$PERF_DATA_DIR/report_without_affinity.txt"

echo "✅ 无绑核版本分析完成"
echo ""

# ========== 步骤 5: 生成对比报告 ==========
echo "步骤 5/5: 生成性能对比报告..."

cat > "$RESULTS_FILE" << 'EOF'
========================================
NexKV CPU 绑核性能分析报告
========================================

EOF

echo "## 1. 绑核版本 (With Affinity)" >> "$RESULTS_FILE"
echo "" >> "$RESULTS_FILE"
grep -A 20 "Performance counter stats" "$PERF_DATA_DIR/stat_with_affinity.txt" >> "$RESULTS_FILE" || true
echo "" >> "$RESULTS_FILE"

echo "## 2. 无绑核版本 (Without Affinity)" >> "$RESULTS_FILE"
echo "" >> "$RESULTS_FILE"
grep -A 20 "Performance counter stats" "$PERF_DATA_DIR/stat_without_affinity.txt" >> "$RESULTS_FILE" || true
echo "" >> "$RESULTS_FILE"

echo "## 3. 热点函数对比 (Top 10)" >> "$RESULTS_FILE"
echo "" >> "$RESULTS_FILE"
echo "### 绑核版本热点:" >> "$RESULTS_FILE"
head -20 "$PERF_DATA_DIR/report_with_affinity.txt" >> "$RESULTS_FILE" || true
echo "" >> "$RESULTS_FILE"
echo "### 无绑核版本热点:" >> "$RESULTS_FILE"
head -20 "$PERF_DATA_DIR/report_without_affinity.txt" >> "$RESULTS_FILE" || true
echo "" >> "$RESULTS_FILE"

echo "✅ 分析报告已生成: $RESULTS_FILE"
echo ""

# ========== 显示关键指标对比 ==========
echo "========================================"
echo "性能对比摘要"
echo "========================================"
echo ""
echo "查看详细报告:"
echo "  cat $RESULTS_FILE"
echo ""
echo "交互式查看 perf report:"
echo "  perf report -i $PERF_DATA_DIR/perf_with_affinity.data"
echo "  perf report -i $PERF_DATA_DIR/perf_without_affinity.data"
echo ""
echo "火焰图生成（需要 FlameGraph 工具）:"
echo "  perf script -i $PERF_DATA_DIR/perf_with_affinity.data | \
    FlameGraph/stackcollapse-perf.pl | \
    FlameGraph/flamegraph.pl > $PERF_DATA_DIR/flamegraph_with_affinity.svg"
echo ""

# 清理
rm -f "$TEST_BINARY"

echo "分析完成！所有数据保存在: $PERF_DATA_DIR"
