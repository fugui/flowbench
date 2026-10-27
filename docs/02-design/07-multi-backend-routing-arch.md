# 架构设计：多下游 Backend 路由劫持与 Mock 隔离设计 (Multi-Backend Routing Architecture)

- **文档编号**：`07-multi-backend-routing-arch.md`
- **文档版本**：v1.0.0
- **编制日期**：2026-10-27
- **归属阶段**：阶段二（系统架构设计）
- **状态**：待评审 (Draft)

---

## 1. 总体设计思想与架构分层

为了支持被测服务（SUT）同时访问同一宿主机上的多个不同端口（如 `127.0.0.1:19201`, `127.0.0.1:19202`）以及少量跨主机的远端微服务（如 `192.168.1.50:19204`），系统采用 **「内核多目标 HASH 映射表 + 独立代理端口精准分流 + 联合键 Mock 隔离」** 架构。

```mermaid
flowchart TD
    subgraph Host["宿主机 (Host / Container)"]
        subgraph SUTSpace["核心被测进程 (TargetPID)"]
            SUT["SUT Server<br/>(监听 127.0.0.1:19100)"]
        end

        subgraph Kernel["Linux 内核层 (cilium/ebpf)"]
            PROG["cgroup/connect4 探针<br/>TargetPID 校验 & BPF Hash Map 查找"]
            MAP[("egress_targets (BPF_MAP_TYPE_HASH)<br/>Key: (DestIP, DestPort)<br/>Val: (ProxyPort)")]
        end

        subgraph Monitor["flowbench-monitor 通用平台进程"]
            ROUTER["Multi-Backend 路由分发器"]
            P1["Egress Proxy 1 (127.0.0.1:P1)<br/>对应 127.0.0.1:19201"]
            P2["Egress Proxy 2 (127.0.0.1:P2)<br/>对应 127.0.0.1:19202"]
            P3["Egress Proxy 3 (127.0.0.1:P3)<br/>对应 192.168.1.50:19204"]
            ASSETS[("Mock 资产库<br/>Key: (BackendAddr, OpCode)")]
        end

        subgraph Backends["真实下游服务"]
            B1["Backend 1: 商品微服务 (127.0.0.1:19201)"]
            B2["Backend 2: 风控微服务 (127.0.0.1:19202)"]
        end
    end

    subgraph Remote["远端物理机 / 虚拟机"]
        B3["Backend 3: 支付微服务 (192.168.1.50:19204)"]
    end

    SUT -->|connect(127.0.0.1:19201)| PROG
    SUT -->|connect(127.0.0.1:19202)| PROG
    SUT -->|connect(192.168.1.50:19204)| PROG

    PROG <-->|Map 查询| MAP
    PROG -->|重定向至 127.0.0.1:P1| P1
    PROG -->|重定向至 127.0.0.1:P2| P2
    PROG -->|重定向至 127.0.0.1:P3| P3

    P1 -.->|录制态转发| B1
    P2 -.->|录制态转发| B2
    P3 -.->|录制态转发| B3

    P1 <==>|回放态 Mock 匹配| ASSETS
    P2 <==>|回放态 Mock 匹配| ASSETS
    P3 <==>|回放态 Mock 匹配| ASSETS
```

---

## 2. 内核态 eBPF 探针机制 (`cgroup/connect4`)

### 2.1 为什么采用「独立代理端口分流 (Per-Target Proxy Port)」设计？
在 Linux 内核执行 `cgroup/connect4` 钩子时，`connect()` 系统调用刚刚进入，TCP 协议栈**尚未分配**客户端本地短暂端口（Ephemeral Source Port），且此时没有 netfilter 的 `SO_ORIGINAL_DST` 支持。
因此，若将所有不同下游连接全部混杂重定向到同一个代理端口，代理层无法无锁、高可靠地判断进入的 Socket 究竟原本是发往哪个 Backend。

**本方案设计**：
通用监控平台为每一个被监控注册的下游 Backend 分配一个专用的本地回环代理端口：
- Backend 1 (`127.0.0.1:19201`) $\to$ Proxy 1 (`127.0.0.1:P1`)
- Backend 2 (`127.0.0.1:19202`) $\to$ Proxy 2 (`127.0.0.1:P2`)
- Backend 3 (`192.168.1.50:19204`) $\to$ Proxy 3 (`127.0.0.1:P3`)

