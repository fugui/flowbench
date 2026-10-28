// Package cros 提供 DOPHI 平台 CROS RPC 复合多级二进制协议的解析、封装与流式状态机。
package cros

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

// 最大魔数滑动扫描查找上限（64KB，防止无底线死循环消耗 CPU）
const MaxMagicScanBytes = 64 * 1024

// ReadFrame 从输入流 r 中消费并解析一帧完整的 CROS RPC 复合报文（免疫粘包与半包）
func ReadFrame(r io.Reader) (*Frame, error) {
	// 1. 读取并对齐 24 字节公共头
	commBuf, err := scanAndReadCommHead(r)
	if err != nil {
		return nil, err
	}

	commHead, err := ParseCommHead(commBuf)
	if err != nil {
		return nil, err
	}

	// 2. 防 OOM 与边界合法性校验
	if commHead.HeadLen > uint16(commHead.Length) {
		return nil, fmt.Errorf("%w: head_len (%d) > total length (%d)", ErrInvalidLength, commHead.HeadLen, commHead.Length)
	}
	if commHead.Length > MaxPayloadLen+uint32(commHead.HeadLen) {
		return nil, fmt.Errorf("%w: length (%d) exceeds max allowed limit", ErrPayloadTooBig, commHead.Length)
	}

	// 3. 读取会话头 + 扩展头 + 载荷数据体 (uiLength 字节)
	bodyBuf := make([]byte, commHead.Length)
	if commHead.Length > 0 {
		if _, err := io.ReadFull(r, bodyBuf); err != nil {
			return nil, err
		}
	}

	// 完整线路报文
	rawWire := make([]byte, CommHeadLen+len(bodyBuf))
	copy(rawWire[:CommHeadLen], commBuf)
	copy(rawWire[CommHeadLen:], bodyBuf)

	// 4. 解剖会话头与扩展消息头
	headLen := int(commHead.HeadLen)
	var reqHead *ReqHead
	var rspHead *RspHead
	var sessionHeadRaw []byte
	var extendHeads []ExtendCommHead

	if headLen > 0 {
		if commHead.MsgType == MsgTypeReq {
			req, err := DecodeReqHead(bodyBuf[:min(headLen, ReqHeadLen)])
			if err == nil {
				reqHead = req
			}
			sessionHeadRaw = bodyBuf[:min(headLen, ReqHeadLen)]
			if headLen > ReqHeadLen {
				extendHeads = parseExtendHeads(bodyBuf[ReqHeadLen:headLen])
			}
		} else if commHead.MsgType == MsgTypeRsp || commHead.MsgType == MsgTypeRspSeg {
			rsp, err := DecodeRspHead(bodyBuf[:min(headLen, RspHeadLen)])
			if err == nil {
				rspHead = rsp
			}
			sessionHeadRaw = bodyBuf[:min(headLen, RspHeadLen)]
			if headLen > RspHeadLen {
				extendHeads = parseExtendHeads(bodyBuf[RspHeadLen:headLen])
			}
		} else {
			sessionHeadRaw = bodyBuf[:headLen]
		}
	}

	// 5. 提取 Protobuf 序列化 Payload (从偏移 headLen 处至结尾)
	var payload []byte
	if int(commHead.Length) > headLen {
		payload = bodyBuf[headLen:commHead.Length]
	}

	return &Frame{
		CommHead:       commHead,
		ReqHead:        reqHead,
		RspHead:        rspHead,
		ExtendHeads:    extendHeads,
		Payload:        payload,
		RawWire:        rawWire,
		SessionHeadRaw: sessionHeadRaw,
	}, nil
}

// scanAndReadCommHead 扫描魔数并读齐 24 字节公共头
func scanAndReadCommHead(r io.Reader) ([]byte, error) {
	buf := make([]byte, CommHeadLen)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}

	// 若首包即对齐 "CSE.TCP"，直接返回
	if bytes.Equal(buf[0:7], []byte(MagicString)) {
		return buf, nil
	}

	// 否则启动滑动窗口扫描魔数
	magicBytes := []byte(MagicString)
	scanned := 0

	// 先在已读入的 24 字节中查找
	idx := bytes.Index(buf, magicBytes)
	if idx != -1 {
		// 找到了魔数，平移对齐
		remainedInBuf := copy(buf, buf[idx:])
		if _, err := io.ReadFull(r, buf[remainedInBuf:]); err != nil {
			return nil, err
		}
		return buf, nil
	}

	// 单字节滑动扫描
	window := make([]byte, MagicLen)
	copy(window, buf[CommHeadLen-MagicLen:])
	oneByte := make([]byte, 1)

	for scanned < MaxMagicScanBytes {
		if _, err := io.ReadFull(r, oneByte); err != nil {
			return nil, err
		}
		scanned++

		// 窗口向左滚动
		copy(window[0:MagicLen-1], window[1:MagicLen])
		window[MagicLen-1] = oneByte[0]

		if bytes.Equal(window, magicBytes) {
			// 匹配到 "CSE.TCP"，构建对齐的公共头
			copy(buf[0:MagicLen], magicBytes)
			// 读取后续 17 字节 (1 字节底座 + 16 字节公共头字段)
			if _, err := io.ReadFull(r, buf[MagicLen:CommHeadLen]); err != nil {
				return nil, err
			}
			return buf, nil
		}
	}

	return nil, fmt.Errorf("%w: failed to find %q after %d bytes scan", ErrInvalidMagic, MagicString, scanned)
}

