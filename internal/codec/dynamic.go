// Package codec 提供声明式二进制帧头 DSL (FramingSpec) 与运行时动态流式拆粘包状态机。
package codec

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/adler32"
	"hash/crc32"
	"io"
	"strings"
)

// ByteOrderType 定义多字节整型字段的字节序
type ByteOrderType string

const (
	ByteOrderBigEndian    ByteOrderType = "BigEndian"
	ByteOrderLittleEndian ByteOrderType = "LittleEndian"
)

// LengthSemantics 定义长度字段的计量范围语义
type LengthSemantics string

const (
	LengthSemanticsPayloadOnly LengthSemantics = "payload_only" // 长度字段仅表示 Payload 字节数
	LengthSemanticsTotalFrame  LengthSemantics = "total_frame"  // 长度字段表示整帧总字节数 (Header + Payload)
)

// ChecksumAlgorithm 定义可插拔校验和算法枚举
type ChecksumAlgorithm string

const (
	ChecksumNone            ChecksumAlgorithm = "none"
	ChecksumCRC32IEEE       ChecksumAlgorithm = "crc32_ieee"
	ChecksumCRC32Castagnoli ChecksumAlgorithm = "crc32_castagnoli"
	ChecksumAdler32         ChecksumAlgorithm = "adler32"
)

// ChecksumScope 定义校验和覆盖的数据范围
type ChecksumScope string

const (
	ChecksumScopePayloadOnly       ChecksumScope = "payload_only"
	ChecksumScopeFullFrameNoChkSum ChecksumScope = "full_frame_except_checksum"
)

// ChecksumPlaceholderMode 定义全帧校验和计算时校验和字段自身的占位方式
type ChecksumPlaceholderMode string

const (
	ChecksumPlaceholderZeroFill  ChecksumPlaceholderMode = "zero_fill"  // 将校验和字段区间置 0 参与计算
	ChecksumPlaceholderSkipBytes ChecksumPlaceholderMode = "skip_bytes" // 跳过校验和字段区间拼接计算
)

var (
	ErrInvalidFramingSpec = errors.New("codec: invalid framing specification")
	castagnoliTable       = crc32.MakeTable(crc32.Castagnoli)
)

// MagicFieldSpec 定义魔数字段探测规则
type MagicFieldSpec struct {
	Enabled  bool   `json:"enabled"`
	Offset   int    `json:"offset"`
	Length   int    `json:"length"`    // 1 | 2 | 4
	ValueHex string `json:"value_hex"` // 如 "0xFB01" 或 "0xAA55"
}

// LengthFieldSpec 定义帧长字段提取与换算规则
type LengthFieldSpec struct {
	Offset          int             `json:"offset"`
	Width           int             `json:"width"`             // 1 | 2 | 4
	Semantics       LengthSemantics `json:"semantics"`         // "payload_only" | "total_frame"
	Bias            int             `json:"bias"`              // 有符号长度补偿值
	MaxAllowedBytes uint32          `json:"max_allowed_bytes"` // 防 OOM 载荷上限 (默认 4MB)
}

// IntFieldSpec 定义操作码或序列号整型字段的位置与宽度
type IntFieldSpec struct {
	Offset int    `json:"offset"`
	Width  int    `json:"width"` // 1 | 2 | 4 | 8
	Type   string `json:"type"`  // "uint8" | "uint16" | "uint32" | "uint64"
}

// ChecksumFieldSpec 定义校验和字段规则與自愈重算策略
type ChecksumFieldSpec struct {
	Enabled         bool                    `json:"enabled"`
	Offset          int                     `json:"offset"`
	Width           int                     `json:"width"`            // 固定 4 字节 (uint32)
	Algorithm       ChecksumAlgorithm       `json:"algorithm"`        // "none" | "crc32_ieee" | "crc32_castagnoli" | "adler32"
	Scope           ChecksumScope           `json:"scope"`            // "payload_only" | "full_frame_except_checksum"
	PlaceholderMode ChecksumPlaceholderMode `json:"placeholder_mode"` // "zero_fill" | "skip_bytes"
}

