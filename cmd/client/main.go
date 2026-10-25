// flowbench-client 是三级微服务调用链的流量发起端与场景断言验证客户端。
// 支持单次拨测 (ping/quote/credit)、全套标准验证序列 (suite) 以及粘包/半包边界测试 (framing)。
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/fugui/flowbench/internal/codec"
	"github.com/fugui/flowbench/internal/protocol"
	"github.com/fugui/flowbench/internal/protocol/pb"
	"github.com/fugui/flowbench/internal/transport"
)

func main() {
	serverAddr := flag.String("server-addr", "127.0.0.1:9100", "目标 Server TCP 地址 (默认 127.0.0.1:9100)")
	mode := flag.String("mode", "suite", "运行模式: suite | ping | quote | credit | framing")
	connMode := flag.String("conn-mode", transport.ConnModeShort, "TCP 连接模式: short | keepalive")
	timeout := flag.Duration("timeout", 5*time.Second, "单次调用超时时间")
	debugFrame := flag.Bool("debug-frame", false, "是否打印 24B 帧头与 Payload Hex 调试信息")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cli := transport.NewClient(transport.ClientConfig{
		Name:       "client->server",
		TargetAddr: *serverAddr,
		ConnMode:   *connMode,
		Timeout:    *timeout,
		DebugFrame: *debugFrame,
		Logger:     logger,
	})
	defer func() {
		if err := cli.Close(); err != nil {
			logger.Warn("关闭客户端连接提示", "err", err)
		}
	}()

	ctx := context.Background()
	var runErr error

	switch *mode {
	case "ping":
		runErr = runPingScenario(ctx, cli, 1001)
	case "quote":
		runErr = runVIPOrderQuoteScenario(ctx, cli, 1002)
	case "credit":
		runErr = runCreditEvalScenario(ctx, cli, 1006)
	case "framing":
		runErr = runFramingTest(*serverAddr, *timeout)
	case "suite":
		runErr = runFullSuite(ctx, cli)
	default:
		logger.Error("未知运行模式", "mode", *mode)
		os.Exit(1)
	}

	if runErr != nil {
		logger.Error("flowbench-client 执行失败", "mode", *mode, "err", runErr)
		os.Exit(1)
	}
	logger.Info("flowbench-client 执行全部通过", "mode", *mode)
}

func runFullSuite(ctx context.Context, cli *transport.Client) error {
	type scenario struct {
		id   string
		name string
		fn   func() error
	}

	scenarios := []scenario{
		{
			id:   "SC-01",
			name: "链路探活与基础帧校验 (Ping/Pong, ReqID=1001)",
			fn:   func() error { return runPingScenario(ctx, cli, 1001) },
		},
		{
			id:   "SC-02",
			name: "VIP 阶梯订单计价与风控聚合 (OrderQuote, ReqID=1002)",
			fn:   func() error { return runVIPOrderQuoteScenario(ctx, cli, 1002) },
		},
		{
			id:   "SC-03",
			name: "Server 前置参数校验短路拦截 (Pre-Validation Short-Circuit, ReqID=1003)",
			fn:   func() error { return runPrecheckRejectScenario(ctx, cli, 1003) },
		},
		{
			id:   "SC-04",
			name: "下游库存不足拒单分支 (Out-of-Stock Reject, ReqID=1004)",
			fn:   func() error { return runOutOfStockScenario(ctx, cli, 1004) },
		},
		{
			id:   "SC-04B",
			name: "低信用分高额订单风控拒单 (Low-Credit Risk Reject, ReqID=1005)",
			fn:   func() error { return runLowCreditOrderScenario(ctx, cli, 1005) },
		},
		{
			id:   "SC-05",
			name: "跨 OpCode 客户授信额度评估 (CreditEval, ReqID=1006)",
			fn:   func() error { return runCreditEvalScenario(ctx, cli, 1006) },
		},
	}

	fmt.Println("================================================================================")
	fmt.Println("  flowbench 三级微服务自定义 Protobuf 二进制协议验证套件 (SC-01 ~ SC-05)")
	fmt.Println("================================================================================")

	for _, sc := range scenarios {
		if err := sc.fn(); err != nil {
			fmt.Printf("  [FAIL] %-6s | %s -> %v\n", sc.id, sc.name, err)
			return err
		}
		fmt.Printf("  [PASS] %-6s | %s\n", sc.id, sc.name)
	}
	fmt.Println("================================================================================")
	return nil
}

