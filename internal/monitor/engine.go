package monitor

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fugui/flowbench/internal/backend"
	"github.com/fugui/flowbench/internal/codec"
	"github.com/fugui/flowbench/internal/ebpf"
	"github.com/fugui/flowbench/internal/protocol"
	"github.com/fugui/flowbench/internal/protocol/pb"
	"github.com/fugui/flowbench/internal/server"
	"github.com/fugui/flowbench/internal/transport"
)

const (
	// DefaultVirtualServerPort 为 Client 连接的虚拟/逻辑 Server 端口 (由内核 cgroup/connect4 拦截)。
	DefaultVirtualServerPort uint16 = 9100
	// DefaultVirtualBackendPort 为 Server 连接的虚拟/逻辑 Backend 端口 (由内核 cgroup/connect4 拦截)。
	DefaultVirtualBackendPort uint16 = 9200
)

// RecordedCaseAsset 表示一条结构化可读的入站录制用例资产。
type RecordedCaseAsset struct {
	ScenarioID   string             `json:"scenario_id"`
	ScenarioName string             `json:"scenario_name"`
	RequestID    uint64             `json:"request_id"`
	OpName       string             `json:"op_name"`
	Request      CapturedFrameEvent `json:"request"`
	Response     CapturedFrameEvent `json:"response"`
	RawReqHex    string             `json:"raw_req_hex"`
	RawRespHex   string             `json:"raw_resp_hex"`
}

// RecordedMockAsset 表示一条结构化可读的出站二进制 Mock 资产。
type RecordedMockAsset struct {
	MockID       string             `json:"mock_id"`
	SemanticKey  string             `json:"semantic_key"`
	OpName       string             `json:"op_name"`
	OriginalReq  CapturedFrameEvent `json:"original_req"`
	OriginalResp CapturedFrameEvent `json:"original_resp"`
	RawReqHex    string             `json:"raw_req_hex"`
	RawRespHex   string             `json:"raw_resp_hex"`
	HitCount     uint64             `json:"hit_count"`
}

// ScenarioAssertionResult 表示单个业务场景在录制或回放实验中的验证结果及字段级 Diff。
type ScenarioAssertionResult struct {
	ScenarioID     string      `json:"scenario_id"`
	ScenarioName   string      `json:"scenario_name"`
	RequestID      uint64      `json:"request_id"`
	OpName         string      `json:"op_name"`
	Passed         bool        `json:"passed"`
	ExpectedCRC32  string      `json:"expected_crc32"`
	ActualCRC32    string      `json:"actual_crc32"`
	ByteExactMatch bool        `json:"byte_exact_match"`
	LatencyMs      float64     `json:"latency_ms"`
	ErrorMessage   string      `json:"error_message,omitempty"`
	FieldDiffs     []FieldDiff `json:"field_diffs,omitempty"`
}

// ExperimentReport 表示一次完整一键实验（Record / Replay / Mutation / Non-Deterministic）的总报告。
type ExperimentReport struct {
	Action          string                    `json:"action"`
	ModeDescription string                    `json:"mode_description"`
	StartedAt       string                    `json:"started_at"`
	DurationMs      float64                   `json:"duration_ms"`
	BackendOnline   bool                      `json:"backend_online"`
	EBPFStatus      ebpf.RuntimeStatus        `json:"ebpf_status"`
	TotalScenarios  int                       `json:"total_scenarios"`
	PassedScenarios int                       `json:"passed_scenarios"`
	FailedScenarios int                       `json:"failed_scenarios"`
	MocksLoaded     int                       `json:"mocks_loaded"`
	MocksConsumed   int                       `json:"mocks_consumed"`
	OverallPassed   bool                      `json:"overall_passed"`
	Scenarios       []ScenarioAssertionResult `json:"scenarios"`
}

// PersistedAssetBundle 为落盘保存至 data/native-ebpf-assets.json 的可读资产包。
type PersistedAssetBundle struct {
	GeneratedBy string              `json:"generated_by"`
	UpdatedAt   string              `json:"updated_at"`
	IngressSet  []RecordedCaseAsset `json:"ingress_test_cases"`
	EgressMocks []RecordedMockAsset `json:"egress_binary_mocks"`
}

