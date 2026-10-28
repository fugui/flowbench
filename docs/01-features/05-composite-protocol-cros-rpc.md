# flowbench 复合多级二进制协议与内置 CROS RPC 特性规范 (Composite Protocol & CROS RPC Specification)

- **文档编号**：`05-composite-protocol-cros-rpc.md`
- **文档版本**：v1.0.0
- **编制日期**：2026-10-28
- **当前阶段**：阶段一：特性清单与边界明确
- **状态**：已定稿 (Finalized)

---

## 1. 特性背景与平台定位 (Vision & Scope)

在支持定长帧头（`FramingSpec`）的基础之上，`flowbench` 正式引入**工业级复合多级二进制协议（Composite Hierarchical Framing）**支持。作为首个内置代表性实现，平台完整内嵌对 **DOPHI CROS RPC 协议**的全面协议感知、流式拆粘包、字段级深度解剖与自愈重放支持。

### 1.1 核心需求与业务目标 (Goals)
1. **多级复合帧头拆包与粘包/半包防御**：
   - 完整支持 CROS RPC「公共消息头 (24B) + 会话头 (Req 252B / Rsp 200B) + 扩展消息头 (0~N个) + Protobuf 载荷」四层物理布局；
   - 具备单字节极端半包读取、密集粘包拆解、以及流中存在脏数据时的 8 字节魔数（`"CSE.TCP"` + Transport）滑动窗口自动同步定位能力。
2. **字符串 RPC 契约与 Protobuf 动态反射自动绑定**：
   - 支持从请求与响应会话头中精确提取 `ascServiceName`（服务名）与 `ascOperation`（API 方法名）；
   - 在加载 `.proto` 文件后，支持根据 `ServiceName.Operation` 自动关联对应的 Protobuf 请求与响应消息结构，无需手动配置数字 OpCode 映射。
3. **极速原地序列号自愈重写 (Zero-Allocation SeqID Self-Healing)**：
   - 离线回放与出站 Mock 劫持场景下，支持对帧头 `ullSNO`（8 字节大端整数）进行原地 $O(1)$ 快速自愈更新，确保客户端请求能正确匹配 Mock 响应。
4. **统一平台集成与 Web 深度解剖可视化**：
   - 在平台 Web 控制台与 API 接口中注册内置预设 `PresetID: "cros-rpc-v1"`；
   - 在流量事件模型（`CapturedFrameEvent`）与解剖结构（`DecodedHeaderInfo`）中暴露服务名、方法名、状态码、TraceID 与会话头详情。

### 1.2 非目标 (Non-Goals)
1. **不引入 C/C++ 动态链接库或 DOPHI SDK 源码**：所有编解码、状态机、反射与序列号自愈完全基于纯 Go 原生实现，保证单二进制零依赖分发；
2. **不承担 CROS 管理帧加密/TLS 握手协商代理**：本里程碑重点覆盖明文 TCP 传输底座（`RPC_TRANSPORT_TCP`）上的业务 RPC 请求（`RPC_MSG_TYPE_REQ`）与应答（`RPC_MSG_TYPE_RSP`）。

---

## 2. 特性需求矩阵 (Feature Matrix)

| 功能编号 | 特性模块 | 详细说明 | 验收标准 (Acceptance Criteria) |
| :--- | :--- | :--- | :--- |
| **FEAT-CROS-01** | **流式拆粘包状态机** | 实现 `cros.ReadFrame(r io.Reader)` 状态机，严格按照公共头 `uiLength` 与 `usHeadLen` 计算并消费完整帧。 | 经受单字节流切片（`fragReader`）、密集粘包（10帧连读）及 32MB 帧长防 OOM 校验。 |
| **FEAT-CROS-02** | **魔数扫描自愈同步** | 报文起始若出现无效垃圾字节，自动逐字节滑动查找 `"CSE.TCP\x01"` 等合法 8 字节魔数特征。 | 垃圾字节前缀被正确剔除并成功解析后续有效帧，错误计数可追溯。 |
| **FEAT-CROS-03** | **会话头解剖** | 区分 `RPC_MSG_TYPE_REQ` 与 `RPC_MSG_TYPE_RSP`，提取 `ServiceName`、`Operation`、`StatusCode`、`TraceBuff` 等。 | 正确解出 64 字节 ASCII 字符串字段（截断 `\0` 填充），数值字段转换大端字节序。 |
| **FEAT-CROS-04** | **扩展消息头链表解析** | 支持解析 0 到多个 4 字节扩展公共头（`usHeadLen` + `ucType`），提取 TraceKey 等内容。 | 扩展头内容被完整保存在帧数据结构中，支持向后兼容。 |
| **FEAT-CROS-05** | **零拷贝 Payload 提取与反射** | 从帧偏移 `24 + usHeadLen` 处提取 Protobuf 载荷（长度 `uiLength - usHeadLen`），传给动态反射器。 | Protobuf 二进制流在加载 schema 后正确转为可读 JSON。 |
| **FEAT-CROS-06** | **$O(1)$ 序列号原地自愈** | 提供 `RebuildFrameSeqID(raw []byte, newSNO uint64)` 原地直接修改 `raw[8:16]`。 | 内存零额外分配，仅修改 8 字节大端整数，性能压测下微秒级响应。 |
| **FEAT-CROS-07** | **控制台预置与统一解剖展示** | 在 Web 控制台预置模板下拉框中提供 `cros-rpc-v1`，事件流中高亮展示服务名与操作名。 | 控制台无需手动配置 DSL 偏移量，一键切换即刻完成 CROS 报文解剖。 |
