// Package protocol 封装 Protobuf 消息的确定性序列化与自定义二进制协议帧打包辅助函数。
package protocol

import (
	"fmt"

	"github.com/fugui/flowbench/internal/codec"
	"google.golang.org/protobuf/proto"
)

var deterministicMarshaller = proto.MarshalOptions{
	Deterministic: true,
}

// MarshalDeterministic 将 Protobuf 消息确定性序列化为二进制字节切片
func MarshalDeterministic(msg proto.Message) ([]byte, error) {
	if msg == nil {
		return []byte{}, nil
	}
	return deterministicMarshaller.Marshal(msg)
}

// Unmarshal 将二进制字节切片反序列化到目标 Protobuf 消息结构中
func Unmarshal(b []byte, msg proto.Message) error {
	return proto.Unmarshal(b, msg)
}

// EncodeMessageFrame 将 Protobuf 消息确定性序列化并封装为完整的 24B+Payload 二进制帧
func EncodeMessageFrame(opCode uint8, reqID uint64, flags uint8, status uint8, msg proto.Message) (*codec.Frame, error) {
	payload, err := MarshalDeterministic(msg)
	if err != nil {
		return nil, fmt.Errorf("protocol: marshal message for opcode 0x%02X failed: %w", opCode, err)
	}
	return codec.NewFrame(opCode, reqID, flags, status, payload), nil
}

// DecodeMessageFrame 从 Frame 中提取并反序列化 Protobuf 消息，同时可选校验预期 OpCode
func DecodeMessageFrame(f *codec.Frame, expectedOp uint8, target proto.Message) error {
	if expectedOp != 0 && f.Header.OpCode != expectedOp {
		return fmt.Errorf("protocol: unexpected opcode 0x%02X (%s), expected 0x%02X (%s)",
			f.Header.OpCode, codec.OpCodeName(f.Header.OpCode),
			expectedOp, codec.OpCodeName(expectedOp))
	}
	if err := Unmarshal(f.Payload, target); err != nil {
		return fmt.Errorf("protocol: unmarshal payload for opcode 0x%02X failed: %w", f.Header.OpCode, err)
	}
	return nil
}
