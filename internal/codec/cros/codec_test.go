package cros

import (
	"bytes"
	"io"
	"testing"
)

// fragReader 每次仅向调用方返回 1 字节数据，用于严苛模拟极端 TCP 半包流
type fragReader struct {
	data []byte
	pos  int
}

func (fr *fragReader) Read(p []byte) (n int, err error) {
	if fr.pos >= len(fr.data) {
		return 0, io.EOF
	}
	p[0] = fr.data[fr.pos]
	fr.pos++
	return 1, nil
}

// TestCROS_ReqAndRsp_RoundTrip 测试请求与应答报文的完整封包与解包回环
func TestCROS_ReqAndRsp_RoundTrip(t *testing.T) {
	sno := uint64(8848)
	service := "OrderService"
	op := "CreateOrder"
	payload := []byte("protobuf-wire-encoded-payload-create-order")

	// 1. 请求报文
	reqWire, err := BuildReqFrame(sno, service, op, payload)
	if err != nil {
		t.Fatalf("BuildReqFrame failed: %v", err)
	}

	frame, err := ReadFrame(bytes.NewReader(reqWire))
	if err != nil {
		t.Fatalf("ReadFrame for req failed: %v", err)
	}

	if frame.CommHead.SNO != sno {
		t.Errorf("expected SNO %d, got %d", sno, frame.CommHead.SNO)
	}
	if frame.CommHead.MsgType != MsgTypeReq {
		t.Errorf("expected MsgType %d, got %d", MsgTypeReq, frame.CommHead.MsgType)
	}
	if frame.ReqHead == nil {
		t.Fatal("expected non-nil ReqHead")
	}
	if frame.ReqHead.ServiceName != service {
		t.Errorf("expected service %q, got %q", service, frame.ReqHead.ServiceName)
	}
	if frame.ReqHead.Operation != op {
		t.Errorf("expected op %q, got %q", op, frame.ReqHead.Operation)
	}
	if !bytes.Equal(frame.Payload, payload) {
		t.Errorf("payload mismatch: expected %q, got %q", payload, frame.Payload)
	}

	// 2. 应答报文
	rspWire, err := BuildRspFrame(sno, service, op, 200, payload)
	if err != nil {
		t.Fatalf("BuildRspFrame failed: %v", err)
	}

	rspFrame, err := ReadFrame(bytes.NewReader(rspWire))
	if err != nil {
		t.Fatalf("ReadFrame for rsp failed: %v", err)
	}
	if rspFrame.CommHead.MsgType != MsgTypeRsp {
		t.Errorf("expected MsgType %d, got %d", MsgTypeRsp, rspFrame.CommHead.MsgType)
	}
	if rspFrame.RspHead == nil {
		t.Fatal("expected non-nil RspHead")
	}
	if rspFrame.RspHead.StatusCode != 200 {
		t.Errorf("expected StatusCode 200, got %d", rspFrame.RspHead.StatusCode)
	}
	if rspFrame.RspHead.ServiceName != service {
		t.Errorf("expected service %q, got %q", service, rspFrame.RspHead.ServiceName)
	}
	if !bytes.Equal(rspFrame.Payload, payload) {
		t.Errorf("payload mismatch")
	}
}

// TestCROS_Fragmentation_SingleByteStream 验证在极端单字节流切分下状态机的粘包拆包正确性
func TestCROS_Fragmentation_SingleByteStream(t *testing.T) {
	payload := []byte("simulated-fragmented-protobuf-stream-data")
	wire, err := BuildReqFrame(1001, "AccountSvc", "QueryBalance", payload)
	if err != nil {
		t.Fatalf("BuildReqFrame failed: %v", err)
	}

	reader := &fragReader{data: wire}
	f, err := ReadFrame(reader)
	if err != nil {
		t.Fatalf("ReadFrame with single byte fragReader failed: %v", err)
	}

	if f.CommHead.SNO != 1001 {
		t.Errorf("expected SNO 1001, got %d", f.CommHead.SNO)
	}
	if f.ReqHead.ServiceName != "AccountSvc" || f.ReqHead.Operation != "QueryBalance" {
		t.Errorf("unexpected ReqHead meta: %+v", f.ReqHead)
	}
	if !bytes.Equal(f.Payload, payload) {
		t.Errorf("payload mismatch in fragmentation test")
	}
}

