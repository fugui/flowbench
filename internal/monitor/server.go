package monitor

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"time"

	"github.com/fugui/flowbench/internal/codec"
	"github.com/fugui/flowbench/internal/ebpf"
	"github.com/fugui/flowbench/internal/protocol/schema"
)

//go:embed web/*
var embeddedWebFS embed.FS

// DashboardStatusResponse 表示 /api/status 返回的全局通用平台状态快照。
type DashboardStatusResponse struct {
	EBPFStatus     ebpf.RuntimeStatus    `json:"ebpf_status"`
	AttachedTarget AttachedTargetState   `json:"attached_target"`
	FramingSpec    codec.FramingSpec     `json:"framing_spec"`
	FramingPresets []codec.FramingSpec   `json:"framing_presets"`
	SchemaStatus   schema.RegistryStatus `json:"schema_status"`
	RecentFrames   []CapturedFrameEvent  `json:"recent_frames"`
	RecordedCases  []RecordedCaseAsset   `json:"recorded_cases"`
	RecordedMocks  []RecordedMockAsset   `json:"recorded_mocks"`
	LastReport     *ExperimentReport     `json:"last_report,omitempty"`
	CurrentSession RecordingSession      `json:"current_session"`
	Telemetry      LinkTelemetrySnapshot `json:"telemetry"`
}

// WebServer 封装内嵌安全 Web 控制台的 HTTP 与 SSE 服务。
type WebServer struct {
	engine     *Engine
	listenAddr string
	httpSrv    *http.Server
	ln         net.Listener
}

// NewWebServer 创建绑定在本地环回地址的监控控制台 Web 服务。
func NewWebServer(listenAddr string, engine *Engine) *WebServer {
	if listenAddr == "" {
		listenAddr = "127.0.0.1:9300"
	}
	return &WebServer{
		engine:     engine,
		listenAddr: listenAddr,
	}
}

func applySecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; object-src 'none'; base-uri 'self';")
}

// Start 启动 Web 监控控制台并返回实际监听地址。
func (ws *WebServer) Start() (string, error) {
	subFS, err := fs.Sub(embeddedWebFS, "web")
	if err != nil {
		return "", err
	}
	fileServer := http.FileServer(http.FS(subFS))

	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", ws.handleStatus)
	mux.HandleFunc("/api/events", ws.handleSSE)
	mux.HandleFunc("/api/run", ws.handleRunExperiment)
	mux.HandleFunc("/api/processes", ws.handleProcesses)
	mux.HandleFunc("/api/attach", ws.handleAttachProcess)
	mux.HandleFunc("/api/detach", ws.handleDetachProcess)
	mux.HandleFunc("/api/backends", ws.handleBackends)
	mux.HandleFunc("/api/backends/add", ws.handleAddBackend)
	mux.HandleFunc("/api/backends/remove", ws.handleRemoveBackend)
	mux.HandleFunc("/api/protocol/spec", ws.handleProtocolSpec)
	mux.HandleFunc("/api/protocol/schema", ws.handleProtocolSchema)
	mux.HandleFunc("/api/mocks/update", ws.handleUpdateMock)
	mux.HandleFunc("/api/session/start", ws.handleSessionStart)
	mux.HandleFunc("/api/session/stop", ws.handleSessionStop)
	mux.HandleFunc("/api/session/current", ws.handleSessionCurrent)
	mux.HandleFunc("/api/session/traces", ws.handleSessionTraces)
	mux.HandleFunc("/api/replay/single", ws.handleReplaySingle)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		applySecurityHeaders(w)
		fileServer.ServeHTTP(w, r)
	})

	ln, err := net.Listen("tcp", ws.listenAddr)
	if err != nil {
		return "", err
	}
	ws.ln = ln
	ws.httpSrv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		_ = ws.httpSrv.Serve(ln)
	}()

	return ln.Addr().String(), nil
}

// Shutdown 优雅停止 Web 控制台。
func (ws *WebServer) Shutdown(ctx context.Context) error {
	if ws.httpSrv != nil {
		return ws.httpSrv.Shutdown(ctx)
	}
	return nil
}