// FramingSpec 定义通用私有二进制协议帧头 DSL 规范
type FramingSpec struct {
	PresetID       string            `json:"preset_id"`
	ProtocolName   string            `json:"protocol_name"`
	Description    string            `json:"description,omitempty"`
	ByteOrder      ByteOrderType     `json:"byte_order"`       // "BigEndian" | "LittleEndian"
	MinHeaderBytes int               `json:"min_header_bytes"` // 定长帧头字节数，必须等于 PayloadOffset
	PayloadOffset  int               `json:"payload_offset"`   // Protobuf 载荷起始字节偏移
	Magic          MagicFieldSpec    `json:"magic"`
	LengthField    LengthFieldSpec   `json:"length_field"`
	OpCodeField    IntFieldSpec      `json:"opcode_field"`
	SeqIDField     IntFieldSpec      `json:"seq_id_field"`
	ChecksumField  ChecksumFieldSpec `json:"checksum_field"`
}

// DynamicFrame 表示按 FramingSpec 解析或构造出的通用二进制协议帧
type DynamicFrame struct {
	Spec          FramingSpec `json:"spec"`
	HeaderRaw     []byte      `json:"header_raw"`
	Payload       []byte      `json:"payload"`
	MagicHex      string      `json:"magic_hex"`
	OpCode        uint32      `json:"op_code"`
	SeqID         uint64      `json:"seq_id"`
	RawLengthVal  uint32      `json:"raw_length_val"`
	PayloadLen    uint32      `json:"payload_len"`
	Checksum      uint32      `json:"checksum"`
	ChecksumValid bool        `json:"checksum_valid"`
}

// Validate 严格校验 FramingSpec 各字段偏移量、宽度与防 OOM 边界的合法性
func (s *FramingSpec) Validate() error {
	if s.ByteOrder != ByteOrderBigEndian && s.ByteOrder != ByteOrderLittleEndian {
		return fmt.Errorf("%w: unsupported byte_order %q", ErrInvalidFramingSpec, s.ByteOrder)
	}
	if s.MinHeaderBytes < 4 || s.MinHeaderBytes > 128 {
		return fmt.Errorf("%w: min_header_bytes (%d) must be in [4, 128]", ErrInvalidFramingSpec, s.MinHeaderBytes)
	}
	if s.PayloadOffset == 0 {
		s.PayloadOffset = s.MinHeaderBytes
	}
	if s.PayloadOffset != s.MinHeaderBytes {
		return fmt.Errorf("%w: payload_offset (%d) must equal min_header_bytes (%d)", ErrInvalidFramingSpec, s.PayloadOffset, s.MinHeaderBytes)
	}
	if s.LengthField.MaxAllowedBytes == 0 {
		s.LengthField.MaxAllowedBytes = MaxPayloadSize
	}
	if s.LengthField.MaxAllowedBytes > 16*1024*1024 {
		return fmt.Errorf("%w: max_allowed_bytes (%d) exceeds 16MB hard limit", ErrInvalidFramingSpec, s.LengthField.MaxAllowedBytes)
	}

	checkBounds := func(name string, off, width int, allowedWidths ...int) error {
		validW := false
		for _, w := range allowedWidths {
			if width == w {
				validW = true
				break
			}
		}
		if !validW {
			return fmt.Errorf("%w: %s invalid width %d", ErrInvalidFramingSpec, name, width)
		}
		if off < 0 || off+width > s.MinHeaderBytes {
			return fmt.Errorf("%w: %s [%d..%d] out of header bounds [0..%d]", ErrInvalidFramingSpec, name, off, off+width, s.MinHeaderBytes)
		}
		return nil
	}

	if s.Magic.Enabled {
		if err := checkBounds("magic", s.Magic.Offset, s.Magic.Length, 1, 2, 4); err != nil {
			return err
		}
		if _, err := parseHexBytes(s.Magic.ValueHex, s.Magic.Length); err != nil {
			return fmt.Errorf("%w: invalid magic.value_hex: %v", ErrInvalidFramingSpec, err)
		}
	}
	if err := checkBounds("length_field", s.LengthField.Offset, s.LengthField.Width, 1, 2, 4); err != nil {
		return err
	}
	if s.LengthField.Semantics != LengthSemanticsPayloadOnly && s.LengthField.Semantics != LengthSemanticsTotalFrame {
		return fmt.Errorf("%w: invalid length_field.semantics %q", ErrInvalidFramingSpec, s.LengthField.Semantics)
	}
	if err := checkBounds("opcode_field", s.OpCodeField.Offset, s.OpCodeField.Width, 1, 2, 4); err != nil {
		return err
	}
	if err := checkBounds("seq_id_field", s.SeqIDField.Offset, s.SeqIDField.Width, 2, 4, 8); err != nil {
		return err
	}
	if s.ChecksumField.Enabled && s.ChecksumField.Algorithm != ChecksumNone {
		if err := checkBounds("checksum_field", s.ChecksumField.Offset, s.ChecksumField.Width, 4); err != nil {
			return err
		}
		if s.ChecksumField.Scope == "" {
			s.ChecksumField.Scope = ChecksumScopePayloadOnly
		}
		if s.ChecksumField.PlaceholderMode == "" {
			s.ChecksumField.PlaceholderMode = ChecksumPlaceholderZeroFill
		}
	}
	return nil
}

