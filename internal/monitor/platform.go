package monitor

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fugui/flowbench/internal/codec"
	"github.com/fugui/flowbench/internal/protocol/schema"
)

// BackendEndpoint 描述一个注册的下游 Backend 服务依赖
type BackendEndpoint struct {
	Name      string `json:"name"`
	Host      string `json:"host"`
	Port      uint16 `json:"port"`
	ProxyPort uint16 `json:"proxy_port,omitempty"`
	Active    bool   `json:"active"`
}

// AttachedTargetState 描述当前动态挂载监听的用户业务进程与网络拦截目标
type AttachedTargetState struct {
	Attached        bool              `json:"attached"`
	TargetPID       int               `json:"target_pid"`
	ProcessName     string            `json:"process_name"`
	IngressOrigPort uint16            `json:"ingress_orig_port"`
	EgressOrigIP    string            `json:"egress_orig_ip"`
	EgressOrigPort  uint16            `json:"egress_orig_port"`
	Backends        []BackendEndpoint `json:"backends,omitempty"`
	AttachedAt      string            `json:"attached_at,omitempty"`
}

// AttachProcess 动态选定并挂载目标业务进程 (TargetPID) 与入站/出站依赖端口 (支持多 Backend)
func (e *Engine) AttachProcess(targetPID int, procName string, ingressPort uint16, egressIP string, egressPort uint16, backends ...BackendEndpoint) (AttachedTargetState, error) {
	if targetPID < 0 {
		return AttachedTargetState{}, errors.New("invalid target_pid")
	}
	cleanedIP := strings.TrimSpace(egressIP)
	if cleanedIP == "" {
		cleanedIP = "127.0.0.1"
	}
	if strings.TrimSpace(procName) == "" {
		procName = fmt.Sprintf("pid-%d", targetPID)
	}
	if ingressPort == 0 {
		ingressPort = e.virtualSrvPort
	}
	if egressPort == 0 {
		egressPort = e.virtualBackPort
	}

	finalBackends := make([]BackendEndpoint, 0)
	if len(backends) > 0 {
		for _, b := range backends {
			h := strings.TrimSpace(b.Host)
			if h == "" {
				h = "127.0.0.1"
			}
			name := strings.TrimSpace(b.Name)
			if name == "" {
				name = fmt.Sprintf("backend-%s-%d", h, b.Port)
			}
			finalBackends = append(finalBackends, BackendEndpoint{
				Name:      name,
				Host:      h,
				Port:      b.Port,
				ProxyPort: b.ProxyPort,
				Active:    true,
			})
		}
	} else {
		finalBackends = append(finalBackends, BackendEndpoint{
			Name:   "default-backend",
			Host:   cleanedIP,
			Port:   egressPort,
			Active: true,
		})
	}

	state := AttachedTargetState{
		Attached:        true,
		TargetPID:       targetPID,
		ProcessName:     procName,
		IngressOrigPort: ingressPort,
		EgressOrigIP:    cleanedIP,
		EgressOrigPort:  egressPort,
		Backends:        finalBackends,
		AttachedAt:      time.Now().Format("15:04:05"),
	}

	e.mu.Lock()
	e.attachedTarget = state
	e.mu.Unlock()

	if e.interceptor != nil {
		_ = e.interceptor.UpdateTargetProcess(
			targetPID,
			ingressPort,
			cleanedIP,
			egressPort,
			false,
			false,
			ingressPort,
			egressPort,
		)
		for _, b := range finalBackends {
			if b.ProxyPort > 0 {
				_ = e.interceptor.AddEgressTarget(b.Host, b.Port, b.ProxyPort)
			}
		}
	}
	return state, nil
}