func runPingScenario(ctx context.Context, cli *transport.Client, reqID uint64) error {
	req := &pb.PingRequest{
		ClientId:   "flowbench-client",
		Message:    "flowbench-probe",
		SentAtUnix: 1700000000,
	}
	reqFrame, err := protocol.EncodeMessageFrame(codec.OpPingReq, reqID, codec.FlagDeterministic, codec.StatusOK, req)
	if err != nil {
		return err
	}
	respFrame, err := cli.Call(ctx, reqFrame)
	if err != nil {
		return err
	}
	var resp pb.PongResponse
	if err := protocol.DecodeMessageFrame(respFrame, codec.OpPongResp, &resp); err != nil {
		return err
	}
	if resp.BizCode != int32(pb.BizCode_BIZ_OK) || resp.EchoMessage != "PONG:flowbench-probe" || resp.Status != "SERVING" {
		return fmt.Errorf("unexpected PongResponse: %+v", &resp)
	}
	return nil
}

func runVIPOrderQuoteScenario(ctx context.Context, cli *transport.Client, reqID uint64) error {
	req := &pb.OrderQuoteRequest{
		UserId:          "USR-VIP-001",
		SkuId:           "SKU-PRO-01",
		Quantity:        5,
		VipLevel:        2,
		Region:          "CN-EAST",
		ClientTimestamp: 1700000100,
	}
	reqFrame, err := protocol.EncodeMessageFrame(codec.OpOrderQuoteReq, reqID, codec.FlagDeterministic, codec.StatusOK, req)
	if err != nil {
		return err
	}
	respFrame, err := cli.Call(ctx, reqFrame)
	if err != nil {
		return err
	}
	var resp pb.OrderQuoteResponse
	if err := protocol.DecodeMessageFrame(respFrame, codec.OpOrderQuoteResp, &resp); err != nil {
		return err
	}
	if resp.BizCode != int32(pb.BizCode_BIZ_OK) ||
		resp.SubtotalCents != 50000 ||
		resp.DiscountCents != 6500 ||
		resp.TaxCents != 2610 ||
		resp.ShippingCents != 4200 ||
		resp.FinalAmountCents != 50310 ||
		resp.RiskDecision != "APPROVED" ||
		len(resp.ProcessingDigest) != 16 {
		return fmt.Errorf("unexpected SC-02 OrderQuoteResponse: %+v", &resp)
	}
	return nil
}

func runPrecheckRejectScenario(ctx context.Context, cli *transport.Client, reqID uint64) error {
	req := &pb.OrderQuoteRequest{
		UserId:          "USR-VIP-001",
		SkuId:           "SKU-PRO-01",
		Quantity:        0, // 非法数量，触发 Server 前置短路拦截
		VipLevel:        1,
		Region:          "CN-EAST",
		ClientTimestamp: 1700000200,
	}
	reqFrame, err := protocol.EncodeMessageFrame(codec.OpOrderQuoteReq, reqID, codec.FlagDeterministic, codec.StatusOK, req)
	if err != nil {
		return err
	}
	respFrame, err := cli.Call(ctx, reqFrame)
	if err != nil {
		return err
	}
	var resp pb.OrderQuoteResponse
	if err := protocol.DecodeMessageFrame(respFrame, codec.OpOrderQuoteResp, &resp); err != nil {
		return err
	}
	if resp.BizCode != int32(pb.BizCode_BIZ_INVALID_ARGUMENT) || resp.RiskDecision != "REJECTED_PRECHECK" || resp.FinalAmountCents != 0 {
		return fmt.Errorf("unexpected SC-03 Precheck Response: %+v", &resp)
	}
	return nil
}

