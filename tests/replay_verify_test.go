package tests

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/fugui/flowbench/internal/backend"
	"github.com/fugui/flowbench/internal/codec"
	"github.com/fugui/flowbench/internal/protocol"
	"github.com/fugui/flowbench/internal/protocol/pb"
	"github.com/fugui/flowbench/internal/server"
	"github.com/fugui/flowbench/internal/transport"
)

// recordedInteraction 表示录制的一对原始 24B+Protobuf 二进制请求与响应帧
type recordedInteraction struct {
	RequestID uint64
	OpCode    uint8
	RawReq    []byte
	RawResp   []byte
}

// binaryTrafficRecorder 模拟 Keploy eBPF 探针在 Record 与 Test 阶段对 Ingress/Egress 二进制帧的捕获与 Mock 劫持
type binaryTrafficRecorder struct {
	mu          sync.Mutex
	ingressSet  []recordedInteraction
	egressMocks map[string][]byte // key: fmt.Sprintf("%d:%x", opCode, rawReqPayload) -> rawRespFrameBytes
}

func newBinaryTrafficRecorder() *binaryTrafficRecorder {
	return &binaryTrafficRecorder{
		egressMocks: make(map[string][]byte),
	}
}

func mockKey(f *codec.Frame) string {
	return fmt.Sprintf("op=%02x|payload=%x", f.Header.OpCode, f.Payload)
}

