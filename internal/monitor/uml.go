package monitor

import (
	"fmt"
	"strings"
)

type ParticipantRole string

const (
	RoleCaller  ParticipantRole = "CALLER"  // 业务调用方 (终端/测试脚本)
	RoleSUT     ParticipantRole = "SUT"     // 被测业务组件 (核心中轴)
	RoleBackend ParticipantRole = "BACKEND" // 下游依赖微服务
)

// UMLParticipant 描述 UML 时序图中的生命线参与者
type UMLParticipant struct {
	ID          string          `json:"id"`           // "caller", "sut", "backend-127.0.0.1:9200"
	DisplayName string          `json:"display_name"` // "业务调用端 (Caller)", "order-server (被测组件 SUT)"
	Role        ParticipantRole `json:"role"`         // "CALLER", "SUT", "BACKEND"
	Endpoint    string          `json:"endpoint"`     // "127.0.0.1:9100", "127.0.0.1:9200"
}

// UMLSequenceStep 描述 UML 时序图中一次横向消息交互步骤
type UMLSequenceStep struct {
	StepIndex   int                `json:"step_index"` // 1-based: 1, 2, 3...
	FromID      string             `json:"from_id"`
	ToID        string             `json:"to_id"`
	FromName    string             `json:"from_name"`
	ToName      string             `json:"to_name"`
	IsReturn    bool               `json:"is_return"` // false 为实线调用请求 ──▶; true 为虚线响应返回 ◀──
	OpCode      uint32             `json:"op_code"`
	OpName      string             `json:"op_name"`
	StatusCode  uint32             `json:"status_code"`
	StatusName  string             `json:"status_name"`
	DurationMs  float64            `json:"duration_ms"` // 该步骤耗时 (ms)
	Timestamp   string             `json:"timestamp"`
	IsMockable  bool               `json:"is_mockable"` // 下游返回支持原地 Mock 注入
	MockAssetID string             `json:"mock_asset_id,omitempty"`
	FrameEvent  CapturedFrameEvent `json:"frame_event"` // 关联的底层捕获帧 (含 Protobuf 载荷与帧头)
}

// UMLDiagramData 描述一笔端到端事务的完整 UML 时序图
type UMLDiagramData struct {
	TraceID         string            `json:"trace_id"`
	RequestID       uint64            `json:"request_id"`
	TotalDurationMs float64           `json:"total_duration_ms"`
	Participants    []UMLParticipant  `json:"participants"`
	Steps           []UMLSequenceStep `json:"steps"`
}

// resolveBackendDisplayName 识别并解析下游微服务友好展示名 (别名优先，IP:Port 兜底)
func resolveBackendDisplayName(targetBackend string, hintName string, registeredBackends []BackendEndpoint) string {
	cleanedTarget := strings.TrimSpace(targetBackend)
	if cleanedTarget == "" {
		cleanedTarget = "127.0.0.1:9200"
	}

	// 1. 优先根据 host:port 在已注册的 backends 列表中精确匹配
	for _, b := range registeredBackends {
		bAddr := fmt.Sprintf("%s:%d", b.Host, b.Port)
		if bAddr == cleanedTarget {
			if strings.TrimSpace(b.Name) != "" {
				return fmt.Sprintf("%s (%s:%d)", strings.TrimSpace(b.Name), b.Host, b.Port)
			}
			return fmt.Sprintf("下游组件 (%s:%d)", b.Host, b.Port)
		}
	}

	// 2. 检查 hintName
	if strings.TrimSpace(hintName) != "" {
		return fmt.Sprintf("%s (%s)", strings.TrimSpace(hintName), cleanedTarget)
	}

	// 3. 兜底未注册的动态下游地址
	return fmt.Sprintf("下游组件 (%s)", cleanedTarget)
}

