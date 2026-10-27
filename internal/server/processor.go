package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/fugui/flowbench/internal/protocol/pb"
)

const defaultDeterministicEpoch = int64(1700000000)

// ValidateOrderQuoteReq 在发起下游 Backend 网络调用前执行本地前置参数校验
func ValidateOrderQuoteReq(req *pb.OrderQuoteRequest) error {
	if strings.TrimSpace(req.UserId) == "" {
		return errors.New("user_id must not be empty")
	}
	if strings.TrimSpace(req.SkuId) == "" {
		return errors.New("sku_id must not be empty")
	}
	if req.Quantity <= 0 || req.Quantity > 1000 {
		return fmt.Errorf("quantity must be between 1 and 1000, got %d", req.Quantity)
	}
	if req.VipLevel < 0 || req.VipLevel > 3 {
		return fmt.Errorf("vip_level must be between 0 and 3, got %d", req.VipLevel)
	}
	return nil
}

// ValidateCreditEvalReq 在发起下游 Backend 调用前校验授信评估请求参数
func ValidateCreditEvalReq(req *pb.CreditEvalRequest) error {
	if strings.TrimSpace(req.UserId) == "" {
		return errors.New("user_id must not be empty")
	}
	if req.RequestedLimitCents <= 0 {
		return fmt.Errorf("requested_limit_cents must be positive, got %d", req.RequestedLimitCents)
	}
	if req.AnnualIncomeCents <= 0 {
		return fmt.Errorf("annual_income_cents must be positive, got %d", req.AnnualIncomeCents)
	}
	if req.ExistingDebtCents < 0 {
		return fmt.Errorf("existing_debt_cents must not be negative, got %d", req.ExistingDebtCents)
	}
	return nil
}

// BuildPrecheckRejectedQuoteResp 构造前置参数校验失败时的短路拦截响应（0 次 Backend 调用）
func BuildPrecheckRejectedQuoteResp(reqID uint64, req *pb.OrderQuoteRequest, reason string, deterministic bool) *pb.OrderQuoteResponse {
	processedAt, traceTag := resolveClockAndTrace(reqID, req.ClientTimestamp, deterministic)
	bizCode := int32(pb.BizCode_BIZ_INVALID_ARGUMENT)
	decision := "REJECTED_PRECHECK"
	digest := computeOrderDigest(reqID, req.UserId, req.SkuId, req.Quantity, 0, 0, 0, 0, 0, bizCode, decision, "")

	return &pb.OrderQuoteResponse{
		BizCode:              bizCode,
		BizMessage:           "Pre-validation failed: " + reason,
		SkuName:              "",
		UnitPriceCents:       0,
		SubtotalCents:        0,
		DiscountCents:        0,
		DiscountedCents:      0,
		TaxCents:             0,
		ShippingCents:        0,
		FinalAmountCents:     0,
		RiskDecision:         decision,
		FulfillmentWarehouse: "",
		ProcessingDigest:     digest,
		TraceTag:             traceTag,
		ProcessedAtUnix:      processedAt,
	}
}