// Engine 封装 cilium/ebpf 探针、协议感知代理、录制回放资产库与 SSE 实时事件广播。
type Engine struct {
	mu              sync.RWMutex
	seqCounter      atomic.Uint64
	interceptor     *ebpf.Interceptor
	assetFilePath   string
	virtualSrvPort  uint16
	virtualBackPort uint16

	ingressCases    []RecordedCaseAsset
	egressMocks     map[string]*RecordedMockAsset // key: SemanticSignature
	egressMockOrder []string
	recentFrames    []CapturedFrameEvent
	lastReport      *ExperimentReport

	subMu       sync.Mutex
	subscribers map[chan CapturedFrameEvent]struct{}
}

// EngineConfig 定义监控与回放引擎初始化配置。
type EngineConfig struct {
	AssetFilePath   string
	VirtualSrvPort  uint16
	VirtualBackPort uint16
}

// NewEngine 创建协议感知监控与回放引擎，并尝试初始化 cilium/ebpf cgroup/connect4 探针。
func NewEngine(cfg EngineConfig) *Engine {
	if cfg.AssetFilePath == "" {
		cfg.AssetFilePath = filepath.Join("data", "native-ebpf-assets.json")
	}
	if cfg.VirtualSrvPort == 0 {
		cfg.VirtualSrvPort = DefaultVirtualServerPort
	}
	if cfg.VirtualBackPort == 0 {
		cfg.VirtualBackPort = DefaultVirtualBackendPort
	}

	eng := &Engine{
		assetFilePath:   cfg.AssetFilePath,
		virtualSrvPort:  cfg.VirtualSrvPort,
		virtualBackPort: cfg.VirtualBackPort,
		egressMocks:     make(map[string]*RecordedMockAsset),
		subscribers:     make(map[chan CapturedFrameEvent]struct{}),
	}

	// 尝试挂载内核 cilium/ebpf cgroup/connect4 探针 (MonitorPID 设为 999999999 以使本进程内的 Client/Server 调用同样经受内核 eBPF 重定向)
	ic, err := ebpf.NewInterceptor(ebpf.Config{
		MonitorPID:       999999999,
		EnableIngress:    false,
		IngressOrigPort:  cfg.VirtualSrvPort,
		IngressProxyPort: cfg.VirtualSrvPort,
		EnableEgress:     false,
		EgressOrigPort:   cfg.VirtualBackPort,
		EgressProxyPort:  cfg.VirtualBackPort,
	})
	if err != nil {
		// 若非 root 环境，ic 仍包含降级原因，自动使用用户态代理模式
		eng.interceptor = ic
	} else {
		eng.interceptor = ic
	}

	_ = eng.loadPersistedAssets()
	return eng
}

// Subscribe 注册一个 SSE 实时二进制帧事件订阅通道。
func (e *Engine) Subscribe() chan CapturedFrameEvent {
	ch := make(chan CapturedFrameEvent, 64)
	e.subMu.Lock()
	e.subscribers[ch] = struct{}{}
	e.subMu.Unlock()
	return ch
}

// Unsubscribe 注销 SSE 订阅通道。
func (e *Engine) Unsubscribe(ch chan CapturedFrameEvent) {
	e.subMu.Lock()
	delete(e.subscribers, ch)
	close(ch)
	e.subMu.Unlock()
}

func (e *Engine) publishFrame(f *codec.Frame, direction, mode string, latencyMs float64) CapturedFrameEvent {
	seq := e.seqCounter.Add(1)
	ev := InspectFrame(f, direction, mode, seq, latencyMs)

	e.mu.Lock()
	e.recentFrames = append(e.recentFrames, ev)
	if len(e.recentFrames) > 120 {
		e.recentFrames = e.recentFrames[len(e.recentFrames)-120:]
	}
	e.mu.Unlock()

	e.subMu.Lock()
	for ch := range e.subscribers {
		select {
		case ch <- ev:
		default:
		}
	}
	e.subMu.Unlock()

	return ev
}

// Close 卸载内核 cilium/ebpf 探针并释放资源。
func (e *Engine) Close() error {
	if e.interceptor != nil {
		return e.interceptor.Close()
	}
	return nil
}

type standardScenarioSpec struct {
	ID       string
	Name     string
	ReqFrame *codec.Frame
}

