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

	"github.com/fugui/flowbench/internal/ebpf"
)

//go:embed web/*
var embeddedWebFS embed.FS

// DashboardStatusResponse 表示 /api/status 返回的全局控制台状态快照。
type DashboardStatusResponse struct {
	EBPFStatus    ebpf.RuntimeStatus   `json:"ebpf_status"`
	RecentFrames  []CapturedFrameEvent `json:"recent_frames"`
	RecordedCases []RecordedCaseAsset  `json:"recorded_cases"`
	RecordedMocks []RecordedMockAsset  `json:"recorded_mocks"`
	LastReport    *ExperimentReport    `json:"last_report,omitempty"`
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
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; object-src 'none';")
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
	ws.engine.mu.RUnlock()

	var ebpfSt ebpf.RuntimeStatus
	if ws.engine.interceptor != nil {
		ebpfSt = ws.engine.interceptor.Status()
	}

	resp := DashboardStatusResponse{
		EBPFStatus:    ebpfSt,
		RecentFrames:  frames,
		RecordedCases: cases,
		RecordedMocks: mocks,
		LastReport:    lastRep,
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(resp)
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
	for {
		select {
		case <-ctx.Done():
			return
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