// AddBackendEndpoint 动态向当前附加目标追加一个下游 Backend
func (e *Engine) AddBackendEndpoint(ep BackendEndpoint) (BackendEndpoint, error) {
	if ep.Port == 0 {
		return BackendEndpoint{}, errors.New("invalid backend port: 0")
	}
	h := strings.TrimSpace(ep.Host)
	if h == "" {
		h = "127.0.0.1"
	}
	ep.Host = h
	if strings.TrimSpace(ep.Name) == "" {
		ep.Name = fmt.Sprintf("backend-%s-%d", h, ep.Port)
	}
	ep.Active = true

	e.mu.Lock()
	// 去重检查
	found := false
	for i, b := range e.attachedTarget.Backends {
		if b.Host == ep.Host && b.Port == ep.Port {
			e.attachedTarget.Backends[i] = ep
			found = true
			break
		}
	}
	if !found {
		e.attachedTarget.Backends = append(e.attachedTarget.Backends, ep)
	}
	e.mu.Unlock()

	if e.interceptor != nil && ep.ProxyPort > 0 {
		_ = e.interceptor.AddEgressTarget(ep.Host, ep.Port, ep.ProxyPort)
	}
	return ep, nil
}

// RemoveBackendEndpoint 动态注销指定下游 Backend
func (e *Engine) RemoveBackendEndpoint(host string, port uint16) error {
	h := strings.TrimSpace(host)
	if h == "" {
		h = "127.0.0.1"
	}

	e.mu.Lock()
	filtered := make([]BackendEndpoint, 0, len(e.attachedTarget.Backends))
	for _, b := range e.attachedTarget.Backends {
		if b.Host == h && b.Port == port {
			continue
		}
		filtered = append(filtered, b)
	}
	e.attachedTarget.Backends = filtered
	e.mu.Unlock()

	if e.interceptor != nil {
		_ = e.interceptor.RemoveEgressTarget(h, port)
	}
	return nil
}

// ListBackendEndpoints 返回当前注册的所有下游依赖
func (e *Engine) ListBackendEndpoints() []BackendEndpoint {
	e.mu.RLock()
	defer e.mu.RUnlock()
	res := make([]BackendEndpoint, len(e.attachedTarget.Backends))
	copy(res, e.attachedTarget.Backends)
	return res
}

// DetachProcess 卸载针对特定用户进程的探针过滤规则，恢复默认沙箱就绪状态
func (e *Engine) DetachProcess() AttachedTargetState {
	state := AttachedTargetState{
		Attached:        false,
		TargetPID:       0,
		ProcessName:     "flowbench-sandbox (Built-in Playground)",
		IngressOrigPort: e.virtualSrvPort,
		EgressOrigIP:    "127.0.0.1",
		EgressOrigPort:  e.virtualBackPort,
		Backends: []BackendEndpoint{
			{
				Name:   "sandbox-backend",
				Host:   "127.0.0.1",
				Port:   e.virtualBackPort,
				Active: true,
			},
		},
	}

	e.mu.Lock()
	oldBackends := e.attachedTarget.Backends
	e.attachedTarget = state
	e.mu.Unlock()

	if e.interceptor != nil {
		for _, b := range oldBackends {
			_ = e.interceptor.RemoveEgressTarget(b.Host, b.Port)
		}
		_ = e.interceptor.UpdateTargetProcess(
			0,
			e.virtualSrvPort,
			"127.0.0.1",
			e.virtualBackPort,
			false,
			false,
			e.virtualSrvPort,
			e.virtualBackPort,
		)
	}
	return state
}

// GetFramingSpec 返回当前生效的二进制帧头 DSL 规范
func (e *Engine) GetFramingSpec() codec.FramingSpec {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.framingSpec
}

// SetFramingSpec 校验并热更新当前协议帧头 DSL 规范
func (e *Engine) SetFramingSpec(spec codec.FramingSpec) (codec.FramingSpec, error) {
	if err := spec.Validate(); err != nil {
		return codec.FramingSpec{}, err
	}
	e.mu.Lock()
	e.framingSpec = spec
	e.mu.Unlock()
	return spec, nil
}

