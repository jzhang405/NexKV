#!/bin/bash
# Perf 分析包装脚本 - 解决 sudo 环境变量问题
# 使用方法: ./scripts/run_perf_analysis.sh

set -e

# 获取当前用户的环境变量
export GOROOT=/home/jzh/go
export GOPATH=/home/jzh/ws/go
export PATH=$GOROOT/bin:$GOPATH/bin:/usr/local/bin:/usr/bin:/bin

echo "========================================"
echo "环境变量检查"
echo "========================================"
echo "GOROOT: $GOROOT"
echo "GOPATH: $GOPATH"
echo "PATH: $PATH"
echo "Go version:"
go version
echo ""

# 使用 sudo -E 保留环境变量
echo "执行 perf 分析（需要 sudo 权限）..."
echo ""

sudo -E bash "$HOME/ws/go/src/github.com/jzhang405/NexKV/scripts/perf_analysis.sh"
