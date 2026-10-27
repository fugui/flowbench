# 特性规范：多下游 Backend 路由劫持与 Mock 隔离 (Multi-Backend Routing & Mock Isolation)

- **文档编号**：`04-multi-backend-routing.md`
- **文档版本**：v1.0.0
- **编制日期**：2026-10-27
- **归属阶段**：阶段一（特性清单与边界明确）
- **状态**：待评审 (Draft)

---

## 1. 背景与核心价值 (Motivation & Value)

在实际的微服务与分布式基准测试场景中，核心被测服务（SUT）通常具有 **“单入口监听、多下游依赖”** 的典型拓扑结构：
1. **主流场景（单机多进程微服务群）**：被测服务监听一个入口 TCP 端口（如 `127.0.0.1:19100`），在其处理流水线中，通过不同的 TCP 端口并发调用同一台主机上的多个后端微服务进程（例如商品中心 `127.0.0.1:19201`、征信风控 `127.0.0.1:19202`、计费核算 `127.0.0.1:19203`）；
2. **偶发场景（跨机下游微服务）**：其中一两个关键下游服务部署在局域网另一台独立物理机或虚拟机上（例如远端支付网关 `192.168.1.50:19204`）。

当前系统的 eBPF 探针仅支持单个 `(EgressOrigIP, EgressOrigPort)` 的点对点单目标劫持，当被测服务调用多个不同端口或不同 IP 的 Backend 时，无法统一录制出站流量并进行多后端隔离 Mock。

本特性旨在将 `flowbench` 升级为支持 **多下游路由规则表（Multi-Backend Routing Table）** 与 **源地址/端口感知隔离（Orig-Dst Aware Isolation）** 的工业级流量治理平台。

---

## 2. 核心拓扑与流量模型 (Topology)

```mermaid
flowchart TD
    subgraph ClientLayer["客户端入口 (Ingress)"]
        CLI["测试客户端 / 业务流量发生器"]
    end

    subgraph SUTLayer["核心被测服务进程 (SUT, TargetPID)"]
        SUT["被测服务<br/>(监听 127.0.0.1:19100)"]
    end

    subgraph EBPFLayer["原生 eBPF 内核治理层 (cgroup/connect4)"]
        PROBE{{"eBPF 探针多目标匹配与连接追踪<br/>- 匹配目标列表 [(127.0.0.1:19201), (127.0.0.1:19202), (192.168.1.50:19204)]<br/>- 记录 client_src_port -> orig_dst_ip:port 至 BPF Conntrack Map<br/>- 改写目的地为 127.0.0.1:EgressProxyPort"}}
    end

    subgraph ProxyLayer["flowbench Egress 协议感知多路代理"]
        PROXY["Egress Proxy 引擎<br/>- 读取 BPF Conntrack 获取真实目标 (OrigDst)<br/>- 录制态：透明转发至对应真实真实后端<br/>- 回放态：按 (OrigDst, OpCode) 精确命中独立 Mock 资产并自愈"]
    end

    subgraph MultiBackends["多下游 Backend 实例"]
        B1["Backend 1: 商品中心<br/>(127.0.0.1:19201)"]
        B2["Backend 2: 征信风控<br/>(127.0.0.1:19202)"]
        B3["Backend 3: 远端支付<br/>(192.168.1.50:19204)"]
    end

    CLI -->|Ingress 业务请求| SUT
    SUT -->|connect() 下游 1| PROBE
    SUT -->|connect() 下游 2| PROBE
    SUT -->|connect() 下游 3| PROBE
    PROBE -->|透明重定向| PROXY
    PROXY -.->|录制态真实转发| B1
    PROXY -.->|录制态真实转发| B2
    PROXY -.->|录制态真实转发| B3
```

---

## 3. 功能需求矩阵 (FR Matrix)

