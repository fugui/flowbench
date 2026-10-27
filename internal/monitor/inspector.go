// Package monitor 提供基于 cilium/ebpf 的协议感知二进制流量监控、智能录制回放、字段级 Diff 断言
// 以及内嵌安全 Web 可视化控制台。
package monitor

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"sort"
	"strings"
	"time"

	"github.com/fugui/flowbench/internal/codec"
	"github.com/fugui/flowbench/internal/protocol"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// DecodedHeaderInfo 表示 24 字节自定义大端帧头的结构化拆解详情。
type DecodedHeaderInfo struct {
	MagicHex         string `json:"magic_hex"`
	Version          uint8  `json:"version"`
	OpCode           uint8  `json:"op_code"`
	OpCodeHex        string `json:"op_code_hex"`
	OpName           string `json:"op_name"`
	Flags            uint8  `json:"flags"`
	FlagsHex         string `json:"flags_hex"`
	Status           uint8  `json:"status"`
	StatusName       string `json:"status_name"`
	Reserved         uint16 `json:"reserved"`
	RequestID        uint64 `json:"request_id"`
	PayloadLength    uint32 `json:"payload_length"`
	CRC32Hex         string `json:"crc32_hex"`
	ComputedCRC32Hex string `json:"computed_crc32_hex"`
}

// CapturedFrameEvent 表示监控器捕获并深度解剖的一帧自定义二进制报文事件。
type CapturedFrameEvent struct {
	SeqID          uint64            `json:"seq_id"`
	Timestamp      string            `json:"timestamp"`
	Direction      string            `json:"direction"`        // INGRESS_REQ, INGRESS_RESP, EGRESS_REQ, EGRESS_RESP, EGRESS_MOCK_RESP
	Mode           string            `json:"mode"`             // RECORD, REPLAY
	Header         DecodedHeaderInfo `json:"header"`           // 24B 帧头字段字典
	HeaderBytesHex []string          `json:"header_bytes_hex"` // 24 个独立字节的十六进制切片
	CRC32Valid     bool              `json:"crc32_valid"`      // CRC32 校验是否通过
	ProtobufJSON   map[string]any    `json:"protobuf_json"`    // 反序列化后的 Protobuf 业务字段明文
	RawWireHex     string            `json:"raw_wire_hex"`     // 24B+Payload 完整十六进制字节流
	RawBase64      string            `json:"raw_base64"`       // 完整帧 Base64 编码字符串
	LatencyMs      float64           `json:"latency_ms"`
}