### 2.2 BPF Map 结构定义
使用 `BPF_MAP_TYPE_HASH` 存储下游规则表：
```c
struct egress_target_key {
    __u32 dest_ip;    // 目标 IPv4 网络字节序 (127.0.0.1 或 192.168.1.50)
    __u16 dest_port;  // 目标端口网络字节序 (如 19201)
    __u16 pad;
};

struct egress_target_val {
    __u16 proxy_port; // 重定向目标代理端口网络字节序
    __u16 pad;
};
```

### 2.3 `cgroup/connect4` 汇编执行流程
1. 读取调用方 TGID（`bpf_get_current_pid_tgid >> 32`）：
   - 若 `tgid == MonitorPID`：直接放行（豁免自循环）；
   - 若配置了 `TargetPID > 0` 且 `tgid != TargetPID`：跳过出站检查（不干扰系统中无关进程）。
2. 在栈上构造 `egress_target_key = (ctx->user_ip4, ctx->user_port)`，调用 `bpf_map_lookup_elem(&egress_targets, &key)`：
   - **若命中**：
     - 将 `ctx->user_ip4` 改写为 `127.0.0.1`（网络字节序 `0x0100007f`）；
     - 将 `ctx->user_port` 改写为 `val->proxy_port`；
     - 原子递增 `StatEgressRedirected` 统计计数；
     - 返回 `1` 放行连接。
   - **若未命中**：继续走 Ingress 端口检查或默认放行。

---

## 3. 用户态代理与 Mock 引擎分层设计

### 3.1 `MultiBackendRouter` 结构
在 `internal/monitor/platform.go` 与 `engine.go` 中抽象出多下游路由管理器：
```go
type BackendEndpoint struct {
    Name        string `json:"name"`        // 逻辑名称，如 "order-db" / "risk-engine"
    Host        string `json:"host"`        // 真实目标 IP，如 "127.0.0.1" 或 "192.168.1.50"
    Port        uint16 `json:"port"`        // 真实目标端口，如 19201
    ProxyPort   uint16 `json:"proxy_port"`  // 分配的本地回环代理端口
    Active      bool   `json:"active"`      // 是否激活拦截
}
```

### 3.2 录制态流量处理
当被测进程发起出站请求连接到 `127.0.0.1:ProxyPort_i` 时：
1. 代理实例根据监听端口即刻判定其原始目标为 `Host_i:Port_i`；
2. 代理向真实的 `Host_i:Port_i` 建立透传连接，实施全双工流式转发；
3. 将出站请求帧（`SubReq`）与出站响应帧（`SubResp`）解剖解析，并自动打上 `BackendAddr = "Host_i:Port_i"` 与 `BackendName = Name_i` 标签；
4. 归档至 Mock 资产库。

### 3.3 回放态 Mock 联合键匹配与自愈
在断开真实后端进行离线回放时：
- Mock 匹配规则由单一 `OpCode` 升级为 **`(TargetBackend, OpCode)` 联合索引**；
- 当 `Proxy 1` 收到请求时，仅在绑定了 `127.0.0.1:19201` 的 Mock 资产集合中检索匹配项；
- 彻底避免微服务体系中不同团队开发的下游后端使用相同/重叠 `OpCode` 导致的冲突；
- 命中 Mock 资产后，动态提取被测进程最新发出的 `RequestID`，填入 Mock 二进制载荷并重新计算 CRC32/Adler32，完成校验和自愈。

---

## 4. RESTful API 与 Web 控制台交互规范

### 4.1 新增/更新 API
- `POST /api/backends/add`：动态注册新的下游 Backend（分配代理端口并下发 BPF Hash Map）；
- `POST /api/backends/remove`：动态注销指定下游 Backend；
- `GET /api/backends`：获取当前已注册的下游列表及其代理状态与连接数统计；
- `POST /api/attach` 升级：请求体中支持传入 `egress_backends: []BackendEndpoint` 数组。

### 4.2 Web 交互升级
1. **进程附着面板**：支持通过表格动态「+ 添加下游依赖」，配置 `[名称] [IP/主机名] [端口]`；
2. **多流列表标签**：在流量列表中显式显示色彩区分的 Backend 徽章（如 `Backend: 127.0.0.1:19201`、`Backend: 192.168.1.50:19204`）；
3. **Mock 资产分类视图**：在线编辑 Mock 区域按 Backend 进行 Tab 分页或分组折叠展示。
