# Milestone 1 作战卡：自定义二进制协议与编解码器 (Protocol & Codec)

- **文档编号**：`02-m1-protocol-and-codec.md`
- **归属阶段**：阶段三/四：M1 任务拆解与验收标准
- **前置依赖**：契约文档 `docs/03-api/01-binary-frame-spec.md`、`docs/03-api/02-protobuf-messages.md`

---

## 1. M1 实施目标

完成 Go 模块初始化、Protobuf 消息结构定义与 Go 代码落地、确定性序列化封装，以及 24 字节定长大端二进制帧编解码器与流式拆粘包状态机实现。

---

## 2. 详细任务拆解 (Tasks Breakdown)

### Task 1.1：初始化 Go Module 与 Protobuf 消息层 (`proto/` & `internal/protocol/`)
1. 初始化 `go.mod`（模块名 `github.com/fugui/flowbench`，引入 `google.golang.org/protobuf` 依赖）。
2. 在 `proto/flowbench/v1/messages.proto` 中落地完整的 Protobuf IDL 定义。
3. 在 `internal/protocol/pb/messages.pb.go` 中生成对应的 Go Protobuf 消息类型（实现 `proto.Message` 接口的 `PingRequest`、`PongResponse`、`OrderQuoteRequest`、`OrderQuoteResponse`、`CreditEvalRequest`、`CreditEvalResponse`、`BackendOrderQueryRequest`、`BackendOrderQueryResponse`、`BackendCreditQueryRequest`、`BackendCreditQueryResponse`、`ErrorResponse`）。
4. 在 `internal/protocol/marshal.go` 中封装：
   - `MarshalDeterministic(msg proto.Message) ([]byte, error)`：固定使用 `proto.MarshalOptions{Deterministic: true}`；
   - `Unmarshal(b []byte, msg proto.Message) error`；
   - `EncodeMessageFrame(opCode uint8, reqID uint64, flags uint8, status uint8, msg proto.Message) (*codec.Frame, error)`。

### Task 1.2：实现 24 字节定长大端二进制帧头编解码 (`internal/codec/frame.go`)
1. 定义 `HeaderSize = 24`、`MagicNumber = 0xFB01`、`ProtocolVer = 0x01`、`MaxPayloadSize = 4 * 1024 * 1024`。
2. 定义所有 `OpCode`、`Flags`、`Status` 常量及强类型错误变量（`ErrInvalidMagic`、`ErrUnsupportedVersion`、`ErrPayloadTooLarge`、`ErrChecksumMismatch`、`ErrNonZeroEmptyCRC`）。
3. 实现 `EncodeHeader(h Header) [24]byte` 与 `DecodeHeader(buf []byte) (Header, error)`，严格使用 `binary.BigEndian`。
4. 实现 `NewFrame(opCode uint8, reqID uint64, flags uint8, status uint8, payload []byte) *Frame`，自动计算 `PayloadLen` 与 `crc32.ChecksumIEEE(payload)`。

### Task 1.3：实现流式 TCP 粘包/半包安全读写器 (`internal/codec/stream.go`)
1. 实现 `WriteFrame(w io.Writer, f *Frame) error`：
   - 校验 `len(f.Payload) <= MaxPayloadSize`；
   - 分配 `make([]byte, HeaderSize+len(f.Payload))` 单一连续切片，拷贝 24B 帧头与 Payload 后执行完整写入，保证单次 `Write` 原子性。
2. 实现 `ReadFrame(r io.Reader) (*Frame, error)`：
   - 第一阶段调用 `io.ReadFull(r, headerBuf[:])` 精确读取 24B 帧头；
   - 调用 `DecodeHeader` 校验 `Magic`、`Version` 与 `PayloadLen <= MaxPayloadSize`；
   - 第二阶段当 `PayloadLen > 0` 时调用 `io.ReadFull(r, payload)` 精确读取载荷，并计算比对 `crc32.ChecksumIEEE(payload) == header.CRC32`。

### Task 1.4：编写编解码与拆粘包边界单元测试 (`internal/codec/frame_test.go`)
覆盖以下 6 项核心测试：
1. 正常帧封包与拆包往返一致性测试（Round-Trip）；
2. 空载荷帧（`PayloadLen == 0`）与非零空 CRC 异常检测测试；
3. 魔数错误（`Magic != 0xFB01`）、版本错误（`Version != 0x01`）、超大载荷（`PayloadLen > 4MB`）拦截测试；
4. 载荷篡改 1 比特触发 `ErrChecksumMismatch` 测试；
5. **TCP 半包模拟测试**：自定义 `oneByteReader`（每次 `Read` 仅返回 1 字节），验证 `ReadFrame` 能完整重组 24B 头 + Protobuf 载荷；
6. **TCP 粘包模拟测试**：将 10 个不同 `OpCode` 与不同长度的帧写入同一个 `bytes.Buffer`，连续调用 10 次 `ReadFrame` 验证全部精确拆出且顺序、内容 100% 匹配。

---

## 3. 验收标准 (Acceptance Criteria)

- [ ] **AC-M1-01**：`go vet ./internal/codec/... ./internal/protocol/...` 零警告通过。
- [ ] **AC-M1-02**：`go test -v -race ./internal/codec/... ./internal/protocol/...` 全部测试通过。
