# CROS RPC 协议包格式说明

## 1. 概述

CROS RPC 是 DOPHI 平台提供的远程过程调用框架，支持多种通信底座（TCP、Mesh、Domain、DMS、UC、FRPC 等）。无论使用哪种底座，**RPC 消息在网络上的逻辑格式是统一的**，由「公共消息头 + 会话头 + 扩展消息头 + 序列化载荷」组成；不同底座仅在魔术字的协议标识字节与底层链路帧封装上有差异。

源码 `cros_rpccore.ph` 中对网络消息格式的明确定义如下：

```
当前RPC请求、应答消息的网络消息格式：其中扩展消息头可能有0个或多个
|公共消息头|请求or应答消息头|扩展消息公共头1|扩展消息头1| ... |扩展消息公共头n|扩展消息头n|RPC参数序列化payload|
```

即一个完整的网络 RPC 报文布局为：

```
┌──────────────┬──────────────────┬───────────────────────────────┬──────────────────┐
│ RPC_COMM_HEAD │  Session Head    │  Extend Head 1 .. Extend Head N │  Serialized      │
│ (公共消息头)   │ (Req/Rsp/Mng头)  │  (每段=公共头4B+内容)            │  Payload         │
└──────────────┴──────────────────┴───────────────────────────────┴──────────────────┘
```

说明：
- **公共消息头** `RPC_COMM_HEAD_S` 固定存在，含魔术字、序列号、长度等，用于分帧与校验。
- **会话头** 三选一：请求头 `RPC_REQ_HEAD_S`、应答头 `RPC_RSP_HEAD_S`、管理消息头 `RPC_MNG_HEAD_S`，由公共头中的 `ucMsgType` 决定。
- **扩展消息头** 0 个或多个，每段由 4 字节扩展公共头 + 对应内容组成，用于携带调用链、时间戳等可选信息。
- **Payload** RPC 接口参数经 IDL 序列化（Protobuf / FlatCC / Simpo）后的字节流。

> 注意：源码中的 `RPC_MSG_HEAD_S` 是**内存中的逻辑表示**，并非线上格式。发送时会把其字段拆解拷贝成上述线性报文；接收时再由 `RPC_CopyRpcMsg` 重组回 `RPC_MSG_HEAD_S`。

---

## 2. 字节序与对齐

- **字节序**：报文中多字节整数字段（`ullSNO`、`uiLength`、`usHeadLen` 及扩展头内的 `usHeadLen` 等）统一采用**大端序（网络字节序）**。源码通过 `rpcMsgInvert16/32/64` 在主机序与大端序间转换；若主机本身为大端则原样保留。
- **魔术字字段** `unMagicInfo.ullProtocolID`（8 字节）作为整体 64 位值比较，不单独做字节序转换。
- **结构体对齐**：消息头结构体使用 `#pragma pack(4)` 4 字节对齐定义。
- **扩展头对齐**：扩展消息头内容需 8 字节对齐，只能往后新增字段，接收侧做兼容性拷贝。

---

## 3. 公共消息头 RPC_COMM_HEAD_S

公共消息头是每个 RPC 报文的起始定长部分，承担分帧、魔术字校验与消息类型识别职责。

```
偏移   长度   字段                类型        说明
0      8      unMagicInfo        union       魔术字+协议标识（见 3.1）
8      8      ullSNO             UINT64      RPC 消息请求序列号（大端）
16     4      uiLength           UINT32      数据总大小 = 会话头+扩展头+payload，不含本公共头（大端）
20     2      usHeadLen          UINT16      RPC 头部长度 = 会话头+扩展头总长（大端）
22     1      ucMsgType          UINT8       消息类型，见 RPC_MSG_TYPE_E（第 4 节）
23     1      isPacked           UINT8       payload 是否已序列化（0=未序列化，1=已序列化）
```

合计 **24 字节**。`RPC_COMM_HEAD_LEN = sizeof(RPC_COMM_HEAD_S)`。

长度字段关系：
- `uiLength = usHeadLen + payloadLen`（即 `uiLength` 不含公共头自身）
- 接收侧校验：`usHeadLen <= uiLength <= RPC_PAYLOAD_MAX_LEN + usHeadLen`
- `RPC_PAYLOAD_MAX_LEN = 32 * 1024 * 1024 - 4096`（约 32MB - 4KB）

### 3.1 魔术字与协议标识 unMagicInfo

