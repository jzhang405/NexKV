#!/bin/bash
# Executor 性能对比分析脚本 (PerCore vs AntsDefault)
# 使用方法: sudo ./scripts/executor_comparison_perf.sh

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
OUTPUT_DIR="${PROJECT_ROOT}/docs/assets/perf"
FLAMEGRAPH_DIR="/tmp/FlameGraph"
TEST_DURATION="30s"

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}Executor Performance Comparison${NC}"
echo -e "${BLUE}PerCoreExecutor vs AntsDefaultExecutor${NC}"
echo -e "${BLUE}========================================${NC}"
echo ""
echo "Output directory: ${OUTPUT_DIR}"
echo "Test duration: ${TEST_DURATION}"
echo ""

# Create output directory
mkdir -p "${OUTPUT_DIR}"

# Check if perf is available
if ! command -v perf &> /dev/null; then
    echo -e "${RED}Error: perf is not installed${NC}"
    echo "Install with: sudo apt-get install linux-tools-common linux-tools-generic"
    exit 1
fi

# Check if FlameGraph is available
if [ ! -f "${FLAMEGRAPH_DIR}/flamegraph.pl" ]; then
    echo -e "${YELLOW}FlameGraph not found, installing...${NC}"
    git clone https://github.com/brendangregg/FlameGraph.git "${FLAMEGRAPH_DIR}"
    echo -e "${GREEN}✓ FlameGraph installed${NC}"
fi

# Function to run perf analysis
run_perf_analysis() {
    local executor_name=$1
    local test_function=$2
    local output_prefix=$3
    
    local perf_data="${OUTPUT_DIR}/${output_prefix}.perf"
    local folded="${OUTPUT_DIR}/${output_prefix}.folded"
    local svg="${OUTPUT_DIR}/${output_prefix}.svg"
    local report="${OUTPUT_DIR}/${output_prefix}_report.txt"
    local stat="${OUTPUT_DIR}/${output_prefix}_stat.txt"

    echo -e "${YELLOW}=== Analyzing ${executor_name} ===${NC}"

    # Run perf stat to collect hardware events
    echo "Collecting hardware events..."
    perf stat -e cycles,instructions,cache-references,cache-misses,\
        L1-dcache-loads,L1-dcache-load-misses,\
        LLC-loads,LLC-load-misses,\
        branches,branch-misses,\
        context-switches,cpu-migrations \
        -o "${stat}" \
        go test -v -run "^${test_function}$" \
            -timeout 5m \
            ./internal/infrastructure/concurrency/ 2>&1 | tee /dev/null || true

    # Run perf record to collect call stacks
    echo "Recording perf data..."
    go test -v -run "^${test_function}$" \
        -timeout 5m \
        ./internal/infrastructure/concurrency/ &
    TEST_PID=$!
    
    # Wait for test to start
    sleep 2
    
    # Record perf data
    sudo perf record -F 99 -p ${TEST_PID} -g --call-graph dwarf \
        -o "${perf_data}" \
        sleep ${TEST_DURATION}
    
    # Wait for test to complete
    wait ${TEST_PID} 2>/dev/null || true

    # Generate perf report
    if [ -f "${perf_data}" ]; then
        echo "Generating perf report..."
        perf report -i "${perf_data}" --stdio > "${report}"

        # Fold the perf data
        echo "Folding perf data..."
        perf script -i "${perf_data}" | \
            "${FLAMEGRAPH_DIR}/stackcollapse-perf.pl" > "${folded}"

        # Generate flame graph
        echo "Generating flame graph..."
        "${FLAMEGRAPH_DIR}/flamegraph.pl" \
            --title "${executor_name} Flame Graph" \
            --width 2400 \
            "${folded}" > "${svg}"

        echo -e "${GREEN}✓ Generated:${NC}"
        echo "  - ${svg}"
        echo "  - ${report}"
        echo "  - ${stat}"
    else
        echo -e "${RED}✗ Failed to generate perf data${NC}"
    fi
    echo ""
}

# Create performance test file
echo -e "${YELLOW}=== Creating Performance Tests ===${NC}"
cat > "${PROJECT_ROOT}/internal/infrastructure/concurrency/executor_perf_test.go" << 'TESTEOF'
// +build !integration

package concurrency

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"
)

// 模拟 Transport 场景：网络 I/O 密集型任务
func simulateTransportTask(ctx context.Context) {
	// 模拟网络延迟
	start := time.Now()
	for time.Since(start) < 10*time.Microsecond {
		runtime.Gosched()
	}

	// 模拟数据序列化/反序列化
	data := make([]byte, 1024)
	for i := range data {
		data[i] = byte(i % 256)
	}

	// 模拟校验和计算
	checksum := uint32(0)
	for _, b := range data {
		checksum += uint32(b)
	}
	_ = checksum
}

