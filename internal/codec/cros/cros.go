// Package cros 提供 DOPHI 平台 CROS RPC 复合多级二进制协议的解析、封装与流式状态机。
package cros

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// 协议常量定义
const (
	// MagicString 7 字节魔数字符串
	MagicString = "CSE.TCP"
	// MagicLen 魔数字节数
	MagicLen = 7

	// 报文结构定长尺寸契约
	CommHeadLen       = 24
	ReqHeadLen        = 256
	RspHeadLen        = 200
	ExtendCommHeadLen = 4
	ServiceNameLen    = 64
	OperationLen      = 64
	TraceBuffLen      = 16

	// 载荷长度限制契约
	MaxPayloadLen      = 32*1024*1024 - 4096 // 约 32MB - 4KB
	DefaultPayloadLim  = 60 * 1024           // 60KB
	DefaultTimeoutMsec = 5000                // 5000ms

	// 消息类型 ucMsgType (RPC_MSG_TYPE_E)
	MsgTypeReq         uint8 = 2  // 请求消息
	MsgTypeRsp         uint8 = 3  // 应答消息
	MsgTypeRspSeg      uint8 = 4  // 应答分段
	MsgTypeMng         uint8 = 5  // 管理消息
	MsgTypeMngReq      uint8 = 6  // 鉴权请求
	MsgTypeUUIDReq     uint8 = 7  // UUID 请求
	MsgTypeTLSRReq     uint8 = 8  // TLS 请求
	MsgTypeTLSRsp      uint8 = 9  // TLS 应答
	MsgTypeTLSRspSeg   uint8 = 10 // TLS 应答分段
	MsgTypeFRPCReq     uint8 = 11 // 共享内存 FRPC 请求
	MsgTypeFRPCDel     uint8 = 12 // FRPC 清理
	MsgTypeDirectChReq uint8 = 13 // 直通通道请求
	MsgTypeDirectChRsp uint8 = 14 // 直通通道应答

	// 传输底座标识 ucProtocol (RPC_TRANSPORT_TYPE_E)
	TransportDefault   uint8 = 0 // 默认
	TransportTCP       uint8 = 1 // TCP
	TransportDMS       uint8 = 2 // DMS
	TransportUC        uint8 = 3 // UC
	TransportMesh      uint8 = 4 // Mesh
	TransportDomain    uint8 = 5 // Unix Domain
	TransportFRPC      uint8 = 6 // 共享内存 FRPC
	TransportCustomize uint8 = 7 // 自定义底座

	// 扩展头类型 (RPC_EXTEND_HEAD_TYPE_E)
	ExtendTypeTraceKey  uint8 = 0 // 调用链 Trace
	ExtendTypeTimeStamp uint8 = 1 // 时间戳

	// 请求类型 (RPC_REQ_MSG_TYPE_E)
	ReqTypeSync   uint8 = 1 // 同步请求
	ReqTypeAsync  uint8 = 2 // 异步请求
	ReqTypeOneway uint8 = 3 // 单向无回包
)

var (
	ErrInvalidMagic   = errors.New("cros: invalid magic header")
	ErrInvalidMsgType = errors.New("cros: invalid message type")
	ErrInvalidLength  = errors.New("cros: invalid length bounds")
	ErrPayloadTooBig  = errors.New("cros: payload exceeds maximum allowed size")
	ErrFrameTooShort  = errors.New("cros: raw frame buffer is too short")
)

// CommHead 描述 24 字节公共消息头 (RPC_COMM_HEAD_S)
type CommHead struct {
	Magic    [7]byte `json:"magic"`     // 固定 "CSE.TCP"
	Protocol uint8   `json:"protocol"`  // 传输底座类型 (1=TCP, 4=Mesh 等)
	SNO      uint64  `json:"sno"`       // 请求序列号 (大端)
	Length   uint32  `json:"length"`    // 数据总大小 = 会话头 + 扩展头 + Payload (不含本公共头 24B)
	HeadLen  uint16  `json:"head_len"`  // 头部总长 = 会话头 + 扩展头总长
	MsgType  uint8   `json:"msg_type"`  // 消息类型 (2=REQ, 3=RSP 等)
	IsPacked uint8   `json:"is_packed"` // 是否已序列化 (0=未序列化, 1=已序列化)
}