```
偏移  长度  字段        说明
0     7    aucMagic    固定字符串 "CSE.TCP"（ASCII 0x43 53 45 2E 54 50 43），继承自 gRPC 互通要求
7     1    ucProtocol  底层通信协议标识，取值见 RPC_TRANSPORT_TYPE_E（第 5 节）
```

这 8 字节同时以 `ullProtocolID`（UINT64）形式整体比较，用于在收包时快速定位报文起始位置。接收侧 `rpcTcpMsgMagicCheck` 会在缓存中扫描该 8 字节特征值，跳过其前的无效数据。

---

## 4. 消息类型 RPC_MSG_TYPE_E

公共头 `ucMsgType` 字段取值，决定会话头的解释方式：

| 值 | 枚举名                       | 含义                                               | 会话头类型       |
|----|------------------------------|----------------------------------------------------|------------------|
| 2  | RPC_MSG_TYPE_REQ             | 请求消息                                           | RPC_REQ_HEAD_S   |
| 3  | RPC_MSG_TYPE_RSP             | 应答消息                                           | RPC_RSP_HEAD_S   |
| 4  | RPC_MSG_TYPE_RSP_SEG         | 应答消息片段（多次响应场景）                       | RPC_RSP_HEAD_S   |
| 5  | RPC_MSG_TYPE_MNG             | 管理消息（鉴权、机密性/完整性协商）                | RPC_MNG_HEAD_S   |
| 6  | RPC_MSG_TYPE_MNG_REQ         | 服务鉴权请求消息                                   | RPC_MNG_HEAD_S   |
| 7  | RPC_MSG_TYPE_UUID_REQ        | UUID 请求消息                                      | RPC_MNG_HEAD_S   |
| 8  | RPC_MSG_TYPE_TLS_REQ         | TLS 请求消息                                       | —                |
| 9  | RPC_MSG_TYPE_TLS_RSP         | TLS 应答消息                                       | —                |
| 10 | RPC_MSG_TYPE_TLS_RSP_SEG     | TLS 应答消息片段                                   | —                |
| 11 | RPC_MSG_TYPE_FRPC_REQ        | FRPC（共享内存快速 RPC）通知请求                   | —                |
| 12 | RPC_MSG_TYPE_FRPC_DEL        | 通知对端清理 FRPC 资源                             | —                |
| 13 | RPC_MSG_TYPE_DIRECT_CHAN_CONN_REQ | 直通通道创建请求                              | —                |
| 14 | RPC_MSG_TYPE_DIRECT_CHAN_CONN_RSP | 直通通道创建应答                              | —                |

合法取值范围为 `[2, RPC_MSG_TYPE_MNG_BUTT)`，超出范围则丢弃报文并清空接收缓存。

---

## 5. 传输协议标识 RPC_TRANSPORT_TYPE_E

公共头魔术字段的 `ucProtocol` 字节取值：

| 值 | 枚举名                   | 通信底座       |
|----|--------------------------|----------------|
| 0  | RPC_TRANSPORT_DEFAULT    | 默认           |
| 1  | RPC_TRANSPORT_TCP        | TCP            |
| 2  | RPC_TRANSPORT_DMS        | DMS            |
| 3  | RPC_TRANSPORT_UC         | UC             |
| 4  | RPC_TRANSPORT_MESH       | Mesh           |
| 5  | RPC_TRANSPORT_DOMAIN     | Unix Domain    |
| 6  | RPC_TRANSPORT_FRPC       | 共享内存 FRPC  |
| 7  | RPC_TRANSPORT_CUSTOMIZE  | 自定义协议栈   |

> TCP 与 Mesh/Domain 等底座使用同一套 RPC 报文格式，仅 `ucProtocol` 字节不同；Mesh 底座通过注册的魔术字检查钩子（`MESH_TCP_MSG_MAGIC_CHECK_HOOK`）完成同样的分帧定位。

---

## 6. 会话头（Session Head）

会话头紧跟公共头，类型由 `ucMsgType` 决定，三者以 union `RPC_SESSION_HEAD_U` 描述。会话头长度计入公共头的 `usHeadLen`。

### 6.1 请求头 RPC_REQ_HEAD_S

用于 `ucMsgType = RPC_MSG_TYPE_REQ` 的消息。