func buildStandardScenarioSpecs() ([]standardScenarioSpec, error) {
	f1, err := protocol.EncodeMessageFrame(codec.OpPingReq, 1001, codec.FlagDeterministic, codec.StatusOK, &pb.PingRequest{
		ClientId:   "flowbench-client",
		Message:    "flowbench-probe",
		SentAtUnix: 1700000000,
	})
	if err != nil {
		return nil, err
	}
	f2, err := protocol.EncodeMessageFrame(codec.OpOrderQuoteReq, 1002, codec.FlagDeterministic, codec.StatusOK, &pb.OrderQuoteRequest{
		UserId:          "USR-VIP-001",
		SkuId:           "SKU-PRO-01",
		Quantity:        5,
		VipLevel:        2,
		Region:          "CN-EAST",
		ClientTimestamp: 1700000100,
	})
	if err != nil {
		return nil, err
	}
	f3, err := protocol.EncodeMessageFrame(codec.OpOrderQuoteReq, 1003, codec.FlagDeterministic, codec.StatusOK, &pb.OrderQuoteRequest{
		UserId:          "USR-VIP-001",
		SkuId:           "SKU-PRO-01",
		Quantity:        0,
		VipLevel:        1,
		Region:          "CN-EAST",
		ClientTimestamp: 1700000200,
	})
	if err != nil {
		return nil, err
	}
	f4, err := protocol.EncodeMessageFrame(codec.OpOrderQuoteReq, 1004, codec.FlagDeterministic, codec.StatusOK, &pb.OrderQuoteRequest{
		UserId:          "USR-VIP-001",
		SkuId:           "SKU-OUT-OF-STOCK",
		Quantity:        2,
		VipLevel:        1,
		Region:          "CN-EAST",
		ClientTimestamp: 1700000300,
	})
	if err != nil {
		return nil, err
	}
	f5, err := protocol.EncodeMessageFrame(codec.OpOrderQuoteReq, 1005, codec.FlagDeterministic, codec.StatusOK, &pb.OrderQuoteRequest{
		UserId:          "USR-LOW-003",
		SkuId:           "SKU-EDGE-02",
		Quantity:        1,
		VipLevel:        0,
		Region:          "US-WEST",
		ClientTimestamp: 1700000400,
	})
	if err != nil {
		return nil, err
	}
	f6, err := protocol.EncodeMessageFrame(codec.OpCreditEvalReq, 1006, codec.FlagDeterministic, codec.StatusOK, &pb.CreditEvalRequest{
		UserId:              "USR-VIP-001",
		RequestedLimitCents: 5000000,
		AnnualIncomeCents:   20000000,
		ExistingDebtCents:   2000000,
		ClientTimestamp:     1700000500,
	})
	if err != nil {
		return nil, err
	}

	return []standardScenarioSpec{
		{ID: "SC-01", Name: "链路探活与基础帧校验 (Ping/Pong)", ReqFrame: f1},
		{ID: "SC-02", Name: "VIP 阶梯订单计价与风控聚合 (OrderQuote)", ReqFrame: f2},
		{ID: "SC-03", Name: "Server 前置参数校验短路拦截 (Short-Circuit)", ReqFrame: f3},
		{ID: "SC-04", Name: "下游库存不足拒单分支 (Out-of-Stock)", ReqFrame: f4},
		{ID: "SC-04B", Name: "低信用分高额订单风控拒单 (Low-Credit)", ReqFrame: f5},
		{ID: "SC-05", Name: "跨 OpCode 客户授信额度评估 (CreditEval)", ReqFrame: f6},
	}, nil
}

// RunExperiment 执行指定的实验动作 ("record" | "replay" | "mutation" | "non_deterministic")。
func (e *Engine) RunExperiment(ctx context.Context, action string) (*ExperimentReport, error) {
	switch action {
	case "record":
		return e.runRecordSuite(ctx)
	case "replay":
		return e.runReplaySuite(ctx, true, server.MutationNone, "replay", "无 Backend 离线 eBPF 语义回放 (确定性模式)")
	case "mutation":
		return e.runReplaySuite(ctx, true, server.MutationDiscountBug, "mutation", "故障突变回归拦截实验 (-fault-mutation=discount_bug)")
	case "non_deterministic":
		return e.runReplaySuite(ctx, false, server.MutationNone, "non_deterministic", "非确定性时间戳对照实验 (-deterministic=false)")
	default:
		return nil, fmt.Errorf("unsupported experiment action: %q", action)
	}
}