// parseExtendHeads 循环拆解 4B 扩展公共头与数据内容
func parseExtendHeads(raw []byte) []ExtendCommHead {
	var list []ExtendCommHead
	offset := 0
	for offset+ExtendCommHeadLen <= len(raw) {
		extLen := binary.BigEndian.Uint16(raw[offset : offset+2])
		extType := raw[offset+2]
		extReserved := raw[offset+3]
		offset += ExtendCommHeadLen

		var extData []byte
		if offset+int(extLen) <= len(raw) {
			extData = raw[offset : offset+int(extLen)]
			offset += int(extLen)
		} else {
			extData = raw[offset:]
			offset = len(raw)
		}

		list = append(list, ExtendCommHead{
			HeadLen:  extLen,
			Type:     extType,
			Reserved: extReserved,
			Data:     extData,
		})
	}
	return list
}

// DecodeReqHead 反序列化 256 字节请求会话头
func DecodeReqHead(buf []byte) (*ReqHead, error) {
	if len(buf) < 136 { // 至少包含 ServiceName 和 Operation
		return nil, ErrFrameTooShort
	}
	rh := &ReqHead{
		MsgStructVer: binary.BigEndian.Uint16(buf[0:2]),
		ReqHeadLen:   binary.BigEndian.Uint16(buf[2:4]),
		Dst2ndAddrID: binary.BigEndian.Uint32(buf[4:8]),
		ServiceName:  CleanCString(buf[8:72]),
		Operation:    CleanCString(buf[72:136]),
	}
	if len(buf) >= 137 {
		rh.ReqType = buf[136]
	}
	if len(buf) >= 138 {
		rh.SubjectType = buf[137]
	}
	if len(buf) >= 140 {
		rh.RPCVer = binary.BigEndian.Uint16(buf[138:140])
	}
	if len(buf) >= 144 {
		rh.SessionID = binary.BigEndian.Uint32(buf[140:144])
	}
	if len(buf) >= 160 {
		copy(rh.TraceBuff[:], buf[144:160])
	}
	if len(buf) >= 168 {
		rh.IdempotentSeq = binary.BigEndian.Uint64(buf[160:168])
	}
	if len(buf) >= 232 {
		rh.SubjectName = CleanCString(buf[168:232])
	}
	if len(buf) >= 240 {
		rh.ClientUUID = binary.BigEndian.Uint64(buf[232:240])
	}
	if len(buf) >= 242 {
		rh.UUIDTimeoutS = binary.BigEndian.Uint16(buf[240:242])
	}
	if len(buf) >= 248 {
		rh.TimeoutSecondLimit = binary.BigEndian.Uint32(buf[244:248])
	}
	if len(buf) >= 256 {
		rh.SendTimestamp = binary.BigEndian.Uint64(buf[248:256])
	}
	return rh, nil
}

// EncodeReqHead 序列化 256 字节请求会话头至 dst 切片
func (rh *ReqHead) Encode(dst []byte) {
	for i := range dst {
		dst[i] = 0
	}
	binary.BigEndian.PutUint16(dst[0:2], rh.MsgStructVer)
	binary.BigEndian.PutUint16(dst[2:4], ReqHeadLen)
	binary.BigEndian.PutUint32(dst[4:8], rh.Dst2ndAddrID)
	CopyCString(dst[8:72], rh.ServiceName)
	CopyCString(dst[72:136], rh.Operation)
	dst[136] = rh.ReqType
	dst[137] = rh.SubjectType
	binary.BigEndian.PutUint16(dst[138:140], rh.RPCVer)
	binary.BigEndian.PutUint32(dst[140:144], rh.SessionID)
	copy(dst[144:160], rh.TraceBuff[:])
	binary.BigEndian.PutUint64(dst[160:168], rh.IdempotentSeq)
	CopyCString(dst[168:232], rh.SubjectName)
	binary.BigEndian.PutUint64(dst[232:240], rh.ClientUUID)
	binary.BigEndian.PutUint16(dst[240:242], rh.UUIDTimeoutS)
	binary.BigEndian.PutUint32(dst[244:248], rh.TimeoutSecondLimit)
	binary.BigEndian.PutUint64(dst[248:256], rh.SendTimestamp)
}