// TestBinaryRecordAndOfflineReplay 完整验证：
// 阶段 A (Record)：三端全开，捕获 Client->Server 入站二进制用例与 Server->Backend 出站二进制 Mock；
// 阶段 B (Replay / Backend Offline)：彻底关闭真实 Backend，挂载出站二进制 Mock，重放入站请求，断言响应 100% 逐字节一致；
// 阶段 C (Mutation Detection)：注入 Server 侧折扣计算缺陷 (discount_bug)，断言回放比对器立即检出二进制与 CRC32 偏差。
func TestBinaryRecordAndOfflineReplay(t *testing.T) {
	recorder := newBinaryTrafficRecorder()
	ctx := context.Background()

	// =========================================================================
	// 阶段 A：录制态 (Record Mode) —— 启动真实 Backend + 代理录制 Egress + 启动 Server
	// =========================================================================
	realStore := backend.NewDefaultStore()
	realBackendHandler := backend.NewHandler(realStore, nil)

	// 包装 Backend Handler 以镜像捕获 Server -> Backend 的出站 (Egress) 二进制流量作为 Mock
	recordingBackendServer := transport.NewServer(transport.ServerConfig{
		Name:       "recording-backend",
		ListenAddr: "127.0.0.1:0",
		IOTimeout:  3 * time.Second,
	}, func(ctx context.Context, reqFrame *codec.Frame) (*codec.Frame, error) {
		respFrame, err := realBackendHandler.HandleFrame(ctx, reqFrame)
		if err != nil {
			return nil, err
		}
		rawResp, err := codec.EncodeFrameBytes(respFrame)
		if err != nil {
			return nil, err
		}
		recorder.mu.Lock()
		recorder.egressMocks[mockKey(reqFrame)] = rawResp
		recorder.mu.Unlock()
		return respFrame, nil
	})

	backendAddr, err := recordingBackendServer.Start()
	if err != nil {
		t.Fatalf("start recording backend: %v", err)
	}

	sCfg := server.Config{
		ListenAddr:    "127.0.0.1:0",
		BackendAddr:   backendAddr,
		ConnMode:      transport.ConnModeShort,
		IOTimeout:     3 * time.Second,
		Deterministic: true,
		FaultMutation: server.MutationNone,
	}
	sHandler := server.NewHandler(sCfg, nil)
	sutServer := transport.NewServer(transport.ServerConfig{
		Name:       "sut-server-record",
		ListenAddr: "127.0.0.1:0",
		IOTimeout:  3 * time.Second,
	}, sHandler.HandleFrame)

	serverAddr, err := sutServer.Start()
	if err != nil {
		t.Fatalf("start sut server: %v", err)
	}

	cli := transport.NewClient(transport.ClientConfig{
		Name:       "record-client",
		TargetAddr: serverAddr,
		ConnMode:   transport.ConnModeShort,
		Timeout:    3 * time.Second,
	})

	// 构造覆盖纯入站、正常三层加工、前置短路、无库存拒单、授信评估的请求帧集合
	f1, _ := protocol.EncodeMessageFrame(codec.OpPingReq, 1001, codec.FlagDeterministic, codec.StatusOK, &pb.PingRequest{
		ClientId: "cli", Message: "ping", SentAtUnix: 1700000000,
	})
	f2, _ := protocol.EncodeMessageFrame(codec.OpOrderQuoteReq, 1002, codec.FlagDeterministic, codec.StatusOK, &pb.OrderQuoteRequest{
		UserId: "USR-VIP-001", SkuId: "SKU-PRO-01", Quantity: 5, VipLevel: 2, Region: "CN-EAST", ClientTimestamp: 1700000100,
	})
	f3, _ := protocol.EncodeMessageFrame(codec.OpOrderQuoteReq, 1003, codec.FlagDeterministic, codec.StatusOK, &pb.OrderQuoteRequest{
		UserId: "USR-VIP-001", SkuId: "SKU-PRO-01", Quantity: 0, VipLevel: 1, Region: "CN-EAST", ClientTimestamp: 1700000200,
	})
	f4, _ := protocol.EncodeMessageFrame(codec.OpOrderQuoteReq, 1004, codec.FlagDeterministic, codec.StatusOK, &pb.OrderQuoteRequest{
		UserId: "USR-VIP-001", SkuId: "SKU-OUT-OF-STOCK", Quantity: 2, VipLevel: 1, Region: "CN-EAST", ClientTimestamp: 1700000300,
	})
	f5, _ := protocol.EncodeMessageFrame(codec.OpCreditEvalReq, 1006, codec.FlagDeterministic, codec.StatusOK, &pb.CreditEvalRequest{
		UserId: "USR-VIP-001", RequestedLimitCents: 5000000, AnnualIncomeCents: 20000000, ExistingDebtCents: 2000000, ClientTimestamp: 1700000500,
	})

	for _, reqF := range []*codec.Frame{f1, f2, f3, f4, f5} {
		respF, err := cli.Call(ctx, reqF)
		if err != nil {
			t.Fatalf("record call reqID=%d failed: %v", reqF.Header.RequestID, err)
		}
		rawReq, _ := codec.EncodeFrameBytes(reqF)
		rawResp, _ := codec.EncodeFrameBytes(respF)
		recorder.ingressSet = append(recorder.ingressSet, recordedInteraction{
			RequestID: reqF.Header.RequestID,
			OpCode:    reqF.Header.OpCode,
			RawReq:    rawReq,
			RawResp:   rawResp,
		})
	}

	_ = cli.Close()
	_ = sutServer.Shutdown(ctx)
	_ = sHandler.Close()
	// 关键步骤：彻底关闭真实 Backend 服务！
	if err := recordingBackendServer.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown real backend failed: %v", err)
	}
	recordedBackendQueries := realStore.QueryCount()
	if recordedBackendQueries != 3 {
		t.Fatalf("expected exactly 3 backend queries during recording (reqIDs 1002, 1004, 1006), got %d", recordedBackendQueries)
	}

	// =========================================================================
	// 阶段 B：离线回放态 (Test Replay Mode) —— 真实 Backend 已关闭，仅靠二进制 Mock 回放
	// =========================================================================
	mockBackendServer := transport.NewServer(transport.ServerConfig{
		Name:       "keploy-ebpf-mock-proxy",
		ListenAddr: "127.0.0.1:0",
		IOTimeout:  3 * time.Second,
	}, func(ctx context.Context, reqFrame *codec.Frame) (*codec.Frame, error) {
		recorder.mu.Lock()
		rawMockResp, ok := recorder.egressMocks[mockKey(reqFrame)]
		recorder.mu.Unlock()
		if !ok {
			return nil, fmt.Errorf("mock proxy: no recorded binary mock for %s", mockKey(reqFrame))
		}
		mockFrame, err := codec.ReadFrame(bytes.NewReader(rawMockResp))
		if err != nil {
			return nil, err
		}
		// 保持当前请求的 RequestID
		mockFrame.Header.RequestID = reqFrame.Header.RequestID
		return mockFrame, nil
	})

	mockBackendAddr, err := mockBackendServer.Start()
	if err != nil {
		t.Fatalf("start mock backend failed: %v", err)
	}
	defer func() {
		_ = mockBackendServer.Shutdown(ctx)
	}()

	replayCfg := server.Config{
		ListenAddr:    "127.0.0.1:0",
		BackendAddr:   mockBackendAddr,
		ConnMode:      transport.ConnModeShort,
		IOTimeout:     3 * time.Second,
		Deterministic: true,
		FaultMutation: server.MutationNone,
	}
	replayHandler := server.NewHandler(replayCfg, nil)
	replaySUT := transport.NewServer(transport.ServerConfig{
		Name:       "sut-server-replay",
		ListenAddr: "127.0.0.1:0",
		IOTimeout:  3 * time.Second,
	}, replayHandler.HandleFrame)
	replayServerAddr, err := replaySUT.Start()
	if err != nil {
		t.Fatalf("start replay server: %v", err)
	}
	defer func() {
		_ = replaySUT.Shutdown(ctx)
		_ = replayHandler.Close()
	}()

	replayCli := transport.NewClient(transport.ClientConfig{
		Name:       "keploy-replay-driver",
		TargetAddr: replayServerAddr,
		ConnMode:   transport.ConnModeShort,
		Timeout:    3 * time.Second,
	})
	defer func() {
		_ = replayCli.Close()
	}()

	for _, tcItem := range recorder.ingressSet {
		reqFrame, err := codec.ReadFrame(bytes.NewReader(tcItem.RawReq))
		if err != nil {
			t.Fatalf("decode recorded req %d: %v", tcItem.RequestID, err)
		}
		actualRespFrame, err := replayCli.Call(ctx, reqFrame)
		if err != nil {
			t.Fatalf("replay call reqID=%d failed: %v", tcItem.RequestID, err)
		}
		actualRawResp, err := codec.EncodeFrameBytes(actualRespFrame)
		if err != nil {
			t.Fatalf("encode actual resp %d: %v", tcItem.RequestID, err)
		}
		if !bytes.Equal(actualRawResp, tcItem.RawResp) {
			t.Fatalf("replay binary mismatch on reqID=%d: recorded %d bytes (CRC=0x%08X), got %d bytes (CRC=0x%08X)",
				tcItem.RequestID, len(tcItem.RawResp), tcItem.RawResp[20:24], len(actualRawResp), actualRespFrame.Header.CRC32)
		}
	}

	// 确认回放期间真实 Backend 完全未被触碰（查询计数保持不变）
	if realStore.QueryCount() != recordedBackendQueries {
		t.Fatalf("real backend was unexpectedly called during offline replay!")
	}

	// =========================================================================
	// 阶段 C：突变拦截验证 (Mutation Regression Detection)
	// =========================================================================
	mutatedCfg := replayCfg
	mutatedCfg.FaultMutation = server.MutationDiscountBug
	mutatedHandler := server.NewHandler(mutatedCfg, nil)
	mutatedSUT := transport.NewServer(transport.ServerConfig{
		Name:       "sut-server-mutated",
		ListenAddr: "127.0.0.1:0",
		IOTimeout:  3 * time.Second,
	}, mutatedHandler.HandleFrame)
	mutatedAddr, err := mutatedSUT.Start()
	if err != nil {
		t.Fatalf("start mutated server: %v", err)
	}
	defer func() {
		_ = mutatedSUT.Shutdown(ctx)
		_ = mutatedHandler.Close()
	}()

	mutatedCli := transport.NewClient(transport.ClientConfig{
		TargetAddr: mutatedAddr,
		ConnMode:   transport.ConnModeShort,
		Timeout:    3 * time.Second,
	})
	defer func() {
		_ = mutatedCli.Close()
	}()

	// 重放 ReqID=1002 (VIP 订单计价)，断言必须检测到字节流与 CRC32 不一致
	recordedQuote := recorder.ingressSet[1]
	reqFrame1002, _ := codec.ReadFrame(bytes.NewReader(recordedQuote.RawReq))
	mutatedRespFrame, err := mutatedCli.Call(ctx, reqFrame1002)
	if err != nil {
		t.Fatalf("mutated call failed: %v", err)
	}
	mutatedRawResp, _ := codec.EncodeFrameBytes(mutatedRespFrame)
	if bytes.Equal(mutatedRawResp, recordedQuote.RawResp) {
		t.Fatalf("expected replay assertion to detect Server discount_bug mutation, but bytes were identical!")
	}
}
