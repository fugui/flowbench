// Package codec 实现 flowbench 24 字节定长大端自定义二进制协议帧编解码与流式拆粘包状态机。
package codec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// 协议帧基础常量定义
const (
	HeaderSize     = 24
	MagicNumber    = uint16(0xFB01)
	ProtocolVer    = uint8(0x01)
	MaxPayloadSize = uint32(4 * 1024 * 1024) // 单帧最大载荷上限 4MB
)

// OpCode 定义业务消息操作码与流向
const (
	OpPingReq                uint8 = 0x01 // Client -> Server 或 Server -> Backend 探活请求
	OpPongResp               uint8 = 0x02 // Server -> Client 或 Backend -> Server 探活响应
	OpOrderQuoteReq          uint8 = 0x11 // Client -> Server 订单计价与风控评估请求
	OpOrderQuoteResp         uint8 = 0x12 // Server -> Client 订单计价与风控评估响应
	OpCreditEvalReq          uint8 = 0x21 // Client -> Server 授信评估请求
	OpCreditEvalResp         uint8 = 0x22 // Server -> Client 授信评估响应
	OpBackendOrderQueryReq   uint8 = 0x31 // Server -> Backend 商品库存与信用底数查询请求
	OpBackendOrderQueryResp  uint8 = 0x32 // Backend -> Server 商品库存与信用底数查询响应
	OpBackendCreditQueryReq  uint8 = 0x41 // Server -> Backend 征信履约档案查询请求
	OpBackendCreditQueryResp uint8 = 0x42 // Backend -> Server 征信履约档案查询响应
	OpErrorResp              uint8 = 0xFF // 通用协议级/框架级错误响应
)

// Flags 帧标志位掩码定义
const (
	FlagNone          uint8 = 0x00
	FlagDeterministic uint8 = 0x01 // 请求指示强制采用确定性模式
	FlagDebugTrace    uint8 = 0x02 // 请求指示附带调试追踪信息
)

// Status 帧级传输状态码定义
const (
	StatusOK                 uint8 = 0x00 // 正常业务帧
	StatusBadPayload         uint8 = 0x01 // Protobuf 载荷反序列化失败
	StatusUnknownOpCode      uint8 = 0x02 // 未知或不支持的 OpCode
	StatusDownstreamError    uint8 = 0x03 // 下游 Backend 调用失败或超时
	StatusInternalError      uint8 = 0x04 // 服务端内部异常
	StatusUnsupportedVersion uint8 = 0x05 // 协议版本不兼容
)

// 强类型协议层错误定义
var (
	ErrInvalidMagic       = errors.New("codec: invalid magic number (expected 0xFB01)")
	ErrUnsupportedVersion = errors.New("codec: unsupported protocol version (expected 0x01)")
	ErrPayloadTooLarge    = errors.New("codec: payload length exceeds maximum limit (4MB)")
	ErrChecksumMismatch   = errors.New("codec: payload CRC32 checksum mismatch")
	ErrNonZeroEmptyCRC    = errors.New("codec: empty payload must have zero CRC32")
	ErrInvalidHeaderLen   = errors.New("codec: header buffer length must be 24 bytes")
)

// Header 表示 24 字节定长大端二进制协议帧头
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

// Frame 表示一个完整的自定义二进制协议帧（24B Header + Protobuf Payload）
type Frame struct {
	Header  Header
	Payload []byte
}

// NewFrame 构造一个自动计算好 PayloadLen 与 IEEE CRC32 校验码的二进制帧
func NewFrame(opCode uint8, reqID uint64, flags uint8, status uint8, payload []byte) *Frame {
	var checksum uint32
	if len(payload) > 0 {
		checksum = crc32.ChecksumIEEE(payload)
	}
	copied := make([]byte, len(payload))
	copy(copied, payload)
	return &Frame{
		Header: Header{
			Magic:      MagicNumber,
			Version:    ProtocolVer,
			OpCode:     opCode,
			Flags:      flags,
			Status:     status,
			Reserved:   0,
			RequestID:  reqID,
			PayloadLen: uint32(len(copied)),
			CRC32:      checksum,
		},
		Payload: copied,
	}
}

