# ADR-02: 引入复合多级二进制协议抽象并内置 DOPHI CROS RPC 编解码器

- **文档编号**：`02-composite-binary-protocol-cros-rpc.md`
- **日期**：2026-10-28
- **状态**：已采纳 (Accepted)
- **关联文档**：[`docs/05-adr/rpc.md`](file:///home/fugui/flowbench/docs/05-adr/rpc.md)、[`docs/01-features/03-universal-protobuf-platform.md`](file:///home/fugui/flowbench/docs/01-features/03-universal-protobuf-platform.md)

---

## 1. 背景与问题陈述 (Context & Problem Statement)

在 `flowbench` 演进为通用私有 Protobuf 协议监听与自愈回放工具平台（Milestone 6 ~ Milestone 7）后，系统已支持通过声明式 DSL（`FramingSpec`）配置任意 4B~128B 的定长二进制帧头。

然而，企业级真实生产环境中存在大量高复杂度的私有 RPC 协议。以 DOPHI 平台提供的 **CROS RPC 协议**（详见 [`docs/05-adr/rpc.md`](file:///home/fugui/flowbench/docs/05-adr/rpc.md)）为例，其在线路上采用**「公共消息头(24B) + 会话头(200~252B) + 扩展消息头(0~N个) + Protobuf序列化载荷」**的多级嵌套结构。

该类复合协议对现有平台带来以下挑战：
1. **帧头长度动态变化**：会话头因请求/应答类型不同而长度不同（Req 约 252B，Rsp 约 200B），且后接变长扩展头链表，整体头部长度 `usHeadLen` 并非定长常数，现有假定 `MinHeaderBytes == PayloadOffset` 的定长 DSL 无法直接表达；
2. **复合 8 字节魔数识别**：魔数字段为 7 字节字符串 `"CSE.TCP"` 加 1 字节传输底座标识，现有 1/2/4 字节整数魔数检查无法覆盖；
3. **RPC 契约寻址维度差异**：现有模型依赖整型 `OpCode`，而 CROS RPC 依赖会话头中的 `ascServiceName`（64B 字符串）与 `ascOperation`（64B 字符串），其与 Protobuf 的 `Service` 和 `Method` 天然对齐，需要支持基于字符串 RPC 契约的自动 Schema 反射绑定；
4. **回放自愈需求**：离线录制回放时，需具备对 8 字节请求序列号 `ullSNO` 进行 $O(1)$ 原地自愈重写的能力。

---

## 2. 决策方案 (Decisions)

### 决策 1：将 CROS RPC 作为平台内置首选复合协议类型（Built-in Composite Protocol）
- 在 `internal/codec/cros/` 独立建立高性能纯 Go 编解码器包，提供无外部 C 依赖的帧解析、流式状态机、拆粘包保护与魔数跳跃定位；
- 在平台预置模板中注册 `PresetID: "cros-rpc-v1"`，使用户无需手动配置繁琐的偏移量即可开箱即用；
- 平台控制台与回放引擎提供对 CROS RPC 会话头元数据（`ServiceName`、`Operation`、`StatusCode`、`TraceID`、`MsgType`）的原生深度解剖与展示。

### 决策 2：采用三阶段流式拆包状态机与零拷贝载荷提取
1. **阶段 1：读取并校验 24 字节公共头 (`RPC_COMM_HEAD_S`)**
   - 匹配 8 字节魔数特征（前 7 字节 `"CSE.TCP"`，第 8 字节为合法底座如 TCP/Mesh/Domain）；若遇到流中噪声，支持逐字节滑动窗口定位有效魔数；
   - 提取 `ullSNO`（序列号）、`uiLength`（总数据长）、`usHeadLen`（头部总长）、`ucMsgType`（消息类型）。
2. **阶段 2：校验并读取头部余量 (`usHeadLen`)**
   - 读取并拆解会话头（`RPC_REQ_HEAD_S` 或 `RPC_RSP_HEAD_S`），提取服务名、操作名、状态码与 Trace；
   - 若存在扩展头（`usHeadLen > sizeof(SessionHead)`），循环拆解扩展公共头 (4B) 及对应内容（TraceKey / TimeStamp）。
3. **阶段 3：读取 Protobuf 序列化 Payload (`uiLength - usHeadLen`)**
   - 直接定位并提取 Protobuf 字节流，无缝对接 `schema.DynamicRegistry` 动态反射引擎。

### 决策 3：基于 `ServiceName.Operation` 的零配置 Protobuf Schema 自动关联
- 扩展动态反射引擎：当协议为 CROS RPC 时，优先以 `ascServiceName + "." + ascOperation` 作为契约标识，在已导入的 `.proto` 文件中自动检索对应的 Input/Output Protobuf 描述符，免除人工维护数字 OpCode 映射字典的负担。

### 决策 4：极速原地自愈重写 (O(1) In-Place SeqID Self-Healing)
- CROS 协议中序列号 `ullSNO` 位于公共头固定偏移量 `[8:16]`（8 字节大端整数），且该协议帧无强制帧头 CRC32；
- 在出站 Mock 劫持与自愈回放中，提供 `RebuildFrameSeqID`，仅需修改 8 个字节的切片内容，实现微秒级零内存分配的高性能序列号自愈。

---

## 3. 架构收益 (Benefits & Consequences)

- **收益 1**：平台通用性大幅跃升，具备了处理工业级多级嵌套复合二进制协议的标准范式；
- **收益 2**：对 DOPHI CROS RPC 的用户提供了完全开箱即用的流量监听、Protobuf 明文审查与无依赖离线回放能力；
- **收益 3**：保持架构清晰性，既不污染简单的定长 DSL，又通过标准适配器与平台统一事件模型（`CapturedFrameEvent`）平滑汇合。