// InjectCustomMock 向引擎注入或覆盖一条指定语义签名的 Mock 资产（支持多后端隔离签名）。
func (e *Engine) InjectCustomMock(semKey, rawReqHex, rawRespHex string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	mockID := fmt.Sprintf("injected-mock-%d", len(e.egressMocks)+1)
	asset := &RecordedMockAsset{
		MockID:      mockID,
		SemanticKey: semKey,
		RawReqHex:   rawReqHex,
		RawRespHex:  rawRespHex,
		HitCount:    0,
	}
	if _, exists := e.egressMocks[semKey]; !exists {
		e.egressMockOrder = append(e.egressMockOrder, semKey)
	}
	e.egressMocks[semKey] = asset
}

// SelectPresetFramingSpec 根据预置模板 ID 切换二进制帧头 DSL 规范
func (e *Engine) SelectPresetFramingSpec(presetID string) (codec.FramingSpec, error) {
	spec, ok := codec.GetPreset(presetID)
	if !ok {
		return codec.FramingSpec{}, fmt.Errorf("unknown framing preset %q", presetID)
	}
	return e.SetFramingSpec(spec)
}

// GetSchemaStatus 返回当前动态 Protobuf Schema 注册表状态
func (e *Engine) GetSchemaStatus() schema.RegistryStatus {
	return e.schemaReg.Status()
}

// ImportProtoSchema 在线导入 .proto 源码与 (OpCode, Direction) 映射表，并原地重激活所有历史捕获帧
func (e *Engine) ImportProtoSchema(filename, protoSource string, bindings []schema.OpCodeRouteBinding) (schema.RegistryStatus, int, error) {
	if len(bindings) == 0 {
		bindings = e.schemaReg.Status().Bindings
		if len(bindings) == 0 {
			bindings = schema.DefaultSandboxBindings()
		}
	}
	if err := e.schemaReg.LoadProtoSource(filename, protoSource, bindings); err != nil {
		return schema.RegistryStatus{}, 0, err
	}
	rehydratedCount := e.RehydrateFrames()
	return e.schemaReg.Status(), rehydratedCount, nil
}

// ClearProtoSchema 清空已导入的 .proto Schema，演示无 Schema (Schema-Less) Wire Tag 树降级模式
func (e *Engine) ClearProtoSchema() (schema.RegistryStatus, int) {
	e.schemaReg.ClearSchema()
	rehydratedCount := e.RehydrateFrames()
	return e.schemaReg.Status(), rehydratedCount
}

// RehydrateFrames 遍历内存中的历史捕获帧、录制用例与 Mock 资产，使用当前 Schema 注册表原地重新解析明文 JSON
func (e *Engine) RehydrateFrames() int {
	e.mu.Lock()
	spec := e.framingSpec
	count := 0

	rehydrateEvent := func(ev *CapturedFrameEvent) {
		rawBytes, err := hex.DecodeString(ev.RawWireHex)
		if err != nil || len(rawBytes) < spec.MinHeaderBytes {
			return
		}
		df, err := codec.ReadDynamicFrame(bytes.NewReader(rawBytes), spec)
		if err != nil {
			// 回退尝试默认 24B 规范
			df, err = codec.ReadDynamicFrame(bytes.NewReader(rawBytes), codec.PresetFlowBenchV1())
			if err != nil {
				return
			}
		}
		dir := inferSchemaDirection(ev.Direction)
		pbMap, opName, schemaDecoded := e.schemaReg.DecodePayload(df.OpCode, dir, df.Payload)
		ev.ProtobufJSON = pbMap
		ev.Header.SchemaDecoded = schemaDecoded
		if opName != "" {
			ev.Header.OpName = opName
		}
		count++
	}

	for i := range e.recentFrames {
		rehydrateEvent(&e.recentFrames[i])
	}
	for i := range e.ingressCases {
		rehydrateEvent(&e.ingressCases[i].Request)
		rehydrateEvent(&e.ingressCases[i].Response)
	}
	for _, k := range e.egressMockOrder {
		if m, ok := e.egressMocks[k]; ok {
			rehydrateEvent(&m.OriginalReq)
			rehydrateEvent(&m.OriginalResp)
		}
	}
	e.mu.Unlock()
	return count
}

