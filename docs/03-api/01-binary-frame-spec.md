# flowbench 自定义二进制协议帧头字节级契约 (Binary Frame Specification)

- **文档编号**：`01-binary-frame-spec.md`
- **文档版本**：v1.0.0
- **编制日期**：2026-10-25
- **当前阶段**：阶段三：协议契约冻结（帧协议契约）
- **状态**：已冻结 (Frozen)

---

## 1. 字节序与基本常量契约 (Endianness & Constants)

- **字节序 (Byte Order)**：所有多字节整型字段（`uint16`、`uint32`、`uint64`）强制使用 **大端字节序 (`binary.BigEndian`)**。
- **固定帧头长度 (`HeaderSize`)**：`24` 字节（Bytes `0..23`）。
- **单帧最大载荷长度 (`MaxPayloadSize`)**：`4,194,304` 字节（`4 MB` = `0x00400000`）。
- **单帧最大总长度 (`MaxFrameSize`)**：`24 + 4,194,304 = 4,194,328` 字节。

```go
package codec

const (
    HeaderSize     = 24
    MagicNumber    = uint16(0xFB01)
    ProtocolVer    = uint8(0x01)
    MaxPayloadSize = uint32(4 * 1024 * 1024) // 4 MB
)
```

---

## 2. 24 字节帧头字节偏移字典 (Byte-Level Offset Dictionary)

| 字节区间 | 偏移 (`Offset`) | 长度 (`Len`) | 字段名 (`Field`) | Go 类型 | 编码规则与合法值契约 |
| :---: | :---: | :---: | :--- | :---: | :--- |
| `[0:2]` | `0` | `2` | `Magic` | `uint16` | `BigEndian.PutUint16(buf[0:2], 0xFB01)` -> 固定字节 `[0xFB, 0x01]` |
| `[2:3]` | `2` | `1` | `Version` | `uint8` | `buf[2] = 0x01` |
| `[3:4]` | `3` | `1` | `OpCode` | `uint8` | `buf[3] = opCode`（合法取值见第 3 节） |
| `[4:5]` | `4` | `1` | `Flags` | `uint8` | `buf[4] = flags`（Bit0: `0x01` Deterministic, Bit1: `0x02` DebugTrace） |
| `[5:6]` | `5` | `1` | `Status` | `uint8` | `buf[5] = status`（请求帧固定 `0x00`，响应帧见第 3 节） |
| `[6:8]` | `6` | `2` | `Reserved` | `uint16` | `BigEndian.PutUint16(buf[6:8], 0x0000)` -> 固定字节 `[0x00, 0x00]` |
| `[8:16]` | `8` | `8` | `RequestID` | `uint64` | `BigEndian.PutUint64(buf[8:16], reqID)` |
| `[16:20]` | `16` | `4` | `PayloadLen` | `uint32` | `BigEndian.PutUint32(buf[16:20], uint32(len(payload)))` |
| `[20:24]` | `20` | `4` | `CRC32` | `uint32` | `BigEndian.PutUint32(buf[20:24], crc32.ChecksumIEEE(payload))`（空载荷时为 `0`） |
| `[24:24+N]` | `24` | `N` | `Payload` | `[]byte` | Protobuf 确定性序列化字节切片，长度必须严格等于 `PayloadLen` |

---

## 3. 操作码 (`OpCode`)、标志位 (`Flags`) 与状态码 (`Status`) 常量契约

