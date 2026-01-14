#!/bin/bash

# NexKV TLA+ 验证 - TLC 运行脚本
#
# 用途：运行 TLC 模型检查器，验证 QuorumSimple.tla
#
# 使用方法：
#   cd tla-verification
#   chmod +x scripts/run-tlc.sh
#   ./scripts/run-tlc.sh

set -e  # 遇到错误立即退出

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${GREEN}======================================${NC}"
echo -e "${GREEN}NexKV TLA+ 验证 - TLC 模型检查器${NC}"
echo -e "${GREEN}======================================${NC}"

# 检查 TLA+ Toolbox 是否安装
if ! command -v tlc2 &> /dev/null; then
    echo -e "${RED}错误: 未找到 TLC 命令行工具${NC}"
    echo -e "${YELLOW}请安装 TLA+ Toolbox:${NC}"
    echo "  brew install --cask tla-plus-toolbox"
    echo "  或访问: https://github.com/tlaplus/tlaplus/releases"
    exit 1
fi

# 进入模型目录
cd "$(dirname "$0")/.."
MODEL_DIR="$(pwd)/models"
REPORT_DIR="$(pwd)/reports"

# 创建报告目录
mkdir -p "$REPORT_DIR"

# 检查模型文件是否存在
MODEL_FILE="$MODEL_DIR/QuorumSimple.tla"
if [ ! -f "$MODEL_FILE" ]; then
    echo -e "${RED}错误: 未找到模型文件: $MODEL_FILE${NC}"
    exit 1
fi

# 生成报告文件名（带时间戳）
TIMESTAMP=$(date +"%Y%m%d-%H%M%S")
REPORT_FILE="$REPORT_DIR/tlc-output-$TIMESTAMP.txt"

echo -e "${YELLOW}模型文件: $MODEL_FILE${NC}"
echo -e "${YELLOW}报告文件: $REPORT_FILE${NC}"
echo ""

# 创建 TLC 配置文件
cat > /tmp/QuorumSimple.cfg <<EOF
CONSTANTS
 Nodes = {n1, n2, n3}
 Majority = 2
EOF

# 运行 TLC
echo -e "${GREEN}开始运行 TLC...${NC}"
echo ""

tlc2 -deadlock -depth 100 -config /tmp/QuorumSimple.cfg "$MODEL_FILE" 2>&1 | tee "$REPORT_FILE"

# 检查 TLC 退出码
TLC_EXIT_CODE=${PIPESTATUS[0]}

echo ""
echo -e "${GREEN}======================================${NC}"
echo -e "${GREEN}TLC 运行完成${NC}"
echo -e "${GREEN}======================================${NC}"

if [ $TLC_EXIT_CODE -eq 0 ]; then
    echo -e "${GREEN}✅ 所有性质验证通过！${NC}"
    echo -e "${GREEN}详细报告: $REPORT_FILE${NC}"
else
    echo -e "${RED}❌ TLC 发现反例或错误${NC}"
    echo -e "${RED}退出码: $TLC_EXIT_CODE${NC}"
    echo -e "${YELLOW}详细报告: $REPORT_FILE${NC}"
    echo -e "${YELLOW}请查看报告中的 'Counterexample' 部分${NC}"
fi

exit $TLC_EXIT_CODE