func runOutOfStockScenario(ctx context.Context, cli *transport.Client, reqID uint64) error {
	req := &pb.OrderQuoteRequest{
		UserId:          "USR-VIP-001",
		SkuId:           "SKU-OUT-OF-STOCK",
		Quantity:        2,
		VipLevel:        1,
		Region:          "CN-EAST",
		ClientTimestamp: 1700000300,
	}
	reqFrame, err := protocol.EncodeMessageFrame(codec.OpOrderQuoteReq, reqID, codec.FlagDeterministic, codec.StatusOK, req)
	if err != nil {
		return err
	}
	respFrame, err := cli.Call(ctx, reqFrame)
	if err != nil {
		return err
	}
	var resp pb.OrderQuoteResponse
	if err := protocol.DecodeMessageFrame(respFrame, codec.OpOrderQuoteResp, &resp); err != nil {
		return err
	}
	if resp.BizCode != int32(pb.BizCode_BIZ_OUT_OF_STOCK) || resp.RiskDecision != "REJECTED_STOCK" || resp.FinalAmountCents != 0 {
		return fmt.Errorf("unexpected SC-04 OutOfStock Response: %+v", &resp)
	}
	return nil
}

func runLowCreditOrderScenario(ctx context.Context, cli *transport.Client, reqID uint64) error {
	req := &pb.OrderQuoteRequest{
		UserId:          "USR-LOW-003",
		SkuId:           "SKU-EDGE-02",
		Quantity:        1,
		VipLevel:        0,
		Region:          "US-WEST",
		ClientTimestamp: 1700000400,
	}
	reqFrame, err := protocol.EncodeMessageFrame(codec.OpOrderQuoteReq, reqID, codec.FlagDeterministic, codec.StatusOK, req)
	if err != nil {
		return err
	}
	respFrame, err := cli.Call(ctx, reqFrame)
	if err != nil {
		return err
	}
	var resp pb.OrderQuoteResponse
	if err := protocol.DecodeMessageFrame(respFrame, codec.OpOrderQuoteResp, &resp); err != nil {
		return err
	}
	if resp.BizCode != int32(pb.BizCode_BIZ_RISK_REJECTED) || resp.RiskDecision != "REJECTED_LOW_CREDIT" || resp.FinalAmountCents != 0 {
		return fmt.Errorf("unexpected SC-04B LowCredit Response: %+v", &resp)
	}
	return nil
}

func runCreditEvalScenario(ctx context.Context, cli *transport.Client, reqID uint64) error {
	req := &pb.CreditEvalRequest{
		UserId:              "USR-VIP-001",
		RequestedLimitCents: 5000000,  // 申请 50,000.00 元
		AnnualIncomeCents:   20000000, // 年收入 200,000.00 元
		ExistingDebtCents:   2000000,  // 存量负债 20,000.00 元
		ClientTimestamp:     1700000500,
	}
	reqFrame, err := protocol.EncodeMessageFrame(codec.OpCreditEvalReq, reqID, codec.FlagDeterministic, codec.StatusOK, req)
	if err != nil {
		return err
	}
	respFrame, err := cli.Call(ctx, reqFrame)
	if err != nil {
		return err
	}
	var resp pb.CreditEvalResponse
	if err := protocol.DecodeMessageFrame(respFrame, codec.OpCreditEvalResp, &resp); err != nil {
		return err
	}
	// A级用户: 20,000,000 * 40% * 1.1(36月账龄) - 2,000,000/2 = 8,800,000 - 1,000,000 = 7,800,000
	// 获批 = min(5,000,000, 7,800,000) = 5,000,000
	if resp.BizCode != int32(pb.BizCode_BIZ_OK) ||
		resp.CreditGrade != "A" ||
		resp.DtiBps != 1000 ||
		resp.MaxAllowableCents != 7800000 ||
		resp.ApprovedLimitCents != 5000000 ||
		resp.Decision != "APPROVED" {
		return fmt.Errorf("unexpected SC-05 CreditEvalResponse: %+v", &resp)
	}
	return nil
}

