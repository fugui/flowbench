package protocol

import (
	"bytes"
	"testing"

	"github.com/fugui/flowbench/internal/codec"
	"github.com/fugui/flowbench/internal/protocol/pb"
)

func TestProtobufEncodeDecodeFrame(t *testing.T) {
	req := &pb.OrderQuoteRequest{
		UserId:          "USR-VIP-001",
		SkuId:           "SKU-PRO-01",
		Quantity:        5,
		VipLevel:        2,
		Region:          "CN-EAST",
		ClientTimestamp: 1700000000,
	}

	frame1, err := EncodeMessageFrame(codec.OpOrderQuoteReq, 1002, codec.FlagDeterministic, codec.StatusOK, req)
	if err != nil {
		t.Fatalf("EncodeMessageFrame failed: %v", err)
	}

	// 验证确定性序列化：连续序列化 100 次结果逐字节一致
	for i := 0; i < 100; i++ {
		frameN, err := EncodeMessageFrame(codec.OpOrderQuoteReq, 1002, codec.FlagDeterministic, codec.StatusOK, req)
		if err != nil {
			t.Fatalf("EncodeMessageFrame iteration %d failed: %v", i, err)
		}
		if !bytes.Equal(frame1.Payload, frameN.Payload) || frame1.Header.CRC32 != frameN.Header.CRC32 {
			t.Fatalf("non-deterministic serialization detected at iteration %d", i)
		}
	}

	var decoded pb.OrderQuoteRequest
	if err := DecodeMessageFrame(frame1, codec.OpOrderQuoteReq, &decoded); err != nil {
		t.Fatalf("DecodeMessageFrame failed: %v", err)
	}

	if decoded.UserId != req.UserId || decoded.SkuId != req.SkuId || decoded.Quantity != req.Quantity || decoded.VipLevel != req.VipLevel || decoded.Region != req.Region || decoded.ClientTimestamp != req.ClientTimestamp {
		t.Fatalf("decoded message mismatch: got %+v, want %+v", &decoded, req)
	}
}