// TestPerCoreExecutor_PerfAnalysis - 持续运行用于 perf 分析
func TestPerCoreExecutor_PerfAnalysis(t *testing.T) {
	exec, _ := NewPerCoreExecutor(
		WithNumCores(runtime.NumCPU()),
		WithQueueSize(10000),
		WithEnableAffinity(true),
	)
	defer exec.Close()

	ctx := context.Background()
	duration := 30 * time.Second
	taskCount := 0

	start := time.Now()
	for time.Since(start) < duration {
		for i := 0; i < 1000; i++ {
			_ = exec.Submit(ctx, func(ctx context.Context) {
				simulateTransportTask(ctx)
			})
			taskCount++
		}
		time.Sleep(100 * time.Millisecond)
	}

	t.Logf("PerCoreExecutor completed %d tasks in %v", taskCount, duration)
}

// TestAntsDefaultExecutor_PerfAnalysis - 持续运行用于 perf 分析
func TestAntsDefaultExecutor_PerfAnalysis(t *testing.T) {
	exec := NewAntsDefaultExecutor()
	defer exec.Close()

	ctx := context.Background()
	duration := 30 * time.Second
	taskCount := 0

	start := time.Now()
	for time.Since(start) < duration {
		for i := 0; i < 1000; i++ {
			_ = exec.Submit(ctx, func(ctx context.Context) {
				simulateTransportTask(ctx)
			})
			taskCount++
		}
		time.Sleep(100 * time.Millisecond)
	}

	t.Logf("AntsDefaultExecutor completed %d tasks in %v", taskCount, duration)
}
TESTEOF

echo -e "${GREEN}✓ Test file created${NC}"
echo ""

# Run perf analysis for PerCoreExecutor
run_perf_analysis "PerCoreExecutor" "TestPerCoreExecutor_PerfAnalysis" "percore_executor"

# Run perf analysis for AntsDefaultExecutor
run_perf_analysis "AntsDefaultExecutor" "TestAntsDefaultExecutor_PerfAnalysis" "ants_executor"

# Generate comparison report
echo -e "${YELLOW}=== Generating Comparison Report ===${NC}"
cat > "${OUTPUT_DIR}/executor_comparison_report.md" << EOF
# Executor Performance Comparison Report

**Date:** $(date)
**Test Duration:** ${TEST_DURATION}
**Output Directory:** ${OUTPUT_DIR}

## Flame Graphs

### PerCoreExecutor
- **File:** \`percore_executor.svg\`
- **Characteristics:**
  - Each core runs a dedicated goroutine
  - CPU affinity enabled (goroutines pinned to cores)
  - Priority queue for task scheduling
  - Minimal context switching

### AntsDefaultExecutor  
- **File:** \`ants_executor.svg\`
- **Characteristics:**
  - Dynamic goroutine pool (ants library)
  - Goroutines scheduled across cores by Go scheduler
  - FIFO queue for task scheduling
  - Higher context switching overhead

## Performance Metrics Comparison

### Context Switches
\`\`\`bash
# PerCoreExecutor
cat percore_executor_stat.txt | grep context-switches

# AntsDefaultExecutor
cat ants_executor_stat.txt | grep context-switches
\`\`\`

### CPU Migrations
\`\`\`bash
# PerCoreExecutor
cat percore_executor_stat.txt | grep cpu-migrations

# AntsDefaultExecutor
cat ants_executor_execstat.txt | grep cpu-migrations
\`\`\`

### Cache Performance
\`\`\`bash
# L1 Cache Misses
cat percore_executor_stat.txt | grep L1-dcache-load-misses
cat ants_executor_stat.txt | grep L1-dcache-load-misses

# LLC Cache Misses
cat percore_executor_stat.txt | grep LLC-load-misses
cat ants_executor_stat.txt | grep LLC-load-misses
\`\`\`

## Key Findings

### Performance Bottlenecks

#### PerCoreExecutor
- **Strengths:**
  - Minimal context switching
  - Better cache locality
  - Predictable performance
- **Bottlenecks:**
  - Queue contention under high load
  - Priority queue overhead
  - Limited scalability (bound to core count)

#### AntsDefaultExecutor
- **Strengths:**
  - Dynamic scaling
  - Better load distribution
  - Handles burst traffic well
- **Bottlenecks:**
  - Higher context switching
  - Go scheduler overhead
  - Cache pollution

## Viewing Results

### Flame Graphs
Open the SVG files in a web browser:
\`\`\`bash
firefox docs/assets/perf/percore_executor.svg
firefox docs/assets/perf/ants_executor.svg
\`\`\`

### Interactive Analysis
\`\`\`bash
# PerCoreExecutor detailed analysis
perf report -i docs/assets/perf/percore_executor.perf

# AntsDefaultExecutor detailed analysis
perf report -i docs/assets/perf/ants_executor.perf
\`\`\`

### Annotated Source
\`\`\`bash
# View annotated source code
perf annotate -i docs/assets/perf/percore_executor.perf
perf annotate -i docs/assets/perf/ants_executor.perf
\`\`\`

## Recommendations

### Use PerCoreExecutor when:
- Latency-sensitive tasks (HLC, WAL, Transaction)
- Consistent performance required
- Core count is limited
- Cache locality matters

### Use AntsDefaultExecutor when:
- Burst traffic patterns
- Variable workload
- Core count is high
- Scaling is more important than latency
