package schema

import (
	"testing"
)

const customFinanceProto = `
syntax = "proto3";
package finance.trade.v2;

// 订单方向枚举
enum Side {
  SIDE_UNSPECIFIED = 0;
  SIDE_BUY         = 1;
  SIDE_SELL        = 2;
}

message RiskContext {
  string account_tier = 1;
  int32  leverage     = 2;
}

message TradeOrderReq {
  string              order_id   = 1;
  string              symbol     = 2;
  Side                side       = 3;
  int64               price_ticks = 4;
  repeated string     tags       = 5;
  map<string, int64>  ext_limits = 6;
  RiskContext         risk_ctx   = 7;
}

message TradeOrderResp {
  int32  status_code    = 1;
  string execution_id   = 2;
  int64  filled_amount  = 3;
}
`

func TestSchemaRegistry_CustomProtoAndSameOpCodeRouting(t *testing.T) {
	reg := NewEmptyRegistry()

	bindings := []OpCodeRouteBinding{
		{
			OpCode:          0x1001,
			OpCodeHex:       "0x1001",
			OpName:          "TradeOrder(0x1001)",
			RequestMessage:  "finance.trade.v2.TradeOrderReq",
			ResponseMessage: "finance.trade.v2.TradeOrderResp",
		},
	}

	if err := reg.LoadProtoSource("finance_trade.proto", customFinanceProto, bindings); err != nil {
		t.Fatalf("LoadProtoSource failed: %v", err)
	}

	st := reg.Status()
	if !st.SchemaLoaded || st.PackageName != "finance.trade.v2" || st.TotalMessages != 3 {
		t.Fatalf("unexpected registry status: %+v", st)
	}

	// 1. 测试同一 OpCode (0x1001) 在 DirectionReq 下编码与解码 TradeOrderReq
	reqJSON := map[string]any{
		"orderId":    "ORD-2026-009",
		"symbol":     "BTC-USDT",
		"side":       "SIDE_BUY",
		"priceTicks": 6850000,
		"tags":       []any{"vip", "maker"},
		"extLimits": map[string]any{
			"maxSlippage": 50,
		},
		"riskCtx": map[string]any{
			"accountTier": "PRO",
			"leverage":    10,
		},
	}

	reqWire, err := reg.EncodeJSONToPayload(0x1001, DirectionReq, reqJSON)
	if err != nil {
		t.Fatalf("EncodeJSONToPayload(REQ) failed: %v", err)
	}

	decodedReq, opName, ok := reg.DecodePayload(0x1001, DirectionReq, reqWire)
	if !ok {
		t.Fatalf("expected schemaDecoded=true for REQ")
	}
	if opName != "TradeOrder(0x1001)" || decodedReq["orderId"] != "ORD-2026-009" || decodedReq["symbol"] != "BTC-USDT" {
		t.Fatalf("unexpected decodedReq: %+v", decodedReq)
	}

	// 2. 测试同一 OpCode (0x1001) 在 DirectionResp 下编码与解码 TradeOrderResp
	respJSON := map[string]any{
		"statusCode":   200,
		"executionId":  "EXEC-8891",
		"filledAmount": 150000,
	}
	respWire, err := reg.EncodeJSONToPayload(0x1001, DirectionResp, respJSON)
	if err != nil {
		t.Fatalf("EncodeJSONToPayload(RESP) failed: %v", err)
	}

	decodedResp, _, ok := reg.DecodePayload(0x1001, DirectionResp, respWire)
	if !ok {
		t.Fatalf("expected schemaDecoded=true for RESP")
	}
	if decodedResp["executionId"] != "EXEC-8891" {
		t.Fatalf("unexpected decodedResp: %+v", decodedResp)
	}

	// 3. 测试无 Schema 降级 (Schema-Less Fallback) 与补传 .proto 后的重新激活
	reg.ClearSchema()
	fallbackMap, _, schemaDecoded := reg.DecodePayload(0x1001, DirectionReq, reqWire)
	if schemaDecoded {
		t.Fatalf("expected schemaDecoded=false after ClearSchema")
	}
	if fallbackMap["_schema_less"] != true {
		t.Fatalf("expected _schema_less=true in fallback output: %+v", fallbackMap)
	}
	tagsMap, ok := fallbackMap["tags"].(map[string]any)
	if !ok || tagsMap["tag_1"] != "ORD-2026-009" {
		t.Fatalf("expected tag_1='ORD-2026-009' in wire tag fallback, got: %+v", fallbackMap)
	}

	// 重新导入 .proto 后，同一份二进制载荷立刻恢复明文业务字段解析
	if err := reg.LoadProtoSource("finance_trade.proto", customFinanceProto, bindings); err != nil {
		t.Fatalf("reload proto failed: %v", err)
	}
	rehydrated, _, schemaDecoded := reg.DecodePayload(0x1001, DirectionReq, reqWire)
	if !schemaDecoded || rehydrated["orderId"] != "ORD-2026-009" {
		t.Fatalf("expected rehydrated orderId='ORD-2026-009', got: %+v", rehydrated)
	}
}
