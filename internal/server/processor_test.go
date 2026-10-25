package server

import (
	"testing"

	"github.com/fugui/flowbench/internal/protocol/pb"
)

func TestProcessOrderQuoteCalculations(t *testing.T) {
	// 验证 SC-02 标准案例：SKU-PRO-01 (10000分, 1500g), qty=5, vip=2 (10%), CN-EAST (6%)
	// 原始小计 = 10000 * 5 = 50000
	// 折扣率 = VIP2(1000 bps) + 满5件(300 bps) = 1300 bps (13%) -> 折扣 = 6500
	// 折后净额 = 43500 (< 50000，不免运费)
	// 税费 = 43500 * 6% = 2610
	// 总重量 = 1500 * 5 = 7500g -> 首重1200 + 6*500 = 4200
	// 应付总额 = 43500 + 2610 + 4200 = 50310
	req := &pb.OrderQuoteRequest{
		UserId:          "USR-VIP-001",
		SkuId:           "SKU-PRO-01",
		Quantity:        5,
		VipLevel:        2,
		Region:          "CN-EAST",
		ClientTimestamp: 1700000100,
	}
	bResp := &pb.BackendOrderQueryResponse{
		Found:           true,
		SkuId:           "SKU-PRO-01",
		SkuName:         "企业级千兆交换机",
		BasePriceCents:  10000,
		AvailableStock:  50,
		UnitWeightGrams: 1500,
		WarehouseCode:   "WH-SH-01",
		UserCreditScore: 780,
		RiskBlacklisted: false,
	}

	resp := ProcessOrderQuote(1002, req, bResp, true, MutationNone)
	if resp.BizCode != int32(pb.BizCode_BIZ_OK) {
		t.Fatalf("expected BIZ_OK, got %d", resp.BizCode)
	}
	if resp.SubtotalCents != 50000 {
		t.Errorf("SubtotalCents: got %d, want 50000", resp.SubtotalCents)
	}
	if resp.DiscountCents != 6500 {
		t.Errorf("DiscountCents: got %d, want 6500", resp.DiscountCents)
	}
	if resp.DiscountedCents != 43500 {
		t.Errorf("DiscountedCents: got %d, want 43500", resp.DiscountedCents)
	}
	if resp.TaxCents != 2610 {
		t.Errorf("TaxCents: got %d, want 2610", resp.TaxCents)
	}
	if resp.ShippingCents != 4200 {
		t.Errorf("ShippingCents: got %d, want 4200", resp.ShippingCents)
	}
	if resp.FinalAmountCents != 50310 {
		t.Errorf("FinalAmountCents: got %d, want 50310", resp.FinalAmountCents)
	}
	if resp.RiskDecision != "APPROVED" {
		t.Errorf("RiskDecision: got %s, want APPROVED", resp.RiskDecision)
	}
	if len(resp.ProcessingDigest) != 16 {
		t.Errorf("expected 16-char hex digest, got %q", resp.ProcessingDigest)
	}

	// 验证突变模式下折扣、应付金额与摘要均发生显著变化
	mutated := ProcessOrderQuote(1002, req, bResp, true, MutationDiscountBug)
	if mutated.DiscountCents == resp.DiscountCents || mutated.FinalAmountCents == resp.FinalAmountCents || mutated.ProcessingDigest == resp.ProcessingDigest {
		t.Fatalf("expected mutation to alter discount, final amount, and digest")
	}
}
