# CROS RPC 自定义二进制协议字节级规范 (CROS RPC Binary Specification)

- **文档编号**：`03-cros-rpc-binary-spec.md`
- **文档版本**：v1.0.0
- **编制日期**：2026-10-28
- **当前阶段**：阶段三：协议契约明晰
- **状态**：已冻结 (Frozen)

---

## 1. 基础常数与字节序契约

- **字节序 (Byte Order)**：多字节整型（`uint16`、`uint32`、`uint64`）统一使用 **大端字节序 (`binary.BigEndian`)**；
- **公共头定长 (`RPC_COMM_HEAD_LEN`)**：`24` 字节；
- **扩展公共头定长 (`RPC_EXTEND_COMM_HEAD_LEN`)**：`4` 字节；
- **最大 Payload 长度 (`RPC_PAYLOAD_MAX_LEN`)**：`33,550,336` 字节（`32 MB - 4 KB`）；
- **默认 Payload 限制 (`RPC_PAYLOAD_DEFAULT_LEN_LIMIT`)**：`61,440` 字节（`60 KB`）；
- **魔数前缀 (`RPC_MAGIC`)**：固定 7 字节 ASCII `"CSE.TCP"`（`0x43 53 45 2E 54 50 43`）。

---

## 2. 24 字节公共头字典 (RPC_COMM_HEAD_S)

| 字节区间 | 偏移 (`Offset`) | 长度 (`Len`) | 字段名 (`Field`) | 类型 | 说明与契约规则 |
| :---: | :---: | :---: | :--- | :---: | :--- |
| `[0:7]` | `0` | `7` | `aucMagic` | `CHAR[7]` | 固定 ASCII 字符串 `"CSE.TCP"` |
| `[7:8]` | `7` | `1` | `ucProtocol` | `UINT8` | 底层传输底座代码（`1=TCP`, `2=DMS`, `4=Mesh`, `5=Domain` 等） |
| `[8:16]` | `8` | `8` | `ullSNO` | `UINT64` | 全局自增 RPC 请求流水号（大端，用于链路配对与自愈） |
| `[16:20]` | `16` | `4` | `uiLength` | `UINT32` | 报文体数据总大小 = `会话头 + 扩展头 + Payload`（不含本公共头 24 字节） |
| `[20:22]` | `20` | `2` | `usHeadLen` | `UINT16` | 头部总长度 = `会话头 + 扩展头总长`（大端） |
| `[22:23]` | `22` | `1` | `ucMsgType` | `UINT8` | 消息大类（`2=REQ`, `3=RSP`, `4=RSP_SEG`, `5=MNG`） |
| `[23:24]` | `23` | `1` | `isPacked` | `UINT8` | 载荷序列化标识（`0=未序列化`, `1=已序列化`，默认填 `1`） |

---

## 3. 会话头字节级字典 (Session Head)

### 3.1 请求头 RPC_REQ_HEAD_S（定长 256 字节，紧跟公共头）

| 字节区间 (相对会话头) | 偏移 | 长度 | 字段名 | 类型 | 说明 |
| :---: | :---: | :---: | :--- | :---: | :--- |
| `[0:2]` | `0` | `2` | `usMsgStructVer` | `UINT16` | 消息结构版本号（大端） |
| `[2:4]` | `2` | `2` | `usReqHeadLen` | `UINT16` | Req 头长度（固定 `256`，大端） |
| `[4:8]` | `4` | `4` | `uiDst2ndAddrId` | `UINT32` | 二级寻址 ID |
| `[8:72]` | `8` | `64` | `ascServiceName` | `CHAR[64]` | 目的微服务名称（ASCII，末尾 `\0` 截断） |
| `[72:136]` | `72` | `64` | `ascOperation` | `CHAR[64]` | 目的 API 方法名（ASCII，末尾 `\0` 截断） |
| `[136:137]` | `136` | `1` | `ucReqType` | `UINT8` | 请求类型（`1=同步`, `2=异步`, `3=Oneway`） |
| `[137:138]` | `137` | `1` | `ucSubjectType` | `UINT8` | 主体标识类型 |
| `[138:140]` | `138` | `2` | `usRpcVer` | `UINT16` | RPC 版本，预留 |
| `[140:144]` | `140` | `4` | `uiSessionId` | `UINT32` | FRPC 会话 ID |
| `[144:160]` | `144` | `16` | `aucTraceBuff` | `UCHAR[16]` | Trace 链路追踪元数据 |
| `[160:168]` | `160` | `8` | `ullIdempotentSeq` | `UINT64` | 幂等请求序号（大端） |
| `[168:232]` | `168` | `64` | `unSubjectName` | `CHAR[64]` | 主体客户端身份名称 |
| `[232:240]` | `232` | `8` | `ullClientUuid` | `UINT64` | 客户端 UUID 标识（大端） |
| `[240:242]` | `240` | `2` | `usUuidTimeoutS` | `UINT16` | UUID 老化时间（秒） |
| `[242:244]` | `242` | `2` | `ucReserved` | `UINT8[2]` | 预留对齐字节（填 `0`） |
| `[244:248]` | `244` | `4` | `uiTimeOutSecondLimit` | `UINT32` | 超时限制（毫秒，大端） |
| `[248:256]` | `248` | `8` | `ullSendTimeStamp` | `UINT64` | 客户端发送时间戳（微秒/毫秒，大端） |