// PresetFlowBenchV1 返回默认内置的 24B 大端 FlowBench v1 协议规范
func PresetFlowBenchV1() FramingSpec {
	return FramingSpec{
		PresetID:       "flowbench-v1",
		ProtocolName:   "FlowBench v1 (24B Default)",
		Description:    "24B 大端定长帧头：Magic(0xFB01) + OpCode(1B) + RequestID(8B) + PayloadLen(4B) + IEEE CRC32(4B)",
		ByteOrder:      ByteOrderBigEndian,
		MinHeaderBytes: 24,
		PayloadOffset:  24,
		Magic: MagicFieldSpec{
			Enabled:  true,
			Offset:   0,
			Length:   2,
			ValueHex: "0xFB01",
		},
		OpCodeField: IntFieldSpec{
			Offset: 3,
			Width:  1,
			Type:   "uint8",
		},
		SeqIDField: IntFieldSpec{
			Offset: 8,
			Width:  8,
			Type:   "uint64",
		},
		LengthField: LengthFieldSpec{
			Offset:          16,
			Width:           4,
			Semantics:       LengthSemanticsPayloadOnly,
			Bias:            0,
			MaxAllowedBytes: MaxPayloadSize,
		},
		ChecksumField: ChecksumFieldSpec{
			Enabled:         true,
			Offset:          20,
			Width:           4,
			Algorithm:       ChecksumCRC32IEEE,
			Scope:           ChecksumScopePayloadOnly,
			PlaceholderMode: ChecksumPlaceholderZeroFill,
		},
	}
}

// PresetGenericFinancialV2 返回 20B 大端金融交易网关协议规范（整帧长度 + 全帧 Castagnoli CRC32）
func PresetGenericFinancialV2() FramingSpec {
	return FramingSpec{
		PresetID:       "generic-financial-v2",
		ProtocolName:   "FinanceTradeProto v2 (20B Full-Frame CRC32C)",
		Description:    "20B 大端金融网关帧头：Magic(0xAA55) + TotalLen(4B) + OpCode(2B) + SeqID(8B) + Castagnoli CRC32(4B, 整帧置零校验)",
		ByteOrder:      ByteOrderBigEndian,
		MinHeaderBytes: 20,
		PayloadOffset:  20,
		Magic: MagicFieldSpec{
			Enabled:  true,
			Offset:   0,
			Length:   2,
			ValueHex: "0xAA55",
		},
		LengthField: LengthFieldSpec{
			Offset:          2,
			Width:           4,
			Semantics:       LengthSemanticsTotalFrame,
			Bias:            0,
			MaxAllowedBytes: MaxPayloadSize,
		},
		OpCodeField: IntFieldSpec{
			Offset: 6,
			Width:  2,
			Type:   "uint16",
		},
		SeqIDField: IntFieldSpec{
			Offset: 8,
			Width:  8,
			Type:   "uint64",
		},
		ChecksumField: ChecksumFieldSpec{
			Enabled:         true,
			Offset:          16,
			Width:           4,
			Algorithm:       ChecksumCRC32Castagnoli,
			Scope:           ChecksumScopeFullFrameNoChkSum,
			PlaceholderMode: ChecksumPlaceholderZeroFill,
		},
	}
}

