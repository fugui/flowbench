package tests

import (
	"bytes"
	"net"
	"testing"
	"time"

	"github.com/fugui/flowbench/internal/codec"
	"github.com/fugui/flowbench/internal/codec/cros"
	"github.com/fugui/flowbench/internal/monitor"
	"github.com/fugui/flowbench/internal/protocol/schema"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

const paymentProto = `
syntax = "proto3";
package finance.payment;

message ProcessPaymentRequest {
  string order_id = 1;
  int64 amount = 2;
}

message ProcessPaymentResponse {
  string tx_id = 1;
  bool success = 2;
}
`

// TestCROS_Protocol_EndToEndInspectionAndSelfHealing 验证 CROS 复合报文深度解剖、Schema 绑定与序列号自愈重写
func TestCROS_Protocol_EndToEndInspectionAndSelfHealing(t *testing.T) {
	spec := codec.PresetCROSv1()
	if spec.PresetID != "cros-rpc-v1" {
		t.Fatalf("expected preset cros-rpc-v1, got %q", spec.PresetID)
	}

	// 1. 动态导入微服务 Protobuf Schema (零数字 OpCode 绑定)
	reg := schema.NewEmptyRegistry()
	if err := reg.LoadProtoSource("payment.proto", paymentProto, nil); err != nil {
		t.Fatalf("LoadProtoSource failed: %v", err)
	}

	// 2. 模拟编码 Protobuf 请求载荷
	reqMD, _, found := reg.ResolveDescriptorByContract("PaymentService", "ProcessPayment", schema.DirectionReq)
	if !found || reqMD == nil {
		t.Fatal("failed to resolve ProcessPaymentRequest descriptor")
	}

	dynReq := dynamicpb.NewMessage(reqMD)
	orderField := reqMD.Fields().ByName("order_id")
	amountField := reqMD.Fields().ByName("amount")
	dynReq.Set(orderField, protoreflect.ValueOfString("ORD-20261028-8888"))
	dynReq.Set(amountField, protoreflect.ValueOfInt64(5000))
	rawPayload, err := proto.Marshal(dynReq)
	if err != nil {
		t.Fatalf("marshal dynReq failed: %v", err)
	}

	// 构造 CROS 请求报文
	sno := uint64(99887766)
	serviceName := "PaymentService"
	operation := "ProcessPayment"
	reqWire, err := cros.BuildReqFrame(sno, serviceName, operation, rawPayload)
	if err != nil {
		t.Fatalf("BuildReqFrame failed: %v", err)
	}

	// 3. 使用平台动态拆包与解剖器读取 CROS 帧
	df, err := codec.ReadDynamicFrame(bytes.NewReader(reqWire), spec)
	if err != nil {
		t.Fatalf("ReadDynamicFrame failed: %v", err)
	}

	event := monitor.InspectDynamicFrame(df, reg, "INGRESS_REQ", "RECORD", 1, 1.2)
	if event.Header.ServiceName != serviceName {
		t.Errorf("expected ServiceName %q, got %q", serviceName, event.Header.ServiceName)
	}
	if event.Header.Operation != operation {
		t.Errorf("expected Operation %q, got %q", operation, event.Header.Operation)
	}
	if event.Header.MsgTypeName != "RPC_MSG_TYPE_REQ" {
		t.Errorf("expected MsgTypeName RPC_MSG_TYPE_REQ, got %q", event.Header.MsgTypeName)
	}
	if event.Header.RequestID != sno {
		t.Errorf("expected RequestID %d, got %d", sno, event.Header.RequestID)
	}
	if !event.Header.SchemaDecoded {
		t.Errorf("expected SchemaDecoded true")
	}
	if event.ProtobufJSON["orderId"] != "ORD-20261028-8888" && event.ProtobufJSON["order_id"] != "ORD-20261028-8888" {
		t.Errorf("expected orderId/order_id 'ORD-20261028-8888', got %v", event.ProtobufJSON)
	}

	// 4. 验证出站 Mock 响应报文生成与序列号原地自愈
	rspPayload := []byte("mock-response-payload")
	rspWire, err := cros.BuildRspFrame(sno, serviceName, operation, 0, rspPayload)
	if err != nil {
		t.Fatalf("BuildRspFrame failed: %v", err)
	}

	newSNO := uint64(123456789)
	rebuiltDF, err := codec.RebuildDynamicFrame(spec, rspWire[:cros.CommHeadLen+cros.RspHeadLen], newSNO, rspPayload)
	if err != nil {
		t.Fatalf("RebuildDynamicFrame failed: %v", err)
	}

	if rebuiltDF.SeqID != newSNO {
		t.Errorf("expected rebuilt SeqID %d, got %d", newSNO, rebuiltDF.SeqID)
	}

	// 再次解剖验证重构后的报文
	rebuiltWire := codec.EncodeDynamicFrameBytes(rebuiltDF)
	parsedRebuilt, err := cros.ReadFrame(bytes.NewReader(rebuiltWire))
	if err != nil {
		t.Fatalf("parse rebuilt frame failed: %v", err)
	}
	if parsedRebuilt.CommHead.SNO != newSNO {
		t.Errorf("expected rebuilt CommHead.SNO %d, got %d", newSNO, parsedRebuilt.CommHead.SNO)
	}
	if parsedRebuilt.RspHead.StatusCode != 0 {
		t.Errorf("expected StatusCode 0, got %d", parsedRebuilt.RspHead.StatusCode)
	}
}

// TestCROS_TCPNetworkRoundTrip 模拟真实 TCP 网络连接下的 CROS 客户端/服务端双向通信与拆粘包
func TestCROS_TCPNetworkRoundTrip(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on local tcp: %v", err)
	}
	defer ln.Close()

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		// 服务端读取 CROS 请求帧
		reqFrame, err := cros.ReadFrame(conn)
		if err != nil {
			t.Errorf("server ReadFrame failed: %v", err)
			return
		}

		if reqFrame.ReqHead.ServiceName != "InventorySvc" || reqFrame.ReqHead.Operation != "DeductStock" {
			t.Errorf("unexpected server request meta: %+v", reqFrame.ReqHead)
			return
		}

		// 服务端回写 CROS 应答帧
		rspWire, err := cros.BuildRspFrame(reqFrame.CommHead.SNO, reqFrame.ReqHead.ServiceName, reqFrame.ReqHead.Operation, 0, []byte("stock-deducted-ok"))
		if err != nil {
			t.Errorf("server BuildRspFrame failed: %v", err)
			return
		}
		_, _ = conn.Write(rspWire)
	}()

	// 客户端建立 TCP 连接
	conn, err := net.DialTimeout("tcp", ln.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("client dial failed: %v", err)
	}
	defer conn.Close()

	clientSNO := uint64(554433)
	reqWire, err := cros.BuildReqFrame(clientSNO, "InventorySvc", "DeductStock", []byte("item-sku-001"))
	if err != nil {
		t.Fatalf("client BuildReqFrame failed: %v", err)
	}

	if _, err := conn.Write(reqWire); err != nil {
		t.Fatalf("client write failed: %v", err)
	}

	rspFrame, err := cros.ReadFrame(conn)
	if err != nil {
		t.Fatalf("client read response failed: %v", err)
	}

	if rspFrame.CommHead.SNO != clientSNO {
		t.Errorf("client expected SNO %d, got %d", clientSNO, rspFrame.CommHead.SNO)
	}
	if rspFrame.RspHead.StatusCode != 0 {
		t.Errorf("client expected StatusCode 0, got %d", rspFrame.RspHead.StatusCode)
	}
	if string(rspFrame.Payload) != "stock-deducted-ok" {
		t.Errorf("client expected payload 'stock-deducted-ok', got %q", string(rspFrame.Payload))
	}

	select {
	case <-serverDone:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not complete in time")
	}
}