| 字段                 | 类型     | 说明                                            |
|----------------------|----------|-------------------------------------------------|
| usMsgStructVer       | UINT16   | 消息结构版本号（见第 8 节）                     |
| usReqHeadLen         | UINT16   | Req 头长度（本结构体大小）                      |
| uiDst2ndAddrId       | UINT32   | 二级寻址 ID                                     |
| ascServiceName       | CHAR[64] | 目的服务名（`RPC_SERVICE_NAME_LEN=64`）         |
| ascOperation         | CHAR[64] | API 名称（`RPC_OPERATION_LEN=64`）              |
| ucReqType            | UINT8    | 请求类型：1=同步 2=异步 3=Oneway（RPC_REQ_MSG_TYPE_E） |
| ucSubjectType        | UINT8    | 主体标识类型 CROS_SUBJECT_TYPE_E                |
| usRpcVer             | UINT16   | RPC 版本校验，预留                              |
| uiSessionId          | UINT32   | FRPC 会话 ID                                    |
| aucTraceBuff         | UCHAR[16]| Trace 缓冲（`RPC_MSG_TRACE_BYTE_NUM=16`）       |
| ullIdempotentSeq     | UINT64   | 幂等序号（V2 引入）                             |
| unSubjectName        | union    | 主体名称/客户端身份：CHAR[64] 或 UINT64         |
| ullClientUuid        | UINT64   | 客户端标识（V4 引入）                           |
| usUuidTimeoutS       | UINT16   | UUID 老化时间，单位秒（V4 引入）                |
| ucReserved[2]        | UINT8[2] | 预留                                            |
| uiTimeOutSecondLimit | UINT32   | 超时限制，毫秒（V5 引入）                       |
| ullSendTimeStamp     | UINT64   | 发送时间戳（V5 引入）                           |

### 6.2 应答头 RPC_RSP_HEAD_S

用于 `ucMsgType = RPC_MSG_TYPE_RSP / RPC_MSG_TYPE_RSP_SEG` 的消息。

| 字段              | 类型     | 说明                                       |
|-------------------|----------|--------------------------------------------|
| usMsgStructVer    | UINT16   | 消息结构版本号（见第 8 节）                |
| usRspHeadLen      | UINT16   | Rsp 头长度（本结构体大小）                 |
| uiStatusCode      | UINT32   | 状态返回码（32 位）                        |
| aucTraceBuff      | UCHAR[16]| Trace 缓冲                                 |
| uiSvrInstId       | UINT32   | 服务实例 ID（V2 引入）                     |
| uiReserved        | UINT32   | 预留                                       |
| ullClientSendTime | UINT64   | 客户端发送时间（V3 可定位性增强引入）      |
| ullServerRecvTime | UINT64   | 服务端接收时间                             |
| ullServerSendTime | UINT64   | 服务端发送时间                             |
| ascServiceName    | CHAR[64] | 目的服务名                                 |
| ascOperation      | CHAR[64] | API 名称                                   |
| ullStatusCode     | UINT64   | 状态返回码（64 位，V4 引入）               |
| usRspMsgNo        | UINT16   | 消息序号（多次响应场景）                   |
| aucReserved[6]    | UINT8[6] | 预留                                       |

### 6.3 管理消息头 RPC_MNG_HEAD_S

用于鉴权、机密性/完整性协商等管理类消息（`ucMsgType = 5/6/7` 等）。

| 字段           | 类型     | 说明                                            |
|----------------|----------|-------------------------------------------------|
| usRole         | UINT16   | 发送者角色 CROS_ROLE_E                           |
| usType         | UINT16   | 管理消息类型 CROS_MNG_MSG_TYPE_E                |
| bRspResult     | BOOL     | 是否为结果数据                                   |
| acProcName     | CHAR[64] | 本地进程名（`RPC_PROC_NAME_LEN=64`）            |
| uiProcRet      | UINT32   | 结果值                                           |
| bSvrAuthorize  | BOOL     | 是否需要开启服务名鉴权                          |
| ucEnCryption   | UINT8    | 是否使能加密（0=不使能）                        |
| ucEnIntegrity  | UINT8    | 是否使能完整性（0=不使能）                      |
| ucCipherSuite  | UINT8    | 算法套（0=缺省）                                |
| aucReserved[5] | UINT8[5] | 预留                                            |

---

## 7. 扩展消息头（Extend Head）

