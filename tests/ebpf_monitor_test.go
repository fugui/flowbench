package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fugui/flowbench/internal/codec"
	"github.com/fugui/flowbench/internal/monitor"
	"github.com/fugui/flowbench/internal/protocol"
	"github.com/fugui/flowbench/internal/protocol/pb"
)

// TestEBPFMonitor_FullLifecycleAndWebConsole 验证原生 cilium/ebpf 监控引擎的录制、无 Backend 离线回放、
// 动态 RequestID 自愈、字段级突变 Diff 及通用平台 Web 控制台全套接口。
func TestEBPFMonitor_FullLifecycleAndWebConsole(t *testing.T) {
	tmpAssetPath := filepath.Join(t.TempDir(), "native-ebpf-assets.json")

	eng := monitor.NewEngine(monitor.EngineConfig{
		AssetFilePath:   tmpAssetPath,
		VirtualSrvPort:  29100,
		VirtualBackPort: 29200,
	})
	defer func() {
		if err := eng.Close(); err != nil {
			t.Fatalf("close engine: %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// 1. 执行全链路录制 (Record)
	recRep, err := eng.RunExperiment(ctx, "record")
	if err != nil {
		t.Fatalf("RunExperiment(record) failed: %v", err)
	}
	if !recRep.OverallPassed || recRep.PassedScenarios != 6 || recRep.MocksLoaded != 4 {
		t.Fatalf("unexpected record report: %+v", recRep)
	}
	if os.Geteuid() == 0 {
		if !recRep.EBPFStatus.KernelActive || recRep.EBPFStatus.StatIngressRedirected < 6 || recRep.EBPFStatus.StatEgressRedirected < 4 {
			t.Fatalf("expected active kernel cilium/ebpf redirection when running as root, got: %+v", recRep.EBPFStatus)
		}
		t.Logf("Kernel cilium/ebpf active! progID=%d, ingressRedirected=%d, egressRedirected=%d",
			recRep.EBPFStatus.ProgramID, recRep.EBPFStatus.StatIngressRedirected, recRep.EBPFStatus.StatEgressRedirected)
	}

	// 2. 执行无 Backend 离线回放 (Replay)
	repRep, err := eng.RunExperiment(ctx, "replay")
	if err != nil {
		t.Fatalf("RunExperiment(replay) failed: %v", err)
	}
	if repRep.BackendOnline {
		t.Fatalf("expected BackendOnline=false during offline replay")
	}
	if !repRep.OverallPassed || repRep.PassedScenarios != 6 || repRep.MocksConsumed != 4 {
		t.Fatalf("unexpected offline replay report: %+v", repRep)
	}

	// 3. 验证智能语义匹配与动态 RequestID + CRC32 自愈重写 (REPLAY-03)
	dynamicReqFrame, err := protocol.EncodeMessageFrame(codec.OpOrderQuoteReq, 999888, codec.FlagDeterministic, codec.StatusOK, &pb.OrderQuoteRequest{
		UserId:          "USR-VIP-001",
		SkuId:           "SKU-PRO-01",
		Quantity:        5,
		VipLevel:        2,
		Region:          "CN-EAST",
		ClientTimestamp: 1700000100,
	})
	if err != nil {
		t.Fatalf("encode dynamic req: %v", err)
	}
	skuReqFrame, err := protocol.EncodeMessageFrame(codec.OpBackendOrderQueryReq, dynamicReqFrame.Header.RequestID, codec.FlagDeterministic, codec.StatusOK, &pb.BackendOrderQueryRequest{
		SkuId:  "SKU-PRO-01",
		UserId: "USR-VIP-001",
		Region: "CN-EAST",
	})
	if err != nil {
		t.Fatalf("encode skuReqFrame: %v", err)
	}
	healedMockResp, err := eng.MatchAndHealMockResponse(skuReqFrame)
	if err != nil {
		t.Fatalf("MatchAndHealMockResponse failed on dynamic RequestID: %v", err)
	}
	if healedMockResp.Header.RequestID != 999888 {
		t.Fatalf("expected healed RequestID=999888, got %d", healedMockResp.Header.RequestID)
	}
	rawHealed, err := codec.EncodeFrameBytes(healedMockResp)
	if err != nil {
		t.Fatalf("EncodeFrameBytes failed: %v", err)
	}
	if _, err := codec.ReadFrame(bytes.NewReader(rawHealed)); err != nil {
		t.Fatalf("healed frame failed ReadFrame validation: %v", err)
	}

	// 4. 验证故障突变注入与字段级 Diff 定位 (REPLAY-04)
	mutRep, err := eng.RunExperiment(ctx, "mutation")
	if err != nil {
		t.Fatalf("RunExperiment(mutation) failed: %v", err)
	}
	if mutRep.OverallPassed {
		t.Fatalf("expected mutation replay to fail overall assertion")
	}
	foundDiscountDiff := false
	for _, sc := range mutRep.Scenarios {
		if sc.ScenarioID == "SC-02" {
			if sc.Passed {
				t.Fatalf("expected SC-02 to fail under discount_bug mutation")
			}
			for _, df := range sc.FieldDiffs {
				if df.Field == "payload.discount_cents" {
					foundDiscountDiff = true
				}
			}
		}
	}
	if !foundDiscountDiff {
		t.Fatalf("expected field-level diff to pinpoint payload.discount_cents on SC-02")
	}

	// 5. 验证通用平台 Web 控制台 API 闭环 (M6: /api/processes, /api/attach, /api/protocol/spec, /api/protocol/schema, /api/mocks/update)
	ws := monitor.NewWebServer("127.0.0.1:0", eng)
	addr, err := ws.Start()
	if err != nil {
		t.Fatalf("start web server: %v", err)
	}
	defer func() {
		_ = ws.Shutdown(context.Background())
	}()
	baseURL := "http://" + addr

	// 5.1 GET /api/status
	resp, err := http.Get(baseURL + "/api/status")
	if err != nil {
		t.Fatalf("GET /api/status: %v", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" || resp.Header.Get("X-Frame-Options") != "DENY" {
		t.Fatalf("missing mandatory security headers: %+v", resp.Header)
	}
	var stBody monitor.DashboardStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&stBody); err != nil {
		t.Fatalf("decode status response: %v", err)
	}
	if len(stBody.RecordedCases) != 6 || len(stBody.RecordedMocks) != 4 || len(stBody.FramingPresets) != 4 {
		t.Fatalf("unexpected dashboard status counts: cases=%d, mocks=%d, presets=%d",
			len(stBody.RecordedCases), len(stBody.RecordedMocks), len(stBody.FramingPresets))
	}

	// 5.2 GET /api/processes (/proc 进程扫描)
	procResp, err := http.Get(baseURL + "/api/processes")
	if err != nil {
		t.Fatalf("GET /api/processes: %v", err)
	}
	_ = procResp.Body.Close()
	if procResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from /api/processes, got %d", procResp.StatusCode)
	}

	// 5.3 POST /api/attach & /api/detach (TargetPID 过滤与跨 IP 绑定)
	attachPayload := `{"target_pid":28412,"process_name":"trade-srv","ingress_port":9100,"egress_ip":"10.0.1.20","egress_port":8000}`
	attResp, err := http.Post(baseURL+"/api/attach", "application/json", strings.NewReader(attachPayload))
	if err != nil {
		t.Fatalf("POST /api/attach: %v", err)
	}
	_ = attResp.Body.Close()
	if attResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from /api/attach, got %d", attResp.StatusCode)
	}

	detResp, err := http.Post(baseURL+"/api/detach", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST /api/detach: %v", err)
	}
	_ = detResp.Body.Close()
	if detResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from /api/detach, got %d", detResp.StatusCode)
	}

	// 5.4 POST /api/protocol/schema (测试无 Schema Wire Tag 树降级与即时重激活)
	clearResp, err := http.Post(baseURL+"/api/protocol/schema", "application/json", strings.NewReader(`{"action":"clear"}`))
	if err != nil {
		t.Fatalf("POST /api/protocol/schema(clear): %v", err)
	}
	_ = clearResp.Body.Close()
	if clearResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK on schema clear, got %d", clearResp.StatusCode)
	}

	restoreResp, err := http.Post(baseURL+"/api/protocol/schema", "application/json", strings.NewReader(`{"action":"restore_default"}`))
	if err != nil {
		t.Fatalf("POST /api/protocol/schema(restore_default): %v", err)
	}
	_ = restoreResp.Body.Close()
	if restoreResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK on schema restore, got %d", restoreResp.StatusCode)
	}

	// 5.5 POST /api/mocks/update (在线修改 Mock JSON 并通过 dynamicpb 重编码二进制帧 + Checksum 自愈)
	firstMockID := stBody.RecordedMocks[0].MockID
	origCRC := stBody.RecordedMocks[0].OriginalResp.Header.CRC32Hex
	updateBody, _ := json.Marshal(map[string]any{
		"mock_id": firstMockID,
		"response_json": map[string]any{
			"found":           true,
			"skuId":           "SKU-PRO-01",
			"skuName":         "Enterprise Gateway Modified",
			"basePriceCents":  8800,
			"availableStock":  99,
			"unitWeightGrams": 500,
			"warehouseCode":   "WH-SH-01",
			"userCreditScore": 820,
			"riskBlacklisted": false,
		},
	})
	updResp, err := http.Post(baseURL+"/api/mocks/update", "application/json", bytes.NewReader(updateBody))
	if err != nil {
		t.Fatalf("POST /api/mocks/update: %v", err)
	}
	var updResult struct {
		UpdatedMock monitor.RecordedMockAsset `json:"updated_mock"`
	}
	if err := json.NewDecoder(updResp.Body).Decode(&updResult); err != nil {
		_ = updResp.Body.Close()
		t.Fatalf("decode updated mock: %v", err)
	}
	_ = updResp.Body.Close()
	if updResult.UpdatedMock.OriginalResp.Header.CRC32Hex == origCRC {
		t.Fatalf("expected updated mock CRC32 to be recomputed after JSON modification")
	}

	// 5.6 POST /api/protocol/spec (切换 generic-financial-v2 20B 协议模板并即时模拟拆帧)
	specResp, err := http.Post(baseURL+"/api/protocol/spec", "application/json", strings.NewReader(`{"preset_id":"generic-financial-v2","simulate":true}`))
	if err != nil {
		t.Fatalf("POST /api/protocol/spec: %v", err)
	}
	_ = specResp.Body.Close()
	if specResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from /api/protocol/spec, got %d", specResp.StatusCode)
	}
}
