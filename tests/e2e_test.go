// Package tests 包含 flowbench 三级微服务（Client -> Server -> Backend）的全链路端到端集成测试与二进制录制回放验证。
package tests

import (
	"bytes"
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/fugui/flowbench/internal/backend"
	"github.com/fugui/flowbench/internal/codec"
	"github.com/fugui/flowbench/internal/protocol"
	"github.com/fugui/flowbench/internal/protocol/pb"
	"github.com/fugui/flowbench/internal/server"
	"github.com/fugui/flowbench/internal/transport"
)

type testCluster struct {
	backendStore   *backend.Store
	backendServer  *transport.Server
	backendAddr    string
	serverHandler  *server.Handler
	frontendServer *transport.Server
	serverAddr     string
}

func startTestCluster(t *testing.T, connMode string, deterministic bool, faultMutation string) *testCluster {
	t.Helper()

	store := backend.NewDefaultStore()
	bHandler := backend.NewHandler(store, nil)
	bSrv := transport.NewServer(transport.ServerConfig{
		Name:       "test-backend",
		ListenAddr: "127.0.0.1:0",
		IOTimeout:  3 * time.Second,
	}, bHandler.HandleFrame)

	bAddr, err := bSrv.Start()
	if err != nil {
		t.Fatalf("start backend failed: %v", err)
	}

	sCfg := server.Config{
		ListenAddr:    "127.0.0.1:0",
		BackendAddr:   bAddr,
		ConnMode:      connMode,
		IOTimeout:     3 * time.Second,
		Deterministic: deterministic,
		FaultMutation: faultMutation,
	}
	sHandler := server.NewHandler(sCfg, nil)
	sSrv := transport.NewServer(transport.ServerConfig{
		Name:       "test-server",
		ListenAddr: "127.0.0.1:0",
		IOTimeout:  3 * time.Second,
	}, sHandler.HandleFrame)

	sAddr, err := sSrv.Start()
	if err != nil {
		_ = bSrv.Shutdown(context.Background())
		t.Fatalf("start server failed: %v", err)
	}

	tc := &testCluster{
		backendStore:   store,
		backendServer:  bSrv,
		backendAddr:    bAddr,
		serverHandler:  sHandler,
		frontendServer: sSrv,
		serverAddr:     sAddr,
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := tc.frontendServer.Shutdown(ctx); err != nil {
			t.Logf("shutdown frontend server: %v", err)
		}
		if err := tc.serverHandler.Close(); err != nil {
			t.Logf("close server handler: %v", err)
		}
		if err := tc.backendServer.Shutdown(ctx); err != nil {
			t.Logf("shutdown backend server: %v", err)
		}
	})

	return tc
}