// runRecordSuite 启动真实 Backend + Server + Ingress/Egress 协议感知代理，通过 cilium/ebpf 拦截并录制全套流量。
func (e *Engine) runRecordSuite(ctx context.Context) (*ExperimentReport, error) {
	startTime := time.Now()
	specs, err := buildStandardScenarioSpecs()
	if err != nil {
		return nil, err
	}

	// 1. 启动真实 Backend 服务
	store := backend.NewDefaultStore()
	bHandler := backend.NewHandler(store, nil)
	realBackendSrv := transport.NewServer(transport.ServerConfig{
		Name:       "real-backend",
		ListenAddr: "127.0.0.1:0",
		IOTimeout:  3 * time.Second,
	}, bHandler.HandleFrame)
	realBackendAddr, err := realBackendSrv.Start()
	if err != nil {
		return nil, fmt.Errorf("start real backend: %w", err)
	}
	defer func() {
		_ = realBackendSrv.Shutdown(context.Background())
	}()

	realBackendCli := transport.NewClient(transport.ClientConfig{
		Name:       "egress-proxy->backend",
		TargetAddr: realBackendAddr,
		ConnMode:   transport.ConnModeShort,
		Timeout:    3 * time.Second,
	})
	defer func() {
		_ = realBackendCli.Close()
	}()

	var newMocks []*RecordedMockAsset
	var mockMu sync.Mutex

	// 2. 启动 Egress 协议感知代理服务器 (被 cilium/ebpf 重定向的目标)
	egressProxySrv := transport.NewServer(transport.ServerConfig{
		Name:       "ebpf-egress-proxy-record",
		ListenAddr: "127.0.0.1:0",
		IOTimeout:  3 * time.Second,
	}, func(callCtx context.Context, reqFrame *codec.Frame) (*codec.Frame, error) {
		t0 := time.Now()
		reqEv := e.publishFrame(reqFrame, "EGRESS_REQ", "RECORD", 0)
		respFrame, callErr := realBackendCli.Call(callCtx, reqFrame)
		if callErr != nil {
			return nil, callErr
		}
		lat := float64(time.Since(t0).Microseconds()) / 1000.0
		respEv := e.publishFrame(respFrame, "EGRESS_RESP", "RECORD", lat)

		rawReqBytes, _ := codec.EncodeFrameBytes(reqFrame)
		rawRespBytes, _ := codec.EncodeFrameBytes(respFrame)
		semKey := SemanticSignature(reqFrame)

		mockMu.Lock()
		mockID := fmt.Sprintf("ebpf-mock-%d", len(newMocks))
		newMocks = append(newMocks, &RecordedMockAsset{
			MockID:       mockID,
			SemanticKey:  semKey,
			OpName:       fmt.Sprintf("%s -> %s", codec.OpCodeName(reqFrame.Header.OpCode), codec.OpCodeName(respFrame.Header.OpCode)),
			OriginalReq:  reqEv,
			OriginalResp: respEv,
			RawReqHex:    hex.EncodeToString(rawReqBytes),
			RawRespHex:   hex.EncodeToString(rawRespBytes),
			HitCount:     0,
		})
		mockMu.Unlock()

		return respFrame, nil
	})
	egressProxyAddr, err := egressProxySrv.Start()
	if err != nil {
		return nil, fmt.Errorf("start egress proxy: %w", err)
	}
	defer func() {
		_ = egressProxySrv.Shutdown(context.Background())
	}()
	egressProxyPort := uint16(mustParsePort(egressProxyAddr))

	// 3. 启动真实 Server (SUT)
	// 若内核 cilium/ebpf 处于激活态，Server 直接配置连向虚拟端口 127.0.0.1:9200，由内核 eBPF 透明重定向至 egressProxyPort！
	serverTargetBackendAddr := egressProxyAddr
	if e.interceptor != nil && e.interceptor.Status().KernelActive {
		serverTargetBackendAddr = fmt.Sprintf("127.0.0.1:%d", e.virtualBackPort)
	}

	sHandler := server.NewHandler(server.Config{
		ListenAddr:    "127.0.0.1:0",
		BackendAddr:   serverTargetBackendAddr,
		ConnMode:      transport.ConnModeShort,
		IOTimeout:     3 * time.Second,
		Deterministic: true,
		FaultMutation: server.MutationNone,
	}, nil)
	defer func() {
		_ = sHandler.Close()
	}()

	realServerSrv := transport.NewServer(transport.ServerConfig{
		Name:       "sut-server-record",
		ListenAddr: "127.0.0.1:0",
		IOTimeout:  3 * time.Second,
	}, sHandler.HandleFrame)
	realServerAddr, err := realServerSrv.Start()
	if err != nil {
		return nil, fmt.Errorf("start sut server: %w", err)
	}
	defer func() {
		_ = realServerSrv.Shutdown(context.Background())
	}()

	realServerCli := transport.NewClient(transport.ClientConfig{
		Name:       "ingress-proxy->server",
		TargetAddr: realServerAddr,
		ConnMode:   transport.ConnModeShort,
		Timeout:    3 * time.Second,
	})
	defer func() {
		_ = realServerCli.Close()
	}()

	// 4. 启动 Ingress 协议感知代理服务器 (支持非 HTTP 入站二进制流量透明拦截与录制)
	ingressProxySrv := transport.NewServer(transport.ServerConfig{
		Name:       "ebpf-ingress-proxy-record",
		ListenAddr: "127.0.0.1:0",
		IOTimeout:  3 * time.Second,
	}, func(callCtx context.Context, reqFrame *codec.Frame) (*codec.Frame, error) {
		t0 := time.Now()
		_ = e.publishFrame(reqFrame, "INGRESS_REQ", "RECORD", 0)
		respFrame, callErr := realServerCli.Call(callCtx, reqFrame)
		if callErr != nil {
			return nil, callErr
		}
		lat := float64(time.Since(t0).Microseconds()) / 1000.0
		_ = e.publishFrame(respFrame, "INGRESS_RESP", "RECORD", lat)
		return respFrame, nil
	})
	ingressProxyAddr, err := ingressProxySrv.Start()
	if err != nil {
		return nil, fmt.Errorf("start ingress proxy: %w", err)
	}
	defer func() {
		_ = ingressProxySrv.Shutdown(context.Background())
	}()
	ingressProxyPort := uint16(mustParsePort(ingressProxyAddr))

	// 5. 更新内核 cilium/ebpf Map，开启 9100 -> ingressProxyPort 与 9200 -> egressProxyPort 的透明劫持
	clientTargetAddr := ingressProxyAddr
	if e.interceptor != nil && e.interceptor.Status().KernelActive {
		if updateErr := e.interceptor.UpdateConfig(true, true, ingressProxyPort, egressProxyPort); updateErr == nil {
			clientTargetAddr = fmt.Sprintf("127.0.0.1:%d", e.virtualSrvPort)
			defer func() {
				_ = e.interceptor.UpdateConfig(false, false, ingressProxyPort, egressProxyPort)
			}()
		}
	}

	cli := transport.NewClient(transport.ClientConfig{
		Name:       "record-suite-driver",
		TargetAddr: clientTargetAddr,
		ConnMode:   transport.ConnModeShort,
		Timeout:    3 * time.Second,
	})
	defer func() {
		_ = cli.Close()
	}()

	var newCases []RecordedCaseAsset
	var scenarioResults []ScenarioAssertionResult
	passedCount := 0

	for _, sp := range specs {
		scStart := time.Now()
		respFrame, callErr := cli.Call(ctx, sp.ReqFrame)
		scLat := float64(time.Since(scStart).Microseconds()) / 1000.0
		if callErr != nil {
			scenarioResults = append(scenarioResults, ScenarioAssertionResult{
				ScenarioID:   sp.ID,
				ScenarioName: sp.Name,
				RequestID:    sp.ReqFrame.Header.RequestID,
				OpName:       codec.OpCodeName(sp.ReqFrame.Header.OpCode),
				Passed:       false,
				LatencyMs:    scLat,
				ErrorMessage: callErr.Error(),
			})
			continue
		}

		rawReqBytes, _ := codec.EncodeFrameBytes(sp.ReqFrame)
		rawRespBytes, _ := codec.EncodeFrameBytes(respFrame)
		crcHex := fmt.Sprintf("0x%08X", respFrame.Header.CRC32)

		newCases = append(newCases, RecordedCaseAsset{
			ScenarioID:   sp.ID,
			ScenarioName: sp.Name,
			RequestID:    sp.ReqFrame.Header.RequestID,
			OpName:       fmt.Sprintf("%s -> %s", codec.OpCodeName(sp.ReqFrame.Header.OpCode), codec.OpCodeName(respFrame.Header.OpCode)),
			Request:      InspectFrame(sp.ReqFrame, "INGRESS_REQ", "RECORD", sp.ReqFrame.Header.RequestID, 0),
			Response:     InspectFrame(respFrame, "INGRESS_RESP", "RECORD", sp.ReqFrame.Header.RequestID, scLat),
			RawReqHex:    hex.EncodeToString(rawReqBytes),
			RawRespHex:   hex.EncodeToString(rawRespBytes),
		})

		passedCount++
		scenarioResults = append(scenarioResults, ScenarioAssertionResult{
			ScenarioID:     sp.ID,
			ScenarioName:   sp.Name,
			RequestID:      sp.ReqFrame.Header.RequestID,
			OpName:         codec.OpCodeName(respFrame.Header.OpCode),
			Passed:         true,
			ExpectedCRC32:  crcHex,
			ActualCRC32:    crcHex,
			ByteExactMatch: true,
			LatencyMs:      scLat,
		})
	}

	// 保存最新录制的用例与二进制 Mock 资产
	e.mu.Lock()
	e.ingressCases = newCases
	e.egressMocks = make(map[string]*RecordedMockAsset, len(newMocks))
	e.egressMockOrder = make([]string, 0, len(newMocks))
	for _, m := range newMocks {
		e.egressMocks[m.SemanticKey] = m
		e.egressMockOrder = append(e.egressMockOrder, m.SemanticKey)
	}
	e.mu.Unlock()

	_ = e.savePersistedAssets()

	var ebpfSt ebpf.RuntimeStatus
	if e.interceptor != nil {
		ebpfSt = e.interceptor.Status()
	}

	report := &ExperimentReport{
		Action:          "record",
		ModeDescription: "三端全开全链路 eBPF 协议感知录制 (Ingress + Egress 雙层捕获)",
		StartedAt:       startTime.Format("15:04:05.000"),
		DurationMs:      float64(time.Since(startTime).Microseconds()) / 1000.0,
		BackendOnline:   true,
		EBPFStatus:      ebpfSt,
		TotalScenarios:  len(specs),
		PassedScenarios: passedCount,
		FailedScenarios: len(specs) - passedCount,
		MocksLoaded:     len(newMocks),
		MocksConsumed:   len(newMocks),
		OverallPassed:   passedCount == len(specs),
		Scenarios:       scenarioResults,
	}

	e.mu.Lock()
	e.lastReport = report
	e.mu.Unlock()

	return report, nil
}

