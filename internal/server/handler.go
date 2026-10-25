package server

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/fugui/flowbench/internal/codec"
	"github.com/fugui/flowbench/internal/protocol"
	"github.com/fugui/flowbench/internal/protocol/pb"
	"github.com/fugui/flowbench/internal/transport"
)

// Handler 实现 Server 子系统（SUT）的入站请求路由、前置校验、下游 Backend 调用与二次加工编排
type Handler struct {
	cfg           Config
	backendClient *transport.Client
	logger        *slog.Logger
}

// NewHandler 创建 Server 协议与业务编排处理器
func NewHandler(cfg Config, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	bClient := transport.NewClient(transport.ClientConfig{
		Name:       "server->backend",
		TargetAddr: cfg.BackendAddr,
		ConnMode:   cfg.ConnMode,
		Timeout:    cfg.IOTimeout,
		DebugFrame: cfg.DebugFrame,
		Logger:     logger,
	})
	return &Handler{
		cfg:           cfg,
		backendClient: bClient,
		logger:        logger,
	}
}

// Close 释放下游客户端连接资源
func (h *Handler) Close() error {
	return h.backendClient.Close()
}

// HandleFrame 分发处理来自 Client 的自定义二进制协议帧
func (h *Handler) HandleFrame(ctx context.Context, reqFrame *codec.Frame) (*codec.Frame, error) {
	start := time.Now()
	reqID := reqFrame.Header.RequestID
	flags := reqFrame.Header.Flags
	deterministic := h.cfg.Deterministic || (flags&codec.FlagDeterministic != 0)

	switch reqFrame.Header.OpCode {
	case codec.OpPingReq:
		return h.handlePing(reqID, flags, reqFrame.Payload, deterministic)
	case codec.OpOrderQuoteReq:
		return h.handleOrderQuote(ctx, reqID, flags, reqFrame.Payload, deterministic, start)
	case codec.OpCreditEvalReq:
		return h.handleCreditEval(ctx, reqID, flags, reqFrame.Payload, deterministic, start)
	default:
		msg := fmt.Sprintf("server: unsupported opcode 0x%02X", reqFrame.Header.OpCode)
		return h.errorFrame(reqID, flags, codec.StatusUnknownOpCode, msg)
	}
}

func (h *Handler) handlePing(reqID uint64, flags uint8, payload []byte, deterministic bool) (*codec.Frame, error) {
	var req pb.PingRequest
	if err := protocol.Unmarshal(payload, &req); err != nil {
		return h.errorFrame(reqID, flags, codec.StatusBadPayload, err.Error())
	}
	repliedAt := time.Now().Unix()
	if deterministic {
		if req.SentAtUnix > 0 {
			repliedAt = req.SentAtUnix + 1
		} else {
			repliedAt = defaultDeterministicEpoch
		}
	}
	resp := &pb.PongResponse{
		BizCode:       int32(pb.BizCode_BIZ_OK),
		ServerId:      "flowbench-server",
		EchoMessage:   "PONG:" + req.Message,
		Status:        "SERVING",
		RepliedAtUnix: repliedAt,
	}
	h.logger.Info("Server 处理 Ping 探活请求 (纯入站，无下游调用)",
		"req_id", reqID,
		"client_id", req.ClientId,
		"echo", resp.EchoMessage,
	)
	return protocol.EncodeMessageFrame(codec.OpPongResp, reqID, flags, codec.StatusOK, resp)
}

func (h *Handler) handleOrderQuote(
	ctx context.Context,
	reqID uint64,
	flags uint8,
	payload []byte,
	deterministic bool,
	start time.Time,
) (*codec.Frame, error) {
	var req pb.OrderQuoteRequest
	if err := protocol.Unmarshal(payload, &req); err != nil {
		return h.errorFrame(reqID, flags, codec.StatusBadPayload, err.Error())
	}

	// Step 1: 前置参数校验（若非法直接短路返回，绝不调用下游 Backend）
	if err := ValidateOrderQuoteReq(&req); err != nil {
		shortResp := BuildPrecheckRejectedQuoteResp(reqID, &req, err.Error(), deterministic)
		h.logger.Info("Server 前置校验拦截请求 (短路返回，0 次 Backend 调用)",
			"req_id", reqID,
			"reason", err.Error(),
			"digest", shortResp.ProcessingDigest,
		)
		return protocol.EncodeMessageFrame(codec.OpOrderQuoteResp, reqID, flags, codec.StatusOK, shortResp)
	}

	// Step 2: 构造发往下游 Backend 的二进制查询帧（透传相同 RequestID）
	bReq := &pb.BackendOrderQueryRequest{
		SkuId:  req.SkuId,
		UserId: req.UserId,
		Region: req.Region,
	}
	bReqFrame, err := protocol.EncodeMessageFrame(codec.OpBackendOrderQueryReq, reqID, flags, codec.StatusOK, bReq)
	if err != nil {
		return nil, err
	}

	bStart := time.Now()
	bRespFrame, err := h.backendClient.Call(ctx, bReqFrame)
	bLatency := time.Since(bStart)
	if err != nil {
		h.logger.Error("Server 调用下游 Backend 失败", "req_id", reqID, "err", err)
		return h.errorFrame(reqID, flags, codec.StatusDownstreamError, err.Error())
	}

	var bResp pb.BackendOrderQueryResponse
	if err := protocol.DecodeMessageFrame(bRespFrame, codec.OpBackendOrderQueryResp, &bResp); err != nil {
		return h.errorFrame(reqID, flags, codec.StatusDownstreamError, err.Error())
	}

	// Step 3: 在 Server 侧执行四步业务二次加工与聚合
	finalResp := ProcessOrderQuote(reqID, &req, &bResp, deterministic, h.cfg.FaultMutation)

	h.logger.Info("Server 完成订单计价二次加工与风控聚合",
		"req_id", reqID,
		"sku_id", req.SkuId,
		"qty", req.Quantity,
		"subtotal_cents", finalResp.SubtotalCents,
		"discount_cents", finalResp.DiscountCents,
		"tax_cents", finalResp.TaxCents,
		"shipping_cents", finalResp.ShippingCents,
		"final_amount_cents", finalResp.FinalAmountCents,
		"decision", finalResp.RiskDecision,
		"digest", finalResp.ProcessingDigest,
		"backend_latency_ms", bLatency.Milliseconds(),
		"total_latency_ms", time.Since(start).Milliseconds(),
	)

	return protocol.EncodeMessageFrame(codec.OpOrderQuoteResp, reqID, flags, codec.StatusOK, finalResp)
}