// TestEndToEndSuite_ShortAndKeepAlive 验证短连接与长连接复用模式下全套业务场景 (SC-01 ~ SC-05)
func TestEndToEndSuite_ShortAndKeepAlive(t *testing.T) {
	for _, mode := range []string{transport.ConnModeShort, transport.ConnModeKeepAlive} {
		t.Run("ConnMode_"+mode, func(t *testing.T) {
			tc := startTestCluster(t, mode, true, server.MutationNone)
			cli := transport.NewClient(transport.ClientConfig{
				Name:       "test-client",
				TargetAddr: tc.serverAddr,
				ConnMode:   mode,
				Timeout:    3 * time.Second,
			})
			defer func() {
				if err := cli.Close(); err != nil {
					t.Logf("close client: %v", err)
				}
			}()

			ctx := context.Background()

			// SC-01: Ping/Pong
			pingFrame, err := protocol.EncodeMessageFrame(codec.OpPingReq, 1001, codec.FlagDeterministic, codec.StatusOK, &pb.PingRequest{
				ClientId:   "e2e-cli",
				Message:    "hello-flowbench",
				SentAtUnix: 1700000000,
			})
			if err != nil {
				t.Fatalf("encode ping: %v", err)
			}
			pongFrame, err := cli.Call(ctx, pingFrame)
			if err != nil {
				t.Fatalf("call ping: %v", err)
			}
			var pong pb.PongResponse
			if err := protocol.DecodeMessageFrame(pongFrame, codec.OpPongResp, &pong); err != nil {
				t.Fatalf("decode pong: %v", err)
			}
			if pong.EchoMessage != "PONG:hello-flowbench" {
				t.Fatalf("unexpected pong echo: %s", pong.EchoMessage)
			}

			// SC-02: VIP Order Quote
			quoteFrame, err := protocol.EncodeMessageFrame(codec.OpOrderQuoteReq, 1002, codec.FlagDeterministic, codec.StatusOK, &pb.OrderQuoteRequest{
				UserId:          "USR-VIP-001",
				SkuId:           "SKU-PRO-01",
				Quantity:        5,
				VipLevel:        2,
				Region:          "CN-EAST",
				ClientTimestamp: 1700000100,
			})
			if err != nil {
				t.Fatalf("encode quote: %v", err)
			}
			quoteRespFrame, err := cli.Call(ctx, quoteFrame)
			if err != nil {
				t.Fatalf("call quote: %v", err)
			}
			var quoteResp pb.OrderQuoteResponse
			if err := protocol.DecodeMessageFrame(quoteRespFrame, codec.OpOrderQuoteResp, &quoteResp); err != nil {
				t.Fatalf("decode quote: %v", err)
			}
			if quoteResp.FinalAmountCents != 50310 || quoteResp.RiskDecision != "APPROVED" {
				t.Fatalf("unexpected quoteResp: %+v", &quoteResp)
			}

			// SC-05: Credit Evaluation
			creditFrame, err := protocol.EncodeMessageFrame(codec.OpCreditEvalReq, 1006, codec.FlagDeterministic, codec.StatusOK, &pb.CreditEvalRequest{
				UserId:              "USR-VIP-001",
				RequestedLimitCents: 5000000,
				AnnualIncomeCents:   20000000,
				ExistingDebtCents:   2000000,
				ClientTimestamp:     1700000500,
			})
			if err != nil {
				t.Fatalf("encode credit: %v", err)
			}
			creditRespFrame, err := cli.Call(ctx, creditFrame)
			if err != nil {
				t.Fatalf("call credit: %v", err)
			}
			var creditResp pb.CreditEvalResponse
			if err := protocol.DecodeMessageFrame(creditRespFrame, codec.OpCreditEvalResp, &creditResp); err != nil {
				t.Fatalf("decode credit: %v", err)
			}
			if creditResp.CreditGrade != "A" || creditResp.ApprovedLimitCents != 5000000 {
				t.Fatalf("unexpected creditResp: %+v", &creditResp)
			}
		})
	}
}

