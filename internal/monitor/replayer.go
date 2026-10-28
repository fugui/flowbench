package monitor

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"time"

	"github.com/fugui/flowbench/internal/codec"
	"github.com/fugui/flowbench/internal/protocol/schema"
	"github.com/fugui/flowbench/internal/transport"
)

// SingleReplayRequest 包含单调用重放请求参数
type SingleReplayRequest struct {
	TraceID             string         `json:"trace_id"`
	OverridePayloadJSON map[string]any `json:"override_payload_json,omitempty"`
	MockEgress          bool           `json:"mock_egress"`
}

// ReplaySingleTrace 驱动被测 SUT Server 对某条特定调用发起原地重放，并通过 eBPF/离线 Mock 进行自愈验证与字段级 Diff 比对
func (e *Engine) ReplaySingleTrace(ctx context.Context, sutAddr string, req SingleReplayRequest) (*SingleReplayResult, error) {
	if req.TraceID == "" {
		return nil, errors.New("trace_id is required")
	}

	e.sessionMu.RLock()
	var targetTrace *TransactionTrace
	for i := range e.currentSession.Traces {
		if e.currentSession.Traces[i].TraceID == req.TraceID {
			tr := e.currentSession.Traces[i]
			targetTrace = &tr
			break
		}
	}
	e.sessionMu.RUnlock()

	if targetTrace == nil {
		return nil, fmt.Errorf("transaction trace not found: %s", req.TraceID)
	}
	if targetTrace.IngressResp == nil {
		return nil, fmt.Errorf("trace %s has no recorded ingress response", req.TraceID)
	}

	// 1. 还原基准请求帧与基准响应帧
	reqBytes, err := hex.DecodeString(targetTrace.IngressReq.RawWireHex)
	if err != nil || len(reqBytes) == 0 {
		return nil, fmt.Errorf("decode ingress request wire hex failed: %w", err)
	}
	reqFrame, err := codec.ReadFrame(bytes.NewReader(reqBytes))
	if err != nil {
		return nil, fmt.Errorf("parse ingress request frame failed: %w", err)
	}

	expRespBytes, err := hex.DecodeString(targetTrace.IngressResp.RawWireHex)
	if err != nil || len(expRespBytes) == 0 {
		return nil, fmt.Errorf("decode ingress response wire hex failed: %w", err)
	}
	expRespFrame, err := codec.ReadFrame(bytes.NewReader(expRespBytes))
	if err != nil {
		return nil, fmt.Errorf("parse expected ingress response frame failed: %w", err)
	}

	// 2. 若用户提供了 OverridePayloadJSON，动态重编码 Payload
	if len(req.OverridePayloadJSON) > 0 {
		newPayload, encodeErr := e.schemaReg.EncodeJSONToPayload(uint32(reqFrame.Header.OpCode), schema.DirectionReq, req.OverridePayloadJSON)
		if encodeErr == nil && len(newPayload) > 0 {
			reqFrame.Payload = newPayload
			reqFrame.Header.PayloadLen = uint32(len(newPayload))
			reqFrame.Header.CRC32 = crc32.ChecksumIEEE(newPayload)
		}
	}

	// 3. 确定目标 SUT Server 地址
	if sutAddr == "" {
		sutAddr = fmt.Sprintf("127.0.0.1:%d", e.attachedTarget.IngressOrigPort)
		if e.attachedTarget.IngressOrigPort == 0 {
			sutAddr = fmt.Sprintf("127.0.0.1:%d", e.virtualSrvPort)
		}
	}

	// 4. 发起调用重放
	driverCli := transport.NewClient(transport.ClientConfig{
		Name:       "single-call-replayer",
		TargetAddr: sutAddr,
		ConnMode:   transport.ConnModeShort,
		Timeout:    3 * time.Second,
	})
	defer func() {
		_ = driverCli.Close()
	}()

	callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	t0 := time.Now()
	actRespFrame, callErr := driverCli.Call(callCtx, reqFrame)
	latMs := float64(time.Since(t0).Microseconds()) / 1000.0

	expCRCHex := fmt.Sprintf("0x%08X", expRespFrame.Header.CRC32)

	if callErr != nil {
		res := &SingleReplayResult{
			TraceID:        req.TraceID,
			Passed:         false,
			ByteExactMatch: false,
			ExpectedCRC32:  expCRCHex,
			LatencyMs:      latMs,
			ErrorMessage:   callErr.Error(),
		}
		e.updateTraceReplayResult(req.TraceID, res)
		return res, nil
	}

	// 5. 校验结果并计算字段级 Diff
	actRespBytes, _ := codec.EncodeFrameBytes(actRespFrame)
	actCRCHex := fmt.Sprintf("0x%08X", actRespFrame.Header.CRC32)
	byteEqual := bytes.Equal(expRespBytes, actRespBytes)

	var fieldDiffs []FieldDiff
	if !byteEqual {
		fieldDiffs = ComputeFieldDiffs(expRespFrame, actRespFrame)
	}

	actEv := InspectFrameWithRegistry(actRespFrame, e.schemaReg, "INGRESS_RESP", "REPLAY", reqFrame.Header.RequestID, latMs)

	res := &SingleReplayResult{
		TraceID:        req.TraceID,
		Passed:         byteEqual,
		ByteExactMatch: byteEqual,
		ExpectedCRC32:  expCRCHex,
		ActualCRC32:    actCRCHex,
		LatencyMs:      latMs,
		FieldDiffs:     fieldDiffs,
		ActualResponse: &actEv,
	}

	e.updateTraceReplayResult(req.TraceID, res)
	return res, nil
}

// updateTraceReplayResult 将单次重放结果记录回事务对象中
func (e *Engine) updateTraceReplayResult(traceID string, res *SingleReplayResult) {
	e.sessionMu.Lock()
	defer e.sessionMu.Unlock()

	for i := range e.currentSession.Traces {
		if e.currentSession.Traces[i].TraceID == traceID {
			e.currentSession.Traces[i].LastReplay = res
			e.currentSession.Traces[i].HasDiff = !res.Passed
			break
		}
	}
}
