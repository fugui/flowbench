package monitor

import (
	"fmt"
	"strings"
	"time"
)

// SessionStatus 描述手工监控录制会话的状态
type SessionStatus string

const (
	SessionStatusIdle      SessionStatus = "IDLE"      // 待命状态：尚未开始或已重置
	SessionStatusRecording SessionStatus = "RECORDING" // 正在录制：eBPF探针/代理透明拦截中
	SessionStatusStopped   SessionStatus = "STOPPED"   // 录制结束：数据已固化，可分析与单调用重放
)

// SessionStartRequest 包含开启录制会话的入参
type SessionStartRequest struct {
	Title       string            `json:"title"`
	TargetPID   int               `json:"target_pid"`
	ProcessName string            `json:"process_name"`
	IngressPort uint16            `json:"ingress_port"`
	Backends    []BackendEndpoint `json:"backends"`
}

// SubCallDetail 描述一个由 Ingress 触发的下游 Backend 出站子调用
type SubCallDetail struct {
	SubCallID     string              `json:"sub_call_id"`
	TargetBackend string              `json:"target_backend"`
	BackendName   string              `json:"backend_name"`
	OpName        string              `json:"op_name"`
	DurationMs    float64             `json:"duration_ms"`
	EgressReq     CapturedFrameEvent  `json:"egress_req"`
	EgressResp    *CapturedFrameEvent `json:"egress_resp,omitempty"`
}

// SingleReplayResult 描述对单条调用进行重放的结果及 Side-by-Side 字段比对
type SingleReplayResult struct {
	TraceID        string              `json:"trace_id"`
	Passed         bool                `json:"passed"`
	ByteExactMatch bool                `json:"byte_exact_match"`
	ExpectedCRC32  string              `json:"expected_crc32"`
	ActualCRC32    string              `json:"actual_crc32"`
	LatencyMs      float64             `json:"latency_ms"`
	ErrorMessage   string              `json:"error_message,omitempty"`
	FieldDiffs     []FieldDiff         `json:"field_diffs,omitempty"`
	ActualResponse *CapturedFrameEvent `json:"actual_response,omitempty"`
}

// TransactionTrace 描述端到端业务调用事务树
type TransactionTrace struct {
	TraceID     string              `json:"trace_id"`
	RequestID   uint64              `json:"request_id"`
	OpName      string              `json:"op_name"`
	StatusCode  uint32              `json:"status_code"`
	StatusName  string              `json:"status_name"`
	DurationMs  float64             `json:"duration_ms"`
	StartedAt   string              `json:"started_at"`
	IngressReq  CapturedFrameEvent  `json:"ingress_req"`
	IngressResp *CapturedFrameEvent `json:"ingress_resp,omitempty"`
	SubCalls    []SubCallDetail     `json:"sub_calls"`
	HasDiff     bool                `json:"has_diff"`
	LastReplay  *SingleReplayResult `json:"last_replay,omitempty"`
}

// RecordingSession 描述一个完整录制会话
type RecordingSession struct {
	SessionID    string               `json:"session_id"`
	Title        string               `json:"title"`
	Status       SessionStatus        `json:"status"`
	StartedAt    string               `json:"started_at"`
	StoppedAt    string               `json:"stopped_at,omitempty"`
	DurationSec  float64              `json:"duration_sec"`
	TargetPID    int                  `json:"target_pid"`
	ProcessName  string               `json:"process_name"`
	IngressPort  uint16               `json:"ingress_port"`
	Backends     []BackendEndpoint    `json:"backends"`
	TotalFrames  int                  `json:"total_frames"`
	TotalTraces  int                  `json:"total_traces"`
	RawFrames    []CapturedFrameEvent `json:"raw_frames"`
	Traces       []TransactionTrace   `json:"traces"`
	MocksCreated []RecordedMockAsset  `json:"mocks_created,omitempty"`
}

// LinkTelemetrySnapshot 描述拓扑图各连线的实时遥测指标
type LinkTelemetrySnapshot struct {
	Timestamp string                 `json:"timestamp"`
	TotalQPS  float64                `json:"total_qps"`
	TotalKBps float64                `json:"total_kbps"`
	Links     map[string]LinkMetrics `json:"links"`
}

// LinkMetrics 描述单条链路指标
type LinkMetrics struct {
	QPS          float64 `json:"qps"`
	KBps         float64 `json:"kbps"`
	AvgLatencyMs float64 `json:"avg_lat_ms"`
	Active       bool    `json:"active"`
}

