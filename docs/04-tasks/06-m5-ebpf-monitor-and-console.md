# Milestone 5 作战卡：原生 `cilium/ebpf` 协议感知监控、智能回放与实时 Web 控制台

- **文档编号**：`06-m5-ebpf-monitor-and-console.md`
- **文档版本**：v1.0.0
- **编制日期**：2026-10-26
- **归属阶段**：阶段三 & 四（演进篇）：任务拆解与实施验收标准

---

## 1. 里程碑目标 (Milestone Goal)

引入 `github.com/cilium/ebpf`，实现内核态 `cgroup/connect4` 透明流量劫持探针（`internal/ebpf`）、协议感知录制与智能回放引擎（`internal/monitor`）以及内嵌实时 Web 可视化监控控制台（`cmd/monitor`），完成对 `flowbench` 私有二进制协议的全链路透明捕获、24B 帧头与 Protobuf 明文可视化解剖、无 `Backend` 离线 eBPF 回放、字段级回归 Diff 诊断以及 Keploy `mocks.yaml` 逆向解码对比。

---

## 2. 任务分解清单 (Task Breakdown)

| 任务 ID | 任务名称 | 交付产物路径 | 核心实施要求 |
| :--- | :--- | :--- | :--- |
| **M5-T1** | `cilium/ebpf` `cgroup/connect4` 内核探针与 BPF Map 管理器 | `internal/ebpf/interceptor.go`<br>`internal/ebpf/interceptor_test.go` | 1. 使用 `cilium/ebpf/asm` 编写 `cgroup/connect4` BPF 程序，支持按 `bpf_get_current_pid_tgid()` 豁免监控器自身 PID；<br>2. 透明改写发往 `127.0.0.1:9100` 与 `127.0.0.1:9200` 的 TCP 连接至代理端口；<br>3. 提供 `Stats()` 读取 BPF Map 计数与 `Close()` 安全卸载探针。 |
| **M5-T2** | 协议感知帧解剖器、Keploy Base64 逆向解码与智能回放引擎 | `internal/monitor/inspector.go`<br>`internal/monitor/engine.go`<br>`internal/monitor/keploy_decoder.go` | 1. 解剖 24B 帧头、校验 CRC32、调用 `protojson` 解码 Protobuf 明文；<br>2. 实现 Ingress/Egress 透明代理、语义级 Mock 匹配、动态 `RequestID` 重写与 `CRC32` 自愈；<br>3. 实现录制基线与回放响应的字段级 Diff 及 Keploy `mocks.yaml` 逆向解码。 |
| **M5-T3** | 内嵌安全 Web 可视化监控与一键实验控制台 | `internal/monitor/web/index.html`<br>`internal/monitor/web/app.js`<br>`internal/monitor/server.go`<br>`cmd/monitor/main.go` | 1. 提供 `127.0.0.1:9300` Web 界面与 `/api/status`、`/api/events` (SSE)、`/api/run`、`/api/keploy-mocks` 接口；<br>2. 前端采用纯 `createElement`/`textContent` 安全 DOM 渲染，实时展示拓扑、24B 帧头解剖色块、Protobuf JSON、字段级 Diff 及 Keploy 对比视图。 |
| **M5-T4** | 实机 `cilium/ebpf` 拦截测试与全链路自动化校验 | `tests/ebpf_monitor_test.go`<br>`scripts/start-monitor.sh`<br>`Makefile` | 1. 编写单元测试与 `cilium/ebpf` 实机内核拦截回放测试；<br>2. 验证在真实 `Linux 6.8` 内核下挂载 `cgroup/connect4` 探针，关闭 `Backend` 完成 100% 离线回放与突变字段 Diff 检出。 |

---

## 3. 自动化验收标准 (Acceptance Criteria)

- **AC-M5-01 (内核 eBPF 真实挂载与防回环劫持)**：在当前主机通过 `sudo` 运行测试时，`cilium/ebpf` 成功加载 `BPF_CGROUP_INET4_CONNECT` 程序并挂载至 `/sys/fs/cgroup`；`Server` 连接 `127.0.0.1:9200` 时被内核自动重定向至代理端口，BPF Map 中 `EgressRedirected` 计数准确等于 `4`（对应 `SC-02`、`SC-04`、`SC-04B`、`SC-05`），且监控器自身透传连接 `BypassedSelf` 准确豁免。
- **AC-M5-02 (无 Backend 离线 eBPF 回放与智能 RequestID 重写)**：彻底停止 `Backend` 后，开启 eBPF 离线回放，不仅标准套件 `SC-01 ~ SC-05` 全绿通过，且当客户端使用随机变化的 `RequestID` 发起调用时，Mock 引擎仍能按业务语义命中并动态重写响应帧 `RequestID` 与 `CRC32`。
- **AC-M5-03 (字段级突变 Diff 与 Keploy Base64 解码)**：开启 `discount_bug` 突变时，引擎精准输出 `discount_cents` (`6500 -> 6600`)、`final_amount_cents` (`50310 -> 50204`) 等字段级 Diff；加载 `keploy/keploy/flowbench-e2e/mocks.yaml` 能 100% 逆向解码出 4 条 Mock 的 24B 帧头与 Protobuf 明文。
- **AC-M5-04 (Web 控制台与安全基线)**：Web 服务监听 `127.0.0.1:9300`，全部 API 与 SSE 正常响应，前端无任何 `innerHTML` 不安全调用，测试结束后所有 eBPF 探针与后台进程 100% 清理卸载。
