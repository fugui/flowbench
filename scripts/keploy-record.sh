#!/usr/bin/env bash
# keploy-record.sh —— 使用 Keploy (eBPF) 监控并录制 flowbench-server 的入站与出站自定义 Protobuf 二进制流量。

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

BACKEND_ADDR="127.0.0.1:9200"
SERVER_ADDR="127.0.0.1:9100"
BACKEND_PID=""
KEPLOY_PID=""

cleanup() {
    if [[ -n "${KEPLOY_PID}" ]] && kill -0 "${KEPLOY_PID}" 2>/dev/null; then
        kill -INT "${KEPLOY_PID}" 2>/dev/null || true
        wait "${KEPLOY_PID}" 2>/dev/null || true
    fi
    if [[ -n "${BACKEND_PID}" ]] && kill -0 "${BACKEND_PID}" 2>/dev/null; then
        kill -TERM "${BACKEND_PID}" 2>/dev/null || true
        wait "${BACKEND_PID}" 2>/dev/null || true
    fi
    pkill -f "flowbench-server" 2>/dev/null || true
    pkill -f "flowbench-backend" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

if ! command -v keploy >/dev/null 2>&1; then
    echo "[WARN] 当前环境尚未安装 keploy CLI 二进制。"
    echo "       可执行以下命令安装 Keploy:"
    echo "       curl --silent -O -L https://keploy.io/install.sh && source install.sh"
    echo "       或运行 'go test -v -race ./tests -run TestBinaryRecordAndOfflineReplay' 执行内置二进制录制回放验证。"
    exit 1
fi

mkdir -p bin keploy
go build -o bin/flowbench-backend ./cmd/backend
go build -o bin/flowbench-server  ./cmd/server
go build -o bin/flowbench-client  ./cmd/client

echo "==> [Record 1/3] 启动真实 Backend 服务 (${BACKEND_ADDR})..."
./bin/flowbench-backend -addr="${BACKEND_ADDR}" &
BACKEND_PID=$!
sleep 0.5

echo "==> [Record 2/3] 通过 Keploy eBPF 启动并监控 Server (${SERVER_ADDR})..."
keploy record -c "./bin/flowbench-server -addr=${SERVER_ADDR} -backend-addr=${BACKEND_ADDR} -conn-mode=short -deterministic=true" --path ./keploy &
KEPLOY_PID=$!
sleep 3

echo "==> [Record 3/3] 执行 Client 全套测试套件生成二进制流量..."
./bin/flowbench-client -server-addr="${SERVER_ADDR}" -mode=suite -conn-mode=short
sleep 2

echo "==> [Record Complete] 停止录制并落盘 Keploy 测试集资产 (./keploy)..."
