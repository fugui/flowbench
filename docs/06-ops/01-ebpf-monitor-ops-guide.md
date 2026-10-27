# flowbench 原生 eBPF 协议感知监控与流量治理实操指南

- **文档编号**：`01-ebpf-monitor-ops-guide.md`
- **归属阶段**：阶段四：运维交付与系统操作手册
- **被测系统**：`flowbench-server` (SUT) / `flowbench-backend` (下游依赖) / `flowbench-client` (流量驱动)
- **治理引擎**：`flowbench-monitor` (基于 `cilium/ebpf` 与协议感知智能回放引擎)

---

## 1. 系统概述与操作目标

`flowbench` 抛弃了对外部通用录制工具（如仅支持 HTTP 入站代理且视 TCP 为盲二进制的 Keploy）的依赖，全面采用基于原生 `cilium/ebpf` 的内核态 `cgroup/connect4` 透明重定向探针与自定义协议感知解剖回放引擎。

本指南提供以下核心操作的标准操作程序 (SOP)：
1. 编译构建全量子系统；
2. 三端微服务独立本地联调与全链路 E2E 验证；
3. 以 `root/CAP_BPF` 权限拉起原生 eBPF 协议感知监控与实时 Web 控制台；
4. 运行全链路录制、无 Backend 离线自愈回放、突变拦截与字段级 Diff 观测。

---

## 2. 环境准备与依赖检查

### 2.1 依赖要求
* **操作系统**：Linux (内核版本 >= 5.8，推荐 5.15+ 或 6.x)；
* **Go 环境**：Go 1.25+；
* **权限要求**：
  * 普通微服务 (`client`, `server`, `backend`)：普通非 root 用户权限；
  * 内核 eBPF 探针 (`flowbench-monitor`)：需要 `sudo` 或具备 `CAP_BPF` + `CAP_NET_ADMIN` 特权。

### 2.2 验证环境
```bash
# 检查 Go 版本
go version

# 检查当前内核版本
uname -r

# 编译子系统
make build
```

---

## 3. 标准操作程序 (SOP)

### SOP-01: 三端全链路 E2E 场景自动化验证
运行集成测试套件，在普通用户权限下一次性验证 6 大核心业务场景（Ping/Pong、计价、风控降级、信用评估、前置拦截、长短连接复用）以及 TCP 粘包/分片边界：

```bash
make verify-e2e
```

**预期输出**：
* 编译 `backend`、`server`、`client`；
* 后台启动 `backend` (`127.0.0.1:19200`) 与 `server` (`127.0.0.1:19100`)；
* 运行 Client 短连接套件、长连接套件与粘包分片压测全部 PASS；
* 自动安全回收所有后台进程。

---

### SOP-02: 启动原生 eBPF 监控与 Web 可视化控制台
使用特权权限拉起内核探针与 Web 控制台：

```bash
make monitor
# 或直接执行:
# sudo ./scripts/start-monitor.sh 127.0.0.1:9300
```

打开浏览器访问：`http://127.0.0.1:9300`

**控制台核心功能面板**：
1. **实时微服务与内核探针拓扑面板**：
   * 展示 `Client`、`Server (:9100)`、`cilium/ebpf Hook`、`Backend (:9200)` 节点的在线与拦截状态；
   * 实时展示 eBPF Program ID、累计重定向拦截次数与 Mock 命中率。
2. **实时帧流监控与 24B 帧头可视化解剖抽屉**：
   * 基于 SSE (`/api/events`) 实时滚动显示捕获的每一个二进制帧；
   * 点击任意行展开 24B 帧头可视化色块解析、Protobuf JSON 明文及原始十六进制/Base64 编解码。
3. **Web 一键实验操控台**：
   * **🟢 一键全链路录制 (`Record Suite`)**：录制入站用例与出站 Mock 资产并落盘至 `data/native-ebpf-assets.json`；
   * **🔵 一键无 Backend 离线回放 (`Offline eBPF Replay`)**：自动关闭 Backend，验证内核透明劫持与 `RequestID` 动态重写 + `CRC32` 自愈；
   * **🔴 一键故障突变检测 (`Mutation Test: discount_bug`)**：注入折扣多扣 100 分缺陷，直观展示字段级 Diff 红色告警；
   * **🟡 一键非确定性对照 (`Non-Deterministic Check`)**：注入动态时间戳，直观展示摘要差异。