// ReqHead 描述定长 256 字节请求会话头 (RPC_REQ_HEAD_S)
type ReqHead struct {
	MsgStructVer       uint16   `json:"msg_struct_ver"`
	ReqHeadLen         uint16   `json:"req_head_len"` // 固定 256
	Dst2ndAddrID       uint32   `json:"dst_2nd_addr_id"`
	ServiceName        string   `json:"service_name"` // 目的微服务名 (64B ASCII)
	Operation          string   `json:"operation"`    // API 名称 (64B ASCII)
	ReqType            uint8    `json:"req_type"`     // 1=同步, 2=异步, 3=Oneway
	SubjectType        uint8    `json:"subject_type"`
	RPCVer             uint16   `json:"rpc_ver"`
	SessionID          uint32   `json:"session_id"`
	TraceBuff          [16]byte `json:"trace_buff"`
	IdempotentSeq      uint64   `json:"idempotent_seq"`
	SubjectName        string   `json:"subject_name"`
	ClientUUID         uint64   `json:"client_uuid"`
	UUIDTimeoutS       uint16   `json:"uuid_timeout_s"`
	Reserved           [2]byte  `json:"reserved"`
	TimeoutSecondLimit uint32   `json:"timeout_second_limit"`
	SendTimestamp      uint64   `json:"send_timestamp"`
}

// RspHead 描述定长 200 字节应答会话头 (RPC_RSP_HEAD_S)
type RspHead struct {
	MsgStructVer   uint16   `json:"msg_struct_ver"`
	RspHeadLen     uint16   `json:"rsp_head_len"` // 固定 200
	StatusCode     uint32   `json:"status_code"`  // 状态返回码 (0=成功)
	TraceBuff      [16]byte `json:"trace_buff"`
	SvrInstID      uint32   `json:"svr_inst_id"`
	Reserved       uint32   `json:"reserved"`
	ClientSendTime uint64   `json:"client_send_time"`
	ServerRecvTime uint64   `json:"server_recv_time"`
	ServerSendTime uint64   `json:"server_send_time"`
	ServiceName    string   `json:"service_name"` // 64B ASCII
	Operation      string   `json:"operation"`    // 64B ASCII
	StatusCode64   uint64   `json:"status_code_64"`
	RspMsgNo       uint16   `json:"rsp_msg_no"`
	Reserved2      [6]byte  `json:"reserved2"`
}

// ExtendCommHead 描述 4 字节扩展公共头及内容 (RPC_EXTEND_COMM_HEAD_S)
type ExtendCommHead struct {
	HeadLen  uint16 `json:"head_len"` // 扩展内容长度
	Type     uint8  `json:"type"`     // 扩展头类型
	Reserved uint8  `json:"reserved"`
	Data     []byte `json:"data"` // 扩展内容字节
}

// Frame 描述解剖后的完整 CROS 报文对象
type Frame struct {
	CommHead       CommHead         `json:"comm_head"`
	ReqHead        *ReqHead         `json:"req_head,omitempty"`
	RspHead        *RspHead         `json:"rsp_head,omitempty"`
	ExtendHeads    []ExtendCommHead `json:"extend_heads,omitempty"`
	Payload        []byte           `json:"payload"`
	RawWire        []byte           `json:"raw_wire"`
	SessionHeadRaw []byte           `json:"session_head_raw"`
}

// CleanCString 截断 C 风格定长字符数组中的 NULL 终止符与多余空白
func CleanCString(b []byte) string {
	idx := bytes.IndexByte(b, 0)
	if idx != -1 {
		b = b[:idx]
	}
	return string(bytes.TrimSpace(b))
}

// CopyCString 将 Go 字符串拷贝至固定长度字节切片并以 0 填充
func CopyCString(dst []byte, s string) {
	for i := range dst {
		dst[i] = 0
	}
	src := []byte(s)
	if len(src) > len(dst) {
		src = src[:len(dst)]
	}
	copy(dst, src)
}

// ParseCommHead 解析 24 字节公共头
func ParseCommHead(buf []byte) (CommHead, error) {
	if len(buf) < CommHeadLen {
		return CommHead{}, ErrFrameTooShort
	}
	if !bytes.Equal(buf[0:7], []byte(MagicString)) {
		return CommHead{}, ErrInvalidMagic
	}
	ch := CommHead{
		Protocol: buf[7],
		SNO:      binary.BigEndian.Uint64(buf[8:16]),
		Length:   binary.BigEndian.Uint32(buf[16:20]),
		HeadLen:  binary.BigEndian.Uint16(buf[20:22]),
		MsgType:  buf[22],
		IsPacked: buf[23],
	}
	copy(ch.Magic[:], buf[0:7])
	return ch, nil
}

// EncodeCommHead 编码 24 字节公共头至缓冲区
func (ch CommHead) Encode(buf []byte) {
	copy(buf[0:7], []byte(MagicString))
	buf[7] = ch.Protocol
	binary.BigEndian.PutUint64(buf[8:16], ch.SNO)
	binary.BigEndian.PutUint32(buf[16:20], ch.Length)
	binary.BigEndian.PutUint16(buf[20:22], ch.HeadLen)
	buf[22] = ch.MsgType
	buf[23] = ch.IsPacked
}