func (ws *WebServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	applySecurityHeaders(w)
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ws.engine.mu.RLock()
	frames := make([]CapturedFrameEvent, len(ws.engine.recentFrames))
	copy(frames, ws.engine.recentFrames)
	cases := make([]RecordedCaseAsset, len(ws.engine.ingressCases))
	copy(cases, ws.engine.ingressCases)
	mocks := make([]RecordedMockAsset, 0, len(ws.engine.egressMockOrder))
	for _, k := range ws.engine.egressMockOrder {
		if m, ok := ws.engine.egressMocks[k]; ok {
			mocks = append(mocks, *m)
		}
	}
	lastRep := ws.engine.lastReport
	attached := ws.engine.attachedTarget
	framingSpec := ws.engine.framingSpec
	ws.engine.mu.RUnlock()

	var ebpfSt ebpf.RuntimeStatus
	if ws.engine.interceptor != nil {
		ebpfSt = ws.engine.interceptor.Status()
	}

	resp := DashboardStatusResponse{
		EBPFStatus:     ebpfSt,
		AttachedTarget: attached,
		FramingSpec:    framingSpec,
		FramingPresets: codec.ListPresets(),
		SchemaStatus:   ws.engine.GetSchemaStatus(),
		RecentFrames:   frames,
		RecordedCases:  cases,
		RecordedMocks:  mocks,
		LastReport:     lastRep,
		CurrentSession: ws.engine.GetCurrentSession(),
		Telemetry:      ws.engine.GetTelemetrySnapshot(),
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(resp)
}

func (ws *WebServer) handleProcesses(w http.ResponseWriter, r *http.Request) {
	applySecurityHeaders(w)
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	query := r.URL.Query().Get("q")
	if len(query) > 64 {
		query = query[:64]
	}
	procs, err := ScanOSProcesses(query)
	if err != nil {
		http.Error(w, "scan processes failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"processes": procs,
	})
}

func (ws *WebServer) handleAttachProcess(w http.ResponseWriter, r *http.Request) {
	applySecurityHeaders(w)
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		TargetPID   int               `json:"target_pid"`
		ProcessName string            `json:"process_name"`
		IngressPort uint16            `json:"ingress_port"`
		EgressIP    string            `json:"egress_ip"`
		EgressPort  uint16            `json:"egress_port"`
		Backends    []BackendEndpoint `json:"backends"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	st, err := ws.engine.AttachProcess(req.TargetPID, req.ProcessName, req.IngressPort, req.EgressIP, req.EgressPort, req.Backends...)
	if err != nil {
		http.Error(w, "attach process failed", http.StatusBadRequest)
		return
	}
	var ebpfSt any
	if ws.engine.interceptor != nil {
		ebpfSt = ws.engine.interceptor.Status()
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"attached_target": st,
		"ebpf_status":     ebpfSt,
	})
}

func (ws *WebServer) handleDetachProcess(w http.ResponseWriter, r *http.Request) {
	applySecurityHeaders(w)
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	st := ws.engine.DetachProcess()
	var ebpfSt any
	if ws.engine.interceptor != nil {
		ebpfSt = ws.engine.interceptor.Status()
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"attached_target": st,
		"ebpf_status":     ebpfSt,
	})
}

func (ws *WebServer) handleBackends(w http.ResponseWriter, r *http.Request) {
	applySecurityHeaders(w)
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	backends := ws.engine.ListBackendEndpoints()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"backends": backends,
	})
}

func (ws *WebServer) handleAddBackend(w http.ResponseWriter, r *http.Request) {
	applySecurityHeaders(w)
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req BackendEndpoint
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	added, err := ws.engine.AddBackendEndpoint(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"backend": added,
	})
}

func (ws *WebServer) handleRemoveBackend(w http.ResponseWriter, r *http.Request) {
	applySecurityHeaders(w)
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Host string `json:"host"`
		Port uint16 `json:"port"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := ws.engine.RemoveBackendEndpoint(req.Host, req.Port); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok": true,
	})
}

