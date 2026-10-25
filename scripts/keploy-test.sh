#!/usr/bin/env bash
# keploy-test.sh —— 在完全停止真实 Backend 的情况下，使用 Keploy 回放测试 flowbench-server。
# 支持参数:
#   --mutation           开启 Server 侧折扣计算突变 (-fault-mutation=discount_bug)，验证 Keploy 检出回归错误
#   --non-deterministic  开启非确定性时间戳 (-deterministic=false)，验证动态字节对二进制回放的影响

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

SERVER_ADDR="127.0.0.1:9100"
BACKEND_ADDR="127.0.0.1:9200"
DETERMINISTIC="true"
FAULT_MUTATION=""

for arg in "$@"; do
    case "$arg" in
        --mutation)
            FAULT_MUTATION="discount_bug"
            ;;
        --non-deterministic)
            DETERMINISTIC="false"
            ;;
    esac
done

cleanup() {
    pkill -f "flowbench-server" 2>/dev/null || true
    pkill -f "flowbench-backend" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

if ! command -v keploy >/dev/null 2>&1; then
    echo "[WARN] 当前环境尚未安装 keploy CLI 二进制。"
    echo "       请先安装 Keploy，或运行 'go test -v -race ./tests -run TestBinaryRecordAndOfflineReplay' 执行内置离线回放验证。"
    exit 1
fi

# 确保真实 Backend 已完全关闭，证明回放流量 100% 由 Keploy eBPF TCP Mock 提供
pkill -f "flowbench-backend" 2>/dev/null || true

mkdir -p bin
go build -o bin/flowbench-server ./cmd/server

SERVER_CMD="./bin/flowbench-server -addr=${SERVER_ADDR} -backend-addr=${BACKEND_ADDR} -conn-mode=short -deterministic=${DETERMINISTIC}"
if [[ -n "${FAULT_MUTATION}" ]]; then
    SERVER_CMD="${SERVER_CMD} -fault-mutation=${FAULT_MUTATION}"
fi

echo "==> [Replay] 启动 Keploy 离线回放测试 (真实 Backend 已停止，Server 命令: ${SERVER_CMD})..."
keploy test -c "${SERVER_CMD}" --path ./keploy --delay 2