扩展消息头位于会话头之后、payload 之前，可有 0 个或多个，按类型编号升序排列。每段由 4 字节扩展公共头 + 内容组成。

### 7.1 扩展公共头 RPC_EXTEND_COMM_HEAD_S（4 字节）

| 字段       | 类型   | 说明                                                |
|------------|--------|-----------------------------------------------------|
| usHeadLen  | UINT16 | 扩展消息头内容长度（不含本公共头，大端）            |
| ucType     | UINT8  | 扩展头类型 RPC_EXTEND_HEAD_TYPE_E                   |
| ucReserved | UINT8  | 预留                                                |

### 7.2 扩展头类型

| 值 | 枚举名                          | 对应结构体               | 内容                                   |
|----|----------------------------------|--------------------------|----------------------------------------|
| 0  | RPC_EXTEND_HEAD_TYPE_TRACE_KEY   | RPC_DOTR_TRACE_HEAD_S    | 调用链 Trace 信息                       |
| 1  | RPC_EXTEND_HEAD_TYPE_TIME_STAMP  | RPC_TIME_STAMP_HEAD_S    | 时间戳信息                             |

- **RPC_DOTR_TRACE_HEAD_S**：`RPC_DOTR_TRACE_KEY_S`（TraceID/SpanID/FatherSpanID 各 UINT64，共 24 字节）+ `ucRecordMsgSwitch`(1) + `aucReserved[7]`。
- **RPC_TIME_STAMP_HEAD_S**：`ullServerEnqueueTime`(UINT64，服务端入队时间戳)。

扩展头机制要点：
- 最多支持 63 种类型（`RPC_EXTEND_HEAD_TYPE_BUTT`）。
- 发送前将内容填入 `RPC_MSG_HEAD_S.stExtendHeadCache` 并通过 `RPC_APPEND_EXTEND_HEAD_TYPE` 置 bitmap 位；发送时由 `RPC_FillMsgExtendHeadByCache` 线性写入报文。
- 接收侧 `RPC_FillExtendHeadCacheByMsg` 按公共头中的 `ucType` 与 `usHeadLen` 解析回缓存，做兼容性拷贝（新版本字段多则补零）。
- 扩展头内容**不**由框架统一做字节序转换，需各扩展头自行处理（如 Trace/时间戳扩展头有对应的 `RPC_TRACE_KEY_INVERT` / `RPC_TIMESTAMP_INVERT` 宏）。
- 约束：会话头 + 扩展公共头总和 + 扩展头内容总和不得超过 65535 字节。

---

## 8. 版本兼容机制

请求头/应答头存在多个结构版本，用于新旧版本进程互通：

- **Req 头版本**：V00 ~ V05（V2 增幂等序号、V3 增主体名称/客户端身份、V4 增客户端标识/UUID 老化、V5 增超时限制/发送时间戳）。
- **Rsp 头版本**：V00 ~ V04（V2 增服务实例、V3 增时间戳可定位性、V4 增 64 位返回码）。
- 当前版本由 `CROS_ReqRspHdrVerSpec` 协商指定；与老版本通信时降级使用 `RPC_REQ_HEAD_V1_S` / `RPC_RSP_HEAD_V1_S`（V1 兼容结构）。
- 自 2023.2 起，收端**不再依赖版本号区分**，改为通过公共头中的 `usHeadLen`（头长度）来识别实际会话头大小，保证前向/后向兼容。

---

## 9. TCP 底座的收发分帧流程

以 TCP 底座（`cros_rpctcp`）为例说明报文在线路上的收发处理，其他流式底座（Mesh/Domain）逻辑类似。

### 9.1 接收侧（分帧与解析）

1. **Peek 公共头**：`rpcTcpMsgPeek` 以 `MSG_PEEK` 预读 `sizeof(RPC_COMM_HEAD_S)`（24 字节），不消费数据。
2. **魔术字定位**：`rpcTcpMsgMagicCheck` 将前 8 字节作为 UINT64 与 `"CSE.TCP"+ucProtocol` 特征值比较；若不匹配则逐字节扫描，丢弃魔术字之前的无效数据。
3. **类型校验**：`rpcTcpCommHeadGet` 校验 `ucMsgType ∈ [2, MNG_BUTT)`，非法则清空接收缓存。
4. **长度校验**：`RPC_ChkMsgLength` 校验 `usHeadLen <= uiLength <= RPC_PAYLOAD_MAX_LEN + usHeadLen`。
5. **读取报文体**：按 `uiLength` 申请 `uiLength + sizeof(RPC_COMM_HEAD_S)` 缓冲，读取后续 `uiLength` 字节（会话头 + 扩展头 + payload）。
6. **重组**：`RPC_CopyRpcMsg` 将线性报文重组为内存态 `RPC_MSG_HEAD_S`，交由上层分发处理。