// ProcessOrderQuote 执行 Server 核心四步业务加工（阶梯+VIP折算、区域税费+续重运费、综合风控决策、SHA-256 摘要）
func ProcessOrderQuote(
	reqID uint64,
	req *pb.OrderQuoteRequest,
	bResp *pb.BackendOrderQueryResponse,
	deterministic bool,
	faultMutation string,
) *pb.OrderQuoteResponse {
	processedAt, traceTag := resolveClockAndTrace(reqID, req.ClientTimestamp, deterministic)

	if !bResp.Found {
		bizCode := int32(pb.BizCode_BIZ_NOT_FOUND)
		decision := "REJECTED_NOT_FOUND"
		digest := computeOrderDigest(reqID, req.UserId, req.SkuId, req.Quantity, 0, 0, 0, 0, 0, bizCode, decision, "")
		return &pb.OrderQuoteResponse{
			BizCode:          bizCode,
			BizMessage:       "SKU not found in backend catalog",
			RiskDecision:     decision,
			ProcessingDigest: digest,
			TraceTag:         traceTag,
			ProcessedAtUnix:  processedAt,
		}
	}

	// 算子 ①：原始小计与阶梯+VIP 双重折扣核算 (定点 int64 分与基点运算)
	subtotalCents := bResp.BasePriceCents * int64(req.Quantity)

	var vipDiscountBps int64
	switch req.VipLevel {
	case 1:
		vipDiscountBps = 500 // 5%
	case 2:
		vipDiscountBps = 1000 // 10%
	case 3:
		vipDiscountBps = 1500 // 15%
	default:
		vipDiscountBps = 0
	}

	var qtyDiscountBps int64
	if req.Quantity >= 20 {
		qtyDiscountBps = 600 // 6%
	} else if req.Quantity >= 5 {
		qtyDiscountBps = 300 // 3%
	}

	totalBps := vipDiscountBps + qtyDiscountBps
	discountCents := (subtotalCents * totalBps) / 10000

	// 若开启回归突变测试开关，人为多减 100 分（1.00 元），检验回放断言能否拦截
	if faultMutation == MutationDiscountBug {
		discountCents += 100
	}
	if discountCents > subtotalCents {
		discountCents = subtotalCents
	}
	discountedCents := subtotalCents - discountCents

	// 算子 ②：区域税费与重量阶梯履约运费核算
	var taxRateBps int64
	switch strings.ToUpper(strings.TrimSpace(req.Region)) {
	case "CN-EAST", "CN-NORTH", "CN-SOUTH":
		taxRateBps = 600 // 6%
	case "US-WEST":
		taxRateBps = 825 // 8.25%
	case "EU-CENTRAL":
		taxRateBps = 1900 // 19%
	default:
		taxRateBps = 1000 // 10%
	}
	taxCents := (discountedCents * taxRateBps) / 10000

	totalWeightGrams := int64(bResp.UnitWeightGrams) * int64(req.Quantity)
	var shippingCents int64
	isDomestic := strings.HasPrefix(strings.ToUpper(strings.TrimSpace(req.Region)), "CN-")
	if isDomestic && discountedCents >= 50000 {
		shippingCents = 0
	} else {
		extraWeight := totalWeightGrams - 1000
		if extraWeight < 0 {
			extraWeight = 0
		}
		shippingCents = 1200 + (extraWeight/1000)*500
	}

	finalAmountCents := discountedCents + taxCents + shippingCents

	// 算子 ③：库存核验与多维综合风控决策机
	bizCode := int32(pb.BizCode_BIZ_OK)
	bizMessage := "Order quote processed and approved"
	riskDecision := "APPROVED"
	warehouse := bResp.WarehouseCode

	if bResp.RiskBlacklisted {
		bizCode = int32(pb.BizCode_BIZ_RISK_REJECTED)
		bizMessage = "User is in risk blacklist"
		riskDecision = "REJECTED_BLACKLIST"
		finalAmountCents = 0
		warehouse = ""
	} else if req.Quantity > bResp.AvailableStock {
		bizCode = int32(pb.BizCode_BIZ_OUT_OF_STOCK)
		bizMessage = fmt.Sprintf("Insufficient stock: requested %d, available %d", req.Quantity, bResp.AvailableStock)
		riskDecision = "REJECTED_STOCK"
		finalAmountCents = 0
		warehouse = ""
	} else if bResp.UserCreditScore < 550 && finalAmountCents > 20000 {
		bizCode = int32(pb.BizCode_BIZ_RISK_REJECTED)
		bizMessage = fmt.Sprintf("Credit score %d too low for high-value order (%d cents)", bResp.UserCreditScore, finalAmountCents)
		riskDecision = "REJECTED_LOW_CREDIT"
		finalAmountCents = 0
		warehouse = ""
	} else if bResp.UserCreditScore < 650 || finalAmountCents > 100000 {
		bizCode = int32(pb.BizCode_BIZ_OK)
		bizMessage = "Order requires manual risk review"
		riskDecision = "MANUAL_REVIEW"
	}

	// 算子 ④：计算 SHA-256 前 16 位十六进制加工摘要
	digest := computeOrderDigest(
		reqID, req.UserId, req.SkuId, req.Quantity,
		subtotalCents, discountCents, taxCents, shippingCents, finalAmountCents,
		bizCode, riskDecision, warehouse,
	)

	return &pb.OrderQuoteResponse{
		BizCode:              bizCode,
		BizMessage:           bizMessage,
		SkuName:              bResp.SkuName,
		UnitPriceCents:       bResp.BasePriceCents,
		SubtotalCents:        subtotalCents,
		DiscountCents:        discountCents,
		DiscountedCents:      discountedCents,
		TaxCents:             taxCents,
		ShippingCents:        shippingCents,
		FinalAmountCents:     finalAmountCents,
		RiskDecision:         riskDecision,
		FulfillmentWarehouse: warehouse,
		ProcessingDigest:     digest,
		TraceTag:             traceTag,
		ProcessedAtUnix:      processedAt,
	}
}

