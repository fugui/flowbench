package codec

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

// singleByteReader 模拟极端 1 字节碎片 TCP 半包读取
type singleByteReader struct {
	r io.Reader
}

func (s *singleByteReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return s.r.Read(p[:1])
}

func TestDynamicFrame_AllPresets_FragmentationAndCoalescing(t *testing.T) {
	presets := ListPresets()
	if len(presets) != 3 {
		t.Fatalf("expected 3 presets, got %d", len(presets))
	}

	for _, spec := range presets {
		t.Run(spec.PresetID, func(t *testing.T) {
			var coalescedStream bytes.Buffer
			const frameCount = 10

			for i := 0; i < frameCount; i++ {
				opCode := uint32(0x11 + i)
				seqID := uint64(10000 + i)
				payload := []byte("dynamic-protobuf-wire-payload-" + spec.PresetID)
				df, err := BuildDynamicFrame(spec, opCode, seqID, payload)
				if err != nil {
					t.Fatalf("BuildDynamicFrame failed: %v", err)
				}
				if err := WriteDynamicFrame(&coalescedStream, df); err != nil {
					t.Fatalf("WriteDynamicFrame failed: %v", err)
				}
			}

			// 使用 1 字节半包流读取连续粘连的 10 帧
			fragReader := &singleByteReader{r: bytes.NewReader(coalescedStream.Bytes())}
			for i := 0; i < frameCount; i++ {
				got, err := ReadDynamicFrame(fragReader, spec)
				if err != nil {
					t.Fatalf("frame %d ReadDynamicFrame failed: %v", i, err)
				}
				if got.OpCode != uint32(0x11+i) {
					t.Fatalf("frame %d expected opcode 0x%X, got 0x%X", i, 0x11+i, got.OpCode)
				}
				if got.SeqID != uint64(10000+i) {
					t.Fatalf("frame %d expected seq_id %d, got %d", i, 10000+i, got.SeqID)
				}
				expectedPayload := "dynamic-protobuf-wire-payload-" + spec.PresetID
				if string(got.Payload) != expectedPayload {
					t.Fatalf("frame %d payload mismatch: got %q", i, string(got.Payload))
				}
				if !got.ChecksumValid {
					t.Fatalf("frame %d checksum should be valid", i)
				}
			}
		})
	}
}

func TestDynamicFrame_FlowBenchV1_InteropWithStaticCodec(t *testing.T) {
	spec := PresetFlowBenchV1()
	payload := []byte{0x08, 0x96, 0x01, 0x12, 0x07, 'S', 'K', 'U', '-', '0', '0', '1'}

	// 1. 静态编码 -> 动态拆帧
	staticFrame := NewFrame(OpOrderQuoteReq, 88001, FlagDeterministic, StatusOK, payload)
	var buf bytes.Buffer
	if err := WriteFrame(&buf, staticFrame); err != nil {
		t.Fatalf("WriteFrame failed: %v", err)
	}

	dynFrame, err := ReadDynamicFrame(&buf, spec)
	if err != nil {
		t.Fatalf("ReadDynamicFrame from static frame failed: %v", err)
	}
	if dynFrame.OpCode != uint32(OpOrderQuoteReq) || dynFrame.SeqID != 88001 {
		t.Fatalf("unexpected decoded header: op=0x%X, seq=%d", dynFrame.OpCode, dynFrame.SeqID)
	}

	// 2. 动态自愈重写 SeqID -> 静态拆帧校验
	rebuilt, err := RebuildDynamicFrame(spec, dynFrame.HeaderRaw, 99999, payload)
	if err != nil {
		t.Fatalf("RebuildDynamicFrame failed: %v", err)
	}
	var outBuf bytes.Buffer
	if err := WriteDynamicFrame(&outBuf, rebuilt); err != nil {
		t.Fatalf("WriteDynamicFrame failed: %v", err)
	}

	decodedStatic, err := ReadFrame(&outBuf)
	if err != nil {
		t.Fatalf("static ReadFrame from rebuilt dynamic frame failed: %v", err)
	}
	if decodedStatic.Header.RequestID != 99999 {
		t.Fatalf("expected rewritten RequestID 99999, got %d", decodedStatic.Header.RequestID)
	}
	if decodedStatic.Header.Flags != FlagDeterministic {
		t.Fatalf("expected preserved Flags 0x%02X, got 0x%02X", FlagDeterministic, decodedStatic.Header.Flags)
	}
}

func TestDynamicFrame_BiasAndSkipBytesChecksum(t *testing.T) {
	spec := PresetGenericFinancialV2()
	spec.LengthField.Bias = -4
	spec.ChecksumField.PlaceholderMode = ChecksumPlaceholderSkipBytes

	df, err := BuildDynamicFrame(spec, 0x0201, 777777, []byte("test-skip-bytes-and-bias"))
	if err != nil {
		t.Fatalf("BuildDynamicFrame failed: %v", err)
	}

	rawBytes := EncodeDynamicFrameBytes(df)
	parsed, err := ReadDynamicFrame(bytes.NewReader(rawBytes), spec)
	if err != nil {
		t.Fatalf("ReadDynamicFrame failed: %v", err)
	}
	if parsed.SeqID != 777777 || string(parsed.Payload) != "test-skip-bytes-and-bias" {
		t.Fatalf("unexpected parsed frame: %+v", parsed)
	}

	// 篡改 1 比特载荷验证 Checksum 拦截
	rawBytes[len(rawBytes)-1] ^= 0x01
	if _, err := ReadDynamicFrame(bytes.NewReader(rawBytes), spec); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("expected ErrChecksumMismatch on bit flip, got %v", err)
	}
}

func TestFramingSpec_ValidateBounds(t *testing.T) {
	badSpec := PresetFlowBenchV1()
	badSpec.SeqIDField.Offset = 20 // 20 + 8 = 28 > 24 (越界)
	if err := badSpec.Validate(); !errors.Is(err, ErrInvalidFramingSpec) {
		t.Fatalf("expected ErrInvalidFramingSpec for out-of-bounds offset, got %v", err)
	}
}