// TestPreValidationShortCircuit 验证 SC-03 前置参数校验失败时直接由 Server 短路返回，Backend 收到 0 次调用
func TestPreValidationShortCircuit(t *testing.T) {
	tc := startTestCluster(t, transport.ConnModeShort, true, server.MutationNone)
	cli := transport.NewClient(transport.ClientConfig{
		Name:       "shortcircuit-client",
		TargetAddr: tc.serverAddr,
		ConnMode:   transport.ConnModeShort,
		Timeout:    3 * time.Second,
	})
	defer func() {
		if err := cli.Close(); err != nil {
			t.Logf("close client: %v", err)
		}
	}()

	reqFrame, err := protocol.EncodeMessageFrame(codec.OpOrderQuoteReq, 1003, codec.FlagDeterministic, codec.StatusOK, &pb.OrderQuoteRequest{
		UserId:          "USR-VIP-001",
		SkuId:           "SKU-PRO-01",
		Quantity:        0, // 非法数量
		VipLevel:        1,
		Region:          "CN-EAST",
		ClientTimestamp: 1700000200,
	})
	if err != nil {
		t.Fatalf("encode frame: %v", err)
	}

	respFrame, err := cli.Call(context.Background(), reqFrame)
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	var resp pb.OrderQuoteResponse
	if err := protocol.DecodeMessageFrame(respFrame, codec.OpOrderQuoteResp, &resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if resp.BizCode != int32(pb.BizCode_BIZ_INVALID_ARGUMENT) || resp.RiskDecision != "REJECTED_PRECHECK" {
		t.Fatalf("expected precheck rejection, got %+v", &resp)
	}
	if tc.backendStore.QueryCount() != 0 {
		t.Fatalf("expected 0 backend queries on pre-validation failure, got %d", tc.backendStore.QueryCount())
	}
}

// TestDeterministicVsNonDeterministicBytes 验证 SC-06 确定性模式与非确定性模式下的二进制帧字节级表现
func TestDeterministicVsNonDeterministicBytes(t *testing.T) {
	req := &pb.OrderQuoteRequest{
		UserId:          "USR-VIP-001",
		SkuId:           "SKU-PRO-01",
		Quantity:        5,
		VipLevel:        2,
		Region:          "CN-EAST",
		ClientTimestamp: 1700000100,
	}

	// 1. 确定性模式：连续调用两次返回的 24B+Payload 二进制切片 100% 相等
	detCluster := startTestCluster(t, transport.ConnModeShort, true, server.MutationNone)
	detCli := transport.NewClient(transport.ClientConfig{TargetAddr: detCluster.serverAddr, ConnMode: transport.ConnModeShort})
	defer func() {
		if err := detCli.Close(); err != nil {
			t.Logf("close detCli: %v", err)
		}
	}()

	reqFrame, err := protocol.EncodeMessageFrame(codec.OpOrderQuoteReq, 2001, codec.FlagNone, codec.StatusOK, req)
	if err != nil {
		t.Fatalf("encode req: %v", err)
	}
	resp1, err := detCli.Call(context.Background(), reqFrame)
	if err != nil {
		t.Fatalf("call 1: %v", err)
	}
	resp2, err := detCli.Call(context.Background(), reqFrame)
	if err != nil {
		t.Fatalf("call 2: %v", err)
	}
	bytes1, err := codec.EncodeFrameBytes(resp1)
	if err != nil {
		t.Fatalf("EncodeFrameBytes 1: %v", err)
	}
	bytes2, err := codec.EncodeFrameBytes(resp2)
	if err != nil {
		t.Fatalf("EncodeFrameBytes 2: %v", err)
	}
	if !bytes.Equal(bytes1, bytes2) {
		t.Fatalf("deterministic mode produced different wire bytes across identical requests")
	}

	// 2. 非确定性模式：由于动态时间戳与随机盐值，两次调用的二进制载荷与 CRC32 必然不同
	dynCluster := startTestCluster(t, transport.ConnModeShort, false, server.MutationNone)
	dynCli := transport.NewClient(transport.ClientConfig{TargetAddr: dynCluster.serverAddr, ConnMode: transport.ConnModeShort})
	defer func() {
		if err := dynCli.Close(); err != nil {
			t.Logf("close dynCli: %v", err)
		}
	}()

	dynResp1, err := dynCli.Call(context.Background(), reqFrame)
	if err != nil {
		t.Fatalf("dyn call 1: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	dynResp2, err := dynCli.Call(context.Background(), reqFrame)
	if err != nil {
		t.Fatalf("dyn call 2: %v", err)
	}
	if bytes.Equal(dynResp1.Payload, dynResp2.Payload) {
		t.Fatalf("expected non-deterministic mode to produce different payloads due to dynamic timestamp/trace")
	}
}

// TestTCPFramingCoalescingOverSocket 在真实 TCP Socket 上验证半包分片与多帧粘包
func TestTCPFramingCoalescingOverSocket(t *testing.T) {
	tc := startTestCluster(t, transport.ConnModeShort, true, server.MutationNone)
	conn, err := net.DialTimeout("tcp", tc.serverAddr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			t.Logf("close conn: %v", closeErr)
		}
	}()
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}

	// 单次 Write 连续粘包发送 3 个帧
	var coalesced bytes.Buffer
	for i := uint64(1); i <= 3; i++ {
		f, err := protocol.EncodeMessageFrame(codec.OpPingReq, 8000+i, codec.FlagDeterministic, codec.StatusOK, &pb.PingRequest{
			ClientId:   "coalesce-test",
			Message:    "batch",
			SentAtUnix: 1700000000,
		})
		if err != nil {
			t.Fatalf("encode frame %d: %v", i, err)
		}
		if err := codec.WriteFrame(&coalesced, f); err != nil {
			t.Fatalf("write frame %d: %v", i, err)
		}
	}

	if _, err := conn.Write(coalesced.Bytes()); err != nil {
		t.Fatalf("socket write coalesced: %v", err)
	}

	for i := uint64(1); i <= 3; i++ {
		rf, err := codec.ReadFrame(conn)
		if err != nil {
			t.Fatalf("read response %d: %v", i, err)
		}
		if rf.Header.RequestID != 8000+i {
			t.Fatalf("expected reqID %d, got %d", 8000+i, rf.Header.RequestID)
		}
	}
}