```go
package codec

// OpCode 定义业务消息类型与流向
const (
    OpPingReq               uint8 = 0x01 // Client -> Server 或 Server -> Backend 探活请求
    OpPongResp              uint8 = 0x02 // Server -> Client 或 Backend -> Server 探活响应
    OpOrderQuoteReq         uint8 = 0x11 // Client -> Server 订单计价与风控评估请求
    OpOrderQuoteResp        uint8 = 0x12 // Server -> Client 订单计价与风控评估响应
    OpCreditEvalReq         uint8 = 0x21 // Client -> Server 授信评估请求
    OpCreditEvalResp        uint8 = 0x22 // Server -> Client 授信评估响应
    OpBackendOrderQueryReq  uint8 = 0x31 // Server -> Backend 商品库存与信用底数查询请求
    OpBackendOrderQueryResp uint8 = 0x32 // Backend -> Server 商品库存与信用底数查询响应
    OpBackendCreditQueryReq uint8 = 0x41 // Server -> Backend 征信履约档案查询请求
    OpBackendCreditQueryResp uint8 = 0x42 // Backend -> Server 征信履约档案查询响应
    OpErrorResp             uint8 = 0xFF // 通用协议级/框架级错误响应
)

// Flags 位掩码定义
const (
    FlagNone          uint8 = 0x00
    FlagDeterministic uint8 = 0x01 // 强制启用确定性时间戳与流水号
    FlagDebugTrace    uint8 = 0x02 // 在响应中附带调试跟踪元数据
)

// Status 帧级传输状态码定义
const (
    StatusOK                 uint8 = 0x00 // 帧封送与路由正常
    StatusBadPayload         uint8 = 0x01 // Protobuf 反序列化失败
    StatusUnknownOpCode      uint8 = 0x02 // 未知或不支持的 OpCode
    StatusDownstreamError    uint8 = 0x03 // 下游 Backend 调用失败或超时
    StatusInternalError      uint8 = 0x04 // 服务端内部异常
    StatusUnsupportedVersion uint8 = 0x05 // 协议版本不兼容
)
```

---

## 4. 二进制帧十六进制字节级示例 (Wire Hex Dump Example)

假设 `Client` 发送一个 `RequestID = 1001 (0x00000000000003E9)`、`OpCode = 0x01 (OpPingReq)`、`Flags = 0x01 (FlagDeterministic)` 的请求帧，其 Protobuf `PingRequest{ClientId: "cli-1", Message: "hi", SentAtUnix: 1700000000}` 序列化后为 17 字节（假设 CRC32 为 `0xA1B2C3D4`），则在线路上的 24 字节帧头 Hex 如下：

```text
Offset  Hex Bytes                                         Field Breakdown
------  ------------------------------------------------  ---------------------------------------
0000    fb 01                                             Magic = 0xFB01
0002          01                                          Version = 0x01
0003             01                                       OpCode = 0x01 (OpPingReq)
0004                01                                    Flags = 0x01 (FlagDeterministic)
0005                   00                                 Status = 0x00 (StatusOK)
0006                      00 00                           Reserved = 0x0000
0008                            00 00 00 00 00 00 03 e9   RequestID = 1001 (uint64 BigEndian)
0010    00 00 00 11                                       PayloadLen = 17 bytes (uint32 BigEndian)
0014                a1 b2 c3 d4                           CRC32 = 0xA1B2C3D4 (uint32 BigEndian)
0018    [17 bytes of Protobuf Payload ...]                Payload
```

---

## 5. `internal/codec` 核心接口与错误契约 (Go API Contract)

```go
package codec

import (
    "errors"
    "io"
)

var (
    ErrInvalidMagic        = errors.New("codec: invalid magic number (expected 0xFB01)")
    ErrUnsupportedVersion  = errors.New("codec: unsupported protocol version (expected 0x01)")
    ErrPayloadTooLarge     = errors.New("codec: payload length exceeds maximum limit (4MB)")
    ErrChecksumMismatch    = errors.New("codec: payload CRC32 checksum mismatch")
    ErrNonZeroEmptyCRC     = errors.New("codec: empty payload must have zero CRC32")
)

// Header 表示 24 字节定长二进制帧头
type Header struct {
    Magic      uint16
    Version    uint8
    OpCode     uint8
    Flags      uint8
    Status     uint8
    Reserved   uint16
    RequestID  uint64
    PayloadLen uint32
    CRC32      uint32
}

// Frame 表示一个完整的自定义二进制协议帧
type Frame struct {
    Header  Header
    Payload []byte
}

// NewFrame 根据 OpCode、RequestID、Flags、Status 与 Payload 构造自动计算好 PayloadLen 与 CRC32 的 Frame
func NewFrame(opCode uint8, reqID uint64, flags uint8, status uint8, payload []byte) *Frame

// EncodeHeader 将 Header 编码为 24 字节数组
func EncodeHeader(h Header) [HeaderSize]byte

// DecodeHeader 从 24 字节切片解码并验证 Magic、Version 与 PayloadLen 上限
func DecodeHeader(buf []byte) (Header, error)

// WriteFrame 将 24B 帧头与 Payload 组装为单一切片并原子写入 w
func WriteFrame(w io.Writer, f *Frame) error

// ReadFrame 使用 io.ReadFull 从 r 中完整读取一帧（自动处理粘包与半包）并验证 CRC32
func ReadFrame(r io.Reader) (*Frame, error)
```