// PresetGamingSyncV1 返回 12B 小端低延迟帧同步协议规范（无魔数 + Adler32 校验）
func PresetGamingSyncV1() FramingSpec {
	return FramingSpec{
		PresetID:       "gaming-sync-v1",
		ProtocolName:   "GamingSync v1 (12B LittleEndian Adler32)",
		Description:    "12B 小端紧凑游戏帧头：PayloadLen(2B) + OpCode(2B) + SeqID(4B) + Adler32(4B)",
		ByteOrder:      ByteOrderLittleEndian,
		MinHeaderBytes: 12,
		PayloadOffset:  12,
		Magic: MagicFieldSpec{
			Enabled: false,
		},
		LengthField: LengthFieldSpec{
			Offset:          0,
			Width:           2,
			Semantics:       LengthSemanticsPayloadOnly,
			Bias:            0,
			MaxAllowedBytes: 65535,
		},
		OpCodeField: IntFieldSpec{
			Offset: 2,
			Width:  2,
			Type:   "uint16",
		},
		SeqIDField: IntFieldSpec{
			Offset: 4,
			Width:  4,
			Type:   "uint32",
		},
		ChecksumField: ChecksumFieldSpec{
			Enabled:         true,
			Offset:          8,
			Width:           4,
			Algorithm:       ChecksumAdler32,
			Scope:           ChecksumScopePayloadOnly,
			PlaceholderMode: ChecksumPlaceholderZeroFill,
		},
	}
}

// ListPresets 返回平台内置的所有标准私有协议模板
func ListPresets() []FramingSpec {
	return []FramingSpec{
		PresetFlowBenchV1(),
		PresetGenericFinancialV2(),
		PresetGamingSyncV1(),
	}
}

// GetPreset 根据模板 ID 查找预置协议规范
func GetPreset(presetID string) (FramingSpec, bool) {
	for _, p := range ListPresets() {
		if p.PresetID == presetID {
			return p, true
		}
	}
	return FramingSpec{}, false
}

