#!/usr/bin/env bash
# keploy-test.sh —— 在完全停止真实 Backend 的情况下，使用 Keploy eBPF 离线回放测试 flowbench-server。
# 支持参数:
#   --mutation           开启 Server 侧折扣计算突变 (-fault-mutation=discount_bug)，验证回放测试检出回归错误
#   --non-deterministic  开启非确定性时间戳 (-deterministic=false)，验证动态字节对二进制回放断言的影响

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

SERVER_ADDR="127.0.0.1:9100"
BACKEND_ADDR="127.0.0.1:9200"
MOCK_SET_NAME="flowbench-e2e"
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
    sudo killall -9 flowbench-server flowbench-backend 2>/dev/null || true
    sudo chown -R "$(id -u):$(id -g)" ./keploy ./bin 2>/dev/null || true
}
trap cleanup EXIT INT TERM

if ! command -v keploy >/dev/null 2>&1; then
    echo "[WARN] 当前环境尚未安装 keploy CLI 二进制。"
    echo "       请先安装 Keploy，或运行 'go test -v -race ./tests -run TestBinaryRecordAndOfflineReplay' 执行内置离线回放验证。"
    exit 1
fi

# 确保真实 Backend 已完全关闭，证明回放流量 100% 由 Keploy eBPF TCP Mock 提供
sudo killall -9 flowbench-backend 2>/dev/null || true

mkdir -p bin
go build -o bin/flowbench-server ./cmd/server
go build -o bin/flowbench-client ./cmd/client

SERVER_CMD="./bin/flowbench-server -addr=${SERVER_ADDR} -backend-addr=${BACKEND_ADDR} -conn-mode=short -deterministic=${DETERMINISTIC}"
if [[ -n "${FAULT_MUTATION}" ]]; then
    SERVER_CMD="${SERVER_CMD} -fault-mutation=${FAULT_MUTATION}"
fi

echo "==> [Replay] 启动 Keploy eBPF 离线 Mock 回放测试 (真实 Backend 已停止，Server 命令: ${SERVER_CMD})..."
sudo -E env PATH="$PATH" keploy mock replay \
    --config-path ./keploy \
    --name "${MOCK_SET_NAME}" \
    --path ./keploy \
    --on-miss fail \
    --strict \
    --emit-mock-events \
    -c "sh -c '${SERVER_CMD} & SPID=\$!; sleep 0.5; ./bin/flowbench-client -server-addr=${SERVER_ADDR} -mode=suite -conn-mode=short; RC=\$?; kill -TERM \$SPID 2>/dev/null || true; exit \$RC'"
