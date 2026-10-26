#!/usr/bin/env bash
# start-monitor.sh —— 编译并以 root/CAP_BPF 权限启动 flowbench-monitor 原生 cilium/ebpf 可视化监控与回放控制台。
# 默认监听: http://127.0.0.1:9300

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

ADDR="${1:-127.0.0.1:9300}"

mkdir -p bin
go build -o bin/flowbench-monitor ./cmd/monitor

echo "==> 启动 flowbench-monitor (cilium/ebpf cgroup/connect4 + 实时 Web 控制台: http://${ADDR})..."
exec sudo -E env PATH="$PATH" ./bin/flowbench-monitor -addr="${ADDR}"