// ReadDynamicFrame 根据 FramingSpec 声明式规范从流 r 中拆解出一帧完整报文（免疫粘包与半包）
func ReadDynamicFrame(r io.Reader, spec FramingSpec) (*DynamicFrame, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}

	headerBuf := make([]byte, spec.MinHeaderBytes)
	if _, err := io.ReadFull(r, headerBuf); err != nil {
		return nil, err
	}

	var magicHex string
	if spec.Magic.Enabled {
		expectedMagic, err := parseHexBytes(spec.Magic.ValueHex, spec.Magic.Length)
		if err != nil {
			return nil, err
		}
		actualMagic := headerBuf[spec.Magic.Offset : spec.Magic.Offset+spec.Magic.Length]
		magicHex = "0x" + strings.ToUpper(hex.EncodeToString(actualMagic))
		if !bytes.Equal(actualMagic, expectedMagic) {
			return nil, fmt.Errorf("%w: expected 0x%X, got %s", ErrInvalidMagic, expectedMagic, magicHex)
		}
	}

	// 若为 flowbench-v1 协议，额外检查 Version (Offset 2 == 0x01)
	if spec.PresetID == "flowbench-v1" && len(headerBuf) >= 3 && headerBuf[2] != ProtocolVer {
		return nil, fmt.Errorf("%w: got 0x%02X", ErrUnsupportedVersion, headerBuf[2])
	}

	rawLen := readUint(headerBuf, spec.LengthField.Offset, spec.LengthField.Width, spec.ByteOrder)
	headerDeduction := int64(0)
	if spec.LengthField.Semantics == LengthSemanticsTotalFrame {
		headerDeduction = int64(spec.PayloadOffset)
	}
	calcPayloadLen := int64(rawLen) + int64(spec.LengthField.Bias) - headerDeduction
	if calcPayloadLen < 0 || uint64(calcPayloadLen) > uint64(spec.LengthField.MaxAllowedBytes) {
		return nil, fmt.Errorf("%w: calculated payload length %d bytes (limit %d)", ErrPayloadTooLarge, calcPayloadLen, spec.LengthField.MaxAllowedBytes)
	}

	payload := make([]byte, calcPayloadLen)
	if calcPayloadLen > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			if errors.Is(err, io.EOF) {
				return nil, io.ErrUnexpectedEOF
			}
			return nil, err
		}
	}

	opCode := uint32(readUint(headerBuf, spec.OpCodeField.Offset, spec.OpCodeField.Width, spec.ByteOrder))
	seqID := readUint(headerBuf, spec.SeqIDField.Offset, spec.SeqIDField.Width, spec.ByteOrder)

	var checksum uint32
	checksumValid := true
	if spec.ChecksumField.Enabled && spec.ChecksumField.Algorithm != ChecksumNone {
		checksum = uint32(readUint(headerBuf, spec.ChecksumField.Offset, spec.ChecksumField.Width, spec.ByteOrder))
		if calcPayloadLen == 0 && spec.ChecksumField.Scope == ChecksumScopePayloadOnly {
			if checksum != 0 {
				return nil, fmt.Errorf("%w: got 0x%08X", ErrNonZeroEmptyCRC, checksum)
			}
		} else {
			expectedChk := ComputeDynamicChecksum(spec, headerBuf, payload)
			if checksum != expectedChk {
				return nil, fmt.Errorf("%w: expected 0x%08X, got 0x%08X", ErrChecksumMismatch, checksum, expectedChk)
			}
		}
	}

	return &DynamicFrame{
		Spec:          spec,
		HeaderRaw:     headerBuf,
		Payload:       payload,
		MagicHex:      magicHex,
		OpCode:        opCode,
		SeqID:         seqID,
		RawLengthVal:  uint32(rawLen),
		PayloadLen:    uint32(calcPayloadLen),
		Checksum:      checksum,
		ChecksumValid: checksumValid,
	}, nil
}

// BuildDynamicFrame 按 FramingSpec 构造一个全新的二进制协议帧并自动计算长度与校验和
func BuildDynamicFrame(spec FramingSpec, opCode uint32, seqID uint64, payload []byte) (*DynamicFrame, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	headerBuf := make([]byte, spec.MinHeaderBytes)
	if spec.Magic.Enabled {
		magicBytes, err := parseHexBytes(spec.Magic.ValueHex, spec.Magic.Length)
		if err != nil {
			return nil, err
		}
		copy(headerBuf[spec.Magic.Offset:spec.Magic.Offset+spec.Magic.Length], magicBytes)
	}
	if spec.PresetID == "flowbench-v1" && len(headerBuf) >= 3 {
		headerBuf[2] = ProtocolVer
	}
	writeUint(headerBuf, spec.OpCodeField.Offset, spec.OpCodeField.Width, spec.ByteOrder, uint64(opCode))
	return RebuildDynamicFrame(spec, headerBuf, seqID, payload)
}