| 需求编号 | 特性分类 | 功能描述 | 优先级 |
| :--- | :--- | :--- | :--- |
| **FR-MB-01** | **多下游规则定义** | 支持注册任意数量的下游后端地址，规则由 `(Host/IP, Port, Name)` 三元组构成；支持默认本地环回 `127.0.0.1` 及其它指定 IPv4。 | **P0** |
| **FR-MB-02** | **eBPF 多目标白名单匹配** | 内核态 `cgroup/connect4` 字节码支持从 BPF Map（`BPF_MAP_TYPE_HASH`）动态检索目标 `(dest_ip, dest_port)`，若命中且发起者匹配 `TargetPID`，则触发重定向。 | **P0** |
| **FR-MB-03** | **原始目的连接追踪 (OrigDst Tracking)** | 内核重定向发生时，探针自动将 `(client_src_port -> orig_dest_ip:port)` 写入 BPF 会话追踪 Map；用户态代理建连时通过读取该 Map 精准获知客户端原始意图访问的下游后端地址。 | **P0** |
| **FR-MB-04** | **用户态 Fallback 模式支持** | 在无 root / eBPF 权限时，自动启动用户态多端口监听代理（为每个下游分配独立的监听端口），保证功能可用性平滑降级。 | **P1** |
| **FR-MB-05** | **多 Backend Mock 资产隔离** | 资产库索引升级为 `(TargetBackend, OpCode)` 联合主键（例如 `127.0.0.1:19201#0x0101` 与 `127.0.0.1:19202#0x0201` 彼此隔离），彻底消除不同下游系统间可能存在的 OpCode 冲突。 | **P0** |
| **FR-MB-06** | **多 Backend 离线自愈回放** | 在断开所有真实下游（B1/B2/B3）时，被测服务发出的各个独立连接均能被 Egress 代理正确识别其目标 Backend，精准命中各自的 Mock 资产，并完成动态 RequestID 与 CRC 校验自愈。 | **P0** |
| **FR-MB-07** | **Web 控制台与 API 多下游管理** | 1. 进程附着弹窗支持动态添加/删除多个下游端口及远端 IP；<br>2. 流量解剖列表显式展示 `Target Backend` 标签；<br>3. Mock 在线编辑区按 Backend 分组展示。 | **P1** |

---

## 4. 边界与非目标 (Non-Goals)

1. **Non-Goal 1（非全量透明黑盒抓包）**：不实施全局无差别全端口流量劫持，必须严格基于用户指定的 `TargetPID` 及下游规则表进行精准过滤，确保宿主机上无关进程网络 100% 豁免。
2. **Non-Goal 2（不依赖 iptables / NAT）**：禁止引入 `iptables` / `nftables`，必须纯基于 `cilium/ebpf` `cgroup/connect4` 在 Socket 层修改 `struct bpf_sock_addr` 实现无锁高性能重定向。
3. **Non-Goal 3（IPv6 支持暂不包含）**：本期聚焦 IPv4（同机环回 `127.0.0.1` 及跨机 IPv4），IPv6 扩展作为未来版本规划。

---

## 5. 验收标准 (Acceptance Criteria, AC)

- **AC-1 (多端口并发拦截与追踪)**：被测服务同时向 `127.0.0.1:19201`、`127.0.0.1:19202` 发起请求，eBPF 探针 100% 拦截并重定向至 Egress 代理，代理通过 Conntrack Map 准确识别出各自原本的目标端口。
- **AC-2 (跨机远端 IPv4 拦截)**：被测服务向局域网 IP `192.168.1.50:19204` 发起请求，探针将其 `user_ip4` 改写为 `127.0.0.1` 并重定向至本地代理，且精准记录远端目标。
- **AC-3 (Mock 隔离与自愈回放)**：在关闭全部真实后端后，回放测试中不同后端即便具有相同或重叠的 `OpCode`，Mock 引擎仍能按 `(TargetBackend, OpCode)` 精准分流回放，断言通过率 100%。
- **AC-4 (零残留与向后兼容)**：现有三级微服务回归套件（`./scripts/verify-e2e.sh`）保持 100% 兼容通过；退出时自动清理 BPF Map 与所有后台连接。