// UpdateMockResponseJSON 在线修改某条 Mock 资产的响应 Protobuf JSON，自动通过 dynamicpb 重新编译二进制帧并自愈重算 Checksum
func (e *Engine) UpdateMockResponseJSON(mockID string, updatedJSON map[string]any) (*RecordedMockAsset, error) {
	e.mu.Lock()
	var targetMock *RecordedMockAsset
	for _, m := range e.egressMocks {
		if m.MockID == mockID {
			targetMock = m
			break
		}
	}
	spec := e.framingSpec
	e.mu.Unlock()

	if targetMock == nil {
		return nil, fmt.Errorf("mock_id %q not found", mockID)
	}

	origRaw, err := hex.DecodeString(targetMock.RawRespHex)
	if err != nil {
		return nil, fmt.Errorf("decode original mock hex: %w", err)
	}

	origDF, err := codec.ReadDynamicFrame(bytes.NewReader(origRaw), spec)
	if err != nil {
		spec = codec.PresetFlowBenchV1()
		origDF, err = codec.ReadDynamicFrame(bytes.NewReader(origRaw), spec)
		if err != nil {
			return nil, fmt.Errorf("parse original mock frame: %w", err)
		}
	}

	newPayload, err := e.schemaReg.EncodeJSONToPayload(origDF.OpCode, schema.DirectionResp, updatedJSON)
	if err != nil {
		return nil, fmt.Errorf("encode updated JSON via dynamicpb: %w", err)
	}

	rebuiltDF, err := codec.RebuildDynamicFrame(spec, origDF.HeaderRaw, origDF.SeqID, newPayload)
	if err != nil {
		return nil, fmt.Errorf("rebuild dynamic frame with new checksum: %w", err)
	}

	newRawHex := strings.ToUpper(hex.EncodeToString(codec.EncodeDynamicFrameBytes(rebuiltDF)))
	newInspected := InspectDynamicFrame(rebuiltDF, e.schemaReg, "EGRESS_MOCK_RESP", "REPLAY", targetMock.OriginalResp.SeqID, targetMock.OriginalResp.LatencyMs)

	e.mu.Lock()
	targetMock.RawRespHex = newRawHex
	targetMock.OriginalResp = newInspected
	copied := *targetMock
	e.mu.Unlock()

	_ = e.savePersistedAssets()
	return &copied, nil
}

// SimulateDissectFrame 供 Web 协议工作台按当前 FramingSpec 与 Schema 即时模拟构造或拆解一帧二进制报文
func (e *Engine) SimulateDissectFrame(opCode uint32, seqID uint64, sampleJSON map[string]any) (CapturedFrameEvent, error) {
	spec := e.GetFramingSpec()
	var payload []byte
	var err error
	if len(sampleJSON) > 0 {
		payload, err = e.schemaReg.EncodeJSONToPayload(opCode, schema.DirectionReq, sampleJSON)
		if err != nil {
			// 若当前 OpCode 未绑定 Schema，构造一段标准示例 Protobuf Wire 字节流 (tag 1="ORD-SIM-01", tag 2=100)
			payload = []byte{0x0A, 0x0A, 'O', 'R', 'D', '-', 'S', 'I', 'M', '-', '0', '1', 0x10, 0x64}
		}
	} else {
		payload = []byte{0x0A, 0x0A, 'O', 'R', 'D', '-', 'S', 'I', 'M', '-', '0', '1', 0x10, 0x64}
	}

	df, err := codec.BuildDynamicFrame(spec, opCode, seqID, payload)
	if err != nil {
		return CapturedFrameEvent{}, err
	}
	seq := e.seqCounter.Add(1)
	ev := InspectDynamicFrame(df, e.schemaReg, "INGRESS_REQ", "RECORD", seq, 0.42)

	e.mu.Lock()
	e.recentFrames = append(e.recentFrames, ev)
	if len(e.recentFrames) > 120 {
		e.recentFrames = e.recentFrames[len(e.recentFrames)-120:]
	}
	e.mu.Unlock()

	e.subMu.Lock()
	for ch := range e.subscribers {
		select {
		case ch <- ev:
		default:
		}
	}
	e.subMu.Unlock()

	return ev, nil
}