// FieldDiff 表示回放断言中检测到的帧头或 Protobuf 业务字段级差异。
type FieldDiff struct {
	Field    string `json:"field"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
}

// InspectFrame 将原始二进制帧解剖为包含 24B 帧头明细、CRC32 校验状态与 Protobuf 明文 JSON 的结构化对象。
func InspectFrame(f *codec.Frame, direction, mode string, seqID uint64, latencyMs float64) CapturedFrameEvent {
	rawBytes, err := codec.EncodeFrameBytes(f)
	if err != nil {
		rawBytes = make([]byte, codec.HeaderSize)
	}

	headerHexSlice := make([]string, codec.HeaderSize)
	for i := 0; i < codec.HeaderSize && i < len(rawBytes); i++ {
		headerHexSlice[i] = fmt.Sprintf("%02X", rawBytes[i])
	}

	computedCRC := crc32.ChecksumIEEE(f.Payload)
	pbMap := decodeProtobufToMap(f.Header.OpCode, f.Payload)

	return CapturedFrameEvent{
		SeqID:     seqID,
		Timestamp: time.Now().Format("15:04:05.000"),
		Direction: direction,
		Mode:      mode,
		Header: DecodedHeaderInfo{
			MagicHex:         fmt.Sprintf("0x%04X", f.Header.Magic),
			Version:          f.Header.Version,
			OpCode:           f.Header.OpCode,
			OpCodeHex:        fmt.Sprintf("0x%02X", f.Header.OpCode),
			OpName:           codec.OpCodeName(f.Header.OpCode),
			Flags:            f.Header.Flags,
			FlagsHex:         fmt.Sprintf("0x%02X", f.Header.Flags),
			Status:           f.Header.Status,
			StatusName:       codec.StatusName(f.Header.Status),
			Reserved:         f.Header.Reserved,
			RequestID:        f.Header.RequestID,
			PayloadLength:    f.Header.PayloadLen,
			CRC32Hex:         fmt.Sprintf("0x%08X", f.Header.CRC32),
			ComputedCRC32Hex: fmt.Sprintf("0x%08X", computedCRC),
		},
		HeaderBytesHex: headerHexSlice,
		CRC32Valid:     computedCRC == f.Header.CRC32,
		ProtobufJSON:   pbMap,
		RawWireHex:     strings.ToUpper(hex.EncodeToString(rawBytes)),
		RawBase64:      base64.StdEncoding.EncodeToString(rawBytes),
		LatencyMs:      latencyMs,
	}
}

func decodeProtobufToMap(opCode uint8, payload []byte) map[string]any {
	out := make(map[string]any)
	msg, err := protocol.NewMessageForOpCode(opCode)
	if err != nil {
		out["_decode_error"] = err.Error()
		return out
	}
	if err := proto.Unmarshal(payload, msg); err != nil {
		out["_unmarshal_error"] = err.Error()
		return out
	}
	jsonBytes, err := protojson.MarshalOptions{
		UseProtoNames:   true,
		EmitUnpopulated: true,
	}.Marshal(msg)
	if err != nil {
		out["_json_error"] = err.Error()
		return out
	}
	if err := json.Unmarshal(jsonBytes, &out); err != nil {
		out["_map_error"] = err.Error()
	}
	return out
}

// SemanticSignature 生成忽略帧头 RequestID 与 Flags 后的确定性业务请求签名，用于智能 Mock 语义匹配。
func SemanticSignature(f *codec.Frame) string {
	msg, err := protocol.NewMessageForOpCode(f.Header.OpCode)
	if err == nil {
		if unmarshalErr := proto.Unmarshal(f.Payload, msg); unmarshalErr == nil {
			if detBytes, marshalErr := (proto.MarshalOptions{Deterministic: true}).Marshal(msg); marshalErr == nil {
				return fmt.Sprintf("op=0x%02X|pb=%x", f.Header.OpCode, detBytes)
			}
		}
	}
	return fmt.Sprintf("op=0x%02X|raw=%x", f.Header.OpCode, f.Payload)
}

// ComputeFieldDiffs 计算录制基线响应帧与实际回放响应帧之间的帧头与 Protobuf 字段级差异。
func ComputeFieldDiffs(expectedFrame, actualFrame *codec.Frame) []FieldDiff {
	var diffs []FieldDiff

	if expectedFrame.Header.OpCode != actualFrame.Header.OpCode {
		diffs = append(diffs, FieldDiff{
			Field:    "header.op_code",
			Expected: fmt.Sprintf("0x%02X (%s)", expectedFrame.Header.OpCode, codec.OpCodeName(expectedFrame.Header.OpCode)),
			Actual:   fmt.Sprintf("0x%02X (%s)", actualFrame.Header.OpCode, codec.OpCodeName(actualFrame.Header.OpCode)),
		})
	}
	if expectedFrame.Header.Status != actualFrame.Header.Status {
		diffs = append(diffs, FieldDiff{
			Field:    "header.status",
			Expected: fmt.Sprintf("0x%02X (%s)", expectedFrame.Header.Status, codec.StatusName(expectedFrame.Header.Status)),
			Actual:   fmt.Sprintf("0x%02X (%s)", actualFrame.Header.Status, codec.StatusName(actualFrame.Header.Status)),
		})
	}
	if expectedFrame.Header.PayloadLen != actualFrame.Header.PayloadLen {
		diffs = append(diffs, FieldDiff{
			Field:    "header.payload_length",
			Expected: fmt.Sprintf("%d", expectedFrame.Header.PayloadLen),
			Actual:   fmt.Sprintf("%d", actualFrame.Header.PayloadLen),
		})
	}
	if expectedFrame.Header.CRC32 != actualFrame.Header.CRC32 {
		diffs = append(diffs, FieldDiff{
			Field:    "header.crc32",
			Expected: fmt.Sprintf("0x%08X", expectedFrame.Header.CRC32),
			Actual:   fmt.Sprintf("0x%08X", actualFrame.Header.CRC32),
		})
	}

	expMap := decodeProtobufToMap(expectedFrame.Header.OpCode, expectedFrame.Payload)
	actMap := decodeProtobufToMap(actualFrame.Header.OpCode, actualFrame.Payload)

	keySet := make(map[string]struct{})
	for k := range expMap {
		keySet[k] = struct{}{}
	}
	for k := range actMap {
		keySet[k] = struct{}{}
	}
	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		ev := formatJSONValue(expMap[k])
		av := formatJSONValue(actMap[k])
		if ev != av {
			diffs = append(diffs, FieldDiff{
				Field:    "payload." + k,
				Expected: ev,
				Actual:   av,
			})
		}
	}
	return diffs
}

func formatJSONValue(v any) string {
	if v == nil {
		return "null"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}
