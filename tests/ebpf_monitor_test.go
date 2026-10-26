package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fugui/flowbench/internal/codec"
	"github.com/fugui/flowbench/internal/monitor"
	"github.com/fugui/flowbench/internal/protocol"
	"github.com/fugui/flowbench/internal/protocol/pb"
)

// TestEBPFMonitor_FullLifecycleAndWebConsole 验证原生 cilium/ebpf 监控引擎的录制、无 Backend 离线回放、
// 动态 RequestID 自愈、字段级突变 Diff、Keploy Base64 逆向解码及安全 Web 控制台接口。
func TestEBPFMonitor_FullLifecycleAndWebConsole(t *testing.T) {
	tmpAssetPath := filepath.Join(t.TempDir(), "native-ebpf-assets.json")
	keployMocksPath := filepath.Join("..", "keploy", "keploy", "flowbench-e2e", "mocks.yaml")

	eng := monitor.NewEngine(monitor.EngineConfig{
		AssetFilePath:   tmpAssetPath,
		KeployMocksPath: keployMocksPath,
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
	// 构造对应的下游 BackendOrderQueryReq (RequestID = 999888，不同于录制时的 1002)
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
	// 验证自愈后的帧可以通过严格的 codec.ReadFrame (含 CRC32 校验)
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

	// 5. 验证 Keploy mocks.yaml Base64 逆向解码器 (REPLAY-05)
	if _, statErr := os.Stat(keployMocksPath); statErr == nil {
		decodedMocks, decErr := monitor.DecodeKeployMocksFile(keployMocksPath)
		if decErr != nil {
			t.Fatalf("DecodeKeployMocksFile failed: %v", decErr)
		}
		if len(decodedMocks) != 4 {
			t.Fatalf("expected 4 decoded Keploy mocks, got %d", len(decodedMocks))
		}
		for _, dm := range decodedMocks {
			if !dm.DecodedRequest.CRC32Valid || !dm.DecodedResponse.CRC32Valid {
				t.Fatalf("expected valid CRC32 on decoded Keploy mock %s", dm.Name)
			}
		}
	}

	// 6. 验证安全 Web 控制台接口与安全响应头
	ws := monitor.NewWebServer("127.0.0.1:0", eng)
	addr, err := ws.Start()
	if err != nil {
		t.Fatalf("start web server: %v", err)
	}
	defer func() {
		_ = ws.Shutdown(context.Background())
	}()

	resp, err := http.Get("http://" + addr + "/api/status")
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
	if len(stBody.RecordedCases) != 6 || len(stBody.RecordedMocks) != 4 {
		t.Fatalf("unexpected dashboard status counts: cases=%d, mocks=%d", len(stBody.RecordedCases), len(stBody.RecordedMocks))
	}
}