// runReplaySuite 在彻底不启动 Backend 的前提下，通过 cilium/ebpf 劫持 Server->Backend(9200) 并执行智能自愈回放与字段级断言。
func (e *Engine) runReplaySuite(ctx context.Context, deterministic bool, faultMutation, actionName, modeDesc string) (*ExperimentReport, error) {
	e.mu.RLock()
	hasCases := len(e.ingressCases) > 0
	e.mu.RUnlock()

	if !hasCases {
		if _, err := e.runRecordSuite(ctx); err != nil {
			return nil, fmt.Errorf("auto-seed record before replay failed: %w", err)
		}
	}

	startTime := time.Now()
	var mocksConsumed atomic.Int32

	// 1. 启动协议感知离线 Mock 引擎 (真实 Backend 完全不启动！)
	mockEgressSrv := transport.NewServer(transport.ServerConfig{
		Name:       "ebpf-egress-mock-engine",
		ListenAddr: "127.0.0.1:0",
		IOTimeout:  3 * time.Second,
	}, func(callCtx context.Context, reqFrame *codec.Frame) (*codec.Frame, error) {
		t0 := time.Now()
		_ = e.publishFrame(reqFrame, "EGRESS_REQ", "REPLAY", 0)

		mockFrame, err := e.MatchAndHealMockResponse(reqFrame)
		if err != nil {
			return nil, err
		}
		mocksConsumed.Add(1)
		lat := float64(time.Since(t0).Microseconds()) / 1000.0
		_ = e.publishFrame(mockFrame, "EGRESS_MOCK_RESP", "REPLAY", lat)
		return mockFrame, nil
	})
	mockEgressAddr, err := mockEgressSrv.Start()
	if err != nil {
		return nil, fmt.Errorf("start mock egress engine: %w", err)
	}
	defer func() {
		_ = mockEgressSrv.Shutdown(context.Background())
	}()
	mockEgressPort := uint16(mustParsePort(mockEgressAddr))

	// 2. 启动被测 Server (SUT) —— 注意此时真实 Backend 根本不存在！
	serverTargetBackendAddr := mockEgressAddr
	if e.interceptor != nil && e.interceptor.Status().KernelActive {
		serverTargetBackendAddr = fmt.Sprintf("127.0.0.1:%d", e.virtualBackPort)
	}

	sHandler := server.NewHandler(server.Config{
		ListenAddr:    "127.0.0.1:0",
		BackendAddr:   serverTargetBackendAddr,
		ConnMode:      transport.ConnModeShort,
		IOTimeout:     3 * time.Second,
		Deterministic: deterministic,
		FaultMutation: faultMutation,
	}, nil)
	defer func() {
		_ = sHandler.Close()
	}()

	replaySrv := transport.NewServer(transport.ServerConfig{
		Name:       "sut-server-replay",
		ListenAddr: "127.0.0.1:0",
		IOTimeout:  3 * time.Second,
	}, sHandler.HandleFrame)
	replayServerAddr, err := replaySrv.Start()
	if err != nil {
		return nil, fmt.Errorf("start replay sut server: %w", err)
	}
	defer func() {
		_ = replaySrv.Shutdown(context.Background())
	}()

	replayServerCli := transport.NewClient(transport.ClientConfig{
		Name:       "ingress-replay->server",
		TargetAddr: replayServerAddr,
		ConnMode:   transport.ConnModeShort,
		Timeout:    3 * time.Second,
	})
	defer func() {
		_ = replayServerCli.Close()
	}()

	// 3. 启动 Ingress 代理捕获回放流量
	ingressReplaySrv := transport.NewServer(transport.ServerConfig{
		Name:       "ebpf-ingress-proxy-replay",
		ListenAddr: "127.0.0.1:0",
		IOTimeout:  3 * time.Second,
	}, func(callCtx context.Context, reqFrame *codec.Frame) (*codec.Frame, error) {
		t0 := time.Now()
		_ = e.publishFrame(reqFrame, "INGRESS_REQ", "REPLAY", 0)
		respFrame, callErr := replayServerCli.Call(callCtx, reqFrame)
		if callErr != nil {
			return nil, callErr
		}
		lat := float64(time.Since(t0).Microseconds()) / 1000.0
		_ = e.publishFrame(respFrame, "INGRESS_RESP", "REPLAY", lat)
		return respFrame, nil
	})
	ingressReplayAddr, err := ingressReplaySrv.Start()
	if err != nil {
		return nil, fmt.Errorf("start ingress replay proxy: %w", err)
	}
	defer func() {
		_ = ingressReplaySrv.Shutdown(context.Background())
	}()
	ingressReplayPort := uint16(mustParsePort(ingressReplayAddr))

	// 4. 激活 cilium/ebpf 内核端口劫持 (9100 -> ingressReplayPort, 9200 -> mockEgressPort)
	driverTargetAddr := ingressReplayAddr
	if e.interceptor != nil && e.interceptor.Status().KernelActive {
		if updateErr := e.interceptor.UpdateConfig(true, true, ingressReplayPort, mockEgressPort); updateErr == nil {
			driverTargetAddr = fmt.Sprintf("127.0.0.1:%d", e.virtualSrvPort)
			defer func() {
				_ = e.interceptor.UpdateConfig(false, false, ingressReplayPort, mockEgressPort)
			}()
		}
	}

	replayDriver := transport.NewClient(transport.ClientConfig{
		Name:       "replay-driver",
		TargetAddr: driverTargetAddr,
		ConnMode:   transport.ConnModeShort,
		Timeout:    3 * time.Second,
	})
	defer func() {
		_ = replayDriver.Close()
	}()

	e.mu.RLock()
	casesToRun := make([]RecordedCaseAsset, len(e.ingressCases))
	copy(casesToRun, e.ingressCases)
	totalMocks := len(e.egressMocks)
	e.mu.RUnlock()

	var results []ScenarioAssertionResult
	passedCount := 0

	for _, tc := range casesToRun {
		reqRaw, _ := hex.DecodeString(tc.RawReqHex)
		expRespRaw, _ := hex.DecodeString(tc.RawRespHex)

		reqFrame, err := codec.ReadFrame(bytes.NewReader(reqRaw))
		if err != nil {
			return nil, err
		}
		expRespFrame, err := codec.ReadFrame(bytes.NewReader(expRespRaw))
		if err != nil {
			return nil, err
		}

		scStart := time.Now()
		actRespFrame, callErr := replayDriver.Call(ctx, reqFrame)
		scLat := float64(time.Since(scStart).Microseconds()) / 1000.0
		if callErr != nil {
			results = append(results, ScenarioAssertionResult{
				ScenarioID:    tc.ScenarioID,
				ScenarioName:  tc.ScenarioName,
				RequestID:     tc.RequestID,
				OpName:        tc.OpName,
				Passed:        false,
				ExpectedCRC32: fmt.Sprintf("0x%08X", expRespFrame.Header.CRC32),
				LatencyMs:     scLat,
				ErrorMessage:  callErr.Error(),
			})
			continue
		}

		actRespRaw, _ := codec.EncodeFrameBytes(actRespFrame)
		byteEqual := bytes.Equal(expRespRaw, actRespRaw)
		var fieldDiffs []FieldDiff
		if !byteEqual {
			fieldDiffs = ComputeFieldDiffs(expRespFrame, actRespFrame)
		}

		if byteEqual {
			passedCount++
		}

		results = append(results, ScenarioAssertionResult{
			ScenarioID:     tc.ScenarioID,
			ScenarioName:   tc.ScenarioName,
			RequestID:      tc.RequestID,
			OpName:         tc.OpName,
			Passed:         byteEqual,
			ExpectedCRC32:  fmt.Sprintf("0x%08X", expRespFrame.Header.CRC32),
			ActualCRC32:    fmt.Sprintf("0x%08X", actRespFrame.Header.CRC32),
			ByteExactMatch: byteEqual,
			LatencyMs:      scLat,
			FieldDiffs:     fieldDiffs,
		})
	}

	var ebpfSt ebpf.RuntimeStatus
	if e.interceptor != nil {
		ebpfSt = e.interceptor.Status()
	}

	report := &ExperimentReport{
		Action:          actionName,
		ModeDescription: modeDesc,
		StartedAt:       startTime.Format("15:04:05.000"),
		DurationMs:      float64(time.Since(startTime).Microseconds()) / 1000.0,
		BackendOnline:   false, // 证明真实 Backend 100% 处于离线关闭状态
		EBPFStatus:      ebpfSt,
		TotalScenarios:  len(casesToRun),
		PassedScenarios: passedCount,
		FailedScenarios: len(casesToRun) - passedCount,
		MocksLoaded:     totalMocks,
		MocksConsumed:   int(mocksConsumed.Load()),
		OverallPassed:   passedCount == len(casesToRun),
		Scenarios:       results,
	}

	e.mu.Lock()
	e.lastReport = report
	e.mu.Unlock()

	return report, nil
}