// RebuildDynamicFrame 在保留原始帧头其他标志位的基础上，动态自愈重写序列号、更新载荷并重算长度与 Checksum
func RebuildDynamicFrame(spec FramingSpec, origHeader []byte, newSeqID uint64, newPayload []byte) (*DynamicFrame, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	if uint32(len(newPayload)) > spec.LengthField.MaxAllowedBytes {
		return nil, fmt.Errorf("%w: %d bytes", ErrPayloadTooLarge, len(newPayload))
	}

	headerBuf := make([]byte, spec.MinHeaderBytes)
	if len(origHeader) >= spec.MinHeaderBytes {
		copy(headerBuf, origHeader[:spec.MinHeaderBytes])
	} else {
		copy(headerBuf, origHeader)
	}

	if spec.Magic.Enabled {
		magicBytes, err := parseHexBytes(spec.Magic.ValueHex, spec.Magic.Length)
		if err != nil {
			return nil, err
		}
		copy(headerBuf[spec.Magic.Offset:spec.Magic.Offset+spec.Magic.Length], magicBytes)
	}

	// 逆运算写入 LengthField
	headerAddition := int64(0)
	if spec.LengthField.Semantics == LengthSemanticsTotalFrame {
		headerAddition = int64(spec.PayloadOffset)
	}
	rawLenVal := int64(len(newPayload)) - int64(spec.LengthField.Bias) + headerAddition
	if rawLenVal < 0 {
		return nil, fmt.Errorf("%w: negative encoded length %d", ErrInvalidFramingSpec, rawLenVal)
	}
	writeUint(headerBuf, spec.LengthField.Offset, spec.LengthField.Width, spec.ByteOrder, uint64(rawLenVal))

	// 写入新的 SeqID
	writeUint(headerBuf, spec.SeqIDField.Offset, spec.SeqIDField.Width, spec.ByteOrder, newSeqID)

	copiedPayload := make([]byte, len(newPayload))
	copy(copiedPayload, newPayload)

	// 重新计算并写入 Checksum
	var chk uint32
	if spec.ChecksumField.Enabled && spec.ChecksumField.Algorithm != ChecksumNone {
		if len(copiedPayload) == 0 && spec.ChecksumField.Scope == ChecksumScopePayloadOnly {
			chk = 0
		} else {
			chk = ComputeDynamicChecksum(spec, headerBuf, copiedPayload)
		}
		writeUint(headerBuf, spec.ChecksumField.Offset, spec.ChecksumField.Width, spec.ByteOrder, uint64(chk))
	}

	var magicHex string
	if spec.Magic.Enabled {
		magicHex = "0x" + strings.ToUpper(hex.EncodeToString(headerBuf[spec.Magic.Offset:spec.Magic.Offset+spec.Magic.Length]))
	}
	opCode := uint32(readUint(headerBuf, spec.OpCodeField.Offset, spec.OpCodeField.Width, spec.ByteOrder))

	return &DynamicFrame{
		Spec:          spec,
		HeaderRaw:     headerBuf,
		Payload:       copiedPayload,
		MagicHex:      magicHex,
		OpCode:        opCode,
		SeqID:         newSeqID,
		RawLengthVal:  uint32(rawLenVal),
		PayloadLen:    uint32(len(copiedPayload)),
		Checksum:      chk,
		ChecksumValid: true,
	}, nil
}

// EncodeDynamicFrameBytes 将 DynamicFrame 组装为连续的字节流切片
func EncodeDynamicFrameBytes(df *DynamicFrame) []byte {
	buf := make([]byte, len(df.HeaderRaw)+len(df.Payload))
	copy(buf[:len(df.HeaderRaw)], df.HeaderRaw)
	copy(buf[len(df.HeaderRaw):], df.Payload)
	return buf
}

// WriteDynamicFrame 将 DynamicFrame 一次性原子写入 w（处理短写）
func WriteDynamicFrame(w io.Writer, df *DynamicFrame) error {
	fullBuf := EncodeDynamicFrameBytes(df)
	totalWritten := 0
	for totalWritten < len(fullBuf) {
		n, err := w.Write(fullBuf[totalWritten:])
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		totalWritten += n
	}
	return nil
}