> **注**：合计定长 **256 字节**（`sizeof(RPC_REQ_HEAD_S) = 256`）。

---

### 3.2 应答头 RPC_RSP_HEAD_S（定长 200 字节，紧跟公共头）

| 字节区间 (相对会话头) | 偏移 | 长度 | 字段名 | 类型 | 说明 |
| :---: | :---: | :---: | :--- | :---: | :--- |
| `[0:2]` | `0` | `2` | `usMsgStructVer` | `UINT16` | 消息结构版本号（大端） |
| `[2:4]` | `2` | `2` | `usRspHeadLen` | `UINT16` | Rsp 头长度（固定 `200`，大端） |
| `[4:8]` | `4` | `4` | `uiStatusCode` | `UINT32` | 32 位状态返回码（`0=成功`，大端） |
| `[8:24]` | `8` | `16` | `aucTraceBuff` | `UCHAR[16]` | Trace 链路追踪元数据 |
| `[24:28]` | `24` | `4` | `uiSvrInstId` | `UINT32` | 服务端实例 ID（大端） |
| `[28:32]` | `28` | `4` | `uiReserved` | `UINT32` | 预留填充 |
| `[32:40]` | `32` | `8` | `ullClientSendTime` | `UINT64` | 客户端发送时间戳（大端） |
| `[40:48]` | `40` | `8` | `ullServerRecvTime` | `UINT64` | 服务端接收时间戳（大端） |
| `[48:56]` | `48` | `8` | `ullServerSendTime` | `UINT64` | 服务端发送时间戳（大端） |
| `[56:120]` | `56` | `64` | `ascServiceName` | `CHAR[64]` | 目的微服务名称（ASCII） |
| `[120:184]` | `120` | `64` | `ascOperation` | `CHAR[64]` | 目的 API 方法名（ASCII） |
| `[184:192]` | `184` | `8` | `ullStatusCode` | `UINT64` | 64 位宽状态返回码（大端） |
| `[192:194]` | `192` | `2` | `usRspMsgNo` | `UINT16` | 分段序号（多次响应场景） |
| `[194:200]` | `194` | `6` | `aucReserved` | `UINT8[6]` | 预留对齐填充 |

> **注**：合计定长 **200 字节**（`sizeof(RPC_RSP_HEAD_S) = 200`）。

---

## 4. 扩展消息头公共头 (RPC_EXTEND_COMM_HEAD_S)

位于会话头之后、Payload 之前（数量由 `usHeadLen` 减去会话头长度决定）：

| 字节区间 | 偏移 | 长度 | 字段名 | 类型 | 说明 |
| :---: | :---: | :---: | :--- | :---: | :--- |
| `[0:2]` | `0` | `2` | `usHeadLen` | `UINT16` | 扩展消息头内容长度（不含本 4B 公共头，大端） |
| `[2:3]` | `2` | `1` | `ucType` | `UINT8` | 扩展头类型（`0=TraceKey`, `1=TimeStamp`） |
| `[3:4]` | `3` | `1` | `ucReserved` | `UINT8` | 预留字节 |

---

## 5. 常量枚举契约字典

```go
package cros

const (
    // Magic 特征
    MagicString = "CSE.TCP"
    MagicLen    = 7

    // 长度契约
    CommHeadLen       = 24
    ReqHeadLen        = 252
    RspHeadLen        = 200
    ExtendCommHeadLen = 4
    MaxPayloadLen     = 32*1024*1024 - 4096

    // 消息类型
    MsgTypeReq        uint8 = 2
    MsgTypeRsp        uint8 = 3
    MsgTypeRspSeg     uint8 = 4
    MsgTypeMng        uint8 = 5

    // 传输底座
    TransportDefault uint8 = 0
    TransportTCP     uint8 = 1
    TransportMesh    uint8 = 4
    TransportDomain  uint8 = 5

    // 扩展头类型
    ExtendTypeTraceKey   uint8 = 0
    ExtendTypeTimeStamp  uint8 = 1
)
```