### 9.2 发送侧（拼装与发送）

1. **计算长度**：`uiHeadLen = sizeof(会话头) + 扩展头总长`；`uiSendMsgSize = RPC_COMM_HEAD_LEN + uiHeadLen + uiPayloadLen`。
2. **填充公共头**：写入魔术字 `ullProtocolID`、`ullSNO`、`uiLength = uiHeadLen + uiPayloadLen`（大端）、`usHeadLen`（大端）、`ucMsgType`、`isPacked`。
3. **填充会话头**：紧接公共头写入 `RPC_REQ_HEAD_S` / `RPC_RSP_HEAD_S` / `RPC_MNG_HEAD_S`。
4. **填充扩展头**：`RPC_FillMsgExtendHeadByCache` 依次写入每段「扩展公共头 + 内容」。
5. **追加 payload**：写入序列化后的接口参数字节流。
6. **发送**：`rpcTcpRawMsgSend` 循环 `send()` 直至整包发出；启用 TLS 时公共头明文发送、报文体经 SSL 加密发送。

### 9.3 报文示例（请求消息）

```
偏移  内容
0     43 53 45 2E 54 50 43 01          "CSE.TCP" + 0x01(TCP)
8     <ullSNO, 8B, 大端>
16    <uiLength, 4B, 大端> = sizeof(Req头) + 扩展头总长 + payloadLen
20    <usHeadLen, 2B, 大端> = sizeof(Req头) + 扩展头总长
22    02                              ucMsgType = RPC_MSG_TYPE_REQ
23    01                              isPacked = 1
24    <RPC_REQ_HEAD_S ...>
24+N  [扩展公共头1(4B)][扩展头1] ... [扩展公共头n(4B)][扩展头n]
      <序列化 payload>
```

---

## 10. Payload 序列化

payload 为 RPC 接口入参/出参的序列化字节流，序列化方式由 IDL 层（`cros_idl`）适配：

| 序列化方式 | 枚举值（RPC_IDL_MSG_TYPE_E） | 说明                          |
|------------|------------------------------|-------------------------------|
| FlatCC     | RPC_IDL_MSG_TYPE_FB          | FlatBuffers 序列化            |
| FlatCC 高性能 | RPC_IDL_MSG_TYPE_FB_SPEEDING | FlatBuffers 高性能场景       |
| Protobuf   | RPC_IDL_MSG_TYPE_PB          | Protobuf 序列化               |
| 通用消息   | RPC_IDL_MSG_TYPE_COMM        | 通用消息，常规场景不使用      |

payload 缺省限长 `RPC_PAYLOAD_DEFAULT_LEN_LIMIT = 60KB`，最大不超过 `RPC_PAYLOAD_MAX_LEN`（约 32MB）；Mesh 连接创建时可通过属性按连接调整限长。

---

## 11. 关键常量速查

| 常量                         | 值            | 说明                          |
|------------------------------|---------------|-------------------------------|
| RPC_MAGIC                    | "CSE.TCP"     | 魔术字字符串                  |
| RPC_MAGIC_NUM                | 7             | 魔术字字节数                  |
| RPC_COMM_HEAD_LEN            | 24            | 公共消息头长度                |
| RPC_EXTEND_COMM_HEAD_LEN     | 4             | 扩展公共头长度                |
| RPC_SERVICE_NAME_LEN         | 64            | 服务名长度                    |
| RPC_OPERATION_LEN            | 64            | API 名长度                    |
| RPC_PROC_NAME_LEN            | 64            | 进程名长度                    |
| RPC_MSG_TRACE_BYTE_NUM       | 16            | Trace 缓冲字节数              |
| RPC_PAYLOAD_MAX_LEN          | 32MB - 4KB    | payload 最大长度              |
| RPC_PAYLOAD_DEFAULT_LEN_LIMIT| 60KB          | payload 缺省限长              |
| RPC_TIMEOUT_DEFAULT          | 5000(ms)      | 默认超时时间                  |

