# flowbench 手工录制监控、动态流量拓扑与单调用自愈重放特性规范

- **文档编号**：`06-interactive-session-recording-and-replay.md`
- **文档版本**：v1.0.0
- **编制日期**：2026-10-28
- **当前阶段**：阶段一：特性定义与验收标准
- **状态**：定稿待审 (Ready for Review)

---

## 1. 业务背景与产品定位 (Context & Value Proposition)

在企业级微服务体系及私有二进制协议排障中，预设的自动化测试套件只能覆盖已知的基准用例。开发者与测试工程师在日常开发联调、线上故障复现或接口验收时，核心心智模型是**“主动录制（User-Driven Recording）与即时探查”**：

1. **用户主导性**：开发者指定正在本地调试的业务服务进程（`TargetPID`）及其下游依赖后端（`Backend`），随后点击**“开始监控”**；
2. **外部真实驱动**：开发者在自己的真实调用端（如业务客户端、命令行工具、单元测试或测试网关）主动发起单次或批量请求，系统全链路捕获流经的真实二进制报文；
3. **会话归档**：操作完成后点击**“结束监控”**，形成一个具有完整业务语义的**“录制会话（Recording Session）”**；
4. **实时流量拓扑与链路遥测**：在调用过程中及会话中，界面展示直观的动态流量拓扑（`Client -> Ingress -> SUT -> Egress -> Backends`），连线上实时展现流速粒子动效、QPS、吞吐与延迟；
5. **单调用原地自愈重放 (Single-Call Replay Studio)**：开发者可以在录制好的会话中任意挑选一笔典型调用，直接在 Web 控制台上发起**单次调用的原地重放**。系统自动向被测服务重放该 Ingress 请求，由内核 eBPF/代理透明拦截下游出站请求并提供自愈 Mock 响应，并与录制基准进行 **字节级 + 结构化字段级（Field-Level Diff）** 实时比对。

---

## 2. 需求功能矩阵 (Feature Matrix)

特性优先级定义：
- **P0 (Must-Have)**：必须实现的基石特性，构成手工录制、事务关联、动态拓扑与单调用自愈重放的闭环。
- **P1 (Should-Have)**：提升开发与排障效率的增强特性。

| 编号 | 特性名称 | 涉及模块 | 优先级 | 功能描述与技术规范 | 验收指标 / 衡量标准 |
| :--- | :--- | :---: | :---: | :--- | :--- |
| **SESSION-01** | 交互式录制会话启停与 TargetPID 动态绑定 | `internal/monitor` | **P0** | 1. 提供 `/api/session/start` 接口：接收选定的 `TargetPID`、监听端口及下游 `Backends` 列表，初始化独立 `SessionID`，激活 eBPF 探针/代理透明拦截通道；<br>2. 外部业务客户端发起真实调用时，透明代理拦截入站和出站流量并写入当前会话事件流；<br>3. 提供 `/api/session/stop` 接口：结束当前会话录制，固化生成用例资产并计算会话级统计指标；<br>4. 提供 `/api/session/current` 接口查询会话状态（`IDLE`, `RECORDING`, `STOPPED`）。 | 1. 点击“开始监控”后，运行 `./bin/flowbench-client -once`，所有入站和出站二进制帧被 100% 捕获至当前会话；<br>2. 会话状态流转准确无误。 |
| **SESSION-02** | 端到端调用事务关联引擎 (Trace Correlator) | `internal/monitor` | **P0** | 将离散的捕获帧按业务调用聚合为**“端到端事务（Transaction Trace）”**：<br>1. 以一次入站请求（`INGRESS_REQ`）为根，根据 `RequestID`（或 CROS-RPC 的 `ullSNO`）以及调用时间窗口，自动关联其触发的所有下游出站请求与响应（`EGRESS_REQ`/`EGRESS_RESP`）以及最终入站响应（`INGRESS_RESP`）；<br>2. 提供 `/api/session/traces` 接口输出结构化 Trace 列表。 | 1. 对于产生下游查询的 `OpOrderQuote` 调用，聚合输出树状 Trace：包含 1 个 Ingress Req、2 个 Egress 子调用和 1 个 Ingress Resp，并计算总耗时。 |
| **SESSION-03** | 动态流量拓扑大图与实时 Telemetry 流动 | `internal/monitor/web` | **P0** | 1. 采用 SVG/CSS 动态拓扑组件替代原有静态文字条，展示 `Caller -> Ingress Hook -> SUT Server -> Egress Interceptor -> Backends`；<br>2. 流量活跃时展示**微粒子流动动画（Flow Animation）**与实时连线 Telemetry（QPS、KB/s、P99 延迟）；<br>3. 支持**点击节点/链路联动过滤**：点击下游 Backend-1 节点，下方调用列表自动联动筛选与其交互的记录。 | 外部发起调用时，拓扑连线实时产生流动动效，数据指标实时更新，点击拓扑节点可平滑过滤下方面板。 |
| **SESSION-04** | 单调用原地自愈重放器 (Single-Call Replay Studio) | `internal/monitor`<br>`internal/monitor/web` | **P0** | 1. 允许在调用事务列表中挑选任意一条调用，切换至【▶️ 原地自愈重放】视图；<br>2. 允许查看、编辑请求 Protobuf JSON（支持 Mutation 注入）；<br>3. 提供 `/api/replay/single` 接口：由 Monitor 作为客户端向 SUT Server 发起该请求，eBPF/代理根据会话录制的 Mock 规则进行出站自动响应（自愈 RequestID 与 CRC32），即使真实下游 Backend 关闭亦能稳定自愈；<br>4. 实时展示 Side-by-Side 响应比对：CRC32 校验、整帧字节匹配度与 Protobuf 字段级 Diff 标红。 | 1. 关闭真实 Backend 后，点击重放某次录制的 `OpOrderQuote`，重放成功率 100%，字段级 Diff 显示完全一致；<br>2. 篡改请求数量时，重放能准确捕获响应字段差异并标红。 |
| **SESSION-05** | 控制台信息架构与双分栏工作台重构 | `internal/monitor/web` | **P0** | 1. **顶部置顶主控条（Recording Master Bar）**：常驻会话状态指示灯（呼吸灯）、录制时长计时器、开始/结束监控大按钮、快捷指标；<br>2. **上方动态拓扑画布**；<br>3. **下半部双分栏布局（38% : 62%）**：左栏为目标配置折叠卡与业务事务树列表；右栏为深度解剖、单调用自愈重放与 Mock 资产编辑器三合一工作台。 | 页面结构紧凑无杂乱长滚动条，交互流畅直观，响应式适配不同分辨率。 |

---

## 3. 非目标与约束边界 (Non-Goals & Constraints)

1. **Non-Goal：非私有二进制协议侵入**：不改变三级微服务和协议栈底层的纯原生 TCP + 24B 大端帧格式，绝不引入 HTTP/REST 或 gRPC 替代业务数据流；
2. **Non-Goal：盲依赖第三方外挂 UI 框架**：控制台继续保持由 Go `embed` 驱动的高性能纯原生 JS/CSS/HTML 单二进制分发，零 NPM 构建包外链，确保离线安全无外网依赖；
3. **约束：单机与跨机端口安全性**：监听与测试默认绑定 `127.0.0.1`，Web 控制台内置 CSP 与严格的安全响应头。