func (ws *WebServer) handleProtocolSpec(w http.ResponseWriter, r *http.Request) {
	applySecurityHeaders(w)
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		PresetID   string             `json:"preset_id"`
		CustomSpec *codec.FramingSpec `json:"custom_spec"`
		Simulate   bool               `json:"simulate"`
		SimOpCode  uint32             `json:"sim_op_code"`
		SimSeqID   uint64             `json:"sim_seq_id"`
		SimJSON    map[string]any     `json:"sim_json"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	var activeSpec codec.FramingSpec
	var err error
	if req.PresetID != "" {
		activeSpec, err = ws.engine.SelectPresetFramingSpec(req.PresetID)
	} else if req.CustomSpec != nil {
		activeSpec, err = ws.engine.SetFramingSpec(*req.CustomSpec)
	} else {
		activeSpec = ws.engine.GetFramingSpec()
	}
	if err != nil {
		http.Error(w, "invalid framing specification", http.StatusBadRequest)
		return
	}

	var simFrame *CapturedFrameEvent
	if req.Simulate {
		op := req.SimOpCode
		if op == 0 {
			op = 0x11
		}
		seq := req.SimSeqID
		if seq == 0 {
			seq = 90001
		}
		ev, simErr := ws.engine.SimulateDissectFrame(op, seq, req.SimJSON)
		if simErr == nil {
			simFrame = &ev
		}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"framing_spec":    activeSpec,
		"simulated_frame": simFrame,
	})
}

func (ws *WebServer) handleProtocolSchema(w http.ResponseWriter, r *http.Request) {
	applySecurityHeaders(w)
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Action      string                      `json:"action"` // "import" | "clear" | "restore_default"
		FileName    string                      `json:"file_name"`
		ProtoSource string                      `json:"proto_source"`
		Bindings    []schema.OpCodeRouteBinding `json:"bindings"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512*1024)).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	var status schema.RegistryStatus
	var rehydrated int
	var err error

	switch req.Action {
	case "clear":
		status, rehydrated = ws.engine.ClearProtoSchema()
	case "restore_default":
		status, rehydrated, err = ws.engine.ImportProtoSchema("messages.proto", schema.DefaultSandboxProtoSource, schema.DefaultSandboxBindings())
	default:
		if req.ProtoSource == "" {
			http.Error(w, "proto_source is required", http.StatusBadRequest)
			return
		}
		status, rehydrated, err = ws.engine.ImportProtoSchema(req.FileName, req.ProtoSource, req.Bindings)
	}
	if err != nil {
		http.Error(w, "parse proto schema failed", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"schema_status":     status,
		"rehydrated_frames": rehydrated,
	})
}

func (ws *WebServer) handleUpdateMock(w http.ResponseWriter, r *http.Request) {
	applySecurityHeaders(w)
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		MockID       string         `json:"mock_id"`
		ResponseJSON map[string]any `json:"response_json"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128*1024)).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.MockID == "" || len(req.ResponseJSON) == 0 {
		http.Error(w, "mock_id and response_json are required", http.StatusBadRequest)
		return
	}
	updated, err := ws.engine.UpdateMockResponseJSON(req.MockID, req.ResponseJSON)
	if err != nil {
		http.Error(w, "update mock failed", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"updated_mock": updated,
	})
}

func (ws *WebServer) handleSSE(w http.ResponseWriter, r *http.Request) {
	applySecurityHeaders(w)
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := ws.engine.Subscribe()
	defer ws.engine.Unsubscribe(ch)

	// 立即刷新响应头，使浏览器 EventSource 触发 onopen
	_, _ = fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	ctx := r.Context()
	telemetryTicker := time.NewTicker(1 * time.Second)
	defer telemetryTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-telemetryTicker.C:
			snap := ws.engine.GetTelemetrySnapshot()
			payload := map[string]any{
				"event_type": "telemetry",
				"telemetry":  snap,
			}
			b, err := json.Marshal(payload)
			if err == nil {
				_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
				flusher.Flush()
			}
		case ev, open := <-ch:
			if !open {
				return
			}
			b, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		}
	}
}

func (ws *WebServer) handleRunExperiment(w http.ResponseWriter, r *http.Request) {
	applySecurityHeaders(w)
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, "invalid request payload", http.StatusBadRequest)
		return
	}

	// 严格白名单校验
	switch req.Action {
	case "record", "replay", "mutation", "non_deterministic":
	default:
		http.Error(w, "unsupported action", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	report, err := ws.engine.RunExperiment(ctx, req.Action)
	if err != nil {
		http.Error(w, "experiment execution failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(report)
}

func (ws *WebServer) handleSessionStart(w http.ResponseWriter, r *http.Request) {
	applySecurityHeaders(w)
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req SessionStartRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	sess, err := ws.engine.StartSession(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var ebpfSt any
	if ws.engine.interceptor != nil {
		ebpfSt = ws.engine.interceptor.Status()
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"session":     sess,
		"ebpf_status": ebpfSt,
	})
}

func (ws *WebServer) handleSessionStop(w http.ResponseWriter, r *http.Request) {
	applySecurityHeaders(w)
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sess, err := ws.engine.StopSession()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"session": sess,
	})
}

func (ws *WebServer) handleSessionCurrent(w http.ResponseWriter, r *http.Request) {
	applySecurityHeaders(w)
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sess := ws.engine.GetCurrentSession()
	telemetry := ws.engine.GetTelemetrySnapshot()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"session":   sess,
		"telemetry": telemetry,
	})
}

func (ws *WebServer) handleSessionTraces(w http.ResponseWriter, r *http.Request) {
	applySecurityHeaders(w)
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query().Get("q")
	traces := ws.engine.ListTransactionTraces(q)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"traces": traces,
	})
}

func (ws *WebServer) handleReplaySingle(w http.ResponseWriter, r *http.Request) {
	applySecurityHeaders(w)
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req SingleReplayRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	res, err := ws.engine.ReplaySingleTrace(ctx, "", req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(res)
}