// EncodeHeader 将 Header 按大端字节序编码为 24 字节数组
func EncodeHeader(h Header) [HeaderSize]byte {
	var buf [HeaderSize]byte
	binary.BigEndian.PutUint16(buf[0:2], h.Magic)
	buf[2] = h.Version
	buf[3] = h.OpCode
	buf[4] = h.Flags
	buf[5] = h.Status
	binary.BigEndian.PutUint16(buf[6:8], h.Reserved)
	binary.BigEndian.PutUint64(buf[8:16], h.RequestID)
	binary.BigEndian.PutUint32(buf[16:20], h.PayloadLen)
	binary.BigEndian.PutUint32(buf[20:24], h.CRC32)
	return buf
}

// DecodeHeader 从 24 字节切片解码并验证 Magic、Version 与 PayloadLen 上限
func DecodeHeader(buf []byte) (Header, error) {
	if len(buf) != HeaderSize {
		return Header{}, fmt.Errorf("%w: got %d", ErrInvalidHeaderLen, len(buf))
	}
	h := Header{
		Magic:      binary.BigEndian.Uint16(buf[0:2]),
		Version:    buf[2],
		OpCode:     buf[3],
		Flags:      buf[4],
		Status:     buf[5],
		Reserved:   binary.BigEndian.Uint16(buf[6:8]),
		RequestID:  binary.BigEndian.Uint64(buf[8:16]),
		PayloadLen: binary.BigEndian.Uint32(buf[16:20]),
		CRC32:      binary.BigEndian.Uint32(buf[20:24]),
	}
	if h.Magic != MagicNumber {
		return h, fmt.Errorf("%w: got 0x%04X", ErrInvalidMagic, h.Magic)
	}
	if h.Version != ProtocolVer {
		return h, fmt.Errorf("%w: got 0x%02X", ErrUnsupportedVersion, h.Version)
	}
	if h.PayloadLen > MaxPayloadSize {
		return h, fmt.Errorf("%w: got %d bytes", ErrPayloadTooLarge, h.PayloadLen)
	}
	return h, nil
}

// OpCodeName 返回可读的 OpCode 名称，用于结构化日志与调试输出
func OpCodeName(op uint8) string {
	switch op {
	case OpPingReq:
		return "OpPingReq(0x01)"
	case OpPongResp:
		return "OpPongResp(0x02)"
	case OpOrderQuoteReq:
		return "OpOrderQuoteReq(0x11)"
	case OpOrderQuoteResp:
		return "OpOrderQuoteResp(0x12)"
	case OpCreditEvalReq:
		return "OpCreditEvalReq(0x21)"
	case OpCreditEvalResp:
		return "OpCreditEvalResp(0x22)"
	case OpBackendOrderQueryReq:
		return "OpBackendOrderQueryReq(0x31)"
	case OpBackendOrderQueryResp:
		return "OpBackendOrderQueryResp(0x32)"
	case OpBackendCreditQueryReq:
		return "OpBackendCreditQueryReq(0x41)"
	case OpBackendCreditQueryResp:
		return "OpBackendCreditQueryResp(0x42)"
	case OpErrorResp:
		return "OpErrorResp(0xFF)"
	default:
		return fmt.Sprintf("OpUnknown(0x%02X)", op)
	}
}

// StatusName 返回可读的帧级传输状态码名称
func StatusName(status uint8) string {
	switch status {
	case StatusOK:
		return "StatusOK(0x00)"
	case StatusBadPayload:
		return "StatusBadPayload(0x01)"
	case StatusUnknownOpCode:
		return "StatusUnknownOpCode(0x02)"
	case StatusDownstreamError:
		return "StatusDownstreamError(0x03)"
	case StatusInternalError:
		return "StatusInternalError(0x04)"
	case StatusUnsupportedVersion:
		return "StatusUnsupportedVersion(0x05)"
	default:
		return fmt.Sprintf("StatusUnknown(0x%02X)", status)
	}
}