// ComputeDynamicChecksum 根据 FramingSpec 配置计算校验和
func ComputeDynamicChecksum(spec FramingSpec, headerBuf []byte, payload []byte) uint32 {
	if !spec.ChecksumField.Enabled || spec.ChecksumField.Algorithm == ChecksumNone {
		return 0
	}

	var dataToHash []byte
	if spec.ChecksumField.Scope == ChecksumScopeFullFrameNoChkSum {
		chkOff := spec.ChecksumField.Offset
		chkEnd := chkOff + spec.ChecksumField.Width
		if spec.ChecksumField.PlaceholderMode == ChecksumPlaceholderSkipBytes {
			dataToHash = make([]byte, 0, len(headerBuf)-spec.ChecksumField.Width+len(payload))
			dataToHash = append(dataToHash, headerBuf[:chkOff]...)
			dataToHash = append(dataToHash, headerBuf[chkEnd:]...)
			dataToHash = append(dataToHash, payload...)
		} else {
			// 默认 zero_fill 模式：将校验和字段区间临时置 0 参与计算
			dataToHash = make([]byte, len(headerBuf)+len(payload))
			copy(dataToHash[:len(headerBuf)], headerBuf)
			for i := chkOff; i < chkEnd && i < len(headerBuf); i++ {
				dataToHash[i] = 0
			}
			copy(dataToHash[len(headerBuf):], payload)
		}
	} else {
		dataToHash = payload
	}

	switch spec.ChecksumField.Algorithm {
	case ChecksumCRC32IEEE:
		return crc32.ChecksumIEEE(dataToHash)
	case ChecksumCRC32Castagnoli:
		return crc32.Checksum(dataToHash, castagnoliTable)
	case ChecksumAdler32:
		return adler32.Checksum(dataToHash)
	default:
		return 0
	}
}

func parseHexBytes(hexStr string, expectedLen int) ([]byte, error) {
	cleaned := strings.TrimSpace(hexStr)
	cleaned = strings.TrimPrefix(strings.TrimPrefix(cleaned, "0x"), "0X")
	raw, err := hex.DecodeString(cleaned)
	if err != nil {
		return nil, err
	}
	if len(raw) != expectedLen {
		return nil, fmt.Errorf("expected %d bytes hex, got %d bytes", expectedLen, len(raw))
	}
	return raw, nil
}

func readUint(buf []byte, offset, width int, order ByteOrderType) uint64 {
	slice := buf[offset : offset+width]
	isLE := order == ByteOrderLittleEndian
	switch width {
	case 1:
		return uint64(slice[0])
	case 2:
		if isLE {
			return uint64(binary.LittleEndian.Uint16(slice))
		}
		return uint64(binary.BigEndian.Uint16(slice))
	case 4:
		if isLE {
			return uint64(binary.LittleEndian.Uint32(slice))
		}
		return uint64(binary.BigEndian.Uint32(slice))
	case 8:
		if isLE {
			return binary.LittleEndian.Uint64(slice)
		}
		return binary.BigEndian.Uint64(slice)
	default:
		return 0
	}
}

func writeUint(buf []byte, offset, width int, order ByteOrderType, val uint64) {
	slice := buf[offset : offset+width]
	isLE := order == ByteOrderLittleEndian
	switch width {
	case 1:
		slice[0] = byte(val)
	case 2:
		if isLE {
			binary.LittleEndian.PutUint16(slice, uint16(val))
		} else {
			binary.BigEndian.PutUint16(slice, uint16(val))
		}
	case 4:
		if isLE {
			binary.LittleEndian.PutUint32(slice, uint32(val))
		} else {
			binary.BigEndian.PutUint32(slice, uint32(val))
		}
	case 8:
		if isLE {
			binary.LittleEndian.PutUint64(slice, val)
		} else {
			binary.BigEndian.PutUint64(slice, val)
		}
	}
}