// DecodeRspHead 反序列化 200 字节应答会话头
func DecodeRspHead(buf []byte) (*RspHead, error) {
	if len(buf) < 184 { // 至少包含 StatusCode 与 ServiceName/Operation
		return nil, ErrFrameTooShort
	}
	rh := &RspHead{
		MsgStructVer: binary.BigEndian.Uint16(buf[0:2]),
		RspHeadLen:   binary.BigEndian.Uint16(buf[2:4]),
		StatusCode:   binary.BigEndian.Uint32(buf[4:8]),
	}
	copy(rh.TraceBuff[:], buf[8:24])
	rh.SvrInstID = binary.BigEndian.Uint32(buf[24:28])
	rh.Reserved = binary.BigEndian.Uint32(buf[28:32])
	rh.ClientSendTime = binary.BigEndian.Uint64(buf[32:40])
	rh.ServerRecvTime = binary.BigEndian.Uint64(buf[40:48])
	rh.ServerSendTime = binary.BigEndian.Uint64(buf[48:56])
	rh.ServiceName = CleanCString(buf[56:120])
	rh.Operation = CleanCString(buf[120:184])

	if len(buf) >= 192 {
		rh.StatusCode64 = binary.BigEndian.Uint64(buf[184:192])
	}
	if len(buf) >= 194 {
		rh.RspMsgNo = binary.BigEndian.Uint16(buf[192:194])
	}
	return rh, nil
}

// EncodeRspHead 序列化 200 字节应答会话头至 dst 切片
func (rh *RspHead) Encode(dst []byte) {
	for i := range dst {
		dst[i] = 0
	}
	binary.BigEndian.PutUint16(dst[0:2], rh.MsgStructVer)
	binary.BigEndian.PutUint16(dst[2:4], RspHeadLen)
	binary.BigEndian.PutUint32(dst[4:8], rh.StatusCode)
	copy(dst[8:24], rh.TraceBuff[:])
	binary.BigEndian.PutUint32(dst[24:28], rh.SvrInstID)
	binary.BigEndian.PutUint32(dst[28:32], rh.Reserved)
	binary.BigEndian.PutUint64(dst[32:40], rh.ClientSendTime)
	binary.BigEndian.PutUint64(dst[40:48], rh.ServerRecvTime)
	binary.BigEndian.PutUint64(dst[48:56], rh.ServerSendTime)
	CopyCString(dst[56:120], rh.ServiceName)
	CopyCString(dst[120:184], rh.Operation)
	binary.BigEndian.PutUint64(dst[184:192], rh.StatusCode64)
	binary.BigEndian.PutUint16(dst[192:194], rh.RspMsgNo)
}

// BuildReqFrame 构造一个标准的 CROS 请求报文切片 (CommHead + ReqHead + Payload)
func BuildReqFrame(sno uint64, serviceName, operation string, payload []byte) ([]byte, error) {
	payloadLen := len(payload)
	if payloadLen > MaxPayloadLen {
		return nil, ErrPayloadTooBig
	}

	totalLen := CommHeadLen + ReqHeadLen + payloadLen
	buf := make([]byte, totalLen)

	// 1. 公共头
	ch := CommHead{
		Protocol: TransportTCP,
		SNO:      sno,
		Length:   uint32(ReqHeadLen + payloadLen),
		HeadLen:  uint16(ReqHeadLen),
		MsgType:  MsgTypeReq,
		IsPacked: 1,
	}
	ch.Encode(buf[:CommHeadLen])

	// 2. 请求头
	rh := ReqHead{
		MsgStructVer:       1,
		ReqHeadLen:         ReqHeadLen,
		ServiceName:        serviceName,
		Operation:          operation,
		ReqType:            ReqTypeSync,
		TimeoutSecondLimit: DefaultTimeoutMsec,
	}
	rh.Encode(buf[CommHeadLen : CommHeadLen+ReqHeadLen])

	// 3. Payload
	if payloadLen > 0 {
		copy(buf[CommHeadLen+ReqHeadLen:], payload)
	}

	return buf, nil
}

// BuildRspFrame 构造一个标准的 CROS 应答报文切片 (CommHead + RspHead + Payload)
func BuildRspFrame(sno uint64, serviceName, operation string, statusCode uint32, payload []byte) ([]byte, error) {
	payloadLen := len(payload)
	if payloadLen > MaxPayloadLen {
		return nil, ErrPayloadTooBig
	}

	totalLen := CommHeadLen + RspHeadLen + payloadLen
	buf := make([]byte, totalLen)

	// 1. 公共头
	ch := CommHead{
		Protocol: TransportTCP,
		SNO:      sno,
		Length:   uint32(RspHeadLen + payloadLen),
		HeadLen:  uint16(RspHeadLen),
		MsgType:  MsgTypeRsp,
		IsPacked: 1,
	}
	ch.Encode(buf[:CommHeadLen])

	// 2. 应答头
	rh := RspHead{
		MsgStructVer: 1,
		RspHeadLen:   RspHeadLen,
		StatusCode:   statusCode,
		ServiceName:  serviceName,
		Operation:    operation,
	}
	rh.Encode(buf[CommHeadLen : CommHeadLen+RspHeadLen])

	// 3. Payload
	if payloadLen > 0 {
		copy(buf[CommHeadLen+RspHeadLen:], payload)
	}

	return buf, nil
}

// RebuildFrameSeqID 原地 O(1) 重写报文切片中的 ullSNO 序列号 (零内存分配)
func RebuildFrameSeqID(raw []byte, newSNO uint64) error {
	if len(raw) < CommHeadLen {
		return ErrFrameTooShort
	}
	binary.BigEndian.PutUint64(raw[8:16], newSNO)
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
