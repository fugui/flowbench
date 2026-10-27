#!/usr/bin/env bash
# verify-e2e.sh —— 一键编译并拉起 Backend 与 Server，执行 Client 全套自定义二进制 Protobuf 场景验证与粘包分片验证。
# 内置 trap 清理机制，确保脚本结束时 100% 回收所有后台进程，绝不残留僵尸进程占用本地端口。

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

INVOKING_USER="${SUDO_USER:-${USER:-$(id -un)}}"
INVOKING_HOME="$(getent passwd "$INVOKING_USER" | cut -d: -f6 || echo "/home/$INVOKING_USER")"
export PATH="${INVOKING_HOME}/.local/go/bin:${INVOKING_HOME}/go/bin:/usr/local/go/bin:${PATH}"

BACKEND_ADDR="127.0.0.1:19200"
SERVER_ADDR="127.0.0.1:19100"
BACKEND_PID=""
SERVER_PID=""

cleanup() {
    if [[ -n "${SERVER_PID}" ]] && kill -0 "${SERVER_PID}" 2>/dev/null; then
        kill -TERM "${SERVER_PID}" 2>/dev/null || true
        wait "${SERVER_PID}" 2>/dev/null || true
    fi
    if [[ -n "${BACKEND_PID}" ]] && kill -0 "${BACKEND_PID}" 2>/dev/null; then
        kill -TERM "${BACKEND_PID}" 2>/dev/null || true
        wait "${BACKEND_PID}" 2>/dev/null || true
    fi
}
trap cleanup EXIT INT TERM

echo "==> [1/4] 编译 flowbench 三大子系统二进制 (backend / server / client)..."
mkdir -p bin
go build -o bin/flowbench-backend ./cmd/backend
go build -o bin/flowbench-server  ./cmd/server
go build -o bin/flowbench-client  ./cmd/client

echo "==> [2/4] 启动下游 Backend 服务 (${BACKEND_ADDR})..."
./bin/flowbench-backend -addr="${BACKEND_ADDR}" > /tmp/flowbench-backend.log 2>&1 &
BACKEND_PID=$!
sleep 0.3

echo "==> [3/4] 启动核心被测 Server 服务 (${SERVER_ADDR} -> ${BACKEND_ADDR})..."
./bin/flowbench-server -addr="${SERVER_ADDR}" -backend-addr="${BACKEND_ADDR}" -conn-mode=short -deterministic=true > /tmp/flowbench-server.log 2>&1 &
SERVER_PID=$!
sleep 0.3

echo "==> [4/4] 运行 Client 标准验证套件 (suite: Short-Conn & Keep-Alive) 与粘包分片压测 (framing)..."
./bin/flowbench-client -server-addr="${SERVER_ADDR}" -mode=suite -conn-mode=short
./bin/flowbench-client -server-addr="${SERVER_ADDR}" -mode=suite -conn-mode=keepalive
./bin/flowbench-client -server-addr="${SERVER_ADDR}" -mode=framing

echo "==> [DONE] 三端全链路自定义 Protobuf 二进制协议验证全部通过，正在自动清理后台服务..."
