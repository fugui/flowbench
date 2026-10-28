package monitor

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/fugui/flowbench/internal/codec"
	"github.com/fugui/flowbench/internal/protocol"
	"github.com/fugui/flowbench/internal/protocol/pb"
	"github.com/fugui/flowbench/internal/server"
	"github.com/fugui/flowbench/internal/transport"
)

// TestSessionLifecycle_StartStop 验证会话启停状态机与帧捕获
func TestSessionLifecycle_StartStop(t *testing.T) {
	eng := NewEngine(EngineConfig{
		AssetFilePath: t.TempDir() + "/test-assets.json",
	})
	defer func() {
		_ = eng.Close()
	}()

	// 初始状态应为 IDLE
	cur := eng.GetCurrentSession()
	if cur.Status != SessionStatusIdle {
		t.Fatalf("expected initial status IDLE, got %s", cur.Status)
	}

	// 启动会话
	sess, err := eng.StartSession(SessionStartRequest{
		Title:       "单元测试会话",
		TargetPID:   12345,
		ProcessName: "test-server",
		IngressPort: 9100,
		Backends: []BackendEndpoint{
			{Name: "stock-srv", Host: "127.0.0.1", Port: 9200, Active: true},
		},
	})
	if err != nil {
		t.Fatalf("start session failed: %v", err)
	}
	if sess.Status != SessionStatusRecording {
		t.Fatalf("expected status RECORDING, got %s", sess.Status)
	}
	if sess.TargetPID != 12345 {
		t.Fatalf("expected target_pid 12345, got %d", sess.TargetPID)
	}

	// 模拟流经一笔帧
	f1, err := protocol.EncodeMessageFrame(codec.OpPingReq, 1001, codec.FlagDeterministic, codec.StatusOK, &pb.PingRequest{
		ClientId:   "cli-1",
		Message:    "ping",
		SentAtUnix: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("encode frame failed: %v", err)
	}
	eng.publishFrame(f1, "INGRESS_REQ", "RECORD", 0)

	// 获取当前状态
	cur = eng.GetCurrentSession()
	if cur.TotalFrames < 1 {
		t.Fatalf("expected total_frames >= 1, got %d", cur.TotalFrames)
	}

	// 停止会话
	stopped, err := eng.StopSession()
	if err != nil {
		t.Fatalf("stop session failed: %v", err)
	}
	if stopped.Status != SessionStatusStopped {
		t.Fatalf("expected status STOPPED, got %s", stopped.Status)
	}
	if stopped.DurationSec < 0 {
		t.Fatalf("invalid duration_sec: %v", stopped.DurationSec)
	}
}

// TestTraceCorrelator_Aggregation 验证端到端调用事务关联聚合
func TestTraceCorrelator_Aggregation(t *testing.T) {
	eng := NewEngine(EngineConfig{
		AssetFilePath: t.TempDir() + "/test-assets.json",
	})
	defer func() {
		_ = eng.Close()
	}()

	_, err := eng.StartSession(SessionStartRequest{
		Title:       "事务聚合测试",
		TargetPID:   8888,
		ProcessName: "order-server",
		IngressPort: 9100,
		Backends: []BackendEndpoint{
			{Name: "stock-srv", Host: "127.0.0.1", Port: 9200, Active: true},
		},
	})
	if err != nil {
		t.Fatalf("start session failed: %v", err)
	}

	// 1. 发送 Ingress Req (ReqID: 2001)
	req1, _ := protocol.EncodeMessageFrame(codec.OpOrderQuoteReq, 2001, codec.FlagDeterministic, codec.StatusOK, &pb.OrderQuoteRequest{
		UserId:   "USR-01",
		SkuId:    "SKU-01",
		Quantity: 2,
		VipLevel: 1,
		Region:   "CN-EAST",
	})
	eng.publishFrame(req1, "INGRESS_REQ", "RECORD", 0)

	// 2. 发送 Egress Req -> Backend (ReqID: 2001)
	egReq, _ := protocol.EncodeMessageFrame(codec.OpBackendOrderQueryReq, 2001, codec.FlagDeterministic, codec.StatusOK, &pb.BackendOrderQueryRequest{
		SkuId:  "SKU-01",
		UserId: "USR-01",
		Region: "CN-EAST",
	})
	eng.publishFrameWithBackend(egReq, "EGRESS_REQ", "RECORD", "127.0.0.1:9200", "stock-srv", 0)

	// 3. 发送 Egress Resp <- Backend (ReqID: 2001)
	egResp, _ := protocol.EncodeMessageFrame(codec.OpBackendOrderQueryResp, 2001, codec.FlagDeterministic, codec.StatusOK, &pb.BackendOrderQueryResponse{
		Found:          true,
		SkuId:          "SKU-01",
		BasePriceCents: 10000,
		AvailableStock: 50,
	})
	eng.publishFrameWithBackend(egResp, "EGRESS_RESP", "RECORD", "127.0.0.1:9200", "stock-srv", 0.5)

	// 4. 发送 Ingress Resp (ReqID: 2001)
	resp1, _ := protocol.EncodeMessageFrame(codec.OpOrderQuoteResp, 2001, codec.FlagDeterministic, codec.StatusOK, &pb.OrderQuoteResponse{
		BizCode:          200,
		RiskDecision:     "APPROVED",
		FinalAmountCents: 19000,
	})
	eng.publishFrame(resp1, "INGRESS_RESP", "RECORD", 1.8)

	// 查询事务列表
	traces := eng.ListTransactionTraces("")
	if len(traces) == 0 {
		t.Fatalf("expected at least 1 trace, got 0")
	}

	tr := traces[0]
	if tr.RequestID != 2001 {
		t.Fatalf("expected request_id 2001, got %d", tr.RequestID)
	}
	if tr.OpName == "" {
		t.Fatalf("op_name should not be empty")
	}
	if len(tr.SubCalls) != 1 {
		t.Fatalf("expected 1 sub call, got %d", len(tr.SubCalls))
	}
	if tr.SubCalls[0].TargetBackend != "127.0.0.1:9200" {
		t.Fatalf("expected target_backend 127.0.0.1:9200, got %s", tr.SubCalls[0].TargetBackend)
	}
	if tr.IngressResp == nil {
		t.Fatalf("expected ingress_resp to be populated")
	}
}

// TestSingleCallReplay 验证针对单条调用事务的原地自愈重放与字段 Diff
func TestSingleCallReplay(t *testing.T) {
	eng := NewEngine(EngineConfig{
		AssetFilePath: t.TempDir() + "/test-assets.json",
	})
	defer func() {
		_ = eng.Close()
	}()

	// 准备出站 Mock
	mockReq, _ := protocol.EncodeMessageFrame(codec.OpBackendOrderQueryReq, 100, codec.FlagDeterministic, codec.StatusOK, &pb.BackendOrderQueryRequest{
		SkuId:  "SKU-TEST-01",
		UserId: "USR-001",
		Region: "CN-EAST",
	})
	mockResp, _ := protocol.EncodeMessageFrame(codec.OpBackendOrderQueryResp, 100, codec.FlagDeterministic, codec.StatusOK, &pb.BackendOrderQueryResponse{
		Found:          true,
		SkuId:          "SKU-TEST-01",
		BasePriceCents: 10000,
		AvailableStock: 50,
		WarehouseCode:  "WH-SH-01",
	})
	rawReqBytes, _ := codec.EncodeFrameBytes(mockReq)
	rawRespBytes, _ := codec.EncodeFrameBytes(mockResp)

	semKey := SemanticSignature(mockReq)
	eng.mu.Lock()
	eng.egressMocks[semKey] = &RecordedMockAsset{
		MockID:       "mock-test-01",
		SemanticKey:  semKey,
		OpName:       "OpBackendOrderQueryReq -> OpBackendOrderQueryResp",
		RawReqHex:    fmt.Sprintf("%x", rawReqBytes),
		RawRespHex:   fmt.Sprintf("%x", rawRespBytes),
		OriginalResp: InspectFrame(mockResp, "EGRESS_RESP", "RECORD", 100, 0),
	}
	eng.egressMockOrder = append(eng.egressMockOrder, semKey)
	eng.mu.Unlock()

	// 启动一个测试用 Server 充当被测目标
	// 下游指向一个会走 Mock 匹配的代理
	var mockServerPort int
	mockServer := transport.NewServer(transport.ServerConfig{
		Name:       "test-mock-proxy",
		ListenAddr: "127.0.0.1:0",
		IOTimeout:  2 * time.Second,
	}, func(ctx context.Context, reqFrame *codec.Frame) (*codec.Frame, error) {
		return eng.MatchAndHealMockResponse(reqFrame)
	})
	mockAddr, err := mockServer.Start()
	if err != nil {
		t.Fatalf("start mock server failed: %v", err)
	}
	defer func() {
		_ = mockServer.Shutdown(context.Background())
	}()
	_, portStr, _ := net.SplitHostPort(mockAddr)
	_, _ = fmt.Sscanf(portStr, "%d", &mockServerPort)

	// 启动目标 Server
	sHandler := server.NewHandler(server.Config{
		ListenAddr:    "127.0.0.1:0",
		BackendAddr:   mockAddr,
		ConnMode:      transport.ConnModeShort,
		IOTimeout:     2 * time.Second,
		Deterministic: true,
	}, nil)
	defer func() {
		_ = sHandler.Close()
	}()

	sutServer := transport.NewServer(transport.ServerConfig{
		Name:       "sut-server",
		ListenAddr: "127.0.0.1:0",
		IOTimeout:  2 * time.Second,
	}, sHandler.HandleFrame)
	sutAddr, err := sutServer.Start()
	if err != nil {
		t.Fatalf("start sut server failed: %v", err)
	}
	defer func() {
		_ = sutServer.Shutdown(context.Background())
	}()

	var sutPort int
	_, sPortStr, _ := net.SplitHostPort(sutAddr)
	_, _ = fmt.Sscanf(sPortStr, "%d", &sutPort)

	// 录制一个 Trace 资产
	inReq, _ := protocol.EncodeMessageFrame(codec.OpOrderQuoteReq, 9001, codec.FlagDeterministic, codec.StatusOK, &pb.OrderQuoteRequest{
		UserId:   "USR-001",
		SkuId:    "SKU-TEST-01",
		Quantity: 1,
		VipLevel: 1,
		Region:   "CN-EAST",
	})
	// 先通过一次真实调用生成基准响应帧
	baseCli := transport.NewClient(transport.ClientConfig{
		Name:       "baseline-client",
		TargetAddr: sutAddr,
		ConnMode:   transport.ConnModeShort,
		Timeout:    2 * time.Second,
	})
	baseResp, callErr := baseCli.Call(context.Background(), inReq)
	if callErr != nil {
		t.Fatalf("call baseline failed: %v", callErr)
	}
	_ = baseCli.Close()

	rawReqBytes, _ = codec.EncodeFrameBytes(inReq)
	rawRespBytes, _ = codec.EncodeFrameBytes(baseResp)

	trace := &TransactionTrace{
		TraceID:    "trace-9001",
		RequestID:  9001,
		OpName:     "OpOrderQuote",
		StatusCode: uint32(codec.StatusOK),
		StatusName: codec.StatusName(codec.StatusOK),
		DurationMs: 2.5,
		IngressReq: InspectFrame(inReq, "INGRESS_REQ", "RECORD", 9001, 0),
		IngressResp: func() *CapturedFrameEvent {
			ev := InspectFrame(baseResp, "INGRESS_RESP", "RECORD", 9001, 2.5)
			return &ev
		}(),
	}
	trace.IngressReq.RawWireHex = fmt.Sprintf("%x", rawReqBytes)
	trace.IngressResp.RawWireHex = fmt.Sprintf("%x", rawRespBytes)

	eng.sessionMu.Lock()
	eng.currentSession.Traces = append(eng.currentSession.Traces, *trace)
	eng.sessionMu.Unlock()

	// 执行原地重放 (不修改 payload, 预期 ByteExactMatch = true)
	replayRes, err := eng.ReplaySingleTrace(context.Background(), sutAddr, SingleReplayRequest{
		TraceID: "trace-9001",
	})
	if err != nil {
		t.Fatalf("replay trace failed: %v", err)
	}
	if !replayRes.Passed {
		t.Fatalf("expected replay to pass, got diffs: %v, err: %s", replayRes.FieldDiffs, replayRes.ErrorMessage)
	}
	if !replayRes.ByteExactMatch {
		t.Fatalf("expected byte exact match")
	}

	// 执行带有 Payload 修改的重放 (修改 quantity = 2, 预期产生字段 Diff)
	replayResDiff, err := eng.ReplaySingleTrace(context.Background(), sutAddr, SingleReplayRequest{
		TraceID: "trace-9001",
		OverridePayloadJSON: map[string]any{
			"quantity": 2,
		},
	})
	if err != nil {
		t.Fatalf("replay with mutation failed: %v", err)
	}
	if replayResDiff.Passed {
		t.Fatalf("expected replay to detect diff when quantity changed")
	}
	if len(replayResDiff.FieldDiffs) == 0 {
		t.Fatalf("expected field diffs, got 0")
	}
}
