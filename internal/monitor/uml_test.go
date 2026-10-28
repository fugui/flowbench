package monitor

import (
	"testing"
)

func TestBuildUMLDiagram_SimplePingPong(t *testing.T) {
	tr := TransactionTrace{
		TraceID:    "trace-1001",
		RequestID:  1001,
		OpName:     "OpPingReq",
		StatusCode: 0x02,
		StatusName: "StatusOK",
		DurationMs: 0.85,
		IngressReq: CapturedFrameEvent{
			SeqID:     1,
			Direction: "INGRESS_REQ",
			Header: DecodedHeaderInfo{
				OpCode:    0x01,
				OpName:    "OpPingReq",
				RequestID: 1001,
			},
			ProtobufJSON: map[string]any{
				"client_name": "flowbench-client",
			},
		},
		IngressResp: &CapturedFrameEvent{
			SeqID:     2,
			Direction: "INGRESS_RESP",
			Header: DecodedHeaderInfo{
				OpCode:     0x02,
				OpName:     "OpPongResp",
				RequestID:  1001,
				StatusName: "StatusOK",
			},
			ProtobufJSON: map[string]any{
				"server_name": "flowbench-server",
				"message":     "PONG",
			},
			LatencyMs: 0.85,
		},
		SubCalls: make([]SubCallDetail, 0),
	}

	backends := []BackendEndpoint{
		{Name: "风控中心 credit-svc", Host: "127.0.0.1", Port: 9200},
	}

	diagram := BuildUMLDiagram(tr, backends, "flowbench-server", 9100)

	if diagram.TraceID != "trace-1001" {
		t.Fatalf("expected trace-1001, got %s", diagram.TraceID)
	}

	// 2 个参与者：Caller, SUT
	if len(diagram.Participants) != 2 {
		t.Fatalf("expected 2 participants, got %d", len(diagram.Participants))
	}
	if diagram.Participants[0].Role != RoleCaller || diagram.Participants[0].ID != "caller" {
		t.Errorf("expected caller participant, got %+v", diagram.Participants[0])
	}
	if diagram.Participants[1].Role != RoleSUT || diagram.Participants[1].ID != "sut" {
		t.Errorf("expected sut participant, got %+v", diagram.Participants[1])
	}

	// 2 个步骤：① Caller -> SUT; ② SUT -> Caller
	if len(diagram.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(diagram.Steps))
	}

	s1 := diagram.Steps[0]
	if s1.StepIndex != 1 || s1.FromID != "caller" || s1.ToID != "sut" || s1.IsReturn {
		t.Errorf("unexpected step 1: %+v", s1)
	}
	if s1.OpName != "OpPingReq" {
		t.Errorf("expected OpPingReq, got %s", s1.OpName)
	}

	s2 := diagram.Steps[1]
	if s2.StepIndex != 2 || s2.FromID != "sut" || s2.ToID != "caller" || !s2.IsReturn {
		t.Errorf("unexpected step 2: %+v", s2)
	}
	if s2.DurationMs != 0.85 {
		t.Errorf("expected 0.85ms, got %f", s2.DurationMs)
	}
}

