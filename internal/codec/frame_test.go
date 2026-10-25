package codec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

// oneByteReader 模拟极端 TCP 半包：每次 Read 最多仅返回 1 个字节
type oneByteReader struct {
	r io.Reader
}

func (o *oneByteReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return o.r.Read(p[:1])
}

func TestFrameRoundTrip(t *testing.T) {
	payload := []byte("flowbench-custom-protobuf-payload-2026")
	orig := NewFrame(OpOrderQuoteReq, 1001, FlagDeterministic, StatusOK, payload)

	var buf bytes.Buffer
	if err := WriteFrame(&buf, orig); err != nil {
		t.Fatalf("WriteFrame failed: %v", err)
	}

	if buf.Len() != HeaderSize+len(payload) {
		t.Fatalf("expected buffer len %d, got %d", HeaderSize+len(payload), buf.Len())
	}

	decoded, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame failed: %v", err)
	}

	if decoded.Header.Magic != MagicNumber {
		t.Errorf("Magic mismatch: got 0x%04X", decoded.Header.Magic)
	}
	if decoded.Header.Version != ProtocolVer {
		t.Errorf("Version mismatch: got 0x%02X", decoded.Header.Version)
	}
	if decoded.Header.OpCode != OpOrderQuoteReq {
		t.Errorf("OpCode mismatch: got 0x%02X", decoded.Header.OpCode)
	}
	if decoded.Header.Flags != FlagDeterministic {
		t.Errorf("Flags mismatch: got 0x%02X", decoded.Header.Flags)
	}
	if decoded.Header.RequestID != 1001 {
		t.Errorf("RequestID mismatch: got %d", decoded.Header.RequestID)
	}
	if !bytes.Equal(decoded.Payload, payload) {
		t.Errorf("Payload mismatch: got %q, want %q", decoded.Payload, payload)
	}
}

func TestEmptyPayloadFrame(t *testing.T) {
	orig := NewFrame(OpPingReq, 42, FlagNone, StatusOK, nil)
	var buf bytes.Buffer
	if err := WriteFrame(&buf, orig); err != nil {
		t.Fatalf("WriteFrame empty failed: %v", err)
	}
	decoded, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame empty failed: %v", err)
	}
	if decoded.Header.PayloadLen != 0 || len(decoded.Payload) != 0 {
		t.Fatalf("expected empty payload, got len %d", len(decoded.Payload))
	}

	// 测试空载荷但 CRC32 非零应报错
	raw := buf.Bytes()
	var badBuf [HeaderSize]byte
	copy(badBuf[:], raw)
	// 手动篡改空载荷帧的 CRC32 为非零
	rawBad := EncodeHeader(Header{
		Magic:      MagicNumber,
		Version:    ProtocolVer,
		OpCode:     OpPingReq,
		RequestID:  42,
		PayloadLen: 0,
		CRC32:      0x12345678,
	})
	if _, err := ReadFrame(bytes.NewReader(rawBad[:])); !errors.Is(err, ErrNonZeroEmptyCRC) {
		t.Fatalf("expected ErrNonZeroEmptyCRC, got %v", err)
	}
}

func TestInvalidHeaderAndCRCTampering(t *testing.T) {
	// 1. Magic 错误
	badMagic := EncodeHeader(Header{Magic: 0x1234, Version: ProtocolVer})
	if _, err := ReadFrame(bytes.NewReader(badMagic[:])); !errors.Is(err, ErrInvalidMagic) {
		t.Fatalf("expected ErrInvalidMagic, got %v", err)
	}

	// 2. Version 错误
	badVer := EncodeHeader(Header{Magic: MagicNumber, Version: 0x99})
	if _, err := ReadFrame(bytes.NewReader(badVer[:])); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("expected ErrUnsupportedVersion, got %v", err)
	}

	// 3. PayloadLen 超限 (> 4MB)
	tooLarge := EncodeHeader(Header{Magic: MagicNumber, Version: ProtocolVer, PayloadLen: MaxPayloadSize + 1})
	if _, err := ReadFrame(bytes.NewReader(tooLarge[:])); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("expected ErrPayloadTooLarge, got %v", err)
	}

	// 4. CRC32 载荷篡改 1 比特检测
	f := NewFrame(OpOrderQuoteResp, 2002, FlagNone, StatusOK, []byte("authentic-protobuf-bytes"))
	raw, err := EncodeFrameBytes(f)
	if err != nil {
		t.Fatalf("EncodeFrameBytes failed: %v", err)
	}
	raw[HeaderSize+2] ^= 0x01 // 翻转载荷中 1 个比特
	if _, err := ReadFrame(bytes.NewReader(raw)); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("expected ErrChecksumMismatch on 1-bit flip, got %v", err)
	}

	// 5. 半截载荷断连检测
	shortRaw := raw[:len(raw)-3]
	if _, err := ReadFrame(bytes.NewReader(shortRaw)); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("expected io.ErrUnexpectedEOF on truncated payload, got %v", err)
	}

	// 6. 大端字节序严格验证
	var reqIDBuf [8]byte
	binary.BigEndian.PutUint64(reqIDBuf[:], 2002)
	if !bytes.Equal(raw[8:16], reqIDBuf[:]) {
		t.Fatalf("RequestID not encoded in BigEndian")
	}
}

func TestTCPFragmentationAndCoalescing(t *testing.T) {
	// 构造 10 个不同 OpCode、不同长度的帧粘连在同一个流中
	var stream bytes.Buffer
	const numFrames = 10
	expected := make([]*Frame, numFrames)

	ops := []uint8{OpPingReq, OpPongResp, OpOrderQuoteReq, OpOrderQuoteResp, OpCreditEvalReq}
	for i := 0; i < numFrames; i++ {
		payload := bytes.Repeat([]byte{byte('A' + i)}, (i+1)*13)
		f := NewFrame(ops[i%len(ops)], uint64(5000+i), FlagDeterministic, StatusOK, payload)
		expected[i] = f
		if err := WriteFrame(&stream, f); err != nil {
			t.Fatalf("WriteFrame %d failed: %v", i, err)
		}
	}

	// 使用每次仅吐出 1 字节的 oneByteReader 同时测试「粘包 + 极端 1 字节半包拆帧」
	fragReader := &oneByteReader{r: &stream}
	for i := 0; i < numFrames; i++ {
		got, err := ReadFrame(fragReader)
		if err != nil {
			t.Fatalf("ReadFrame %d from fragmented stream failed: %v", i, err)
		}
		if got.Header.RequestID != expected[i].Header.RequestID {
			t.Fatalf("frame %d RequestID mismatch: got %d, want %d", i, got.Header.RequestID, expected[i].Header.RequestID)
		}
		if got.Header.OpCode != expected[i].Header.OpCode {
			t.Fatalf("frame %d OpCode mismatch: got 0x%02X, want 0x%02X", i, got.Header.OpCode, expected[i].Header.OpCode)
		}
		if !bytes.Equal(got.Payload, expected[i].Payload) {
			t.Fatalf("frame %d Payload mismatch", i)
		}
	}

	// 第 11 次读取应当返回干净的 io.EOF
	if _, err := ReadFrame(fragReader); !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF at end of stream, got %v", err)
	}
}