// BuildUMLDiagram 将一个 TransactionTrace 编排为符合 UML 规范的结构化时序图
func BuildUMLDiagram(tr TransactionTrace, backends []BackendEndpoint, procName string, ingressPort uint16) UMLDiagramData {
	sutName := strings.TrimSpace(procName)
	if sutName == "" {
		sutName = "被测组件"
	}
	if ingressPort == 0 {
		ingressPort = 9100
	}

	pCaller := UMLParticipant{
		ID:          "caller",
		DisplayName: "业务调用端 (Caller)",
		Role:        RoleCaller,
		Endpoint:    "client",
	}

	pSUT := UMLParticipant{
		ID:          "sut",
		DisplayName: fmt.Sprintf("%s (被测组件 SUT)", sutName),
		Role:        RoleSUT,
		Endpoint:    fmt.Sprintf("127.0.0.1:%d", ingressPort),
	}

	participants := []UMLParticipant{pCaller, pSUT}
	backendMap := make(map[string]UMLParticipant)

	// 收集并注册涉及的所有下游组件
	for _, sc := range tr.SubCalls {
		tb := strings.TrimSpace(sc.TargetBackend)
		if tb == "" {
			tb = "127.0.0.1:9200"
		}
		bID := fmt.Sprintf("backend-%s", tb)
		if _, exists := backendMap[bID]; !exists {
			dispName := resolveBackendDisplayName(tb, sc.BackendName, backends)
			pBack := UMLParticipant{
				ID:          bID,
				DisplayName: dispName,
				Role:        RoleBackend,
				Endpoint:    tb,
			}
			backendMap[bID] = pBack
			participants = append(participants, pBack)
		}
	}

	steps := make([]UMLSequenceStep, 0)
	stepIdx := 1

	// 步骤 1: Caller -> SUT (入站请求)
	opName := tr.IngressReq.Header.OpName
	if opName == "" {
		opName = tr.OpName
	}
	steps = append(steps, UMLSequenceStep{
		StepIndex:  stepIdx,
		FromID:     pCaller.ID,
		ToID:       pSUT.ID,
		FromName:   pCaller.DisplayName,
		ToName:     pSUT.DisplayName,
		IsReturn:   false,
		OpCode:     tr.IngressReq.Header.OpCode,
		OpName:     opName,
		StatusCode: 0,
		StatusName: "",
		DurationMs: 0,
		Timestamp:  tr.IngressReq.Timestamp,
		IsMockable: false,
		FrameEvent: tr.IngressReq,
	})
	stepIdx++

	// 步骤 2 ~ 2N+1: 下游子调用
	for _, sc := range tr.SubCalls {
		tb := strings.TrimSpace(sc.TargetBackend)
		if tb == "" {
			tb = "127.0.0.1:9200"
		}
		bID := fmt.Sprintf("backend-%s", tb)
		pBack, ok := backendMap[bID]
		if !ok {
			pBack = UMLParticipant{
				ID:          bID,
				DisplayName: resolveBackendDisplayName(tb, sc.BackendName, backends),
				Role:        RoleBackend,
				Endpoint:    tb,
			}
		}

		// 子请求: SUT -> Backend
		reqOp := sc.EgressReq.Header.OpName
		if reqOp == "" {
			reqOp = sc.OpName
		}
		steps = append(steps, UMLSequenceStep{
			StepIndex:  stepIdx,
			FromID:     pSUT.ID,
			ToID:       pBack.ID,
			FromName:   pSUT.DisplayName,
			ToName:     pBack.DisplayName,
			IsReturn:   false,
			OpCode:     sc.EgressReq.Header.OpCode,
			OpName:     reqOp,
			Timestamp:  sc.EgressReq.Timestamp,
			IsMockable: false,
			FrameEvent: sc.EgressReq,
		})
		stepIdx++

		// 子响应: Backend -> SUT (若已收到响应)
		if sc.EgressResp != nil {
			respOp := sc.EgressResp.Header.OpName
			if respOp == "" {
				respOp = sc.OpName
			}
			steps = append(steps, UMLSequenceStep{
				StepIndex:   stepIdx,
				FromID:      pBack.ID,
				ToID:        pSUT.ID,
				FromName:    pBack.DisplayName,
				ToName:      pSUT.DisplayName,
				IsReturn:    true,
				OpCode:      sc.EgressResp.Header.OpCode,
				OpName:      respOp,
				DurationMs:  sc.DurationMs,
				Timestamp:   sc.EgressResp.Timestamp,
				IsMockable:  true,
				MockAssetID: fmt.Sprintf("session-mock-%s", sc.SubCallID),
				FrameEvent:  *sc.EgressResp,
			})
			stepIdx++
		}
	}

	// 最终响应: SUT -> Caller (若已完成入站闭环)
	if tr.IngressResp != nil {
		respOp := tr.IngressResp.Header.OpName
		if respOp == "" {
			respOp = tr.OpName
		}
		steps = append(steps, UMLSequenceStep{
			StepIndex:  stepIdx,
			FromID:     pSUT.ID,
			ToID:       pCaller.ID,
			FromName:   pSUT.DisplayName,
			ToName:     pCaller.DisplayName,
			IsReturn:   true,
			OpCode:     tr.IngressResp.Header.OpCode,
			OpName:     respOp,
			StatusCode: tr.StatusCode,
			StatusName: tr.StatusName,
			DurationMs: tr.DurationMs,
			Timestamp:  tr.IngressResp.Timestamp,
			IsMockable: false,
			FrameEvent: *tr.IngressResp,
		})
	}

	return UMLDiagramData{
		TraceID:         tr.TraceID,
		RequestID:       tr.RequestID,
		TotalDurationMs: tr.DurationMs,
		Participants:    participants,
		Steps:           steps,
	}
}