func TestBuildUMLDiagram_FullOrderQuoteWithMultipleBackends(t *testing.T) {
	tr := TransactionTrace{
		TraceID:    "trace-1002",
		RequestID:  1002,
		OpName:     "OpOrderQuoteReq",
		StatusCode: 0x12,
		StatusName: "StatusOK",
		DurationMs: 1.65,
		IngressReq: CapturedFrameEvent{
			SeqID:     10,
			Direction: "INGRESS_REQ",
			Header: DecodedHeaderInfo{
				OpCode:    0x11,
				OpName:    "OpOrderQuoteReq",
				RequestID: 1002,
			},
			ProtobufJSON: map[string]any{
				"userId":   "USR-VIP-001",
				"quantity": 5,
			},
		},
		SubCalls: []SubCallDetail{
			{
				SubCallID:     "sub-1",
				TargetBackend: "127.0.0.1:9200",
				BackendName:   "风控中心",
				OpName:        "OpBackendCreditQueryReq",
				DurationMs:    0.32,
				EgressReq: CapturedFrameEvent{
					SeqID:     11,
					Direction: "EGRESS_REQ",
					Header: DecodedHeaderInfo{
						OpCode:    0x41,
						OpName:    "OpBackendCreditQueryReq",
						RequestID: 1002,
					},
					ProtobufJSON: map[string]any{"userId": "USR-VIP-001"},
				},
				EgressResp: &CapturedFrameEvent{
					SeqID:     12,
					Direction: "EGRESS_RESP",
					Header: DecodedHeaderInfo{
						OpCode:    0x42,
						OpName:    "OpBackendCreditQueryResp",
						RequestID: 1002,
					},
					ProtobufJSON: map[string]any{"creditScore": 750},
					LatencyMs:    0.32,
				},
			},
			{
				SubCallID:     "sub-2",
				TargetBackend: "127.0.0.1:9201",
				BackendName:   "", // 无显式后端名，需从 backends 匹配或兜底
				OpName:        "OpBackendStockQueryReq",
				DurationMs:    0.41,
				EgressReq: CapturedFrameEvent{
					SeqID:     13,
					Direction: "EGRESS_REQ",
					Header: DecodedHeaderInfo{
						OpCode:    0x31,
						OpName:    "OpBackendStockQueryReq",
						RequestID: 1002,
					},
					ProtobufJSON: map[string]any{"sku": "SKU-PRO-01"},
				},
				EgressResp: &CapturedFrameEvent{
					SeqID:     14,
					Direction: "EGRESS_RESP",
					Header: DecodedHeaderInfo{
						OpCode:    0x32,
						OpName:    "OpBackendStockQueryResp",
						RequestID: 1002,
					},
					ProtobufJSON: map[string]any{"stock": 100},
					LatencyMs:    0.41,
				},
			},
		},
		IngressResp: &CapturedFrameEvent{
			SeqID:     15,
			Direction: "INGRESS_RESP",
			Header: DecodedHeaderInfo{
				OpCode:     0x12,
				OpName:     "OpOrderQuoteResp",
				RequestID:  1002,
				StatusName: "StatusOK",
			},
			ProtobufJSON: map[string]any{
				"finalAmountCents": 50310,
			},
			LatencyMs: 1.65,
		},
	}

	backends := []BackendEndpoint{
		{Name: "风控中心 credit-svc", Host: "127.0.0.1", Port: 9200},
		{Name: "库存中心 stock-svc", Host: "127.0.0.1", Port: 9201},
	}

	diagram := BuildUMLDiagram(tr, backends, "order-server", 9100)

	// 参与者应为 4 个：Caller, SUT, credit-svc:9200, stock-svc:9201
	if len(diagram.Participants) != 4 {
		t.Fatalf("expected 4 participants, got %d", len(diagram.Participants))
	}
	if diagram.Participants[0].Role != RoleCaller {
		t.Errorf("expected caller as 1st participant, got %+v", diagram.Participants[0])
	}
	if diagram.Participants[1].Role != RoleSUT || diagram.Participants[1].DisplayName != "order-server (被测组件 SUT)" {
		t.Errorf("expected sut as 2nd participant, got %+v", diagram.Participants[1])
	}
	if diagram.Participants[2].DisplayName != "风控中心 credit-svc (127.0.0.1:9200)" {
		t.Errorf("expected credit-svc participant, got %s", diagram.Participants[2].DisplayName)
	}
	if diagram.Participants[3].DisplayName != "库存中心 stock-svc (127.0.0.1:9201)" {
		t.Errorf("expected stock-svc participant, got %s", diagram.Participants[3].DisplayName)
	}

	// 步骤应为 6 步：
	// ① Caller -> SUT (OpOrderQuoteReq)
	// ② SUT -> credit-svc (OpBackendCreditQueryReq)
	// ③ credit-svc -> SUT (OpBackendCreditQueryResp, isReturn=true, isMockable=true)
	// ④ SUT -> stock-svc (OpBackendStockQueryReq)
	// ⑤ stock-svc -> SUT (OpBackendStockQueryResp, isReturn=true, isMockable=true)
	// ⑥ SUT -> Caller (OpOrderQuoteResp, isReturn=true)
	if len(diagram.Steps) != 6 {
		t.Fatalf("expected 6 steps, got %d", len(diagram.Steps))
	}

	stepExpectations := []struct {
		stepIdx  int
		fromID   string
		toID     string
		isReturn bool
		mockable bool
		opName   string
	}{
		{1, "caller", "sut", false, false, "OpOrderQuoteReq"},
		{2, "sut", diagram.Participants[2].ID, false, false, "OpBackendCreditQueryReq"},
		{3, diagram.Participants[2].ID, "sut", true, true, "OpBackendCreditQueryResp"},
		{4, "sut", diagram.Participants[3].ID, false, false, "OpBackendStockQueryReq"},
		{5, diagram.Participants[3].ID, "sut", true, true, "OpBackendStockQueryResp"},
		{6, "sut", "caller", true, false, "OpOrderQuoteResp"},
	}

	for i, exp := range stepExpectations {
		st := diagram.Steps[i]
		if st.StepIndex != exp.stepIdx {
			t.Errorf("step %d index expected %d, got %d", i, exp.stepIdx, st.StepIndex)
		}
		if st.FromID != exp.fromID {
			t.Errorf("step %d fromID expected %s, got %s", i, exp.fromID, st.FromID)
		}
		if st.ToID != exp.toID {
			t.Errorf("step %d toID expected %s, got %s", i, exp.toID, st.ToID)
		}
		if st.IsReturn != exp.isReturn {
			t.Errorf("step %d isReturn expected %v, got %v", i, exp.isReturn, st.IsReturn)
		}
		if st.IsMockable != exp.mockable {
			t.Errorf("step %d isMockable expected %v, got %v", i, exp.mockable, st.IsMockable)
		}
		if st.OpName != exp.opName {
			t.Errorf("step %d opName expected %s, got %s", i, exp.opName, st.OpName)
		}
	}
}

func TestResolveBackendDisplayName(t *testing.T) {
	backends := []BackendEndpoint{
		{Name: "风控中心 credit-svc", Host: "127.0.0.1", Port: 9200},
		{Name: "", Host: "127.0.0.1", Port: 9201},
	}

	// 1. 已注册且有别名
	name1 := resolveBackendDisplayName("127.0.0.1:9200", "风控中心", backends)
	if name1 != "风控中心 credit-svc (127.0.0.1:9200)" {
		t.Errorf("unexpected name1: %s", name1)
	}

	// 2. 已注册但无别名
	name2 := resolveBackendDisplayName("127.0.0.1:9201", "", backends)
	if name2 != "下游组件 (127.0.0.1:9201)" {
		t.Errorf("unexpected name2: %s", name2)
	}

	// 3. 未注册的下游地址 (兜底)
	name3 := resolveBackendDisplayName("192.168.1.50:8888", "", backends)
	if name3 != "下游组件 (192.168.1.50:8888)" {
		t.Errorf("unexpected name3: %s", name3)
	}
}