// StartSession 启动一个全新的手工录制会话
func (e *Engine) StartSession(req SessionStartRequest) (RecordingSession, error) {
	e.sessionMu.Lock()
	defer e.sessionMu.Unlock()

	sessID := fmt.Sprintf("REC-%s-%d", time.Now().Format("20060102-150405"), req.TargetPID)
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = fmt.Sprintf("手工录制会话 (%s)", time.Now().Format("15:04:05"))
	}
	procName := strings.TrimSpace(req.ProcessName)
	if procName == "" {
		procName = fmt.Sprintf("pid-%d", req.TargetPID)
	}
	ingPort := req.IngressPort
	if ingPort == 0 {
		ingPort = e.virtualSrvPort
	}

	sess := RecordingSession{
		SessionID:    sessID,
		Title:        title,
		Status:       SessionStatusRecording,
		StartedAt:    time.Now().Format("15:04:05"),
		TargetPID:    req.TargetPID,
		ProcessName:  procName,
		IngressPort:  ingPort,
		Backends:     req.Backends,
		TotalFrames:  0,
		TotalTraces:  0,
		RawFrames:    make([]CapturedFrameEvent, 0),
		Traces:       make([]TransactionTrace, 0),
		MocksCreated: make([]RecordedMockAsset, 0),
	}

	e.currentSession = sess
	e.activeTraces = make(map[uint64]*TransactionTrace)
	e.sessionStartTime = time.Now()

	// 挂载进程和后端路由规则
	_, _ = e.AttachProcess(req.TargetPID, procName, ingPort, "127.0.0.1", e.virtualBackPort, req.Backends...)

	return sess, nil
}

// StopSession 停止当前录制会话并固化资产
func (e *Engine) StopSession() (RecordingSession, error) {
	e.sessionMu.Lock()
	defer e.sessionMu.Unlock()

	if e.currentSession.Status != SessionStatusRecording {
		return e.currentSession, nil
	}

	now := time.Now()
	dur := now.Sub(e.sessionStartTime).Seconds()
	e.currentSession.Status = SessionStatusStopped
	e.currentSession.StoppedAt = now.Format("15:04:05")
	e.currentSession.DurationSec = dur
	e.currentSession.TotalFrames = len(e.currentSession.RawFrames)
	e.currentSession.TotalTraces = len(e.currentSession.Traces)

	// 自动从会话出站子调用中生成出站 Mock 规则库
	newMocks := make([]RecordedMockAsset, 0)
	for _, tr := range e.currentSession.Traces {
		for _, sc := range tr.SubCalls {
			if sc.EgressResp != nil {
				mockID := fmt.Sprintf("session-mock-%s", sc.SubCallID)
				newMocks = append(newMocks, RecordedMockAsset{
					MockID:        mockID,
					SemanticKey:   fmt.Sprintf("target=%s|op=%s", sc.TargetBackend, sc.OpName),
					OpName:        sc.OpName,
					TargetBackend: sc.TargetBackend,
					BackendName:   sc.BackendName,
					OriginalReq:   sc.EgressReq,
					OriginalResp:  *sc.EgressResp,
					HitCount:      0,
				})
			}
		}
	}
	e.currentSession.MocksCreated = newMocks

	return e.currentSession, nil
}

// GetCurrentSession 获取当前录制会话的状态及汇总
func (e *Engine) GetCurrentSession() RecordingSession {
	e.sessionMu.RLock()
	defer e.sessionMu.RUnlock()

	// 复制一份副本避免并发竞态
	res := e.currentSession
	res.RawFrames = append([]CapturedFrameEvent(nil), e.currentSession.RawFrames...)
	res.Traces = append([]TransactionTrace(nil), e.currentSession.Traces...)
	return res
}

// ListTransactionTraces 列出当前会话聚合的端到端调用事务
func (e *Engine) ListTransactionTraces(filter string) []TransactionTrace {
	e.sessionMu.RLock()
	defer e.sessionMu.RUnlock()

	f := strings.TrimSpace(strings.ToLower(filter))
	if f == "" {
		res := make([]TransactionTrace, len(e.currentSession.Traces))
		copy(res, e.currentSession.Traces)
		return res
	}

	var matched []TransactionTrace
	for _, tr := range e.currentSession.Traces {
		if strings.Contains(strings.ToLower(tr.OpName), f) ||
			strings.Contains(strings.ToLower(tr.StatusName), f) ||
			strings.Contains(fmt.Sprintf("%d", tr.RequestID), f) {
			matched = append(matched, tr)
		}
	}
	return matched
}
