# NexKV TLA+ Verification Test Automation Makefile
# 自动化测试、覆盖率、性能对比和报告生成

.PHONY: help test test-verbose test-coverage test-bench test-report test-all clean

# 默认目标
.DEFAULT_GOAL := help

# 颜色输出
BLUE := \033[0;34m
GREEN := \033[0;32m
RED := \033[0;31m
YELLOW := \033[0;33m
NC := \033[0m # No Color

# 目录
REPORT_DIR := reports
COVERAGE_DIR := $(REPORT_DIR)/coverage
BENCHMARK_DIR := $(REPORT_DIR)/benchmark

# help: 显示帮助信息
help:
	@echo "$(BLUE)NexKV TLA+ Verification 测试自动化$(NC)"
	@echo ""
	@echo "$(GREEN)可用目标:$(NC)"
	@echo "  make test           - 运行所有测试"
	@echo "  make test-verbose   - 运行测试（详细输出）"
	@echo "  make test-coverage  - 生成测试覆盖率报告"
	@echo "  make test-bench     - 运行性能测试并生成对比报告"
	@echo "  make test-report    - 生成 HTML 测试汇总报告"
	@echo "  make test-all       - 运行所有测试和报告（完整流程）"
	@echo "  make clean          - 清理报告文件"
	@echo ""
	@echo "$(GREEN)示例:$(NC)"
	@echo "  make test-all       # 完整的测试+报告流程"

# test: 运行所有测试
test:
	@echo "$(BLUE)▶ 运行所有测试...$(NC)"
	@go test -v ./... -timeout 10m

# test-verbose: 运行测试（详细输出）
test-verbose:
	@echo "$(BLUE)▶ 运行测试（详细模式）...$(NC)"
	@go test -v ./... -timeout 10m -coverprofile=$(COVERAGE_DIR)/coverage.out

# test-coverage: 生成测试覆盖率报告
test-coverage:
	@echo "$(BLUE)▶ 生成测试覆盖率报告...$(NC)"
	@mkdir -p $(COVERAGE_DIR)
	@go test ./... -coverprofile=$(COVERAGE_DIR)/coverage.out -covermode=atomic -timeout 10m
	@go tool cover -html=$(COVERAGE_DIR)/coverage.out -o $(COVERAGE_DIR)/coverage.html
	@echo "$(GREEN)✅ 覆盖率报告生成完成:$(NC)"
	@echo "   - $(COVERAGE_DIR)/coverage.out"
	@echo "   - $(COVERAGE_DIR)/coverage.html"
	@echo ""
	@echo "$(YELLOW)覆盖率摘要:$(NC)"
	@go tool cover -func=$(COVERAGE_DIR)/coverage.out | tail -1

# test-bench: 运行性能测试并生成对比报告
test-bench:
	@echo "$(BLUE)▶ 运行性能测试（3种 Transport）...$(NC)"
	@mkdir -p $(BENCHMARK_DIR)
	@echo "$(YELLOW)注意：性能测试需要一定时间，请耐心等待...$(NC)"
	@go test -bench=. -benchmem -benchtime=1s ./... > $(BENCHMARK_DIR)/benchmark.txt 2>&1
	@cd scripts/benchmark-report && go run main.go
	@echo "$(GREEN)✅ 性能测试完成:$(NC)"
	@echo "   - $(BENCHMARK_DIR)/benchmark.txt"
	@echo "   - $(BENCHMARK_DIR)/benchmark_report.html"
	@echo "   - $(BENCHMARK_DIR)/benchmark_comparison.md"
	@echo ""
	@echo "$(YELLOW)性能对比摘要:$(NC)"
	@go test -bench=. -benchmem -benchtime=1s ./... 2>/dev/null | grep -E "(PASS|FAIL|Benchmark)" | head -20

# test-report: 生成 HTML 测试汇总报告
test-report:
	@echo "$(BLUE)▶ 生成 HTML 测试汇总报告...$(NC)"
	@mkdir -p $(REPORT_DIR)
	@go test -v ./... -timeout 10m > $(REPORT_DIR)/test_output.txt 2>&1
	@cd scripts/test-report && go run main.go
	@echo "$(GREEN)✅ 测试报告:$(NC)"
	@echo "   - $(REPORT_DIR)/test_output.txt"
	@if [ -f $(REPORT_DIR)/test_report.html ]; then \
		echo "   - $(REPORT_DIR)/test_report.html"; \
	fi

# test-all: 运行完整测试流程（测试+覆盖率+性能+报告）
test-all: clean test test-coverage test-bench test-report
	@echo ""
	@echo "$(GREEN)========================================$(NC)"
	@echo "$(GREEN)✅ 完整测试流程已完成！$(NC)"
	@echo "$(GREEN)========================================$(NC)"
	@echo ""
	@echo "$(BLUE)📊 报告文件位置:$(NC)"
	@echo "   覆盖率报告: $(COVERAGE_DIR)/coverage.html"
	@echo "   性能测试:   $(BENCHMARK_DIR)/benchmark_report.html"
	@echo "   测试输出:   $(REPORT_DIR)/test_output.txt"
	@echo ""
	@echo "$(BLUE)💡 查看报告:$(NC)"
	@echo "   open $(COVERAGE_DIR)/coverage.html"
	@echo "   open $(BENCHMARK_DIR)/benchmark_report.html"

# clean: 清理报告文件
clean:
	@echo "$(BLUE)▶ 清理报告文件...$(NC)"
	@rm -rf $(REPORT_DIR)
	@echo "$(GREEN)✅ 清理完成$(NC)"
