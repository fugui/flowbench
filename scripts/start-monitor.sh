#!/usr/bin/env bash
# start-monitor.sh —— 编译并以 root/CAP_BPF 权限启动 flowbench-monitor 原生 cilium/ebpf 可视化监控与回放控制台。
# 默认监听: http://127.0.0.1:9300

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

# 兼容 sudo 导致的 PATH 重置：自动探测调用用户的 Go 安装路径
INVOKING_USER="${SUDO_USER:-${USER:-$(id -un)}}"
INVOKING_HOME="$(getent passwd "$INVOKING_USER" | cut -d: -f6 || echo "/home/$INVOKING_USER")"
export PATH="${INVOKING_HOME}/.local/go/bin:${INVOKING_HOME}/go/bin:/usr/local/go/bin:${PATH}"

ADDR="${1:-127.0.0.1:9300}"

mkdir -p bin
if command -v go >/dev/null 2>&1; then
    if [[ "${EUID:-$(id -u)}" -eq 0 && -n "${SUDO_USER:-}" ]]; then
        sudo -u "$SUDO_USER" env PATH="$PATH" go build -o bin/flowbench-monitor ./cmd/monitor
    else
        go build -o bin/flowbench-monitor ./cmd/monitor
    fi
elif [[ ! -x ./bin/flowbench-monitor ]]; then
    echo "错误: 未找到 go 编译器且 ./bin/flowbench-monitor 不存在，请先运行 make build" >&2
    exit 1
fi

echo "==> 启动 flowbench-monitor (cilium/ebpf cgroup/connect4 + 实时 Web 控制台: http://${ADDR})..."
if [[ "${EUID:-$(id -u)}" -eq 0 ]]; then
    exec ./bin/flowbench-monitor -addr="${ADDR}"
else
    exec sudo -E env PATH="$PATH" ./bin/flowbench-monitor -addr="${ADDR}"
fi