// runFramingTest 专项验证 TCP 半包分片发送与多帧粘包合并发送
func runFramingTest(serverAddr string, timeout time.Duration) error {
	conn, err := net.DialTimeout("tcp", serverAddr, timeout)
	if err != nil {
		return fmt.Errorf("framing-test dial failed: %w", err)
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			return
		}
	}()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}

	// 1. 半包分片发送测试：将 1 个帧拆分为 3 段分次写入 TCP 流
	pingFrame, err := protocol.EncodeMessageFrame(codec.OpPingReq, 9001, codec.FlagDeterministic, codec.StatusOK, &pb.PingRequest{
		ClientId:   "framing-tester",
		Message:    "fragmented-frame",
		SentAtUnix: 1700000000,
	})
	if err != nil {
		return err
	}
	rawBytes, err := codec.EncodeFrameBytes(pingFrame)
	if err != nil {
		return err
	}
	if _, err := conn.Write(rawBytes[:5]); err != nil {
		return err
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := conn.Write(rawBytes[5:codec.HeaderSize]); err != nil {
		return err
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := conn.Write(rawBytes[codec.HeaderSize:]); err != nil {
		return err
	}

	resp1, err := codec.ReadFrame(conn)
	if err != nil {
		return fmt.Errorf("read fragmented response failed: %w", err)
	}
	if resp1.Header.RequestID != 9001 || resp1.Header.OpCode != codec.OpPongResp {
		return fmt.Errorf("unexpected fragmented response header: %+v", resp1.Header)
	}

	// 2. 粘包合并发送测试：将 3 个完整请求帧拼入单个缓冲区一次性 conn.Write 发送
	var coalesced bytes.Buffer
	for i := uint64(1); i <= 3; i++ {
		f, err := protocol.EncodeMessageFrame(codec.OpOrderQuoteReq, 9100+i, codec.FlagDeterministic, codec.StatusOK, &pb.OrderQuoteRequest{
			UserId:          "USR-VIP-001",
			SkuId:           "SKU-PRO-01",
			Quantity:        int32(i),
			VipLevel:        2,
			Region:          "CN-EAST",
			ClientTimestamp: 1700000100,
		})
		if err != nil {
			return err
		}
		if err := codec.WriteFrame(&coalesced, f); err != nil {
			return err
		}
	}

	if _, err := conn.Write(coalesced.Bytes()); err != nil {
		return fmt.Errorf("write coalesced frames failed: %w", err)
	}

	for i := uint64(1); i <= 3; i++ {
		rf, err := codec.ReadFrame(conn)
		if err != nil {
			return fmt.Errorf("read coalesced response %d failed: %w", i, err)
		}
		if rf.Header.RequestID != 9100+i {
			return fmt.Errorf("coalesced response RequestID mismatch: got %d, want %d", rf.Header.RequestID, 9100+i)
		}
		var qr pb.OrderQuoteResponse
		if err := protocol.DecodeMessageFrame(rf, codec.OpOrderQuoteResp, &qr); err != nil {
			return err
		}
		if qr.BizCode != int32(pb.BizCode_BIZ_OK) {
			return fmt.Errorf("coalesced quote %d failed with biz_code %d", i, qr.BizCode)
		}
	}

	fmt.Println("  [PASS] FRAMING | TCP 半包分片 (3段拆分) 与多帧粘包 (3帧合并单次Write) 全部验证通过")
	return nil
}