// TestCROS_CoalescedFrames_10Frames 验证连续 10 帧高密度粘包连读
func TestCROS_CoalescedFrames_10Frames(t *testing.T) {
	var coalescedBuf bytes.Buffer
	count := 10

	for i := 1; i <= count; i++ {
		payload := []byte("payload-coalesced-frame")
		wire, err := BuildReqFrame(uint64(i), "TestSvc", "BatchOp", payload)
		if err != nil {
			t.Fatalf("build frame %d failed: %v", i, err)
		}
		coalescedBuf.Write(wire)
	}

	reader := bytes.NewReader(coalescedBuf.Bytes())
	for i := 1; i <= count; i++ {
		f, err := ReadFrame(reader)
		if err != nil {
			t.Fatalf("failed reading frame %d from stream: %v", i, err)
		}
		if f.CommHead.SNO != uint64(i) {
			t.Errorf("frame %d expected SNO %d, got %d", i, i, f.CommHead.SNO)
		}
	}
}

// TestCROS_MagicScan_GarbageRecovery 验证在流起始存在噪声垃圾数据时的魔数滑动窗口恢复
func TestCROS_MagicScan_GarbageRecovery(t *testing.T) {
	garbage := []byte("INVALID_GARBAGE_NOISE_HEADER_DATA_1234567890_TRAILING_BYTES")
	validWire, err := BuildReqFrame(777, "SyncService", "Heartbeat", []byte("ping"))
	if err != nil {
		t.Fatalf("build frame failed: %v", err)
	}

	var stream bytes.Buffer
	stream.Write(garbage)
	stream.Write(validWire)

	f, err := ReadFrame(&stream)
	if err != nil {
		t.Fatalf("ReadFrame with garbage prefix failed: %v", err)
	}

	if f.CommHead.SNO != 777 {
		t.Errorf("expected SNO 777, got %d", f.CommHead.SNO)
	}
	if f.ReqHead.ServiceName != "SyncService" || f.ReqHead.Operation != "Heartbeat" {
		t.Errorf("unexpected meta: %+v", f.ReqHead)
	}
	if string(f.Payload) != "ping" {
		t.Errorf("expected payload 'ping', got %q", string(f.Payload))
	}
}

// TestCROS_RebuildFrameSeqID_InPlace 验证 O(1) 原地序列号自愈重写
func TestCROS_RebuildFrameSeqID_InPlace(t *testing.T) {
	wire, err := BuildReqFrame(100, "Svc", "Op", []byte("test"))
	if err != nil {
		t.Fatalf("BuildReqFrame failed: %v", err)
	}

	newSNO := uint64(99999999)
	if err := RebuildFrameSeqID(wire, newSNO); err != nil {
		t.Fatalf("RebuildFrameSeqID failed: %v", err)
	}

	f, err := ReadFrame(bytes.NewReader(wire))
	if err != nil {
		t.Fatalf("ReadFrame on rebuilt wire failed: %v", err)
	}
	if f.CommHead.SNO != newSNO {
		t.Errorf("expected rebuilt SNO %d, got %d", newSNO, f.CommHead.SNO)
	}
}

// BenchmarkRebuildFrameSeqID 性能压测：验证原地重写零内存分配
func BenchmarkRebuildFrameSeqID(b *testing.B) {
	wire, err := BuildReqFrame(100, "Svc", "Op", []byte("test"))
	if err != nil {
		b.Fatalf("build frame failed: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = RebuildFrameSeqID(wire, uint64(i))
	}
}
