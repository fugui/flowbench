#!/usr/bin/env bash
# keploy-record.sh —— 使用 Keploy (eBPF) 监控并录制 flowbench-server 的自定义 Protobuf 二进制流量。
# 采用 `keploy mock record` 模式：
#   - Client -> Server (127.0.0.1:9100) 由 Keploy 识别为同作业 self-call 原生透传（绕过仅支持 HTTP/1.1 的 Ingress Proxy）；
#   - Server -> Backend (127.0.0.1:9200) 由 Keploy eBPF Egress Proxy (V2 generic parser) 完整捕获为二进制 Mock (kind: Generic, type: binary)。

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

BACKEND_ADDR="127.0.0.1:9200"
SERVER_ADDR="127.0.0.1:9100"
MOCK_SET_NAME="flowbench-e2e"
BACKEND_PID=""

cleanup() {
    if [[ -n "${BACKEND_PID}" ]] && kill -0 "${BACKEND_PID}" 2>/dev/null; then
        kill -TERM "${BACKEND_PID}" 2>/dev/null || true
        wait "${BACKEND_PID}" 2>/dev/null || true
    fi
    sudo killall -9 flowbench-server flowbench-backend 2>/dev/null || true
    if [[ -d "./keploy" ]]; then
        sudo chown -R "$(id -u):$(id -g)" ./keploy 2>/dev/null || true
    fi
}
trap cleanup EXIT INT TERM

if ! command -v keploy >/dev/null 2>&1; then
    echo "[WARN] 当前环境尚未安装 keploy CLI 二进制。"
    echo "       可执行以下命令安装 Keploy:"
    echo "       curl --silent -O -L https://keploy.io/install.sh && source install.sh --oss"
    echo "       或运行 'go test -v -race ./tests -run TestBinaryRecordAndOfflineReplay' 执行内置二进制录制回放验证。"
    exit 1
fi

mkdir -p bin keploy
go build -o bin/flowbench-backend ./cmd/backend
go build -o bin/flowbench-server  ./cmd/server
go build -o bin/flowbench-client  ./cmd/client

echo "==> [Record 1/3] 启动真实 Backend 服务 (${BACKEND_ADDR})..."
./bin/flowbench-backend -addr="${BACKEND_ADDR}" > /tmp/flowbench-backend-record.log 2>&1 &
BACKEND_PID=$!
sleep 0.4

echo "==> [Record 2/3] 启动 Keploy eBPF (mock record) 监控 Server -> Backend 二进制流量并执行 Client 验证套件..."
sudo -E env PATH="$PATH" keploy mock record \
    --config-path ./keploy \
    --name "${MOCK_SET_NAME}" \
    --path ./keploy \
    -c "sh -c './bin/flowbench-server -addr=${SERVER_ADDR} -backend-addr=${BACKEND_ADDR} -conn-mode=short -deterministic=true & SPID=\$!; sleep 0.5; ./bin/flowbench-client -server-addr=${SERVER_ADDR} -mode=suite -conn-mode=short; RC=\$?; kill -TERM \$SPID 2>/dev/null || true; exit \$RC'"

echo "==> [Record 3/3] 录制完成，生成的二进制 Mock 资产位于: ./keploy/keploy/${MOCK_SET_NAME}/mocks.yaml"
ls -lh "./keploy/keploy/${MOCK_SET_NAME}/mocks.yaml"
