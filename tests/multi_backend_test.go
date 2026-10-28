package tests

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fugui/flowbench/internal/backend"
	"github.com/fugui/flowbench/internal/codec"
	"github.com/fugui/flowbench/internal/monitor"
	"github.com/fugui/flowbench/internal/protocol"
	"github.com/fugui/flowbench/internal/protocol/pb"
	"github.com/fugui/flowbench/internal/transport"
)

// TestMultiBackend_SameHostMultiPortsAndRemoteIP 验证“同一主机多个端口 + 跨机远端 IP”的多下游 Backend 治理与 Mock 隔离能力。
func TestMultiBackend_SameHostMultiPortsAndRemoteIP(t *testing.T) {
	tmpAssetPath := filepath.Join(t.TempDir(), "multi-backend-assets.json")

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

	// 1. 模拟在同一主机上启动 2 个独立的真实后端服务：
	// Backend 1 (商品中心): 监听本地随机端口 A
	b1Store := backend.NewDefaultStore()
	b1Handler := backend.NewHandler(b1Store, nil)
	b1Srv := transport.NewServer(transport.ServerConfig{
		Name:       "backend-order-stock-service",
		ListenAddr: "127.0.0.1:0",
		IOTimeout:  3 * time.Second,
	}, b1Handler.HandleFrame)
	b1Addr, err := b1Srv.Start()
	if err != nil {
		t.Fatalf("start backend 1: %v", err)
	}
	defer func() {
		_ = b1Srv.Shutdown(context.Background())
	}()
	_, b1PortStr, _ := net.SplitHostPort(b1Addr)
	var b1Port uint16
	_, _ = fmt.Sscanf(b1PortStr, "%d", &b1Port)

	// Backend 2 (征信风控中心): 监听本地随机端口 B
	b2Store := backend.NewDefaultStore()
	b2Handler := backend.NewHandler(b2Store, nil)
	b2Srv := transport.NewServer(transport.ServerConfig{
		Name:       "backend-credit-risk-service",
		ListenAddr: "127.0.0.1:0",
		IOTimeout:  3 * time.Second,
	}, b2Handler.HandleFrame)
	b2Addr, err := b2Srv.Start()
	if err != nil {
		t.Fatalf("start backend 2: %v", err)
	}
	defer func() {
		_ = b2Srv.Shutdown(context.Background())
	}()
	_, b2PortStr, _ := net.SplitHostPort(b2Addr)
	var b2Port uint16
	_, _ = fmt.Sscanf(b2PortStr, "%d", &b2Port)

	// 2. 将被测服务 (TargetPID) 附着到多后端规则上
	// 包含同机多端口 (b1Port, b2Port) 以及一个模拟远端 IP (192.168.10.88:19204)
	attached, err := eng.AttachProcess(
		os.Getpid(),
		"sut-multi-backend-gateway",
		29100,
		"127.0.0.1",
		b1Port,
		monitor.BackendEndpoint{
			Name: "order-stock-backend",
			Host: "127.0.0.1",
			Port: b1Port,
		},
		monitor.BackendEndpoint{
			Name: "credit-risk-backend",
			Host: "127.0.0.1",
			Port: b2Port,
		},
		monitor.BackendEndpoint{
			Name: "remote-payment-backend",
			Host: "192.168.10.88",
			Port: 19204,
		},
	)
	if err != nil {
		t.Fatalf("attach multi-backend: %v", err)
	}
	if len(attached.Backends) != 3 {
		t.Fatalf("expected 3 backends attached, got %d", len(attached.Backends))
	}

	// 3. 测试通过平台 API 动态增删下游 Backend
	added, err := eng.AddBackendEndpoint(monitor.BackendEndpoint{
		Name: "dynamic-thirdparty-service",
		Host: "10.0.1.200",
		Port: 8888,
	})
	if err != nil {
		t.Fatalf("add backend: %v", err)
	}
	if added.Port != 8888 {
		t.Fatalf("unexpected added backend: %+v", added)
	}
	if len(eng.ListBackendEndpoints()) != 4 {
		t.Fatalf("expected 4 backends, got %d", len(eng.ListBackendEndpoints()))
	}
	_ = eng.RemoveBackendEndpoint("10.0.1.200", 8888)
	if len(eng.ListBackendEndpoints()) != 3 {
		t.Fatalf("expected 3 backends after removal, got %d", len(eng.ListBackendEndpoints()))
	}

	// 4. 验证多后端 Mock 签名隔离机制 (SemanticSignatureWithBackend)
	skuReq, err := protocol.EncodeMessageFrame(codec.OpBackendOrderQueryReq, 1002, codec.FlagDeterministic, codec.StatusOK, &pb.BackendOrderQueryRequest{
		SkuId:  "SKU-PRO-01",
		UserId: "USR-VIP-001",
	})
	if err != nil {
		t.Fatalf("encode sku req: %v", err)
	}

	sigB1 := monitor.SemanticSignatureWithBackend(b1Addr, skuReq)
	sigB2 := monitor.SemanticSignatureWithBackend(b2Addr, skuReq)
	if sigB1 == sigB2 {
		t.Fatalf("expected distinct signatures for different backends, got: %s", sigB1)
	}
	if sigB1 != fmt.Sprintf("%s#%s", b1Addr, monitor.SemanticSignature(skuReq)) {
		t.Fatalf("unexpected signature format: %s", sigB1)
	}

	// 5. 验证 Mock 资产在不同 Backend 间的隔离自愈匹配
	// 构造 B1 专有 Mock
	b1Resp, _ := protocol.EncodeMessageFrame(codec.OpBackendOrderQueryResp, 1002, codec.FlagDeterministic, codec.StatusOK, &pb.BackendOrderQueryResponse{
		Found:           true,
		BasePriceCents:  10000,
		AvailableStock:  50,
		UserCreditScore: 780,
	})
	b1ReqBytes, _ := codec.EncodeFrameBytes(skuReq)
	b1RespBytes, _ := codec.EncodeFrameBytes(b1Resp)

	// 直接注入隔离 Mock
	eng.InjectCustomMock(sigB1, fmt.Sprintf("%x", b1ReqBytes), fmt.Sprintf("%x", b1RespBytes))

	// 回放请求指定发给 B1：成功匹配并自愈 RequestID 为 999999
	queryFrame, _ := protocol.EncodeMessageFrame(codec.OpBackendOrderQueryReq, 999999, codec.FlagDeterministic, codec.StatusOK, &pb.BackendOrderQueryRequest{
		SkuId:  "SKU-PRO-01",
		UserId: "USR-VIP-001",
	})
	healedFrame, err := eng.MatchAndHealMockResponseWithBackend(b1Addr, queryFrame)
	if err != nil {
		t.Fatalf("expected match on B1: %v", err)
	}
	if healedFrame.Header.RequestID != 999999 {
		t.Fatalf("expected healed req_id=999999, got %d", healedFrame.Header.RequestID)
	}

	// 回放相同报文但发给 B2：由于签名隔离，应触发未命中或回退
	// 当 B2 未命中且无未带前缀的全局 Mock 时，应返回错误
	_, errB2 := eng.MatchAndHealMockResponseWithBackend(b2Addr, queryFrame)
	if errB2 == nil {
		t.Fatalf("expected miss on B2 when mock was registered exclusively for B1")
	}

	t.Logf("Multi-backend same host multi-port routing & mock isolation verified successfully!")
}
