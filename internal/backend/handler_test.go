package backend

import (
	"context"
	"testing"

	"github.com/fugui/flowbench/internal/codec"
	"github.com/fugui/flowbench/internal/protocol"
	"github.com/fugui/flowbench/internal/protocol/pb"
)

func TestBackendOrderAndCreditQuery(t *testing.T) {
	h := NewHandler(NewDefaultStore(), nil)
	ctx := context.Background()

	// 1. 测试 OrderQuery
	reqFrame, err := protocol.EncodeMessageFrame(
		codec.OpBackendOrderQueryReq,
		1002,
		codec.FlagDeterministic,
		codec.StatusOK,
		&pb.BackendOrderQueryRequest{
			SkuId:  "SKU-PRO-01",
			UserId: "USR-VIP-001",
			Region: "CN-EAST",
		},
	)
	if err != nil {
		t.Fatalf("EncodeMessageFrame failed: %v", err)
	}

	respFrame, err := h.HandleFrame(ctx, reqFrame)
	if err != nil {
		t.Fatalf("HandleFrame failed: %v", err)
	}
	var orderResp pb.BackendOrderQueryResponse
	if err := protocol.DecodeMessageFrame(respFrame, codec.OpBackendOrderQueryResp, &orderResp); err != nil {
		t.Fatalf("DecodeMessageFrame failed: %v", err)
	}
	if !orderResp.Found || orderResp.BasePriceCents != 10000 || orderResp.AvailableStock != 50 || orderResp.UserCreditScore != 780 {
		t.Fatalf("unexpected BackendOrderQueryResponse: %+v", &orderResp)
	}

	// 2. 测试 CreditQuery
	creditReqFrame, err := protocol.EncodeMessageFrame(
		codec.OpBackendCreditQueryReq,
		1006,
		codec.FlagDeterministic,
		codec.StatusOK,
		&pb.BackendCreditQueryRequest{UserId: "USR-VIP-001"},
	)
	if err != nil {
		t.Fatalf("EncodeMessageFrame credit failed: %v", err)
	}
	creditRespFrame, err := h.HandleFrame(ctx, creditReqFrame)
	if err != nil {
		t.Fatalf("HandleFrame credit failed: %v", err)
	}
	var creditResp pb.BackendCreditQueryResponse
	if err := protocol.DecodeMessageFrame(creditRespFrame, codec.OpBackendCreditQueryResp, &creditResp); err != nil {
		t.Fatalf("DecodeMessageFrame credit failed: %v", err)
	}
	if !creditResp.Found || creditResp.CreditScore != 780 || creditResp.AccountAgeMonths != 36 {
		t.Fatalf("unexpected BackendCreditQueryResponse: %+v", &creditResp)
	}

	if h.Store().QueryCount() != 2 {
		t.Fatalf("expected query count 2, got %d", h.Store().QueryCount())
	}
}