// ProcessCreditEval 执行客户授信额度多维规则聚合加工
func ProcessCreditEval(
	reqID uint64,
	req *pb.CreditEvalRequest,
	bResp *pb.BackendCreditQueryResponse,
	deterministic bool,
	faultMutation string,
) *pb.CreditEvalResponse {
	processedAt, traceTag := resolveClockAndTrace(reqID, req.ClientTimestamp, deterministic)

	dtiBps := int32((req.ExistingDebtCents * 10000) / req.AnnualIncomeCents)

	if bResp.RiskBlacklisted || bResp.HistoricalDefaultCount >= 3 || bResp.CreditScore < 500 || dtiBps > 6000 {
		bizCode := int32(pb.BizCode_BIZ_RISK_REJECTED)
		grade := "D"
		decision := "REJECTED"
		digest := computeCreditDigest(reqID, req.UserId, grade, dtiBps, 0, 0, bizCode, decision)
		return &pb.CreditEvalResponse{
			BizCode:            bizCode,
			BizMessage:         "Credit evaluation rejected due to high risk profile",
			CreditGrade:        grade,
			DtiBps:             dtiBps,
			MaxAllowableCents:  0,
			ApprovedLimitCents: 0,
			Decision:           decision,
			ProcessingDigest:   digest,
			TraceTag:           traceTag,
			ProcessedAtUnix:    processedAt,
		}
	}

	var grade string
	var incomeFactorBps int64
	if bResp.CreditScore >= 750 && bResp.HistoricalDefaultCount == 0 {
		grade = "A"
		incomeFactorBps = 4000 // 年收入的 40%
	} else if bResp.CreditScore >= 650 {
		grade = "B"
		incomeFactorBps = 2500 // 年收入的 25%
	} else {
		grade = "C"
		incomeFactorBps = 1500 // 年收入的 15%
	}

	// 账龄满 24 个月额外提升 10% 授信基数
	baseLimit := (req.AnnualIncomeCents * incomeFactorBps) / 10000
	if bResp.AccountAgeMonths >= 24 {
		baseLimit = (baseLimit * 110) / 100
	}

	// 扣减存量负债的 50%
	maxAllowable := baseLimit - (req.ExistingDebtCents / 2)
	if maxAllowable < 0 {
		maxAllowable = 0
	}

	approved := req.RequestedLimitCents
	if approved > maxAllowable {
		approved = maxAllowable
	}
	if faultMutation == MutationDiscountBug && approved > 1000 {
		approved -= 1000
	}

	bizCode := int32(pb.BizCode_BIZ_OK)
	decision := "APPROVED"
	digest := computeCreditDigest(reqID, req.UserId, grade, dtiBps, maxAllowable, approved, bizCode, decision)

	return &pb.CreditEvalResponse{
		BizCode:            bizCode,
		BizMessage:         "Credit evaluation completed",
		CreditGrade:        grade,
		DtiBps:             dtiBps,
		MaxAllowableCents:  maxAllowable,
		ApprovedLimitCents: approved,
		Decision:           decision,
		ProcessingDigest:   digest,
		TraceTag:           traceTag,
		ProcessedAtUnix:    processedAt,
	}
}

func resolveClockAndTrace(reqID uint64, clientTimestamp int64, deterministic bool) (int64, string) {
	if deterministic {
		ts := defaultDeterministicEpoch
		if clientTimestamp > 0 {
			ts = clientTimestamp + 1
		}
		return ts, fmt.Sprintf("trace-det-%d", reqID)
	}
	return time.Now().UnixNano(), fmt.Sprintf("trace-dyn-%d-%d", reqID, rand.Int64())
}

func computeOrderDigest(
	reqID uint64,
	userID, skuID string,
	qty int32,
	subtotal, discount, tax, shipping, final int64,
	bizCode int32,
	decision, warehouse string,
) string {
	raw := fmt.Sprintf(
		"req=%d|user=%s|sku=%s|qty=%d|sub=%d|disc=%d|tax=%d|ship=%d|final=%d|code=%d|decision=%s|wh=%s",
		reqID, userID, skuID, qty,
		subtotal, discount, tax, shipping, final,
		bizCode, decision, warehouse,
	)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:8])
}

func computeCreditDigest(
	reqID uint64,
	userID, grade string,
	dtiBps int32,
	maxAllowable, approved int64,
	bizCode int32,
	decision string,
) string {
	raw := fmt.Sprintf(
		"req=%d|user=%s|grade=%s|dti=%d|max=%d|app=%d|code=%d|decision=%s",
		reqID, userID, grade, dtiBps, maxAllowable, approved, bizCode, decision,
	)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:8])
}
