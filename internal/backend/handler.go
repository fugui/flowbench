package backend

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/fugui/flowbench/internal/codec"
	"github.com/fugui/flowbench/internal/protocol"
	"github.com/fugui/flowbench/internal/protocol/pb"
)

// Handler 实现 Backend 子系统的二进制协议帧路由与数据响应逻辑
type Handler struct {
	store  *Store
	logger *slog.Logger
}

// NewHandler 创建 Backend 协议处理器
func NewHandler(store *Store, logger *slog.Logger) *Handler {
	if store == nil {
		store = NewDefaultStore()
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		store:  store,
		logger: logger,
	}
}

// Store 返回底层数据底座引用
func (h *Handler) Store() *Store {
	return h.store
}

// HandleFrame 根据帧头 OpCode 分发处理来自 Server 的二进制请求帧
func (h *Handler) HandleFrame(ctx context.Context, reqFrame *codec.Frame) (*codec.Frame, error) {
	reqID := reqFrame.Header.RequestID
	flags := reqFrame.Header.Flags

	switch reqFrame.Header.OpCode {
	case codec.OpPingReq:
		var req pb.PingRequest
		if err := protocol.Unmarshal(reqFrame.Payload, &req); err != nil {
			return h.errorFrame(reqID, flags, codec.StatusBadPayload, err.Error())
		}
		resp := &pb.PongResponse{
			BizCode:       int32(pb.BizCode_BIZ_OK),
			ServerId:      "flowbench-backend",
			EchoMessage:   "PONG:" + req.Message,
			Status:        "SERVING",
			RepliedAtUnix: req.SentAtUnix + 1,
		}
		return protocol.EncodeMessageFrame(codec.OpPongResp, reqID, flags, codec.StatusOK, resp)

	case codec.OpBackendOrderQueryReq:
		var req pb.BackendOrderQueryRequest
		if err := protocol.Unmarshal(reqFrame.Payload, &req); err != nil {
			return h.errorFrame(reqID, flags, codec.StatusBadPayload, err.Error())
		}
		h.store.IncQueryCount()

		// 若请求特定慢查询 SKU，模拟可控延迟
		if req.SkuId == "SKU-SLOW-500MS" {
			select {
			case <-time.After(150 * time.Millisecond):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		sku, found := h.store.LookupSKU(req.SkuId)
		userProfile, _ := h.store.LookupUser(req.UserId)

		resp := &pb.BackendOrderQueryResponse{
			Found:           found,
			SkuId:           req.SkuId,
			SkuName:         sku.SkuName,
			BasePriceCents:  sku.BasePriceCents,
			AvailableStock:  sku.AvailableStock,
			UnitWeightGrams: sku.UnitWeightGrams,
			WarehouseCode:   sku.WarehouseCode,
			UserCreditScore: userProfile.CreditScore,
			RiskBlacklisted: userProfile.RiskBlacklisted,
		}
		h.logger.Info("Backend 处理商品与信用底层查询",
			"req_id", reqID,
			"sku_id", req.SkuId,
			"user_id", req.UserId,
			"found", found,
			"base_price_cents", resp.BasePriceCents,
			"available_stock", resp.AvailableStock,
			"credit_score", resp.UserCreditScore,
		)
		return protocol.EncodeMessageFrame(codec.OpBackendOrderQueryResp, reqID, flags, codec.StatusOK, resp)

	case codec.OpBackendCreditQueryReq:
		var req pb.BackendCreditQueryRequest
		if err := protocol.Unmarshal(reqFrame.Payload, &req); err != nil {
			return h.errorFrame(reqID, flags, codec.StatusBadPayload, err.Error())
		}
		h.store.IncQueryCount()

		userProfile, found := h.store.LookupUser(req.UserId)
		resp := &pb.BackendCreditQueryResponse{
			Found:                  found,
			UserId:                 req.UserId,
			CreditScore:            userProfile.CreditScore,
			HistoricalDefaultCount: userProfile.HistoricalDefaultCount,
			AccountAgeMonths:       userProfile.AccountAgeMonths,
			RiskBlacklisted:        userProfile.RiskBlacklisted,
		}
		h.logger.Info("Backend 处理客户征信底层查询",
			"req_id", reqID,
			"user_id", req.UserId,
			"found", found,
			"credit_score", resp.CreditScore,
			"defaults", resp.HistoricalDefaultCount,
		)
		return protocol.EncodeMessageFrame(codec.OpBackendCreditQueryResp, reqID, flags, codec.StatusOK, resp)

	default:
		msg := fmt.Sprintf("backend: unsupported opcode 0x%02X", reqFrame.Header.OpCode)
		return h.errorFrame(reqID, flags, codec.StatusUnknownOpCode, msg)
	}
}

func (h *Handler) errorFrame(reqID uint64, flags uint8, status uint8, message string) (*codec.Frame, error) {
	errResp := &pb.ErrorResponse{
		ErrorCode:    int32(status),
		ErrorMessage: message,
		RequestId:    reqID,
	}
	return protocol.EncodeMessageFrame(codec.OpErrorResp, reqID, flags, status, errResp)
}
