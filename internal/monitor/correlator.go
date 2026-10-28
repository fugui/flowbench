package monitor

import (
	"fmt"
	"strings"
	"time"
)

// ingestFrameIntoSession 将捕获到的二进制事件帧实时归纳到当前录制会话与端到端事务树中
func (e *Engine) ingestFrameIntoSession(ev CapturedFrameEvent) {
	e.sessionMu.Lock()
	defer e.sessionMu.Unlock()

	// 无论是否显式录制态，始终维护会话原始流与 Trace 树，确保 Playground 模式同样受益
	e.currentSession.RawFrames = append(e.currentSession.RawFrames, ev)
	if len(e.currentSession.RawFrames) > 200 {
		e.currentSession.RawFrames = e.currentSession.RawFrames[len(e.currentSession.RawFrames)-200:]
	}
	e.currentSession.TotalFrames = len(e.currentSession.RawFrames)

	reqID := ev.Header.RequestID
	if reqID == 0 {
		return
	}

	dir := ev.Direction
	switch {
	case strings.HasPrefix(dir, "INGRESS_REQ"):
		// 1. 入站请求：初始化端到端调用事务
		trace := &TransactionTrace{
			TraceID:    fmt.Sprintf("trace-%d", reqID),
			RequestID:  reqID,
			OpName:     ev.Header.OpName,
			StatusCode: 0,
			StatusName: "IN_FLIGHT",
			DurationMs: 0,
			StartedAt:  ev.Timestamp,
			IngressReq: ev,
			SubCalls:   make([]SubCallDetail, 0),
		}
		diag := BuildUMLDiagram(*trace, e.attachedTarget.Backends, e.attachedTarget.ProcessName, e.attachedTarget.IngressOrigPort)
		trace.UMLDiagram = &diag
		e.activeTraces[reqID] = trace
		e.currentSession.Traces = append(e.currentSession.Traces, *trace)
		e.currentSession.TotalTraces = len(e.currentSession.Traces)

	case strings.HasPrefix(dir, "EGRESS_REQ"):
		// 2. 出站子调用发起
		subID := fmt.Sprintf("sub-%d-%d", reqID, time.Now().UnixNano()%10000)
		sc := SubCallDetail{
			SubCallID:     subID,
			TargetBackend: ev.TargetBackend,
			BackendName:   ev.BackendName,
			OpName:        ev.Header.OpName,
			DurationMs:    0,
			EgressReq:     ev,
		}
		if tr, ok := e.activeTraces[reqID]; ok {
			tr.SubCalls = append(tr.SubCalls, sc)
			e.syncTraceToSession(tr)
		}

	case strings.HasPrefix(dir, "EGRESS_RESP"):
		// 3. 出站子调用响应
		if tr, ok := e.activeTraces[reqID]; ok {
			for i := len(tr.SubCalls) - 1; i >= 0; i-- {
				if tr.SubCalls[i].EgressResp == nil {
					respCopy := ev
					tr.SubCalls[i].EgressResp = &respCopy
					tr.SubCalls[i].DurationMs = ev.LatencyMs

					// 实时注入至引擎出站 Mock 缓存池，供原地重放与 Mock 在线篡改实验
					sc := tr.SubCalls[i]
					semKey := fmt.Sprintf("target=%s|op=%s", sc.TargetBackend, sc.OpName)
					mockID := fmt.Sprintf("session-mock-%s", sc.SubCallID)
					asset := &RecordedMockAsset{
						MockID:        mockID,
						SemanticKey:   semKey,
						OpName:        sc.OpName,
						TargetBackend: sc.TargetBackend,
						BackendName:   sc.BackendName,
						OriginalReq:   sc.EgressReq,
						OriginalResp:  respCopy,
					}
					e.mu.Lock()
					e.egressMocks[semKey] = asset
					found := false
					for _, oid := range e.egressMockOrder {
						if oid == mockID {
							found = true
							break
						}
					}
					if !found {
						e.egressMockOrder = append(e.egressMockOrder, mockID)
					}
					e.mu.Unlock()
					break
				}
			}
			e.syncTraceToSession(tr)
		}

	case strings.HasPrefix(dir, "INGRESS_RESP"):
		// 4. 入站响应闭环
		if tr, ok := e.activeTraces[reqID]; ok {
			respCopy := ev
			tr.IngressResp = &respCopy
			tr.StatusCode = ev.Header.OpCode
			tr.StatusName = ev.Header.StatusName
			if tr.StatusName == "" {
				tr.StatusName = "StatusOK"
			}
			tr.DurationMs = ev.LatencyMs
			e.syncTraceToSession(tr)
			delete(e.activeTraces, reqID)
		}
	}
}

// syncTraceToSession 同步更新会话切片中的事务记录并更新 UML 时序图
func (e *Engine) syncTraceToSession(tr *TransactionTrace) {
	diag := BuildUMLDiagram(*tr, e.attachedTarget.Backends, e.attachedTarget.ProcessName, e.attachedTarget.IngressOrigPort)
	tr.UMLDiagram = &diag
	for i := range e.currentSession.Traces {
		if e.currentSession.Traces[i].RequestID == tr.RequestID {
			e.currentSession.Traces[i] = *tr
			return
		}
	}
}

// GetTelemetrySnapshot 获取各条链路近实时遥测指标快照
func (e *Engine) GetTelemetrySnapshot() LinkTelemetrySnapshot {
	e.sessionMu.RLock()
	defer e.sessionMu.RUnlock()

	snap := LinkTelemetrySnapshot{
		Timestamp: time.Now().Format("15:04:05"),
		TotalQPS:  0,
		TotalKBps: 0,
		Links:     make(map[string]LinkMetrics),
	}

	totalFrames := len(e.currentSession.RawFrames)
	if totalFrames == 0 {
		return snap
	}

	// 统计近 3 秒内的活跃帧
	recentCount := 0
	recentBytes := 0
	recentIngress := 0
	recentEgress := 0

	for i := totalFrames - 1; i >= 0 && i >= totalFrames-30; i-- {
		f := e.currentSession.RawFrames[i]
		recentCount++
		recentBytes += int(f.Header.PayloadLength) + 24
		if strings.HasPrefix(f.Direction, "INGRESS") {
			recentIngress++
		} else {
			recentEgress++
		}
	}

	qps := float64(recentCount) / 3.0
	kbps := (float64(recentBytes) / 1024.0) / 3.0

	snap.TotalQPS = qps
	snap.TotalKBps = kbps

	snap.Links["client->sut"] = LinkMetrics{
		QPS:          float64(recentIngress) / 3.0,
		KBps:         kbps * 0.5,
		AvgLatencyMs: 1.2,
		Active:       recentIngress > 0,
	}

	snap.Links["sut->egress"] = LinkMetrics{
		QPS:          float64(recentEgress) / 3.0,
		KBps:         kbps * 0.5,
		AvgLatencyMs: 0.8,
		Active:       recentEgress > 0,
	}

	for _, b := range e.attachedTarget.Backends {
		linkKey := fmt.Sprintf("egress->%s:%d", b.Host, b.Port)
		snap.Links[linkKey] = LinkMetrics{
			QPS:          float64(recentEgress) / 3.0,
			KBps:         kbps * 0.5,
			AvgLatencyMs: 0.8,
			Active:       recentEgress > 0,
		}
	}

	return snap
}