func (h *Handler) handleCreditEval(
	ctx context.Context,
	reqID uint64,
	flags uint8,
	payload []byte,
	deterministic bool,
	start time.Time,
) (*codec.Frame, error) {
	var req pb.CreditEvalRequest
	if err := protocol.Unmarshal(payload, &req); err != nil {
		return h.errorFrame(reqID, flags, codec.StatusBadPayload, err.Error())
	}

	if err := ValidateCreditEvalReq(&req); err != nil {
		processedAt, traceTag := resolveClockAndTrace(reqID, req.ClientTimestamp, deterministic)
		bizCode := int32(pb.BizCode_BIZ_INVALID_ARGUMENT)
		decision := "REJECTED_PRECHECK"
		digest := computeCreditDigest(reqID, req.UserId, "N/A", 0, 0, 0, bizCode, decision)
		resp := &pb.CreditEvalResponse{
			BizCode:          bizCode,
			BizMessage:       "Pre-validation failed: " + err.Error(),
			CreditGrade:      "N/A",
			Decision:         decision,
			ProcessingDigest: digest,
			TraceTag:         traceTag,
			ProcessedAtUnix:  processedAt,
		}
		return protocol.EncodeMessageFrame(codec.OpCreditEvalResp, reqID, flags, codec.StatusOK, resp)
	}

	bReq := &pb.BackendCreditQueryRequest{UserId: req.UserId}
	bReqFrame, err := protocol.EncodeMessageFrame(codec.OpBackendCreditQueryReq, reqID, flags, codec.StatusOK, bReq)
	if err != nil {
		return nil, err
	}

	bStart := time.Now()
	bRespFrame, err := h.backendClient.Call(ctx, bReqFrame)
	bLatency := time.Since(bStart)
	if err != nil {
		h.logger.Error("Server 调用下游 Backend 征信查询失败", "req_id", reqID, "err", err)
		return h.errorFrame(reqID, flags, codec.StatusDownstreamError, err.Error())
	}

	var bResp pb.BackendCreditQueryResponse
	if err := protocol.DecodeMessageFrame(bRespFrame, codec.OpBackendCreditQueryResp, &bResp); err != nil {
		return h.errorFrame(reqID, flags, codec.StatusDownstreamError, err.Error())
	}

	finalResp := ProcessCreditEval(reqID, &req, &bResp, deterministic, h.cfg.FaultMutation)
	h.logger.Info("Server 完成客户授信评估多维规则聚合",
		"req_id", reqID,
		"user_id", req.UserId,
		"grade", finalResp.CreditGrade,
		"dti_bps", finalResp.DtiBps,
		"approved_limit_cents", finalResp.ApprovedLimitCents,
		"decision", finalResp.Decision,
		"digest", finalResp.ProcessingDigest,
		"backend_latency_ms", bLatency.Milliseconds(),
		"total_latency_ms", time.Since(start).Milliseconds(),
	)

	return protocol.EncodeMessageFrame(codec.OpCreditEvalResp, reqID, flags, codec.StatusOK, finalResp)
}

func (h *Handler) errorFrame(reqID uint64, flags uint8, status uint8, message string) (*codec.Frame, error) {
	errResp := &pb.ErrorResponse{
		ErrorCode:    int32(status),
		ErrorMessage: message,
		RequestId:    reqID,
	}
	return protocol.EncodeMessageFrame(codec.OpErrorResp, reqID, flags, status, errResp)
}
