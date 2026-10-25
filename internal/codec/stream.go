package codec

import (
	"fmt"
	"hash/crc32"
	"io"
)

// EncodeFrameBytes 将完整帧（24B 帧头 + Payload）组装为单一连续字节切片
func EncodeFrameBytes(f *Frame) ([]byte, error) {
	if uint32(len(f.Payload)) > MaxPayloadSize {
		return nil, fmt.Errorf("%w: %d bytes", ErrPayloadTooLarge, len(f.Payload))
	}
	h := f.Header
	if h.Magic == 0 {
		h.Magic = MagicNumber
	}
	if h.Version == 0 {
		h.Version = ProtocolVer
	}
	h.PayloadLen = uint32(len(f.Payload))
	if len(f.Payload) > 0 {
		h.CRC32 = crc32.ChecksumIEEE(f.Payload)
	} else {
		h.CRC32 = 0
	}

	headerBytes := EncodeHeader(h)
	fullBuf := make([]byte, HeaderSize+len(f.Payload))
	copy(fullBuf[:HeaderSize], headerBytes[:])
	copy(fullBuf[HeaderSize:], f.Payload)
	return fullBuf, nil
}

// WriteFrame 将 24B 帧头与 Payload 组装为单一切片后一次性写入 w，
// 确保单帧对应单次底层系统调用 Write，并处理短写情况。
func WriteFrame(w io.Writer, f *Frame) error {
	fullBuf, err := EncodeFrameBytes(f)
	if err != nil {
		return err
	}
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

// ReadFrame 使用 io.ReadFull 两阶段定长读取从 r 中完整拆出一帧，
// 自动处理 TCP 粘包与半包，并严格验证 CRC32 载荷完整性。
func ReadFrame(r io.Reader) (*Frame, error) {
	var headerBuf [HeaderSize]byte
	if _, err := io.ReadFull(r, headerBuf[:]); err != nil {
		return nil, err
	}

	header, err := DecodeHeader(headerBuf[:])
	if err != nil {
		return nil, err
	}

	if header.PayloadLen == 0 {
		if header.CRC32 != 0 {
			return nil, fmt.Errorf("%w: got 0x%08X", ErrNonZeroEmptyCRC, header.CRC32)
		}
		return &Frame{
			Header:  header,
			Payload: []byte{},
		}, nil
	}

	payload := make([]byte, header.PayloadLen)
	if _, err := io.ReadFull(r, payload); err != nil {
		if err == io.EOF {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, err
	}

	actualCRC := crc32.ChecksumIEEE(payload)
	if actualCRC != header.CRC32 {
		return nil, fmt.Errorf("%w: expected 0x%08X, got 0x%08X", ErrChecksumMismatch, header.CRC32, actualCRC)
	}

	return &Frame{
		Header:  header,
		Payload: payload,
	}, nil
}