// MatchAndHealMockResponse 根据请求的业务语义签名匹配录制的二进制 Mock，
// 并动态将响应帧头的 RequestID 改写为当前请求的 RequestID，同时自动重算 CRC32 校验和。
func (e *Engine) MatchAndHealMockResponse(reqFrame *codec.Frame) (*codec.Frame, error) {
	semKey := SemanticSignature(reqFrame)

	e.mu.Lock()
	mockAsset, ok := e.egressMocks[semKey]
	if ok {
		mockAsset.HitCount++
	}
	e.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("ebpf mock miss for semantic key %s (reqID=%d)", semKey, reqFrame.Header.RequestID)
	}

	rawRespBytes, err := hex.DecodeString(mockAsset.RawRespHex)
	if err != nil {
		return nil, fmt.Errorf("decode mock response hex: %w", err)
	}
	mockRespFrame, err := codec.ReadFrame(bytes.NewReader(rawRespBytes))
	if err != nil {
		return nil, fmt.Errorf("parse mock response frame: %w", err)
	}

	// 动态回填当前请求的 RequestID 并自愈重算 CRC32
	mockRespFrame.Header.RequestID = reqFrame.Header.RequestID
	mockRespFrame.Header.PayloadLen = uint32(len(mockRespFrame.Payload))
	mockRespFrame.Header.CRC32 = crc32.ChecksumIEEE(mockRespFrame.Payload)
	return mockRespFrame, nil
}

func (e *Engine) savePersistedAssets() error {
	e.mu.RLock()
	mocks := make([]RecordedMockAsset, 0, len(e.egressMockOrder))
	for _, k := range e.egressMockOrder {
		if m, ok := e.egressMocks[k]; ok {
			mocks = append(mocks, *m)
		}
	}
	bundle := PersistedAssetBundle{
		GeneratedBy: "flowbench-monitor (native cilium/ebpf protocol-aware engine)",
		UpdatedAt:   time.Now().Format(time.RFC3339),
		IngressSet:  e.ingressCases,
		EgressMocks: mocks,
	}
	e.mu.RUnlock()

	if err := os.MkdirAll(filepath.Dir(e.assetFilePath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(e.assetFilePath, data, 0o644)
}

func (e *Engine) loadPersistedAssets() error {
	data, err := os.ReadFile(e.assetFilePath)
	if err != nil {
		return err
	}
	var bundle PersistedAssetBundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		return err
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	e.ingressCases = bundle.IngressSet
	e.egressMocks = make(map[string]*RecordedMockAsset, len(bundle.EgressMocks))
	e.egressMockOrder = make([]string, 0, len(bundle.EgressMocks))
	for i := range bundle.EgressMocks {
		m := bundle.EgressMocks[i]
		e.egressMocks[m.SemanticKey] = &m
		e.egressMockOrder = append(e.egressMockOrder, m.SemanticKey)
	}
	return nil
}

func mustParsePort(addr string) int {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	var p int
	_, _ = fmt.Sscanf(portStr, "%d", &p)
	return p
}
